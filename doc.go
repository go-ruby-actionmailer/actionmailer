// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package actionmailer is a pure-Go (CGO-free) reimplementation of the
// foundation of Ruby on Rails' Action Mailer — the framework that composes and
// delivers email from mailer classes. It is faithful to MRI / Rails 4.0.5
// semantics for message composition, multipart MIME assembly, delivery methods,
// and the interceptor/observer hooks, without any Ruby runtime.
//
// # What it is — and isn't
//
// Composing a message (assembling headers, multipart/alternative bodies,
// attachments, and choosing a delivery method) is deterministic and
// interpreter-independent, so it lives here as pure Go. Two things are host
// seams rather than baked in:
//
//   - Rendering a mail body from a template is delegated to a [RenderBody]
//     callback. In Rails this is Action View resolving `welcome.text.erb` /
//     `welcome.html.erb`; here the host (for example go-embedded-ruby wiring up
//     go-ruby-actionview) supplies the rendered text/HTML for each format and
//     this package assembles the MIME structure around it.
//   - The on-the-wire byte encoding of a message is delegated to
//     go-ruby-mail (the Mail gem): this package builds a [*mail.Message] and
//     lets go-ruby-mail fold headers, encode words, and lay out boundaries.
//
// # API shape
//
// [New] creates a [Base] — the analogue of a subclass of ActionMailer::Base —
// carrying class-level configuration ([Base.Default] params, the delivery
// method, perform/raise flags, interceptors, observers). [Base.Register] binds a
// named action to an [Action] closure; running it with [Base.Process] returns a
// [MessageDelivery] (the analogue of `MyMailer.welcome(user)`), whose
// [MessageDelivery.DeliverNow] / [MessageDelivery.DeliverLater] /
// [MessageDelivery.Message] mirror the Rails proxy.
//
// Inside an action, the [*Mailer] receiver exposes [Mailer.Mail] (the
// `mail(to:, from:, subject:, …)` call), [Mailer.Headers], and
// [Mailer.Attachments] (both regular and inline/content-id attachments).
//
// # Delivery methods
//
// [DeliveryMethod] is the pluggable transport. [TestDelivery] appends to a
// slice (ActionMailer::Base.deliveries), [FileDelivery] writes one file per
// recipient, and [SMTPDelivery] speaks SMTP via net/smtp through an injectable
// send function so tests never open a socket.
package actionmailer
