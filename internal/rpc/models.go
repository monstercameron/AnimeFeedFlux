package rpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
)

// models.go implements SystemService.ListModels: ask the provider which
// models this deployment's key can actually use, so /settings can offer a
// menu instead of a text box.
//
// The provider call is made HERE and never in the browser. The key is
// server-side only (PLAN.md §4: "never displayed, never sent to the
// client"), so the client cannot make this call itself even in principle —
// which is the whole reason this is an RPC.

// ListModels reports the models available to the configured provider.
//
// It NEVER returns a gRPC error for a provider problem. A settings screen
// that fails to load because a third party is slow, rate-limiting, or
// misconfigured is worse than one that says "couldn't reach the provider"
// and lets the operator type a model id — which is exactly what the UI did
// before this RPC existed, and must keep being able to do. So a failure
// comes back as a successful response with Unavailable set, and only a
// genuinely broken request (nothing here can produce one) would be an
// error.
func (s *SystemServer) ListModels(ctx context.Context, _ *affv1.SystemServiceListModelsRequest) (*affv1.SystemServiceListModelsResponse, error) {
	// Resolve the active profile on every call rather than at boot: an
	// operator who points the app at a local shim must see THAT endpoint's
	// models on the next refresh, not after a restart (A4-42). A read
	// failure degrades to the deployment default rather than emptying the
	// menu, which is the same judgement the stale-cache path below makes.
	endpoint := ProviderEndpoint{APIKey: s.getenv(sysProviderAPIKeyEnv)}
	if s.st != nil {
		if p, err := s.sysLoadProvider(ctx, s.st.Reader()); err != nil {
			s.log.WarnContext(ctx, "reading provider settings for the model list", "error", err.Error())
		} else {
			endpoint = ResolveProviderEndpoint(p, s.getenv)
		}
	}

	apiKey := endpoint.APIKey
	if apiKey == "" {
		// Not an error: a deployment can legitimately run without a
		// provider key (feeds keep serving; only generation stops — §13's
		// kill switch is the same shape), and the operator configuring one
		// for the first time will hit exactly this state.
		return &affv1.SystemServiceListModelsResponse{
			Unavailable:       true,
			UnavailableReason: "No provider API key is configured, so the model list cannot be fetched.",
		}, nil
	}

	lister := s.modelLister
	if lister == nil {
		lister = llm.NewOpenAIModelLister(apiKey, endpoint.BaseURL)
	}

	if cached, ok := s.cachedModels(); ok {
		return cached, nil
	}

	models, err := lister.ListModels(ctx)
	if err != nil {
		// The raw provider error is logged, never returned: it can carry
		// organisation ids and quota details, and this response is read by
		// a browser. The operator gets a sentence they can act on.
		s.log.WarnContext(ctx, "listing provider models failed", "error", err.Error())
		// Serve the last good list if there is one, even though it is past
		// its refresh window. A model list is nearly static — it changes
		// when the provider ships a model, not minute to minute — so stale
		// data is enormously more useful than an empty menu, and a single
		// slow call must not undo a working screen. Reported live: the
		// menu emptied itself on a transient timeout.
		if stale, ok := s.staleModels(); ok {
			s.log.WarnContext(ctx, "serving the last known model list after a provider failure")
			return stale, nil
		}
		return &affv1.SystemServiceListModelsResponse{
			Unavailable:       true,
			UnavailableReason: "Couldn't reach the provider to list models. Check the API key and try again.",
		}, nil
	}

	out := make([]*affv1.ProviderModel, 0, len(models))
	for _, m := range models {
		out = append(out, &affv1.ProviderModel{
			Id:        m.ID,
			OwnedBy:   m.OwnedBy,
			Chat:      m.Chat,
			Embedding: m.Embedding,
		})
	}
	resp := &affv1.SystemServiceListModelsResponse{Models: out}
	s.storeModels(resp)
	return resp, nil
}

// modelCacheTTL is how long a fetched list is served without asking the
// provider again. The list changes when OpenAI ships a model, so minutes of
// staleness costs nothing and saves a multi-second call on every visit to
// the settings screen.
const modelCacheTTL = 10 * time.Minute

// cachedModels returns the cached list if it is still fresh.
func (s *SystemServer) cachedModels() (*affv1.SystemServiceListModelsResponse, bool) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	if s.modelsCache == nil || time.Since(s.modelsAt) > modelCacheTTL {
		return nil, false
	}
	return s.modelsCache, true
}

// staleModels returns the cached list regardless of age — the fallback for
// when the provider call just failed. See CostHistory's caller for why a
// stale list beats an empty one.
func (s *SystemServer) staleModels() (*affv1.SystemServiceListModelsResponse, bool) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	if s.modelsCache == nil {
		return nil, false
	}
	return s.modelsCache, true
}

func (s *SystemServer) storeModels(resp *affv1.SystemServiceListModelsResponse) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	s.modelsCache = resp
	s.modelsAt = time.Now()
}

// costHistoryDefaultDays is the window CostHistory uses when the caller does
// not say. A month is long enough to show a weekly rhythm and a price
// change, short enough that the chart is still readable at one bar per day.
const costHistoryDefaultDays = 30

// costHistoryMaxDays bounds the query. A year of daily buckets is already
// more than a chart can render legibly; past that this is an export, not a
// dashboard.
const costHistoryMaxDays = 365

// CostHistory reports daily provider spend for the Provider settings chart.
//
// The numbers come from runs.est_cost_usd, which each run stamped at the
// price in force at the time (PLAN.md §13), so this is history as it
// actually happened — editing the price table changes future runs, never
// past ones. §22 J8 states that invariant as a sanity assertion; this is the
// surface that makes it visible.
func (s *SystemServer) CostHistory(ctx context.Context, req *affv1.SystemServiceCostHistoryRequest) (*affv1.SystemServiceCostHistoryResponse, error) {
	days := int(req.GetDays())
	switch {
	case days <= 0:
		days = costHistoryDefaultDays
	case days > costHistoryMaxDays:
		days = costHistoryMaxDays
	}

	// days counts back INCLUSIVE of today, so a 30-day window starts 29 days
	// ago — an off-by-one here would quietly show 31 bars for "30 days".
	since := time.Now().UTC().AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)

	daily, err := s.st.SpendByDay(ctx, since)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reading cost history: %v", err)
	}

	buckets := make([]*affv1.CostBucket, 0, len(daily))
	var total float64
	for _, d := range daily {
		total += d.CostUSD
		buckets = append(buckets, &affv1.CostBucket{
			Date:      d.Date,
			Usd:       d.CostUSD,
			Runs:      d.Runs,
			TokensIn:  d.TokensIn,
			TokensOut: d.TokensOut,
		})
	}
	return &affv1.SystemServiceCostHistoryResponse{Buckets: buckets, TotalUsd: total}, nil
}
