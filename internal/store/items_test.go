package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// newTestStore mirrors openTemp in store_test.go, named distinctly so this
// file does not redeclare a helper that already exists in this package.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := t.Context()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func makeFeed(slug string) model.Feed {
	return model.Feed{
		Slug:     slug,
		Title:    "Test Feed " + slug,
		Kind:     model.KindGenerative,
		Timezone: "UTC",
		Enabled:  true,
	}
}

func makeItem(feedID int64, key string, published time.Time) model.Item {
	return model.Item{
		FeedID:      feedID,
		ItemKey:     key,
		ContentHash: "hash-" + key,
		Title:       "Title " + key,
		SummaryText: "summary",
		BodyHTML:    "<p>body</p>",
		PublishedAt: published,
		Origin:      model.OriginGenerated,
	}
}

func TestCreateFeedAndGetFeedBySlugRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	id, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if id == 0 {
		t.Fatal("create feed returned id 0")
	}

	got, err := s.GetFeedBySlug(ctx, "trivia")
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %d, want %d", got.ID, id)
	}
	if got.Slug != "trivia" || got.Title != "Test Feed trivia" || got.Kind != model.KindGenerative {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !got.Enabled {
		t.Error("enabled did not round trip")
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
}

func TestGetFeedBySlugMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetFeedBySlug(t.Context(), "no-such-feed")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestInsertItemAndGetItemRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	item := makeItem(feedID, "key-1", published)
	item.Link = "https://example.com/a"
	item.SourceName = "Example"

	id, err := s.InsertItem(ctx, item)
	if err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if id == 0 {
		t.Fatal("insert item returned id 0")
	}

	got, err := s.GetItem(ctx, "key-1")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.ItemKey != "key-1" || got.Title != "Title key-1" || got.ContentHash != "hash-key-1" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	if !got.PublishedAt.Equal(published) {
		t.Errorf("published_at = %v, want %v", got.PublishedAt, published)
	}
	if got.Link != "https://example.com/a" || got.SourceName != "Example" {
		t.Errorf("nullable fields did not round trip: %+v", got)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want 1", got.Version)
	}
	if got.IsDeleted() {
		t.Error("fresh item reports deleted")
	}
}

func TestGetItemMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetItem(t.Context(), "no-such-key")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListItemsReturnsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("news"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	keys := []string{"a", "b", "c"}
	for i, k := range keys {
		it := makeItem(feedID, k, base.Add(time.Duration(i)*time.Second))
		if _, err := s.InsertItem(ctx, it); err != nil {
			t.Fatalf("insert %s: %v", k, err)
		}
	}

	got, err := s.ListItems(ctx, feedID, 10, false)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3", len(got))
	}
	want := []string{"c", "b", "a"}
	for i, w := range want {
		if got[i].ItemKey != w {
			t.Errorf("position %d: key = %q, want %q", i, got[i].ItemKey, w)
		}
	}
}

func TestListItemsExcludesSoftDeletedUnlessIncluded(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("news"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := s.InsertItem(ctx, makeItem(feedID, "alive", base)); err != nil {
		t.Fatalf("insert alive: %v", err)
	}
	if _, err := s.InsertItem(ctx, makeItem(feedID, "gone", base.Add(time.Second))); err != nil {
		t.Fatalf("insert gone: %v", err)
	}
	if err := s.SoftDeleteItem(ctx, "gone"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	visible, err := s.ListItems(ctx, feedID, 10, false)
	if err != nil {
		t.Fatalf("list (exclude deleted): %v", err)
	}
	if len(visible) != 1 || visible[0].ItemKey != "alive" {
		t.Errorf("got %+v, want only 'alive'", visible)
	}

	all, err := s.ListItems(ctx, feedID, 10, true)
	if err != nil {
		t.Fatalf("list (include deleted): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d items with includeDeleted, want 2", len(all))
	}
}

func TestSoftDeleteThenGetItemStillFindsItAsDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := s.InsertItem(ctx, makeItem(feedID, "k1", time.Now().UTC())); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.SoftDeleteItem(ctx, "k1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	got, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get after soft delete: %v", err)
	}
	if !got.IsDeleted() {
		t.Error("IsDeleted() = false after SoftDeleteItem")
	}
}

func TestRestoreItemClearsSoftDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if _, err := s.InsertItem(ctx, makeItem(feedID, "k1", time.Now().UTC())); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.SoftDeleteItem(ctx, "k1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := s.RestoreItem(ctx, "k1"); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if got.IsDeleted() {
		t.Error("IsDeleted() = true after RestoreItem")
	}
}

func TestSoftDeleteItemMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.SoftDeleteItem(t.Context(), "no-such-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRestoreItemMissingReturnsErrNotFound(t *testing.T) {
	s := newTestStore(t)
	if err := s.RestoreItem(t.Context(), "no-such-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateItemStaleVersionReturnsErrVersionConflictAndChangesNothing(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	id, err := s.InsertItem(ctx, makeItem(feedID, "k1", published))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	before, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	update := before
	update.ID = id
	update.Title = "Changed title"

	err = s.UpdateItem(ctx, update, before.Version+1) // wrong on purpose
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}

	after, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.Title != before.Title {
		t.Errorf("title changed despite version conflict: %q -> %q", before.Title, after.Title)
	}
	if after.Version != before.Version {
		t.Errorf("version changed despite version conflict: %d -> %d", before.Version, after.Version)
	}
}

func TestUpdateItemRightVersionSucceedsAndBumpsVersion(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	id, err := s.InsertItem(ctx, makeItem(feedID, "k1", published))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	before, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	update := before
	update.ID = id
	update.Title = "Changed title"
	update.SummaryText = "changed summary"

	if err := s.UpdateItem(ctx, update, before.Version); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, err := s.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.Title != "Changed title" || after.SummaryText != "changed summary" {
		t.Errorf("update did not apply: %+v", after)
	}
	if after.Version != before.Version+1 {
		t.Errorf("version = %d, want %d", after.Version, before.Version+1)
	}
}

func TestUpdateItemDoesNotChangeItemKey(t *testing.T) {
	// The whole reason item_key is excluded from the UPDATE (§5.1): if this
	// ever fails, an edited item resurfaces as a duplicate everywhere.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	published := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	id, err := s.InsertItem(ctx, makeItem(feedID, "stable-key", published))
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	before, err := s.GetItem(ctx, "stable-key")
	if err != nil {
		t.Fatalf("get before: %v", err)
	}

	update := before
	update.ID = id
	update.ItemKey = "attempted-new-key" // must be ignored
	update.Title = "Edited"

	if err := s.UpdateItem(ctx, update, before.Version); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Still reachable by the ORIGINAL key.
	got, err := s.GetItem(ctx, "stable-key")
	if err != nil {
		t.Fatalf("get by original key after update: %v", err)
	}
	if got.Title != "Edited" {
		t.Errorf("title did not update: %q", got.Title)
	}

	// And NOT reachable by the attempted new key.
	if _, err := s.GetItem(ctx, "attempted-new-key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("item became reachable under the attempted new key (err = %v)", err)
	}
}
