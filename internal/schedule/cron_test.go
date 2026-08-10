package schedule

import (
	"strings"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func TestParse_Valid(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cases := []string{
		"* * * * *",
		"0 12 * * *",
		"*/15 * * * *",
		"1-30/5 * * * *",
		"0,15,30,45 * * * *",
		"30 2 * * 1-5",
		"0 0 1 1 *",
		"0 0 29 2 *", // leap-day only, still a valid expression
	}
	for _, expr := range cases {
		if _, err := Parse(expr, loc); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", expr, err)
		}
	}
}

func TestParse_Invalid(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cases := map[string]string{
		"* * * *":         "too few fields",
		"* * * * * *":     "too many fields",
		"60 * * * *":      "minute out of range",
		"* 24 * * *":      "hour out of range",
		"* * 0 * *":       "day-of-month out of range (min 1)",
		"* * 32 * *":      "day-of-month out of range (max 31)",
		"* * * 13 *":      "month out of range",
		"* * * * 7":       "day-of-week out of range",
		"* * * * -1":      "negative value",
		"abc * * * *":     "non-numeric",
		"5-2 * * * *":     "inverted range",
		"*/0 * * * *":     "zero step",
		"1,,2 * * * *":    "empty list item",
		"@daily":          "macros unsupported",
		"* * * * *   foo": "extra token",
	}
	for expr, why := range cases {
		if _, err := Parse(expr, loc); err == nil {
			t.Errorf("Parse(%q) expected error (%s), got nil", expr, why)
		}
	}
}

func TestParse_NamesOffendingField(t *testing.T) {
	loc := mustLoc(t, "UTC")
	_, err := Parse("60 * * * *", loc)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "minute") {
		t.Errorf("error %q does not name the offending field", got)
	}
}

func TestParse_RequiresTimezone(t *testing.T) {
	if _, err := Parse("* * * * *", nil); err == nil {
		t.Fatal("expected error for nil timezone")
	}
}

func TestNext_EveryMinute(t *testing.T) {
	loc := mustLoc(t, "UTC")
	sched, err := Parse("* * * * *", loc)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 10, 30, 15, 0, loc)
	got := sched.Next(after)
	want := time.Date(2026, 1, 1, 10, 31, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

func TestNext_DailyAtHour(t *testing.T) {
	loc := mustLoc(t, "UTC")
	sched, err := Parse("0 12 * * *", loc)
	if err != nil {
		t.Fatal(err)
	}

	// Before today's fire: fires today.
	after := time.Date(2026, 1, 1, 8, 0, 0, 0, loc)
	got := sched.Next(after)
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}

	// After today's fire: rolls to tomorrow.
	after2 := time.Date(2026, 1, 1, 12, 0, 0, 0, loc)
	got2 := sched.Next(after2)
	want2 := time.Date(2026, 1, 2, 12, 0, 0, 0, loc)
	if !got2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", after2, got2, want2)
	}
}

func TestNext_StepAndList(t *testing.T) {
	loc := mustLoc(t, "UTC")
	sched, err := Parse("*/15 * * * *", loc)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 10, 16, 0, 0, loc)
	got := sched.Next(after)
	want := time.Date(2026, 1, 1, 10, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

func TestNext_DayOfMonthAndWeekOR(t *testing.T) {
	loc := mustLoc(t, "UTC")
	// Fires on the 15th of the month OR on Fridays, whichever comes first.
	sched, err := Parse("0 9 15 * 5", loc)
	if err != nil {
		t.Fatal(err)
	}
	// 2026-01-01 is a Thursday; the next Friday is 2026-01-02.
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	got := sched.Next(after)
	want := time.Date(2026, 1, 2, 9, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

func TestNext_MonthField(t *testing.T) {
	loc := mustLoc(t, "UTC")
	sched, err := Parse("0 0 1 3 *", loc)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	got := sched.Next(after)
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

// --- The two tests this package exists for. ---

// TestNext_SpringForwardGap: America/New_York springs forward on
// 2024-03-10, 02:00 -> 03:00 EDT. A run scheduled for 02:30 that day has no
// wall-clock instant to fire at — it must fire at the next valid instant
// (03:00:00 EDT), not be silently dropped until the next day.
func TestNext_SpringForwardGap(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	sched, err := Parse("30 2 * * *", loc)
	if err != nil {
		t.Fatal(err)
	}

	after := time.Date(2024, 3, 9, 12, 0, 0, 0, loc)
	got := sched.Next(after)
	want := time.Date(2024, 3, 10, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("Next(%v) = %v, want %v (gap-end instant)", after, got, want)
	}
	if _, offset := got.Zone(); offset != -4*3600 {
		t.Errorf("expected EDT (-04:00) after the gap, got offset %d", offset)
	}

	// Sanity: the following day's run is unaffected — normal 02:30.
	got2 := sched.Next(got)
	want2 := time.Date(2024, 3, 11, 2, 30, 0, 0, loc)
	if !got2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", got, got2, want2)
	}
}

// TestNext_FallBackRepeatedHour: America/New_York falls back on
// 2024-11-03, 02:00 EDT -> 01:00 EST, so local 01:00-01:59 happens twice. A
// run scheduled for 01:30 must fire exactly ONCE that day (on the first,
// EDT, occurrence) — calling Next again from that result must skip straight
// past the repeated occurrence to the following day, never firing a second
// time on 2024-11-03.
func TestNext_FallBackRepeatedHour(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	sched, err := Parse("30 1 * * *", loc)
	if err != nil {
		t.Fatal(err)
	}

	after := time.Date(2024, 11, 3, 0, 0, 0, 0, loc)
	first := sched.Next(after)
	wantFirst := time.Date(2024, 11, 3, 1, 30, 0, 0, loc)
	if !first.Equal(wantFirst) {
		t.Fatalf("first Next(%v) = %v, want %v", after, first, wantFirst)
	}
	if _, offset := first.Zone(); offset != -4*3600 {
		t.Fatalf("expected the EARLIER (EDT, -04:00) occurrence to fire, got offset %d", offset)
	}

	second := sched.Next(first)
	wantSecond := time.Date(2024, 11, 4, 1, 30, 0, 0, loc)
	if !second.Equal(wantSecond) {
		t.Fatalf("second Next(%v) = %v, want %v (must skip the repeated hour entirely, not fire again on 11-03)", first, second, wantSecond)
	}
}

func TestNext_UnschedulableReturnsZero(t *testing.T) {
	loc := mustLoc(t, "UTC")
	// February never has a 31st.
	sched, err := Parse("0 0 31 2 *", loc)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	got := sched.Next(after)
	if !got.IsZero() {
		t.Errorf("Next(%v) = %v, want zero Time for an unschedulable expression", after, got)
	}
}
