// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-ruby-mail/mail"
	"net/smtp"
)

// fixedNow is a deterministic Date source.
func fixedNow() time.Time { return time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC) }

// renderStub returns a RenderBody that serves the given per-format bodies and
// reports ErrNoTemplate for any format not present.
func renderStub(bodies map[string]string) RenderBody {
	return func(mailer, action, format string, locals map[string]any) (string, error) {
		if b, ok := bodies[format]; ok {
			return b, nil
		}
		return "", ErrNoTemplate
	}
}

// newTestBase builds a Base wired to test delivery with a deterministic clock.
func newTestBase(t *testing.T) *Base {
	t.Helper()
	b := New("UserMailer")
	b.Now = fixedNow
	b.UseTestDelivery()
	return b
}

func TestNewDefaults(t *testing.T) {
	b := New("UserMailer")
	if b.Name != "UserMailer" || !b.PerformDeliveries || !b.RaiseDeliveryErrors {
		t.Fatalf("unexpected defaults: %+v", b)
	}
	if b.Now == nil || b.actions == nil || b.Defaults == nil {
		t.Fatal("New did not initialise maps/clock")
	}
}

func TestMultipartAlternativeWithAttachment(t *testing.T) {
	b := newTestBase(t)
	b.RenderBody = renderStub(map[string]string{
		"text": "Hello text",
		"html": "<p>Hello html</p>",
	})
	b.MessageIDGen = func() string { return "<abc@example.com>" }
	b.Default("from", "notifications@example.com")
	b.Default("X-Auto", "yes")

	b.Register("welcome", func(m *Mailer, params ...any) error {
		name := params[0].(string)
		at := m.Attachments().Set("terms.pdf", []byte("PDFDATA"))
		if at.ContentType != "application/pdf" {
			t.Fatalf("guessed type = %q", at.ContentType)
		}
		m.Attachments().SetInline("logo.png", []byte("PNGDATA"))
		m.Headers(map[string]string{"X-Campaign": "spring"})
		return m.Mail(MailOptions{
			To:      []string{"a@example.com", "b@example.com"},
			Cc:      []string{"c@example.com"},
			Bcc:     []string{"d@example.com"},
			ReplyTo: []string{"reply@example.com"},
			Subject: "Welcome, " + name,
			Locals:  map[string]any{"name": name},
		})
	})

	d := b.Process("welcome", "Ada")
	msg, err := d.Message()
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	if !strings.HasPrefix(msg.ContentType(), "multipart/mixed") {
		t.Fatalf("top content-type = %q", msg.ContentType())
	}
	// mixed = [ related, terms.pdf ]
	parts := msg.Parts()
	if len(parts) != 2 {
		t.Fatalf("mixed parts = %d", len(parts))
	}
	if !strings.HasPrefix(parts[0].ContentType(), "multipart/related") {
		t.Fatalf("related content-type = %q", parts[0].ContentType())
	}
	if parts[1].Filename() != "terms.pdf" || !parts[1].IsAttachment() {
		t.Fatalf("attachment part = %q inline=%v", parts[1].Filename(), parts[1].IsAttachment())
	}
	// related = [ alternative, logo.png(inline) ]
	rel := parts[0].Parts()
	if len(rel) != 2 || !strings.HasPrefix(rel[0].ContentType(), "multipart/alternative") {
		t.Fatalf("related layout: %d %q", len(rel), rel[0].ContentType())
	}
	if rel[1].ContentID() != "logo.png" { // accessor strips angle brackets
		t.Fatalf("inline content-id = %q", rel[1].ContentID())
	}
	// alternative = [ text/plain, text/html ]
	alt := rel[0].Parts()
	if len(alt) != 2 || alt[0].MimeType() != "text/plain" || alt[1].MimeType() != "text/html" {
		t.Fatalf("alternative layout: %v", alt)
	}
	// envelope headers
	if got := msg.To(); len(got) != 2 {
		t.Fatalf("To = %v", got)
	}
	if msg.Subject() != "Welcome, Ada" {
		t.Fatalf("subject = %q", msg.Subject())
	}
	if msg.Field("X-Auto") != "yes" || msg.Field("X-Campaign") != "spring" {
		t.Fatalf("custom headers missing: %q %q", msg.Field("X-Auto"), msg.Field("X-Campaign"))
	}
	if msg.MessageID() != "abc@example.com" {
		t.Fatalf("message-id = %q", msg.MessageID())
	}
	// base64 attachment round-trips
	if string(parts[1].Decoded()) != "PDFDATA" {
		t.Fatalf("attachment decode = %q", parts[1].Decoded())
	}
	// full round-trip through the wire
	if reparsed := mail.New(msg.Encoded()); !reparsed.Multipart() {
		t.Fatal("re-parsed message is not multipart")
	}
	if len(b.Deliveries) != 0 {
		t.Fatal("delivery happened during compose")
	}
}

func TestSinglePartExplicitBodyAndDefaults(t *testing.T) {
	b := newTestBase(t)
	b.Default("from", "default@example.com")
	b.Default("reply_to", "r@example.com")
	b.Default("to", "fallback@example.com")
	b.Default("cc", "cc@example.com")
	b.Default("bcc", "bcc@example.com")
	b.Default("subject", "Default Subject")

	b.Register("plain", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{Body: "just text"})
	})
	msg, err := b.Process("plain").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.MimeType() != "text/plain" || msg.Multipart() {
		t.Fatalf("expected single text/plain, got %q multipart=%v", msg.ContentType(), msg.Multipart())
	}
	if msg.From()[0] != "default@example.com" || msg.To()[0] != "fallback@example.com" {
		t.Fatalf("defaults not applied: from=%v to=%v", msg.From(), msg.To())
	}
	if msg.Cc()[0] != "cc@example.com" || msg.Bcc()[0] != "bcc@example.com" {
		t.Fatalf("cc/bcc defaults: %v %v", msg.Cc(), msg.Bcc())
	}
	if msg.ReplyTo()[0] != "r@example.com" || msg.Subject() != "Default Subject" {
		t.Fatalf("reply/subject defaults: %v %q", msg.ReplyTo(), msg.Subject())
	}
	if _, ok := msg.Date(); !ok {
		t.Fatal("Date not set from Now seam")
	}
}

func TestExplicitHTMLBodyContentType(t *testing.T) {
	b := newTestBase(t)
	b.Default("content_type", "text/html")
	b.Register("h", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{Body: "<b>hi</b>", From: "x@example.com", To: []string{"y@example.com"}})
	})
	msg, err := b.Process("h").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.MimeType() != "text/html" {
		t.Fatalf("content type = %q", msg.ContentType())
	}
}

func TestTextOnlyRender(t *testing.T) {
	b := newTestBase(t)
	b.Now = nil // exercise the no-Date branch
	b.RenderBody = renderStub(map[string]string{"text": "only text"})
	b.Register("t", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	msg, err := b.Process("t").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.MimeType() != "text/plain" || msg.Multipart() {
		t.Fatalf("expected single text part, got %q", msg.ContentType())
	}
	if _, ok := msg.Date(); ok {
		t.Fatal("Date should be absent when Now is nil")
	}
}

func TestExplicitFormatsAndDate(t *testing.T) {
	b := newTestBase(t)
	b.RenderBody = renderStub(map[string]string{"html": "<i>x</i>"})
	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	b.Register("f", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{
			Formats: []string{"html"},
			HasDate: true,
			Date:    when,
			To:      []string{"y@example.com"},
		})
	})
	msg, err := b.Process("f").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.MimeType() != "text/html" {
		t.Fatalf("expected html single part, got %q", msg.ContentType())
	}
	if got, ok := msg.Date(); !ok || !got.Equal(when) {
		t.Fatalf("date = %v ok=%v", got, ok)
	}
}

func TestEmptyBodyWhenNoTemplates(t *testing.T) {
	b := newTestBase(t)
	b.RenderBody = renderStub(map[string]string{}) // every format ErrNoTemplate
	b.Register("e", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	msg, err := b.Process("e").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.MimeType() != "text/plain" || msg.Multipart() {
		t.Fatalf("expected empty single part, got %q", msg.ContentType())
	}
}

func TestRenderErrorPropagates(t *testing.T) {
	b := newTestBase(t)
	boom := errors.New("boom")
	b.RenderBody = func(mailer, action, format string, locals map[string]any) (string, error) {
		return "", boom
	}
	b.Register("bad", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	if err := b.Process("bad").DeliverNow(); !errors.Is(err, boom) {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestMissingRenderSeam(t *testing.T) {
	b := newTestBase(t)
	b.Register("nr", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	if _, err := b.Process("nr").Message(); !errors.Is(err, ErrNoRenderBody) {
		t.Fatalf("expected ErrNoRenderBody, got %v", err)
	}
}

func TestUnknownAction(t *testing.T) {
	b := newTestBase(t)
	if err := b.Process("nope").DeliverNow(); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got %v", err)
	}
}

func TestActionError(t *testing.T) {
	b := newTestBase(t)
	oops := errors.New("oops")
	b.Register("er", func(m *Mailer, params ...any) error { return oops })
	if err := b.Process("er").DeliverLater(); !errors.Is(err, oops) {
		t.Fatalf("expected oops, got %v", err)
	}
}

func TestActionWithoutMail(t *testing.T) {
	b := newTestBase(t)
	b.Register("nomail", func(m *Mailer, params ...any) error { return nil })
	if _, err := b.Process("nomail").Message(); err == nil || !strings.Contains(err.Error(), "did not call Mail") {
		t.Fatalf("expected did-not-call-Mail error, got %v", err)
	}
}

func TestHeaderPrecedence(t *testing.T) {
	b := newTestBase(t)
	b.Default("X-Src", "default")
	b.RenderBody = renderStub(map[string]string{"text": "x"})
	b.Register("p", func(m *Mailer, params ...any) error {
		m.Headers(map[string]string{"X-Src": "permsg", "X-Only": "perm"})
		return m.Mail(MailOptions{
			To:      []string{"y@example.com"},
			Headers: map[string]string{"X-Src": "opts"},
		})
	})
	msg, err := b.Process("p").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Field("X-Src") != "opts" {
		t.Fatalf("X-Src precedence = %q (want opts)", msg.Field("X-Src"))
	}
	if msg.Field("X-Only") != "perm" {
		t.Fatalf("X-Only = %q", msg.Field("X-Only"))
	}
}

// recorder records interceptor/observer invocations.
type recorder struct{ intercepted, observed int }

func (r *recorder) DeliveringEmail(m *mail.Message) { r.intercepted++ }
func (r *recorder) DeliveredEmail(m *mail.Message)  { r.observed++ }

func TestDeliverNowInterceptorsObservers(t *testing.T) {
	b := newTestBase(t)
	b.RenderBody = renderStub(map[string]string{"text": "hi"})
	rec := &recorder{}
	b.RegisterInterceptor(rec)
	b.RegisterObserver(rec)
	b.Register("w", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}, From: "x@example.com"})
	})
	if err := b.Process("w").DeliverNow(); err != nil {
		t.Fatal(err)
	}
	if rec.intercepted != 1 || rec.observed != 1 || len(b.Deliveries) != 1 {
		t.Fatalf("hooks/deliveries: %d %d %d", rec.intercepted, rec.observed, len(b.Deliveries))
	}
}

func TestPerformDeliveriesFalseStillRunsHooks(t *testing.T) {
	b := newTestBase(t)
	b.PerformDeliveries = false
	b.RenderBody = renderStub(map[string]string{"text": "hi"})
	rec := &recorder{}
	b.RegisterInterceptor(rec)
	b.RegisterObserver(rec)
	b.Register("w", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	if err := b.Process("w").DeliverNow(); err != nil {
		t.Fatal(err)
	}
	if rec.intercepted != 1 || rec.observed != 1 || len(b.Deliveries) != 0 {
		t.Fatalf("expected hooks but no delivery: %d %d %d", rec.intercepted, rec.observed, len(b.Deliveries))
	}
}

func TestNoDeliveryMethodConfigured(t *testing.T) {
	b := New("UserMailer")
	b.Now = fixedNow
	b.RenderBody = renderStub(map[string]string{"text": "hi"})
	b.Register("w", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})
	if err := b.Process("w").DeliverNow(); !errors.Is(err, ErrNoDeliveryMethod) {
		t.Fatalf("expected ErrNoDeliveryMethod, got %v", err)
	}
}

// failMethod always errors on Deliver.
type failMethod struct{ err error }

func (f failMethod) Deliver(m *mail.Message) error { return f.err }

func TestRaiseDeliveryErrors(t *testing.T) {
	sent := errors.New("smtp down")
	b := New("UserMailer")
	b.Now = fixedNow
	b.RenderBody = renderStub(map[string]string{"text": "hi"})
	b.DeliveryMethod = failMethod{err: sent}
	b.Register("w", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})

	// Raise on (default true).
	if err := b.Process("w").DeliverNow(); !errors.Is(err, sent) {
		t.Fatalf("expected raised error, got %v", err)
	}
	// Swallow when disabled.
	b.RaiseDeliveryErrors = false
	if err := b.Process("w").DeliverNow(); err != nil {
		t.Fatalf("expected swallowed error, got %v", err)
	}
}

func TestDeliverLaterInlineAndSeam(t *testing.T) {
	b := newTestBase(t)
	b.RenderBody = renderStub(map[string]string{"text": "hi"})
	b.Register("w", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}})
	})

	// Inline (no seam).
	if err := b.Process("w").DeliverLater(); err != nil {
		t.Fatal(err)
	}
	if len(b.Deliveries) != 1 {
		t.Fatalf("inline deliver_later did not deliver: %d", len(b.Deliveries))
	}

	// Via seam.
	var queued func() error
	b.EnqueueJob = func(job func() error) error { queued = job; return nil }
	if err := b.Process("w").DeliverLater(); err != nil {
		t.Fatal(err)
	}
	if len(b.Deliveries) != 1 {
		t.Fatal("seam should defer delivery")
	}
	if err := queued(); err != nil {
		t.Fatal(err)
	}
	if len(b.Deliveries) != 2 {
		t.Fatalf("running the job should deliver: %d", len(b.Deliveries))
	}
}

func TestDeliverLaterCaptureError(t *testing.T) {
	b := newTestBase(t)
	b.Register("er", func(m *Mailer, params ...any) error { return errors.New("x") })
	if err := b.Process("er").DeliverLater(); err == nil {
		t.Fatal("expected captured compose error")
	}
}

func TestFileDelivery(t *testing.T) {
	dir := t.TempDir()
	fd := NewFileDelivery(filepath.Join(dir, "mails"))
	m := mail.New("").SetTo("a@example.com, b@x.com").SetBody("hi")
	if err := fd.Deliver(m); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a@example.com", "b@x.com"} {
		if _, err := os.Stat(filepath.Join(dir, "mails", name)); err != nil {
			t.Fatalf("missing file %s: %v", name, err)
		}
	}

	// No recipients: nothing written, no error.
	if err := fd.Deliver(mail.New("").SetBody("hi")); err != nil {
		t.Fatal(err)
	}

	// mkdir error.
	fdErr := NewFileDelivery("x")
	mkErr := errors.New("mkdir failed")
	fdErr.mkdirAll = func(string, os.FileMode) error { return mkErr }
	if err := fdErr.Deliver(m); !errors.Is(err, mkErr) {
		t.Fatalf("expected mkdir error, got %v", err)
	}

	// write error.
	fdW := NewFileDelivery("x")
	wErr := errors.New("write failed")
	fdW.mkdirAll = func(string, os.FileMode) error { return nil }
	fdW.writeFile = func(string, []byte, os.FileMode) error { return wErr }
	if err := fdW.Deliver(m); !errors.Is(err, wErr) {
		t.Fatalf("expected write error, got %v", err)
	}
}

func TestTestDeliveryAppends(t *testing.T) {
	var sink []*mail.Message
	td := NewTestDelivery(&sink)
	m := mail.New("").SetBody("x")
	if err := td.Deliver(m); err != nil {
		t.Fatal(err)
	}
	if len(sink) != 1 || sink[0] != m {
		t.Fatal("test delivery did not append")
	}
}

func TestSMTPDelivery(t *testing.T) {
	m := mail.New("").
		SetFrom("from@example.com").
		SetTo("to@example.com").
		SetCc("cc@example.com").
		SetBcc("bcc@example.com").
		SetBody("hi")

	// With auth + capture args.
	var gotAddr, gotFrom string
	var gotTo []string
	var gotAuth smtp.Auth
	sd := NewSMTPDelivery("mail.example.com:587")
	sd.Username = "user"
	sd.Password = "pass"
	sd.Send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		gotAddr, gotAuth, gotFrom, gotTo = addr, a, from, to
		return nil
	}
	if err := sd.Deliver(m); err != nil {
		t.Fatal(err)
	}
	if gotAddr != "mail.example.com:587" || gotFrom != "from@example.com" {
		t.Fatalf("addr/from = %q %q", gotAddr, gotFrom)
	}
	if gotAuth == nil {
		t.Fatal("expected PLAIN auth")
	}
	if len(gotTo) != 3 {
		t.Fatalf("recipients = %v", gotTo)
	}

	// No auth, explicit Host, send error, no From.
	sendErr := errors.New("send failed")
	sd2 := &SMTPDelivery{
		Addr: "mail.example.com:587",
		Host: "override.example.com",
		Send: func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
			if a != nil {
				t.Fatal("expected no auth")
			}
			if from != "" {
				t.Fatalf("expected empty from, got %q", from)
			}
			return sendErr
		},
	}
	noFrom := mail.New("").SetTo("to@example.com").SetBody("hi")
	if err := sd2.Deliver(noFrom); !errors.Is(err, sendErr) {
		t.Fatalf("expected send error, got %v", err)
	}
}

func TestSMTPHostDerivation(t *testing.T) {
	if h := (&SMTPDelivery{Addr: "host:25"}).host(); h != "host" {
		t.Fatalf("host = %q", h)
	}
	if h := (&SMTPDelivery{Addr: "hostonly"}).host(); h != "hostonly" {
		t.Fatalf("host = %q", h)
	}
	if h := (&SMTPDelivery{Addr: "a:1", Host: "explicit"}).host(); h != "explicit" {
		t.Fatalf("host = %q", h)
	}
}

func TestAttachmentsCollection(t *testing.T) {
	a := newAttachments()
	a.Set("a.txt", []byte("1"))
	if got := a.Get("a.txt"); got == nil || got.ContentType != "text/plain" {
		t.Fatalf("get a.txt = %v", got)
	}
	if a.Get("missing") != nil {
		t.Fatal("missing should be nil")
	}
	// Replace in place keeps order length stable.
	a.Set("a.txt", []byte("2"))
	if len(a.All()) != 1 || string(a.Get("a.txt").Data) != "2" {
		t.Fatalf("replace failed: %v", a.All())
	}
	a.SetInline("i.gif", []byte("g"))
	if len(a.Inline()) != 1 || len(a.Regular()) != 1 {
		t.Fatalf("inline/regular split: %d %d", len(a.Inline()), len(a.Regular()))
	}
}

func TestGuessType(t *testing.T) {
	cases := map[string]string{
		"x.png":     "image/png",
		"x.jpeg":    "image/jpeg",
		"x.unknown": "application/octet-stream",
		"noext":     "application/octet-stream",
	}
	for name, want := range cases {
		if got := guessType(name); got != want {
			t.Fatalf("guessType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAttachmentPartDefaultContentType(t *testing.T) {
	// Empty ContentType falls back to octet-stream in the part.
	p := attachmentPart(&Attachment{Name: "f", Data: []byte("d")})
	if !strings.Contains(p.ContentType(), "application/octet-stream") {
		t.Fatalf("content type = %q", p.ContentType())
	}
}

func TestMergedHeadersHelper(t *testing.T) {
	out := mergedHeaders(
		map[string]string{"A": "d", "B": "d"},
		map[string]string{"A": "m", "C": "m"},
		map[string]string{"A": "o"},
	)
	if out["A"] != "o" || out["B"] != "d" || out["C"] != "m" {
		t.Fatalf("merged = %v", out)
	}
}

func TestSanitizeRecipient(t *testing.T) {
	if got := sanitizeRecipient("a/b\\c@x"); got != "a_b_c@x" {
		t.Fatalf("sanitize = %q", got)
	}
}
