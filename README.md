<p align="center"><img src="https://raw.githubusercontent.com/go-ruby-actionmailer/brand/main/social/go-ruby-actionmailer-actionmailer.png" alt="go-ruby-actionmailer/actionmailer" width="720"></p>

# actionmailer — go-ruby-actionmailer

[![Docs](https://img.shields.io/badge/docs-mkdocs--material-DC2626)](https://go-ruby-actionmailer.github.io/docs/)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

**A pure-Go (no cgo) reimplementation of the foundation of Rails'
[Action Mailer](https://guides.rubyonrails.org/action_mailer_basics.html)** —
the framework that composes and delivers email from mailer classes. Faithful to
MRI / Rails 4.0.5 semantics for message composition, multipart MIME assembly,
delivery methods, and the interceptor/observer hooks — **without any Ruby
runtime**.

It is the Action Mailer backend for
[go-embedded-ruby](https://github.com/go-embedded-ruby/ruby), but a
**standalone, reusable** module. It builds on two siblings:

- [**go-ruby-mail**](https://github.com/go-ruby-mail/mail) — the Mail gem: this
  package assembles a `Mail::Message` and lets go-ruby-mail fold headers, encode
  words, and lay out MIME boundaries (the on-the-wire byte encoding).
- [**go-ruby-activesupport**](https://github.com/go-ruby-activesupport/activesupport)
  — `present?` / `blank?` option handling and `reverse_merge` header precedence.

## Two seams

Everything interpreter-independent lives here as pure Go. Two things are host
seams:

1. **Body rendering** is a [`RenderBody`](#api) callback. In Rails this is Action
   View resolving `welcome.text.erb` / `welcome.html.erb`; here the host (for
   example wiring up [go-ruby-actionview](https://github.com/go-ruby-actionview/actionview))
   supplies the rendered text/HTML per format and this package assembles the MIME
   structure (multipart/alternative → related → mixed) around it. Returning
   `ErrNoTemplate` skips a format, exactly as a missing template does.
2. **Delivery transport** is the [`DeliveryMethod`](#api) interface. `SMTPDelivery`
   performs the send through an injectable `net/smtp.SendMail`-shaped function, so
   tests never open a socket.

## Install

```sh
go get github.com/go-ruby-actionmailer/actionmailer
```

## Usage

```go
package main

import (
	"fmt"

	am "github.com/go-ruby-actionmailer/actionmailer"
)

func main() {
	m := am.New("UserMailer")
	m.Default("from", "notifications@example.com")
	m.UseTestDelivery() // append to m.Deliveries instead of sending

	// The body-rendering seam (Action View stands in here).
	m.RenderBody = func(mailer, action, format string, locals map[string]any) (string, error) {
		switch format {
		case "text":
			return fmt.Sprintf("Welcome, %s!", locals["name"]), nil
		case "html":
			return fmt.Sprintf("<h1>Welcome, %s!</h1>", locals["name"]), nil
		}
		return "", am.ErrNoTemplate
	}

	m.Register("welcome", func(mm *am.Mailer, params ...any) error {
		name := params[0].(string)
		mm.Attachments().Set("terms.pdf", []byte("%PDF-1.4 ..."))
		return mm.Mail(am.MailOptions{
			To:      []string{"ada@example.com"},
			Subject: "Welcome",
			Locals:  map[string]any{"name": name},
		})
	})

	// Mirrors UserMailer.welcome("Ada").deliver_now
	if err := m.Process("welcome", "Ada").DeliverNow(); err != nil {
		panic(err)
	}
	fmt.Println(m.Deliveries[0].Encoded())
}
```

The composed message above is `multipart/mixed` [ `multipart/alternative`
[ text/plain, text/html ], terms.pdf ]. Add inline attachments with
`Attachments().SetInline(name, data)` and the alternative is wrapped in a
`multipart/related` carrying the Content-IDs.

## API

```go
// Mailer class configuration + action registry (subclass of ActionMailer::Base).
func New(name string) *Base
func (b *Base) Default(key, value string) *Base         // default from:, subject:, …
func (b *Base) Register(action string, fn Action) *Base // bind a mailer action
func (b *Base) Process(action string, params ...any) *MessageDelivery // Mailer.action(params)
func (b *Base) RegisterInterceptor(i Interceptor) *Base
func (b *Base) RegisterObserver(o Observer) *Base
func (b *Base) UseTestDelivery() *Base

// Class-level config fields: RenderBody, DeliveryMethod, PerformDeliveries,
// RaiseDeliveryErrors, Deliveries, Now, MessageIDGen, EnqueueJob.

// The body-rendering seam.
type RenderBody func(mailer, action, format string, locals map[string]any) (string, error)

// An action's body; call m.Mail(...) inside it.
type Action func(m *Mailer, params ...any) error

// Per-invocation mailer instance (self).
func (m *Mailer) Mail(opts MailOptions) error       // mail(to:, from:, subject:, …)
func (m *Mailer) Headers(h map[string]string) *Mailer
func (m *Mailer) Attachments() *Attachments

type MailOptions struct {
	From                 string
	To, Cc, Bcc, ReplyTo []string
	Subject              string
	Date                 time.Time
	HasDate              bool
	Headers              map[string]string
	Body, ContentType    string   // explicit single-part body (skips RenderBody)
	Formats              []string // formats to render (default text, html)
	Locals               map[string]any
}

// attachments[name] = data / attachments.inline[name] = data
func (a *Attachments) Set(name string, data []byte) *Attachment
func (a *Attachments) SetInline(name string, data []byte) *Attachment
func (a *Attachments) Get(name string) *Attachment
func (a *Attachments) All() []*Attachment
func (a *Attachments) Inline() []*Attachment
func (a *Attachments) Regular() []*Attachment

// The lazy delivery proxy (ActionMailer::MessageDelivery).
func (d *MessageDelivery) Message() (*mail.Message, error) // .message
func (d *MessageDelivery) DeliverNow() error               // .deliver_now
func (d *MessageDelivery) DeliverLater() error             // .deliver_later (Active Job seam)

// Delivery methods.
type DeliveryMethod interface{ Deliver(m *mail.Message) error }
func NewTestDelivery(dst *[]*mail.Message) *TestDelivery // :test — append to a slice
func NewFileDelivery(location string) *FileDelivery      // :file — one file per recipient
func NewSMTPDelivery(addr string) *SMTPDelivery          // :smtp — net/smtp (injectable Send)

// Interceptors & observers (register_interceptor / register_observer).
type Interceptor interface{ DeliveringEmail(m *mail.Message) }
type Observer interface{ DeliveredEmail(m *mail.Message) }
```

## MIME assembly

`Mail` builds the tree ActionMailer produces:

- rendered parts → a single leaf, or `multipart/alternative` when more than one;
- inline attachments wrap the body in `multipart/related` (carrying Content-IDs);
- regular attachments wrap everything in `multipart/mixed`.

Boundaries are produced by the overridable `GenerateBoundary` (deterministic by
default for reproducible output); inline Content-IDs by `GenerateContentID`.
Attachment media types are guessed from the filename extension and overridable on
the returned `*Attachment`.

## Roadmap (deferred)

This is the **v0.1 foundation** — mailer base, message composition, delivery
methods, and interceptor/observer hooks. Deferred to later passes:

- The **Rails engine & generators** (`rails g mailer`), railtie configuration.
- The full **Action View template resolver** — template lookup/inheritance,
  layouts, format/locale/handler resolution (rendering stays behind `RenderBody`).
- **i18n subject lookup** (`en.user_mailer.welcome.subject`) and locale fallbacks.
- **Mailer previews** (`ActionMailer::Preview`).
- **Inline-CSS / premailer** for HTML parts.
- **Text-part transfer-encoding** (quoted-printable / base64 auto-selection for
  non-ASCII bodies; v0.1 emits UTF-8 text parts as 7bit).
- The **brand asset** (banner) and MkDocs/mike docs site (org infra follow-up).

## Tests & coverage

Deterministic, ruby-free tests hold **line coverage at 100%** — message
composition (single-part, alternative, related, mixed, empty, attachments both
regular and inline), every delivery method (via fakes/temp dirs and an injected
SMTP sender — no real socket), the deliveries array, interceptors/observers,
`deliver_now` / `deliver_later` (inline and via the Active Job seam), and all
error branches. MIME output is asserted against the expected part structure and
round-tripped through go-ruby-mail's encoder.

```sh
COVERPKG=$(go list ./... | paste -sd, -)
go test -race -coverpkg="$COVERPKG" -coverprofile=cover.out ./...
go tool cover -func=cover.out | tail -1   # 100.0%
```

CGO-free, `gofmt` + `go vet` clean, and green across the six 64-bit Go targets
(amd64, arm64, riscv64, loong64, ppc64le, s390x) and three OSes
(Linux, macOS, Windows).

## License

BSD-3-Clause — see [LICENSE](LICENSE). Copyright the go-ruby-actionmailer/actionmailer authors.

## WebAssembly

Being pure Go (CGO=0), this library also compiles to **WebAssembly** — both
`GOOS=js GOARCH=wasm` (browser / Node.js) and `GOOS=wasip1 GOARCH=wasm` (WASI).
CI builds both targets on every push, alongside the six 64-bit native/qemu arches.

```sh
GOOS=js     GOARCH=wasm go build ./...   # browser / Node
GOOS=wasip1 GOARCH=wasm go build ./...   # WASI (wasmtime, wasmer, wasmedge, …)
```
