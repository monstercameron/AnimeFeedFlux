package schedule

import (
	"testing"
	"time"
)

// recurrence_test.go is organised around the schedules Cam asked for, because
// those are the ones cron could not express and therefore the ones with no
// prior coverage anywhere: weekly, biweekly, every 3 weeks, monthly, every 6
// months, daily, and "every other Thursday at 4pm".
//
// The DST cases at the bottom are the ones that would go wrong quietly. A
// fortnightly schedule that slips a week each spring is not a crash; it is a
// feed that publishes on the wrong Thursday for six months.

func mustRecurrence(t *testing.T, r Recurrence) Recurrence {
	t.Helper()
	out, err := NewRecurrence(r)
	if err != nil {
		t.Fatalf("NewRecurrence: %v", err)
	}
	return out
}

// fireDates renders the next n firings as "2006-01-02 15:04" in the
// recurrence's own zone, which is how a human reads a schedule and how a
// failure here stays diagnosable.
func fireDates(t *testing.T, r Recurrence, after time.Time, n int) []string {
	t.Helper()
	out := []string{}
	for _, f := range r.NextN(after, n) {
		out = append(out, f.In(r.Loc).Format("2006-01-02 15:04"))
	}
	return out
}

func assertDates(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d firings %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("firing %d = %s, want %s\n  got:  %v\n  want: %v", i, got[i], want[i], got, want)
		}
	}
}

// --- the schedules cron cannot write down --------------------------------

// TestEveryOtherThursdayAt4pm is the example that motivated the whole model.
// There is no cron expression for it: `*/2` in the day-of-week field means
// every second weekday NUMBER, not every second week.
func TestEveryOtherThursdayAt4pm(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	// Anchor on a Thursday so "every other" starts from that one.
	anchor := time.Date(2026, 8, 6, 0, 0, 0, 0, loc) // Thursday
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 2,
		Weekdays: []time.Weekday{time.Thursday},
		Hour:     16, Minute: 0,
		Anchor: anchor, Loc: loc,
	})

	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 5)
	assertDates(t, got, []string{
		"2026-08-06 16:00",
		"2026-08-20 16:00",
		"2026-09-03 16:00",
		"2026-09-17 16:00",
		"2026-10-01 16:00",
	})
}

// The phase is what the anchor buys. Same rule, anchored a week later, must
// fire on the OTHER Thursdays — and being unable to express this is exactly
// why cron was not enough.
func TestBiweeklyPhaseFollowsTheAnchor(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 2,
		Weekdays: []time.Weekday{time.Thursday},
		Hour:     16,
		Anchor:   time.Date(2026, 8, 13, 0, 0, 0, 0, loc), // the next Thursday
		Loc:      loc,
	})

	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 3)
	assertDates(t, got, []string{
		"2026-08-13 16:00",
		"2026-08-27 16:00",
		"2026-09-10 16:00",
	})
}

func TestEveryThreeWeeks(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 3,
		Weekdays: []time.Weekday{time.Monday},
		Hour:     9,
		Anchor:   time.Date(2026, 1, 5, 0, 0, 0, 0, loc), // a Monday
		Loc:      loc,
	})

	got := fireDates(t, r, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-01-05 09:00",
		"2026-01-26 09:00",
		"2026-02-16 09:00",
		"2026-03-09 09:00",
	})
}

// TestBiweeklyOnTwoWeekdaysFiresTwicePerFiringWeek pins the rule that the
// interval skips WEEKS, not individual days — the difference between "every
// other week on Mon and Thu" (2 runs, then a blank week) and a reading where
// the days alternate.
func TestBiweeklyOnTwoWeekdaysFiresTwicePerFiringWeek(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 2,
		Weekdays: []time.Weekday{time.Monday, time.Thursday},
		Hour:     12,
		Anchor:   time.Date(2026, 8, 3, 0, 0, 0, 0, loc), // Monday
		Loc:      loc,
	})

	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-08-03 12:00", // Mon, firing week
		"2026-08-06 12:00", // Thu, same week
		"2026-08-17 12:00", // Mon, two weeks later
		"2026-08-20 12:00",
	})
}

func TestEverySixMonths(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqMonthly, Interval: 6,
		MonthDay: 1, Hour: 8,
		Anchor: time.Date(2026, 1, 1, 0, 0, 0, 0, loc),
		Loc:    loc,
	})

	got := fireDates(t, r, time.Date(2025, 12, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-01-01 08:00",
		"2026-07-01 08:00",
		"2027-01-01 08:00",
		"2027-07-01 08:00",
	})
}

func TestMonthlyOnTheSecondTuesday(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqMonthly, Interval: 1,
		Weekdays: []time.Weekday{time.Tuesday}, SetPos: 2,
		Hour:   10,
		Anchor: time.Date(2026, 8, 1, 0, 0, 0, 0, loc),
		Loc:    loc,
	})

	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-08-11 10:00",
		"2026-09-08 10:00",
		"2026-10-13 10:00",
		"2026-11-10 10:00",
	})
}

func TestMonthlyOnTheLastFriday(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqMonthly, Interval: 1,
		Weekdays: []time.Weekday{time.Friday}, SetPos: SetPosLast,
		Hour:   17,
		Anchor: time.Date(2026, 8, 1, 0, 0, 0, 0, loc),
		Loc:    loc,
	})

	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-08-28 17:00", // 5-Friday month: the last is the 5th
		"2026-09-25 17:00",
		"2026-10-30 17:00",
		"2026-11-27 17:00",
	})
}

// TestMonthlyOnTheLastDayHandlesShortMonths is why LastDayOfMonth exists: an
// operator who means "end of the month" must not get 28ths in January.
func TestMonthlyOnTheLastDayHandlesShortMonths(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqMonthly, Interval: 1,
		MonthDay: LastDayOfMonth, Hour: 23,
		Anchor: time.Date(2028, 1, 1, 0, 0, 0, 0, loc),
		Loc:    loc,
	})

	got := fireDates(t, r, time.Date(2028, 1, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2028-01-31 23:00",
		"2028-02-29 23:00", // leap year
		"2028-03-31 23:00",
		"2028-04-30 23:00",
	})
}

// TestMonthlyOnThe31stSkipsShortMonths is RFC 5545's rule, and the one most
// likely to be questioned: a day that does not exist does not fire. Silently
// moving it to the 28th would be a different schedule than the one asked for.
func TestMonthlyOnThe31stSkipsShortMonths(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqMonthly, Interval: 1, MonthDay: 31, Hour: 12,
		Anchor: time.Date(2026, 1, 1, 0, 0, 0, 0, loc),
		Loc:    loc,
	})

	got := fireDates(t, r, time.Date(2026, 1, 1, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2026-01-31 12:00",
		"2026-03-31 12:00", // February skipped entirely
		"2026-05-31 12:00", // April skipped
		"2026-07-31 12:00", // June skipped
	})
}

func TestDailyAndEveryThirdDay(t *testing.T) {
	loc := mustLoc(t, "UTC")
	anchor := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)

	daily := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 1, Hour: 7, Anchor: anchor, Loc: loc})
	assertDates(t, fireDates(t, daily, anchor, 3), []string{
		"2026-08-01 07:00", "2026-08-02 07:00", "2026-08-03 07:00",
	})

	third := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 3, Hour: 7, Anchor: anchor, Loc: loc})
	assertDates(t, fireDates(t, third, anchor, 3), []string{
		"2026-08-01 07:00", "2026-08-04 07:00", "2026-08-07 07:00",
	})
}

func TestWeeklyOnSelectedDays(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 1,
		Weekdays: []time.Weekday{time.Monday, time.Wednesday, time.Friday},
		Hour:     6,
		Anchor:   time.Date(2026, 8, 3, 0, 0, 0, 0, loc),
		Loc:      loc,
	})

	assertDates(t, fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 4), []string{
		"2026-08-03 06:00", "2026-08-05 06:00", "2026-08-07 06:00", "2026-08-10 06:00",
	})
}

// --- DST, which is the whole reason this package is hand-written ---------

// TestFortnightlyDoesNotSlipAcrossSpringForward is the failure this model is
// most exposed to. Counting the interval by subtracting instants and dividing
// by 24h is off by one for any span containing a 23-hour day, which would
// shift every subsequent firing by a week. The dates below straddle the US
// spring-forward transition (2027-03-14).
func TestFortnightlyDoesNotSlipAcrossSpringForward(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 2,
		Weekdays: []time.Weekday{time.Thursday},
		Hour:     16,
		Anchor:   time.Date(2027, 2, 25, 0, 0, 0, 0, loc), // Thursday
		Loc:      loc,
	})

	got := fireDates(t, r, time.Date(2027, 2, 20, 0, 0, 0, 0, loc), 4)
	assertDates(t, got, []string{
		"2027-02-25 16:00",
		"2027-03-11 16:00",
		"2027-03-25 16:00", // still a Thursday, still 16:00 local, after the transition
		"2027-04-08 16:00",
	})
}

// TestDailyKeepsLocalWallClockAcrossBothTransitions: "7am" means 7am, before
// and after. If the engine drifted to UTC the hour would move by one.
func TestDailyKeepsLocalWallClockAcrossBothTransitions(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	r := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 1, Hour: 7, Loc: loc})

	// Spring forward 2027-03-14.
	spring := fireDates(t, r, time.Date(2027, 3, 12, 12, 0, 0, 0, loc), 3)
	assertDates(t, spring, []string{"2027-03-13 07:00", "2027-03-14 07:00", "2027-03-15 07:00"})

	// Fall back 2027-11-07.
	fall := fireDates(t, r, time.Date(2027, 11, 5, 12, 0, 0, 0, loc), 3)
	assertDates(t, fall, []string{"2027-11-06 07:00", "2027-11-07 07:00", "2027-11-08 07:00"})
}

// TestFiringInTheSkippedHourStillHappens inherits Schedule.Next's promise: a
// run scheduled inside the spring-forward gap fires at the first valid
// instant rather than being dropped for the day.
func TestFiringInTheSkippedHourStillHappens(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	// 2:30am does not exist on 2027-03-14 in New York.
	r := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 1, Hour: 2, Minute: 30, Loc: loc})

	next := r.Next(time.Date(2027, 3, 14, 0, 0, 0, 0, loc))
	if next.IsZero() {
		t.Fatal("no firing on the spring-forward day — the run was dropped")
	}
	if got := next.In(loc).Format("2006-01-02"); got != "2027-03-14" {
		t.Errorf("firing landed on %s, want it to stay on the transition day", got)
	}
	if next.In(loc).Hour() < 3 {
		t.Errorf("firing at %s is inside the skipped hour", next.In(loc).Format("15:04"))
	}
}

// TestFallBackFiresOnceNotTwice: the repeated wall-clock hour maps to two
// instants, and a schedule must not run twice.
func TestFallBackFiresOnceNotTwice(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	r := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 1, Hour: 1, Minute: 30, Loc: loc})

	// 2027-11-07: 1:30am happens twice.
	firings := r.NextN(time.Date(2027, 11, 6, 12, 0, 0, 0, loc), 2)
	if len(firings) != 2 {
		t.Fatalf("expected 2 firings, got %d", len(firings))
	}
	d0 := firings[0].In(loc).Format("2006-01-02")
	d1 := firings[1].In(loc).Format("2006-01-02")
	if d0 == d1 {
		t.Errorf("fired twice on %s — the repeated hour must fire once", d0)
	}
}

// --- validation ----------------------------------------------------------

func TestNewRecurrenceRejectsUnschedulableConfigurations(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cases := []struct {
		name string
		in   Recurrence
	}{
		{"no timezone", Recurrence{Freq: FreqDaily, Interval: 1}},
		{"zero interval", Recurrence{Freq: FreqDaily, Interval: 0, Loc: loc}},
		{"negative interval", Recurrence{Freq: FreqDaily, Interval: -2, Loc: loc}},
		{"hour out of range", Recurrence{Freq: FreqDaily, Interval: 1, Hour: 24, Loc: loc}},
		{"minute out of range", Recurrence{Freq: FreqDaily, Interval: 1, Minute: 60, Loc: loc}},
		{"weekly with no days", Recurrence{Freq: FreqWeekly, Interval: 1, Loc: loc}},
		{"weekly with a duplicate day", Recurrence{Freq: FreqWeekly, Interval: 1,
			Weekdays: []time.Weekday{time.Monday, time.Monday}, Loc: loc}},
		{"monthly day 0", Recurrence{Freq: FreqMonthly, Interval: 1, MonthDay: 0, Loc: loc}},
		{"monthly day 32", Recurrence{Freq: FreqMonthly, Interval: 1, MonthDay: 32, Loc: loc}},
		{"set position 5", Recurrence{Freq: FreqMonthly, Interval: 1, SetPos: 5,
			Weekdays: []time.Weekday{time.Monday}, Loc: loc}},
		{"set position with two weekdays", Recurrence{Freq: FreqMonthly, Interval: 1, SetPos: 1,
			Weekdays: []time.Weekday{time.Monday, time.Friday}, Loc: loc}},
		{"set position with no weekday", Recurrence{Freq: FreqMonthly, Interval: 1, SetPos: 1, Loc: loc}},
		{"unknown frequency", Recurrence{Freq: 99, Interval: 1, Loc: loc}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRecurrence(tc.in); err == nil {
				t.Error("accepted a configuration that can never fire, or fires wrongly")
			}
		})
	}
}

// TestAnchorlessRecurrenceIsDeterministic: two processes loading the same
// stored schedule must agree on which weeks fire. An anchor defaulted to
// time.Now() would make that depend on when the server booted.
func TestAnchorlessRecurrenceIsDeterministic(t *testing.T) {
	loc := mustLoc(t, "UTC")
	build := func() Recurrence {
		return mustRecurrence(t, Recurrence{
			Freq: FreqWeekly, Interval: 2,
			Weekdays: []time.Weekday{time.Thursday}, Hour: 16, Loc: loc,
		})
	}
	after := time.Date(2026, 8, 1, 0, 0, 0, 0, loc)
	a := fireDates(t, build(), after, 3)
	b := fireDates(t, build(), after, 3)
	assertDates(t, a, b)
}

// TestDatesBeforeTheAnchorStillComputeCoherently guards the negative-modulo
// case: the editor previews a schedule whose anchor the operator just set to
// a future date.
func TestDatesBeforeTheAnchorStillComputeCoherently(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{
		Freq: FreqWeekly, Interval: 2,
		Weekdays: []time.Weekday{time.Thursday}, Hour: 16,
		Anchor: time.Date(2026, 9, 3, 0, 0, 0, 0, loc), // in the future
		Loc:    loc,
	})

	// Asking from well before the anchor must still produce every-other-Thursday
	// in the same phase, not nothing and not every Thursday.
	got := fireDates(t, r, time.Date(2026, 8, 1, 0, 0, 0, 0, loc), 3)
	assertDates(t, got, []string{
		"2026-08-06 16:00",
		"2026-08-20 16:00",
		"2026-09-03 16:00",
	})
}

func TestNextNStopsCleanlyAtZero(t *testing.T) {
	loc := mustLoc(t, "UTC")
	r := mustRecurrence(t, Recurrence{Freq: FreqDaily, Interval: 1, Hour: 9, Loc: loc})
	if got := r.NextN(time.Now(), 0); got != nil {
		t.Errorf("NextN(_, 0) = %v, want nil", got)
	}
	if got := r.NextN(time.Now(), -1); got != nil {
		t.Errorf("NextN(_, -1) = %v, want nil", got)
	}
}

func TestFrequencyString(t *testing.T) {
	for _, tc := range []struct {
		f    Frequency
		want string
	}{
		{FreqDaily, "daily"}, {FreqWeekly, "weekly"}, {FreqMonthly, "monthly"}, {Frequency(99), "unknown"},
	} {
		if got := tc.f.String(); got != tc.want {
			t.Errorf("Frequency(%d).String() = %q, want %q", tc.f, got, tc.want)
		}
	}
}
