// Copyright (c) the go-ruby-actionmailer/actionmailer authors
//
// SPDX-License-Identifier: BSD-3-Clause

package actionmailer

import (
	"fmt"
	"strings"
	"unicode"
)

// I18n is the subject-translation store consulted when a mailer action calls
// mail(...) without a subject (and no "subject" default is set), mirroring
// ActionMailer's default_i18n_subject. Translations are keyed
// "<locale>.<mailer_scope>.<action>.subject" — for example
// "en.user_mailer.welcome.subject" — where the mailer scope is the mailer class
// name underscored. Values may embed %{name} interpolations resolved from the
// MailOptions.SubjectVars.
//
// When no translation is found the humanised action name is used (the Rails
// default: "welcome" -> "Welcome").
type I18n struct {
	// Locale is the active locale (default "en" when empty).
	Locale string
	// Translations maps a full dotted key to its (possibly interpolated) value.
	Translations map[string]string
}

// NewI18n returns an empty [I18n] store for locale (defaulting to "en").
func NewI18n(locale string) *I18n {
	if locale == "" {
		locale = "en"
	}
	return &I18n{Locale: locale, Translations: map[string]string{}}
}

// Set registers translation value for a dotted key (without the leading locale
// segment, which is prefixed automatically) and returns the store for chaining:
//
//	i18n.Set("user_mailer.welcome.subject", "Welcome, %{name}!")
func (i *I18n) Set(key, value string) *I18n {
	i.Translations[i.locale()+"."+key] = value
	return i
}

// locale returns the active locale, defaulting to "en".
func (i *I18n) locale() string {
	if i.Locale == "" {
		return "en"
	}
	return i.Locale
}

// subject resolves the subject for a mailer scope + action, interpolating vars,
// or returns ("", false) when no translation exists.
func (i *I18n) subject(scope, action string, vars map[string]any) (string, bool) {
	key := i.locale() + "." + scope + "." + action + ".subject"
	tmpl, ok := i.Translations[key]
	if !ok {
		return "", false
	}
	return interpolate(tmpl, vars), true
}

// i18nSubject resolves the subject for an action via the Base's I18n store,
// falling back to the humanised action name (mirroring
// ActionMailer#default_i18n_subject). It returns "" only when no I18n store is
// configured, so composition without i18n keeps the prior "no subject" behaviour.
func (b *Base) i18nSubject(action string, vars map[string]any) string {
	if b.I18n == nil {
		return ""
	}
	if s, ok := b.I18n.subject(mailerScope(b.Name), action, vars); ok {
		return s
	}
	return humanize(action)
}

// interpolate replaces %{name} placeholders in tmpl with the string form of the
// matching value in vars (mirroring Ruby I18n's %{} interpolation). An unknown
// placeholder is left verbatim.
func interpolate(tmpl string, vars map[string]any) string {
	if !strings.Contains(tmpl, "%{") {
		return tmpl
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '%' && i+1 < len(tmpl) && tmpl[i+1] == '{' {
			if end := strings.IndexByte(tmpl[i+2:], '}'); end >= 0 {
				name := tmpl[i+2 : i+2+end]
				if v, ok := vars[name]; ok {
					b.WriteString(fmt.Sprint(v))
				} else {
					b.WriteString(tmpl[i : i+2+end+1])
				}
				i += 2 + end
				continue
			}
		}
		b.WriteByte(tmpl[i])
	}
	return b.String()
}

// mailerScope underscores a mailer class name into its i18n scope, e.g.
// "UserMailer" -> "user_mailer" (ActiveSupport's underscore for the simple
// CamelCase case).
func mailerScope(name string) string {
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// humanize turns an action name into a capitalised, space-separated phrase, the
// Rails default subject: "welcome" -> "Welcome", "order_shipped" -> "Order shipped".
func humanize(action string) string {
	if action == "" {
		return ""
	}
	s := strings.ReplaceAll(action, "_", " ")
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
