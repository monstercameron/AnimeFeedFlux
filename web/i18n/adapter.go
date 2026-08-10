package i18n

import (
	"strconv"
	"time"

	gwci18n "github.com/monstercameron/GoWebComponents/v5/i18n"
)

// NewLabelResolver returns a function satisfying web/ui's T signature —
// `type T func(key string, args ...any) string` (web/ui/labels.go) —
// without this package importing web/ui: an unnamed func literal with an
// identical underlying type is assignable to a named func type, so no
// import is needed for the two packages to agree on the contract. web/ui's
// primitives (Button, Input, Modal, Confirm, StatePanel, Kebab, ...) only
// ever render common.* keys (keys_common.go: action.*, state.*, confirm.*,
// kebab.*), so this adapter is fixed to NSCommon rather than taking a
// namespace argument — the whole point of web/ui.T's narrow signature is
// that its callers don't carry a namespace around.
//
// Positional args are converted to gwci18n.Arguments using the "arg1",
// "arg2", ... (1-indexed) naming convention documented on keys_common.go's
// primitive-key block, since ui.T's signature is positional (...any) while
// gwci18n.Message templates interpolate named {placeholder}s and something
// has to bridge the two exactly once. Every common.* Message that accepts
// arguments (confirm.typePhrase, kebab.actionsFor) names its placeholder
// accordingly.
//
// Call it once per Bundle (typically alongside NewBundle) and pass the
// result as ButtonProps.T / ModalProps.T / etc.:
//
//	b := i18n.NewBundle()
//	t := i18n.NewLabelResolver(b)
//	ui.Button(ui.ButtonProps{T: t, LabelKey: i18n.KeyActionSave})
func NewLabelResolver(b *gwci18n.Bundle) func(key string, args ...any) string {
	return func(key string, args ...any) string {
		return b.Translate(DefaultLocale, NSCommon, key, positionalArguments(args), DefaultLocale)
	}
}

// positionalArguments converts a positional (...any) arg list into
// gwci18n.Arguments keyed "arg1", "arg2", ... in call order. Returns nil
// (not an empty map) for zero args so interpolateTemplate's fast path
// (len(args)==0 → return the template unchanged) still applies.
func positionalArguments(args []any) gwci18n.Arguments {
	if len(args) == 0 {
		return nil
	}
	out := make(gwci18n.Arguments, len(args))
	for i, a := range args {
		out["arg"+strconv.Itoa(i+1)] = a
	}
	return out
}

// --- Locale-aware formatting helpers (D6-08, PLAN.md §12.6: "dates, times
// and currency formatted by locale-aware formatters, never fmt.Sprintf") ---
//
// generate/history/settings each declare their own narrow Formatters
// interface (e.g. web/pages/generate/i18n.go's DateTime/Currency/
// RelativeTime) specifically so they don't need to import this package
// (see each package's doc.go). The functions below are what a later wiring
// wave adapts INTO those interfaces — this package does not implement
// those interfaces itself, since doing so would require importing the
// three packages this wave does not own.

// FormatDateTime renders t in the IANA zone named by tz (falling back to
// UTC for an empty or unparseable tz — never panicking on a bad zone name)
// as a locale-aware date plus a 24-hour clock time. gwci18n's own
// FormatDate formats only the date component (no time-of-day formatter
// exists in the v5 i18n package as of this writing), so the clock portion
// here is plain time.Time.Format — it does not vary by locale the way the
// date prefix does, which is an honest gap, not a fmt.Sprintf smuggled in
// for the whole string.
func FormatDateTime(t time.Time, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.UTC
	}
	lt := t.In(loc)
	datePart := gwci18n.FormatDate(DefaultLocale, lt, gwci18n.DateOptions{Style: gwci18n.DateStyleMedium})
	return datePart + " " + lt.Format("15:04") + " " + lt.Format("MST")
}

// FormatRelativeTime renders how long ago t was relative to now (e.g. "3
// hours ago", "in 2 minutes") via gwci18n's own FormatRelativeTime, which
// already carries plural rules per locale family.
func FormatRelativeTime(t, now time.Time) string {
	return gwci18n.FormatRelativeTime(DefaultLocale, t, now)
}

// FormatCurrencyUSD renders a USD amount with locale-aware digit grouping.
// Fixed at 4 fractional digits: PLAN.md §12.5/§13 cost figures are
// per-token USD amounts small enough that 2-digit rounding would display
// "$0.00" for real, nonzero spend.
func FormatCurrencyUSD(usd float64) string {
	return "$" + gwci18n.FormatNumber(DefaultLocale, usd, gwci18n.NumberOptions{MaximumFractionDigits: 4})
}

// FormatCount renders an integer-valued count with locale-aware digit
// grouping (feed counts, item counts, token counts).
func FormatCount(n int64) string {
	return gwci18n.FormatNumber(DefaultLocale, float64(n), gwci18n.NumberOptions{MaximumFractionDigits: 0})
}

// byteUnits mirrors web/pages/settings/format.go's ByteUnit scale
// (bytes/KB/MB/GB/TB, 1024-based) independently rather than importing that
// package — web/i18n does not depend on the page packages it hands keys
// to, in either direction (see this file's package doc comment above).
var byteUnits = [...]string{"B", "KB", "MB", "GB", "TB"}

// FormatByteSize renders n bytes as a human-scaled, locale-aware string
// (e.g. "1.46 GB"). Negative n is clamped to zero. Unit abbreviations are
// deliberately NOT translated (TODOS.md D6-19: "identifiers and operator
// surface, not prose" — same category as slugs and cron expressions), only
// the numeric magnitude goes through FormatNumber.
func FormatByteSize(n int64) string {
	if n < 0 {
		n = 0
	}
	value := float64(n)
	unit := 0
	for value >= 1024 && unit < len(byteUnits)-1 {
		value /= 1024
		unit++
	}
	digits := 2
	if unit == 0 { // whole bytes never carry fractional digits
		digits = 0
	}
	return gwci18n.FormatNumber(DefaultLocale, value, gwci18n.NumberOptions{MaximumFractionDigits: digits}) + " " + byteUnits[unit]
}

// FormatPercent renders a 0..1 fraction as a locale-aware "NN%" (e.g.
// novelty thresholds, similarity scores in generate.sampler.novelty.*).
func FormatPercent(fraction float64, maxFractionDigits int) string {
	return gwci18n.FormatNumber(DefaultLocale, fraction*100, gwci18n.NumberOptions{MaximumFractionDigits: maxFractionDigits}) + "%"
}
