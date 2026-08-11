package schedule

import (
	"testing"
	"time"
)

// TestNext_SpringForwardAtMidnight is the transition TestNext_SpringForwardGap
// does not cover. That test uses America/New_York, where the clock moves at
// 02:00 and the swallowed hour stays inside one calendar day. Several zones
// move it at 24:00 instead — America/Santiago and Asia/Beirut among them — so
// the hour that ceases to exist is 00:00 of the FOLLOWING day.
//
// Next's gap detection used to require the jump to stay within one day, so in
// those zones it never noticed that a scheduled hour had been swallowed, and
// a daily 00:30 schedule skipped the transition day outright: it fired on the
// 5th and then the 7th, with nothing on the 6th and nothing logged to say so.
// PLAN.md §7 promises the opposite — the run is not dropped, it moves to the
// first instant that exists after the gap.
//
// A daily job set for just after midnight is not an exotic configuration; it
// is the obvious choice for "run overnight", which is what makes the zones
// where this failed the ones most likely to be configured this way.
func TestNext_SpringForwardAtMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	s, err := Parse("30 0 * * *", loc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// 2026-09-06: 00:00 -> 01:00, so 00:30 does not exist that day.
	from := time.Date(2026, 9, 5, 0, 30, 0, 0, loc)
	got := s.Next(from)

	if y, m, d := got.In(loc).Date(); y != 2026 || m != time.September || d != 6 {
		t.Fatalf("Next after %v = %v — the transition day was skipped entirely (PLAN.md §7)",
			from, got.In(loc))
	}
	if h := got.In(loc).Hour(); h != 1 {
		t.Errorf("fired at hour %d on the transition day, want 1 — the first instant after the gap", h)
	}
	// And the day after is back to normal.
	if next := s.Next(got).In(loc); next.Day() != 7 || next.Hour() != 0 || next.Minute() != 30 {
		t.Errorf("the fire after the transition = %v, want 2026-09-07 00:30", next)
	}
}

// TestNext_SpringForwardHalfHour covers the other assumption the gap check
// used to make: that a transition moves the clock by a whole hour. Lord Howe
// Island moves it by thirty minutes — 02:00 becomes 02:30 — so "0 2 * * *"
// is swallowed on the transition day while 02:30 goes on existing. An
// hour-granular check sees hour 2 present and concludes nothing was lost.
func TestNext_SpringForwardHalfHour(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	s, err := Parse("0 2 * * *", loc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// 2026-10-04: 02:00 -> 02:30.
	from := time.Date(2026, 10, 3, 2, 0, 0, 0, loc)
	got := s.Next(from).In(loc)

	if got.Day() != 4 {
		t.Fatalf("Next after %v = %v — the transition day was skipped", from, got)
	}
	if got.Hour() != 2 || got.Minute() != 30 {
		t.Errorf("fired at %02d:%02d, want 02:30 — the first instant after the half-hour gap",
			got.Hour(), got.Minute())
	}
}

// TestNext_DailyScheduleNeverSkipsADay walks a year of firings in the zones
// with the least forgiving transitions — midnight moves, a 30-minute offset
// (Lord Howe), a 45-minute offset (Chatham) — and asserts the two invariants
// a daily schedule has to hold across all of them: every firing is strictly
// after the instant it was derived from, and consecutive firings are about a
// day apart. A skipped day shows up as a ~48h gap, a doubled fire as ~0h.
func TestNext_DailyScheduleNeverSkipsADay(t *testing.T) {
	for _, zone := range []string{
		"America/Santiago", "Asia/Beirut", "Australia/Lord_Howe",
		"Pacific/Chatham", "America/New_York", "Europe/Dublin", "UTC",
	} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skipf("tzdata unavailable: %v", err)
		}
		for _, expr := range []string{"30 0 * * *", "0 2 * * *", "15 23 * * *"} {
			s, err := Parse(expr, loc)
			if err != nil {
				t.Fatalf("%s %q: parse: %v", zone, expr, err)
			}
			cur := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			var prev time.Time
			for range 400 {
				next := s.Next(cur)
				if next.IsZero() {
					t.Fatalf("%s %q: no firing found after %v", zone, expr, cur.In(loc))
				}
				if !next.After(cur) {
					t.Fatalf("%s %q: Next(%v) = %v, which is not strictly after it",
						zone, expr, cur.In(loc), next.In(loc))
				}
				if !prev.IsZero() {
					if gap := next.Sub(prev); gap < 22*time.Hour || gap > 26*time.Hour {
						t.Errorf("%s %q: consecutive firings %v -> %v are %v apart, want ~24h",
							zone, expr, prev.In(loc), next.In(loc), gap)
					}
				}
				prev, cur = next, next
			}
		}
	}
}
