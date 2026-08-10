// J4 — promote a sample (PLAN.md §22 "J4 — Promote a sample", TODOS.md
// BF-16..BF-21).
package flowtest

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
)

// promoteOne is the shared J4 setup every test below builds on: a feed
// seeded with one existing item, a fresh sample, and a promotion of it. It
// returns the feed, the pre-existing item, the id PromoteSample assigned,
// and the item read back from the store, so each test only has to assert
// its own piece of §22 J4's sanity list instead of re-deriving this setup.
func promoteOne(t *testing.T, w *World, slug string) (feed model.Feed, existing model.Item, promotedID int64, promoted model.Item) {
	t.Helper()
	ctx := t.Context()

	var err error
	feed, err = w.CreateFeed(ctx, validGenerativeFeed(slug))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	existing = model.Item{
		FeedID:      feed.ID,
		ItemKey:     w.IDs.NewItemKey(w.Clock.Now()),
		ContentHash: "promote-baseline-" + slug,
		Title:       "The pre-existing item already in this feed",
		SummaryText: "Whatever was published before this promotion.",
		BodyHTML:    "<p>Existing body.</p>",
		Link:        "https://example.com/existing",
		SourceName:  "AnimeFeedFlux",
		PublishedAt: w.Clock.Now(),
		Origin:      model.OriginGenerated,
		CreatedAt:   w.Clock.Now(),
		UpdatedAt:   w.Clock.Now(),
	}
	if _, err := w.Store.InsertItem(ctx, existing); err != nil {
		t.Fatalf("seeding existing item: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("A brand new candidate worth promoting today"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(outcome.Result.Items) != 1 {
		t.Fatalf("Sample returned %d items, want 1", len(outcome.Result.Items))
	}

	candidate := w.StampForPromotion(outcome.Result.Items[0], feed)
	promotedID, err = w.PromoteSample(ctx, outcome.SampleID, candidate)
	if err != nil {
		t.Fatalf("PromoteSample: %v", err)
	}

	promoted, err = w.Store.GetItem(ctx, candidate.ItemKey)
	if err != nil {
		t.Fatalf("reading back the promoted item: %v", err)
	}
	return feed, existing, promotedID, promoted
}

// TestJ4_ExactlyOneNewItemOriginSampled is BF-16.
func TestJ4_ExactlyOneNewItemOriginSampled(t *testing.T) {
	w := New(t)
	feed, existing, _, promoted := promoteOne(t, w, "j4-origin")

	// BF-16 (§22 J4): exactly one new item, origin = sampled.
	n, err := w.ItemCount(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 2 { // the seeded existing item plus exactly one promoted item
		t.Fatalf("feed has %d items after promoting one sample onto one existing item, want 2", n)
	}
	if promoted.Origin != model.OriginSampled {
		t.Fatalf("promoted item origin = %q, want %q", promoted.Origin, model.OriginSampled)
	}
	if promoted.ItemKey == existing.ItemKey {
		t.Fatal("promoted item reused the existing item's item_key")
	}
}

// TestJ4_PublishedAtStrictlyAfterPreviousNewest is BF-17.
func TestJ4_PublishedAtStrictlyAfterPreviousNewest(t *testing.T) {
	w := New(t)
	_, existing, _, promoted := promoteOne(t, w, "j4-published-at")

	// BF-17 (§22 J4): published_at is strictly greater than the previously
	// newest item.
	if !promoted.PublishedAt.After(existing.PublishedAt) {
		t.Fatalf("promoted published_at %v is not strictly after the previous newest %v",
			promoted.PublishedAt, existing.PublishedAt)
	}
}

// TestJ4_FreshULIDAndGuidContainsIt is BF-18.
func TestJ4_FreshULIDAndGuidContainsIt(t *testing.T) {
	w := New(t)
	feed, existing, _, promoted := promoteOne(t, w, "j4-guid")

	// BF-18 (§22 J4): item_key is a fresh ULID, and its guid contains it.
	if len(promoted.ItemKey) != 26 {
		t.Fatalf("item_key %q is not a 26-character ULID", promoted.ItemKey)
	}
	if promoted.ItemKey == existing.ItemKey {
		t.Fatal("promoted item_key is not fresh — it collides with the pre-existing item")
	}

	ch := w.Channel(feed, []model.Item{promoted})
	rss, err := render.RSS(ch)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	if !strings.Contains(string(rss), promoted.ItemKey) {
		t.Fatalf("RSS guid does not contain the item's ULID %q:\n%s", promoted.ItemKey, rss)
	}
	atom, err := render.Atom(ch)
	if err != nil {
		t.Fatalf("render.Atom: %v", err)
	}
	if !strings.Contains(string(atom), promoted.ItemKey) {
		t.Fatalf("Atom id does not contain the item's ULID %q:\n%s", promoted.ItemKey, atom)
	}
}

// TestJ4_ItemAppearsExactlyOnceInAllThreeFormats is BF-20.
func TestJ4_ItemAppearsExactlyOnceInAllThreeFormats(t *testing.T) {
	w := New(t)
	feed, _, _, promoted := promoteOne(t, w, "j4-three-formats")
	ctx := t.Context()

	items, err := w.Store.ListItems(ctx, feed.ID, 0, false)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	ch := w.Channel(feed, items)

	rss, err := render.RSS(ch)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	atom, err := render.Atom(ch)
	if err != nil {
		t.Fatalf("render.Atom: %v", err)
	}
	jsonFeed, err := render.JSONFeed(ch)
	if err != nil {
		t.Fatalf("render.JSONFeed: %v", err)
	}

	// BF-20 (§22 J4): the item appears exactly once in all three formats.
	if n := strings.Count(string(rss), promoted.ItemKey); n != 1 {
		t.Fatalf("RSS contains the promoted item's key %d times, want 1", n)
	}
	if n := strings.Count(string(atom), promoted.ItemKey); n != 1 {
		t.Fatalf("Atom contains the promoted item's key %d times, want 1", n)
	}
	if n := strings.Count(string(jsonFeed), promoted.ItemKey); n != 1 {
		t.Fatalf("JSON Feed contains the promoted item's key %d times, want 1", n)
	}
}

// TestJ4_TimestampCollisionRetriesInsteadOfFailing is BF-21. store.
// PromoteSample stamps published_at from the real wall clock (it is not
// clock-injectable), so racing it against a real time.Now() call from this
// test would be nanosecond-precision luck, not a reliable test. Instead this
// forces the SAME branch a genuine race would hit, deterministically: the
// feed's newest item is stamped safely in the FUTURE (relative to real
// time.Now()), which makes PromoteSample's own "stamp is not after newest"
// check fail and fall through to its deterministic "newest + 1s" branch —
// exactly the line PLAN.md §11 says a concurrent scheduled run collides
// with. Pre-seeding a second item at precisely that "newest + 1s" instant
// guarantees the first insert attempt collides, so the retry path is
// exercised on every run rather than occasionally.
func TestJ4_TimestampCollisionRetriesInsteadOfFailing(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j4-collision"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	future := time.Now().Add(time.Hour)
	newest := model.Item{
		FeedID:      feed.ID,
		ItemKey:     w.IDs.NewItemKey(w.Clock.Now()),
		ContentHash: "j4-collision-newest",
		Title:       "The feed's current newest item, stamped in the future",
		SummaryText: "Forces PromoteSample's stamp-not-after-newest branch.",
		BodyHTML:    "<p>Newest body.</p>",
		PublishedAt: future,
		Origin:      model.OriginGenerated,
		CreatedAt:   w.Clock.Now(),
		UpdatedAt:   w.Clock.Now(),
	}
	if _, err := w.Store.InsertItem(ctx, newest); err != nil {
		t.Fatalf("seeding newest item: %v", err)
	}

	// The colliding row: exactly newest+1s, the deterministic first stamp
	// PromoteSample will try once "stamp.After(newest)" is false.
	colliding := model.Item{
		FeedID:      feed.ID,
		ItemKey:     w.IDs.NewItemKey(w.Clock.Now()),
		ContentHash: "j4-collision-plus-one",
		Title:       "A concurrent scheduled run's item landing at newest+1s",
		SummaryText: "Whoever got here first.",
		BodyHTML:    "<p>Racing body.</p>",
		PublishedAt: future.Add(time.Second),
		Origin:      model.OriginGenerated,
		CreatedAt:   w.Clock.Now(),
		UpdatedAt:   w.Clock.Now(),
	}
	if _, err := w.Store.InsertItem(ctx, colliding); err != nil {
		t.Fatalf("seeding colliding item: %v", err)
	}

	w.Provider.QueueResult(validGenerateResult("The item being promoted into a colliding second"))
	outcome, err := w.Sample(ctx, feed, validSampleSpec())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	candidate := w.StampForPromotion(outcome.Result.Items[0], feed)

	// BF-21 (§22 J4): a timestamp collision retries at +1s, no constraint
	// error escapes.
	if _, err := w.PromoteSample(ctx, outcome.SampleID, candidate); err != nil {
		t.Fatalf("PromoteSample did not retry past the published_at collision, it returned: %v", err)
	}

	promoted, err := w.Store.GetItem(ctx, candidate.ItemKey)
	if err != nil {
		t.Fatalf("reading back the promoted item: %v", err)
	}
	if !promoted.PublishedAt.After(colliding.PublishedAt) {
		t.Fatalf("promoted published_at %v did not retry past the collision at %v",
			promoted.PublishedAt, colliding.PublishedAt)
	}

	n, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount: %v", err)
	}
	if n != 3 { // past + colliding + the promoted item, none lost to a failed transaction
		t.Fatalf("feed has %d items after the collision retry, want 3", n)
	}
}

// TestJ4_LastBuildDateReflectsThePromotedItem is the lastBuildDate half of
// BF-19 ("the feed's render cache was invalidated and lastBuildDate
// bumped"). The cache-invalidation half is asserted separately — see this
// test's sibling skip below — but lastBuildDate is a property of the
// rendered Channel built from current store state, testable directly: once
// the promoted item exists, a fresh render's lastBuildDate/pubDate must
// reflect it as the newest item, not the pre-promotion state.
func TestJ4_LastBuildDateReflectsThePromotedItem(t *testing.T) {
	w := New(t)
	feed, existing, _, promoted := promoteOne(t, w, "j4-lastbuilddate")
	ctx := t.Context()

	items, err := w.Store.ListItems(ctx, feed.ID, 0, false)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	ch := w.Channel(feed, items)

	newest := ch.NewestPublished()
	if !newest.Equal(promoted.PublishedAt) {
		t.Fatalf("Channel.NewestPublished() = %v, want the promoted item's published_at %v", newest, promoted.PublishedAt)
	}
	if !newest.After(existing.PublishedAt) {
		t.Fatalf("NewestPublished() %v is not after the pre-promotion item %v", newest, existing.PublishedAt)
	}

	rss, err := render.RSS(ch)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	if !strings.Contains(string(rss), render.RFC822(promoted.PublishedAt)) {
		t.Fatalf("rendered <pubDate> does not reflect the promoted item's timestamp:\n%s", rss)
	}
}

// TestJ4_RenderCacheInvalidationIsUnwired documents, rather than papers
// over, a real gap this task's brief specifically calls out as the failure
// mode to catch ("a promote that skips cache invalidation"). PLAN.md §22 J4
// requires "the feed's render cache was invalidated" after a promote.
// internal/publish.Cache (cache.go) exposes Invalidate/InvalidateAll and IS
// what publish.NewServer uses internally — but NewServer returns a bare
// http.Handler with that *Cache unexported and unreachable from outside the
// package, and nothing today calls Invalidate on a write: that wiring is
// the not-yet-built PromoteSample RPC / control-plane (TODOS.md B1-04,
// B1-07). A flow test in internal/flowtest cannot reach into another
// package's private field to invalidate it, and building a parallel cache
// here would test flowtest's own plumbing, not the real one. Per the task's
// instruction to write a visible, named skip rather than silently omitting
// the assertion:
func TestJ4_RenderCacheInvalidationIsUnwired(t *testing.T) {
	t.Skip("blocked on B1: no control-plane path calls publish.Cache.Invalidate on a write; " +
		"publish.NewServer's *Cache is a private field with no external invalidation hook (PLAN.md §22 J4, BF-19)")
}
