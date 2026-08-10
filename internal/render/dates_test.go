package render

import "time"

import "testing"

func TestRFC822(t *testing.T) {
	// The Best Practices Profile's own example instant, used verbatim so the
	// expected string is traceable back to the spec rather than hand-derived.
	instant := time.Date(2007, time.October, 4, 23, 59, 45, 0, time.UTC)
	want := "Thu, 04 Oct 2007 23:59:45 +0000"
	if got := RFC822(instant); got != want {
		t.Errorf("RFC822(%v) = %q, want %q", instant, got, want)
	}
}

func TestRFC822_NonUTCInputIsConverted(t *testing.T) {
	// -0500 (EST) has no daylight offset ambiguity; 23:59:45 EST is
	// 04:59:45 UTC the following day. If this passed through unconverted
	// it would print "-0500", which is disallowed.
	loc := time.FixedZone("EST", -5*60*60)
	instant := time.Date(2007, time.October, 4, 23, 59, 45, 0, loc)
	want := "Fri, 05 Oct 2007 04:59:45 +0000"
	if got := RFC822(instant); got != want {
		t.Errorf("RFC822(%v) = %q, want %q", instant, got, want)
	}
}

func TestRFC3339(t *testing.T) {
	instant := time.Date(2007, time.October, 4, 23, 59, 45, 0, time.UTC)
	want := "2007-10-04T23:59:45Z"
	if got := RFC3339(instant); got != want {
		t.Errorf("RFC3339(%v) = %q, want %q", instant, got, want)
	}
}

func TestRFC3339_NonUTCInputIsConverted(t *testing.T) {
	loc := time.FixedZone("EST", -5*60*60)
	instant := time.Date(2007, time.October, 4, 23, 59, 45, 0, loc)
	want := "2007-10-05T04:59:45Z"
	if got := RFC3339(instant); got != want {
		t.Errorf("RFC3339(%v) = %q, want %q", instant, got, want)
	}
}

// TestFourDigitYear guards against the layout ever regressing to a two-digit
// year ("06" instead of "2006" in the Go reference layout), which would be a
// silent, hard-to-notice spec violation since most feed dates in the 2000s
// and 2020s still "look plausible" truncated to two digits.
func TestFourDigitYear(t *testing.T) {
	instant := time.Date(9, time.January, 2, 3, 4, 5, 0, time.UTC)

	got822 := RFC822(instant)
	want822 := "Fri, 02 Jan 0009 03:04:05 +0000"
	if got822 != want822 {
		t.Errorf("RFC822(%v) = %q, want %q", instant, got822, want822)
	}

	got3339 := RFC3339(instant)
	want3339 := "0009-01-02T03:04:05Z"
	if got3339 != want3339 {
		t.Errorf("RFC3339(%v) = %q, want %q", instant, got3339, want3339)
	}
}

// TestFormatsAreNeverInterchangeable is RULE-5's dedicated test (A2-03,
// TODOS.md): the two encoders must never accidentally converge or be
// swappable. Table-driven so future instants (leap seconds' neighbors, year
// boundaries, DST-transition inputs) are cheap to add.
func TestFormatsAreNeverInterchangeable(t *testing.T) {
	cases := []struct {
		name    string
		instant time.Time
	}{
		{"epoch-plus", time.Date(2007, time.October, 4, 23, 59, 45, 0, time.UTC)},
		{"midnight-utc", time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{"non-utc-input", time.Date(2026, time.June, 18, 9, 30, 0, 0, time.FixedZone("EST", -5*60*60))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got822 := RFC822(tc.instant)
			got3339 := RFC3339(tc.instant)

			if got822 == got3339 {
				t.Fatalf("RFC822 and RFC3339 produced identical output %q for %v", got822, tc.instant)
			}

			// An RFC3339 string must not parse as RFC1123Z (the closest
			// standard-library layout to our RFC822 output): different
			// separators ("T" vs a space, "Z"/offset vs a trailing
			// "+0000") make this fail, which is the point.
			if _, err := time.Parse(time.RFC1123Z, got3339); err == nil {
				t.Errorf("RFC3339 output %q unexpectedly parsed as RFC1123Z", got3339)
			}

			// An RFC822 string must not parse as RFC3339: no "T"
			// separator, and "+0000" has no colon where RFC3339 requires
			// one in its numeric offset.
			if _, err := time.Parse(time.RFC3339, got822); err == nil {
				t.Errorf("RFC822 output %q unexpectedly parsed as RFC3339", got822)
			}
		})
	}
}
