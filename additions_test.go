// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-ruby-actionview/actionview"
	"github.com/go-ruby-mail/mail"
)

// --- transfer-encoding negotiation --------------------------------------------

func TestNegotiateTextEncoding(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"", "7bit"},                                 // empty is 7bit-safe
		{"plain ascii text", "7bit"},                 // pure ASCII
		{"Cafés naïve €\n", "base64"},                // heavy non-ASCII -> base64 cheaper
		{"aaaaaaaaaaaaaaaaaaaé", "quoted-printable"}, // sparse non-ASCII -> QP
	}
	for _, c := range cases {
		if got := negotiateTextEncoding(c.body); got != c.want {
			t.Errorf("negotiateTextEncoding(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestIs7bitSafe(t *testing.T) {
	if is7bitSafe("a\x00b") {
		t.Fatal("NUL must not be 7bit-safe")
	}
	if is7bitSafe("\x80") {
		t.Fatal("high byte must not be 7bit-safe")
	}
	if !is7bitSafe("hello\tworld\r\n") {
		t.Fatal("tab/CRLF ASCII should be 7bit-safe")
	}
}

func TestQpCostEmpty(t *testing.T) {
	if c := qpCost(""); c != 0 {
		t.Fatalf("qpCost(\"\") = %v, want 0", c)
	}
	// '=' (0x3D) is not in the safe set, so it costs 3.
	if c := qpCost("="); c != 3 {
		t.Fatalf("qpCost(\"=\") = %v, want 3", c)
	}
}

// --- header field ordering ----------------------------------------------------

func TestSortHeadersCanonicalOrder(t *testing.T) {
	m := mail.New("")
	m.SetHeader("Content-Type", "text/plain")
	m.SetHeader("Subject", "s")
	m.SetHeader("X-Custom", "x") // unknown -> sorts last
	m.SetHeader("From", "a@x")
	m.SetHeader("Date", "d")
	sortHeaders(m)
	var names []string
	for _, f := range m.Header().Fields() {
		names = append(names, f.Name)
	}
	want := []string{"Date", "From", "Subject", "Content-Type", "X-Custom"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestSortHeadersRecursesParts(t *testing.T) {
	parent := mail.New("")
	parent.SetContentTypeParams("multipart/mixed", []string{"boundary"}, map[string]string{"boundary": "b"})
	part := mail.New("")
	part.SetHeader("Content-Disposition", "attachment")
	part.SetHeader("Content-Type", "application/pdf")
	parent.AddPart(part)
	sortHeaders(parent)
	if part.Header().Fields()[0].Name != "Content-Type" {
		t.Fatalf("part not sorted: %v", part.Header().Fields()[0].Name)
	}
}

func TestFieldOrderIDUnknown(t *testing.T) {
	if fieldOrderID("X-Nope") != 100 {
		t.Fatal("unknown field should rank 100")
	}
	if fieldOrderID("Date") != fieldOrderID("date") {
		t.Fatal("field order must be case-insensitive")
	}
}

// --- i18n subject -------------------------------------------------------------

func TestI18nSubjectLookupAndInterpolation(t *testing.T) {
	b := New("UserMailer")
	b.I18n = NewI18n("").Set("user_mailer.welcome.subject", "Welcome, %{name}!")
	got := b.i18nSubject("welcome", map[string]any{"name": "Ada"})
	if got != "Welcome, Ada!" {
		t.Fatalf("subject = %q", got)
	}
}

func TestI18nSubjectMissingFallsBackToHumanize(t *testing.T) {
	b := New("UserMailer")
	b.I18n = NewI18n("en")
	if got := b.i18nSubject("order_shipped", nil); got != "Order shipped" {
		t.Fatalf("humanized = %q", got)
	}
}

func TestI18nSubjectNoStore(t *testing.T) {
	if got := New("M").i18nSubject("welcome", nil); got != "" {
		t.Fatalf("no store subject = %q", got)
	}
}

func TestI18nSubjectLocaleFallbackAndUnknownVar(t *testing.T) {
	i := NewI18n("fr")
	i.Set("user_mailer.welcome.subject", "Bonjour %{name} %{missing}")
	s, ok := i.subject("user_mailer", "welcome", map[string]any{"name": "Ada"})
	if !ok || s != "Bonjour Ada %{missing}" {
		t.Fatalf("interpolation = %q ok=%v", s, ok)
	}
	if _, ok := i.subject("user_mailer", "nope", nil); ok {
		t.Fatal("unknown action should miss")
	}
}

func TestI18nInterpolateNoPlaceholders(t *testing.T) {
	if got := interpolate("plain", map[string]any{"x": 1}); got != "plain" {
		t.Fatalf("no-placeholder = %q", got)
	}
	// A dangling "%{" with no closing brace is left verbatim.
	if got := interpolate("a %{b", nil); got != "a %{b" {
		t.Fatalf("dangling = %q", got)
	}
}

func TestI18nZeroValueLocaleDefaults(t *testing.T) {
	// A directly-constructed store (empty Locale) defaults to "en".
	i := &I18n{Translations: map[string]string{}}
	i.Set("user_mailer.welcome.subject", "Hi")
	if _, ok := i.Translations["en.user_mailer.welcome.subject"]; !ok {
		t.Fatalf("empty locale did not default to en: %v", i.Translations)
	}
	if s, ok := i.subject("user_mailer", "welcome", nil); !ok || s != "Hi" {
		t.Fatalf("subject = %q ok=%v", s, ok)
	}
}

func TestTextLeafNonASCIIUsesBase64(t *testing.T) {
	p := textLeaf("text/plain", "Cafés €")
	if !strings.Contains(strings.ToLower(p.Field("Content-Transfer-Encoding")), "base64") {
		t.Fatalf("CTE = %q", p.Field("Content-Transfer-Encoding"))
	}
	if string(p.Decoded()) != "Cafés €" {
		t.Fatalf("decoded = %q", p.Decoded())
	}
}

func TestMailerScopeAndHumanize(t *testing.T) {
	if s := mailerScope("UserMailer"); s != "user_mailer" {
		t.Fatalf("scope = %q", s)
	}
	if humanize("") != "" {
		t.Fatal("humanize empty")
	}
}

func TestI18nSubjectViaMail(t *testing.T) {
	b := New("UserMailer")
	b.UseTestDelivery()
	b.I18n = NewI18n("en").Set("user_mailer.welcome.subject", "Hi %{name}")
	b.Register("welcome", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{
			To:          []string{"y@example.com"},
			Body:        "hi",
			SubjectVars: map[string]any{"name": "Ada"},
		})
	})
	msg, err := b.Process("welcome").Message()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject() != "Hi Ada" {
		t.Fatalf("subject = %q", msg.Subject())
	}
}

// --- attachment filename quoting ---------------------------------------------

func TestQuoteFilename(t *testing.T) {
	if got := quoteFilename("terms.pdf"); got != "terms.pdf" {
		t.Fatalf("token filename = %q", got)
	}
	if got := quoteFilename("my report.pdf"); got != `"my report.pdf"` {
		t.Fatalf("spaced filename = %q", got)
	}
	if got := quoteFilename(`a"b.pdf`); got != `"a\"b.pdf"` {
		t.Fatalf("quoted filename = %q", got)
	}
	if got := quoteFilename(""); got != `""` {
		t.Fatalf("empty filename = %q", got)
	}
}

func TestIsToken(t *testing.T) {
	if isToken("a b") {
		t.Fatal("space is not a token")
	}
	if isToken("a;b") {
		t.Fatal("tspecial is not a token")
	}
	if isToken("a\x7f") {
		t.Fatal("DEL is not a token")
	}
	if !isToken("file-1.txt") {
		t.Fatal("plain name is a token")
	}
}

// --- addRootCharset -----------------------------------------------------------

func TestAddRootCharsetSinglePartNoop(t *testing.T) {
	leaf := textLeaf("text/plain", "x")
	addRootCharset(leaf)
	if strings.Contains(leaf.ContentType(), "charset=UTF-8; charset") {
		t.Fatal("single part should not gain a second charset")
	}
}

// --- sendmail delivery --------------------------------------------------------

func TestSendmailDeliveryReadsRecipientsFromMessage(t *testing.T) {
	sd := NewSendmailDelivery()
	if sd.Location != "/usr/sbin/sendmail" {
		t.Fatalf("location = %q", sd.Location)
	}
	var gotPath string
	var gotArgs []string
	var gotStdin []byte
	sd.run = func(path string, args []string, stdin []byte) error {
		gotPath, gotArgs, gotStdin = path, args, stdin
		return nil
	}
	m := mail.New("").SetFrom("f@x").SetTo("t@x").SetBody("hi")
	if err := sd.Deliver(m); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/usr/sbin/sendmail" {
		t.Fatalf("path = %q", gotPath)
	}
	// -t present, so recipients are NOT appended.
	if strings.Join(gotArgs, " ") != "-i -t" {
		t.Fatalf("args = %v", gotArgs)
	}
	if !strings.Contains(string(gotStdin), "hi") {
		t.Fatalf("stdin = %q", gotStdin)
	}
}

func TestSendmailDeliveryAppendsRecipientsWithoutT(t *testing.T) {
	var gotArgs []string
	sd := &SendmailDelivery{
		Location:  "/bin/false",
		Arguments: []string{"-i"},
		run:       func(path string, args []string, stdin []byte) error { gotArgs = args; return nil },
	}
	m := mail.New("").SetTo("a@x").SetCc("b@x").SetBcc("c@x").SetBody("hi")
	if err := sd.Deliver(m); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgs, " ") != "-i a@x b@x c@x" {
		t.Fatalf("args = %v", gotArgs)
	}
}

func TestSendmailDeliveryRunErrorAndNilFallback(t *testing.T) {
	boom := errors.New("sendmail failed")
	sd := &SendmailDelivery{Location: "x", Arguments: []string{"-t"},
		run: func(string, []string, []byte) error { return boom }}
	if err := sd.Deliver(mail.New("").SetBody("x")); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// A zero-value SendmailDelivery (nil run) falls back to the real executor,
	// which fails to spawn the bogus binary — we only assert it returns an error.
	sd2 := &SendmailDelivery{Location: "/nonexistent/sendmail", Arguments: []string{"-t"}}
	if err := sd2.Deliver(mail.New("").SetBody("x")); err == nil {
		t.Fatal("expected spawn error from nil-run fallback")
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"-i", "-t"}, "-t") || hasFlag([]string{"-i"}, "-t") {
		t.Fatal("hasFlag mismatch")
	}
}

func TestUseSendmail(t *testing.T) {
	b := New("M").UseSendmail()
	if _, ok := b.DeliveryMethod.(*SendmailDelivery); !ok {
		t.Fatalf("delivery method = %T", b.DeliveryMethod)
	}
}

// --- View: template + layout rendering ---------------------------------------

// testEval is a tiny stand-in for the ERB seam: it substitutes <%= name %> with
// the string form of the matching local (yield is html-safe), and returns an
// error for the sentinel source "BOOM".
func testEval(src string, locals map[string]any) (string, error) {
	if src == "BOOM" {
		return "", errors.New("eval boom")
	}
	out := src
	for {
		i := strings.Index(out, "<%=")
		if i < 0 {
			break
		}
		j := strings.Index(out[i:], "%>")
		if j < 0 {
			break
		}
		name := strings.TrimSpace(out[i+3 : i+j])
		val := ""
		if v, ok := locals[name]; ok {
			val = toStr(v)
		}
		out = out[:i] + val + out[i+j+2:]
	}
	return out, nil
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case actionview.SafeBuffer:
		return x.String()
	default:
		return ""
	}
}

func newTestStore() *TemplateStore {
	return NewTemplateStore().
		Add("UserMailer", "welcome", "text", "Hi <%= name %>").
		Add("UserMailer", "welcome", "html", "<p>Hi <%= name %></p>").
		AddLayout("mailer", "text", "", "== <%= yield %> ==").
		AddLayout("mailer", "html", "", "<body><%= yield %></body>")
}

func TestViewRenderBodyWithLayout(t *testing.T) {
	v := &View{Store: newTestStore(), Eval: testEval, Layout: "mailer"}
	rb := v.RenderBody()
	text, err := rb("UserMailer", "welcome", "text", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "== Hi Ada ==" {
		t.Fatalf("text = %q", text)
	}
	html, err := rb("UserMailer", "welcome", "html", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if html != "<body><p>Hi Ada</p></body>" {
		t.Fatalf("html = %q", html)
	}
}

func TestViewRenderBodyNoLayout(t *testing.T) {
	v := &View{Store: newTestStore(), Eval: testEval} // no Layout
	rb := v.RenderBody()
	got, err := rb("UserMailer", "welcome", "text", map[string]any{"name": "Zoe"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hi Zoe" {
		t.Fatalf("got = %q", got)
	}
}

func TestViewRenderBodyMissingTemplateSkips(t *testing.T) {
	v := &View{Store: newTestStore(), Eval: testEval}
	if _, err := v.RenderBody()("UserMailer", "welcome", "pdf", nil); !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("err = %v", err)
	}
}

func TestViewLocaleFallback(t *testing.T) {
	s := NewTemplateStore().
		Add("UserMailer", "welcome", "text", "default").
		AddLocalized("UserMailer", "welcome", "text", "fr", "bonjour")
	vFr := &View{Store: s, Eval: testEval, Locale: "fr"}
	got, _ := vFr.RenderBody()("UserMailer", "welcome", "text", nil)
	if got != "bonjour" {
		t.Fatalf("fr = %q", got)
	}
	vEs := &View{Store: s, Eval: testEval, Locale: "es"} // falls back to locale-less
	got, _ = vEs.RenderBody()("UserMailer", "welcome", "text", nil)
	if got != "default" {
		t.Fatalf("es fallback = %q", got)
	}
}

func TestViewEvalErrorPropagates(t *testing.T) {
	s := NewTemplateStore().Add("M", "a", "text", "BOOM")
	v := &View{Store: s, Eval: testEval}
	if _, err := v.RenderBody()("M", "a", "text", nil); err == nil {
		t.Fatal("expected eval error")
	}
	// Layout eval error.
	s2 := NewTemplateStore().Add("M", "a", "text", "ok").AddLayout("mailer", "text", "", "BOOM")
	v2 := &View{Store: s2, Eval: testEval, Layout: "mailer"}
	if _, err := v2.RenderBody()("M", "a", "text", nil); err == nil {
		t.Fatal("expected layout eval error")
	}
}

func TestViewNoEvalSeam(t *testing.T) {
	v := &View{Store: NewTemplateStore().Add("M", "a", "text", "x")} // Eval nil
	if _, err := v.RenderBody()("M", "a", "text", nil); !errors.Is(err, ErrNoEval) {
		t.Fatalf("err = %v", err)
	}
}

func TestViewPartialResolution(t *testing.T) {
	s := NewTemplateStore().
		Add("M", "a", "html", "body").
		AddPartial("footer", "html", "", "FOOTER").
		AddPartial("sig", "text", "", "SIG")
	v := &View{Store: s, Eval: testEval}
	ctx := v.context()
	// A registered partial name resolves to its source.
	got, err := ctx.RenderTemplate("footer", map[string]any{"format": "html"})
	if err != nil || got != "FOOTER" {
		t.Fatalf("footer = %q err=%v", got, err)
	}
	// Unknown identifier is evaluated as inline source.
	got, _ = ctx.RenderTemplate("literal <%= name %>", map[string]any{"name": "Z"})
	if got != "literal Z" {
		t.Fatalf("inline = %q", got)
	}
	// Default format is html when no "format" local is present.
	if v.currentFormat(nil) != "html" {
		t.Fatal("default format should be html")
	}
	if v.currentFormat(map[string]any{"format": "text"}) != "text" {
		t.Fatal("explicit format ignored")
	}
}

func TestViewContextIsCopied(t *testing.T) {
	base := &actionview.Context{ProtectAgainstForgery: true}
	v := &View{Store: NewTemplateStore(), Eval: testEval, Context: base}
	ctx := v.context()
	if !ctx.ProtectAgainstForgery {
		t.Fatal("context fields should be copied")
	}
	if base.RenderTemplate != nil {
		t.Fatal("caller's context seam must not be mutated")
	}
}

func TestBaseUseView(t *testing.T) {
	v := &View{Store: newTestStore(), Eval: testEval, Layout: "mailer"}
	b := New("UserMailer").UseView(v)
	b.UseTestDelivery()
	b.Register("welcome", func(m *Mailer, params ...any) error {
		return m.Mail(MailOptions{To: []string{"y@example.com"}, Locals: map[string]any{"name": "Ada"}})
	})
	msg, err := b.Process("welcome").Message()
	if err != nil {
		t.Fatal(err)
	}
	if !msg.Multipart() {
		t.Fatal("expected multipart from text+html templates")
	}
	txt := msg.TextPart()
	if txt == nil || !strings.Contains(string(txt.Decoded()), "== Hi Ada ==") {
		t.Fatalf("text part = %v", txt)
	}
}

func TestMergeViewLocals(t *testing.T) {
	out := mergeViewLocals(map[string]any{"a": 1, "b": 2}, map[string]any{"b": 3})
	if out["a"] != 1 || out["b"] != 3 {
		t.Fatalf("merged = %v", out)
	}
}
