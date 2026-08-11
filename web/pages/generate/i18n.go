package generatepage

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Translator is the minimal string-lookup surface this package needs.
// TODOS.md D6-11 requires every user-visible string on this page to come
// from the `generate.*` i18n namespace, but this package must not import
// web/i18n (owned by another concurrent agent — see doc.go). Whoever wires
// the real catalogue in satisfies this interface and passes it in via
// Deps.I18n; until then fallbackTranslator keeps the page functional.
//
// key is a dotted `generate.*` key named for meaning, not for the English
// text (TODOS.md D6-03), e.g. "generate.rail.runNow", never
// "generate.rail.runNowButtonLabel". args are positional interpolation
// values — the real catalogue is expected to route these through its own
// plural/interpolation rules (TODOS.md D6-05/D6-06); fallbackTranslator
// does the simplest possible thing (fmt.Sprintf-style %v substitution)
// since it exists only so this package is not dead in the water before the
// real catalogue lands.
type Translator interface {
	T(key string, args ...any) string
}

// Formatters is the minimal locale-aware formatting surface this package
// needs (PLAN.md §12.6: "dates, times and currency formatted by
// locale-aware formatters, never fmt.Sprintf"). Real formatters are
// expected to come from the same i18n wave as Translator; fallbackFormatters
// exists for the same "not dead in the water" reason.
type Formatters interface {
	// DateTime renders t in tz (an IANA zone, e.g. "America/New_York") as a
	// locale-appropriate local date+time string. Used by the rail's "next
	// run in local time" (D2-02) and "next three runs, jittered" (D2-09).
	DateTime(t time.Time, tz string) string
	// Currency renders a USD amount (PLAN.md §12.5/§13: every cost figure).
	Currency(usd float64) string
	// RelativeTime renders how long ago t was, relative to now (rail's
	// "last build" — D2-02).
	RelativeTime(t, now time.Time) string
	// Percent renders a 0..1 fraction as "NN%". Novelty similarity is the
	// only caller: it was being interpolated raw, so the sampler showed
	// "0.8734222" where it meant "87%".
	Percent(fraction float64) string
}

// fallbackTranslator returns the key itself when no real catalogue is
// wired, deliberately mirroring D6-07's rule for the real catalogue ("a
// missing key renders the key itself... never an empty string") rather
// than inventing hardcoded English here, which would just be a second,
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

// DefaultTranslator is used until Deps.I18n is set.
var DefaultTranslator Translator = fallbackTranslator{}

// fallbackFormatters is an ISO-ish/USD formatter used until Deps.Formatters
// is set. It is intentionally locale-naive (fixed layout, "$" prefix) —
// real locale awareness is the wired-in i18n wave's job.
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

func (fallbackFormatters) Percent(fraction float64) string {
	return strconv.FormatFloat(fraction*100, 'f', 0, 64) + "%"
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

// DefaultFormatters is used until Deps.Formatters is set.
var DefaultFormatters Formatters = fallbackFormatters{}

// catalogLocale, formatCount and errorText moved to errors.go: every caller
// is `js && wasm`, and an untagged declaration is dead code in the host
// build that `staticcheck ./...` — which the pre-commit hook runs with the
// host toolchain — reports as U1000.
