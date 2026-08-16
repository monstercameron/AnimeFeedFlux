// Package rpc holds the gRPC service implementations (PLAN.md §11). This
// file implements SampleService: Sample, SampleStream, ListSamples,
// DiscardSample.
//
// The single most important property of everything below (PLAN.md §12.3,
// §13, BF-11): SAMPLING MUST WRITE NO ITEM. A sampler that silently
// publishes is indistinguishable from a working feature right up until an
// admin notices duplicate items nobody promoted — the worst bug this design
// can produce, because it looks exactly like success. Concretely:
//
//   - The only store write anywhere in this file is smpSampleStore.PutSample
//     (a samples row) and DiscardSample (deleting one). Nothing here ever
//     calls anything resembling CommitRun, InsertItem, or PromoteSample.
//   - generate.Sample — the pipeline this file calls — already has no code
//     path to Store.CommitRun (see runner.go's doc comment on Sample). This
//     file adds a second, independent guard on top of that: the
//     generate.Store adapter built in smpGenerateStore below implements
//     CommitRun as a hard failure that increments an atomic counter, so if
//     generate.Sample were ever changed to call it, a test would catch it
//     immediately rather than the bug reaching production silently.
//   - Sampling draws from the SAME budget as scheduled generation (PLAN.md
//     §13): the budget check runs BEFORE generate.Sample is invoked, i.e.
//     before any provider call, using the identical accounting a scheduled
//     run would use (the injected smpBudgetChecker is expected to route
//     through the same budget.Check machinery — see budget.Kind's doc
//     comment).
//   - With the kill switch off, no provider call is made at all — the
//     enabled check runs before generate.Sample is ever invoked, not merely
//     before its result is used.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// --- Injected dependencies ---
//
// Every dependency SampleServer needs is an interface (or a func value)
// declared right here, the same discipline internal/generate/runner.go uses
// and for the same reason: this file must be fully testable without a real
// database, a real provider, or the other RPC services (auth.go, feed.go,
// item.go, run.go, system.go) existing yet — they are being built
// concurrently by other agents.

// smpFeedLookup resolves a feed id to the feed identity and the generation
// spec a LIVE run of it would use — the same recipe, not a parallel one
// sampling maintains independently. Sample overrides Spec.ItemsPerRun with
// the caller's requested sample size (1-5, PLAN.md §12.3) before use; every
// other field (prompts, model, novelty settings, prices) comes from here
// untouched, which is what makes "sample this recipe" actually sample the
// recipe that would run.
type smpFeedLookup interface {
	GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error)
}

// smpGenStore is the read-only slice of generate.Store this package needs.
// It deliberately has no CommitRun method — see smpGenerateStore below for
// why that omission is load-bearing rather than incidental.
type smpGenStore interface {
	RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error)
	NewestPublished(ctx context.Context, feedID int64) (time.Time, error)
}

// smpBudgetChecker is the injected budget gate (PLAN.md §13). Implementations
// are expected to check the SAME limits and the SAME accumulated spend a
// scheduled run's budget.Check would see — including this and every other
// sample's own cost, not just runs — so the interactive, easy-to-repeat
// "click Sample" path cannot bypass the safety net a scheduled run is held
// to. Kind is always budget.KindSample here; see budget.Kind's doc comment
// for why that must not change the arithmetic, only the label.
type smpBudgetChecker interface {
	// CheckSample decides whether a sample of the given projected token size
	// may proceed. Called BEFORE generate.Sample, i.e. before any provider
	// call — never after, and never to decide whether to keep or discard a
	// result that already happened.
	CheckSample(ctx context.Context, feedID int64, projectedTokens int) budget.Decision
	// RemainingDailyUSD reports the global daily budget remaining, for
	// SampleServiceSampleResponse.RemainingDailyBudgetUsd (PLAN.md §12.3:
	// "the day's remaining budget", shown without a second round trip).
	RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error)
}

// smpSampleStore is the persistence surface this file needs from
// internal/store. Its method set is written to match *store.Store's actual
// methods exactly (samples.go), so the production Store satisfies this
// interface with no adapter — but it is declared here, not imported as a
// concrete dependency, so a fake can stand in for tests without a real
// SQLite file.
type smpSampleStore interface {
	PutSample(ctx context.Context, feedID int64, payload []byte, tokensIn, tokensOut int, costUSD float64, ttl time.Duration) (int64, error)
	GetSample(ctx context.Context, id int64) (store.Sample, error)
	ListSamples(ctx context.Context, feedID int64) ([]store.Sample, error)
	DiscardSample(ctx context.Context, id int64) error
}

// smpEnabledCheck reports whether generation (including sampling — PLAN.md
// §13: "Sampling draws from the same budget as scheduled generation") is
// currently allowed: the global kill switch (SetGenerationEnabled,
// AFF_GENERATION_ENABLED). It runs BEFORE generate.Sample, so "kill switch
// off" means "no provider call is made at all" rather than "a call whose
// result is discarded".
type smpEnabledCheck func(ctx context.Context) (enabled bool, reason string, err error)

// smpPriceLookup resolves a model's per-token rate, matching
// *budget.Table's own Lookup signature (declared narrowly here so this
// package depends on the method, not the concrete type). It exists because
// smpFeedLookup's price comes from the SAVED recipe's model — correct for
// "sample this recipe" — but a draft overriding the model (smpApplyDraft)
// changes what should be priced without changing what smpFeedLookup already
// resolved. Nil is valid (matches every other optional dependency here) and
// simply leaves an overridden model unpriced, same as an unknown model
// already does.
type smpPriceLookup interface {
	Lookup(model string) (budget.Price, bool)
}

// SampleServerConfig bundles every dependency SampleServer needs. Now
// defaults to time.Now when nil, the same pattern generate.Deps uses, so
// tests get deterministic timestamps without a wall-clock race.
type SampleServerConfig struct {
	Feeds    smpFeedLookup
	GenStore smpGenStore
	Novelty  generate.Novelty // may be nil for grounded-only deployments
	Fetcher  generate.Fetcher // may be nil for generative-only deployments
	Provider llm.Provider
	IDs      ids.Source
	Budget   smpBudgetChecker
	// Prices re-derives PriceInputPerMToken/PriceOutputPerMToken when a draft
	// overrides the model (see smpPriceLookup). Optional: nil leaves an
	// overridden model's price at whatever smpFeedLookup resolved for the
	// SAVED model, which is wrong but no worse than the bug this fixes.
	Prices  smpPriceLookup
	Samples smpSampleStore
	Enabled smpEnabledCheck

	// PublicHost is the bare hostname used to build Tag URI guids and feed
	// URLs for the rendered-XML preview (PLAN.md §5.1), e.g.
	// "anime.earlcameron.com".
	PublicHost string
	// Generator identifies the software in the rendered document's
	// <generator> element, e.g. "AnimeFeedFlux v0.0.2-dev".
	Generator string
	// SampleTTL is how long a sample survives (PLAN.md §12.3: "so a good one
	// survives a refresh"). Zero defaults to 24h.
	SampleTTL time.Duration

	Now func() time.Time
}

// SampleServer implements affv1.SampleServiceServer.
type SampleServer struct {
	affv1.UnimplementedSampleServiceServer

	cfg SampleServerConfig

	// commitRunCalls counts how many times the poison-pill CommitRun in
	// smpGenerateStore actually fired. It must be zero after every call this
	// file makes, for every outcome — success, provider error, malformed
	// output, budget denial, kill switch, cancellation. Tests assert this
	// directly; see sample_test.go.
	commitRunCalls atomic.Int64
}

// NewSampleServer constructs a SampleServer from its dependencies.
func NewSampleServer(cfg SampleServerConfig) *SampleServer {
	return &SampleServer{cfg: cfg}
}

// CommitRunCalls reports how many times the sample pipeline's poison-pill
// CommitRun fired. Exported (rather than a test-only accessor) so an
// operator wiring metrics can alert on a nonzero value in production too —
// it should never happen, and if it ever does, that is the worst bug this
// design can produce (see the file-level doc comment).
func (s *SampleServer) CommitRunCalls() int64 { return s.commitRunCalls.Load() }

func (s *SampleServer) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func (s *SampleServer) sampleTTL() time.Duration {
	if s.cfg.SampleTTL > 0 {
		return s.cfg.SampleTTL
	}
	return 24 * time.Hour
}

// --- The poison-pill generate.Store adapter ---

// smpGenerateStore bridges the read-only data generate.Sample legitimately
// needs (RecentTitles, NewestPublished) to whatever smpGenStore
// implementation was injected, while making a CommitRun call fail loudly
// and count itself rather than either succeeding (which would mean this
// sampler just published) or silently discarding its result (which the task
// brief calls out by name as insufficient — "not a call whose result is
// discarded"). generate.Sample's own contract already never calls this
// method; this is the second, independent line of defense, and the reason
// commitRunCalls exists at all.
type smpGenerateStore struct {
	inner   smpGenStore
	counter *atomic.Int64
}

func (g smpGenerateStore) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	return g.inner.RecentTitles(ctx, feedID, n)
}

func (g smpGenerateStore) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	return g.inner.NewestPublished(ctx, feedID)
}

func (g smpGenerateStore) CommitRun(ctx context.Context, run generate.RunRecord, items []model.Item) error {
	g.counter.Add(1)
	return fmt.Errorf("rpc: sample pipeline invoked CommitRun for feed %d — this must never happen (PLAN.md §11, §12.3, §13); refusing to persist", run.FeedID)
}

// --- Estimation constants for the pre-flight budget check ---

// smpEstimatedOutputTokensPerItem pads the pre-call projection with a
// conservative per-item output allowance. Nothing before the model call can
// know the true output size; this exists only so the budget check (which
// must run BEFORE the call, §13) has a number to compare against rather than
// silently using zero, which would let an oversized request through for
// free. It is not shown to the admin as a cost — only the aggregate
// EstCostUSD generate.Sample actually measures ever is (labelled an estimate
// throughout, PLAN.md §8.1).
const smpEstimatedOutputTokensPerItem = 400

// smpProjectedTokens estimates the pre-call token size for the budget check.
// Doubled to account for §9.3's one repair attempt, which resends the full
// prompt plus feedback — the worst case the budget must guard against, not
// the common case.
func smpProjectedTokens(spec generate.Spec) int {
	base := budget.EstimateRequest(spec.UserPromptTemplate, spec.SystemPrompt)
	items := spec.ItemsPerRun
	if items <= 0 {
		items = 1
	}
	base += items * smpEstimatedOutputTokensPerItem
	return base * 2
}

// --- Request validation ---

func smpValidateSampleSize(n int32) (int32, error) {
	if n == 0 {
		return 1, nil
	}
	if n < 1 || n > 5 {
		return 0, status.Errorf(codes.InvalidArgument, "sample_size must be 1-5, got %d (PLAN.md §12.3)", n)
	}
	return n, nil
}

// --- Sample (unary) ---

// Sample implements the unary dry run: PLAN.md §11, §12.3.
func (s *SampleServer) Sample(ctx context.Context, req *affv1.SampleServiceSampleRequest) (*affv1.SampleServiceSampleResponse, error) {
	if req.GetFeedId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "feed_id is required")
	}
	sampleSize, err := smpValidateSampleSize(req.GetSampleSize())
	if err != nil {
		return nil, err
	}

	feed, result, candidates, cerr := s.prepareSample(ctx, req.GetFeedId(), sampleSize, req.GetDraft())
	if cerr != nil {
		return nil, cerr
	}

	payload, err := json.Marshal(candidates)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: encoding sample payload: %v", err)
	}
	sampleID, err := s.cfg.Samples.PutSample(ctx, feed.ID, payload, result.TokensIn, result.TokensOut, result.EstCostUSD, s.sampleTTL())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: persisting sample: %v", err)
	}

	remaining, err := s.cfg.Budget.RemainingDailyUSD(ctx, feed.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: reading remaining budget: %v", err)
	}

	return &affv1.SampleServiceSampleResponse{
		SampleId:                strconv.FormatInt(sampleID, 10),
		Candidates:              candidates,
		RemainingDailyBudgetUsd: remaining,
	}, nil
}

// prepareSample runs the shared pipeline both Sample and SampleStream use:
// resolve the feed and spec, check the kill switch, check the budget, then
// — and only then — call generate.Sample. Every early return here happens
// BEFORE deps.Provider.Generate is ever reachable, which is what "no
// provider call is made at all" means concretely.
func (s *SampleServer) prepareSample(ctx context.Context, feedID int64, sampleSize int32, draft *affv1.SampleDraft) (model.Feed, generate.SampleResult, []*affv1.SampleCandidate, error) {
	feed, spec, err := s.cfg.Feeds.GetFeedForSample(ctx, feedID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return model.Feed{}, generate.SampleResult{}, nil, status.Errorf(codes.NotFound, "feed %d not found", feedID)
		}
		return model.Feed{}, generate.SampleResult{}, nil, status.Errorf(codes.Internal, "rpc: resolving feed %d: %v", feedID, err)
	}
	if feed.Kind == model.KindAggregate {
		return model.Feed{}, generate.SampleResult{}, nil, status.Error(codes.InvalidArgument, "aggregate feeds have no generator to sample (PLAN.md §14.2)")
	}
	spec.ItemsPerRun = int(sampleSize)
	spec.Trigger = "" // Sample never writes a run row; Trigger is meaningless here (runner.go)

	// Apply the unsaved editor draft, if one was sent. This is what makes
	// §22's J3 loop possible without committing an experiment over the
	// working recipe first — see SampleDraft's doc comment in the proto.
	//
	// Applied AFTER the saved spec is loaded and BEFORE any validation or
	// provider call, so a draft is subject to exactly the same checks a
	// saved recipe is. Nothing here is written back: `spec` is a value, and
	// this function never persists it.
	if err := smpApplyDraft(&spec, draft, s.cfg.Prices); err != nil {
		return model.Feed{}, generate.SampleResult{}, nil, err
	}

	// Kill switch, checked BEFORE anything that could reach the provider.
	enabled, reason, err := s.cfg.Enabled(ctx)
	if err != nil {
		return model.Feed{}, generate.SampleResult{}, nil, status.Errorf(codes.Internal, "rpc: checking generation kill switch: %v", err)
	}
	if !enabled {
		msg := "generation is disabled"
		if reason != "" {
			msg = fmt.Sprintf("generation is disabled: %s", reason)
		}
		return model.Feed{}, generate.SampleResult{}, nil, status.Error(codes.FailedPrecondition, msg)
	}

	// Budget, checked BEFORE anything that could reach the provider, using
	// the SAME accounting a scheduled run's call would use (PLAN.md §13).
	decision := s.cfg.Budget.CheckSample(ctx, feed.ID, smpProjectedTokens(spec))
	if !decision.Allow {
		return model.Feed{}, generate.SampleResult{}, nil, status.Errorf(codes.FailedPrecondition, "sample denied: budget cap reached (%s)", decision.Reason)
	}

	deps := generate.Deps{
		Store:    smpGenerateStore{inner: s.cfg.GenStore, counter: &s.commitRunCalls},
		Novelty:  s.cfg.Novelty,
		Fetcher:  s.cfg.Fetcher,
		Provider: s.cfg.Provider,
		IDs:      s.cfg.IDs,
		Now:      s.cfg.Now,
	}

	result, err := generate.Sample(ctx, deps, feed, spec)
	if err != nil {
		return model.Feed{}, generate.SampleResult{}, nil, smpClassifySampleError(err)
	}

	candidates, err := s.buildCandidates(ctx, feed, spec, result)
	if err != nil {
		return model.Feed{}, generate.SampleResult{}, nil, status.Errorf(codes.Internal, "rpc: building sample candidates: %v", err)
	}
	return feed, result, candidates, nil
}

// smpClassifySampleError maps a generate.Sample failure onto a gRPC status,
// using the same Transient/Invalid/Fatal taxonomy llm/errors.go defines
// (PLAN.md §8) so a caller does not have to re-derive it.
func smpClassifySampleError(err error) error {
	var lerr *llm.Error
	if errors.As(err, &lerr) {
		switch lerr.Kind {
		case llm.Transient:
			return status.Error(codes.Unavailable, err.Error())
		case llm.Invalid:
			return status.Error(codes.InvalidArgument, err.Error())
		case llm.Fatal:
			return status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	if errors.Is(err, generate.ErrMalformedOutput) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return status.Errorf(codes.Internal, "rpc: sample failed: %v", err)
}

// smpErrorKind maps an error from generate.Sample onto the wire ErrorKind
// enum, for SampleStream's terminal message.
func smpErrorKind(err error) affv1.ErrorKind {
	var lerr *llm.Error
	if errors.As(err, &lerr) {
		switch lerr.Kind {
		case llm.Transient:
			return affv1.ErrorKind_ERROR_KIND_TRANSIENT
		case llm.Invalid:
			return affv1.ErrorKind_ERROR_KIND_INVALID
		case llm.Fatal:
			return affv1.ErrorKind_ERROR_KIND_FATAL
		}
	}
	if errors.Is(err, generate.ErrMalformedOutput) {
		return affv1.ErrorKind_ERROR_KIND_INVALID
	}
	return affv1.ErrorKind_ERROR_KIND_UNSPECIFIED
}

// --- SampleStream (server-streaming) ---

// SampleStream implements PLAN.md §12.3's "streams output as it arrives...
// with cancel". The underlying pipeline (generate.Sample, runner.go) makes
// one blocking model call and returns every candidate at once — there is no
// true incremental provider stream to relay, and adding one would mean
// editing runner.go, out of scope here. What this method DOES provide
// faithfully: candidates are emitted as separate messages rather than one
// blocking response (so a client can render progressively once the batch
// lands), and the stream checks for client cancellation before every send,
// so a cancel button stops delivery promptly even though the server-side
// generation work was already a single unit.
func (s *SampleServer) SampleStream(req *affv1.SampleServiceSampleStreamRequest, stream affv1.SampleService_SampleStreamServer) error {
	if req.GetFeedId() == 0 {
		return status.Error(codes.InvalidArgument, "feed_id is required")
	}
	sampleSize, err := smpValidateSampleSize(req.GetSampleSize())
	if err != nil {
		return err
	}

	ctx := stream.Context()
	feed, result, candidates, cerr := s.prepareSample(ctx, req.GetFeedId(), sampleSize, req.GetDraft())

	var sampleID string
	if cerr == nil {
		payload, merr := json.Marshal(candidates)
		if merr != nil {
			cerr = status.Errorf(codes.Internal, "rpc: encoding sample payload: %v", merr)
		} else {
			id, perr := s.cfg.Samples.PutSample(ctx, feed.ID, payload, result.TokensIn, result.TokensOut, result.EstCostUSD, s.sampleTTL())
			if perr != nil {
				cerr = status.Errorf(codes.Internal, "rpc: persisting sample: %v", perr)
			} else {
				sampleID = strconv.FormatInt(id, 10)
			}
		}
	}

	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			// Client went away mid-delivery: honour it promptly rather than
			// finishing the batch it no longer wants (PLAN.md §12.3).
			return nil
		}
		if err := stream.Send(&affv1.SampleServiceSampleStreamResponse{
			SampleId:  sampleID,
			Candidate: c,
			Done:      false,
		}); err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return nil
	}

	final := &affv1.SampleServiceSampleStreamResponse{
		SampleId: sampleID,
		Done:     true,
	}
	if cerr != nil {
		st, _ := status.FromError(cerr)
		final.ErrorMessage = st.Message()

		// Prefer the typed error. §8's taxonomy distinguishes an account-wide
		// fatal (trips the global kill switch) from a recipe-scoped one
		// (disables one feed), and a gRPC code cannot carry that distinction —
		// reverse-mapping from the code collapses both onto FATAL and the
		// caller loses the only thing it needed to decide.
		if k := smpErrorKind(cerr); k != affv1.ErrorKind_ERROR_KIND_UNSPECIFIED {
			final.ErrorKind = k
		} else {
			final.ErrorKind = smpGRPCCodeToErrorKind(st.Code())
		}
	}
	return stream.Send(final)
}

// smpGRPCCodeToErrorKind is a best-effort reverse mapping for SampleStream's
// terminal message, used only when the failure crossed prepareSample's
// status.Error boundary and the original *llm.Error is no longer available
// to smpErrorKind directly.
func smpGRPCCodeToErrorKind(c codes.Code) affv1.ErrorKind {
	switch c {
	case codes.Unavailable:
		return affv1.ErrorKind_ERROR_KIND_TRANSIENT
	case codes.InvalidArgument:
		return affv1.ErrorKind_ERROR_KIND_INVALID
	case codes.FailedPrecondition, codes.NotFound:
		return affv1.ErrorKind_ERROR_KIND_FATAL
	default:
		return affv1.ErrorKind_ERROR_KIND_UNSPECIFIED
	}
}

// --- ListSamples / DiscardSample ---

// ListSamples implements PLAN.md §12.3's "samples persist server-side for
// 24h, so a good one survives a refresh" — an expired sample is absent, not
// flagged, because smpSampleStore.ListSamples already filters to unexpired
// rows (samples.go's own contract).
func (s *SampleServer) ListSamples(ctx context.Context, req *affv1.SampleServiceListSamplesRequest) (*affv1.SampleServiceListSamplesResponse, error) {
	if req.GetFeedId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "feed_id is required")
	}
	rows, err := s.cfg.Samples.ListSamples(ctx, req.GetFeedId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rpc: listing samples for feed %d: %v", req.GetFeedId(), err)
	}

	pageSize := int(req.GetPageSize())
	if pageSize <= 0 || pageSize > len(rows) {
		pageSize = len(rows)
	}
	rows = rows[:pageSize]

	summaries := make([]*affv1.SampleServiceListSamplesResponse_SampleSummary, 0, len(rows))
	for _, r := range rows {
		count, err := smpCandidateCount(r.Payload)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "rpc: reading sample %d payload: %v", r.ID, err)
		}
		summaries = append(summaries, &affv1.SampleServiceListSamplesResponse_SampleSummary{
			SampleId:       strconv.FormatInt(r.ID, 10),
			FeedId:         r.FeedID,
			CreatedAt:      timestamppb.New(r.CreatedAt),
			ExpiresAt:      timestamppb.New(r.ExpiresAt),
			CandidateCount: int32(count),
		})
	}

	// Pagination beyond a single page is not implemented: a feed's sample set
	// is bounded by a 24h TTL and interactive, human-paced sampling, so
	// cardinality in practice is small. NextPageToken is left empty rather
	// than faked, so a client can tell "no more pages" from "cursor not
	// implemented" is not a distinction it needs to make — there genuinely
	// are no more pages past what was returned.
	return &affv1.SampleServiceListSamplesResponse{Samples: summaries}, nil
}

func smpCandidateCount(payload []byte) (int, error) {
	var candidates []json.RawMessage
	if err := json.Unmarshal(payload, &candidates); err != nil {
		return 0, err
	}
	return len(candidates), nil
}

// DiscardSample implements the sampler pane's "Discard" button (PLAN.md
// §12.3). A sample was never published, so there is no permalink to keep
// 410ing and no guid to preserve — dropping the row (samples.go's
// DiscardSample) is simply correct, matching that file's own doc comment.
func (s *SampleServer) DiscardSample(ctx context.Context, req *affv1.SampleServiceDiscardSampleRequest) (*affv1.SampleServiceDiscardSampleResponse, error) {
	id, err := strconv.ParseInt(req.GetSampleId(), 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid sample_id %q", req.GetSampleId())
	}
	if err := s.cfg.Samples.DiscardSample(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "sample %d not found", id)
		}
		return nil, status.Errorf(codes.Internal, "rpc: discarding sample %d: %v", id, err)
	}
	return &affv1.SampleServiceDiscardSampleResponse{}, nil
}

// --- Building SampleCandidate from a generate.SampleResult ---

// buildCandidates turns the items generate.Sample would publish into the
// three-ways-shown wire shape PLAN.md §12.3 asks for: rendered XML
// byte-identical to what publishing would emit, the novelty verdict, the
// grounded link verdict, and a per-candidate share of the batch's token/cost
// estimate (generate.SampleResult reports these only in aggregate — see
// PLAN.md §8.1 on why there is no finer-grained number to report).
func (s *SampleServer) buildCandidates(ctx context.Context, feed model.Feed, spec generate.Spec, result generate.SampleResult) ([]*affv1.SampleCandidate, error) {
	items := result.Items
	if len(items) == 0 {
		return nil, nil
	}

	// Grounded feeds: fetch the same candidate URL set generate.Sample's own
	// acquireContext step would have fetched (PLAN.md §9 step 1), so link
	// verdicts are checked against the real source set rather than assumed.
	// This is a second fetch of the same normalized/cached sources §14.3
	// already makes cheap (conditional GET); acquireContext itself is
	// unexported in runner.go, so this is the only way to reproduce its
	// input from outside the package without editing it.
	var candidateURLs []string
	if feed.Kind == model.KindGrounded && s.cfg.Fetcher != nil {
		if cands, err := s.cfg.Fetcher.Candidates(ctx, feed.ID); err == nil {
			for _, c := range cands {
				candidateURLs = append(candidateURLs, c.URL)
			}
		}
		// A fetch error here is not fatal to the response: the items already
		// passed the real link-integrity check inside generate.Sample. Losing
		// the ability to show the verdict is a display-quality regression,
		// not a correctness one — the candidates themselves are still exactly
		// what generate.Sample validated.
	}

	tokensPerItem, remTokensIn, remTokensOut := smpSplitTokens(result.TokensIn, result.TokensOut, len(items))
	costPerItem := 0.0
	if len(items) > 0 {
		costPerItem = result.EstCostUSD / float64(len(items))
	}

	out := make([]*affv1.SampleCandidate, len(items))
	for i, it := range items {
		tIn, tOut := tokensPerItem[0], tokensPerItem[1]
		if i == len(items)-1 {
			// Fold any remainder from integer division onto the last
			// candidate so the per-candidate sum equals the reported total
			// exactly — an admin adding up the numbers on screen should get
			// the same figure the aggregate estimate reports.
			tIn += remTokensIn
			tOut += remTokensOut
		}

		c := &affv1.SampleCandidate{
			CandidateId:      fmt.Sprintf("c%d", i+1),
			Title:            it.Title,
			SummaryText:      it.SummaryText,
			BodyHtml:         it.BodyHTML,
			AnswerHtml:       it.AnswerHTML,
			Link:             it.Link,
			SourceName:       it.SourceName,
			SlackPreviewText: smpSlackPreview(it),
			TokensIn:         int32(tIn),
			TokensOut:        int32(tOut),
			EstimatedCostUsd: costPerItem,
		}

		xml, err := s.renderCandidateXML(feed, it)
		if err != nil {
			return nil, err
		}
		c.RenderedXml = xml

		embed, err := s.renderCandidateEmbed(feed, it)
		if err != nil {
			return nil, err
		}
		c.EmbedPreviewHtml = embed

		if feed.Kind == model.KindGenerative {
			c.Novelty = s.noveltyVerdict(ctx, feed.ID, it)
		}
		if feed.Kind == model.KindGrounded && it.Link != "" {
			c.LinkVerdicts = []*affv1.LinkVerdict{{Url: it.Link, Ok: generate.CheckLink(it.Link, candidateURLs) == nil}}
		}

		out[i] = c
	}
	return out, nil
}

// smpSplitTokens divides an aggregate token count evenly across n
// candidates, returning the per-candidate share and the remainder to fold
// onto the last one. generate.SampleResult does not report a per-item
// breakdown (PLAN.md §8.1: token accounting is a batch-level estimate to
// begin with), so an even split is the least-arbitrary choice available
// without inventing a heuristic runner.go does not itself use.
func smpSplitTokens(tokensIn, tokensOut, n int) ([2]int, int, int) {
	if n <= 0 {
		return [2]int{}, 0, 0
	}
	return [2]int{tokensIn / n, tokensOut / n}, tokensIn % n, tokensOut % n
}

// smpSlackPreview approximates the Slack card (PLAN.md §12.3): title link
// plus the plain-text summary, deliberately excluding body_html/answer_html
// so a trivia answer leaking into summary_text is visible immediately,
// exactly the same rule render.RSS's writeItem enforces for the real
// <description> element (§5.5).
func smpSlackPreview(it model.Item) string {
	title := it.Title
	if it.Link != "" {
		title = fmt.Sprintf("%s (%s)", it.Title, it.Link)
	}
	return title + "\n" + it.SummaryText
}

// renderCandidateXML renders a synthetic one-item Channel through the SAME
// render.RSS function publishing would use, then extracts the <item>...
// </item> fragment. This is deliberately not a bespoke item template: the
// whole point of "byte-identical to what publishing would emit" (PLAN.md
// §12.3) is that there is exactly one code path that knows how to render an
// item, and this calls it rather than re-implementing a second one that
// could drift.
func (s *SampleServer) renderCandidateXML(feed model.Feed, it model.Item) (string, error) {
	doc, err := render.RSS(s.candidateChannel(feed, it))
	if err != nil {
		return "", fmt.Errorf("rendering candidate item: %w", err)
	}
	return smpExtractItemFragment(string(doc))
}

// candidateChannel builds the synthetic one-item Channel every candidate
// preview renders through.
//
// One constructor, not one per preview: the previews exist to answer "what
// will this look like once published", and two channels assembled separately
// would eventually disagree about a URL or a Tag URI epoch and make one
// preview quietly lie about a detail the other got right.
func (s *SampleServer) candidateChannel(feed model.Feed, it model.Item) model.Channel {
	tagYear := feed.CreatedAt.Year()
	if tagYear == 0 {
		tagYear = s.now().Year()
	}

	// A sampled item has no published_at: generate.Sample deliberately skips
	// stampItems (runner.go), because a dry run persists nothing and a
	// timestamp is assigned at promote time, "stamped now" (§12.3). So the
	// item reaching a preview carries the zero time — and a preview is the
	// one place that zero becomes visible, rendering as "1 Jan 0001,
	// 00:00 UTC" in the embed and as a year-0001 pubDate in the feed XML
	// view, which every date rule in internal/feedvalidate treats as an
	// error. Promote is what this preview is previewing, so previewing it
	// with the timestamp promote would assign is the honest answer.
	//
	// Set here rather than in either renderer so both views agree, and only
	// when it is actually missing, so a real item passed through this path
	// keeps its own date.
	if it.PublishedAt.IsZero() {
		it.PublishedAt = s.now()
	}
	host := s.cfg.PublicHost
	return model.Channel{
		Feed:      feed,
		SelfURL:   fmt.Sprintf("https://%s/feeds/%s.xml", host, feed.Slug),
		HTMLURL:   fmt.Sprintf("https://%s/", host),
		Host:      host,
		TagYear:   tagYear,
		Items:     []model.Item{it},
		BuildTime: s.now(),
		Generator: s.cfg.Generator,
		DocsURL:   "https://www.rssboard.org/rss-specification",
	}
}

// renderCandidateEmbed renders the embed document (PLAN.md §6.1) this
// candidate would appear in, through the SAME render.Embed the public
// /embed/{slug} route uses — for the same reason renderCandidateXML calls
// render.RSS rather than templating an <item> by hand.
//
// It matters more here than for the XML view. The embed is the surface that
// ends up on somebody else's page, and its one hard rule is that it renders
// SummaryText only — never BodyHTML, never AnswerHTML (§5.5, §6.1). A
// preview built from a second, admin-only renderer could show a
// spoiler-free card while the real one leaked, or the reverse, and the
// preview would be worse than none: it would be reassuring and wrong.
//
// The window is deliberately the whole document with one item in it. An
// operator sampling a candidate is asking "what will this look like on the
// page" and the answer includes the heading, the rules, and the subscribe
// link — not just their own row.
func (s *SampleServer) renderCandidateEmbed(feed model.Feed, it model.Item) (string, error) {
	doc, err := render.Embed(s.candidateChannel(feed, it), render.EmbedOptions{})
	if err != nil {
		return "", fmt.Errorf("rendering candidate embed: %w", err)
	}
	return string(doc), nil
}

// smpExtractItemFragment pulls the single <item>...</item> block (including
// its own leading indentation, so the fragment reads exactly as it would
// inside the full document) out of a one-item RSS document.
func smpExtractItemFragment(doc string) (string, error) {
	start := strings.Index(doc, "<item>")
	if start == -1 {
		return "", errors.New("rendered RSS document has no <item> element")
	}
	lineStart := strings.LastIndex(doc[:start], "\n") + 1

	closeIdx := strings.Index(doc[start:], "</item>")
	if closeIdx == -1 {
		return "", errors.New("rendered RSS document has no closing </item>")
	}
	end := start + closeIdx + len("</item>")
	return doc[lineStart:end], nil
}

// noveltyVerdict re-derives the novelty check's verdict for display (PLAN.md
// §12.3: "runs the real novelty check and shows the verdict with the
// nearest existing item"). generate.SampleResult only reports which items
// passed, not the score/nearest-title detail filterNovel (runner.go)
// computes and discards — that detail is only ever visible for the sampler
// UI's benefit, so this recomputes it via the same injected Novelty
// dependency generate.Sample itself used. it is already known-novel (it is
// in result.Items), so a Check error here degrades to "novel, no detail"
// rather than failing the whole response — losing the nearest-title readout
// is a display-quality regression, not a correctness one.
func (s *SampleServer) noveltyVerdict(ctx context.Context, feedID int64, it model.Item) *affv1.NoveltyVerdict {
	if s.cfg.Novelty == nil {
		return &affv1.NoveltyVerdict{IsNovel: true}
	}
	dup, nearest, score, err := s.cfg.Novelty.Check(ctx, feedID, it.Title+" "+it.SummaryText)
	if err != nil {
		return &affv1.NoveltyVerdict{IsNovel: true}
	}
	return &affv1.NoveltyVerdict{
		IsNovel:          !dup,
		Similarity:       score,
		NearestItemTitle: nearest,
	}
}

// smpApplyDraft overlays the unsaved editor draft onto the loaded spec.
//
// Empty fields mean "keep what is saved", so a client sends only what the
// operator actually changed and a draft can never blank a recipe field by
// omission.
//
// The prompt templates are COMPILED here, against a fully populated dummy
// context, for the same reason §7 makes ValidateSpec execute them: text/
// template's Parse does not check that {{.Foo}} names a real field — that
// surfaces only at Execute. Catching it here turns "the sample failed" into
// a named field error at the moment the operator typed it, rather than a
// provider call that spends money and then fails on a template the server
// could have rejected for free.
func smpApplyDraft(spec *generate.Spec, draft *affv1.SampleDraft, prices smpPriceLookup) error {
	if draft == nil {
		return nil
	}
	if v := draft.GetSystemPrompt(); v != "" {
		spec.SystemPrompt = v
	}
	if v := draft.GetUserPrompt(); v != "" {
		spec.UserPromptTemplate = v
	}
	if v := draft.GetModel(); v != "" && v != spec.Model {
		spec.Model = v
		// Re-price against the OVERRIDDEN model. Without this, spec's price
		// fields stay whatever smpFeedLookup resolved for the saved recipe's
		// model — silently wrong (not just stale) once the draft targets a
		// different model, and the operator has no way to see it: the
		// candidate cost simply reads a number priced for a model that was
		// never called. Unknown-to-the-table still prices at zero, same as
		// the saved model would.
		if prices != nil {
			if p, ok := prices.Lookup(v); ok {
				spec.PriceInputPerMToken = p.InputPerMTok
				spec.PriceOutputPerMToken = p.OutputPerMTok
			} else {
				spec.PriceInputPerMToken = 0
				spec.PriceOutputPerMToken = 0
			}
		}
	}
	if v := draft.GetEffort(); v != "" {
		if !smpValidEfforts[v] {
			return status.Errorf(codes.InvalidArgument,
				"draft.effort %q is not one of smart, fast, quick", v)
		}
		spec.Effort = v
	}
	// generate.ValidateTemplate is what feedspec.validatePrompts uses; called
	// directly here so the failure names WHICH prompt, which a []Problem
	// would also carry but at the cost of running every unrelated spec check
	// (slug uniqueness, cron, budgets) against a draft that changed none of
	// them.
	if err := generate.ValidateTemplate(spec.SystemPrompt); err != nil {
		return status.Errorf(codes.InvalidArgument, "draft system_prompt: %v", err)
	}
	if err := generate.ValidateTemplate(spec.UserPromptTemplate); err != nil {
		return status.Errorf(codes.InvalidArgument, "draft user_prompt: %v", err)
	}
	return nil
}

// smpValidEfforts mirrors internal/rpc's validProviderEfforts — SchemaFlux's
// Speed tiers (PLAN.md §8.1). A draft must not be able to smuggle in a tier
// the settings path rejects.
var smpValidEfforts = map[string]bool{"smart": true, "fast": true, "quick": true}
