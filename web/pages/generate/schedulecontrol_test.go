package generatepage

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
)

// schedulecontrol_test.go covers the layer between the controls on screen and
// the recipe that gets saved. Two properties matter most:
//
//   - The round trip. An operator sets "every 2 years", saves, reopens, and
//     must see "every 2 years" — not "every 24 months", which is the same
//     schedule and not the same sentence.
//   - The preview. It is the only thing that answers "which Thursdays", so a
//     wrong preview is worse than none: it is a confident wrong answer.

// schedTestT renders keys with their args visible, so a readback assertion
// can prove the right key got the right values without pinning English.
type schedT struct{}

func (schedT) T(key string, args ...any) string { return schedTestT(key, args...) }

var schedTestTranslator = schedT{}

func schedTestT(key string, args ...any) string {
	if len(args) == 0 {
		return "[" + key + "]"
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, strings.TrimSpace(strings.Trim(stringify(a), "[]")))
	}
	return "[" + key + ":" + strings.Join(parts, "|") + "]"
}

func stringify(a any) string {
	switch v := a.(type) {
	case string:
		return v
	case int:
		return itoa(v)
	default:
		return "?"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func today() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }

// --- conversion ----------------------------------------------------------

func TestEveryOtherThursdayConvertsToAWeeklyRecurrence(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitWeek, 2
	d.Weekdays = []time.Weekday{time.Thursday}
	d.Hour, d.Minute = 16, 0
	d.Anchor = "2026-08-06"

	r := d.ToRecurrence()
	if r.Frequency != "weekly" || r.Interval != 2 {
		t.Fatalf("got frequency=%q interval=%d, want weekly/2", r.Frequency, r.Interval)
	}
	if len(r.Weekdays) != 1 || r.Weekdays[0] != "thursday" {
		t.Errorf("weekdays = %v, want [thursday]", r.Weekdays)
	}
	if r.Hour != 16 {
		t.Errorf("hour = %d, want 16", r.Hour)
	}
}

// TestYearsBecomeMonthsAndComeBackAsYears is the round trip that would
// otherwise leak the engine's simplification into the operator's vocabulary.
func TestYearsBecomeMonthsAndComeBackAsYears(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitYear, 2
	d.Mode, d.MonthDay = MonthlyOnDay, 1

	r := d.ToRecurrence()
	if r.Frequency != "monthly" || r.Interval != 24 {
		t.Fatalf("got %s/%d, want monthly/24 — the engine has no yearly frequency", r.Frequency, r.Interval)
	}

	back := FromRecurrence(&r, today())
	if back.Unit != UnitYear || back.Interval != 2 {
		t.Errorf("round trip produced %s/%d, want year/2 — the operator typed years", back.Unit, back.Interval)
	}
}

// Six months must NOT come back as years: 6 is not a whole number of them.
func TestEverySixMonthsStaysMonths(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval, d.Mode, d.MonthDay = UnitMonth, 6, MonthlyOnDay, 1

	back := FromRecurrence(ptr(d.ToRecurrence()), today())
	if back.Unit != UnitMonth || back.Interval != 6 {
		t.Errorf("round trip produced %s/%d, want month/6", back.Unit, back.Interval)
	}
}

func TestMonthlyByWeekdayRoundTrips(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitMonth, 1
	d.Mode, d.SetPosition, d.MonthWeekday = MonthlyOnWeekday, 2, time.Tuesday

	r := d.ToRecurrence()
	if r.SetPosition != 2 || len(r.Weekdays) != 1 || r.Weekdays[0] != "tuesday" {
		t.Fatalf("got setpos=%d weekdays=%v, want 2/[tuesday]", r.SetPosition, r.Weekdays)
	}
	if r.MonthDay != 0 {
		t.Errorf("month_day = %d, want 0 — a weekday-positioned schedule must not also carry a day", r.MonthDay)
	}

	back := FromRecurrence(&r, today())
	if back.Mode != MonthlyOnWeekday || back.SetPosition != 2 || back.MonthWeekday != time.Tuesday {
		t.Errorf("round trip lost the position: %+v", back)
	}
}

func TestDailyDropsWeekdayAndMonthFields(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitDay, 3
	d.Weekdays = []time.Weekday{time.Monday} // stale form state
	d.MonthDay = 15

	r := d.ToRecurrence()
	if r.Frequency != "daily" || r.Interval != 3 {
		t.Fatalf("got %s/%d, want daily/3", r.Frequency, r.Interval)
	}
	if len(r.Weekdays) != 0 || r.MonthDay != 0 {
		t.Errorf("daily recipe carried leftovers: weekdays=%v month_day=%d", r.Weekdays, r.MonthDay)
	}
}

func TestIntervalIsNeverBelowOne(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Interval = 0
	if got := d.ToRecurrence().Interval; got != 1 {
		t.Errorf("interval 0 became %d, want 1 — a zero interval never fires", got)
	}
}

func TestFromRecurrenceHandlesNil(t *testing.T) {
	d := FromRecurrence(nil, today())
	if d.Unit != UnitDay || d.Interval != 1 {
		t.Errorf("a feed with no recurrence should open on the daily default, got %s/%d", d.Unit, d.Interval)
	}
	if d.Anchor == "" {
		t.Error("default draft has no anchor; the interval would have no phase")
	}
}

// --- preview -------------------------------------------------------------

// TestPreviewAnswersWhichThursdays is the control's reason for existing.
func TestPreviewAnswersWhichThursdays(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitWeek, 2
	d.Weekdays = []time.Weekday{time.Thursday}
	d.Hour, d.Minute = 16, 0
	d.Anchor = "2026-08-06"

	runs, err := d.PreviewRuns("America/New_York", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 3)
	if err != nil {
		t.Fatalf("PreviewRuns: %v", err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	want := []string{"2026-08-06 16:00", "2026-08-20 16:00", "2026-09-03 16:00"}
	if len(runs) != len(want) {
		t.Fatalf("got %d runs, want %d", len(runs), len(want))
	}
	for i, w := range want {
		if got := runs[i].In(loc).Format("2006-01-02 15:04"); got != w {
			t.Errorf("run %d = %s, want %s", i, got, w)
		}
	}
}

func TestPreviewReportsAnUnusableTimezone(t *testing.T) {
	d := DefaultScheduleDraft(today())
	if _, err := d.PreviewRuns("Not/AZone", time.Now(), 3); err == nil {
		t.Error("an invalid timezone previewed successfully; the operator would see fabricated times")
	}
}

// --- weekday toggling ----------------------------------------------------

func TestToggleWeekdayKeepsTheListSortedAndNonEmpty(t *testing.T) {
	days := []time.Weekday{time.Thursday}

	days = ToggleWeekday(days, time.Monday)
	if len(days) != 2 || days[0] != time.Monday || days[1] != time.Thursday {
		t.Fatalf("got %v, want [Monday Thursday] in weekday order", days)
	}

	days = ToggleWeekday(days, time.Monday) // remove
	if len(days) != 1 || days[0] != time.Thursday {
		t.Fatalf("got %v, want [Thursday]", days)
	}

	// Removing the last day must be refused: a weekly schedule with no days
	// never fires, and the editor must not be able to build one.
	days = ToggleWeekday(days, time.Thursday)
	if len(days) != 1 {
		t.Errorf("emptied the weekday list (%v); that schedule would never run", days)
	}
}

// --- readback ------------------------------------------------------------

func TestReadbackUsesTheRightKeyPerShape(t *testing.T) {
	base := DefaultScheduleDraft(today())

	cases := []struct {
		name    string
		mutate  func(*ScheduleDraft)
		wantKey string
	}{
		{"daily", func(d *ScheduleDraft) { d.Unit, d.Interval = UnitDay, 1 },
			"generate.editor.schedule.readback.daily"},
		{"every 3 days", func(d *ScheduleDraft) { d.Unit, d.Interval = UnitDay, 3 },
			"generate.editor.schedule.readback.dailyEvery"},
		{"weekly", func(d *ScheduleDraft) {
			d.Unit, d.Interval = UnitWeek, 1
			d.Weekdays = []time.Weekday{time.Thursday}
		}, "generate.editor.schedule.readback.weekly"},
		{"biweekly", func(d *ScheduleDraft) {
			d.Unit, d.Interval = UnitWeek, 2
			d.Weekdays = []time.Weekday{time.Thursday}
		}, "generate.editor.schedule.readback.weeklyEvery"},
		{"monthly on a day", func(d *ScheduleDraft) {
			d.Unit, d.Interval, d.Mode, d.MonthDay = UnitMonth, 1, MonthlyOnDay, 15
		}, "generate.editor.schedule.readback.monthly"},
		{"every 6 months", func(d *ScheduleDraft) {
			d.Unit, d.Interval, d.Mode, d.MonthDay = UnitMonth, 6, MonthlyOnDay, 1
		}, "generate.editor.schedule.readback.monthlyEvery"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			tc.mutate(&d)
			got := ScheduleReadback(schedTestTranslator, d, "UTC")
			if !strings.Contains(got, tc.wantKey) {
				t.Errorf("readback = %q, want it built from %s", got, tc.wantKey)
			}
		})
	}
}

func TestReadbackNamesTheLastDayAndNthWeekday(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Mode, d.MonthDay = UnitMonth, MonthlyOnDay, -1
	if got := ScheduleReadback(schedTestTranslator, d, "UTC"); !strings.Contains(got, "readback.lastDay") {
		t.Errorf("readback = %q, want the last-day phrasing", got)
	}

	d.Mode, d.SetPosition, d.MonthWeekday = MonthlyOnWeekday, -1, time.Friday
	got := ScheduleReadback(schedTestTranslator, d, "UTC")
	if !strings.Contains(got, "readback.nthWeekday") || !strings.Contains(got, "ordinal.last") {
		t.Errorf("readback = %q, want the last-Friday phrasing", got)
	}
	if !strings.Contains(got, "weekday.friday") {
		t.Errorf("readback = %q, want it to name Friday through the catalogue", got)
	}
}

// TestReadbackJoinsWeekdaysThroughTheCatalogue guards the i18n rule: a list
// assembled with a hardcoded ", " and " and " assumes English.
func TestReadbackJoinsWeekdaysThroughTheCatalogue(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Unit, d.Interval = UnitWeek, 1
	d.Weekdays = []time.Weekday{time.Monday, time.Wednesday, time.Friday}

	got := ScheduleReadback(schedTestTranslator, d, "UTC")
	for _, want := range []string{"listSeparator", "listAnd", "weekday.monday", "weekday.friday"} {
		if !strings.Contains(got, want) {
			t.Errorf("readback = %q, missing %s", got, want)
		}
	}
}

func TestReadbackCarriesTheClockAndZone(t *testing.T) {
	d := DefaultScheduleDraft(today())
	d.Hour, d.Minute = 16, 5
	got := ScheduleReadback(schedTestTranslator, d, "America/New_York")
	if !strings.Contains(got, "16:05") {
		t.Errorf("readback = %q, want the wall-clock time", got)
	}
	if !strings.Contains(got, "America/New_York") {
		t.Errorf("readback = %q, want the zone named — 4pm where is the whole question", got)
	}
}

func ptr(r feedspec.Recurrence) *feedspec.Recurrence { return &r }
