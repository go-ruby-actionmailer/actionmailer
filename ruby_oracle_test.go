// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// oracleScript drives the real Rails Action Mailer (>= 7) to emit m.encoded for
// a named scenario, so the go composition can be diffed against the gem's
// Mail::Message#to_s. Kept byte-for-byte in step with the go builders in
// oracleGo below.
const oracleScript = `
gem 'actionmailer', '7.1.5.1'
require 'action_mailer'
ActionMailer::Base.raise_delivery_errors = false
ActionMailer::Base.delivery_method = :test

class OracleMailer < ActionMailer::Base
  default from: 'notifications@example.com'

  def single
    mail(to: 'ada@example.com', subject: 'Hi', body: 'just text', content_type: 'text/plain')
  end

  def alternative
    mail(to: 'ada@example.com', cc: 'c@example.com', subject: 'Welcome, Ada') do |f|
      f.text { render plain: 'Hello text' }
      f.html { render html: '<p>Hello html</p>'.html_safe }
    end
  end

  def with_attachment
    attachments['terms.pdf'] = { mime_type: 'application/pdf', content: 'PDFDATA' }
    mail(to: 'ada@example.com', subject: 'Docs') do |f|
      f.text { render plain: 'see attached' }
    end
  end

  def with_inline
    attachments.inline['logo.png'] = { mime_type: 'image/png', content: 'PNGDATA' }
    mail(to: 'ada@example.com', subject: 'Logo') do |f|
      f.html { render html: '<p>hi</p>'.html_safe }
    end
  end

  def full
    attachments['terms.pdf'] = { mime_type: 'application/pdf', content: 'PDFDATA' }
    attachments.inline['logo.png'] = { mime_type: 'image/png', content: 'PNGDATA' }
    mail(to: 'ada@example.com', cc: 'c@example.com', subject: 'Welcome, Ada') do |f|
      f.text { render plain: 'Hello text' }
      f.html { render html: '<p>Hello html</p>'.html_safe }
    end
  end

  def nonascii
    mail(to: 'ada@example.com', subject: 'Grüße €', body: "Cafés naïve €\n", content_type: 'text/plain')
  end
end

STDOUT.write OracleMailer.public_send(ARGV[0]).encoded
`

// oracleGo builds the go-side message for a scenario, matching oracleScript.
func oracleGo(t *testing.T, scenario string) string {
	t.Helper()
	// Deterministic composition; the Message-ID is left unset (the gem's random
	// one is normalised out), Date is fixed and normalised out too.
	oldB, oldC := GenerateBoundary, GenerateContentID
	GenerateBoundary = func(seq int) string { return fmt.Sprintf("B%d", seq) }
	GenerateContentID = func(name string) string { return "<cid-" + name + ">" }
	t.Cleanup(func() { GenerateBoundary, GenerateContentID = oldB, oldC })

	b := New("OracleMailer")
	b.Now = func() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }
	b.Default("from", "notifications@example.com")
	b.RenderBody = func(mailer, action, format string, locals map[string]any) (string, error) {
		switch action {
		case "alternative", "full":
			switch format {
			case "text":
				return "Hello text", nil
			case "html":
				return "<p>Hello html</p>", nil
			}
		case "with_attachment":
			if format == "text" {
				return "see attached", nil
			}
		case "with_inline":
			if format == "html" {
				return "<p>hi</p>", nil
			}
		}
		return "", ErrNoTemplate
	}
	b.Register("single", func(m *Mailer, _ ...any) error {
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Subject: "Hi", Body: "just text", ContentType: "text/plain"})
	})
	b.Register("alternative", func(m *Mailer, _ ...any) error {
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Cc: []string{"c@example.com"}, Subject: "Welcome, Ada"})
	})
	b.Register("with_attachment", func(m *Mailer, _ ...any) error {
		m.Attachments().Set("terms.pdf", []byte("PDFDATA"))
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Subject: "Docs"})
	})
	b.Register("with_inline", func(m *Mailer, _ ...any) error {
		m.Attachments().SetInline("logo.png", []byte("PNGDATA"))
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Subject: "Logo"})
	})
	b.Register("full", func(m *Mailer, _ ...any) error {
		m.Attachments().Set("terms.pdf", []byte("PDFDATA"))
		m.Attachments().SetInline("logo.png", []byte("PNGDATA"))
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Cc: []string{"c@example.com"}, Subject: "Welcome, Ada"})
	})
	b.Register("nonascii", func(m *Mailer, _ ...any) error {
		return m.Mail(MailOptions{To: []string{"ada@example.com"}, Subject: "Grüße €", Body: "Cafés naïve €\n", ContentType: "text/plain"})
	})

	msg, err := b.Process(scenario).Message()
	if err != nil {
		t.Fatalf("go compose %s: %v", scenario, err)
	}
	return msg.Encoded()
}

// TestRubyOracleByteParity differentially compares the go composition against
// the real Rails Action Mailer 7.1.5's Mail::Message#encoded for a range of
// scenarios. It is skip-gated when Ruby or the actionmailer gem is unavailable
// (so it never runs on a Ruby-less CI).
//
// The comparison normalises away (a) the intrinsically non-deterministic
// Date / Message-ID / inline Content-ID, and (b) three go-ruby-mail-vs-mail-gem
// encoder-policy details that carry no semantic weight: the gem folds structured
// parameters onto their own continuation lines, quotes boundary tokens, and
// emits an empty MIME preamble line before the first boundary. Everything that
// Action Mailer *composes* — the MIME tree shape, the header set and its
// canonical order, Content-Type parameter order, the transfer-encoding choice,
// the RFC 2047 subject encoding, and the attachment layout/filenames — is
// asserted to match byte-for-byte.
func TestRubyOracleByteParity(t *testing.T) {
	script := ensureRubyOracle(t)
	for _, scenario := range []string{"single", "alternative", "with_attachment", "with_inline", "full", "nonascii"} {
		t.Run(scenario, func(t *testing.T) {
			cmd := exec.Command("ruby", script, scenario)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ruby oracle %s: %v\n%s", scenario, err, out)
			}
			gem := normalizeMIME(string(out))
			got := normalizeMIME(oracleGo(t, scenario))
			if got != gem {
				t.Fatalf("scenario %s mismatch\n--- go ---\n%s\n--- gem ---\n%s", scenario, got, gem)
			}
		})
	}
}

// ensureRubyOracle writes the oracle script to a temp file and verifies Ruby and
// the pinned actionmailer gem are available, skipping the test otherwise.
func ensureRubyOracle(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby not installed; skipping gem oracle")
	}
	probe := exec.Command("ruby", "-e", "gem 'actionmailer','7.1.5.1'; require 'action_mailer'")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("actionmailer 7.1.5.1 gem unavailable; skipping gem oracle: %s", out)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "oracle.rb")
	if err := os.WriteFile(script, []byte(oracleScript), 0o644); err != nil {
		t.Fatal(err)
	}
	return script
}

var (
	boundaryDecl = regexp.MustCompile(`boundary="?([^";\s]+)"?`)
	dequoteBND   = regexp.MustCompile(`"(BND\d+)"`)
)

// normalizeMIME canonicalises an encoded message for differential comparison:
// it unfolds continuation lines, rewrites boundary tokens to stable BNDn
// placeholders (dropping the gem's quoting), removes the empty preamble blank
// line that the gem emits before each boundary, and neutralises the
// non-deterministic Date / Message-ID / Content-ID.
func normalizeMIME(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")

	// Unfold: a line starting with WSP continues the previous one.
	var unfolded []string
	for _, ln := range strings.Split(s, "\n") {
		if len(ln) > 0 && (ln[0] == ' ' || ln[0] == '\t') && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += ln
			continue
		}
		unfolded = append(unfolded, ln)
	}
	text := canonicalizeBoundaries(strings.Join(unfolded, "\n"))

	var out []string
	for _, ln := range strings.Split(text, "\n") {
		lower := strings.ToLower(ln)
		switch {
		case strings.HasPrefix(lower, "date:"), strings.HasPrefix(lower, "message-id:"):
			continue
		case strings.HasPrefix(lower, "content-id:"):
			ln = "Content-ID: <CID>"
		}
		// Strip every blank line that precedes a boundary delimiter — symmetric
		// across both sides — so the gem's empty MIME preamble and its trailing
		// part whitespace do not skew the diff.
		if strings.HasPrefix(ln, "--") {
			for len(out) > 0 && out[len(out)-1] == "" {
				out = out[:len(out)-1]
			}
		}
		out = append(out, ln)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// canonicalizeBoundaries replaces each distinct boundary token (in order of
// first appearance) with a stable BNDn placeholder in both the Content-Type
// declarations and the --boundary delimiter lines, then strips the gem's quotes
// around them.
func canonicalizeBoundaries(text string) string {
	seen := map[string]string{}
	var vals []string
	for _, m := range boundaryDecl.FindAllStringSubmatch(text, -1) {
		if _, ok := seen[m[1]]; !ok {
			seen[m[1]] = fmt.Sprintf("BND%d", len(vals))
			vals = append(vals, m[1])
		}
	}
	// Replace longer tokens first so a short token never matches inside a longer
	// one.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		text = strings.ReplaceAll(text, v, seen[v])
	}
	return dequoteBND.ReplaceAllString(text, "$1")
}
