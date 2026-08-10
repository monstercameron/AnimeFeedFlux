package generate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
)

// --- stub Store/Novelty/Fetcher: no network, no database (RULE-1) ---

type stubStore struct {
	recentTitles []string
	recentErr    error

	newest    time.Time
	newestErr error

	commits     []stubCommit
	commitErr   error
	commitCalls int
}

type stubCommit struct {
	run   RunRecord
	items []model.Item
}

func (s *stubStore) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	return s.recentTitles, s.recentErr
}

func (s *stubStore) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	return s.newest, s.newestErr
}

func (s *stubStore) CommitRun(ctx context.Context, run RunRecord, items []model.Item) error {
	s.commitCalls++
	s.commits = append(s.commits, stubCommit{run: run, items: items})
	return s.commitErr
}

// stubNovelty flags every text in dupTitles as a duplicate; everything else
// is novel. errOnCheck forces Check to fail, for exercising the
// novelty-check-failed path.
type stubNovelty struct {
	dupAll    bool
	errOnCall error
	calls     int
}

func (n *stubNovelty) Check(ctx context.Context, feedID int64, text string) (bool, string, float64, error) {
	n.calls++
	if n.errOnCall != nil {
		return false, "", 0, n.errOnCall
	}
	if n.dupAll {
		return true, "an existing item", 0.99, nil
	}
	return false, "", 0, nil
}

type stubFetcher struct {
	candidates []sources.Candidate
	err        error
}

func (f *stubFetcher) Candidates(ctx context.Context, feedID int64) ([]sources.Candidate, error) {
	return f.candidates, f.err
}

// --- fixtures ---

func testFeedGenerative() model.Feed {
	return model.Feed{ID: 1, Slug: "trivia-daily", Title: "Anime Trivia Daily", Kind: model.KindGenerative, Timezone: "UTC"}
}

func testFeedGrounded() model.Feed {
	return model.Feed{ID: 2, Slug: "anime-news", Title: "Anime News", Kind: model.KindGrounded, Timezone: "UTC"}
}

func testSpec() Spec {
	return Spec{
		SystemPrompt:       "You write anime trivia.",
		UserPromptTemplate: "Write {{.ItemsPerRun}} item(s). Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		Model:              "gpt-test",
		ItemsPerRun:        2,
		MaxNoveltyRetries:  2,
	}
}

func groundedSpec() Spec {
	return Spec{
		SystemPrompt:       "You edit anime news.",
		UserPromptTemplate: "Rank these: {{range .Candidates}}{{.Title}} {{.URL}} {{end}}",
		Model:              "gpt-test",
		ItemsPerRun:        2,
	}
}

func validGeneratedItem(title string) llm.GeneratedItem {
	return llm.GeneratedItem{
		Title:       title,
		SummaryText: "A short plain-text summary of the item, safely under the cap.",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a>.</p>`,
		Tags:        []string{"anime"},
	}
}

func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func newDeps(store *stubStore, nov *stubNovelty, fetch *stubFetcher, provider llm.Provider, now time.Time) Deps {
	return Deps{
		Store:    store,
		Novelty:  nov,
		Fetcher:  fetch,
		Provider: provider,
		IDs:      ids.NewDeterministicSource(1),
		Now:      fixedNow(now),
	}
}

// --- tests ---

func TestRun_HappyPath_StrictlyIncreasingTimestamps(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable trivia title one"),
		validGeneratedItem("A perfectly reasonable trivia title two"),
	}})

	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)

	result, err := Run(context.Background(), deps, testFeedGenerative(), testSpec())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Run.Status != StatusCompleted {
		t.Fatalf("Status = %q, want %q", result.Run.Status, StatusCompleted)
	}
	if len(result.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(result.Items))
	}
	if !result.Items[1].PublishedAt.After(result.Items[0].PublishedAt) {
		t.Fatalf("items not strictly increasing: %v then %v", result.Items[0].PublishedAt, result.Items[1].PublishedAt)
	}
	if result.Items[0].PublishedAt.Equal(result.Items[1].PublishedAt) {
		t.Fatalf("items share a timestamp: %v", result.Items[0].PublishedAt)
	}
	if store.commitCalls != 1 {
		t.Fatalf("CommitRun called %d times, want 1", store.commitCalls)
	}
	for _, it := range result.Items {
		if it.ItemKey == "" {
			t.Error("item_key not assigned")
		}
		if it.ContentHash == "" {
			t.Error("content_hash not assigned")
		}
	}
}

func TestRun_TwoItemsNeverShareTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable trivia title alpha"),
		validGeneratedItem("A perfectly reasonable trivia title beta"),
	}})

	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)
	spec := testSpec()
	spec.ItemsPerRun = 2

	result, err := Run(context.Background(), deps, testFeedGenerative(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	seen := map[time.Time]bool{}
	for _, it := range result.Items {
		if seen[it.PublishedAt] {
			t.Fatalf("duplicate published_at %v", it.PublishedAt)
		}
		seen[it.PublishedAt] = true
	}
}

func TestSample_NeverCallsCommitRun(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable trivia title for sampling"),
	}})

	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)

	result, err := Sample(context.Background(), deps, testFeedGenerative(), testSpec())
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(result.Items))
	}
	if store.commitCalls != 0 {
		t.Fatalf("CommitRun called %d times, want 0 — a sampler that writes is the worst bug this design can have", store.commitCalls)
	}
}

func TestSample_NeverCallsCommitRun_EvenOnMalformedOutput(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	// Both the first attempt and the repair attempt return an invalid item
	// (title too short), so Sample should surface ErrMalformedOutput but,
	// crucially, still never touch Store.CommitRun.
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{Title: "short", SummaryText: "x", BodyHTML: "<p>x</p>"}}})
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{Title: "short", SummaryText: "x", BodyHTML: "<p>x</p>"}}})

	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)

	_, err := Sample(context.Background(), deps, testFeedGenerative(), testSpec())
	if !errors.Is(err, ErrMalformedOutput) {
		t.Fatalf("err = %v, want ErrMalformedOutput", err)
	}
	if store.commitCalls != 0 {
		t.Fatalf("CommitRun called %d times, want 0", store.commitCalls)
	}
}

func TestRun_MalformedOutput_OneRepairAttemptThenFails(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	// Both calls return a title that is too short: every item is rejected
	// both times, so this should count as "malformed" and fail after
	// exactly one repair attempt (two Generate calls total).
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{Title: "short", SummaryText: "x", BodyHTML: "<p>x</p>"}}})
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{{Title: "short", SummaryText: "x", BodyHTML: "<p>x</p>"}}})

	store := &stubStore{}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)

	result, err := Run(context.Background(), deps, testFeedGenerative(), testSpec())
	if !errors.Is(err, ErrMalformedOutput) {
		t.Fatalf("err = %v, want ErrMalformedOutput", err)
	}
	if result.Run.Status != StatusFailed {
		t.Fatalf("Status = %q, want %q", result.Run.Status, StatusFailed)
	}
	if provider.GenerateCallCount() != 2 {
		t.Fatalf("Generate called %d times, want exactly 2 (one repair attempt)", provider.GenerateCallCount())
	}
	if store.commitCalls != 1 {
		t.Fatalf("CommitRun called %d times, want 1 (a failed run is still a run row)", store.commitCalls)
	}
}

func TestRun_NoveltyRejection_RetriesThenSkips(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	spec := testSpec()
	spec.MaxNoveltyRetries = 2
	// One Generate call per attempt: 1 initial + 2 retries = 3 total.
	for i := 0; i < 3; i++ {
		provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{validGeneratedItem("A perfectly reasonable duplicate title")}})
	}

	store := &stubStore{}
	nov := &stubNovelty{dupAll: true} // every candidate is flagged a duplicate
	deps := newDeps(store, nov, nil, provider, now)

	result, err := Run(context.Background(), deps, testFeedGenerative(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (a skipped run is not a failed run)", err)
	}
	if result.Run.Status != StatusSkipped {
		t.Fatalf("Status = %q, want %q", result.Run.Status, StatusSkipped)
	}
	if len(result.Items) != 0 {
		t.Fatalf("got %d items, want 0", len(result.Items))
	}
	if provider.GenerateCallCount() != 3 {
		t.Fatalf("Generate called %d times, want 3 (1 + MaxNoveltyRetries)", provider.GenerateCallCount())
	}
	if store.commitCalls != 1 {
		t.Fatalf("CommitRun called %d times, want 1", store.commitCalls)
	}
	if store.commits[0].run.RejectReasons[ReasonNoveltyDuplicate] != 3 {
		t.Fatalf("RejectReasons[novelty_duplicate] = %d, want 3", store.commits[0].run.RejectReasons[ReasonNoveltyDuplicate])
	}
}

func TestRun_Grounded_LinkNotInCandidates_DroppedAndCounted(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	candidateURL := "https://news.example.com/real-article"

	provider := llm.NewFake()
	good := validGeneratedItem("A perfectly reasonable grounded headline one")
	good.Link = candidateURL
	good.SourceName = "Example News"
	bad := validGeneratedItem("A perfectly reasonable grounded headline two")
	bad.Link = "https://not-a-candidate.example.com/fabricated"
	bad.SourceName = "Example News"
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{good, bad}})

	store := &stubStore{}
	fetch := &stubFetcher{candidates: []sources.Candidate{
		{Title: "Real article", URL: candidateURL, SourceName: "Example News", Published: now.Add(-time.Hour)},
	}}
	deps := newDeps(store, nil, fetch, provider, now)

	result, err := Run(context.Background(), deps, testFeedGrounded(), groundedSpec())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d items, want 1 (the fabricated link should be dropped)", len(result.Items))
	}
	if result.Items[0].Link != candidateURL {
		t.Fatalf("kept item link = %q, want %q", result.Items[0].Link, candidateURL)
	}
	if store.commits[0].run.RejectReasons[ReasonLinkNotCandidate] != 1 {
		t.Fatalf("RejectReasons[link_not_in_candidate_set] = %d, want 1", store.commits[0].run.RejectReasons[ReasonLinkNotCandidate])
	}
}

func TestRun_PublishedAt_NeverEarlierThanFeedsNewest(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	// The feed's newest item is stamped in the future relative to "now" (a
	// clock skew / fast-follow-up-run scenario) — Run must anchor strictly
	// after it rather than trusting now().
	newest := now.Add(10 * time.Minute)

	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{
		validGeneratedItem("A perfectly reasonable trivia title after newest"),
	}})

	store := &stubStore{newest: newest}
	nov := &stubNovelty{}
	deps := newDeps(store, nov, nil, provider, now)

	result, err := Run(context.Background(), deps, testFeedGenerative(), testSpec())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(result.Items))
	}
	if !result.Items[0].PublishedAt.After(newest) {
		t.Fatalf("published_at %v is not after feed's newest %v", result.Items[0].PublishedAt, newest)
	}
}
