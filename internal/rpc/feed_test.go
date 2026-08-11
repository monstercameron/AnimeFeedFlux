package rpc

import (
	"path/filepath"
	"sync"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- test fixtures ----------------------------------------------------

func feedOpenTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// fakeFeedInvalidator records every call, so tests can assert both the count
// and the slugs invalidated without a real publish.Cache.
type fakeFeedInvalidator struct {
	mu    sync.Mutex
	feeds []string
	all   int
}

func (f *fakeFeedInvalidator) InvalidateFeed(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds = append(f.feeds, slug)
}

func (f *fakeFeedInvalidator) InvalidateAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all++
}

func (f *fakeFeedInvalidator) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.feeds)
}

// fakeFeedExecutor records RunNow's hand-off instead of actually generating
// anything — this package must not depend on internal/scheduler or
// internal/generate to test RunNow's single-flight behavior.
type fakeFeedExecutor struct {
	mu    sync.Mutex
	calls []struct{ feedID, runID int64 }
}

func (f *fakeFeedExecutor) ExecuteRun(feedID, runID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct{ feedID, runID int64 }{feedID, runID})
}

// feedTestSpec returns a minimal, valid FeedSpec for a generative feed —
// enough to pass feedspec.Validate without exercising template edge cases,
// which internal/generate/prompt_test.go already covers.
func feedTestSpec() *affv1.FeedSpec {
	return &affv1.FeedSpec{
		Cron:        "0 12 * * *",
		Timezone:    "UTC",
		ItemsPerRun: 1,
		FeedWindow:  50,
		Model:       "gpt-test",
		Novelty: &affv1.NoveltySettings{
			NoveltyWindowItems:  50,
			SimilarityThreshold: 0.9,
		},
		DailyTokenBudget: 10_000,
		DailyRunBudget:   10,
	}
}

func feedTestFeed(slug string) *affv1.Feed {
	return &affv1.Feed{
		Slug:  slug,
		Kind:  affv1.FeedKind_FEED_KIND_GENERATIVE,
		Title: "Test Feed " + slug,
		Spec:  feedTestSpec(),
	}
}

// feedTestAggregate builds a well-formed aggregate feed. feedspec.Validate
// applies its schedule/budget/range checks to every kind, including
// aggregate (validate.go has no kind-specific exemption for them, even
// though PLAN.md §14.2 says aggregates "never spend") — so an aggregate spec
// still needs a parseable cron and non-zero budgets/ranges to pass, exactly
// like a generative one. Only prompts and sources are actually forbidden for
// aggregates (feedTestSpec sets neither).
func feedTestAggregate(slug, title string, members []string) *affv1.Feed {
	return &affv1.Feed{
		Slug:        slug,
		Kind:        affv1.FeedKind_FEED_KIND_AGGREGATE,
		Title:       title,
		Spec:        feedTestSpec(),
		MemberSlugs: members,
	}
}

func mustCreateFeed(t *testing.T, s *FeedServer, feed *affv1.Feed) *affv1.Feed {
	t.Helper()
	resp, err := s.Create(t.Context(), &affv1.FeedServiceCreateRequest{Feed: feed})
	if err != nil {
		t.Fatalf("Create(%q): %v", feed.GetSlug(), err)
	}
	return resp.GetFeed()
}

// --- version mismatch ------------------------------------------------------

func TestFeedUpdateVersionMismatchRejected(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	created := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))

	in := created
	in.Title = "Renamed"
	_, err := s.Update(t.Context(), &affv1.FeedServiceUpdateRequest{
		Feed:            in,
		ExpectedVersion: created.GetVersion() + 1, // deliberately wrong
	})
	if err == nil {
		t.Fatal("Update with a stale expected_version succeeded, want a version-conflict error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition (distinguishable from other failures per PLAN.md §11)", status.Code(err))
	}

	// The title must not have changed.
	got, err := s.Get(t.Context(), &affv1.FeedServiceGetRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetFeed().GetTitle() == "Renamed" {
		t.Error("update applied despite version conflict")
	}
}

// --- invalidator called on every mutating path ------------------------

func TestFeedInvalidatorCalledOnMutations(t *testing.T) {
	st := feedOpenTestStore(t)
	inv := &fakeFeedInvalidator{}
	s := NewFeedServer(st, inv, nil)

	// Create.
	created := mustCreateFeed(t, s, feedTestFeed("news-roundup"))
	if got := inv.count(); got != 1 {
		t.Fatalf("after Create: invalidator called %d times, want 1", got)
	}

	// Update.
	upd := created
	upd.Description = "updated description"
	updResp, err := s.Update(t.Context(), &affv1.FeedServiceUpdateRequest{
		Feed: upd, ExpectedVersion: created.GetVersion(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := inv.count(); got != 2 {
		t.Fatalf("after Update: invalidator called %d times, want 2", got)
	}

	// SetEnabled.
	seResp, err := s.SetEnabled(t.Context(), &affv1.FeedServiceSetEnabledRequest{
		FeedId: created.GetId(), Enabled: true, ExpectedVersion: updResp.GetFeed().GetVersion(),
	})
	if err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if got := inv.count(); got != 3 {
		t.Fatalf("after SetEnabled: invalidator called %d times, want 3", got)
	}

	// SetMembers, on a separate aggregate feed containing the one above —
	// exercises both the aggregate's own invalidation and confirms the
	// fan-out path does not panic when there happen to be no containing
	// aggregates of ITS OWN.
	agg := feedTestAggregate("everything", "Everything", []string{"news-roundup"})
	aggCreated := mustCreateFeed(t, s, agg)
	if got := inv.count(); got != 4 {
		t.Fatalf("after aggregate Create: invalidator called %d times, want 4", got)
	}

	_, err = s.SetMembers(t.Context(), &affv1.FeedServiceSetMembersRequest{
		AggregateFeedId: aggCreated.GetId(),
		MemberSlugs:     []string{"news-roundup"},
		ExpectedVersion: aggCreated.GetVersion(),
	})
	if err != nil {
		t.Fatalf("SetMembers: %v", err)
	}
	if got := inv.count(); got != 5 {
		t.Fatalf("after SetMembers: invalidator called %d times, want 5", got)
	}

	// Delete. news-roundup is a member of the "everything" aggregate created
	// above, so this also fans out to that aggregate (§11) — one call for
	// the feed itself, one for its containing aggregate.
	_, err = s.Delete(t.Context(), &affv1.FeedServiceDeleteRequest{
		FeedId: created.GetId(), ExpectedVersion: seResp.GetFeed().GetVersion(),
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := inv.count(); got != 7 {
		t.Fatalf("after Delete: invalidator called %d times, want 7 (feed + containing aggregate)", got)
	}
}

// TestFeedInvalidatorFansOutToAggregate confirms that mutating a MEMBER feed
// also invalidates the aggregate containing it (§11: "Any aggregate
// containing the feed is invalidated too").
func TestFeedInvalidatorFansOutToAggregate(t *testing.T) {
	st := feedOpenTestStore(t)
	inv := &fakeFeedInvalidator{}
	s := NewFeedServer(st, inv, nil)

	member := mustCreateFeed(t, s, feedTestFeed("member-feed"))
	agg := mustCreateFeed(t, s, feedTestAggregate("aggregate-feed", "Aggregate", []string{"member-feed"}))
	_ = agg

	before := inv.count()
	upd := member
	upd.Description = "changed"
	_, err := s.Update(t.Context(), &affv1.FeedServiceUpdateRequest{
		Feed: upd, ExpectedVersion: member.GetVersion(),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// One call for the member itself, one for the containing aggregate.
	if got := inv.count() - before; got != 2 {
		t.Fatalf("Update on a member feed invalidated %d slugs, want 2 (member + containing aggregate)", got)
	}
}

// --- slug immutability -------------------------------------------------

func TestFeedSlugChangeAfterPublishRefused(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	created := mustCreateFeed(t, s, feedTestFeed("season-news"))

	// Publish an item directly against the store — this RPC's own item write
	// path (ItemService) belongs to another file, so this test seeds the
	// item the same way store_test.go's seedFeed/insertItem helpers do.
	_, err := st.Writer().ExecContext(t.Context(), `
		INSERT INTO items (feed_id, item_key, content_hash, title, summary_text, body_html,
		                    published_at, origin, created_at, updated_at)
		VALUES (?, 'item-key-1', 'hash-1', 'Title', 'summary', '<p>body</p>',
		        '2026-08-10T00:00:00Z', 'generated', '2026-08-10T00:00:00Z', '2026-08-10T00:00:00Z')`,
		created.GetId())
	if err != nil {
		t.Fatalf("seeding item: %v", err)
	}

	renamed := created
	renamed.Slug = "season-news-renamed"
	_, err = s.Update(t.Context(), &affv1.FeedServiceUpdateRequest{
		Feed: renamed, ExpectedVersion: created.GetVersion(),
	})
	if err == nil {
		t.Fatal("Update renamed a feed's slug after it had published an item, want refusal")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", status.Code(err))
	}
}

// --- reserved slug -------------------------------------------------------

func TestFeedReservedSlugRefused(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	_, err := s.Create(t.Context(), &affv1.FeedServiceCreateRequest{Feed: feedTestFeed("feeds")})
	if err == nil {
		t.Fatal("Create with a reserved slug succeeded, want refusal")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

// --- aggregate cannot nest -------------------------------------------------

func TestFeedAggregateInAggregateRefused(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	// leaf-feed first: an aggregate must have >=1 member from creation (§7,
	// ReasonAggregateRequiresMember), so inner-aggregate is created already
	// well-formed rather than empty-then-fixed-up — isolating the assertion
	// below to "aggregate cannot be a member", not "empty aggregate".
	member := mustCreateFeed(t, s, feedTestFeed("leaf-feed"))
	_ = member

	inner := mustCreateFeed(t, s, feedTestAggregate("inner-aggregate", "Inner", []string{"leaf-feed"}))
	_ = inner

	outer := mustCreateFeed(t, s, feedTestAggregate("outer-aggregate", "Outer", []string{"leaf-feed"}))

	_, err := s.SetMembers(t.Context(), &affv1.FeedServiceSetMembersRequest{
		AggregateFeedId: outer.GetId(),
		MemberSlugs:     []string{"inner-aggregate"},
		ExpectedVersion: outer.GetVersion(),
	})
	if err == nil {
		t.Fatal("SetMembers accepted an aggregate as a member, want refusal (PLAN.md §14.2)")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

// --- ValidateSpec field errors -----------------------------------------

func TestFeedValidateSpecFieldErrors(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	spec := feedTestSpec()
	spec.Cron = "not a cron expression"

	resp, err := s.ValidateSpec(t.Context(), &affv1.FeedServiceValidateSpecRequest{
		Kind: affv1.FeedKind_FEED_KIND_GENERATIVE,
		Slug: "bad-cron-feed",
		Spec: spec,
	})
	if err != nil {
		t.Fatalf("ValidateSpec: %v", err)
	}
	if resp.GetValid() {
		t.Fatal("ValidateSpec reported valid for a bad cron expression")
	}
	found := false
	for _, fe := range resp.GetErrors() {
		if fe.GetField() == "cron" {
			found = true
		}
	}
	if !found {
		t.Errorf("ValidateSpec errors = %v, want an error scoped to field %q", resp.GetErrors(), "cron")
	}
}

// --- pagination cursor round-trip ---------------------------------------

func TestFeedListPaginationCursorRoundTrips(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	const total = 5
	want := map[string]bool{}
	for i := 0; i < total; i++ {
		slug := "page-feed-" + string(rune('a'+i))
		mustCreateFeed(t, s, feedTestFeed(slug))
		want[slug] = true
	}

	got := map[string]bool{}
	token := ""
	pages := 0
	for {
		resp, err := s.List(t.Context(), &affv1.FeedServiceListRequest{PageSize: 2, PageToken: token})
		if err != nil {
			t.Fatalf("List (page %d): %v", pages, err)
		}
		pages++
		for _, f := range resp.GetFeeds() {
			if got[f.GetSlug()] {
				t.Fatalf("slug %q returned twice across pages", f.GetSlug())
			}
			got[f.GetSlug()] = true
		}
		if resp.GetNextPageToken() == "" {
			break
		}
		token = resp.GetNextPageToken()
		if pages > total+2 {
			t.Fatal("cursor never terminated — pagination looping")
		}
	}

	if len(got) != total {
		t.Fatalf("collected %d feeds across %d pages, want %d", len(got), pages, total)
	}
	for slug := range want {
		if !got[slug] {
			t.Errorf("slug %q never returned by List pagination", slug)
		}
	}
	if pages < total/2 {
		t.Errorf("got %d pages for page_size=2 and %d feeds, expected pagination to actually page", pages, total)
	}
}

// --- RunNow single-flight -------------------------------------------------

func TestFeedRunNowWhileRunningRefused(t *testing.T) {
	st := feedOpenTestStore(t)
	exec := &fakeFeedExecutor{}
	s := NewFeedServer(st, nil, exec)

	created := mustCreateFeed(t, s, feedTestFeed("run-now-feed"))

	first, err := s.RunNow(t.Context(), &affv1.FeedServiceRunNowRequest{FeedId: created.GetId()})
	if err != nil {
		t.Fatalf("first RunNow: %v", err)
	}
	if first.GetRunId() == 0 {
		t.Error("first RunNow returned a zero run_id")
	}

	_, err = s.RunNow(t.Context(), &affv1.FeedServiceRunNowRequest{FeedId: created.GetId()})
	if err == nil {
		t.Fatal("second RunNow while the first is still running succeeded, want refusal")
	}
	if status.Code(err) != codes.FailedPrecondition && status.Code(err) != codes.AlreadyExists {
		t.Errorf("code = %v, want FailedPrecondition or AlreadyExists", status.Code(err))
	}

	exec.mu.Lock()
	calls := len(exec.calls)
	exec.mu.Unlock()
	if calls != 1 {
		t.Errorf("executor called %d times, want exactly 1 (single-flight)", calls)
	}
}

// TestFeedDeleteReleasesSlugForReuse pins the behaviour that a deleted feed's
// name goes back into circulation.
//
// Deletion is soft and `slug` is UNIQUE across every row, deleted or not, so
// without the tombstone rename a deleted feed holds its name forever: the
// operator deletes "trivia-daily", tries to create it again, and is told the
// slug already exists — about a feed that is not in any list, cannot be
// restored (there is no feed Restore RPC), and that they just deliberately
// removed. This test exists because that is a silent, permanent trap and the
// fix for it lives in one easily-lost line of the delete statement.
func TestFeedDeleteReleasesSlugForReuse(t *testing.T) {
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	created := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))
	if _, err := s.Delete(t.Context(), &affv1.FeedServiceDeleteRequest{
		FeedId:          created.GetId(),
		ExpectedVersion: created.GetVersion(),
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// The same slug must be usable again.
	again, err := s.Create(t.Context(), &affv1.FeedServiceCreateRequest{Feed: feedTestFeed("trivia-daily")})
	if err != nil {
		t.Fatalf("re-creating a deleted feed's slug: %v", err)
	}
	if again.GetFeed().GetId() == created.GetId() {
		t.Error("re-create returned the tombstoned row; it must be a new feed")
	}
	if got := again.GetFeed().GetSlug(); got != "trivia-daily" {
		t.Errorf("slug = %q, want %q", got, "trivia-daily")
	}

	// And the tombstone must still be there, under a suffixed name, so the
	// delete stays auditable rather than becoming a hard delete by stealth.
	var tombstones int
	if err := st.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM feeds WHERE id = ? AND deleted_at IS NOT NULL AND slug LIKE 'trivia-daily-deleted-%'`,
		created.GetId()).Scan(&tombstones); err != nil {
		t.Fatalf("counting tombstones: %v", err)
	}
	if tombstones != 1 {
		t.Errorf("tombstoned row count = %d, want 1 (the original row, renamed)", tombstones)
	}
}
