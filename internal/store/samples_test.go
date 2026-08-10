package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// makeSampleItem builds an item suitable for PromoteSample. Named distinctly
// from items_test.go's makeItem and runs_test.go's makeRunItem, even though
// there is no actual collision (same package) — keeps sample-specific
// fixtures easy to find.
func makeSampleItem(key string) model.Item {
	return model.Item{
		ItemKey:     key,
		ContentHash: "hash-" + key,
		Title:       "Title " + key,
		SummaryText: "summary",
		BodyHTML:    "<p>body</p>",
		Origin:      model.OriginSampled,
	}
}

func TestPutSampleAndGetSampleRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	id, err := s.PutSample(ctx, feedID, []byte(`{"title":"hi"}`), 100, 50, 0.01, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}
	if id == 0 {
		t.Fatal("put sample returned id 0")
	}

	got, err := s.GetSample(ctx, id)
	if err != nil {
		t.Fatalf("get sample: %v", err)
	}
	if got.FeedID != feedID {
		t.Errorf("feed_id = %d, want %d", got.FeedID, feedID)
	}
	if string(got.Payload) != `{"title":"hi"}` {
		t.Errorf("payload = %q, want %q", got.Payload, `{"title":"hi"}`)
	}
	if got.TokensIn != 100 || got.TokensOut != 50 || got.CostUSD != 0.01 {
		t.Errorf("token/cost round trip mismatch: %+v", got)
	}
	if !got.ExpiresAt.After(got.CreatedAt) {
		t.Errorf("expires_at %v is not after created_at %v", got.ExpiresAt, got.CreatedAt)
	}
}

func TestGetSampleMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSample(t.Context(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetSampleExpiredReturnsErrNotFoundAndIsAbsentFromList(t *testing.T) {
	// §12.3: an expired sample must behave as absent, not merely stale, so a
	// promote can never reach a prompt that's since changed.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	id, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, -time.Hour) // already expired
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	if _, err := s.GetSample(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get expired sample err = %v, want ErrNotFound", err)
	}

	list, err := s.ListSamples(ctx, feedID)
	if err != nil {
		t.Fatalf("list samples: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expired sample appeared in ListSamples: %+v", list)
	}
}

func TestListSamplesReturnsUnexpiredNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	first, err := s.PutSample(ctx, feedID, []byte(`{"n":1}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put first: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // created_at has millisecond-ish precision headroom via RFC3339Nano
	second, err := s.PutSample(ctx, feedID, []byte(`{"n":2}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put second: %v", err)
	}
	if _, err := s.PutSample(ctx, feedID, []byte(`{"n":3}`), 1, 1, 0, -time.Hour); err != nil {
		t.Fatalf("put expired: %v", err)
	}

	got, err := s.ListSamples(ctx, feedID)
	if err != nil {
		t.Fatalf("list samples: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2 (expired excluded)", len(got))
	}
	if got[0].ID != second || got[1].ID != first {
		t.Errorf("order = [%d, %d], want [%d, %d] (newest first)", got[0].ID, got[1].ID, second, first)
	}
}

func TestDiscardSampleRemovesIt(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	id, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	if err := s.DiscardSample(ctx, id); err != nil {
		t.Fatalf("discard: %v", err)
	}

	if _, err := s.GetSample(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after discard: err = %v, want ErrNotFound", err)
	}
}

func TestDiscardSampleMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.DiscardSample(t.Context(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPruneExpiredSamplesDeletesOnlyExpiredAndReportsCount(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	live, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put live: %v", err)
	}
	if _, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, -time.Hour); err != nil {
		t.Fatalf("put expired 1: %v", err)
	}
	if _, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, -2*time.Hour); err != nil {
		t.Fatalf("put expired 2: %v", err)
	}

	n, err := s.PruneExpiredSamples(ctx, time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}

	var remaining int
	if err := s.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples`).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Errorf("remaining samples = %d, want 1", remaining)
	}

	// The unexpired one survives, unharmed.
	if _, err := s.GetSample(ctx, live); err != nil {
		t.Errorf("live sample missing after prune: %v", err)
	}

	// A second prune finds nothing left to delete.
	n2, err := s.PruneExpiredSamples(ctx, time.Now())
	if err != nil {
		t.Fatalf("second prune: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second prune removed %d, want 0", n2)
	}
}

func TestPromoteSampleCreatesOneItemAndRemovesSample(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	sampleID, err := s.PutSample(ctx, feedID, []byte(`{}`), 10, 5, 0.001, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	itemID, err := s.PromoteSample(ctx, sampleID, makeSampleItem("promoted-1"))
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if itemID == 0 {
		t.Fatal("promote returned item id 0")
	}

	got, err := s.GetItem(ctx, "promoted-1")
	if err != nil {
		t.Fatalf("get promoted item: %v", err)
	}
	if got.FeedID != feedID {
		t.Errorf("promoted item feed_id = %d, want %d", got.FeedID, feedID)
	}
	if got.Origin != model.OriginSampled {
		t.Errorf("promoted item origin = %q, want %q", got.Origin, model.OriginSampled)
	}

	items, err := s.ListItems(ctx, feedID, 10, false)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items after promote, want exactly 1", len(items))
	}

	if _, err := s.GetSample(ctx, sampleID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sample still present after promote: err = %v, want ErrNotFound", err)
	}
}

func TestPromoteSampleFailureLeavesNeitherItemNorSampleGone(t *testing.T) {
	// Force a failure downstream of "sample confirmed to exist" by promoting
	// an item whose content_hash collides with one already in the feed
	// (items.content_hash is UNIQUE per feed, §5.1) — the insert fails, and
	// the whole transaction must roll back: the sample must still be there,
	// and no second item may have landed.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	existing := makeItem(feedID, "already-here", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	existing.ContentHash = "dup-hash"
	if _, err := s.InsertItem(ctx, existing); err != nil {
		t.Fatalf("seed existing item: %v", err)
	}

	sampleID, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	colliding := makeSampleItem("promoted-collide")
	colliding.ContentHash = "dup-hash" // forces the failure

	_, err = s.PromoteSample(ctx, sampleID, colliding)
	if err == nil {
		t.Fatal("promote with colliding content_hash succeeded, want an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected ErrNotFound: %v", err)
	}

	// Neither effect happened: the sample survives...
	if _, gerr := s.GetSample(ctx, sampleID); gerr != nil {
		t.Errorf("sample was discarded despite the failed promote: %v", gerr)
	}
	// ...and no second item landed.
	items, lerr := s.ListItems(ctx, feedID, 10, false)
	if lerr != nil {
		t.Fatalf("list items: %v", lerr)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items after failed promote, want exactly 1 (only the seed)", len(items))
	}
}

func TestPromoteSampleStampsStrictlyAfterNewestExistingItemEvenWhenClockIsBehind(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Seed an item published far in the future relative to the real clock —
	// simulating "the clock is behind the feed's newest item".
	future := time.Now().Add(24 * time.Hour)
	if _, err := s.InsertItem(ctx, makeItem(feedID, "future-item", future)); err != nil {
		t.Fatalf("seed future item: %v", err)
	}

	sampleID, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	if _, err := s.PromoteSample(ctx, sampleID, makeSampleItem("promoted-after-future")); err != nil {
		t.Fatalf("promote: %v", err)
	}

	got, err := s.GetItem(ctx, "promoted-after-future")
	if err != nil {
		t.Fatalf("get promoted item: %v", err)
	}
	if !got.PublishedAt.After(future) {
		t.Errorf("promoted published_at %v is not strictly after existing newest %v", got.PublishedAt, future)
	}
}

func TestPromoteSampleRetriesOnTimestampCollision(t *testing.T) {
	// A promote can race a scheduled run stamping the same second (§11), and
	// PromoteSample must retry past that rather than surface the raw UNIQUE
	// constraint error. Reproducing a genuine race deterministically (instead
	// of hoping two real time.Now() calls happen to land on the same second)
	// needs two things: a fixed clock, so the stamp PromoteSample will choose
	// is known in advance, and a real race window — samples.go's
	// promoteAfterReadNewestHook fires after PromoteSample has read the
	// feed's newest item but before its own insert runs, which is exactly
	// where a competing writer's commit would land in production.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	sampleID, err := s.PutSample(ctx, feedID, []byte(`{}`), 1, 1, 0, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	fixed := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	origClock := promoteClock
	promoteClock = func() time.Time { return fixed }
	t.Cleanup(func() { promoteClock = origClock })

	// A second, independent connection to the SAME file, standing in for a
	// concurrent writer (e.g. a scheduled run) that is not going through
	// PromoteSample's own collision-avoidance at all.
	rival, err := sql.Open(driverName, writerDSN(s.Path()))
	if err != nil {
		t.Fatalf("open rival connection: %v", err)
	}
	t.Cleanup(func() { _ = rival.Close() })

	origHook := promoteAfterReadNewestHook
	promoteAfterReadNewestHook = func() {
		// Runs after PromoteSample has read "no existing items" (newest is
		// zero) but before it computes and inserts its own stamp, which (with
		// promoteClock fixed and nothing to bump past) will be exactly
		// "fixed". Steal that timestamp out from under it first.
		now := time.Now().UTC().Format(timeLayout)
		if _, err := rival.ExecContext(ctx, `
			INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
			                    published_at, origin, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			feedID, "rival-item", "rival-hash", "Rival", "summary", "<p>x</p>",
			fixed.Format(timeLayout), string(model.OriginGenerated), now, now,
		); err != nil {
			t.Fatalf("rival insert: %v", err)
		}
	}
	t.Cleanup(func() { promoteAfterReadNewestHook = origHook })

	itemID, err := s.PromoteSample(ctx, sampleID, makeSampleItem("promoted-retried"))
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if itemID == 0 {
		t.Fatal("promote returned item id 0")
	}

	got, err := s.GetItem(ctx, "promoted-retried")
	if err != nil {
		t.Fatalf("get promoted item: %v", err)
	}
	if got.PublishedAt.Equal(fixed) {
		t.Fatalf("promoted item landed on the rival's timestamp %v instead of retrying past it", fixed)
	}
	if !got.PublishedAt.After(fixed) {
		t.Errorf("promoted published_at %v did not retry past the collision at %v", got.PublishedAt, fixed)
	}

	rivalItem, err := s.GetItem(ctx, "rival-item")
	if err != nil {
		t.Fatalf("get rival item: %v", err)
	}
	if !rivalItem.PublishedAt.Equal(fixed) {
		t.Errorf("rival item published_at = %v, want %v (sanity check the collision was real)", rivalItem.PublishedAt, fixed)
	}
}
