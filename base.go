// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"errors"
	"fmt"
	"time"

	"github.com/go-ruby-mail/mail"
)

// RenderBody is the body-rendering seam. Given the mailer name, the action, a
// format ("text" / "html" / …) and the action's locals, it returns the rendered
// body for that format. Returning [ErrNoTemplate] signals that no template
// exists for the format, so it is skipped (mirroring Action View not finding a
// `.text` or `.html` template); any other error aborts composition.
type RenderBody func(mailer, action, format string, locals map[string]any) (string, error)

// Action is a mailer action: the body of a Rails mailer method. It receives the
// per-invocation [*Mailer] (the mailer instance / `self`) and the caller's
// parameters, and is expected to call [Mailer.Mail] to compose the message.
type Action func(m *Mailer, params ...any) error

// Interceptor is registered with [Base.RegisterInterceptor] and is invoked with
// the fully composed message just before delivery, mirroring
// register_interceptor / Mail's inform_interceptors.
type Interceptor interface {
	DeliveringEmail(m *mail.Message)
}

// Observer is registered with [Base.RegisterObserver] and is invoked with the
// message just after delivery, mirroring register_observer / inform_observers.
type Observer interface {
	DeliveredEmail(m *mail.Message)
}

// DeliveryMethod is the pluggable transport that actually delivers a composed
// message, mirroring ActionMailer's delivery_method registry.
type DeliveryMethod interface {
	Deliver(m *mail.Message) error
}

// Sentinel errors returned during composition.
var (
	// ErrNoTemplate is returned by a [RenderBody] seam to indicate that no
	// template exists for the requested format, so that format is skipped.
	ErrNoTemplate = errors.New("actionmailer: no template for format")

	// ErrNoRenderBody is returned by [Mailer.Mail] when a body must be rendered
	// but no [RenderBody] seam is configured on the [Base].
	ErrNoRenderBody = errors.New("actionmailer: no RenderBody seam configured")

	// ErrNoDeliveryMethod is returned when a message must be delivered but no
	// [DeliveryMethod] is configured.
	ErrNoDeliveryMethod = errors.New("actionmailer: no delivery method configured")
)

// Base is the analogue of a subclass of ActionMailer::Base: it holds the
// class-level configuration shared by every delivery, and the registry of named
// actions. Construct one with [New].
type Base struct {
	// Name is the mailer class name (e.g. "UserMailer"), passed to the
	// [RenderBody] seam so it can locate templates.
	Name string

	// Defaults holds the default headers/params (the Rails `default from: …`).
	// Recognised keys "from", "to", "cc", "bcc", "reply_to", "subject" and
	// "content_type" fill the corresponding message fields when an action omits
	// them; any other key becomes a default header.
	Defaults map[string]string

	// RenderBody is the body-rendering seam (nil unless bodies are rendered).
	RenderBody RenderBody

	// DeliveryMethod is the transport used by DeliverNow/DeliverLater.
	DeliveryMethod DeliveryMethod

	// PerformDeliveries gates whether the delivery method is actually invoked
	// (ActionMailer::Base.perform_deliveries). Defaults to true.
	PerformDeliveries bool

	// RaiseDeliveryErrors reports whether a delivery-method error propagates
	// (ActionMailer::Base.raise_delivery_errors). Defaults to true.
	RaiseDeliveryErrors bool

	// Deliveries is the sink used by [TestDelivery] (ActionMailer::Base.deliveries).
	Deliveries []*mail.Message

	// Now supplies the Date header when an action does not set one. When nil,
	// no Date is added. Defaults to time.Now.
	Now func() time.Time

	// MessageIDGen, when non-nil, supplies the Message-ID header.
	MessageIDGen func() string

	// EnqueueJob is the Active Job seam used by [MessageDelivery.DeliverLater].
	// When nil, DeliverLater runs the delivery inline.
	EnqueueJob func(job func() error) error

	// I18n resolves a subject when an action omits one (and no "subject" default
	// is set), mirroring ActionMailer's default_i18n_subject. When nil, an
	// action with no subject simply has no Subject header.
	I18n *I18n

	interceptors []Interceptor
	observers    []Observer
	actions      map[string]Action
}

// New creates a [Base] named name with Rails-default flags (perform_deliveries
// and raise_delivery_errors both true) and time.Now as the Date source.
func New(name string) *Base {
	return &Base{
		Name:                name,
		Defaults:            map[string]string{},
		PerformDeliveries:   true,
		RaiseDeliveryErrors: true,
		Now:                 time.Now,
		actions:             map[string]Action{},
	}
}

// Default sets a default param/header (Rails `default key: value`) and returns
// the receiver for chaining.
func (b *Base) Default(key, value string) *Base {
	b.Defaults[key] = value
	return b
}

// Register binds an [Action] to a name so it can be run with [Base.Process].
func (b *Base) Register(action string, fn Action) *Base {
	b.actions[action] = fn
	return b
}

// RegisterInterceptor adds a delivery interceptor.
func (b *Base) RegisterInterceptor(i Interceptor) *Base {
	b.interceptors = append(b.interceptors, i)
	return b
}

// RegisterObserver adds a delivery observer.
func (b *Base) RegisterObserver(o Observer) *Base {
	b.observers = append(b.observers, o)
	return b
}

// UseTestDelivery wires the delivery method to a [TestDelivery] appending to
// b.Deliveries, mirroring `config.action_mailer.delivery_method = :test`.
func (b *Base) UseTestDelivery() *Base {
	b.DeliveryMethod = NewTestDelivery(&b.Deliveries)
	return b
}

// UseSendmail wires the delivery method to a [SendmailDelivery], mirroring
// `config.action_mailer.delivery_method = :sendmail`.
func (b *Base) UseSendmail() *Base {
	b.DeliveryMethod = NewSendmailDelivery()
	return b
}

// UseView installs v's [View.RenderBody] as the body-rendering seam, mirroring
// wiring Action View's template resolver to a mailer.
func (b *Base) UseView(v *View) *Base {
	b.RenderBody = v.RenderBody()
	return b
}

// Process runs the named action to compose a message and returns a
// [MessageDelivery], mirroring `MyMailer.action(params)`. Composition errors
// (unknown action, action error, render error, or an action that never called
// [Mailer.Mail]) are captured on the delivery and surface from its methods.
func (b *Base) Process(action string, params ...any) *MessageDelivery {
	fn, ok := b.actions[action]
	if !ok {
		return &MessageDelivery{base: b, err: fmt.Errorf("actionmailer: unknown action %q", action)}
	}
	m := &Mailer{base: b, action: action, atts: newAttachments(), headers: map[string]string{}}
	if err := fn(m, params...); err != nil {
		return &MessageDelivery{base: b, err: err}
	}
	if m.msg == nil {
		return &MessageDelivery{base: b, err: fmt.Errorf("actionmailer: action %q did not call Mail", action)}
	}
	return &MessageDelivery{base: b, msg: m.msg}
}

// deliver runs the interceptors, performs the delivery (when enabled), and runs
// the observers, mirroring Mail::Message#deliver.
func (b *Base) deliver(m *mail.Message) error {
	for _, i := range b.interceptors {
		i.DeliveringEmail(m)
	}
	if b.PerformDeliveries {
		if b.DeliveryMethod == nil {
			return ErrNoDeliveryMethod
		}
		if err := b.DeliveryMethod.Deliver(m); err != nil {
			if b.RaiseDeliveryErrors {
				return err
			}
		}
	}
	for _, o := range b.observers {
		o.DeliveredEmail(m)
	}
	return nil
}

// recognizedDefault lists the default keys that map to message fields rather
// than to raw headers.
var recognizedDefault = map[string]bool{
	"from": true, "to": true, "cc": true, "bcc": true,
	"reply_to": true, "subject": true, "content_type": true,
}

// defaultHeaders returns the default entries that are plain header names.
func (b *Base) defaultHeaders() map[string]string {
	out := map[string]string{}
	for k, v := range b.Defaults {
		if !recognizedDefault[k] {
			out[k] = v
		}
	}
	return out
}
