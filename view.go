// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"errors"

	"github.com/go-ruby-actionview/actionview"
)

// ErrNoEval is returned by a [View] when it must evaluate a template but no
// [EvalTemplate] seam is configured.
var ErrNoEval = errors.New("actionmailer: no EvalTemplate seam configured")

// EvalTemplate is the template-evaluation seam: it compiles+runs a template
// source with the given locals and returns the rendered output. In Rails this is
// Action View handing the .erb to Erubis and running it; here a host wires it to
// go-ruby-erb + a Ruby runtime (rbgo), keeping this package CGO- and
// runtime-free. The [View] owns everything around it — template lookup, locale
// fallback, implicit text/html pairing, and layout wrapping.
type EvalTemplate func(source string, locals map[string]any) (string, error)

// TemplateStore holds mailer template, layout and partial sources, keyed for
// lookup with locale fallback — the in-memory analogue of Action View's
// resolver for the mail case. Register sources with [TemplateStore.Add],
// [TemplateStore.AddLocalized], [TemplateStore.AddLayout] and
// [TemplateStore.AddPartial]; lookups first try the requested locale, then the
// locale-less entry.
type TemplateStore struct {
	templates map[tmplKey]string
	layouts   map[tmplKey]string
	partials  map[tmplKey]string
}

// tmplKey identifies a template variant. Name means different things per map:
// "<mailer>/<action>" for templates, the layout base name for layouts, and the
// partial name for partials.
type tmplKey struct {
	name   string
	format string
	locale string
}

// NewTemplateStore returns an empty [TemplateStore].
func NewTemplateStore() *TemplateStore {
	return &TemplateStore{
		templates: map[tmplKey]string{},
		layouts:   map[tmplKey]string{},
		partials:  map[tmplKey]string{},
	}
}

// Add registers a locale-less template for a mailer action + format
// (welcome.text.erb), returning the store for chaining.
func (s *TemplateStore) Add(mailer, action, format, source string) *TemplateStore {
	return s.AddLocalized(mailer, action, format, "", source)
}

// AddLocalized registers a template for a specific locale
// (welcome.en.text.erb). A locale of "" is the fallback variant.
func (s *TemplateStore) AddLocalized(mailer, action, format, locale, source string) *TemplateStore {
	s.templates[tmplKey{mailer + "/" + action, format, locale}] = source
	return s
}

// AddLayout registers a layout for a base name + format (layouts/mailer.html.erb),
// with an optional locale ("" for the fallback).
func (s *TemplateStore) AddLayout(name, format, locale, source string) *TemplateStore {
	s.layouts[tmplKey{name, format, locale}] = source
	return s
}

// AddPartial registers a partial by name + format (_footer.html.erb), with an
// optional locale ("" for the fallback).
func (s *TemplateStore) AddPartial(name, format, locale, source string) *TemplateStore {
	s.partials[tmplKey{name, format, locale}] = source
	return s
}

// lookup returns the template source for a mailer action + format, preferring
// the requested locale and falling back to the locale-less variant.
func (s *TemplateStore) lookup(mailer, action, format, locale string) (string, bool) {
	return resolve(s.templates, mailer+"/"+action, format, locale)
}

// layout returns the layout source for a base name + format with locale fallback.
func (s *TemplateStore) layout(name, format, locale string) (string, bool) {
	if name == "" {
		return "", false
	}
	return resolve(s.layouts, name, format, locale)
}

// partial returns the partial source for a name + format with locale fallback.
func (s *TemplateStore) partial(name, format, locale string) (string, bool) {
	return resolve(s.partials, name, format, locale)
}

// resolve does the locale-then-fallback lookup shared by templates, layouts and
// partials.
func resolve(m map[tmplKey]string, name, format, locale string) (string, bool) {
	if locale != "" {
		if src, ok := m[tmplKey{name, format, locale}]; ok {
			return src, true
		}
	}
	src, ok := m[tmplKey{name, format, ""}]
	return src, ok
}

// View resolves and renders mailer templates and layouts (with locale fallback
// and implicit text/html pairing) through a go-ruby-actionview [actionview.Context]
// and an [EvalTemplate] seam, producing a [RenderBody] to wire to
// [Base.RenderBody].
type View struct {
	// Store holds the template, layout and partial sources.
	Store *TemplateStore
	// Eval evaluates a resolved template source. Required.
	Eval EvalTemplate
	// Locale is the active locale for lookups (default "en" when empty).
	Locale string
	// Layout is the default layout base name (e.g. "mailer"); "" renders bodies
	// without a layout.
	Layout string
	// Context is an optional go-ruby-actionview context supplying the view
	// helpers/routing available inside templates. It is copied per render so its
	// RenderTemplate seam can be wired to the store; a nil Context uses a zero one.
	Context *actionview.Context
}

// RenderBody returns a [RenderBody] closure over the view: for each requested
// format it resolves the mailer's template (skipping the format with
// [ErrNoTemplate] when none exists), evaluates it through the actionview Context,
// and wraps the result in the format's layout (exposing the rendered body to the
// layout as the html-safe "yield" local) when a layout is configured.
func (v *View) RenderBody() RenderBody {
	return func(mailer, action, format string, locals map[string]any) (string, error) {
		src, ok := v.Store.lookup(mailer, action, format, v.locale())
		if !ok {
			return "", ErrNoTemplate
		}
		ctx := v.context()
		body, err := ctx.Render(actionview.RenderOptions{Inline: src, Locals: locals})
		if err != nil {
			return "", err
		}
		layoutSrc, ok := v.Store.layout(v.Layout, format, v.locale())
		if !ok {
			return body.String(), nil
		}
		wrapped, err := ctx.Render(actionview.RenderOptions{
			Inline: layoutSrc,
			Locals: mergeViewLocals(locals, map[string]any{"yield": body}),
		})
		if err != nil {
			return "", err
		}
		return wrapped.String(), nil
	}
}

// locale returns the active locale, defaulting to "en".
func (v *View) locale() string {
	if v.Locale == "" {
		return "en"
	}
	return v.Locale
}

// context builds the per-render actionview context: a copy of v.Context (so the
// caller's seam is never clobbered) whose RenderTemplate resolves partials from
// the store before falling back to evaluating the identifier as an inline
// source, and errs when no Eval seam is set.
func (v *View) context() *actionview.Context {
	ctx := &actionview.Context{}
	if v.Context != nil {
		*ctx = *v.Context
	}
	ctx.RenderTemplate = func(identifier string, locals map[string]any) (string, error) {
		if v.Eval == nil {
			return "", ErrNoEval
		}
		if src, ok := v.Store.partial(identifier, v.currentFormat(locals), v.locale()); ok {
			return v.Eval(src, locals)
		}
		return v.Eval(identifier, locals)
	}
	return ctx
}

// currentFormat reports the format a partial render should resolve in, taken
// from the reserved "format" local when present, else "html" (partials are most
// commonly HTML).
func (v *View) currentFormat(locals map[string]any) string {
	if f, ok := locals["format"].(string); ok && f != "" {
		return f
	}
	return "html"
}

// mergeViewLocals returns a new map combining base and extra (extra wins),
// without mutating the caller's map.
func mergeViewLocals(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, val := range base {
		out[k] = val
	}
	for k, val := range extra {
		out[k] = val
	}
	return out
}
