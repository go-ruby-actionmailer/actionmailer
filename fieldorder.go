// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"sort"
	"strings"

	"github.com/go-ruby-mail/mail"
)

// fieldOrder mirrors the Mail gem's Mail::Field::FIELD_ORDER_LOOKUP: header
// fields are emitted in this canonical order on encode, with any field not in
// the table sorted after the known ones (the gem uses index 100 for those).
var fieldOrder = func() map[string]int {
	names := []string{
		"return-path", "received",
		"resent-date", "resent-from", "resent-sender", "resent-to",
		"resent-cc", "resent-bcc", "resent-message-id",
		"date", "from", "sender", "reply-to", "to", "cc", "bcc",
		"message-id", "in-reply-to", "references",
		"subject", "comments", "keywords",
		"mime-version", "content-type", "content-transfer-encoding",
		"content-location", "content-disposition", "content-description",
	}
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[n] = i
	}
	return m
}()

// fieldOrderID returns a field's sort rank: its position in the Mail gem's
// canonical order, or 100 for any unrecognised field (matching the gem's
// FIELD_ORDER_LOOKUP.fetch(name, 100)).
func fieldOrderID(name string) int {
	if id, ok := fieldOrder[strings.ToLower(name)]; ok {
		return id
	}
	return 100
}

// sortHeaders reorders m's header fields into the Mail gem's canonical
// FIELD_ORDER (stably, so equal-ranked fields keep their insertion order,
// matching how the gem emits its headers) and recurses into every MIME part so
// nested part headers are ordered the same way.
func sortHeaders(m *mail.Message) {
	fields := m.Header().Fields()
	sort.SliceStable(fields, func(i, j int) bool {
		return fieldOrderID(fields[i].Name) < fieldOrderID(fields[j].Name)
	})
	for _, p := range m.Parts() {
		sortHeaders(p)
	}
}
