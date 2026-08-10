// Package schedule computes cron firing times in an IANA timezone and adds
// deterministic per-feed jitter (PLAN.md §7, §14.3).
//
// The reason this is hand-written rather than pulled off the shelf: "daily
// trivia at 7am" means 7am *local*, and evaluating cron in UTC silently
// shifts by an hour twice a year. Getting DST right is the entire point of
// the package — a run scheduled in the skipped hour (spring forward) must
// still fire, at the next valid instant, and a run in the repeated hour
// (fall back) must fire exactly once, not twice. See Schedule.Next.
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// fieldSpec is a parsed cron field: the set of values that satisfy it, plus
// whether the original text was a bare "*" (needed for the day-of-month /
// day-of-week OR rule below — a restricted field and a wildcard field are not
// interchangeable even though both may accept the same values).
type fieldSpec struct {
	set      [64]bool // sized to cover the widest field (0-59 minutes/seconds-shaped)
	wildcard bool
}

// Schedule is a parsed 5-field cron expression bound to a timezone. The zero
// value is not valid; construct with Parse.
type Schedule struct {
	minute fieldSpec
	hour   fieldSpec
	dom    fieldSpec
	month  fieldSpec
	dow    fieldSpec
	loc    *time.Location
	expr   string
}

// Parse parses a standard 5-field cron expression (minute hour day-of-month
// month day-of-week) to be evaluated in tz. Supported syntax per field: `*`,
// integers, ranges (`1-5`), lists (`1,3,5`), and steps (`*/15`, `1-30/5`).
// Macros like `@daily` are deliberately not supported — PLAN.md only asks for
// the five numeric fields, and adding macro handling is scope the recipe
// format doesn't need.
func Parse(expr string, tz *time.Location) (Schedule, error) {
	if tz == nil {
		return Schedule{}, errors.New("schedule: timezone is required")
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("schedule: expected 5 fields (minute hour day-of-month month day-of-week), got %d in %q", len(fields), expr)
	}

	minute, err := parseField("minute", fields[0], 0, 59)
	if err != nil {
		return Schedule{}, err
	}
	hour, err := parseField("hour", fields[1], 0, 23)
	if err != nil {
		return Schedule{}, err
	}
	dom, err := parseField("day-of-month", fields[2], 1, 31)
	if err != nil {
		return Schedule{}, err
	}
	month, err := parseField("month", fields[3], 1, 12)
	if err != nil {
		return Schedule{}, err
	}
	dow, err := parseField("day-of-week", fields[4], 0, 6)
	if err != nil {
		return Schedule{}, err
	}

	return Schedule{
		minute: minute,
		hour:   hour,
		dom:    dom,
		month:  month,
		dow:    dow,
		loc:    tz,
		expr:   expr,
	}, nil
}

// parseField parses one comma-separated cron field into the set of values in
// [min, max] that satisfy it.
func parseField(name, expr string, min, max int) (fieldSpec, error) {
	var fs fieldSpec
	fs.wildcard = expr == "*"

	items := strings.Split(expr, ",")
	for _, item := range items {
		if item == "" {
			return fieldSpec{}, fmt.Errorf("schedule: %s field %q: empty item", name, expr)
		}

		base, step, err := splitStep(item)
		if err != nil {
			return fieldSpec{}, fmt.Errorf("schedule: %s field %q: %w", name, expr, err)
		}

		var lo, hi int
		switch {
		case base == "*":
			lo, hi = min, max
		case strings.Contains(base, "-"):
			parts := strings.SplitN(base, "-", 2)
			lo, err = strconv.Atoi(parts[0])
			if err != nil {
				return fieldSpec{}, fmt.Errorf("schedule: %s field %q: invalid range start %q", name, expr, parts[0])
			}
			hi, err = strconv.Atoi(parts[1])
			if err != nil {
				return fieldSpec{}, fmt.Errorf("schedule: %s field %q: invalid range end %q", name, expr, parts[1])
			}
			if lo > hi {
				return fieldSpec{}, fmt.Errorf("schedule: %s field %q: range start %d exceeds end %d", name, expr, lo, hi)
			}
		default:
			n, err := strconv.Atoi(base)
			if err != nil {
				return fieldSpec{}, fmt.Errorf("schedule: %s field %q: invalid value %q", name, expr, base)
			}
			lo, hi = n, n
		}

		if lo < min || hi > max {
			return fieldSpec{}, fmt.Errorf("schedule: %s field %q: value out of range [%d,%d]", name, expr, min, max)
		}
		for v := lo; v <= hi; v += step {
			fs.set[v] = true
		}
	}
	return fs, nil
}

// splitStep splits "base/step" into its parts, defaulting step to 1 when
// absent, and validates the step is a positive integer.
func splitStep(item string) (base string, step int, err error) {
	if i := strings.IndexByte(item, '/'); i >= 0 {
		base = item[:i]
		stepStr := item[i+1:]
		step, err = strconv.Atoi(stepStr)
		if err != nil {
			return "", 0, fmt.Errorf("invalid step %q", stepStr)
		}
		if step <= 0 {
			return "", 0, fmt.Errorf("step must be positive, got %d", step)
		}
		return base, step, nil
	}
	return item, 1, nil
}

// dayMatches applies cron's day-of-month / day-of-week rule: if either field
// is restricted (not "*"), a restricted field alone must match; if BOTH are
// restricted, a day matching either one is enough (the standard, if slightly
// surprising, OR rule — e.g. "15 * * * 5" fires on the 15th of every month
// AND every Friday, not their intersection).
func (s Schedule) dayMatches(t time.Time) bool {
	domMatch := s.dom.set[t.Day()]
	dowMatch := s.dow.set[int(t.Weekday())]
	switch {
	case s.dom.wildcard && s.dow.wildcard:
		return true
	case s.dom.wildcard:
		return dowMatch
	case s.dow.wildcard:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

// Next returns the next firing strictly after `after`, computed in the
// schedule's timezone. It advances using real elapsed time (time.Time.Add),
// never by reconstructing a wall-clock tuple with time.Date and hoping it
// lands somewhere sane — that distinction is what makes DST correct here:
//
//   - Spring forward (skipped hour): if the schedule's target hour(s) fall
//     entirely inside the gap (e.g. 2:00-3:00 doesn't exist), the run is not
//     dropped for the day — it fires at the first valid instant after the
//     gap, which is exactly the promise in PLAN.md §7.
//   - Fall back (repeated hour): a wall-clock minute in the repeated hour
//     maps to two distinct absolute instants. time.Date always resolves an
//     ambiguous tuple to the EARLIER of the two (documented Go behavior).
//     Next uses that as the canonical instant and skips any candidate that
//     isn't it, so the second pass through the same wall clock never
//     matches — one fire, not two.
//
// Returns the zero Time if no firing exists within a generous search horizon
// (guards against unschedulable expressions like day-of-month 31 in a
// month-restricted-to-February schedule, rather than looping forever).
func (s Schedule) Next(after time.Time) time.Time {
	loc := s.loc
	at := after.In(loc)
	t := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, loc).Add(time.Minute)

	deadline := after.AddDate(8, 0, 0)

	for {
		if t.After(deadline) {
			return time.Time{}
		}

		if !s.month.set[int(t.Month())] {
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			continue
		}
		if !s.hour.set[t.Hour()] {
			// Start-of-hour computed by REAL subtraction from t, not by
			// reconstructing (year,month,day,hour,0) via time.Date — the
			// latter always resolves to the canonical (earlier) instant of
			// an ambiguous hour, which would re-derive the same start every
			// time we're sitting in the second pass of a repeated hour and
			// loop forever instead of advancing past it.
			startOfHour := t.Add(-time.Duration(t.Minute()) * time.Minute)
			next := startOfHour.Add(time.Hour)
			if next.Day() == t.Day() && next.Hour() > t.Hour()+1 {
				for h := t.Hour() + 1; h < next.Hour(); h++ {
					if s.hour.set[h] {
						return next
					}
				}
			}
			t = next
			continue
		}
		canonical := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		if !t.Equal(canonical) {
			t = t.Add(time.Minute)
			continue
		}
		if !s.minute.set[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
}
