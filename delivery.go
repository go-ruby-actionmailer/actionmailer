// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"net/smtp"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-ruby-mail/mail"
)

// TestDelivery is the :test delivery method: it appends each delivered message
// to a slice (ActionMailer::Base.deliveries) instead of sending it.
type TestDelivery struct {
	deliveries *[]*mail.Message
}

// NewTestDelivery returns a [TestDelivery] appending to dst.
func NewTestDelivery(dst *[]*mail.Message) *TestDelivery {
	return &TestDelivery{deliveries: dst}
}

// Deliver appends m to the target slice.
func (t *TestDelivery) Deliver(m *mail.Message) error {
	*t.deliveries = append(*t.deliveries, m)
	return nil
}

// FileDelivery is the :file delivery method: it writes one file per recipient
// into a directory, each containing the encoded message.
type FileDelivery struct {
	// Location is the destination directory.
	Location string

	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
}

// NewFileDelivery returns a [FileDelivery] writing into location.
func NewFileDelivery(location string) *FileDelivery {
	return &FileDelivery{
		Location:  location,
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
	}
}

// Deliver writes the encoded message to Location/<recipient> for each To
// recipient.
func (f *FileDelivery) Deliver(m *mail.Message) error {
	if err := f.mkdirAll(f.Location, 0o755); err != nil {
		return err
	}
	encoded := []byte(m.Encoded())
	for _, to := range m.To() {
		path := filepath.Join(f.Location, sanitizeRecipient(to))
		if err := f.writeFile(path, encoded, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeRecipient makes a recipient address safe to use as a file name.
func sanitizeRecipient(addr string) string {
	repl := func(r rune) rune {
		switch r {
		case '/', '\\':
			return '_'
		default:
			return r
		}
	}
	return strings.Map(repl, addr)
}

// SMTPFunc is the socket seam used by [SMTPDelivery]: it matches the signature
// of net/smtp.SendMail so tests can inject a fake and never open a socket.
type SMTPFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// SMTPDelivery is the :smtp delivery method. The actual send is performed by
// Send (net/smtp.SendMail by default), which is injectable for testing.
type SMTPDelivery struct {
	// Addr is the "host:port" of the SMTP server.
	Addr string
	// Host is the server host used for PLAIN auth; when empty it is derived
	// from Addr.
	Host string
	// Username and Password enable PLAIN auth when Username is non-empty.
	Username string
	Password string
	// Send performs the send; defaults to net/smtp.SendMail.
	Send SMTPFunc
}

// NewSMTPDelivery returns an [SMTPDelivery] targeting addr ("host:port") using
// net/smtp.SendMail.
func NewSMTPDelivery(addr string) *SMTPDelivery {
	return &SMTPDelivery{Addr: addr, Send: smtp.SendMail}
}

// Deliver sends the encoded message via SMTP to its To, Cc and Bcc recipients.
func (s *SMTPDelivery) Deliver(m *mail.Message) error {
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, s.host())
	}

	var from string
	if froms := m.From(); len(froms) > 0 {
		from = froms[0]
	}

	to := make([]string, 0, len(m.To())+len(m.Cc())+len(m.Bcc()))
	to = append(to, m.To()...)
	to = append(to, m.Cc()...)
	to = append(to, m.Bcc()...)

	return s.Send(s.Addr, auth, from, to, []byte(m.Encoded()))
}

// host returns the auth host: Host if set, else the host part of Addr.
func (s *SMTPDelivery) host() string {
	if s.Host != "" {
		return s.Host
	}
	if i := strings.LastIndex(s.Addr, ":"); i >= 0 {
		return s.Addr[:i]
	}
	return s.Addr
}
