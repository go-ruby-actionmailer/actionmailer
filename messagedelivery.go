// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import "github.com/go-ruby-mail/mail"

// MessageDelivery is the lazy delivery proxy returned by [Base.Process],
// mirroring ActionMailer::MessageDelivery (`MyMailer.welcome(user)`). It carries
// the composed message, or the composition error, and exposes the deliver
// verbs.
type MessageDelivery struct {
	base *Base
	msg  *mail.Message
	err  error
}

// Message returns the composed message and any composition error, mirroring
// `.message`.
func (d *MessageDelivery) Message() (*mail.Message, error) { return d.msg, d.err }

// DeliverNow delivers the message immediately through the configured delivery
// method, mirroring `.deliver_now`. Composition errors surface here.
func (d *MessageDelivery) DeliverNow() error {
	if d.err != nil {
		return d.err
	}
	return d.base.deliver(d.msg)
}

// DeliverLater enqueues the delivery through the Active Job seam
// (Base.EnqueueJob), mirroring `.deliver_later`. When no seam is configured the
// delivery runs inline. Composition errors surface here.
func (d *MessageDelivery) DeliverLater() error {
	if d.err != nil {
		return d.err
	}
	job := func() error { return d.base.deliver(d.msg) }
	if d.base.EnqueueJob != nil {
		return d.base.EnqueueJob(job)
	}
	return job()
}
