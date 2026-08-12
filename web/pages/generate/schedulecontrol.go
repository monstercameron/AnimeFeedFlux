package generatepage

import (
	"fmt"
	"strings"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// schedulecontrol.go is the pure logic behind the schedule builder: the unit
// conversions, the English readback, and the preview of when a schedule
// actually fires.
//
// No build tag, so all of it is host-testable. The rendering half
// (render_schedule.go) is the thin js-only part that turns these values into
// controls — the same split web/guard and web/appstate use, and the reason
// the awkward parts of this feature (does "every 2 years" mean 24 months?
// what does the readback say for the last Friday?) can be tested without a
// browser.
//
// # Why the editor computes the preview itself
//
// internal/schedule is ordinary Go and compiles to wasm, so the browser can
// evaluate a recurrence directly. That matters more than saving an RPC: the
// preview updates as the operator changes a control, before anything is
// saved, so "every other Thursday" stops being a phrase you have to trust and
// becomes five dates you can read. TODOS.md D2-09 wanted exactly this and
// concluded it was impossible because no RPC returned fire times; the
// conclusion was right about the RPC and wrong about needing one.

// ScheduleUnit is the unit shown in the editor's "every N ___" control.
//
// Years exist here and nowhere below: internal/schedule has no yearly
// frequency on purpose (see its Frequency doc comment), so this layer
// converts years to months. The operator gets the word they expect and the
// engine keeps its smaller set of cases.
type ScheduleUnit string

const (
	UnitDay   ScheduleUnit = "day"
	UnitWeek  ScheduleUnit = "week"
	UnitMonth ScheduleUnit = "month"
	UnitYear  ScheduleUnit = "year"
)

// ScheduleUnits is the ordered list the unit picker offers.
var ScheduleUnits = []ScheduleUnit{UnitDay, UnitWeek, UnitMonth, UnitYear}

// MonthlyMode is which of the two ways a monthly schedule picks its day.
type MonthlyMode string

const (
	// MonthlyOnDay is "on day 15" — a fixed date.
	MonthlyOnDay MonthlyMode = "day"
	// MonthlyOnWeekday is "on the second Tuesday" — a position and a weekday.
	MonthlyOnWeekday MonthlyMode = "weekday"
)

// ScheduleDraft is the editor's working state: exactly the controls on
// screen, before conversion to a feedspec.Recurrence.
//
// Separate from feedspec.Recurrence because the two do not have the same
// shape. The form always has a value for every control (an operator can flip
// from weekly to monthly and back without losing what they typed), while the
// recipe carries only the fields its frequency actually uses. Collapsing them
// would mean either the form losing state on every switch, or the recipe
// carrying meaningless leftovers into validation.
type ScheduleDraft struct {
	Unit     ScheduleUnit
	Interval int

	// Weekdays applies to weekly schedules.
	Weekdays []time.Weekday

	// Monthly controls.
	Mode         MonthlyMode
	MonthDay     int          // 1-31, or feedspec/schedule's -1 for "last day"
	SetPosition  int          // 1-4, or -1 for "last"
	MonthWeekday time.Weekday // which weekday the position counts

	Hour   int
	Minute int

	// Anchor is the "starting from" date, "2006-01-02". It is what gives
	// "every other Thursday" an answer to "which Thursdays".
	Anchor string
}

// DefaultScheduleDraft is what a new feed starts with: every day at noon,
// matching feedspec.Defaults()' own "0 12 * * *" so a feed created through
// the builder and one created from defaults behave identically.
func DefaultScheduleDraft(today time.Time) ScheduleDraft {
	return ScheduleDraft{
		Unit:         UnitDay,
		Interval:     1,
		Weekdays:     []time.Weekday{today.Weekday()},
		Mode:         MonthlyOnDay,
		MonthDay:     today.Day(),
		SetPosition:  1,
		MonthWeekday: today.Weekday(),
		Hour:         12,
		Minute:       0,
		Anchor:       today.Format(feedspec.AnchorLayout),
	}
}

// ToRecurrence converts the form state into the recipe form, keeping only the
// fields the chosen frequency uses.
func (d ScheduleDraft) ToRecurrence() feedspec.Recurrence {
	r := feedspec.Recurrence{
		Interval: d.Interval,
		Hour:     d.Hour,
		Minute:   d.Minute,
		Anchor:   d.Anchor,
	}
	if r.Interval < 1 {
		r.Interval = 1
	}

	switch d.Unit {
	case UnitWeek:
		r.Frequency = "weekly"
		for _, wd := range d.Weekdays {
			r.Weekdays = append(r.Weekdays, feedspec.WeekdayName(wd))
		}

	case UnitMonth, UnitYear:
		r.Frequency = "monthly"
		if d.Unit == UnitYear {
			// A year is twelve months. Done here rather than in the engine so
			// the engine keeps one fewer frequency to be correct about.
			r.Interval = r.Interval * 12
		}
		if d.Mode == MonthlyOnWeekday {
			r.SetPosition = d.SetPosition
			r.Weekdays = []string{feedspec.WeekdayName(d.MonthWeekday)}
		} else {
			r.MonthDay = d.MonthDay
		}

	default:
		r.Frequency = "daily"
	}
	return r
}

// FromRecurrence rebuilds form state from a saved recipe, filling the
// controls the saved frequency does not use with sensible values so switching
// frequency in the editor never lands on a blank or invalid control.
func FromRecurrence(r *feedspec.Recurrence, today time.Time) ScheduleDraft {
	d := DefaultScheduleDraft(today)
	if r == nil {
		return d
	}

	d.Interval = r.Interval
	if d.Interval < 1 {
		d.Interval = 1
	}
	d.Hour, d.Minute = r.Hour, r.Minute
	if r.Anchor != "" {
		d.Anchor = r.Anchor
	}

	switch strings.ToLower(r.Frequency) {
	case "weekly":
		d.Unit = UnitWeek
		days := parseWeekdays(r.Weekdays)
		if len(days) > 0 {
			d.Weekdays = days
		}

	case "monthly":
		// Whole years render as years: a schedule saved as "every 24 months"
		// comes back as "every 2 years", which is what the operator typed.
		if d.Interval%12 == 0 && d.Interval >= 12 {
			d.Unit = UnitYear
			d.Interval = d.Interval / 12
		} else {
			d.Unit = UnitMonth
		}
		if r.SetPosition != 0 {
			d.Mode = MonthlyOnWeekday
			d.SetPosition = r.SetPosition
			if days := parseWeekdays(r.Weekdays); len(days) == 1 {
				d.MonthWeekday = days[0]
			}
		} else {
			d.Mode = MonthlyOnDay
			if r.MonthDay != 0 {
				d.MonthDay = r.MonthDay
			}
		}

	default:
		d.Unit = UnitDay
	}
	return d
}

func parseWeekdays(names []string) []time.Weekday {
	out := make([]time.Weekday, 0, len(names))
	for _, n := range names {
		for wd := time.Sunday; wd <= time.Saturday; wd++ {
			if feedspec.WeekdayName(wd) == strings.ToLower(strings.TrimSpace(n)) {
				out = append(out, wd)
				break
			}
		}
	}
	return out
}

// PreviewRuns returns the next n firing times for a draft, or an error the
// editor can show.
//
// The preview is the whole point of the builder. "Every other Thursday" is
// ambiguous in exactly one way — which Thursdays — and no amount of label
// wording resolves it as well as showing the dates.
func (d ScheduleDraft) PreviewRuns(tz string, now time.Time, n int) ([]time.Time, error) {
	rec, err := d.ToRecurrence().Resolve(tz)
	if err != nil {
		return nil, err
	}
	return rec.NextN(now, n), nil
}

// ToggleWeekday adds or removes a weekday, keeping the list sorted so the
// readback reads "Monday and Thursday" rather than in click order.
//
// Removing the last remaining day is refused: a weekly schedule with no days
// never fires, and the editor should not be able to build one. The control
// this backs shows the day as selected and unclickable rather than letting
// the operator create a feed that silently does nothing.
func ToggleWeekday(days []time.Weekday, wd time.Weekday) []time.Weekday {
	out := make([]time.Weekday, 0, len(days)+1)
	found := false
	for _, d := range days {
		if d == wd {
			found = true
			continue
		}
		out = append(out, d)
	}
	if found {
		if len(out) == 0 {
			return days // refuse to empty the list
		}
		return out
	}
	out = append(out, wd)
	// Insertion sort by weekday number: the list is at most seven long.
	for i := len(out) - 1; i > 0 && out[i] < out[i-1]; i-- {
		out[i], out[i-1] = out[i-1], out[i]
	}
	return out
}

// --- readback -----------------------------------------------------------

// ScheduleReadback renders a draft as an English sentence.
//
// Built from catalogue keys rather than concatenated words, because a
// sentence assembled from fragments assumes English word order and falls
// apart in translation — the rule PLAN.md §12.6 states and the reason this
// takes a translator rather than returning a string it built itself.
func ScheduleReadback(t Translator, d ScheduleDraft, tz string) string {
	at := t.T("generate.editor.schedule.readback.at", formatClock(d.Hour, d.Minute), tz)

	switch d.Unit {
	case UnitWeek:
		days := readbackWeekdays(t, d.Weekdays)
		if d.Interval == 1 {
			return t.T("generate.editor.schedule.readback.weekly", days, at)
		}
		return t.T("generate.editor.schedule.readback.weeklyEvery", d.Interval, days, at)

	case UnitMonth, UnitYear:
		unitLabel := t.T("generate.editor.schedule.unit.month.plural")
		interval := d.Interval
		if d.Unit == UnitYear {
			unitLabel = t.T("generate.editor.schedule.unit.year.plural")
		}
		var day string
		if d.Mode == MonthlyOnWeekday {
			day = t.T("generate.editor.schedule.readback.nthWeekday",
				ordinalLabel(t, d.SetPosition), t.T(weekdayKey(d.MonthWeekday)))
		} else if d.MonthDay == -1 {
			day = t.T("generate.editor.schedule.readback.lastDay")
		} else {
			day = t.T("generate.editor.schedule.readback.onDay", d.MonthDay)
		}
		if interval == 1 {
			return t.T("generate.editor.schedule.readback.monthly", day, at)
		}
		return t.T("generate.editor.schedule.readback.monthlyEvery", interval, unitLabel, day, at)

	default:
		if d.Interval == 1 {
			return t.T("generate.editor.schedule.readback.daily", at)
		}
		return t.T("generate.editor.schedule.readback.dailyEvery", d.Interval, at)
	}
}

// formatClock renders 24-hour local time. Not through a locale formatter:
// this is a wall-clock setting the operator typed into two number controls,
// not an instant, and gwci18n's formatters take a time.Time.
func formatClock(hour, minute int) string {
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func readbackWeekdays(t Translator, days []time.Weekday) string {
	if len(days) == 0 {
		return ""
	}
	names := make([]string, 0, len(days))
	for _, d := range days {
		names = append(names, t.T(weekdayKey(d)))
	}
	if len(names) == 1 {
		return names[0]
	}
	// "A, B and C" — the separator and the final conjunction are catalogue
	// entries because neither is the same in every language.
	last := names[len(names)-1]
	rest := strings.Join(names[:len(names)-1], t.T("generate.editor.schedule.readback.listSeparator"))
	return t.T("generate.editor.schedule.readback.listAnd", rest, last)
}

func weekdayKey(d time.Weekday) string {
	return "generate.editor.schedule.weekday." + feedspec.WeekdayName(d)
}

func ordinalLabel(t Translator, pos int) string {
	switch pos {
	case -1:
		return t.T("generate.editor.schedule.ordinal.last")
	case 1:
		return t.T("generate.editor.schedule.ordinal.first")
	case 2:
		return t.T("generate.editor.schedule.ordinal.second")
	case 3:
		return t.T("generate.editor.schedule.ordinal.third")
	case 4:
		return t.T("generate.editor.schedule.ordinal.fourth")
	}
	return ""
}

// --- proto bridge --------------------------------------------------------
//
// The editor holds an *affv1.Feed, so the builder has to read and write the
// wire type directly. These mirror internal/rpc/recurrence_conv.go, which
// cannot be imported from here: that package pulls in the store and the whole
// server. The duplication is two maps and two loops, and the parity is
// enforced by round-trip tests on both sides rather than by sharing code that
// would drag a database driver into the browser bundle.

var protoWeekdayFor = map[time.Weekday]affv1.Weekday{
	time.Sunday:    affv1.Weekday_WEEKDAY_SUNDAY,
	time.Monday:    affv1.Weekday_WEEKDAY_MONDAY,
	time.Tuesday:   affv1.Weekday_WEEKDAY_TUESDAY,
	time.Wednesday: affv1.Weekday_WEEKDAY_WEDNESDAY,
	time.Thursday:  affv1.Weekday_WEEKDAY_THURSDAY,
	time.Friday:    affv1.Weekday_WEEKDAY_FRIDAY,
	time.Saturday:  affv1.Weekday_WEEKDAY_SATURDAY,
}

var weekdayForProto = func() map[affv1.Weekday]time.Weekday {
	out := make(map[affv1.Weekday]time.Weekday, len(protoWeekdayFor))
	for k, v := range protoWeekdayFor {
		out[v] = k
	}
	return out
}()

var protoFrequencyFor = map[string]affv1.Frequency{
	"daily":   affv1.Frequency_FREQUENCY_DAILY,
	"weekly":  affv1.Frequency_FREQUENCY_WEEKLY,
	"monthly": affv1.Frequency_FREQUENCY_MONTHLY,
}

var frequencyForProto = func() map[affv1.Frequency]string {
	out := make(map[affv1.Frequency]string, len(protoFrequencyFor))
	for k, v := range protoFrequencyFor {
		out[v] = k
	}
	return out
}()

// DraftFromProto builds editor state from a feed's stored recurrence, falling
// back to the daily default when the feed has none (an older feed still on
// cron, or a brand-new draft).
func DraftFromProto(pr *affv1.Recurrence, today time.Time) ScheduleDraft {
	if pr == nil {
		return DefaultScheduleDraft(today)
	}
	freq, ok := frequencyForProto[pr.GetFrequency()]
	if !ok {
		return DefaultScheduleDraft(today)
	}

	days := make([]string, 0, len(pr.GetWeekdays()))
	for _, d := range pr.GetWeekdays() {
		if wd, ok := weekdayForProto[d]; ok {
			days = append(days, feedspec.WeekdayName(wd))
		}
	}
	anchor := ""
	if ts := pr.GetAnchor(); ts != nil && ts.IsValid() {
		anchor = ts.AsTime().UTC().Format(feedspec.AnchorLayout)
	}

	return FromRecurrence(&feedspec.Recurrence{
		Frequency:   freq,
		Interval:    int(pr.GetInterval()),
		Weekdays:    days,
		MonthDay:    int(pr.GetMonthDay()),
		SetPosition: int(pr.GetSetPosition()),
		Hour:        int(pr.GetHour()),
		Minute:      int(pr.GetMinute()),
		Anchor:      anchor,
	}, today)
}

// ToProto converts editor state to the wire form the feed is saved with.
func (d ScheduleDraft) ToProto() *affv1.Recurrence {
	r := d.ToRecurrence()

	days := make([]affv1.Weekday, 0, len(r.Weekdays))
	for _, name := range r.Weekdays {
		for wd, pd := range protoWeekdayFor {
			if feedspec.WeekdayName(wd) == name {
				days = append(days, pd)
				break
			}
		}
	}

	var anchor *timestamppb.Timestamp
	if r.Anchor != "" {
		if t, err := time.Parse(feedspec.AnchorLayout, r.Anchor); err == nil {
			anchor = timestamppb.New(t)
		}
	}

	return &affv1.Recurrence{
		Frequency:   protoFrequencyFor[r.Frequency],
		Interval:    int32(r.Interval),
		Weekdays:    days,
		MonthDay:    int32(r.MonthDay),
		SetPosition: int32(r.SetPosition),
		Hour:        int32(r.Hour),
		Minute:      int32(r.Minute),
		Anchor:      anchor,
	}
}
