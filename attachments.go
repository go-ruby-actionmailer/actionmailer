// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"path/filepath"
	"strings"

	"github.com/go-ruby-mail/mail"
)

// GenerateContentID produces the Content-ID header value for an inline
// attachment. The default derives it deterministically from the name; a host
// may override it (Rails uses a random token @ the host name).
var GenerateContentID = func(name string) string { return "<" + name + ">" }

// Attachment is a single attachment, mirroring a Mail::Part built from
// `attachments[name] = data`.
type Attachment struct {
	// Name is the filename.
	Name string
	// Data is the decoded content (base64-encoded on the wire).
	Data []byte
	// ContentType is the media type; defaulted from the filename extension and
	// overridable before composition.
	ContentType string
	// Inline reports whether this is an inline (Content-Disposition: inline)
	// attachment carrying a Content-ID.
	Inline bool
	// ContentID is the Content-ID for an inline attachment (set on SetInline).
	ContentID string
}

// Attachments is the ordered attachment collection of a mailer action,
// mirroring the `attachments` proxy.
type Attachments struct {
	order  []*Attachment
	byName map[string]*Attachment
}

func newAttachments() *Attachments {
	return &Attachments{byName: map[string]*Attachment{}}
}

// Set adds (or replaces) a regular attachment named name and returns it,
// mirroring `attachments[name] = data`.
func (a *Attachments) Set(name string, data []byte) *Attachment {
	at := &Attachment{Name: name, Data: data, ContentType: guessType(name)}
	a.add(at)
	return at
}

// SetInline adds (or replaces) an inline attachment and returns it, assigning a
// Content-ID, mirroring `attachments.inline[name] = data`.
func (a *Attachments) SetInline(name string, data []byte) *Attachment {
	at := &Attachment{
		Name:        name,
		Data:        data,
		ContentType: guessType(name),
		Inline:      true,
		ContentID:   GenerateContentID(name),
	}
	a.add(at)
	return at
}

func (a *Attachments) add(at *Attachment) {
	if _, exists := a.byName[at.Name]; exists {
		for i, o := range a.order {
			if o.Name == at.Name {
				a.order[i] = at
			}
		}
	} else {
		a.order = append(a.order, at)
	}
	a.byName[at.Name] = at
}

// Get returns the attachment named name, or nil, mirroring `attachments[name]`.
func (a *Attachments) Get(name string) *Attachment { return a.byName[name] }

// All returns every attachment in insertion order.
func (a *Attachments) All() []*Attachment { return a.order }

// Inline returns the inline attachments in insertion order.
func (a *Attachments) Inline() []*Attachment { return a.filter(true) }

// Regular returns the non-inline attachments in insertion order.
func (a *Attachments) Regular() []*Attachment { return a.filter(false) }

func (a *Attachments) filter(inline bool) []*Attachment {
	var out []*Attachment
	for _, at := range a.order {
		if at.Inline == inline {
			out = append(out, at)
		}
	}
	return out
}

// attachmentPart builds the Mail::Part for an attachment, mirroring the Mail
// gem's add_file output: a bare (parameter-less) Content-Type, a base64 body,
// and a Content-Disposition carrying the filename (plus a Content-ID for inline
// parts). Field order is normalised later by sortHeaders.
func attachmentPart(a *Attachment) *mail.Message {
	p := mail.New("")
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	p.SetHeader("Content-Type", ct)

	disposition := "attachment"
	if a.Inline {
		disposition = "inline"
	}
	p.SetHeader("Content-Disposition", disposition+"; filename="+quoteFilename(a.Name))
	if a.Inline {
		p.SetHeader("Content-ID", a.ContentID)
	}

	b := &mail.Body{Raw: string(a.Data)}
	p.SetBody(b.Encode("base64"))
	// SetContentTransferEncoding (not a bare header) keeps the part's Body
	// Encoding in sync so Decoded() round-trips the base64 payload.
	p.SetContentTransferEncoding("base64")
	return p
}

// quoteFilename renders a filename for an RFC 2045 parameter: a bare token when
// it is made only of token characters (as the Mail gem emits, e.g.
// filename=terms.pdf), otherwise a quoted-string.
func quoteFilename(name string) string {
	if name != "" && isToken(name) {
		return name
	}
	return `"` + strings.ReplaceAll(name, `"`, `\"`) + `"`
}

// isToken reports whether s is a valid RFC 2045 token (no tspecials, spaces or
// controls), so it may appear unquoted as a parameter value.
func isToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x20 || c >= 0x7F {
			return false
		}
		switch c {
		case '(', ')', '<', '>', '@', ',', ';', ':', '\\', '"', '/', '[', ']', '?', '=':
			return false
		}
	}
	return true
}

// builtinTypes maps common extensions to media types, kept deterministic across
// platforms (unlike mime.TypeByExtension, which consults system tables).
var builtinTypes = map[string]string{
	".txt":  "text/plain",
	".html": "text/html",
	".htm":  "text/html",
	".csv":  "text/csv",
	".pdf":  "application/pdf",
	".json": "application/json",
	".xml":  "application/xml",
	".zip":  "application/zip",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
}

// guessType derives a media type from the filename extension, defaulting to
// application/octet-stream.
func guessType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := builtinTypes[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}
