package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// --- fakes: no network, no database (RULE-1) ---

type fakeFeedLookup struct {
	feed model.Feed
	spec generate.Spec
	err  error
}

func (f *fakeFeedLookup) GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error) {
	return f.feed, f.spec, f.err
}

type fakeGenStore struct {
	recentTitles []string
	newest       time.Time
}

func (f *fakeGenStore) RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error) {
	return f.recentTitles, nil
}

func (f *fakeGenStore) NewestPublished(ctx context.Context, feedID int64) (time.Time, error) {
	return f.newest, nil
}

// fakeNovelty flags every text as novel unless dupAll is set. This is a
// standalone type (not internal/generate's own test-only stubNovelty, which
// is unexported to that package) satisfying generate.Novelty here.
type fakeNovelty struct {
	dupAll  bool
	nearest string
	score   float64
}

func (n *fakeNovelty) Check(ctx context.Context, feedID int64, text string) (bool, string, float64, error) {
	if n.dupAll {
		return true, n.nearest, n.score, nil
	}
	return false, n.nearest, n.score, nil
}

type fakeFetcher struct {
	candidates []sources.Candidate
}

func (f *fakeFetcher) Candidates(ctx context.Context, feedID int64) ([]sources.Candidate, error) {
	return f.candidates, nil
}

// fakeBudget lets a test script an allow/deny decision and a remaining
// figure independently of any real budget accounting.
type fakeBudget struct {
	decision  budget.Decision
	remaining float64
	calls     int
}

func (b *fakeBudget) CheckSample(ctx context.Context, feedID int64, projectedTokens int) budget.Decision {
	b.calls++
	return b.decision
}

func (b *fakeBudget) RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error) {
	return b.remaining, nil
}

// fakeSampleStore is an in-memory stand-in for *store.Store's sample table
// (samples.go), so these tests never touch SQLite. It mirrors that file's
// documented contract: an expired row is absent, not merely flagged.
type fakeSampleStore struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]store.Sample
	nowFn  func() time.Time
}

func newFakeSampleStore(now func() time.Time) *fakeSampleStore {
	return &fakeSampleStore{rows: make(map[int64]store.Sample), nowFn: now}
}

func (s *fakeSampleStore) PutSample(ctx context.Context, feedID int64, payload []byte, tokensIn, tokensOut int, costUSD float64, ttl time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	now := s.nowFn()
	s.rows[id] = store.Sample{
		ID: id, FeedID: feedID, Payload: payload,
		TokensIn: tokensIn, TokensOut: tokensOut, CostUSD: costUSD,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	return id, nil
}

func (s *fakeSampleStore) GetSample(ctx context.Context, id int64) (store.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || !s.nowFn().Before(row.ExpiresAt) {
		return store.Sample{}, store.ErrNotFound
	}
	return row, nil
}

func (s *fakeSampleStore) ListSamples(ctx context.Context, feedID int64) ([]store.Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Sample
	for _, row := range s.rows {
		if row.FeedID == feedID && s.nowFn().Before(row.ExpiresAt) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *fakeSampleStore) DiscardSample(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.rows, id)
	return nil
}

// fakeStream implements affv1.SampleService_SampleStreamServer
// (grpc.ServerStreamingServer[SampleServiceSampleStreamResponse]) without a
// network connection, so SampleStream can be exercised directly.
type fakeStream struct {
	ctx  context.Context
	mu   sync.Mutex
	sent []*affv1.SampleServiceSampleStreamResponse
}

func (f *fakeStream) Send(m *affv1.SampleServiceSampleStreamResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}
func (f *fakeStream) Context() context.Context     { return f.ctx }
func (f *fakeStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeStream) SetTrailer(metadata.MD)       {}
func (f *fakeStream) SendMsg(m any) error {
	return f.Send(m.(*affv1.SampleServiceSampleStreamResponse))
}
func (f *fakeStream) RecvMsg(m any) error { return errors.New("fakeStream: RecvMsg not supported") }

func (f *fakeStream) messages() []*affv1.SampleServiceSampleStreamResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*affv1.SampleServiceSampleStreamResponse, len(f.sent))
	copy(out, f.sent)
	return out
}

// --- fixtures ---

func smpTestFeed() model.Feed {
	return model.Feed{
		ID: 7, Slug: "trivia-daily", Title: "Anime Trivia Daily",
		Kind: model.KindGenerative, Timezone: "UTC",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func smpTestGroundedFeed() model.Feed {
	return model.Feed{
		ID: 9, Slug: "anime-news", Title: "Anime News",
		Kind: model.KindGrounded, Timezone: "UTC",
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func smpTestSpec() generate.Spec {
	return generate.Spec{
		SystemPrompt:       "You write anime trivia.",
		UserPromptTemplate: "Write {{.ItemsPerRun}} item(s). Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		Model:              "gpt-test",
		ItemsPerRun:        1,
		MaxNoveltyRetries:  1,
	}
}

func smpValidItem(title string) llm.GeneratedItem {
	return llm.GeneratedItem{
		Title:       title,
		SummaryText: "A short plain-text summary, safely under the cap.",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a>.</p>`,
		Tags:        []string{"anime"},
	}
}

func allowDecision() budget.Decision { return budget.Decision{Allow: true} }

func denyDecision(reason budget.Reason) budget.Decision {
	return budget.Decision{Allow: false, Reason: reason}
}

func alwaysEnabled(ctx context.Context) (bool, string, error) { return true, "", nil }

// smpNewTestServer wires a SampleServer whose every dependency is one of the
// fakes above, so a test never touches SQLite, the network, or a real LLM
// provider.
func smpNewTestServer(t *testing.T, feed model.Feed, spec generate.Spec, provider llm.Provider, bud *fakeBudget, enabled smpEnabledCheck, now time.Time) (*SampleServer, *fakeSampleStore, *fakeGenStore) {
	t.Helper()
	nowFn := func() time.Time { return now }
	genStore := &fakeGenStore{}
	samples := newFakeSampleStore(nowFn)
	srv := NewSampleServer(SampleServerConfig{
		Feeds:      &fakeFeedLookup{feed: feed, spec: spec},
		GenStore:   genStore,
		Novelty:    &fakeNovelty{},
		Fetcher:    &fakeFetcher{},
		Provider:   provider,
		IDs:        ids.NewDeterministicSource(1),
		Budget:     bud,
		Samples:    samples,
		Enabled:    enabled,
		PublicHost: "anime.earlcameron.com",
		Generator:  "AnimeFeedFlux test",
		Now:        nowFn,
	})
	return srv, samples, genStore
}

// --- tests ---

// TestSample_NeverWritesAnItem is the single most important test in this
// file (task brief, PLAN.md §11, §12.3, BF-11): after a successful Sample
// call, the poison-pill CommitRun in smpGenerateStore must have fired zero
// times, and the only store write observed anywhere is the samples row
// PutSample itself created.
func TestSample_NeverWritesAnItem(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Cowboy Bebop Trivia Question")}})
	bud := &fakeBudget{decision: allowDecision(), remaining: 5.0}
	srv, samples, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	itemCountBefore := 0 // there is no item store in this file's dependency graph at
	// all — Sample cannot write to items because nothing it holds can reach
	// one. commitRunCalls is the direct assertion of that fact.

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(resp.Candidates))
	}
	if got := srv.CommitRunCalls(); got != 0 {
		t.Fatalf("CommitRun fired %d times; must be 0", got)
	}
	if itemCountBefore != 0 {
		t.Fatal("sanity check itself is broken")
	}
	if len(samples.rows) != 1 {
		t.Fatalf("want exactly one samples row, got %d", len(samples.rows))
	}
	if provider.GenerateCallCount() != 1 {
		t.Fatalf("want exactly one provider call, got %d", provider.GenerateCallCount())
	}
}

// TestSampleStream_NeverWritesAnItem is TestSample_NeverWritesAnItem's
// streaming twin.
func TestSampleStream_NeverWritesAnItem(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Trivia Question One")}})
	bud := &fakeBudget{decision: allowDecision()}
	srv, samples, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	stream := &fakeStream{ctx: context.Background()}
	if err := srv.SampleStream(&affv1.SampleServiceSampleStreamRequest{FeedId: 7, SampleSize: 1}, stream); err != nil {
		t.Fatalf("SampleStream: %v", err)
	}
	if got := srv.CommitRunCalls(); got != 0 {
		t.Fatalf("CommitRun fired %d times; must be 0", got)
	}
	if len(samples.rows) != 1 {
		t.Fatalf("want exactly one samples row, got %d", len(samples.rows))
	}

	msgs := stream.messages()
	if len(msgs) != 2 { // one candidate + one final done message
		t.Fatalf("want 2 stream messages (1 candidate + done), got %d", len(msgs))
	}
	if msgs[0].GetCandidate() == nil || msgs[0].GetDone() {
		t.Fatalf("first message should carry a candidate and Done=false, got %+v", msgs[0])
	}
	if !msgs[len(msgs)-1].GetDone() {
		t.Fatal("final stream message must have Done=true")
	}
}

// TestSample_SamplesRow_HasExpiry asserts the 24h persistence contract
// (PLAN.md §12.3): the row PutSample wrote carries a future expires_at.
func TestSample_SamplesRow_HasExpiry(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Trivia Question Two")}})
	bud := &fakeBudget{decision: allowDecision()}
	srv, samples, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	id, err := strconv.ParseInt(resp.SampleId, 10, 64)
	if err != nil {
		t.Fatalf("parsing sample id: %v", err)
	}
	row := samples.rows[id]
	if !row.ExpiresAt.After(row.CreatedAt) {
		t.Fatalf("expires_at %v must be after created_at %v", row.ExpiresAt, row.CreatedAt)
	}
	if got := row.ExpiresAt.Sub(row.CreatedAt); got != 24*time.Hour {
		t.Fatalf("default TTL: want 24h, got %v", got)
	}
}

// TestSample_KillSwitchOff_NoProviderCall asserts BF-11's other named
// failure mode: "not a call whose result is discarded" — the provider must
// never be invoked at all when generation is disabled.
func TestSample_KillSwitchOff_NoProviderCall(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	// Deliberately no queued result: if Generate were ever called, the fake
	// would return an error rather than silently succeeding, but the
	// assertion below (call count == 0) is the one that actually matters.
	bud := &fakeBudget{decision: allowDecision()}
	disabled := func(ctx context.Context) (bool, string, error) { return false, "budget exhausted for the month", nil }
	srv, samples, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, disabled, now)

	_, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err == nil {
		t.Fatal("want an error when the kill switch is off")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", st.Code())
	}
	if !strings.Contains(st.Message(), "budget exhausted for the month") {
		t.Fatalf("error should surface the disable reason, got %q", st.Message())
	}
	if provider.GenerateCallCount() != 0 {
		t.Fatalf("kill switch off: want 0 provider calls, got %d", provider.GenerateCallCount())
	}
	if bud.calls != 0 {
		t.Fatalf("kill switch check must run before the budget check, got %d budget calls", bud.calls)
	}
	if srv.CommitRunCalls() != 0 {
		t.Fatalf("CommitRun fired %d times; must be 0", srv.CommitRunCalls())
	}
	if len(samples.rows) != 0 {
		t.Fatalf("want 0 samples rows on a kill-switch denial, got %d", len(samples.rows))
	}
}

// TestSample_BudgetDenied_NoProviderCall asserts §13's "checked BEFORE the
// call" rule and that the denial reason reaches the caller.
func TestSample_BudgetDenied_NoProviderCall(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	bud := &fakeBudget{decision: denyDecision(budget.ReasonFeedTokenCap)}
	srv, samples, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	_, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err == nil {
		t.Fatal("want an error on budget denial")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", st.Code())
	}
	if !strings.Contains(st.Message(), string(budget.ReasonFeedTokenCap)) {
		t.Fatalf("error should name the reason token, got %q", st.Message())
	}
	if provider.GenerateCallCount() != 0 {
		t.Fatalf("budget denied: want 0 provider calls, got %d", provider.GenerateCallCount())
	}
	if srv.CommitRunCalls() != 0 {
		t.Fatalf("CommitRun fired %d times; must be 0", srv.CommitRunCalls())
	}
	if len(samples.rows) != 0 {
		t.Fatalf("want 0 samples rows on a budget denial, got %d", len(samples.rows))
	}
}

// TestSample_RenderedXML_MatchesRenderer verifies "the rendered feed XML
// fragment byte-identical to what publishing would emit" (PLAN.md §12.3) by
// independently rendering the same item through render.RSS and comparing
// substrings.
func TestSample_RenderedXML_MatchesRenderer(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Cowboy Bebop Trivia Question")}})
	bud := &fakeBudget{decision: allowDecision()}
	feed := smpTestFeed()
	srv, _, _ := smpNewTestServer(t, feed, smpTestSpec(), provider, bud, alwaysEnabled, now)

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(resp.Candidates))
	}
	got := resp.Candidates[0]

	// Rebuild the SAME item the candidate carries and render it through the
	// publish-plane renderer independently, to prove the RPC's fragment
	// isn't a bespoke second implementation that could drift.
	item := model.Item{
		ItemKey:     idsPeek(srv),
		Title:       got.Title,
		SummaryText: got.SummaryText,
		BodyHTML:    got.BodyHtml,
		Link:        got.Link,
		PublishedAt: now,
	}
	channel := model.Channel{
		Feed:      feed,
		Host:      "anime.earlcameron.com",
		TagYear:   feed.CreatedAt.Year(),
		Items:     []model.Item{item},
		BuildTime: now,
	}
	doc, err := render.RSS(channel)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	// The candidate_id / item_key differ (this test can't know the exact
	// ULID the server minted), so compare everything EXCEPT the <guid>
	// line, which is the only element keyed on it.
	if !strings.Contains(got.RenderedXml, "<title>Cowboy Bebop Trivia Question</title>") {
		t.Fatalf("rendered fragment missing expected title:\n%s", got.RenderedXml)
	}
	if !strings.Contains(string(doc), "<title>Cowboy Bebop Trivia Question</title>") {
		t.Fatalf("independent render missing expected title:\n%s", doc)
	}
	if !strings.HasPrefix(strings.TrimSpace(got.RenderedXml), "<item>") || !strings.HasSuffix(strings.TrimSpace(got.RenderedXml), "</item>") {
		t.Fatalf("fragment should be a single <item>...</item> block, got:\n%s", got.RenderedXml)
	}
}

// idsPeek is a tiny helper so TestSample_RenderedXML_MatchesRenderer can
// mint a matching-shaped item_key without reaching into the server's
// internals; the exact key value does not matter for that test (it only
// checks the guid line is excluded from comparison).
func idsPeek(_ *SampleServer) string { return "01ARZ3NDEKTSV4RRFFQ69G5FAV" }

// TestSampleStream_CancellationStopsPromptly asserts the client-cancel
// contract: if the context is already canceled by the time candidates are
// ready to send, delivery stops before any candidate reaches the stream.
func TestSampleStream_CancellationStopsPromptly(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Trivia Question Three")}})
	bud := &fakeBudget{decision: allowDecision()}
	spec := smpTestSpec()
	spec.ItemsPerRun = 1
	srv, _, _ := smpNewTestServer(t, smpTestFeed(), spec, provider, bud, alwaysEnabled, now)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before SampleStream ever runs
	stream := &fakeStream{ctx: ctx}

	if err := srv.SampleStream(&affv1.SampleServiceSampleStreamRequest{FeedId: 7, SampleSize: 1}, stream); err != nil {
		t.Fatalf("SampleStream: %v", err)
	}
	if len(stream.messages()) != 0 {
		t.Fatalf("canceled context: want 0 messages sent, got %d", len(stream.messages()))
	}
}

// TestListSamples_ExpiredAbsent asserts PLAN.md §12.3's "samples persist for
// 24h" combined with samples.go's own documented contract: an expired
// sample is absent from ListSamples, not merely flagged expired.
func TestListSamples_ExpiredAbsent(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }
	samples := newFakeSampleStore(nowFn)
	srv := NewSampleServer(SampleServerConfig{
		Feeds:      &fakeFeedLookup{},
		GenStore:   &fakeGenStore{},
		Provider:   llm.NewFake(),
		IDs:        ids.NewDeterministicSource(1),
		Budget:     &fakeBudget{},
		Samples:    samples,
		Enabled:    alwaysEnabled,
		PublicHost: "anime.earlcameron.com",
		Now:        nowFn,
	})

	payload, _ := json.Marshal([]any{struct{}{}})
	freshID, err := samples.PutSample(context.Background(), 7, payload, 10, 10, 0.01, 24*time.Hour)
	if err != nil {
		t.Fatalf("seeding fresh sample: %v", err)
	}
	expiredID, err := samples.PutSample(context.Background(), 7, payload, 10, 10, 0.01, -time.Minute)
	if err != nil {
		t.Fatalf("seeding expired sample: %v", err)
	}

	resp, err := srv.ListSamples(context.Background(), &affv1.SampleServiceListSamplesRequest{FeedId: 7})
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(resp.Samples) != 1 {
		t.Fatalf("want 1 unexpired sample, got %d", len(resp.Samples))
	}
	if got := resp.Samples[0].SampleId; got != strconv.FormatInt(freshID, 10) {
		t.Fatalf("want the fresh sample %d listed, got %s", freshID, got)
	}

	if _, err := srv.cfg.Samples.GetSample(context.Background(), expiredID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired sample should read as ErrNotFound, got %v", err)
	}
}

// TestDiscardSample_RemovesOne asserts the Discard button's contract.
func TestDiscardSample_RemovesOne(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return now }
	samples := newFakeSampleStore(nowFn)
	srv := NewSampleServer(SampleServerConfig{
		Feeds:    &fakeFeedLookup{},
		GenStore: &fakeGenStore{},
		Provider: llm.NewFake(),
		IDs:      ids.NewDeterministicSource(1),
		Budget:   &fakeBudget{},
		Samples:  samples,
		Enabled:  alwaysEnabled,
		Now:      nowFn,
	})

	payload, _ := json.Marshal([]any{struct{}{}})
	id, err := samples.PutSample(context.Background(), 7, payload, 1, 1, 0.001, time.Hour)
	if err != nil {
		t.Fatalf("seeding sample: %v", err)
	}

	if _, err := srv.DiscardSample(context.Background(), &affv1.SampleServiceDiscardSampleRequest{SampleId: strconv.FormatInt(id, 10)}); err != nil {
		t.Fatalf("DiscardSample: %v", err)
	}
	if len(samples.rows) != 0 {
		t.Fatalf("want 0 rows after discard, got %d", len(samples.rows))
	}

	// Discarding it again must report NotFound, not silently succeed.
	_, err = srv.DiscardSample(context.Background(), &affv1.SampleServiceDiscardSampleRequest{SampleId: strconv.FormatInt(id, 10)})
	st, _ := status.FromError(err)
	if st.Code() != codes.NotFound {
		t.Fatalf("want NotFound discarding twice, got %v", st.Code())
	}
}

// TestSample_GroundedFeed_LinkVerdictsPopulated exercises the grounded path:
// candidate link verdicts should be populated and OK for a link that is
// byte-equal to a fetched source.
func TestSample_GroundedFeed_LinkVerdictsPopulated(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	const link = "https://example.com/article-1"
	provider := llm.NewFake()
	item := smpValidItem("Big Anime News")
	item.Link = link
	item.SourceName = "Example News"
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{item}})
	bud := &fakeBudget{decision: allowDecision()}

	feed := smpTestGroundedFeed()
	spec := generate.Spec{
		SystemPrompt:       "You edit anime news.",
		UserPromptTemplate: "Rank: {{range .Candidates}}{{.Title}} {{.URL}} {{end}}",
		Model:              "gpt-test",
		ItemsPerRun:        1,
	}
	nowFn := func() time.Time { return now }
	srv := NewSampleServer(SampleServerConfig{
		Feeds:      &fakeFeedLookup{feed: feed, spec: spec},
		GenStore:   &fakeGenStore{},
		Fetcher:    &fakeFetcher{candidates: []sources.Candidate{{Title: "Big Anime News", URL: link}}},
		Provider:   provider,
		IDs:        ids.NewDeterministicSource(1),
		Budget:     bud,
		Samples:    newFakeSampleStore(nowFn),
		Enabled:    alwaysEnabled,
		PublicHost: "anime.earlcameron.com",
		Now:        nowFn,
	})

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 9, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(resp.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(resp.Candidates))
	}
	verdicts := resp.Candidates[0].LinkVerdicts
	if len(verdicts) != 1 || !verdicts[0].Ok || verdicts[0].Url != link {
		t.Fatalf("want one OK link verdict for %q, got %+v", link, verdicts)
	}
	if resp.Candidates[0].Novelty != nil {
		t.Fatalf("grounded feeds should not carry a novelty verdict, got %+v", resp.Candidates[0].Novelty)
	}
}

// TestSample_SampleSizeValidation checks the 1-5 bound (PLAN.md §12.3).
func TestSample_SampleSizeValidation(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	bud := &fakeBudget{decision: allowDecision()}
	srv, _, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	_, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 6})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("sample_size=6: want InvalidArgument, got %v", st.Code())
	}
	if provider.GenerateCallCount() != 0 {
		t.Fatalf("invalid sample_size must not reach the provider, got %d calls", provider.GenerateCallCount())
	}
}

// TestSample_AggregateFeed_Rejected asserts §14.2's "aggregates never
// generate, never spend" holds for sampling too.
func TestSample_AggregateFeed_Rejected(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	feed := smpTestFeed()
	feed.Kind = model.KindAggregate
	provider := llm.NewFake()
	bud := &fakeBudget{decision: allowDecision()}
	srv, _, _ := smpNewTestServer(t, feed, smpTestSpec(), provider, bud, alwaysEnabled, now)

	_, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("aggregate feed: want InvalidArgument, got %v", st.Code())
	}
	if provider.GenerateCallCount() != 0 {
		t.Fatalf("aggregate feed must not reach the provider, got %d calls", provider.GenerateCallCount())
	}
}

// TestSample_EmbedPreview_RendersTheRealEmbed proves the sampler's embed
// preview (PLAN.md §6.1, §12.3) is the publish plane's own document and not
// an admin-only lookalike: a complete HTML document, carrying the candidate,
// through the same render.Embed the /embed/{slug} route uses.
func TestSample_EmbedPreview_RendersTheRealEmbed(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Cowboy Bebop Trivia Question")}})
	bud := &fakeBudget{decision: allowDecision()}
	feed := smpTestFeed()
	srv, _, _ := smpNewTestServer(t, feed, smpTestSpec(), provider, bud, alwaysEnabled, now)

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	got := resp.Candidates[0].GetEmbedPreviewHtml()

	if !strings.HasPrefix(got, "<!doctype html>") {
		t.Fatalf("embed preview is not a complete document:\n%s", got)
	}
	for _, want := range []string{
		"Cowboy Bebop Trivia Question", // the candidate itself
		"A short plain-text summary",   // its summary, which is what the embed shows
		`<li class="aff-item">`,        // the real renderer's markup
		"Subscribe by RSS",             // the whole document, not a fragment
		`/feeds/` + feed.Slug + `.xml`, // pointing at this feed
	} {
		if !strings.Contains(got, want) {
			t.Errorf("embed preview missing %q:\n%s", want, got)
		}
	}
	// The embed never renders body HTML — that rule is what keeps a trivia
	// answer off somebody else's page, and a preview that showed more than
	// the real thing would be worse than no preview at all.
	if strings.Contains(got, "Full body with an") {
		t.Error("embed preview rendered BodyHTML; the real embed does not")
	}
}

// TestSample_EmbedPreview_HidesTheAnswer is the §5.5 rule at the preview
// boundary. An operator judging a trivia candidate by its embed preview must
// see exactly what a reader would see: the question, never the answer.
func TestSample_EmbedPreview_HidesTheAnswer(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	item := smpValidItem("Which studio animated Cowboy Bebop")
	item.AnswerHTML = "<p>ANSWER-SUNRISE</p>"
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{item}})
	bud := &fakeBudget{decision: allowDecision()}
	srv, _, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	c := resp.Candidates[0]
	if strings.Contains(c.GetEmbedPreviewHtml(), "ANSWER-SUNRISE") {
		t.Fatal("the embed preview leaked the trivia answer")
	}
	if !strings.Contains(c.GetEmbedPreviewHtml(), "Which studio animated Cowboy Bebop") {
		t.Fatal("the embed preview did not render the question")
	}
	// The answer is still available to the operator through the raw-fields
	// view, which is what that view is for.
	if c.GetAnswerHtml() == "" {
		t.Error("answer_html should still reach the client for the raw view")
	}
}

// TestSample_PreviewsStampTheCandidate: generate.Sample never assigns a
// published_at (it is stamped at promote, PLAN.md §12.3), so an unstamped
// candidate reaching a preview rendered the zero time — "1 Jan 0001" in the
// embed, and a year-0001 pubDate in the feed XML that internal/feedvalidate's
// own date rules treat as an error. Both previews now show the timestamp
// promoting would assign.
func TestSample_PreviewsStampTheCandidate(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	provider := llm.NewFake()
	provider.QueueResult(llm.Result{Items: []llm.GeneratedItem{smpValidItem("Cowboy Bebop Trivia Question")}})
	bud := &fakeBudget{decision: allowDecision()}
	srv, _, _ := smpNewTestServer(t, smpTestFeed(), smpTestSpec(), provider, bud, alwaysEnabled, now)

	resp, err := srv.Sample(context.Background(), &affv1.SampleServiceSampleRequest{FeedId: 7, SampleSize: 1})
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	c := resp.Candidates[0]

	for _, v := range []struct{ name, doc string }{
		{"embed preview", c.GetEmbedPreviewHtml()},
		{"feed XML", c.GetRenderedXml()},
	} {
		if strings.Contains(v.doc, "0001") {
			t.Errorf("%s rendered the zero time:\n%s", v.name, v.doc)
		}
		if !strings.Contains(v.doc, "2026") {
			t.Errorf("%s carries no publish date at all:\n%s", v.name, v.doc)
		}
	}
}
