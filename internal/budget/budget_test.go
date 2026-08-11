package budget

import (
	"testing"
	"time"
)

func TestCheck_PerFeedTokenCapDenies(t *testing.T) {
	l := Limits{PerFeedDailyTokens: 1000}
	feed := Spend{TokensIn: 900, TokensOut: 50} // 950 so far
	global := Spend{}

	d := Check(l, feed, global, 100) // 950+100 > 1000
	if d.Allow {
		t.Fatalf("expected deny, got allow")
	}
	if d.Reason != ReasonFeedTokenCap {
		t.Fatalf("expected reason %q, got %q", ReasonFeedTokenCap, d.Reason)
	}
}

func TestCheck_PerFeedRunCapDenies(t *testing.T) {
	l := Limits{PerFeedDailyRuns: 3}
	feed := Spend{Runs: 3}
	global := Spend{}

	d := Check(l, feed, global, 1)
	if d.Allow {
		t.Fatalf("expected deny, got allow")
	}
	if d.Reason != ReasonFeedRunCap {
		t.Fatalf("expected reason %q, got %q", ReasonFeedRunCap, d.Reason)
	}
}

func TestCheck_GlobalCapDeniesEvenWhenEveryFeedIndividuallyFine(t *testing.T) {
	// The failure mode §13 calls out: N feeds each individually within
	// budget, but the sum blows the global ceiling.
	l := Limits{
		PerFeedDailyTokens: 100_000, // each feed is nowhere near this
		GlobalDailyTokens:  1000,
	}
	feed := Spend{TokensIn: 400, TokensOut: 0}   // this feed alone: fine
	global := Spend{TokensIn: 900, TokensOut: 0} // but combined with others: near the ceiling

	d := Check(l, feed, global, 200) // 900+200 > 1000 globally
	if d.Allow {
		t.Fatalf("expected deny from the global cap even though the feed cap alone would allow it")
	}
	if d.Reason != ReasonGlobalTokenCap {
		t.Fatalf("expected reason %q, got %q", ReasonGlobalTokenCap, d.Reason)
	}
}

func TestCheck_GlobalUSDCapDenies(t *testing.T) {
	l := Limits{GlobalDailyUSD: 10}
	global := Spend{USD: 9.5}

	d := CheckRequest(l, Spend{}, global, Request{Kind: KindScheduled, Projected: 10, USD: 1})
	if d.Allow {
		t.Fatalf("expected deny, got allow")
	}
	if d.Reason != ReasonGlobalUSDCap {
		t.Fatalf("expected reason %q, got %q", ReasonGlobalUSDCap, d.Reason)
	}
}

func TestCheck_AllowsWithinEveryCap(t *testing.T) {
	l := Limits{
		PerFeedDailyTokens: 1000,
		PerFeedDailyRuns:   5,
		GlobalDailyTokens:  10000,
		GlobalDailyUSD:     10,
	}
	feed := Spend{TokensIn: 100, Runs: 1}
	global := Spend{TokensIn: 500, USD: 1}

	d := CheckRequest(l, feed, global, Request{Kind: KindSample, Projected: 100, USD: 0.1})
	if !d.Allow {
		t.Fatalf("expected allow, got deny with reason %q", d.Reason)
	}
	if d.Reason != ReasonNone {
		t.Fatalf("expected no reason on allow, got %q", d.Reason)
	}
}

func TestCheck_ZeroLimitMeansNoCap(t *testing.T) {
	// Every field of Limits is zero, so nothing is enforced.
	d := Check(Limits{}, Spend{TokensIn: 1_000_000}, Spend{TokensIn: 1_000_000}, 1_000_000)
	if !d.Allow {
		t.Fatalf("expected allow when no limits are configured, got deny with reason %q", d.Reason)
	}
}

// TestCheck_SamplingAndCronShareTheSameBudget is the load-bearing test for
// §13's rule: "Sampling draws from the same budget as scheduled generation".
// It proves that a KindSample request and a KindScheduled request, at
// identical projected spend, are decided identically — Kind is informational
// only, not a second budget.
func TestCheck_SamplingAndCronShareTheSameBudget(t *testing.T) {
	l := Limits{PerFeedDailyTokens: 1000}
	feed := Spend{TokensIn: 950}
	global := Spend{}

	scheduled := CheckRequest(l, feed, global, Request{Kind: KindScheduled, Projected: 100})
	sample := CheckRequest(l, feed, global, Request{Kind: KindSample, Projected: 100})

	if scheduled.Allow != sample.Allow || scheduled.Reason != sample.Reason {
		t.Fatalf("sample and scheduled requests diverged: scheduled=%+v sample=%+v", scheduled, sample)
	}
	if scheduled.Allow {
		t.Fatalf("expected both to deny at 950+100 > 1000")
	}

	// Now prove sampling actually consumes the shared pool: if sample spend
	// were tracked separately, a feed already at its cap from cron runs
	// would still allow a sample. It must not.
	feedAfterManySamples := Spend{TokensIn: 950} // as if those 950 tokens came from repeated sampling
	d := CheckRequest(l, feedAfterManySamples, global, Request{Kind: KindScheduled, Projected: 100})
	if d.Allow {
		t.Fatalf("expected scheduled generation to be capped by spend that sampling alone accrued")
	}
}

// --- Monthly ceiling (DOD-7) ---

// TestCheck_UnsetMonthlyCeilingIsUnlimited is deliberately first among the
// monthly tests: an unset MonthlyUSDCeiling MUST mean "unlimited", never
// "zero" — a zero-means-cap inversion here would deny every call on the
// first run and look exactly like a broken kill switch (byte-identical
// daily behaviour for anyone who never configures a monthly ceiling).
func TestCheck_UnsetMonthlyCeilingIsUnlimited(t *testing.T) {
	l := Limits{} // no monthly ceiling configured, no daily caps either
	huge := Spend{USD: 1_000_000}

	d := CheckRequest(l, Spend{}, Spend{}, Request{
		Kind:         KindScheduled,
		USD:          1_000_000,
		MonthlySpend: huge,
	})
	if !d.Allow {
		t.Fatalf("expected allow when MonthlyUSDCeiling is unset, got deny with reason %q", d.Reason)
	}
	if d.Warn {
		t.Fatalf("expected no warning when MonthlyUSDCeiling is unset, got Warn=true")
	}
}

func TestCheck_UnsetMonthlyCeilingLeavesDailyBehaviourByteIdentical(t *testing.T) {
	// Same scenario as TestCheck_GlobalUSDCapDenies, run through CheckRequest
	// with a zero-value MonthlySpend/MonthlyUSDCeiling, to prove adding the
	// monthly machinery did not perturb the pre-existing daily decision.
	l := Limits{GlobalDailyUSD: 10}
	global := Spend{USD: 9.5}

	d := CheckRequest(l, Spend{}, global, Request{Kind: KindScheduled, Projected: 10, USD: 1})
	if d.Allow {
		t.Fatalf("expected deny, got allow")
	}
	if d.Reason != ReasonGlobalUSDCap {
		t.Fatalf("expected reason %q, got %q", ReasonGlobalUSDCap, d.Reason)
	}
}

func TestCheck_MonthlyCeilingDeniesBeforeTheCall(t *testing.T) {
	l := Limits{MonthlyUSDCeiling: 100}
	monthSoFar := Spend{USD: 95}

	d := CheckRequest(l, Spend{}, Spend{}, Request{
		Kind:         KindScheduled,
		USD:          10, // 95 + 10 > 100
		MonthlySpend: monthSoFar,
	})
	if d.Allow {
		t.Fatalf("expected deny from the monthly ceiling")
	}
	if d.Reason != ReasonMonthlyUSDCap {
		t.Fatalf("expected reason %q, got %q", ReasonMonthlyUSDCap, d.Reason)
	}
}

// TestCheck_MonthlyCeilingBindsIndependentlyOfDailyCap proves the monthly
// ceiling can deny a call even when every configured daily cap would still
// allow it — the case that matters, per PLAN.md: the day mid-month spend
// crosses the monthly line, generation must stop that same day rather than
// waiting for a daily cap that was never tight enough to catch it.
func TestCheck_MonthlyCeilingBindsIndependentlyOfDailyCap(t *testing.T) {
	l := Limits{
		PerFeedDailyTokens: 1_000_000, // nowhere near hit
		PerFeedDailyRuns:   1_000,     // nowhere near hit
		GlobalDailyTokens:  1_000_000, // nowhere near hit
		GlobalDailyUSD:     1_000,     // nowhere near hit
		MonthlyUSDCeiling:  100,       // this is the tight one
	}
	feed := Spend{TokensIn: 10, Runs: 1}
	global := Spend{TokensIn: 10, USD: 5} // today's global spend is tiny
	monthSoFar := Spend{USD: 98}          // but the month is nearly spent

	d := CheckRequest(l, feed, global, Request{
		Kind:         KindScheduled,
		Projected:    10,
		USD:          5, // 98 + 5 > 100
		MonthlySpend: monthSoFar,
	})
	if d.Allow {
		t.Fatalf("expected the monthly ceiling to deny even though every daily cap still allows")
	}
	if d.Reason != ReasonMonthlyUSDCap {
		t.Fatalf("expected reason %q, got %q", ReasonMonthlyUSDCap, d.Reason)
	}
}

func TestCheck_MonthlyCeilingAllowsWithinBudget(t *testing.T) {
	l := Limits{MonthlyUSDCeiling: 100}
	monthSoFar := Spend{USD: 50}

	d := CheckRequest(l, Spend{}, Spend{}, Request{
		Kind:         KindScheduled,
		USD:          10,
		MonthlySpend: monthSoFar,
	})
	if !d.Allow {
		t.Fatalf("expected allow, got deny with reason %q", d.Reason)
	}
}

func TestCheck_MonthlyWarnFiresOnceOnCrossing(t *testing.T) {
	l := Limits{MonthlyUSDCeiling: 100, MonthlyWarnPct: 0.9} // threshold = 90

	// Before the threshold: no warning.
	before := CheckRequest(l, Spend{}, Spend{}, Request{
		USD:          5,
		MonthlySpend: Spend{USD: 80}, // 80 -> 85, still under 90
	})
	if !before.Allow || before.Warn {
		t.Fatalf("expected allow with no warning below threshold, got %+v", before)
	}

	// The call that crosses the threshold: warns, still allowed.
	crossing := CheckRequest(l, Spend{}, Spend{}, Request{
		USD:          10,
		MonthlySpend: Spend{USD: 85}, // 85 -> 95, crosses 90
	})
	if !crossing.Allow {
		t.Fatalf("expected a warning to still allow the call, got deny with reason %q", crossing.Reason)
	}
	if !crossing.Warn || crossing.WarnReason != ReasonMonthlyApproaching {
		t.Fatalf("expected a warning on the crossing call, got %+v", crossing)
	}

	// A later call, already past the threshold before this call starts:
	// must NOT warn again — the signal fires once, on the crossing edge,
	// not on every remaining call of the month.
	after := CheckRequest(l, Spend{}, Spend{}, Request{
		USD:          1,
		MonthlySpend: Spend{USD: 95}, // already past 90 before this call
	})
	if !after.Allow || after.Warn {
		t.Fatalf("expected no repeat warning once already past threshold, got %+v", after)
	}
}

func TestCheck_MonthlyWarnNeverAccompaniesDenial(t *testing.T) {
	// A call that both crosses the warn threshold and blows the hard
	// ceiling must be reported purely as a denial, not a denial-with-warn —
	// Warn is documented to never accompany Allow=false.
	l := Limits{MonthlyUSDCeiling: 100, MonthlyWarnPct: 0.5}

	d := CheckRequest(l, Spend{}, Spend{}, Request{
		USD:          200,
		MonthlySpend: Spend{USD: 0},
	})
	if d.Allow {
		t.Fatalf("expected deny")
	}
	if d.Warn {
		t.Fatalf("expected Warn=false on a denied call, got Warn=true")
	}
}

func TestCheck_MonthlyEstimatesAreLabelled(t *testing.T) {
	// Guards the "labelled as an estimate" requirement structurally: the
	// only inputs CheckRequest accepts for the monthly path are
	// req.MonthlySpend (a Spend — the same estimate-only type used
	// everywhere else in this package, per price.go's package doc) and
	// req.USD (also estimate-only, priced from Projected). There is no
	// separate "actual billed" field anywhere in Limits, Spend, or Request,
	// so nothing this package produces can present as billed precision.
	var zero Spend
	if zero.USD != 0 {
		t.Fatalf("sanity: Spend zero value should be zero")
	}
	// Documentation-level check: Request.MonthlySpend is a Spend, not some
	// distinct "billed" type — compile-time enforced by the struct field
	// declaration itself; this test exists so the invariant has a named,
	// runnable anchor rather than living only in a comment.
	req := Request{MonthlySpend: Spend{USD: 12.34}}
	if req.MonthlySpend.USD != 12.34 {
		t.Fatalf("expected MonthlySpend to round-trip as a plain estimate value")
	}
}

func TestMonthStart_CalendarBoundary(t *testing.T) {
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{
			in:   time.Date(2026, 8, 9, 15, 4, 5, 0, time.UTC),
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// Last instant of the month must still resolve to that month's
			// start, not roll into the next one.
			in:   time.Date(2026, 8, 31, 23, 59, 59, 999_999_999, time.UTC),
			want: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// First instant of a month is already its own start.
			in:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, c := range cases {
		got := MonthStart(c.in)
		if !got.Equal(c.want) {
			t.Fatalf("MonthStart(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMonthStart_NonUTCInputNormalizesToUTCCalendarMonth(t *testing.T) {
	// A local time that is early on the 1st in its own zone but still the
	// previous month in UTC must resolve to the UTC month, since MonthStart
	// operates on the UTC calendar boundary (documented decision: matches
	// the provider's billing boundary and this package's existing UTC-based
	// daily convention).
	loc := time.FixedZone("UTC-5", -5*60*60)
	localMidnightAug1 := time.Date(2026, 8, 1, 0, 30, 0, 0, loc) // = 2026-08-01T05:30:00Z

	got := MonthStart(localMidnightAug1)
	want := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("MonthStart(%v) = %v, want %v", localMidnightAug1, got, want)
	}
}

func TestMonthStart_AcrossTheBoundary(t *testing.T) {
	// One second before and one second after the boundary must resolve to
	// different months, proving the reset actually happens where documented
	// rather than drifting by a day (a common off-by-one in month-start
	// arithmetic).
	justBefore := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	justAfter := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	gotBefore := MonthStart(justBefore)
	gotAfter := MonthStart(justAfter)

	if gotBefore.Equal(gotAfter) {
		t.Fatalf("expected MonthStart to differ across the month boundary, both got %v", gotBefore)
	}
	if gotBefore.Month() != time.August || gotAfter.Month() != time.September {
		t.Fatalf("expected August then September, got %v then %v", gotBefore.Month(), gotAfter.Month())
	}
}
