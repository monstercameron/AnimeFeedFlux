package settings

import "time"

// Byte-size scaling (Data section's DB size, D4-10) and duration
// breakdown (About section's uptime, D4-11) are pure host-testable
// logic; only the digit-grouping and unit-word choice are locale work
// (left to Formatters/Translator — PLAN.md §12.6: "never fmt.Sprintf").

// ByteUnit names a binary (1024-based) size scale. SQLite reports its own
// size in bytes; DB size at this project's scale (PLAN.md §14.4) never
// approaches petabytes, so the scale stops at TB.
type ByteUnit int

const (
	UnitBytes ByteUnit = iota
	UnitKB
	UnitMB
	UnitGB
	UnitTB
)

// UnitSuffix returns the IEC-ish ASCII abbreviation for unit. Unit
// abbreviations are technical/operator surface, not prose — same
// category TODOS.md D6-19 names for slugs and cron expressions — so this
// is deliberately not routed through Translator; every locale still
// reads "MB".
func (u ByteUnit) UnitSuffix() string {
	switch u {
	case UnitKB:
		return "KB"
	case UnitMB:
		return "MB"
	case UnitGB:
		return "GB"
	case UnitTB:
		return "TB"
	default:
		return "B"
	}
}

// ScaledBytes is n rescaled to the largest unit for which Value >= 1 (or
// UnitBytes if n < 1024).
type ScaledBytes struct {
	Value float64
	Unit  ByteUnit
}

// ScaleBytes picks the display unit for n bytes. Negative n is clamped to
// zero — a DB size can never be negative, and treating it as such is
// safer than propagating a nonsensical negative reading onto the page.
func ScaleBytes(n int64) ScaledBytes {
	if n < 0 {
		n = 0
	}
	value := float64(n)
	unit := UnitBytes
	for value >= 1024 && unit < UnitTB {
		value /= 1024
		unit++
	}
	return ScaledBytes{Value: value, Unit: unit}
}

// FormatByteSize renders n bytes using formatNumber (expected to be a
// locale-aware Formatters-backed number formatter, e.g.
// Translator/Formatters' own FormatNumber-equivalent) for the numeric
// part and ScaleBytes/UnitSuffix for the unit, so no call site
// fmt.Sprintf's a raw byte count directly.
func FormatByteSize(n int64, formatNumber func(float64) string) string {
	s := ScaleBytes(n)
	return formatNumber(s.Value) + " " + s.Unit.UnitSuffix()
}

// DurationParts is an uptime broken into whole days/hours/minutes, the
// shape the About panel's "uptime" (D4-11) renders through locale-plural
// keys ("settings.about.uptime.days" etc.) rather than a single
// fmt.Sprintf("%dd %dh %dm", ...) string.
type DurationParts struct {
	Days    int
	Hours   int
	Minutes int
}

// BreakDuration decomposes d (clamped to zero if negative — a start time
// in the future, e.g. clock skew, must not render a negative uptime)
// into whole days/hours/minutes, discarding sub-minute precision:
// operators reading "uptime" do not need seconds.
func BreakDuration(d time.Duration) DurationParts {
	if d < 0 {
		d = 0
	}
	totalMinutes := int64(d / time.Minute)
	days := totalMinutes / (24 * 60)
	rem := totalMinutes % (24 * 60)
	hours := rem / 60
	minutes := rem % 60
	return DurationParts{Days: int(days), Hours: int(hours), Minutes: int(minutes)}
}

// Uptime is a thin, named wrapper over now.Sub(startedAt) so call sites
// read "Uptime(startedAt, now)" rather than repeating the subtraction
// (and its clamp-to-zero requirement) at each one.
func Uptime(startedAt, now time.Time) time.Duration {
	d := now.Sub(startedAt)
	if d < 0 {
		return 0
	}
	return d
}

// VacuumDurationEstimate is a coarse bucket for how long Vacuum's exclusive
// lock will likely be held, derived from the database's size BEFORE the
// call (D4-10: "tell the user it will block, and roughly for how long").
// There is no cheap way to predict it precisely — internal/store.Vacuum's
// own doc comment gives the same rough budget this buckets against ("a few
// seconds per 100 MB... a multi-GB database can mean a stall measured in
// tens of seconds to low minutes") — so this is a warning label, not an
// ETA countdown.
type VacuumDurationEstimate int

const (
	// VacuumEstimateBrief: under 100MB, a few seconds.
	VacuumEstimateBrief VacuumDurationEstimate = iota
	// VacuumEstimateModerate: 100MB up to 1GB, tens of seconds.
	VacuumEstimateModerate
	// VacuumEstimateLong: 1GB or more, minutes.
	VacuumEstimateLong
)

// EstimateVacuumDuration buckets sizeBytes (the database size reported
// BEFORE vacuuming, e.g. SystemServiceStatsResponse.DbSizeBytes) into a
// VacuumDurationEstimate per store.Vacuum's documented rough budget.
// Negative sizes clamp to zero, same convention as ScaleBytes/BreakDuration.
func EstimateVacuumDuration(sizeBytes int64) VacuumDurationEstimate {
	if sizeBytes < 0 {
		sizeBytes = 0
	}
	const mb = 1024 * 1024
	switch {
	case sizeBytes < 100*mb:
		return VacuumEstimateBrief
	case sizeBytes < 1024*mb:
		return VacuumEstimateModerate
	default:
		return VacuumEstimateLong
	}
}

// VacuumDurationParts breaks a completed Vacuum call's ACTUAL measured
// duration (SystemServiceVacuumResponse.DurationMs — the real elapsed
// time, not a prediction, per store.Vacuum's doc comment) into whole
// minutes and remaining seconds. Separate from DurationParts/BreakDuration
// above: uptime's day/hour/minute breakdown discards sub-minute precision
// on purpose, but a vacuum run is typically itself sub-minute to
// low-minutes, so seconds are the unit that matters here.
type VacuumDurationParts struct {
	Minutes int
	Seconds int
}

// BreakVacuumDuration decomposes d (clamped to zero if negative) into whole
// minutes and remaining seconds.
func BreakVacuumDuration(d time.Duration) VacuumDurationParts {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d / time.Second)
	return VacuumDurationParts{Minutes: int(totalSeconds / 60), Seconds: int(totalSeconds % 60)}
}
