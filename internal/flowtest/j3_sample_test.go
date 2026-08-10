// J3 — iterate a prompt by sampling (PLAN.md §22 "J3 — Iterate a prompt by
// sampling", TODOS.md BF-11..BF-15).
package flowtest

import (
	"bytes"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
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
