package schedule

import (
	"errors"
	"fmt"
	"time"
)

// recurrence.go is the schedule model cron cannot express.
//
// # Why cron is not enough, stated concretely
//
// Cron matches calendar FIELDS. It has no concept of an interval, so a whole
// family of ordinary schedules simply cannot be written down in it:
//
//   - "every other Thursday" — `*/2` in the day-of-week field means "every
//     second weekday NUMBER" (Sun, Tue, Thu, Sat), not "every second week".
//     There is no cron expression for a fortnight.
//   - "every 3 weeks" — same reason. Weeks are not a cron field at all.
//   - "every 6 months" — expressible only by luck, as an explicit month list
//     (`1,7`), and only when the interval divides 12 evenly.
//   - "the second Tuesday of the month" — needs nth-weekday, which standard
//     cron lacks (some dialects add `#`; this one does not).
//
// What all of those need and cron has nowhere to put is a PHASE: given "every
// other Thursday", which Thursdays? That question has no answer without a
// reference date, so Recurrence carries one (Anchor).
//
// # Shape borrowed from RFC 5545 (iCalendar RRULE)
//
// Deliberately a constrained subset of the recurrence rule every calendar
// application already implements: FREQ + INTERVAL + BYDAY + BYMONTHDAY +
// BYSETPOS. Not because interoperability is a goal here, but because the edge
// cases (what "the 31st" means in February, how nth-weekday interacts with
// interval) were settled by that spec decades ago, and inventing a fourth
// dialect of recurrence would mean rediscovering them.
//
// # DST correctness is INHERITED, not reimplemented
//
// Getting DST right is the entire reason internal/schedule is hand-written
// rather than pulled off a shelf, and Schedule.Next's doc comment records
// exactly how: advance by real elapsed time, fire at the first valid instant
// when the target hour falls in the spring-forward gap, and use the canonical
// (earlier) instant of an ambiguous fall-back hour so a run fires once rather
// than twice.
//
// Recurrence therefore does NOT compute instants itself. It splits the
// problem in two: a time-of-day engine, which is a Schedule pinned to one
// hour and minute and already DST-correct, and a pure DATE predicate that
// asks "is this local date one of the firing dates?". Re-deriving the DST
// handling for a second code path is precisely how one of the two ends up
// subtly wrong, and only twice a year.

// Frequency is the unit Interval counts in.
//
// There is no YEARLY. A year is twelve months, and folding it into Monthly
// removes a whole set of cases (What is the yearly-with-BYSETPOS rule? What
// does interval-2 yearly do to February 29?) in exchange for the caller
// multiplying by twelve. The UI still offers "years" as a unit; it converts.
type Frequency int

const (
	// FreqDaily fires every Interval days.
	FreqDaily Frequency = iota + 1
	// FreqWeekly fires on the selected Weekdays, in every Interval-th week.
	// The interval applies to the WEEK, not to each day: "every other week on
	// Monday and Thursday" fires twice in a firing week and not at all in the
	// week between.
	FreqWeekly
	// FreqMonthly fires in every Interval-th month, on either MonthDay or the
	// SetPos-th Weekday of that month.
	FreqMonthly
)

func (f Frequency) String() string {
	switch f {
	case FreqDaily:
		return "daily"
	case FreqWeekly:
		return "weekly"
	case FreqMonthly:
		return "monthly"
	default:
		return "unknown"
	}
}

// LastDayOfMonth is the MonthDay value meaning "whatever the final day of
// this month is" — 28, 29, 30 or 31 as the calendar requires.
//
// It exists because "the 31st" is a trap: a feed set to the 31st would skip
// February entirely, and every 30-day month besides. An operator who says
// "end of the month" means the end of the month.
const LastDayOfMonth = -1

// SetPosLast is the SetPos value meaning "the last one in the month" — the
// last Friday, whether that is the 4th or the 5th.
const SetPosLast = -1

// Recurrence is a schedule expressed as an interval rather than a set of
// calendar fields. The zero value is not valid; construct through
// NewRecurrence, which validates.
type Recurrence struct {
	Freq     Frequency
	Interval int

	// Weekdays applies to FreqWeekly (which days of the week) and to
	// FreqMonthly when SetPos is set (which weekday the SetPos counts).
	Weekdays []time.Weekday

	// MonthDay applies to FreqMonthly when SetPos is zero: 1-31, or
	// LastDayOfMonth.
	MonthDay int

	// SetPos applies to FreqMonthly: 1-4 for "the first/second/third/fourth
	// <weekday>", or SetPosLast. Zero means "use MonthDay instead".
	SetPos int

	// Hour and Minute are LOCAL wall-clock time in Loc. Never UTC — "7am"
	// means 7am where the operator lives, across DST, which is the whole
	// premise of this package.
	Hour   int
	Minute int

	// Anchor is the phase reference: the date the interval counts from. For
	// Interval == 1 it is irrelevant to which dates fire and is kept only so
	// a round-trip does not lose it. For Interval > 1 it is the difference
	// between "every other Thursday" meaning this week's or next week's.
	//
	// Only the DATE part is used. A caller should set it to the first day the
	// operator wants the schedule to be live from — the feed's creation date
	// is a reasonable default and is what the UI offers.
	Anchor time.Time

	Loc *time.Location

	// timeOfDay is the inherited DST-correct engine: a cron schedule pinned
	// to exactly this hour and minute, every day. Recurrence filters its
	// output by dateFires.
	timeOfDay Schedule
}

// NewRecurrence validates and returns a Recurrence.
//
// Validation is strict rather than forgiving on purpose: every invalid
// combination here is one that would otherwise produce a feed that silently
// never runs, which is the single worst failure mode a scheduler has. A feed
// that refuses to save is visible; a feed that saves and never fires looks
// exactly like a feed that is working until someone checks the publish date.
func NewRecurrence(r Recurrence) (Recurrence, error) {
	if r.Loc == nil {
		return Recurrence{}, errors.New("schedule: timezone is required")
	}
	if r.Interval < 1 {
		return Recurrence{}, fmt.Errorf("schedule: interval must be at least 1, got %d", r.Interval)
	}
	if r.Hour < 0 || r.Hour > 23 {
		return Recurrence{}, fmt.Errorf("schedule: hour must be 0-23, got %d", r.Hour)
	}
	if r.Minute < 0 || r.Minute > 59 {
		return Recurrence{}, fmt.Errorf("schedule: minute must be 0-59, got %d", r.Minute)
	}

	switch r.Freq {
	case FreqDaily:
		// Nothing further: every Interval-th day, at Hour:Minute.

	case FreqWeekly:
		if len(r.Weekdays) == 0 {
			return Recurrence{}, errors.New("schedule: a weekly schedule needs at least one weekday")
		}
		if err := validWeekdays(r.Weekdays); err != nil {
			return Recurrence{}, err
		}

	case FreqMonthly:
		if r.SetPos != 0 {
			if r.SetPos < SetPosLast || r.SetPos == 0 || r.SetPos > 4 {
				return Recurrence{}, fmt.Errorf("schedule: set position must be 1-4 or %d (last), got %d", SetPosLast, r.SetPos)
			}
			if len(r.Weekdays) != 1 {
				return Recurrence{}, errors.New("schedule: \"the Nth <weekday>\" needs exactly one weekday")
			}
			if err := validWeekdays(r.Weekdays); err != nil {
				return Recurrence{}, err
			}
		} else {
			if r.MonthDay != LastDayOfMonth && (r.MonthDay < 1 || r.MonthDay > 31) {
				return Recurrence{}, fmt.Errorf("schedule: day of month must be 1-31 or %d (last), got %d", LastDayOfMonth, r.MonthDay)
			}
		}

	default:
		return Recurrence{}, fmt.Errorf("schedule: unknown frequency %d", r.Freq)
	}

	// The time-of-day engine: fires every day at exactly Hour:Minute, in Loc.
	// Recurrence.Next then keeps only the days dateFires accepts. Built here
	// so an invalid hour/minute fails at construction rather than at the first
	// firing, months later.
	tod, err := Parse(fmt.Sprintf("%d %d * * *", r.Minute, r.Hour), r.Loc)
	if err != nil {
		return Recurrence{}, fmt.Errorf("schedule: building time-of-day: %w", err)
	}
	r.timeOfDay = tod

	if r.Anchor.IsZero() {
		// No anchor supplied: phase from the Unix epoch, which is stable
		// across processes and restarts. Deterministic beats convenient here —
		// an anchor of time.Now() would mean the same stored recurrence fired
		// on different days depending on when the server happened to load it.
		r.Anchor = time.Unix(0, 0).In(r.Loc)
	}
	r.Anchor = r.Anchor.In(r.Loc)

	return r, nil
}

func validWeekdays(days []time.Weekday) error {
	seen := map[time.Weekday]bool{}
	for _, d := range days {
		if d < time.Sunday || d > time.Saturday {
			return fmt.Errorf("schedule: invalid weekday %d", d)
		}
		if seen[d] {
			return fmt.Errorf("schedule: weekday %s listed twice", d)
		}
		seen[d] = true
	}
	return nil
}

// Next returns the next firing strictly after `after`, or the zero Time if
// none exists within the search horizon.
//
// The horizon guard matters more here than for cron: a monthly recurrence on
// day 30 with a large interval can legitimately skip a long way, and an
// impossible combination must terminate rather than spin.
func (r Recurrence) Next(after time.Time) time.Time {
	if r.Loc == nil {
		return time.Time{}
	}

	// Walk the time-of-day engine, which yields exactly one instant per day
	// (one hour, one minute), and keep the first whose DATE qualifies. Each
	// step is a real firing time, so DST is already correct — including the
	// spring-forward case where Schedule.Next returns the first valid instant
	// after the gap rather than skipping the day.
	deadline := after.AddDate(8, 0, 0)
	t := r.timeOfDay.Next(after)
	for !t.IsZero() && !t.After(deadline) {
		if r.dateFires(t.In(r.Loc)) {
			return t
		}
		// Advance past the rest of this day. Stepping by a day of real time
		// would drift across a DST transition, so ask the engine for its next
		// firing instead — it is the thing that knows how to cross one.
		t = r.timeOfDay.Next(t)
	}
	return time.Time{}
}

// NextN returns up to n firings after `after`, stopping early if the
// recurrence runs out within the horizon.
//
// This exists for the editor's schedule preview. A recurrence builder that
// does not show you the dates it produces is a form you have to trust; one
// that does is a form you can check. "Every other Thursday" is ambiguous in
// exactly one way — which Thursdays — and a list of the next five answers it
// without the operator having to reason about anchors at all.
func (r Recurrence) NextN(after time.Time, n int) []time.Time {
	if n <= 0 {
		return nil
	}
	out := make([]time.Time, 0, n)
	t := after
	for len(out) < n {
		next := r.Next(t)
		if next.IsZero() {
			break
		}
		out = append(out, next)
		t = next
	}
	return out
}

// dateFires reports whether the local date of t is one this recurrence fires
// on. Pure calendar arithmetic — no clock, no timezone conversion beyond the
// caller's, and no notion of time of day.
func (r Recurrence) dateFires(t time.Time) bool {
	switch r.Freq {
	case FreqDaily:
		days := daysBetween(dateOnly(r.Anchor), dateOnly(t))
		return positiveModulo(days, r.Interval) == 0

	case FreqWeekly:
		if !containsWeekday(r.Weekdays, t.Weekday()) {
			return false
		}
		// Bucket by WEEK, not by day: the interval skips whole weeks, so
		// every selected weekday inside a firing week fires.
		weeks := daysBetween(weekStart(dateOnly(r.Anchor)), weekStart(dateOnly(t))) / 7
		return positiveModulo(weeks, r.Interval) == 0

	case FreqMonthly:
		months := monthsBetween(r.Anchor, t)
		if positiveModulo(months, r.Interval) != 0 {
			return false
		}
		if r.SetPos != 0 {
			return isNthWeekdayOfMonth(t, r.Weekdays[0], r.SetPos)
		}
		if r.MonthDay == LastDayOfMonth {
			return t.Day() == daysInMonth(t.Year(), t.Month())
		}
		// A day that does not exist this month simply does not fire this
		// month. That is RFC 5545's rule and it is the least surprising one:
		// "the 31st" in February means no run in February, not a run on the
		// 28th (which would be a different schedule the operator did not ask
		// for) and not an error at save time (the schedule is fine, February
		// is just short).
		return t.Day() == r.MonthDay

	default:
		return false
	}
}

// --- calendar helpers -------------------------------------------------

// dateOnly strips the time of day, keeping the location.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// daysBetween counts whole days from a to b, both midnight-local.
//
// Computed from the UTC-normalised calendar date, NOT by subtracting the two
// instants and dividing by 24h: a day containing a DST transition is 23 or 25
// hours long, so the division answer is off by one for any span crossing one.
// That error would make "every other Thursday" slip a week each spring.
func daysBetween(a, b time.Time) int {
	ua := time.Date(a.Year(), a.Month(), a.Day(), 0, 0, 0, 0, time.UTC)
	ub := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.UTC)
	return int(ub.Sub(ua) / (24 * time.Hour))
}

// weekStart returns the Sunday on or before t.
//
// Sunday because Go's time.Weekday numbers it zero, so the arithmetic needs no
// adjustment table. The choice is invisible to the operator: it only sets
// where week boundaries fall for interval counting, and the anchor already
// determines the phase within that.
func weekStart(t time.Time) time.Time {
	return t.AddDate(0, 0, -int(t.Weekday()))
}

func monthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

// positiveModulo keeps the interval arithmetic correct for dates BEFORE the
// anchor, where the difference is negative and Go's % would return a negative
// remainder. A schedule anchored today must still describe last month
// coherently — the editor's preview asks exactly that when an operator picks
// an anchor in the future.
func positiveModulo(a, b int) int {
	if b <= 0 {
		return 0
	}
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func containsWeekday(days []time.Weekday, d time.Weekday) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

func daysInMonth(year int, month time.Month) int {
	// Day 0 of the next month is the last day of this one.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// isNthWeekdayOfMonth reports whether t is the nth occurrence of weekday wd in
// its month, with n == SetPosLast meaning the last one.
func isNthWeekdayOfMonth(t time.Time, wd time.Weekday, n int) bool {
	if t.Weekday() != wd {
		return false
	}
	if n == SetPosLast {
		// Last if adding a week leaves the month.
		return t.AddDate(0, 0, 7).Month() != t.Month()
	}
	// The 1st..4th: which occurrence this is, counted from the 1st.
	return (t.Day()-1)/7+1 == n
}

// Firing is the one thing the scheduler needs from a schedule: when next.
//
// Both Schedule (cron) and Recurrence satisfy it, which is what lets a feed
// carry either without the runner, the preview, or the doctor knowing which.
// Deliberately one method: everything else about a schedule — how it was
// written down, whether it has an anchor, what it reads like in English — is
// the editor's problem, not the runner's.
type Firing interface {
	// Next returns the next firing strictly after `after`, or the zero Time
	// if there is none within a reasonable horizon.
	Next(after time.Time) time.Time
}

var (
	_ Firing = Schedule{}
	_ Firing = Recurrence{}
)
