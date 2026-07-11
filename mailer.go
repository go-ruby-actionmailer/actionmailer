// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-ruby-activesupport/activesupport/coreext"
	"github.com/go-ruby-mail/mail"
)

// GenerateBoundary produces a MIME multipart boundary for the seq-th container
// of a message. The default is deterministic (so composed output is
// reproducible in tests); a host may override it for random boundaries.
var GenerateBoundary = func(seq int) string { return fmt.Sprintf("boundary_%d", seq) }

// Mailer is the per-invocation mailer instance — the `self` of a Rails mailer
// action. It accumulates attachments and extra headers and, via [Mailer.Mail],
// composes the message. Actions receive it from [Base.Process].
type Mailer struct {
	base    *Base
	action  string
	atts    *Attachments
	headers map[string]string
	msg     *mail.Message

	boundarySeq int
}

// MailOptions are the keyword arguments of the Rails `mail(...)` call.
type MailOptions struct {
	// From is the sender; when blank the "from" default is used.
	From string
	// To, Cc, Bcc and ReplyTo are recipient/reply lists; each list is joined
	// with ", " into its header. Blank lists fall back to the matching default.
	To, Cc, Bcc, ReplyTo []string
	// Subject is the subject; when blank the "subject" default is used, and if
	// that is blank too an i18n lookup (see [Base.I18n]) resolves it.
	Subject string
	// SubjectVars are the %{name} interpolation values for an i18n subject
	// lookup (the interpolations of ActionMailer's default_i18n_subject).
	SubjectVars map[string]any
	// Date sets the Date header when HasDate is true; otherwise Base.Now is used.
	Date    time.Time
	HasDate bool
	// Headers are extra headers set on the message (highest precedence).
	Headers map[string]string

	// Body, when non-empty, is used as a single-part body of ContentType
	// (default text/plain), bypassing the RenderBody seam.
	Body string
	// ContentType is the media type for an explicit Body (default text/plain).
	ContentType string

	// Formats lists the formats rendered via the RenderBody seam when Body is
	// empty (default {"text", "html"}).
	Formats []string
	// Locals are passed through to the RenderBody seam.
	Locals map[string]any
}

// Attachments returns the attachment collection for this action, mirroring the
// mailer's `attachments` accessor.
func (m *Mailer) Attachments() *Attachments { return m.atts }

// Headers merges the given headers into the message being built, mirroring the
// mailer's `headers(hash)` call. Later calls override earlier ones.
func (m *Mailer) Headers(h map[string]string) *Mailer {
	for k, v := range h {
		m.headers[k] = v
	}
	return m
}

// Mail composes the message from opts and the accumulated attachments/headers,
// mirroring `mail(...)`. It stores the result so [Base.Process] can return it.
func (m *Mailer) Mail(opts MailOptions) error {
	d := m.base.Defaults
	from := firstPresent(opts.From, d["from"])
	subject := firstPresent(opts.Subject, d["subject"])
	if !coreext.Present(subject) {
		subject = m.base.i18nSubject(m.action, opts.SubjectVars)
	}
	replyTo := defaultList(opts.ReplyTo, d["reply_to"])
	to := defaultList(opts.To, d["to"])
	cc := defaultList(opts.Cc, d["cc"])
	bcc := defaultList(opts.Bcc, d["bcc"])
	opts.ContentType = firstPresent(opts.ContentType, d["content_type"])

	root, err := m.buildBodyRoot(opts)
	if err != nil {
		return err
	}

	if coreext.Present(from) {
		root.SetFrom(from)
	}
	if len(to) > 0 {
		root.SetTo(strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		root.SetCc(strings.Join(cc, ", "))
	}
	if len(bcc) > 0 {
		root.SetBcc(strings.Join(bcc, ", "))
	}
	if len(replyTo) > 0 {
		root.SetReplyTo(strings.Join(replyTo, ", "))
	}
	if coreext.Present(subject) {
		root.SetSubject(subject)
	}
	if opts.HasDate {
		root.SetDate(opts.Date)
	} else if m.base.Now != nil {
		root.SetDate(m.base.Now())
	}
	root.SetHeader("MIME-Version", "1.0")
	if m.base.MessageIDGen != nil {
		root.SetMessageID(m.base.MessageIDGen())
	}

	// Custom headers: defaults (lowest) < per-message Headers() < opts.Headers,
	// resolved with ActiveSupport's reverse_merge precedence.
	for k, v := range mergedHeaders(m.base.defaultHeaders(), m.headers, opts.Headers) {
		root.SetHeader(k, v)
	}

	// Stamp charset on a multipart root and emit every field in the Mail gem's
	// canonical order, so the wire bytes match Mail::Message#encoded.
	addRootCharset(root)
	sortHeaders(root)

	m.msg = root
	return nil
}

// buildBodyRoot assembles the MIME body tree: the rendered/explicit content
// parts (wrapped in multipart/alternative when there is more than one), then a
// multipart/related layer for inline attachments, then a multipart/mixed layer
// for regular attachments — mirroring ActionMailer's assembly.
func (m *Mailer) buildBodyRoot(opts MailOptions) (*mail.Message, error) {
	var leaves []*mail.Message

	if opts.Body != "" {
		ct := opts.ContentType
		if ct == "" {
			ct = "text/plain"
		}
		leaves = append(leaves, textLeaf(ct, opts.Body))
	} else {
		if m.base.RenderBody == nil {
			return nil, ErrNoRenderBody
		}
		formats := opts.Formats
		if len(formats) == 0 {
			formats = []string{"text", "html"}
		}
		for _, f := range formats {
			body, err := m.base.RenderBody(m.base.Name, m.action, f, opts.Locals)
			if errors.Is(err, ErrNoTemplate) {
				continue
			}
			if err != nil {
				return nil, err
			}
			leaves = append(leaves, textLeaf(mimeForFormat(f), body))
		}
	}

	var body *mail.Message
	switch len(leaves) {
	case 0:
		body = nil
	case 1:
		body = leaves[0]
	default:
		body = m.multipart("alternative", leaves)
	}

	if inline := m.atts.Inline(); len(inline) > 0 {
		parts := []*mail.Message{}
		if body != nil {
			parts = append(parts, body)
		}
		for _, a := range inline {
			parts = append(parts, attachmentPart(a))
		}
		body = m.multipart("related", parts)
	}

	if regular := m.atts.Regular(); len(regular) > 0 {
		parts := []*mail.Message{}
		if body != nil {
			parts = append(parts, body)
		}
		for _, a := range regular {
			parts = append(parts, attachmentPart(a))
		}
		body = m.multipart("mixed", parts)
	}

	if body == nil {
		body = textLeaf("text/plain", "")
	}
	return body, nil
}

// multipart builds a multipart/<subtype> container wrapping parts, with a fresh
// deterministic boundary. Like the Mail gem, every multipart container carries a
// Content-Transfer-Encoding of 7bit.
func (m *Mailer) multipart(subtype string, parts []*mail.Message) *mail.Message {
	m.boundarySeq++
	boundary := GenerateBoundary(m.boundarySeq)
	c := mail.New("")
	c.SetContentTypeParams("multipart/"+subtype, []string{"boundary"},
		map[string]string{"boundary": boundary})
	c.SetHeader("Content-Transfer-Encoding", "7bit")
	for _, p := range parts {
		c.AddPart(p)
	}
	return c
}

// addRootCharset stamps charset=UTF-8 onto a multipart root container's
// Content-Type (after the boundary parameter), mirroring how ActionMailer sets
// the message charset on the top-level part. Single-part leaves already carry
// their own charset, so only multipart roots need this.
func addRootCharset(root *mail.Message) {
	if len(root.Parts()) == 0 {
		return
	}
	params := root.ContentTypeParameters()
	root.SetContentTypeParams(root.MimeType(), []string{"boundary", "charset"},
		map[string]string{"boundary": params["boundary"], "charset": "UTF-8"})
}

// textLeaf builds a single content part of the given media type (charset UTF-8),
// negotiating its Content-Transfer-Encoding the way the Mail gem does: 7bit for
// pure US-ASCII, otherwise the lower-cost of quoted-printable and base64.
func textLeaf(contentType, body string) *mail.Message {
	p := mail.New("")
	p.SetContentTypeParams(contentType, []string{"charset"},
		map[string]string{"charset": "UTF-8"})
	enc := negotiateTextEncoding(body)
	if enc == "7bit" {
		p.SetHeader("Content-Transfer-Encoding", "7bit")
		p.SetBody(body)
		return p
	}
	b := &mail.Body{Raw: body}
	p.SetBody(b.Encode(enc))
	p.SetContentTransferEncoding(enc)
	return p
}

// mimeForFormat maps a format token to a media type.
func mimeForFormat(format string) string {
	if format == "html" {
		return "text/html"
	}
	return "text/plain"
}

// firstPresent returns a when it is present (ActiveSupport present?), else b.
func firstPresent(a, b string) string {
	if coreext.Present(a) {
		return a
	}
	return b
}

// defaultList returns list when non-empty, else a single-element list built from
// def when def is present, else nil.
func defaultList(list []string, def string) []string {
	if len(list) > 0 {
		return list
	}
	if coreext.Present(def) {
		return []string{def}
	}
	return nil
}

// mergedHeaders resolves custom-header precedence with ActiveSupport's
// reverse_merge: overrides win over perMessage, which win over defaults.
func mergedHeaders(defaults, perMessage, overrides map[string]string) map[string]string {
	merged := coreext.ReverseMerge(toAnyMap(perMessage), toAnyMap(defaults))
	merged = coreext.ReverseMerge(toAnyMap(overrides), merged)
	out := map[string]string{}
	for k, v := range merged {
		out[k.(string)] = v.(string)
	}
	return out
}

// toAnyMap adapts a string map to the map[any]any shape ActiveSupport uses.
func toAnyMap(m map[string]string) map[any]any {
	out := map[any]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
