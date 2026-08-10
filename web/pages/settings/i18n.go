package settings

import (
	"fmt"
	"strings"
	"time"
)

// Translator is the minimal string-lookup surface this package needs
// (doc.go: "must not import web/i18n"). key is a dotted `settings.*` key
// named for meaning, not for the English text (TODOS.md D6-03), e.g.
// "settings.security.changePassword.title", never
// "settings.security.changePasswordHeading". args are positional
// interpolation values; the real catalogue (wired in by whoever owns
// web/i18n) is expected to route these through its own interpolation and
// plural rules (TODOS.md D6-05/D6-06) — fallbackTranslator does the
// simplest possible thing so this package is not dead in the water before
// that wave lands.
type Translator interface {
	T(key string, args ...any) string
}

// Formatters is the minimal locale-aware formatting surface this package
// needs (PLAN.md §12.6: "dates, times and currency formatted by
// locale-aware formatters, never fmt.Sprintf"). Byte sizes and durations
// are not literally named in §12.6's list but are the same class of
// value (a raw int64/time.Duration a human reads as a magnitude, not as a
// token) so they go through the same seam rather than a local
// fmt.Sprintf("%d bytes", n).
type Formatters interface {
	// DateTime renders t in tz (an IANA zone) as a locale-appropriate
	// local date+time string. Used by "active sessions ... last-seen"
	// (D4-04) and "last successful run per feed" (D4-11, see doc.go
	// assumption #1).
	DateTime(t time.Time, tz string) string
	// Currency renders a USD amount (price-table entries, spend
	// ceilings — PLAN.md §12.5/§13).
	Currency(usd float64) string
	// RelativeTime renders how long ago t was, relative to now (session
	// last-seen, "last build" on the About panel).
	RelativeTime(t, now time.Time) string
	// ByteSize renders n bytes as a human-scaled string (Data section's
	// "DB size" — D4-10). Implementations are expected to still choose
	// locale-appropriate digit grouping via their own FormatNumber, not
	// this interface's job to specify.
	ByteSize(n int64) string
}

// fallbackTranslator returns the key itself (optionally with a
// parenthesized dump of args) when no real catalogue is wired,
// deliberately mirroring D6-07's rule for the real catalogue ("a missing
// key renders the key itself... never an empty string") rather than
// inventing hardcoded English here, which would just be a second,
// competing catalogue.
type fallbackTranslator struct{}

func (fallbackTranslator) T(key string, args ...any) string {
	if len(args) == 0 {
		return key
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}
	return key + "(" + strings.Join(parts, ", ") + ")"
}

// DefaultTranslator is used until Deps.I18n is set (js build) or directly
// by host tests that only care about the pure logic, not real copy.
var DefaultTranslator Translator = fallbackTranslator{}

// fallbackFormatters is an ISO-ish/USD/binary-prefix formatter used until
// Deps.Formatters is set. It is intentionally locale-naive — real locale
// awareness is the wired-in i18n wave's job — but it is NOT
// fmt.Sprintf-on-a-raw-value at every call site, which is the actual
// property PLAN.md §12.6 cares about: one seam, swappable in one place.
type fallbackFormatters struct{}

func (fallbackFormatters) DateTime(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02 15:04 MST")
}

func (fallbackFormatters) Currency(usd float64) string {
	return fmt.Sprintf("$%.4f", usd)
}

func (fallbackFormatters) RelativeTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "moments ago"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func (fallbackFormatters) ByteSize(n int64) string {
	return FormatByteSize(n, func(v float64) string {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
	})
}

// DefaultFormatters is used until Deps.Formatters is set.
var DefaultFormatters Formatters = fallbackFormatters{}
