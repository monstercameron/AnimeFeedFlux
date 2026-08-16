package generate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// TestWatchModeEmptyBatchIsQuietSkip pins the whole watch contract in one
// run: an empty batch is answered with StatusSkipped/"nothing_noteworthy",
// after EXACTLY ONE model call — no §9.3 repair (which would feed "you
// returned nothing" back as an error) and no novelty re-roll (which would
// ask the model to change its answer), both of which would badger it into
// inventing an event.
func TestWatchModeEmptyBatchIsQuietSkip(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: nil})

	store := &stubStore{}
	deps := newDeps(store, &stubNovelty{}, nil, provider, now)

	spec := testSpec()
	spec.WatchMode = true
	spec.MaxNoveltyRetries = 3 // must NOT translate into retries on a quiet day

	result, err := Run(context.Background(), deps, testFeedGenerative(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != StatusSkipped {
		t.Fatalf("Status = %q, want %q", result.Run.Status, StatusSkipped)
	}
	if result.Run.ErrorKind != "nothing_noteworthy" {
		t.Fatalf("ErrorKind = %q, want nothing_noteworthy", result.Run.ErrorKind)
	}
	if got := provider.GenerateCallCount(); got != 1 {
		t.Fatalf("Generate called %d times, want exactly 1 (no repair, no novelty re-roll)", got)
	}
	if len(result.Items) != 0 {
		t.Fatalf("a quiet watch run published %d items", len(result.Items))
	}
}

// TestGroundedWatchWithNoCandidatesSkipsWithoutSpending: the grounded watch
// loop's cheap half — the scheduled fetch IS the check of the outside
// world, and when it surfaces nothing there is nothing to judge, so the run
// quiet-skips before a single model call is made.
func TestGroundedWatchWithNoCandidatesSkipsWithoutSpending(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	store := &stubStore{}
	deps := newDeps(store, &stubNovelty{}, &stubFetcher{}, provider, now)

	spec := testSpec()
	spec.WatchMode = true

	result, err := Run(context.Background(), deps, testFeedGrounded(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != StatusSkipped || result.Run.ErrorKind != "nothing_noteworthy" {
		t.Fatalf("status=%q kind=%q, want skipped/nothing_noteworthy", result.Run.Status, result.Run.ErrorKind)
	}
	if got := provider.GenerateCallCount(); got != 0 {
		t.Fatalf("Generate called %d times on a zero-candidate check, want 0", got)
	}
}

// TestWatchModeStillPublishesWhenSomethingHappens: the mode changes what an
// empty answer means, never what a real one does.
func TestWatchModeStillPublishesWhenSomethingHappens(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable event headline"),
	}})

	store := &stubStore{}
	deps := newDeps(store, &stubNovelty{}, nil, provider, now)
	spec := testSpec()
	spec.WatchMode = true

	result, err := Run(context.Background(), deps, testFeedGenerative(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != StatusCompleted || len(result.Items) != 1 {
		t.Fatalf("status=%q items=%d, want completed with 1 item", result.Run.Status, len(result.Items))
	}
}

// TestWatchModeSystemPromptCarriesTheEscape: the empty-batch permission is
// the pipeline's, appended to whatever the recipe wrote, so a recipe author
// never has to know the contract exists.
func TestWatchModeSystemPromptCarriesTheEscape(t *testing.T) {
	spec := testSpec()
	spec.SystemPrompt = "recipe system prompt"

	if got := effectiveSystem(spec); got != "recipe system prompt" {
		t.Fatalf("non-watch system prompt changed: %q", got)
	}
	spec.WatchMode = true
	got := effectiveSystem(spec)
	if !strings.HasPrefix(got, "recipe system prompt") || !strings.Contains(got, "empty") {
		t.Fatalf("watch system prompt missing recipe text or escape clause: %q", got)
	}
}

// TestNonWatchEmptyBatchStillRepairs: outside watch mode, an empty batch
// remains malformed output and §9.3's one repair call still fires.
func TestNonWatchEmptyBatchStillRepairs(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: nil})
	provider.QueueResult(llm.Result{Items: nil})

	store := &stubStore{}
	deps := newDeps(store, &stubNovelty{}, nil, provider, now)
	spec := testSpec()
	spec.MaxNoveltyRetries = 0

	if _, err := Run(context.Background(), deps, testFeedGenerative(), spec); err == nil {
		t.Fatal("expected the malformed-output failure a non-watch empty batch produces")
	}
	if got := provider.GenerateCallCount(); got != 2 {
		t.Fatalf("Generate called %d times, want 2 (base + repair)", got)
	}
}
