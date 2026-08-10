package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// hookRecorder collects fired slugs and lets a test assert exact counts and
// contents, and is safe for the concurrency test's parallel firing.
type hookRecorder struct {
	mu    sync.Mutex
	slugs []string
}

func (r *hookRecorder) hook(slug string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slugs = append(r.slugs, slug)
}

func (r *hookRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.slugs)
}

func (r *hookRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.slugs))
	copy(out, r.slugs)
	return out
}

func newHookedTestStore(t *testing.T) (*Hooked, *hookRecorder) {
	t.Helper()
	s := newTestStore(t) // items_test.go's helper: opens + migrates a temp DB
	rec := &hookRecorder{}
	return NewHooked(s, rec.hook), rec
}

// --- fires exactly once per committed write ---------------------------

func TestHookedInsertItemFiresOnceOnTheOwningFeed(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, err := h.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if rec.count() != 1 || rec.all()[0] != "trivia" {
		t.Fatalf("CreateFeed hook fired %v, want exactly [\"trivia\"]", rec.all())
	}

	item := makeItem(feedID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	if got := rec.count(); got != 2 {
		t.Fatalf("hook fired %d times after CreateFeed+InsertItem, want 2 (got %v)", got, rec.all())
	}
	if got := rec.all()[1]; got != "trivia" {
		t.Fatalf("InsertItem hook fired for slug %q, want %q", got, "trivia")
	}
}

func TestHookedUpdateItemFiresOnce(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("trivia"))
	item := makeItem(feedID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	got, err := h.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}

	before := rec.count()
	got.Title = "Edited title"
	if err := h.UpdateItem(ctx, got, got.Version); err != nil {
		t.Fatalf("update item: %v", err)
	}
	if got := rec.count(); got != before+1 {
		t.Fatalf("UpdateItem fired the hook %d times, want exactly 1 more (before=%d)", got-before, before)
	}
}

func TestHookedSoftDeleteAndRestoreEachFireOnce(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("trivia"))
	item := makeItem(feedID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	before := rec.count()
	if err := h.SoftDeleteItem(ctx, "k1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if got := rec.count(); got != before+1 {
		t.Fatalf("SoftDeleteItem fired %d times, want 1", got-before)
	}
	if got := rec.all()[len(rec.all())-1]; got != "trivia" {
		t.Fatalf("SoftDeleteItem fired for slug %q, want trivia", got)
	}

	before = rec.count()
	if err := h.RestoreItem(ctx, "k1"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := rec.count(); got != before+1 {
		t.Fatalf("RestoreItem fired %d times, want 1", got-before)
	}
}

func TestHookedCommitRunFiresOnceForTheFeed(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("news"))
	runID, err := h.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	before := rec.count()
	items := []model.Item{
		makeItem(feedID, "r1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)),
		makeItem(feedID, "r2", time.Date(2026, 8, 9, 12, 0, 1, 0, time.UTC)),
	}
	if err := h.CommitRun(ctx, runID, items, RunSummary{}); err != nil {
		t.Fatalf("commit run: %v", err)
	}
	if got := rec.count(); got != before+1 {
		t.Fatalf("CommitRun fired the hook %d times, want exactly 1 regardless of item count", got-before)
	}
	if got := rec.all()[len(rec.all())-1]; got != "news" {
		t.Fatalf("CommitRun fired for slug %q, want news", got)
	}
}

func TestHookedPromoteSampleFiresOnce(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("trivia"))
	sampleID, err := h.PutSample(ctx, feedID, []byte(`{}`), 10, 20, 0.01, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	before := rec.count()
	item := makeItem(0, "promoted-1", time.Time{}) // FeedID/PublishedAt overwritten by PromoteSample
	item.Origin = model.OriginSampled
	if _, err := h.PromoteSample(ctx, sampleID, item); err != nil {
		t.Fatalf("promote sample: %v", err)
	}
	if got := rec.count(); got != before+1 {
		t.Fatalf("PromoteSample fired the hook %d times, want 1", got-before)
	}
	if got := rec.all()[len(rec.all())-1]; got != "trivia" {
		t.Fatalf("PromoteSample fired for slug %q, want trivia", got)
	}
}

// --- does NOT fire on a rolled-back write -------------------------------

func TestHookedInsertItemDoesNotFireOnConstraintFailure(t *testing.T) {
	// UNIQUE(feed_id, content_hash) rejects a repeated content_hash (§5.1's
	// idempotency rule) — the insert never commits, so the hook must not
	// fire for the second attempt.
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("trivia"))
	item := makeItem(feedID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	before := rec.count()
	dupe := makeItem(feedID, "k2", time.Date(2026, 8, 9, 12, 0, 1, 0, time.UTC))
	dupe.ContentHash = item.ContentHash // force the UNIQUE collision
	if _, err := h.InsertItem(ctx, dupe); err == nil {
		t.Fatal("expected the duplicate content_hash insert to fail")
	}
	if got := rec.count(); got != before {
		t.Fatalf("hook fired %d times on a failed insert, want 0", got-before)
	}
}

func TestHookedCommitRunDoesNotFireWhenTheTransactionRollsBack(t *testing.T) {
	// Two items in one run sharing published_at collide on
	// UNIQUE(feed_id, published_at) (§5.5), which rolls the whole CommitRun
	// transaction back per its own doc comment ("no partial commit"). The
	// hook must see that as nothing having happened.
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("news"))
	runID, err := h.StartRun(ctx, feedID, "cron", "worker-1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	before := rec.count()
	sameStamp := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	items := []model.Item{
		makeItem(feedID, "r1", sameStamp),
		makeItem(feedID, "r2", sameStamp), // collides with r1's published_at
	}
	if err := h.CommitRun(ctx, runID, items, RunSummary{}); err == nil {
		t.Fatal("expected CommitRun to fail on colliding published_at")
	}
	if got := rec.count(); got != before {
		t.Fatalf("hook fired %d times on a rolled-back CommitRun, want 0", got-before)
	}

	// And the run genuinely did not commit: it must still be 'running',
	// never left half-closed.
	runs, err := h.ListRuns(ctx, feedID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("run status after rollback = %+v, want a single run still 'running'", runs)
	}
}

func TestHookedUpdateItemDoesNotFireOnVersionConflict(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	feedID, _ := h.CreateFeed(ctx, makeFeed("trivia"))
	item := makeItem(feedID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))
	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	got, err := h.GetItem(ctx, "k1")
	if err != nil {
		t.Fatalf("get item: %v", err)
	}

	before := rec.count()
	got.Title = "stale writer's edit"
	err = h.UpdateItem(ctx, got, got.Version+1) // wrong expected_version
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
	if got := rec.count(); got != before {
		t.Fatalf("hook fired %d times on a version-conflict update, want 0", got-before)
	}
}

// --- fires for feed-level writes as well as item writes ------------------

func TestHookedFeedLevelWriteFiresOnFeedCreation(t *testing.T) {
	// PLAN.md §11: the rule covers feed-level writes, not only item writes,
	// because a feed's own fields (title, og:image, ttl) are baked into
	// every channel element. CreateFeed is the one feed-level write *Store
	// implements today (see hooks.go's doc comment on why
	// Update/SetEnabled/SetMembers are not wrapped here: they do not exist
	// on *Store yet — TODOS.md's B1 phase is unbuilt).
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	if _, err := h.CreateFeed(ctx, makeFeed("weekly-recap")); err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if got := rec.all(); len(got) != 1 || got[0] != "weekly-recap" {
		t.Fatalf("feed-level write fired %v, want exactly [\"weekly-recap\"]", got)
	}
}

// --- invalidating a slug drops every format plus any aggregate -----------
//
// Hooked only knows a feed's OWN slug — it has no notion of aggregate
// membership (feed_members), which is a control-plane (FeedService) concern
// per PLAN.md §11's own text ("Any aggregate containing the feed is
// invalidated too"). So the "aggregate" half of this requirement is proven
// against publish.Cache directly (internal/publish/invalidate_test.go
// covers Cache.Invalidate's format+index fan-out), and this test proves the
// half that IS this package's job: firing carries the correct, single feed
// slug that a caller's closure would then fan out from (e.g. also
// invalidating every aggregate slug that lists this member — the caller's
// job, documented in invalidate.go's wiring comment).

func TestHookedFiresTheExactFeedSlugForFanOut(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	memberID, _ := h.CreateFeed(ctx, makeFeed("daily-trivia"))
	item := makeItem(memberID, "k1", time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC))

	rec.mu.Lock()
	rec.slugs = nil // reset after the CreateFeed noise above
	rec.mu.Unlock()

	if _, err := h.InsertItem(ctx, item); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	got := rec.all()
	if len(got) != 1 || got[0] != "daily-trivia" {
		t.Fatalf("hook fired %v, want exactly the member's own slug [\"daily-trivia\"] for the caller to fan out from", got)
	}
}

// --- safe for concurrent use ---------------------------------------------

func TestHookedConcurrentWrites(t *testing.T) {
	h, rec := newHookedTestStore(t)
	ctx := context.Background()

	const feedCount = 5
	const itemsPerFeed = 20
	feedIDs := make([]int64, feedCount)
	slugs := make([]string, feedCount)
	for i := 0; i < feedCount; i++ {
		slug := "feed-" + string(rune('a'+i))
		id, err := h.CreateFeed(ctx, makeFeed(slug))
		if err != nil {
			t.Fatalf("create feed %d: %v", i, err)
		}
		feedIDs[i] = id
		slugs[i] = slug
	}

	before := rec.count()
	var wg sync.WaitGroup
	for i := 0; i < feedCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
			for j := 0; j < itemsPerFeed; j++ {
				it := makeItem(feedIDs[i], slugs[i]+"-"+string(rune('a'+j)), base.Add(time.Duration(j)*time.Second))
				if _, err := h.InsertItem(ctx, it); err != nil {
					t.Errorf("insert item feed %d item %d: %v", i, j, err)
				}
			}
		}()
	}
	wg.Wait()

	want := feedCount * itemsPerFeed
	if got := rec.count() - before; got != want {
		t.Fatalf("hook fired %d times across concurrent writers, want %d (no lost or duplicate fires)", got, want)
	}

	// Every fired slug must be one of the feeds we created — no cross-talk,
	// no corruption of the shared slice under concurrent append.
	valid := map[string]bool{}
	for _, s := range slugs {
		valid[s] = true
	}
	for _, s := range rec.all() {
		if !valid[s] {
			t.Fatalf("hook fired for unexpected slug %q", s)
		}
	}
}
