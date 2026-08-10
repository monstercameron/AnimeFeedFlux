package settings

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Publishing field validation. PLAN.md §12.5: Publishing's base URL, TTL,
// and og:image "land in every channel element of every feed", so they are
// "validated on save (absolute URL, correct scheme)". This is the client
// convenience copy of that gate — SystemService.UpdateSettings is the
// real one (PLAN.md §7's "the UI's copy is a convenience, never the
// gate", applied here to settings fields rather than feed fields).

// ErrURLEmpty, ErrURLNotAbsolute, and ErrURLBadScheme name the three ways
// ValidateAbsoluteURL can fail, so callers (and tests) can distinguish
// them without parsing an error string.
var (
	ErrURLEmpty       = errors.New("url is empty")
	ErrURLNotAbsolute = errors.New("url is not absolute")
	ErrURLBadScheme   = errors.New("url scheme is not allowed")
)

// ValidateAbsoluteURL reports whether raw is an absolute URL (has both a
// scheme and a host) whose scheme is one of allowedSchemes. Publishing's
// base URL and default og:image both call this with {"http", "https"} —
// a relative path or a bare host without a scheme is rejected the same
// way AFF_PUBLIC_BASE_URL's own boot-time check does (PLAN.md §16: "is
// absolute with a scheme — it is baked into guids"), because a Publishing
// base URL is the same value, just editable after boot instead of only
// at it.
func ValidateAbsoluteURL(raw string, allowedSchemes ...string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ErrURLEmpty
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ErrURLNotAbsolute
	}
	if u.Scheme == "" || u.Host == "" {
		return ErrURLNotAbsolute
	}
	if len(allowedSchemes) == 0 {
		return nil
	}
	for _, s := range allowedSchemes {
		if strings.EqualFold(u.Scheme, s) {
			return nil
		}
	}
	return ErrURLBadScheme
}

// PublishingURLSchemes is the scheme allowlist Publishing's base URL and
// default og:image both validate against. A feed URL or an OG image URL
// that is not fetchable over plain HTTP or HTTPS cannot do its job in an
// RSS reader or an unfurl.
var PublishingURLSchemes = []string{"http", "https"}

// Password policy — display and client-side length convenience check
// only (PLAN.md §4/§4 SEC-11..16, TODOS.md's "no composition rules"
// instruction). The compromised-password blocklist (SEC-15) is a
// server-side check this package cannot and must not attempt to
// duplicate — a client-side "not breached" check would either need the
// same offline blocklist shipped to the browser (defeats the purpose of
// having it be a maintained server-side list) or a third-party API call
// PLAN.md §4/SEC-16 explicitly rejects ("no k-anonymity API call at
// login"). ValidatePasswordLength is therefore the only client-side gate;
// UpdateSettings-equivalent server calls (ChangePassword) are the real
// one.
const (
	PasswordMinLength = 15
	PasswordMaxLength = 128
)

var (
	ErrPasswordTooShort = errors.New("password shorter than minimum length")
	ErrPasswordTooLong  = errors.New("password longer than maximum length")
)

// ValidatePasswordLength checks only length, in runes (not bytes — a
// multi-byte passphrase must not be penalized for its UTF-8 encoding
// being longer than its character count; PLAN.md §4: "spaces and Unicode
// allowed"). It deliberately does NOT check composition (no symbol rule,
// no digit rule) — TODOS.md's task brief: "must not reintroduce 'must
// contain a symbol'... that rule was removed because it was actively
// selecting weaker passwords."
func ValidatePasswordLength(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < PasswordMinLength {
		return ErrPasswordTooShort
	}
	if n > PasswordMaxLength {
		return ErrPasswordTooLong
	}
	return nil
}

// PasswordGuidanceArgs returns the interpolation args for the single
// password-guidance string this page may show next to the new-password
// field: length bounds and "not previously breached", nothing else. Kept
// as a pure function (rather than embedding the numbers in an i18n
// literal) so a test can assert the two numbers shown to the admin match
// PasswordMinLength/PasswordMaxLength rather than drifting from them.
func PasswordGuidanceArgs() map[string]any {
	return map[string]any{
		"min": PasswordMinLength,
		"max": PasswordMaxLength,
	}
}
