package main

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/feedspec"
)

// generateSpecFrom is where a USD ceiling can quietly stop existing.
//
// A model with no row in the price table prices at zero, so every run it
// performs records est_cost_usd = 0; SpendSince sums that column and
// budget.CheckRequest compares the sum against GlobalDailyUSD and
// MonthlyUSDCeiling, neither of which a permanent zero can ever reach. The
// default configuration has a ceiling — DefaultGlobalDailySpendCeilingUSD is
// 5.0 — so this is the out-of-the-box state whenever the price table does not
// name the configured model, which a version bump or a typo is enough to
// cause.
//
// This test pins the mechanism rather than a policy: it asserts what the
// zero-price case actually produces today. Whichever way the refuse-or-warn
// question is settled, the fix has to change what this records.
func TestGenerateSpecFrom_UnknownModelPricesAtZero(t *testing.T) {
	prices := budget.NewTable()
	prices.Set(budget.Price{Model: "priced-model", InputPerMTok: 3, OutputPerMTok: 15})

	priced := generateSpecFrom(feedspec.Spec{
		Model: feedspec.ModelParams{Model: "priced-model"},
	}, "cron", prices, providerDefaults{})
	if priced.PriceInputPerMToken != 3 || priced.PriceOutputPerMToken != 15 {
		t.Fatalf("a priced model lost its rates: in=%v out=%v",
			priced.PriceInputPerMToken, priced.PriceOutputPerMToken)
	}

	// A recipe with NO pinned model inherits the settings default — and its
	// price lookup uses the RESOLVED model, not the blank recipe value
	// (2026-08-15: per-feed models are overrides on the global default).
	inherited := generateSpecFrom(feedspec.Spec{},
		"cron", prices, providerDefaults{Model: "priced-model", Effort: "quick"})
	if inherited.Model != "priced-model" || inherited.Effort != "quick" {
		t.Fatalf("defaults not inherited: model=%q effort=%q", inherited.Model, inherited.Effort)
	}
	if inherited.PriceInputPerMToken != 3 || inherited.PriceOutputPerMToken != 15 {
		t.Fatalf("inherited model not priced at its own rate: in=%v out=%v",
			inherited.PriceInputPerMToken, inherited.PriceOutputPerMToken)
	}
	// A pinned model stays pinned — the default never overrides an override.
	pinned := generateSpecFrom(feedspec.Spec{
		Model: feedspec.ModelParams{Model: "priced-model"},
	}, "cron", prices, providerDefaults{Model: "some-other-default"})
	if pinned.Model != "priced-model" {
		t.Fatalf("a pinned model was overridden by the default: %q", pinned.Model)
	}

	unpriced := generateSpecFrom(feedspec.Spec{
		Model: feedspec.ModelParams{Model: "model-nobody-entered"},
	}, "cron", prices, providerDefaults{})
	if unpriced.PriceInputPerMToken != 0 || unpriced.PriceOutputPerMToken != 0 {
		t.Fatalf("unexpected rates for an unpriced model: in=%v out=%v — if this now "+
			"refuses or defaults instead, update the gate comment in wire.go to match",
			unpriced.PriceInputPerMToken, unpriced.PriceOutputPerMToken)
	}

	// The consequence, stated as arithmetic: no token count produces a cost
	// that any ceiling can see.
	cost := float64(10_000_000)/1_000_000*unpriced.PriceInputPerMToken +
		float64(2_000_000)/1_000_000*unpriced.PriceOutputPerMToken
	if cost != 0 {
		t.Fatalf("expected 12M tokens to cost 0 at zero rates, got %v", cost)
	}
}
