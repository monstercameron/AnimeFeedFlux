// J6 — correct a wrong item (PLAN.md §22 "J6 — Correct a wrong item",
// TODOS.md BF-26..BF-30).
//
// There is no CorrectItem RPC/store method yet (TODOS.md B1/C-series own
// that wiring) — internal/store only exposes the pieces §12.4's correction
// mechanism is built FROM: InsertItem (a correction is nothing but a new
// item, origin='correction') and the migrated-but-unwrapped `corrections`
// table (migrations/0002_feeds_items.sql). This file drives those pieces
// directly, through w.Store.Writer() (already exported and already used by
// World's own ItemCount helper in harness.go) for the one INSERT no store
// method wraps yet, rather than inventing a parallel abstraction here that
// the real CorrectItem call would only have to replace.
package flowtest

import (
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/render"
)

// insertItem is a small local wrapper over w.Store.InsertItem building a
// fully-populated model.Item from a few required fields, so every J6 test
// below reads as "insert an item" instead of repeating the same seven-field
// struct literal promoteOne (j4) already needed once.
func insertItem(t *testing.T, w *World, feed model.Feed, title string, publishedAt time.Time, origin model.Origin) model.Item {
	t.Helper()
	now := w.Clock.Now()
	it := model.Item{
		FeedID:      feed.ID,
		ItemKey:     w.IDs.NewItemKey(now),
		ContentHash: "j6-" + feed.Slug + "-" + title,
		Title:       title,
		SummaryText: "A short plain-text summary, safely under the cap.",
		BodyHTML:    "<p>Body.</p>",
		PublishedAt: publishedAt,
		Origin:      origin,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	id, err := w.Store.InsertItem(t.Context(), it)
	if err != nil {
		t.Fatalf("InsertItem(%q): %v", title, err)
	}
	it.ID = id
	// Re-read through GetItem rather than trusting the struct above, so
	// every test asserts against what the STORE says landed (parsed
	// timestamps, defaulted fields), not against this helper's echo of its
	// own input.
	stored, err := w.Store.GetItem(t.Context(), it.ItemKey)
	if err != nil {
		t.Fatalf("reading back inserted item %q: %v", it.ItemKey, err)
	}
	return stored
}

// linkCorrection inserts the corrections row itself (item_id -> the new
// correction row's numeric id, corrects_item_id -> the original's numeric
// id), matching migrations/0002_feeds_items.sql's PRIMARY KEY (item_id,
// corrects_item_id). See the package doc above for why this is raw SQL
// rather than a store method.
func linkCorrection(t *testing.T, w *World, correctionID, correctsID int64) {
	t.Helper()
	if _, err := w.Store.Writer().ExecContext(t.Context(),
		`INSERT INTO corrections (item_id, corrects_item_id) VALUES (?, ?)`,
		correctionID, correctsID); err != nil {
		t.Fatalf("linking correction %d -> %d: %v", correctionID, correctsID, err)
	}
}

// TestJ6_OriginalGuidAndPublishedAtUnchanged is BF-26.
func TestJ6_OriginalGuidAndPublishedAtUnchanged(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j6-unchanged"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	original := insertItem(t, w, feed, "The original item that turns out to be wrong", w.Clock.Now(), model.OriginGenerated)

	w.Clock.Advance(time.Hour)
	correction := insertItem(t, w, feed, "A corrected version of the same fact", w.Clock.Now(), model.OriginCorrection)
	linkCorrection(t, w, correction.ID, original.ID)

	// BF-26 (§22 J6): the original item's guid and published_at are
	// unchanged after a correction. Re-read from the store rather than
	// reusing the `original` value captured before the correction existed —
	// that is the only way this test could catch a correction path that
	// (wrongly) touched the original row.
	reread, err := w.Store.GetItem(ctx, original.ItemKey)
	if err != nil {
		t.Fatalf("re-reading original item: %v", err)
	}
	if reread.ItemKey != original.ItemKey {
		t.Fatalf("original item_key changed: %q -> %q", original.ItemKey, reread.ItemKey)
	}
	if !reread.PublishedAt.Equal(original.PublishedAt) {
		t.Fatalf("original published_at changed: %v -> %v", original.PublishedAt, reread.PublishedAt)
	}

	ch := w.Channel(feed, []model.Item{reread})
	rss, err := render.RSS(ch)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	if !strings.Contains(string(rss), original.ItemKey) {
		t.Fatalf("original item's guid %q not present in its own rendered RSS", original.ItemKey)
	}
}

// TestJ6_CorrectionIsANewItemWithFreshULIDAndLaterPublishedAt is BF-27.
func TestJ6_CorrectionIsANewItemWithFreshULIDAndLaterPublishedAt(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j6-fresh-ulid"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	original := insertItem(t, w, feed, "A fact that will need correcting", w.Clock.Now(), model.OriginGenerated)

	w.Clock.Advance(time.Hour)
	correction := insertItem(t, w, feed, "The corrected fact, published as a new item", w.Clock.Now(), model.OriginCorrection)
	linkCorrection(t, w, correction.ID, original.ID)

	// BF-27 (§22 J6): the correction is a new item with a new ULID and a
	// strictly later published_at.
	if len(correction.ItemKey) != 26 {
		t.Fatalf("correction item_key %q is not a 26-character ULID", correction.ItemKey)
	}
	if correction.ItemKey == original.ItemKey {
		t.Fatal("correction reused the original item's item_key — this is exactly the bug §5.5 forbids")
	}
	if !correction.PublishedAt.After(original.PublishedAt) {
		t.Fatalf("correction published_at %v is not strictly after the original %v", correction.PublishedAt, original.PublishedAt)
	}
	if correction.Origin != model.OriginCorrection {
		t.Fatalf("correction origin = %q, want %q", correction.Origin, model.OriginCorrection)
	}
}

// TestJ6_CorrectionsRowLinksTheTwo is BF-28.
func TestJ6_CorrectionsRowLinksTheTwo(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j6-corrections-row"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	original := insertItem(t, w, feed, "The item a correction will point back to", w.Clock.Now(), model.OriginGenerated)
	w.Clock.Advance(time.Hour)
	correction := insertItem(t, w, feed, "The item that corrects it", w.Clock.Now(), model.OriginCorrection)
	linkCorrection(t, w, correction.ID, original.ID)

	// BF-28 (§22 J6): the corrections row links the two.
	var correctsID int64
	row := w.Store.Writer().QueryRowContext(ctx,
		`SELECT corrects_item_id FROM corrections WHERE item_id = ?`, correction.ID)
	if err := row.Scan(&correctsID); err != nil {
		t.Fatalf("reading back the corrections row for item %d: %v", correction.ID, err)
	}
	if correctsID != original.ID {
		t.Fatalf("corrections row points at item %d, want the original %d", correctsID, original.ID)
	}
}

// TestJ6_OriginalStillResolvableAtItsPermalink is BF-29. It fetches over
// real HTTP (the same discipline §17.5 requires of J10) rather than calling
// the store directly, because the actual guarantee being made is to a
// subscriber's HTTP client, not to a Go caller.
func TestJ6_OriginalStillResolvableAtItsPermalink(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j6-still-resolvable"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	original := insertItem(t, w, feed, "An item that stays resolvable after correction", w.Clock.Now(), model.OriginGenerated)
	w.Clock.Advance(time.Hour)
	correction := insertItem(t, w, feed, "The correction that supersedes it", w.Clock.Now(), model.OriginCorrection)
	linkCorrection(t, w, correction.ID, original.ID)

	resp, err := w.httpServer.Client().Get(w.BaseURL + "/items/" + original.ItemKey)
	if err != nil {
		t.Fatalf("GET the original's permalink: %v", err)
	}
	defer resp.Body.Close()

	// BF-29 (§22 J6): the original is still resolvable at its permalink —
	// 200, not 404 and not 410 (410 is reserved for an actually deleted
	// item, §12.4; a corrected item was never deleted).
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d, want 200", "/items/"+original.ItemKey, resp.StatusCode)
	}
}

// TestJ6_PlainEditProducesNoNewGuidAndNoRedelivery is BF-30, called out in
// the task brief as the assertion that justifies the whole correction
// mechanism (§5.5: editing never reposts). It drives the real
// store.UpdateItem path (the admin "just edit it" temptation the failure
// branch names) and proves the guid space is untouched: same item count,
// same item_key, and exactly one occurrence of that key in the rendered
// feed both before and after.
func TestJ6_PlainEditProducesNoNewGuidAndNoRedelivery(t *testing.T) {
	w := New(t)
	ctx := t.Context()

	feed, err := w.CreateFeed(ctx, validGenerativeFeed("j6-plain-edit"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	original := insertItem(t, w, feed, "An item with a factual mistake in it", w.Clock.Now(), model.OriginGenerated)

	before, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount before edit: %v", err)
	}

	edited := original
	edited.Title = "An item with the mistake fixed in place"
	if err := w.Store.UpdateItem(ctx, edited, original.Version); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}

	// BF-30 (§22 J6): a plain edit produces no new guid and therefore no
	// redelivery.
	after, err := w.ItemCount(ctx, feed.ID)
	if err != nil {
		t.Fatalf("ItemCount after edit: %v", err)
	}
	if after != before {
		t.Fatalf("item count changed from %d to %d across a plain edit — an edit must never create a new item/guid", before, after)
	}

	reread, err := w.Store.GetItem(ctx, original.ItemKey)
	if err != nil {
		t.Fatalf("re-reading edited item: %v", err)
	}
	if reread.ItemKey != original.ItemKey {
		t.Fatalf("item_key changed across an edit: %q -> %q", original.ItemKey, reread.ItemKey)
	}
	if reread.Title != edited.Title {
		t.Fatalf("edit did not take: title = %q, want %q", reread.Title, edited.Title)
	}
	if reread.EditedAt.IsZero() {
		t.Fatal("edited_at was not stamped by UpdateItem")
	}

	ch := w.Channel(feed, []model.Item{reread})
	rss, err := render.RSS(ch)
	if err != nil {
		t.Fatalf("render.RSS: %v", err)
	}
	// Exactly once, not "present": a redelivery bug would show the same guid
	// twice (old cached entry + a wrongly-minted new one), not a missing one.
	if n := strings.Count(string(rss), original.ItemKey); n != 1 {
		t.Fatalf("edited item's guid appears %d times in the rendered feed, want exactly 1", n)
	}
}
