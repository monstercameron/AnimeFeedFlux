// J3 — iterate a prompt by sampling (PLAN.md §22 "J3 — Iterate a prompt by
// sampling", TODOS.md BF-11..BF-15).
package flowtest

import (
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/budget"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// TestJ3_SampleWritesNoItems is BF-11, called out in the task brief as the
// single most important assertion in this whole suite: a sampler that
// writes an item is indistinguishable from a working feature until a human
// notices a duplicate nobody promoted.
func TestJ3_SampleWritesNoItems(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	before, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount before: %v", err)
	}
	if before != 0 {
		t.Fatalf("fresh feed already has %d items", before)
	}

	w.Provider.QueueResult(validGenerateResult("What year did the first Gundam series air"))

	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(outcome.Result.Items) != 1 {
		t.Fatalf("Sample returned %d items, want 1 (fixture failed contract.go validation?)", len(outcome.Result.Items))
	}

	// BF-11 (§22 J3): items row count is unchanged after sampling.
	after, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount after: %v", err)
	}
	if after != before {
		t.Fatalf("items row count changed from %d to %d across a sample — a sampler that writes is the worst bug this design can have", before, after)
	}
}

// TestJ3_SampleWithMultipleItemsStillWritesNothing repeats BF-11 across
// several sampled items and several sample calls in a row, since a sampler
// that is clean on exactly one item/one call could still leak a write on a
// second — the "iterate" part of J3's name (sample, edit, sample again).
func TestJ3_SampleWithMultipleItemsStillWritesNothing(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-iterate"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	spec := validSampleSpec()
	spec.ItemsPerRun = 3
	for i := 0; i < 3; i++ {
		w.Provider.QueueResult(validGenerateResult(
			"First candidate trivia question",
			"Second candidate trivia question",
			"Third candidate trivia question",
		))
		if _, err := w.Sample(ctx, feed, spec); err != nil {
			t.Fatalf("Sample call %d: %v", i, err)
		}
	}

	// BF-11 (§22 J3), repeated across an iterate loop.
	n, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("items row count is %d after three sample calls, want 0", n)
	}
}

// TestJ3_SamplePersistsSampleRowWithExpiry is BF-12.
func TestJ3_SamplePersistsSampleRowWithExpiry(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-samples-row"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("Which studio animated this classic film"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	// BF-12 (§22 J3): a samples row exists with expires_at set.
	row, err := w.SampleRow(ctx, outcome.SampleID)
	if err != nil {
		t.Fatalf("reading back samples row %d: %v", outcome.SampleID, err)
	}
	if row.FeedID != feed.ID {
		t.Fatalf("sample row feed_id = %d, want %d", row.FeedID, feed.ID)
	}
	if row.ExpiresAt.IsZero() {
		t.Fatal("samples row has no expires_at")
	}
	if !row.ExpiresAt.After(row.CreatedAt) {
		t.Fatalf("expires_at %v is not after created_at %v", row.ExpiresAt, row.CreatedAt)
	}
}

// TestJ3_SampleCostIsNonZeroFromTheSameBudget is half of BF-13: the reported
// cost is non-zero. The other half — "debited from the same budget scheduled
// runs use" — is a property of internal/budget.CheckRequest treating
// budget.KindSample and budget.KindScheduled identically (already unit
// tested in internal/budget), not something a flow test re-proves; what a
// flow test CAN prove is that Sample's own reported numbers are the same
// shape (tokens, USD) a scheduled run reports, so nothing downstream would
// need a special case to spend them from one ledger.
func TestJ3_SampleCostIsNonZeroFromTheSameBudget(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-cost"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("How many episodes does this anime have"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	// BF-13 (§22 J3): the reported cost is non-zero.
	if outcome.Result.EstCostUSD <= 0 {
		t.Fatalf("EstCostUSD = %v, want > 0", outcome.Result.EstCostUSD)
	}
	if outcome.Result.TokensIn <= 0 || outcome.Result.TokensOut <= 0 {
		t.Fatalf("tokens in/out = %d/%d, want both > 0", outcome.Result.TokensIn, outcome.Result.TokensOut)
	}

	// The persisted samples row (BF-12's row) carries the identical figures
	// a scheduled run's runs.est_cost_usd/tokens_in/tokens_out would carry
	// for the same call — the "same budget" claim's storage half.
	row, err := w.SampleRow(ctx, outcome.SampleID)
	if err != nil {
		t.Fatalf("SampleRow: %v", err)
	}
	if row.CostUSD != outcome.Result.EstCostUSD {
		t.Fatalf("persisted samples.cost_usd = %v, want %v (the reported figure)", row.CostUSD, outcome.Result.EstCostUSD)
	}
	if row.TokensIn != outcome.Result.TokensIn || row.TokensOut != outcome.Result.TokensOut {
		t.Fatalf("persisted samples tokens = %d/%d, want %d/%d",
			row.TokensIn, row.TokensOut, outcome.Result.TokensIn, outcome.Result.TokensOut)
	}
}

// TestJ3_SampleXMLFragmentMatchesPublishing is BF-14. Sample() itself never
// stamps an item_key or published_at (only a real promote does, PLAN.md §9
// step 7, and store.PromoteSample stamps published_at from the wall clock
// internally — it is not clock-injectable, so a test cannot predict that
// value before calling it). What this test CAN and does prove: take the
// sampled candidate, stamp it for promotion, and render it with render.RSS
// BEFORE it ever touches the database; then promote it, read the row back
// through the real store, and render THAT. If the two differ, something
// between "what the admin was shown" and "what SQLite gives back" silently
// rewrote the content — double-sanitized HTML, a re-escaped summary, a
// truncated field — which is exactly the class of bug BF-14 exists to catch
// (the preview lied about what publishing would emit). The one field that
// cannot be compared this way is published_at itself, since only
// PromoteSample assigns it and it never leaves this test's control; the
// in-memory preview is updated to the real stamped value (read back once,
// after promote) before rendering it, isolating the comparison to exactly
// the content fields "publishing would emit" is actually about.
func TestJ3_SampleXMLFragmentMatchesPublishing(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-xml-fragment"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("Which mecha anime popularized the giant robot genre"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(outcome.Result.Items) != 1 {
		t.Fatalf("Sample returned %d items, want 1", len(outcome.Result.Items))
	}

	previewItem := w.StampForPromotion(outcome.Result.Items[0], feed)

	if _, err := w.PromoteSample(ctx, outcome.SampleID, previewItem); err != nil {
		t.Fatalf("PromoteSample: %v", err)
	}

	storedItem, err := w.Store.GetItem(ctx, previewItem.ItemKey)
	if err != nil {
		t.Fatalf("reading back the promoted item: %v", err)
	}
	// The only field PromoteSample computes rather than accepts. Align the
	// in-memory preview to it so the rendered comparison below is about
	// content, not about a timestamp this test was never meant to predict.
	previewItem.PublishedAt = storedItem.PublishedAt

	previewXML, err := render.RSS(w.Channel(feed, []model.Item{previewItem}))
	if err != nil {
		t.Fatalf("rendering preview RSS: %v", err)
	}
	publishedXML, err := render.RSS(w.Channel(feed, []model.Item{storedItem}))
	if err != nil {
		t.Fatalf("rendering published RSS: %v", err)
	}

	// BF-14 (§22 J3): the returned XML fragment is byte-identical to what
	// publishing would emit.
	if !bytes.Equal(previewXML, publishedXML) {
		t.Fatalf("sample preview XML does not match the published XML:\n--- preview ---\n%s\n--- published ---\n%s",
			previewXML, publishedXML)
	}
}

// TestJ3_KillSwitchBlocksTheProviderEntirely is BF-15.
func TestJ3_KillSwitchBlocksTheProviderEntirely(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j3-kill-switch"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	// Queue a result that WOULD succeed if the provider were called, so a
	// bug that ignores the kill switch is caught by items existing, not just
	// by a call count that happened to already be zero.
	w.Provider.QueueResult(validGenerateResult("This should never be requested"))

	w.KillSwitch = true
	_, err = w.Sample(ctx, feed, validSampleSpec())
	if err == nil {
		t.Fatal("Sample succeeded with the kill switch on, want an error")
	}

	// BF-15 (§22 J3): with the kill switch on, no provider call is made at
	// all.
	if calls := w.Provider.GenerateCallCount(); calls != 0 {
		t.Fatalf("provider Generate was called %d times with the kill switch on, want 0", calls)
	}

	n, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 0 {
		t.Fatalf("items row count is %d with the kill switch on, want 0", n)
	}

	samples, err := w.Store.ListSamples(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("%d samples rows exist with the kill switch on, want 0", len(samples))
	}
}

// --- B2-06: SampleStream driven over a real network connection ---
//
// Every TestJ3_* test above drives World.Sample, which calls
// generate.Sample in-process — proving the sampling pipeline's own logic,
// never that a real client on the other end of a real socket gets the same
// candidates back. TestB2_06_SampleStreamOverRealConnection closes that gap
// for SampleStream specifically; RunService.Watch's real-connection half of
// B2-06 is BF-40/41/43 in j9_watch_test.go, and TestJ7_
// ElevatedSessionReachesOnlyPasswordAndTOTP (BF-32) already proves the
// session interceptor works over a real connection of this same shape, so
// this test — like j9's — registers only SampleService on a plain TCP
// grpc.Server with no interceptor chained in; auth is not what B2-06 is
// about. Per §17.5, it asserts on RESULTING SYSTEM STATE: the candidates
// arrive as separate stream messages rather than one batched response, and
// the samples row this real RPC call persisted is readable back from the
// store afterward — not merely that the RPC returned without error.

// b2FeedLookup satisfies SampleServerConfig.Feeds against this package's
// real store, handing back validSampleSpec() for every feed — the same
// "a fixed, valid generate.Spec supplied directly" pattern this package's
// own doc comment already establishes (the scheduler that would map a
// stored FeedSpec into a live generate.Spec, PLAN.md A7, does not exist
// yet).
type b2FeedLookup struct{ w *World }

func (l b2FeedLookup) GetFeedForSample(ctx context.Context, feedID int64) (model.Feed, generate.Spec, error) {
	f, found, err := l.w.feedByID(ctx, feedID)
	if err != nil {
		return model.Feed{}, generate.Spec{}, err
	}
	if !found {
		return model.Feed{}, generate.Spec{}, store.ErrNotFound
	}
	return f, validSampleSpec(), nil
}

// b2AlwaysAllowBudget always allows: budget enforcement is BF-13's job, not
// B2-06's.
type b2AlwaysAllowBudget struct{}

func (b2AlwaysAllowBudget) CheckSample(ctx context.Context, feedID int64, projectedTokens int) budget.Decision {
	return budget.Decision{Allow: true}
}

func (b2AlwaysAllowBudget) RemainingDailyUSD(ctx context.Context, feedID int64) (float64, error) {
	return 100, nil
}

// b2BuildSampleRPCServer stands up a real, minimal *grpc.Server exposing
// ONLY SampleService against w's real store, over a real TCP loopback
// listener — the same shape j9BuildRunRPCServer (j9_watch_test.go) uses.
func b2BuildSampleRPCServer(t *testing.T, w *World) (addr string) {
	t.Helper()
	srv := rpc.NewSampleServer(rpc.SampleServerConfig{
		Feeds:    b2FeedLookup{w: w},
		GenStore: storeAdapter{s: w.Store},
		Provider: w.Provider,
		IDs:      w.IDs,
		Budget:   b2AlwaysAllowBudget{},
		Samples:  w.Store,
		Enabled:  func(context.Context) (bool, string, error) { return true, "", nil },
		Now:      w.Clock.Now,
	})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for b2's plain grpc server: %v", err)
	}
	gs := grpc.NewServer()
	affv1.RegisterSampleServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

// TestB2_06_SampleStreamOverRealConnection is B2-06's SampleStream half.
func TestB2_06_SampleStreamOverRealConnection(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("b2-06-samplestream"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	w.Provider.QueueResult(validGenerateResult(
		"Real-connection candidate one",
		"Real-connection candidate two",
	))

	addr := b2BuildSampleRPCServer(t, w)
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing b2's plain grpc server: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := affv1.NewSampleServiceClient(conn)

	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stream, err := client.SampleStream(streamCtx, &affv1.SampleServiceSampleStreamRequest{
		FeedId:     feed.ID,
		SampleSize: 2,
	})
	if err != nil {
		t.Fatalf("SampleStream: %v", err)
	}

	var sampleID string
	var titles []string
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream.Recv: %v", err)
		}
		if msg.GetSampleId() != "" {
			sampleID = msg.GetSampleId()
		}
		if c := msg.GetCandidate(); c != nil {
			titles = append(titles, c.GetTitle())
		}
	}

	// B2-06: the candidates were delivered as separate stream messages over
	// the real connection, not one batched response.
	if len(titles) != 2 {
		t.Fatalf("SampleStream delivered %d candidate messages over the real connection, want 2 (titles: %v)", len(titles), titles)
	}

	// §17.5: resulting system state, not a mock's call log — the samples
	// row this real RPC call persisted is readable back from the store,
	// over the real connection, matching BF-12's in-process assertion.
	if sampleID == "" {
		t.Fatal("SampleStream never sent a sample id over the real connection")
	}
	id, err := strconv.ParseInt(sampleID, 10, 64)
	if err != nil {
		t.Fatalf("parsing sample id %q: %v", sampleID, err)
	}
	row, err := w.SampleRow(ctx, id)
	if err != nil {
		t.Fatalf("reading back the samples row SampleStream persisted, over the real connection: %v", err)
	}
	if row.FeedID != feed.ID {
		t.Fatalf("persisted sample row feed_id = %d, want %d", row.FeedID, feed.ID)
	}
}
