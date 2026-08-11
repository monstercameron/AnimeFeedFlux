package e2e

import (
	"context"
	"io"
	"strings"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// --- novelty wiring: reproduces cmd/animefeedflux/wire.go's noveltyAdapter
// (an unexported type in package main this suite cannot import) against the
// REAL item_embeddings table, so the corpus a second run compares against is
// whatever the first run's own CommitRun actually persisted — never a
// hand-seeded row. See app.go's package doc for why this kind of
// reproduction, rather than a shortcut, is this suite's established pattern
// for a real gap between what exists and what nothing wires together yet. ---

// novEmbedder is a fixed internal/novelty.FakeEmbedder: deterministic,
// content-addressed vectors with no network call (RULE-1), so "the exact
// same title+summary text embeds to the exact same vector" is guaranteed —
// which is what makes a repeat a REAL cosine-similarity duplicate rather
// than one asserted by construction.
var novEmbedder = novelty.NewFakeEmbedder(16)

// novThreshold matches this suite's other fixtures' NoveltySettings
// (SimilarityThreshold: 0.9) — high enough that only a near-identical
// candidate trips it.
const novThreshold = 0.9

// novStoreAdapter is runStoreAdapter (killswitch_test.go) plus the one thing
// it does not do: persist the novelty vector CommitRun's embeddings argument
// carries, converting generate.ItemEmbedding to store.ItemEmbedding
// field-for-field. Without this, EmbeddingsForFeed's corpus would stay empty
// forever and the gate would never find anything to compare against, no
// matter how real the rest of the wiring is.
type novStoreAdapter struct{ runStoreAdapter }

func (a novStoreAdapter) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	runID, err := a.st.StartRun(ctx, run.FeedID, run.Trigger, "e2e-novelty")
	if err != nil {
		return err
	}
	summary := store.RunSummary{
		TokensIn: run.TokensIn, TokensOut: run.TokensOut, CostUSD: run.EstCostUSD,
		ItemsRejected: run.ItemsRejected, RejectReasons: run.RejectReasons,
	}
	embeddings := make([]store.ItemEmbedding, len(run.Embeddings))
	for i, e := range run.Embeddings {
		embeddings[i] = store.ItemEmbedding{ItemKey: e.ItemKey, Vector: e.Vector}
	}
	switch run.Status {
	case generate.StatusCompleted:
		return a.st.CommitRun(ctx, runID, items, summary, embeddings...)
	case generate.StatusSkipped:
		// See killswitch_test.go's runStoreAdapter.CommitRun for why this is
		// SkipRun(ctx, runID, run.ErrorKind, summary), not a bare
		// SkipRun(ctx, runID, run.Error): a skipped run's real reject
		// reasons and token/cost spend must not be silently dropped.
		return a.st.SkipRun(ctx, runID, run.ErrorKind, summary)
	default:
		return a.st.FailRun(ctx, runID, run.ErrorKind, run.Error, summary)
	}
}

// noveltyGateAdapter implements generate.Novelty + generate.VectorNovelty
// over a REAL novelty.Gate: it loads feedID's comparison corpus from the
// real store (store.EmbeddingsForFeed) and delegates the actual cosine
// comparison to novelty.Gate.Check — the same package the production
// adapter delegates to, not a re-implementation of the comparison itself.
type noveltyGateAdapter struct {
	st        *store.Store
	embedder  novelty.Embedder
	threshold float64
}

func (n noveltyGateAdapter) Check(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, err error) {
	dup, nearest, score, _, err = n.check(ctx, feedID, text)
	return dup, nearest, score, err
}

func (n noveltyGateAdapter) CheckVector(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, vec novelty.Vector, err error) {
	return n.check(ctx, feedID, text)
}

func (n noveltyGateAdapter) check(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, vec novelty.Vector, err error) {
	const window = 500
	corpus, err := n.st.EmbeddingsForFeed(ctx, feedID, window)
	if err != nil {
		return false, "", 0, novelty.Vector{}, err
	}
	capture := &novCapturingEmbedder{Embedder: n.embedder}
	gate := novelty.Gate{Embedder: capture, Threshold: n.threshold, Window: window}
	dup, nearest, score, err = gate.Check(ctx, text, corpus)
	if err != nil {
		return false, "", 0, novelty.Vector{}, err
	}
	return dup, nearest, score, capture.vec, nil
}

// novCapturingEmbedder wraps an Embedder and records the single vector its
// one Embed call produced, mirroring wire.go's capturingEmbedder — the only
// way to recover the vector novelty.Gate.Check computes internally and
// otherwise discards, so it can be persisted via CommitRun's embeddings
// argument instead of embedding the same text a second time.
type novCapturingEmbedder struct {
	novelty.Embedder
	vec novelty.Vector
}

func (c *novCapturingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := c.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) > 0 {
		c.vec = novelty.Vector{Model: c.Embedder.Model(), Dim: c.Embedder.Dim(), Vec: vecs[0]}
	}
	return vecs, nil
}

// noveltyRun drives one real generate.Run for feedID with the novelty gate
// wired in, exactly as killswitch_test.go's realExecutor does for the kill
// switch (same feedLookup, same runStoreAdapter shape) plus the Novelty dep.
func noveltyRun(ctx context.Context, app *App, feedID int64, trigger string) (generate.RunResult, error) {
	feed, spec, err := feedLookup{app.Store}.GetFeedForSample(ctx, feedID)
	if err != nil {
		return generate.RunResult{}, err
	}
	spec.Trigger = trigger
	deps := generate.Deps{
		Store:    novStoreAdapter{runStoreAdapter{st: app.Store}},
		Novelty:  noveltyGateAdapter{st: app.Store, embedder: novEmbedder, threshold: novThreshold},
		Provider: app.Provider,
		IDs:      app.idSource,
		Now:      app.Clock.Now,
	}
	return generate.Run(ctx, deps, feed, spec)
}

// TestNoveltyGateRejectsARealRepeat proves PLAN.md §9 step 5's dedup gate
// against a corpus the pipeline itself built, not a hand-seeded one: the
// FIRST run publishes an item and its own CommitRun writes the embedding a
// real novelty check would compare against; the SECOND run's provider
// returns the EXACT SAME title+summary text, and the real novelty.Gate
// (deterministic FakeEmbedder, so the same text really does hash to the
// same vector) must reject it as a duplicate — a run that is SKIPPED, not
// silently republished, with the item count over the real ItemService
// unchanged.
func TestNoveltyGateRejectsARealRepeat(t *testing.T) {
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

	const slug = "e2e-novelty-repeat-feed"
	createResp, err := login.Clients.Feed.Create(ctx, &affv1.FeedServiceCreateRequest{
		Feed: &affv1.Feed{
			Slug: slug, Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
			Title: "E2E Novelty Fixture", Description: "d", Language: "en",
			Spec: &affv1.FeedSpec{
				Cron: "0 12 * * *", Timezone: "UTC", ItemsPerRun: 1, FeedWindow: 50,
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

	repeatedResult := llm.Result{Items: []llm.GeneratedItem{{
		Title:       "A trivia question the model will produce twice in a row",
		SummaryText: "The exact same summary text both times, on purpose.",
		BodyHTML:    `<p>Body with an <a href="https://example.com/a">absolute link</a></p>`,
		AnswerHTML:  "<p>Answer.</p>",
	}}}

	// --- first run: empty corpus, the item is genuinely novel, publishes ---
	app.Provider.QueueResult(repeatedResult)
	first, err := noveltyRun(ctx, app, feedID, "manual")
	if err != nil {
		t.Fatalf("first noveltyRun: %v", err)
	}
	if first.Run.Status != generate.StatusCompleted {
		t.Fatalf("first run status = %q, want %q (first occurrence must publish)", first.Run.Status, generate.StatusCompleted)
	}
	if len(first.Items) != 1 {
		t.Fatalf("first run published %d items, want 1", len(first.Items))
	}

	// --- second run: the SAME text, real corpus now has one entry. The
	// generative retry loop (PLAN.md §9 step 5) makes up to
	// 1+MaxNoveltyRetries provider calls if every attempt is rejected, so
	// queue the identical result enough times to cover every attempt this
	// feed's spec allows (feedspec.Defaults().Novelty.MaxRetries == 3, i.e.
	// up to 4 attempts) rather than assume exactly one call is made ---
	for i := 0; i < 4; i++ {
		app.Provider.QueueResult(repeatedResult)
	}
	callsBefore := app.Provider.GenerateCallCount()
	second, err := noveltyRun(ctx, app, feedID, "manual")
	if err != nil {
		t.Fatalf("second noveltyRun: %v", err)
	}
	if second.Run.Status != generate.StatusSkipped {
		t.Fatalf("second run status = %q, want %q (a real repeat must be rejected, not republished)", second.Run.Status, generate.StatusSkipped)
	}
	if len(second.Items) != 0 {
		t.Fatalf("second run reports %d published items, want 0", len(second.Items))
	}
	if app.Provider.GenerateCallCount() == callsBefore {
		t.Fatal("second run made no provider call at all — the gate must still ask the model before rejecting its answer")
	}

	// --- ASSERT over the real public surface, never the store directly:
	// ItemService.List for this feed must still report exactly the one item
	// the FIRST run published. A novelty bug that let the duplicate through
	// would show up here as 2, which is the failure this test exists to
	// catch — a store-level read could pass while a real subscriber still
	// received a duplicate item, which is exactly the trap this suite's own
	// rules warn against. ---
	listResp, err := login.Clients.Item.List(ctx, &affv1.ItemServiceListRequest{FeedId: feedID})
	if err != nil {
		t.Fatalf("ItemService.List: %v", err)
	}
	if len(listResp.GetItems()) != 1 {
		t.Fatalf("feed has %d items after a rejected repeat, want 1 (the gate must not have let the duplicate through)", len(listResp.GetItems()))
	}

	// The published feed itself must contain the title exactly once too —
	// the actual thing a subscriber would receive, not an inference from a
	// row count.
	resp := app.FetchFeed(t, slug, "xml")
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading feed body: %v", err)
	}
	title := "A trivia question the model will produce twice in a row"
	if got := strings.Count(string(body), title); got != 1 {
		t.Fatalf("published feed contains the title %d times, want exactly 1:\n%s", got, body)
	}
}
