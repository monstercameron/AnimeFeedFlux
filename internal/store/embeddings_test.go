package store

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
)

func TestCommitRunPersistsEmbeddingsInTheSameTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	items := []model.Item{makeRunItem(feedID, "key-1", published)}
	vec := novelty.Vector{Model: "fake-embedding-test", Dim: 4, Vec: []float32{1, 0, 0, 0}}
	embedding := ItemEmbedding{ItemKey: "key-1", Vector: vec}

	if err := s.CommitRun(ctx, runID, items, RunSummary{}, embedding); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	corpus, err := s.EmbeddingsForFeed(ctx, feedID, 10)
	if err != nil {
		t.Fatalf("embeddings for feed: %v", err)
	}
	if len(corpus) != 1 {
		t.Fatalf("len(corpus) = %d, want 1", len(corpus))
	}
	if corpus[0].ID != "key-1" || corpus[0].Model != "fake-embedding-test" || corpus[0].Dim != 4 {
		t.Errorf("corpus[0] = %+v", corpus[0])
	}
	if len(corpus[0].Vec) != 4 || corpus[0].Vec[0] != 1 {
		t.Errorf("corpus[0].Vec = %v", corpus[0].Vec)
	}
}

func TestCommitRunEmbeddingForUnknownItemKeyFailsWholeCommit(t *testing.T) {
	// A caller bug (a typo'd or stale ItemKey) must not silently drop the
	// embedding — that is indistinguishable from the exact "nothing writes
	// it" bug this file exists to close. It fails loudly instead, and
	// nothing partial lands.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	items := []model.Item{makeRunItem(feedID, "key-1", published)}
	stray := ItemEmbedding{ItemKey: "does-not-exist", Vector: novelty.Vector{Model: "m", Dim: 1, Vec: []float32{1}}}

	if err := s.CommitRun(ctx, runID, items, RunSummary{}, stray); err == nil {
		t.Fatal("expected commit run to fail on an embedding for an unknown item key")
	}

	if status := runStatus(t, s, runID); status != "running" {
		t.Errorf("status after failed commit = %q, want running (nothing partial)", status)
	}
	if _, err := s.GetItem(ctx, "key-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("key-1 err = %v, want ErrNotFound — the whole commit should have rolled back", err)
	}
}

// TestNoveltyCheckFindsPriorEmbeddingsWrittenByCommitRun is the test the
// gap report asked for: a novelty check against a store where items have
// already been created must actually find their embeddings and flag a
// near-duplicate. Before embeddings.go existed, nothing in internal/store
// wrote item_embeddings, so the only test that could be written here was
// "novelty passes against an empty table" — which proves the gate runs, not
// that it defends anything. This proves the defence: the same text embedded
// twice must come back as a duplicate once the first item's embedding is
// actually in storage.
func TestNoveltyCheckFindsPriorEmbeddingsWrittenByCommitRun(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	embedder := novelty.NewFakeEmbedder(8)
	gate := novelty.Gate{Embedder: embedder, Threshold: 0.99, Window: 500}

	// First run: publish one item and persist its embedding, exactly like a
	// real generation pass would (embed candidate text, then commit it
	// alongside the item that carried it).
	firstText := "What year did Cowboy Bebop first air?"
	vecs, err := embedder.Embed(ctx, []string{firstText})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	runID, err := s.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	item := makeRunItem(feedID, "key-1", published)
	embedding := ItemEmbedding{
		ItemKey: "key-1",
		Vector:  novelty.Vector{Model: embedder.Model(), Dim: embedder.Dim(), Vec: vecs[0]},
	}
	if err := s.CommitRun(ctx, runID, []model.Item{item}, RunSummary{}, embedding); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	corpus, err := s.EmbeddingsForFeed(ctx, feedID, 500)
	if err != nil {
		t.Fatalf("embeddings for feed: %v", err)
	}
	if len(corpus) != 1 {
		t.Fatalf("len(corpus) = %d, want 1 — the write path is broken if this is 0", len(corpus))
	}

	// The SAME candidate text again: a real repeat, the exact case the gate
	// exists to catch.
	dup, nearestID, score, err := gate.Check(ctx, firstText, corpus)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !dup {
		t.Fatalf("dup = false for an identical repeat (score %v) — the gate is comparing against an empty or wrong corpus", score)
	}
	if nearestID != "key-1" {
		t.Errorf("nearestID = %q, want key-1", nearestID)
	}

	// A genuinely different candidate must NOT be flagged, or the gate is
	// just rejecting everything rather than actually discriminating.
	dup2, _, _, err := gate.Check(ctx, "What is the capital of France?", corpus)
	if err != nil {
		t.Fatalf("check (distinct candidate): %v", err)
	}
	if dup2 {
		t.Error("an unrelated candidate was flagged as a duplicate")
	}
}
