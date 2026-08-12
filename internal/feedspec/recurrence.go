package feedspec

import (
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
)

// recurrence.go is the recipe-file representation of a structured schedule.
//
// # Why this is not just internal/schedule.Recurrence
//
// A recipe is exported as TOML and read by humans (settings.data.recipe.*),
// so its schedule has to survive a round trip through text and be legible
// when it gets there. schedule.Recurrence holds a *time.Location and a
// precomputed cron engine — neither serialises, and neither should be in a
// file someone edits by hand.
//
// So this is the written-down form: strings where a human wants words
// ("weekly", "thursday"), numbers where a human wants numbers, and a plain
// date for the anchor. Resolve converts it to the engine form, which is where
// validation of the combination actually happens.
//
// The duplication is deliberate and small. The alternative — putting TOML
// tags on the engine type — would drag serialisation concerns into the
// package whose only job is being correct about time.
type Recurrence struct {
	// Frequency is "daily", "weekly" or "monthly". See schedule.Frequency for
	// why there is no "yearly": the editor offers years and multiplies by 12.
	Frequency string `toml:"frequency" json:"frequency"`

	// Interval is how many of Frequency between firings: 1 = every,
	// 2 = every other, 3 = every third.
	Interval int `toml:"interval" json:"interval"`

	// Weekdays are lowercase English day names ("monday"). Used by weekly
	// schedules, and by monthly ones that count a position ("the second
	// tuesday"), which take exactly one.
	Weekdays []string `toml:"weekdays,omitempty" json:"weekdays,omitempty"`

	// MonthDay is 1-31, or -1 for the last day of the month.
	MonthDay int `toml:"month_day,omitempty" json:"month_day,omitempty"`

	// SetPosition is 1-4 for the first..fourth weekday of the month, or -1
	// for the last. Zero means "use MonthDay".
	SetPosition int `toml:"set_position,omitempty" json:"set_position,omitempty"`

	// Hour and Minute are local wall-clock time in the spec's Timezone.
	Hour   int `toml:"hour" json:"hour"`
	Minute int `toml:"minute" json:"minute"`

	// Anchor is a plain local date, "2006-01-02", giving the interval its
	// phase. Empty means the epoch — see schedule.Recurrence.Anchor on why
	// that default is deterministic rather than "today".
	Anchor string `toml:"anchor,omitempty" json:"anchor,omitempty"`
}

// AnchorLayout is the date format Anchor uses: a plain local date, no time
// and no zone. The time of day lives in Hour/Minute and the zone in the
// spec's Timezone; putting either in the anchor would give a schedule two
// places to disagree with itself.
const AnchorLayout = "2006-01-02"

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

var frequencyNames = map[string]schedule.Frequency{
	"daily": schedule.FreqDaily, "weekly": schedule.FreqWeekly, "monthly": schedule.FreqMonthly,
}

// WeekdayName renders a time.Weekday in the form this file stores.
func WeekdayName(d time.Weekday) string { return strings.ToLower(d.String()) }

// Resolve converts the written form into the engine form, in tz, validating
// the whole combination.
//
// Every error here is a schedule that would never fire, or would fire on days
// the operator did not ask for. Both are worse than a refusal at save time:
// the feed looks configured either way, and only the calendar knows the
// difference.
func (r Recurrence) Resolve(tz string) (schedule.Recurrence, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return schedule.Recurrence{}, fmt.Errorf("feedspec: timezone %q: %w", tz, err)
	}

	freq, ok := frequencyNames[strings.ToLower(strings.TrimSpace(r.Frequency))]
	if !ok {
		return schedule.Recurrence{}, fmt.Errorf("feedspec: unknown frequency %q (want daily, weekly or monthly)", r.Frequency)
	}

	days := make([]time.Weekday, 0, len(r.Weekdays))
	for _, name := range r.Weekdays {
		d, ok := weekdayNames[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			return schedule.Recurrence{}, fmt.Errorf("feedspec: unknown weekday %q", name)
		}
		days = append(days, d)
	}

	var anchor time.Time
	if s := strings.TrimSpace(r.Anchor); s != "" {
		// Parsed IN the schedule's own zone, not UTC: an anchor is a local
		// calendar date, and parsing "2026-08-06" as UTC then viewing it in
		// New York yields August 5th, which silently shifts the phase of
		// every fortnightly schedule by a day.
		anchor, err = time.ParseInLocation(AnchorLayout, s, loc)
		if err != nil {
			return schedule.Recurrence{}, fmt.Errorf("feedspec: anchor %q must be a date like 2026-08-06: %w", r.Anchor, err)
		}
	}

	return schedule.NewRecurrence(schedule.Recurrence{
		Freq:     freq,
		Interval: r.Interval,
		Weekdays: days,
		MonthDay: r.MonthDay,
		SetPos:   r.SetPosition,
		Hour:     r.Hour,
		Minute:   r.Minute,
		Anchor:   anchor,
		Loc:      loc,
	})
}

// Firing returns the schedule this spec actually runs on: the structured
// recurrence when there is one, the cron expression otherwise.
//
// This is the single place the precedence is decided, so the scheduler, the
// editor preview, `aff doctor` and the CLI cannot each answer it differently
// — a feed that the scheduler runs weekly while the UI previews it daily is a
// bug nobody would think to look for.
func (s Spec) Firing() (schedule.Firing, error) {
	if s.Recurrence != nil {
		return s.Recurrence.Resolve(s.Timezone)
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("feedspec: timezone %q: %w", s.Timezone, err)
	}
	return schedule.Parse(s.Cron, loc)
}
