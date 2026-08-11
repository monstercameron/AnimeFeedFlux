package e2e

import (
	"context"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// TestJ5DiagnoseARunThatRejectsEverything drives PLAN.md §22 J5 end to end:
// force a real run whose every candidate is rejected, then read it back
// through the REAL RunService (over the real bridge, not the store
// directly) and assert the reject reasons are SPECIFIC — a per-field
// breakdown an admin could act on — not merely a count. "A count is not a
// diagnosis" is the literal wording this test is checking.
//
// The two rejections are deliberately from TWO DIFFERENT subsystems (plain
// contract validation, and the novelty gate) rather than the same rule
// twice: a candidate that is contract-valid but a real duplicate of
// something already published (reuses the noveltyRun wiring from
// novelty_test.go, so the corpus is real, not hand-seeded), alongside a
// candidate that fails contract validation outright. Mixing them this way
// also sidesteps a real pitfall discovered while writing this test: if
// EVERY candidate in one model response fails plain contract validation
// (runner.go's runAttempt), that is not treated as "reject and retry" —
// it is "malformed output", which triggers a same-attempt repair call and,
// if that also produces zero valid items, fails the run outright
// (ErrMalformedOutput) rather than skipping it. Keeping one candidate
// contract-valid per attempt (it still gets rejected, but by the novelty
// gate afterward) keeps this a genuine "rejected everything, skipped"
// run — the case PLAN.md §22 J5 actually describes — rather than a
// malformed-output failure.
func TestJ5DiagnoseARunThatRejectsEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: full end-to-end suite skipped in -short mode")
	}

	ctx := context.Background()
	app := New(t)

	totpSecret, err := app.InitAdmin(ctx, adminPassword)
	if err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	login, err := app.Login(ctx, adminPassword, totpSecret)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer login.Close()

	const slug = "e2e-j5-bad-run-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E J5 Fixture", Description: "d", Language: "en",
			Spec: &affv1.FeedSpec{
				Cron: "0 12 * * *", Timezone: "UTC", ItemsPerRun: 2, FeedWindow: 50,
				Model: "gpt-4o-mini", SystemPromptTemplate: "sys", UserPromptTemplate: "user",
				Novelty:          &affv1.NoveltySettings{NoveltyWindowItems: 500, SimilarityThreshold: novThreshold},
				DailyTokenBudget: 1000000, DailyRunBudget: 50,
			},
		},
	})
	if err != nil {
		t.Fatalf("FeedService.Create: %v", err)
	}
	feedID := createResp.GetFeed().GetId()

	// --- seed a real corpus entry: an ordinary, valid, novel run through
	// the SAME real novelty wiring the second run below uses ---
	const repeatedTitle = "A trivia question the second run will repeat verbatim"
	const repeatedSummary = "The exact same summary text the second run will repeat too."
	app.Provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{
		Title:       repeatedTitle,
		SummaryText: repeatedSummary,
		BodyHTML:    `<p>Body with an <a href="https://example.com/a">absolute link</a></p>`,
	}}})
	seed, err := noveltyRun(ctx, app, feedID, "manual")
	if err != nil {
		t.Fatalf("seeding noveltyRun: %v", err)
	}
	if seed.Run.Status != generate.StatusCompleted {
		t.Fatalf("seed run status = %q, want %q", seed.Run.Status, generate.StatusCompleted)
	}

	// --- the run under test: one candidate is a byte-identical repeat
	// (contract-valid, rejected by the real novelty gate), the other fails
	// contract validation outright (title under the 10-rune minimum) ---
	mixedBatch := llm.Result{Items: []llm.GeneratedItem{
		{
			Title:       repeatedTitle,
			SummaryText: repeatedSummary,
			BodyHTML:    `<p>Body with an <a href="https://example.com/a">absolute link</a></p>`,
		},
		{
			Title:       "Too short", // 9 runes, under titleMinLen
			SummaryText: "A perfectly fine summary that itself passes every other check.",
			BodyHTML:    `<p>Fine body with an <a href="https://example.com/x">absolute link</a></p>`,
		},
	}}
	// The generative retry loop (PLAN.md §9 step 5) retries up to
	// 1+MaxNoveltyRetries times whenever the published batch is empty, which
	// it will be here on every attempt (the repeat is always a duplicate of
	// the SAME seeded corpus entry, and the short title always fails the
	// same contract check). feedspec.Defaults().Novelty.MaxRetries == 3, so
	// queue enough identical attempts to cover the worst case.
	for i := 0; i < 4; i++ {
		app.Provider.QueueResult(mixedBatch)
	}

	result, err := noveltyRun(ctx, app, feedID, "manual")
	if err != nil {
		t.Fatalf("noveltyRun under test: %v", err)
	}
	if result.Run.Status != generate.StatusSkipped {
		t.Fatalf("run status = %q, want %q (every candidate was rejected, nothing to publish)", result.Run.Status, generate.StatusSkipped)
	}
	if len(result.Items) != 0 {
		t.Fatalf("run reports %d published items, want 0", len(result.Items))
	}

	// --- ASSERT over the real RunService, not the store: find the run and
	// read its diagnosis exactly as the History tab would. Two runs exist
	// for this feed now (the seed, and the one under test); this one is the
	// SKIPPED one. ---
	historyResp, err := login.Clients.Run.History(ctx, &affv1.RunServiceHistoryRequest{
		FeedId: feedID, Status: affv1.RunStatus_RUN_STATUS_SKIPPED,
	})
	if err != nil {
		t.Fatalf("RunService.History: %v", err)
	}
	if len(historyResp.GetRuns()) != 1 {
		t.Fatalf("skipped run history for feed %d has %d entries, want 1", feedID, len(historyResp.GetRuns()))
	}
	run := historyResp.GetRuns()[0]

	// items_added + items_rejected reconciles (BF-23's claim, re-checked
	// here over the RPC surface rather than assumed).
	if run.GetItemsAdded() != 0 {
		t.Fatalf("items_added = %d, want 0", run.GetItemsAdded())
	}
	if run.GetItemsRejected() == 0 {
		t.Fatal("items_rejected = 0, but every candidate this run saw was rejected — the run recorded no rejections at all")
	}

	// The diagnosis that actually matters: reject_reasons must name BOTH
	// specific, distinct failure modes, each with its own count — not one
	// opaque total. Get(run_id) is checked too (not just History), since
	// PLAN.md §22 J5's steps are "open history -> find the run -> read
	// status, error kind, and reject reasons", i.e. a drill-down, not just
	// a list row.
	getResp, err := login.Clients.Run.Get(ctx, &affv1.RunServiceGetRequest{RunId: run.GetId()})
	if err != nil {
		t.Fatalf("RunService.Get: %v", err)
	}
	reasons := getResp.GetRun().GetRejectReasons()
	if len(reasons) < 2 {
		t.Fatalf("reject_reasons has %d distinct entries, want at least 2 (a count alone is not a diagnosis): %+v", len(reasons), reasons)
	}
	byReason := map[string]int32{}
	for _, r := range reasons {
		byReason[r.GetReason()] += r.GetCount()
	}
	if byReason["title_too_short"] == 0 {
		t.Fatalf("reject_reasons %v does not name title_too_short, the actual, specific defect in the second candidate", byReason)
	}
	if byReason["novelty_duplicate"] == 0 {
		t.Fatalf("reject_reasons %v does not name novelty_duplicate, the actual, specific reason the first candidate was rejected", byReason)
	}

	// A run that spent tokens and money must still show it, even though it
	// published nothing (BF-25's claim, re-checked over the RPC surface).
	if run.GetTokensIn() <= 0 {
		t.Fatalf("tokens_in = %d, want > 0 (the prompt was built and sent on every attempt)", run.GetTokensIn())
	}
}
