// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

// negotiateTextEncoding chooses the Content-Transfer-Encoding for a text body,
// mirroring Mail::Encodings::TransferEncoding.negotiate for a UTF-8 text part:
//
//   - a body that is already 7bit-safe (pure US-ASCII, no NUL) is transported
//     verbatim as "7bit";
//   - otherwise the gem picks the lowest-cost of quoted-printable and base64,
//     breaking ties in favour of quoted-printable (its PRIORITY, 2, is lower
//     than base64's 3). base64 has a constant cost of 4/3; quoted-printable's
//     cost is ((bytesize-safe)*3 + safe) / bytesize.
//
// It returns the encoding name understood by go-ruby-mail's Body.Encode
// ("7bit", "quoted-printable" or "base64").
func negotiateTextEncoding(body string) string {
	if is7bitSafe(body) {
		return "7bit"
	}
	if qpCost(body) <= base64Cost {
		return "quoted-printable"
	}
	return "base64"
}

// base64Cost is Mail::Encodings::Base64.cost — 3 bytes in produce 4 bytes out.
const base64Cost = 4.0 / 3.0

// is7bitSafe reports whether body can be transported as 7bit: every byte is a
// non-NUL US-ASCII byte (< 0x80), matching SevenBit.compatible_input? for the
// common case of a text part.
func is7bitSafe(body string) bool {
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == 0x00 || c >= 0x80 {
			return false
		}
	}
	return true
}

// qpCost computes Mail::Encodings::QuotedPrintable.cost(str): the "safe" bytes
// (\t \n \r, 0x20-0x3C, 0x3E-0x7E — note '=' 0x3D is excluded) cost 1 each and
// every other byte costs 3, divided by the total length.
func qpCost(body string) float64 {
	if len(body) == 0 {
		return 0
	}
	safe := 0
	for i := 0; i < len(body); i++ {
		if qpSafeByte(body[i]) {
			safe++
		}
	}
	total := (len(body)-safe)*3 + safe
	return float64(total) / float64(len(body))
}

// qpSafeByte reports whether c is in the Mail gem's quoted-printable "safe" set
// str.count("\x9\xA\xD\x20-\x3C\x3E-\x7E").
func qpSafeByte(c byte) bool {
	switch c {
	case 0x09, 0x0A, 0x0D:
		return true
	}
	return (c >= 0x20 && c <= 0x3C) || (c >= 0x3E && c <= 0x7E)
}
