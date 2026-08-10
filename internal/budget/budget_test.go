package budget

import "testing"

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
