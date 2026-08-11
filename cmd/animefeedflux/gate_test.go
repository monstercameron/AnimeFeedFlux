package main

import (
	"context"
	"strings"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// genGate.Allowed is the one place every scheduled run passes through before
// spending money. Only its kill-switch branch was tested; the budget branches
// — the ones that decide whether a runaway feed keeps billing — were not, and
// neither was the fail-closed behaviour that makes an unreadable database
// stop generation rather than wave it through.

func TestGateAllowsAHealthyFeed(t *testing.T) {
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true)

	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger()}
	allowed, reason := gate.Allowed(feedID)
	if !allowed {
		t.Fatalf("a fresh feed with generation enabled was refused: %q", reason)
	}
	if reason != "" {
		t.Errorf("an allowed run carries reason %q, want empty", reason)
	}
}

func TestGateFailsClosedOnAnUnknownFeed(t *testing.T) {
	// Failing OPEN here would mean a feed that was just deleted, or an id
	// from a stale schedule entry, generates anyway — spending against a
	// recipe nobody can read.
	st := openTestStore(t)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true)

	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger()}
	allowed, reason := gate.Allowed(99999)
	if allowed {
		t.Fatal("the gate allowed a run for a feed that does not exist")
	}
	if reason != "feed_lookup_failed" {
		t.Errorf("reason = %q, want feed_lookup_failed", reason)
	}
}

func TestGateRefusesAFeedSwitchedOffAfterBoot(t *testing.T) {
	// The scheduler's job set is built once, at boot, from a query that
	// filters `enabled = 1`. Switching a feed off in the admin UI therefore
	// changes a column the scheduler never re-reads — so before this check
	// existed the feed stayed in the map and kept generating, and kept
	// spending, until the process restarted. The global kill switch above is
	// a different control, and genExecutor.ExecuteRun never re-reads the
	// column either, so the gate is the only place this can be caught.
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true)
	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger()}

	if allowed, reason := gate.Allowed(feedID); !allowed {
		t.Fatalf("an enabled feed was refused before the switch was touched: %q", reason)
	}

	// Exactly what FeedService.SetEnabled writes.
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET enabled = 0 WHERE id = ?`, feedID); err != nil {
		t.Fatalf("disabling the feed: %v", err)
	}

	allowed, reason := gate.Allowed(feedID)
	if allowed {
		t.Fatal("a feed switched off in the UI was still allowed to generate — it would keep billing")
	}
	if reason != "feed_disabled" {
		t.Errorf("reason = %q, want feed_disabled (it lands on the run row and the metric label)", reason)
	}

	// And switching it back on takes effect just as immediately, without a
	// restart — otherwise the fix would trade one stale read for another.
	if _, err := st.Writer().ExecContext(t.Context(),
		`UPDATE feeds SET enabled = 1 WHERE id = ?`, feedID); err != nil {
		t.Fatalf("re-enabling the feed: %v", err)
	}
	if allowed, reason := gate.Allowed(feedID); !allowed {
		t.Errorf("a re-enabled feed stayed refused: %q", reason)
	}
}

func TestGateHonoursTheKillSwitchRowNotJustTheBootDefault(t *testing.T) {
	// Two ways generation can be off, and they are different code paths.
	//
	// AFF_GENERATION_ENABLED=false is the boot default and is already
	// covered. The switch an OPERATOR flips is the settings row that
	// SystemService.SetGenerationEnabled writes, and until now nothing
	// asserted the production gate against it: internal/e2e's kill-switch
	// journey flips the real switch but checks dispatch through its own
	// settingsGate stand-in, while the existing unit test here uses the
	// config default. Both halves were tested; the join was not — which is
	// the same shape as every real defect this codebase has had.
	//
	// §13's promise is that flipping this stops new generation while feeds
	// keep serving, so a gate that ignored the row would keep spending
	// against a switch the operator believes is off.
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true) // boot default: ENABLED
	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger()}

	if allowed, reason := gate.Allowed(feedID); !allowed {
		t.Fatalf("refused before the switch was touched: %q", reason)
	}

	writeGenerationSetting(t, st, `{"enabled":false}`)

	allowed, reason := gate.Allowed(feedID)
	if allowed {
		t.Fatal("the gate ignored the settings row — generation continues against a switch the operator turned off")
	}
	if reason != "generation_disabled" {
		t.Errorf("reason = %q, want generation_disabled", reason)
	}

	// And back on, without a restart.
	writeGenerationSetting(t, st, `{"enabled":true}`)
	if allowed, reason := gate.Allowed(feedID); !allowed {
		t.Errorf("re-enabling via the settings row did not take effect: %q", reason)
	}
}

// writeGenerationSetting writes the row SystemService.SetGenerationEnabled
// writes, with the same key and the same encoding/json shape wire.go reads.
func writeGenerationSetting(t *testing.T, st *store.Store, raw string) {
	t.Helper()
	if _, err := st.Writer().ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('generation', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, raw); err != nil {
		t.Fatalf("writing the generation setting: %v", err)
	}
}

func TestGateEnforcesTheMonthlyCeilingIndependentlyOfTheDailyOne(t *testing.T) {
	// A month of small, under-the-daily-cap spends is exactly the case the
	// monthly ceiling exists for, and exactly the case a daily cap cannot
	// see. Today's spend here is well under any daily limit.
	st := openTestStore(t)
	feedID := seedFeed(t, st, "trivia-daily", true)
	cfg := wireTestConfig(t, "127.0.0.1:0", "127.0.0.1:0", true)

	runID, err := st.StartRun(t.Context(), feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := st.CommitRun(t.Context(), runID, nil, store.RunSummary{CostUSD: 1.00}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	// Ceiling below what has already been spent this month.
	gate := &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger(), monthlyCeilingUSD: 0.50}
	allowed, reason := gate.Allowed(feedID)
	if allowed {
		t.Fatal("a run was allowed past the monthly ceiling")
	}
	if reason == "" {
		t.Error("the refusal carries no reason")
	}

	// And zero means unlimited, not "zero budget" — reading it the other way
	// would stop generation on the very first run of every deployment that
	// never set the variable.
	gate = &genGate{st: st, cfg: cfg, prices: budget.NewTable(), log: discardLogger(), monthlyCeilingUSD: 0}
	if allowed, reason := gate.Allowed(feedID); !allowed {
		t.Errorf("an unset monthly ceiling refused a run: %q", reason)
	}
}

// --- liveBaseURL -------------------------------------------------------------

func TestLiveBaseURLNormalisesAndFallsBack(t *testing.T) {
	// Every use appends a path beginning with "/", so a stored value ending
	// in one produces "https://host//feeds/x.xml" — a different URL to most
	// aggregators, and a feed whose guid changed is a feed that reposts
	// everything.
	var b liveBaseURL
	if got := b.get(); got != "" {
		t.Errorf("a fresh holder returned %q, want empty", got)
	}

	b.set("  https://feeds.example.com/  ")
	if got := b.get(); got != "https://feeds.example.com" {
		t.Errorf("get() = %q, want the trimmed, slash-stripped value", got)
	}

	b.set("https://feeds.example.com///")
	if got := b.get(); got != "https://feeds.example.com" {
		t.Errorf("get() = %q, want every trailing slash removed", got)
	}

	b.set("")
	if got := b.get(); got != "" {
		t.Errorf("clearing left %q", got)
	}
}

func TestBaseURLFnIsNilForANilHolder(t *testing.T) {
	// publish.Deps treats a nil BaseURLFn as "use the boot-time value", so
	// the adapter must hand back nil rather than a closure over a nil
	// pointer that panics on the first request.
	if fn := baseURLFn(nil); fn != nil {
		t.Error("baseURLFn(nil) returned a function")
	}
	b := &liveBaseURL{}
	b.set("https://feeds.example.com")
	fn := baseURLFn(b)
	if fn == nil {
		t.Fatal("baseURLFn returned nil for a real holder")
	}
	if got := fn(); got != "https://feeds.example.com" {
		t.Errorf("fn() = %q", got)
	}
}

func TestLoadPublishingAtBootSeedsFromTheStoredSetting(t *testing.T) {
	// Without this, a restart quietly reverts every feed URL to the env var
	// — the operator's saved setting silently stops applying at the least
	// observable moment.
	st := openTestStore(t)
	ctx := t.Context()

	var b liveBaseURL
	if err := loadPublishingAtBoot(ctx, st.Reader(), &b); err != nil {
		t.Fatalf("with no stored setting: %v", err)
	}
	if got := b.get(); got != "" {
		t.Errorf("an absent setting seeded %q", got)
	}

	// The stored blob is written with encoding/json (not protojson) by
	// internal/rpc, so the key is the Go struct tag's snake_case name. A
	// camelCase key here would parse cleanly into an EMPTY message and this
	// test would pass while seeding nothing.
	writePublishingSetting(t, st, `{"public_base_url":"https://feeds.example.com/"}`)
	if err := loadPublishingAtBoot(ctx, st.Reader(), &b); err != nil {
		t.Fatalf("loadPublishingAtBoot: %v", err)
	}
	if got := b.get(); got != "https://feeds.example.com" {
		t.Errorf("seeded %q, want the stored value with its trailing slash trimmed", got)
	}

	// A corrupt row is an error, not a silent revert to the env var: the
	// operator's configured host disappearing without a word is how every
	// guid changes overnight.
	writePublishingSetting(t, st, `{not json`)
	if err := loadPublishingAtBoot(ctx, st.Reader(), &b); err == nil {
		t.Error("a corrupt publishing setting was accepted")
	}
}

func writePublishingSetting(t *testing.T, st *store.Store, raw string) {
	t.Helper()
	if _, err := st.Writer().ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('publishing', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, raw); err != nil {
		t.Fatalf("writing the publishing setting: %v", err)
	}
}

// --- small wire helpers ------------------------------------------------------

func TestMidnightUTCIsTheStartOfTodayInUTC(t *testing.T) {
	// The daily budget window is defined in UTC; using local midnight would
	// give a deployment east of UTC a budget that resets mid-afternoon.
	when := time.Date(2026, 8, 11, 23, 59, 59, 0, time.FixedZone("EDT", -4*3600))
	got := midnightUTC(when)
	if got.Location() != time.UTC {
		t.Errorf("midnightUTC returned a %s time", got.Location())
	}
	if got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 || got.Nanosecond() != 0 {
		t.Errorf("midnightUTC = %s, want a midnight", got)
	}
	// 23:59 EDT on the 11th is 03:59 UTC on the 12th, so UTC midnight is the
	// 12th — not the 11th.
	if got.Day() != 12 {
		t.Errorf("midnightUTC = %s, want the UTC day (12th), not the local one", got)
	}
}

func TestCountRunsSinceCountsOnlyThisFeedAndWindow(t *testing.T) {
	st := openTestStore(t)
	ctx := t.Context()
	feedID := seedFeed(t, st, "trivia-daily", true)
	otherID := seedFeed(t, st, "other-feed", true)

	for _, id := range []int64{feedID, feedID, otherID} {
		runID, err := st.StartRun(ctx, id, "cron", "worker-1")
		if err != nil {
			t.Fatalf("StartRun: %v", err)
		}
		if err := st.CommitRun(ctx, runID, nil, store.RunSummary{}); err != nil {
			t.Fatalf("CommitRun: %v", err)
		}
	}

	got, err := countRunsSince(ctx, st.Reader(), feedID, midnightUTC(time.Now()))
	if err != nil {
		t.Fatalf("countRunsSince: %v", err)
	}
	if got != 2 {
		t.Errorf("countRunsSince = %d, want 2 (another feed's runs must not count against this one's budget)", got)
	}

	// A window that starts in the future counts nothing rather than
	// everything — the direction of the comparison matters, and getting it
	// backwards would make every budget look exhausted.
	future, err := countRunsSince(ctx, st.Reader(), feedID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("countRunsSince: %v", err)
	}
	if future != 0 {
		t.Errorf("countRunsSince over a future window = %d, want 0", future)
	}
}

func TestApplyPriceTableConvertsPerThousandToPerMillion(t *testing.T) {
	// The wire field is usd_per_1k_tokens; budget.Price is per MILLION.
	// Passing one straight into the other is a 1000x error, and in the
	// dangerous direction it makes every run look 1000x cheaper than it is,
	// so no ceiling ever trips.
	table := budget.NewTable()
	applyPriceTable(table, []*affv1.PriceEntry{
		{Model: "gpt-4o", UsdPer_1KTokensIn: 0.0025, UsdPer_1KTokensOut: 0.010},
		{Model: "   ", UsdPer_1KTokensIn: 1}, // no model name: skipped, not stored blank
		{Model: "  gpt-4.1  ", UsdPer_1KTokensIn: 0.002, UsdPer_1KTokensOut: 0.008},
	})

	p, ok := table.Lookup("gpt-4o")
	if !ok {
		t.Fatal("gpt-4o has no price")
	}
	if p.InputPerMTok != 2.5 || p.OutputPerMTok != 10 {
		t.Errorf("gpt-4o = %v in / %v out per Mtok, want 2.5 / 10", p.InputPerMTok, p.OutputPerMTok)
	}
	if _, ok := table.Lookup("gpt-4.1"); !ok {
		t.Error("a model name with surrounding whitespace was not trimmed before storing")
	}
	if _, ok := table.Lookup(""); ok {
		t.Error("an entry with no model name was stored under the empty string")
	}

	// Documented current behaviour, NOT an endorsement: this MERGES into the
	// live table rather than replacing it, so a rate the operator deleted in
	// Settings stays in force until the process restarts (boot builds the
	// table from scratch, so the two paths disagree). Keeping the old rate is
	// the safer direction — losing a rate means $0.0000 and no ceiling — but
	// the divergence itself is worth knowing about.
	applyPriceTable(table, []*affv1.PriceEntry{{Model: "gpt-4.1", UsdPer_1KTokensIn: 0.001}})
	if _, ok := table.Lookup("gpt-4o"); !ok {
		t.Error("gpt-4o's rate vanished; if that is now intended, this test and the doc comment need updating together")
	}

	// A nil table is a no-op rather than a panic: the sample path can be
	// wired without one.
	applyPriceTable(nil, []*affv1.PriceEntry{{Model: "gpt-4o"}})
}

func TestNoFetcherFailsLoudly(t *testing.T) {
	// Grounded-feed fetching is not wired. The stub must say so rather than
	// returning an empty candidate list, which the pipeline would read as
	// "the source published nothing today".
	got, err := noFetcher{}.Candidates(context.Background(), 7)
	if err == nil {
		t.Fatal("noFetcher returned no error")
	}
	if got != nil {
		t.Errorf("noFetcher returned %d candidates alongside its error", len(got))
	}
	if !strings.Contains(err.Error(), "not wired") {
		t.Errorf("error %q does not say the feature is unwired", err)
	}
}
