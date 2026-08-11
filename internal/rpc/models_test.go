package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// fakeLister stands in for the provider. It counts calls, because the cache's
// whole job is to not make them.
type fakeLister struct {
	models []llm.Model
	err    error
	calls  int
}

func (f *fakeLister) ListModels(context.Context) ([]llm.Model, error) {
	f.calls++
	return f.models, f.err
}

func modelsServer(t *testing.T, lister llm.ModelLister, key string) *SystemServer {
	t.Helper()
	env := map[string]string{}
	if key != "" {
		env[sysProviderAPIKeyEnv] = key
	}
	return NewSystemServer(sysTestStore(t), nil,
		WithGetenv(sysFakeGetenv(env)),
		WithModelLister(lister),
	)
}

func TestListModelsWithNoKeyIsUnavailableNotAnError(t *testing.T) {
	// A deployment can legitimately run with no provider key — feeds keep
	// serving, only generation stops — and this is the first state an
	// operator configuring one sees. Failing the RPC would break the very
	// screen they came to fix it on.
	srv := modelsServer(t, &fakeLister{}, "")

	resp, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels returned a gRPC error: %v", err)
	}
	if !resp.GetUnavailable() {
		t.Error("want unavailable=true with no key configured")
	}
	if resp.GetUnavailableReason() == "" {
		t.Error("unavailable with no reason leaves the operator nothing to act on")
	}
	if len(resp.GetModels()) != 0 {
		t.Errorf("want no models, got %d", len(resp.GetModels()))
	}
}

func TestListModelsMapsEveryFieldAndCaches(t *testing.T) {
	lister := &fakeLister{models: []llm.Model{
		{ID: "gpt-4o", OwnedBy: "openai", Chat: true},
		{ID: "text-embedding-3-small", OwnedBy: "openai", Embedding: true},
		{ID: "some-unknown-model", OwnedBy: "acme"},
	}}
	srv := modelsServer(t, lister, "sk-not-a-real-key")

	resp, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if resp.GetUnavailable() {
		t.Fatalf("a successful fetch reported unavailable: %v", resp.GetUnavailableReason())
	}
	if len(resp.GetModels()) != 3 {
		t.Fatalf("got %d models, want 3 — an unclassified model must still be offered", len(resp.GetModels()))
	}
	first := resp.GetModels()[0]
	if first.GetId() != "gpt-4o" || first.GetOwnedBy() != "openai" || !first.GetChat() || first.GetEmbedding() {
		t.Errorf("model mapping dropped a field: %+v", first)
	}
	if !resp.GetModels()[1].GetEmbedding() {
		t.Error("the embedding flag did not survive the mapping")
	}

	// Second call inside the TTL must not touch the provider: a multi-second
	// call on every visit to the settings screen is the cost this avoids.
	if _, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{}); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if lister.calls != 1 {
		t.Errorf("provider was called %d times, want 1 (the second read should be cached)", lister.calls)
	}
}

func TestListModelsServesTheLastGoodListAfterAFailure(t *testing.T) {
	// Reported live: the menu emptied itself on a transient timeout. A model
	// list changes when the provider ships a model, not minute to minute, so
	// stale data beats an empty menu by a wide margin.
	lister := &fakeLister{models: []llm.Model{{ID: "gpt-4o", Chat: true}}}
	srv := modelsServer(t, lister, "sk-not-a-real-key")

	if _, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{}); err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	// Age the cache past its TTL so the next call really does try the
	// provider, and make that attempt fail.
	expireModelCache(srv)
	lister.err = errors.New("context deadline exceeded")
	lister.models = nil

	resp, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels returned a gRPC error: %v", err)
	}
	if lister.calls != 2 {
		t.Errorf("the provider was called %d times, want 2 — an expired cache must be refreshed", lister.calls)
	}
	if resp.GetUnavailable() {
		t.Error("the screen went unavailable despite having a last known list")
	}
	if len(resp.GetModels()) != 1 || resp.GetModels()[0].GetId() != "gpt-4o" {
		t.Errorf("want the last good list, got %+v", resp.GetModels())
	}
}

func TestListModelsFailureWithNoCacheDegradesGracefully(t *testing.T) {
	lister := &fakeLister{err: errors.New("dial tcp: i/o timeout")}
	srv := modelsServer(t, lister, "sk-not-a-real-key")

	resp, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels returned a gRPC error: %v", err)
	}
	if !resp.GetUnavailable() {
		t.Error("want unavailable=true when the provider failed and nothing is cached")
	}
	// The raw provider error can carry organisation ids and quota details, and
	// this response is read by a browser.
	if got := resp.GetUnavailableReason(); got == "" || strings.Contains(got, "dial tcp") {
		t.Errorf("unavailable reason = %q, want an operator-facing sentence with no raw provider error", got)
	}
}

func TestListModelsEmptyListIsStillASuccess(t *testing.T) {
	// A key that can use no models is a real answer, not a failure — and it
	// must not be reported as "couldn't reach the provider", which would send
	// the operator to check their network instead of their account.
	srv := modelsServer(t, &fakeLister{}, "sk-not-a-real-key")
	resp, err := srv.ListModels(t.Context(), &affv1.SystemServiceListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if resp.GetUnavailable() {
		t.Errorf("an empty-but-successful list reported unavailable: %q", resp.GetUnavailableReason())
	}
	if len(resp.GetModels()) != 0 {
		t.Errorf("got %d models, want none", len(resp.GetModels()))
	}
}

// --- CostHistory ------------------------------------------------------------

func TestCostHistoryWindowSizes(t *testing.T) {
	// days counts back INCLUSIVE of today, so a 30-day window is 30 bars, not
	// 31. An off-by-one here is invisible on the chart and wrong in the total.
	cases := []struct {
		name     string
		days     int32
		wantBars int
	}{
		{"explicit week", 7, 7},
		{"zero means the default month", 0, costHistoryDefaultDays},
		{"negative means the default month", -5, costHistoryDefaultDays},
		{"over the cap is clamped", 5000, costHistoryMaxDays},
		{"single day", 1, 1},
	}
	srv := NewSystemServer(sysTestStore(t), nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.CostHistory(t.Context(), &affv1.SystemServiceCostHistoryRequest{Days: tc.days})
			if err != nil {
				t.Fatalf("CostHistory: %v", err)
			}
			if got := len(resp.GetBuckets()); got != tc.wantBars {
				t.Errorf("got %d buckets, want %d", got, tc.wantBars)
			}
		})
	}
}

func TestCostHistoryTotalsWhatItDraws(t *testing.T) {
	// The hero number and the bars come from one read, so they cannot
	// disagree — but only if the total is summed from the same buckets.
	st := sysTestStore(t)
	ctx := t.Context()

	feedID, err := st.CreateFeed(ctx, model.Feed{
		Slug: "trivia", Title: "Trivia", Kind: model.KindGenerative, Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	runID, err := st.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := st.CommitRun(ctx, runID, nil, store.RunSummary{TokensIn: 10, TokensOut: 20, CostUSD: 1.25}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	srv := NewSystemServer(st, nil)
	resp, err := srv.CostHistory(ctx, &affv1.SystemServiceCostHistoryRequest{Days: 7})
	if err != nil {
		t.Fatalf("CostHistory: %v", err)
	}

	var summed float64
	var runs int64
	for _, b := range resp.GetBuckets() {
		summed += b.GetUsd()
		runs += b.GetRuns()
	}
	if summed != resp.GetTotalUsd() {
		t.Errorf("total %v does not match the sum of the bars %v", resp.GetTotalUsd(), summed)
	}
	if resp.GetTotalUsd() != 1.25 || runs != 1 {
		t.Errorf("total=%v runs=%d, want 1.25 and 1", resp.GetTotalUsd(), runs)
	}

	// Every bucket carries a date, including the empty ones — that is what
	// makes a quiet day visible as a gap rather than absent from the axis.
	for i, b := range resp.GetBuckets() {
		if b.GetDate() == "" {
			t.Errorf("bucket %d has no date: %+v", i, b)
		}
	}
}

func TestCostHistoryReportsAStoreFailure(t *testing.T) {
	// Unlike ListModels, this one MUST fail loudly: a cost chart that renders
	// zeros because the read failed is a chart that says "you spent nothing".
	srv := NewSystemServer(sysTestStore(t), nil)
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := srv.CostHistory(dead, &affv1.SystemServiceCostHistoryRequest{Days: 7}); err == nil {
		t.Fatal("want an error when the underlying read fails")
	}
}

// expireModelCache backdates the cache stamp so the next call refetches,
// without a test having to sleep for the TTL.
func expireModelCache(s *SystemServer) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	s.modelsAt = time.Now().Add(-2 * modelCacheTTL)
}
