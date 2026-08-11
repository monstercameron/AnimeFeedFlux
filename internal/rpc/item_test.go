package rpc

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// itemFakeInvalidator counts InvalidateFeed calls so tests can assert every
// mutating RPC fires the injected publish.Invalidator exactly once, after
// commit (PLAN.md §11).
type itemFakeInvalidator struct {
	mu    sync.Mutex
	calls int
	slugs []string
}

func (f *itemFakeInvalidator) InvalidateFeed(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.slugs = append(f.slugs, slug)
}

func (f *itemFakeInvalidator) InvalidateAll() {}

func (f *itemFakeInvalidator) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newItemTestServer(t *testing.T) (*ItemServer, *store.Store, *itemFakeInvalidator) {
	t.Helper()
	ctx := t.Context()
	st, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	inv := &itemFakeInvalidator{}
	srv := NewItemServer(st, inv, ids.NewDeterministicSource(1))
	return srv, st, inv
}

func itemMustCreateFeed(t *testing.T, st *store.Store, slug string) int64 {
	t.Helper()
	id, err := st.CreateFeed(t.Context(), model.Feed{
		Slug: slug, Title: "Test " + slug, Kind: model.KindGenerative, Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create feed %q: %v", slug, err)
	}
	return id
}

// TestItemUpdate_PreservesItemKey proves the guid the RSS/Atom output derives
// from (item_key) is byte-identical before and after an edit, even when the
// caller tries to supply a different one (PLAN.md §5.1, §12.4).
func TestItemUpdate_PreservesItemKey(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-key")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original title", SummaryText: "sum", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalKey := created.GetItem().GetItemKey()
	if originalKey == "" {
		t.Fatal("expected item_key to be assigned on create")
	}

	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id:          created.GetItem().GetId(),
			ItemKey:     "attacker-supplied-key-must-be-ignored",
			Title:       "Updated title",
			SummaryText: "sum2",
			BodyHtml:    "<p>b2</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, want := updated.GetItem().GetItemKey(), originalKey; got != want {
		t.Fatalf("item_key changed on edit: got %q, want %q (byte-identical, PLAN.md §5.1)", got, want)
	}

	// Re-read from storage to rule out the response echoing a stale value.
	reread, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := reread.GetItem().GetItemKey(); got != originalKey {
		t.Fatalf("stored item_key changed on edit: got %q, want %q", got, originalKey)
	}
	if got := reread.GetItem().GetTitle(); got != "Updated title" {
		t.Fatalf("update did not persist: title = %q", got)
	}
}

// TestItemCreate_RejectsBackdatedPublishedAt proves RULE-7 (PLAN.md §5.5):
// Slack's bookmark model makes a published_at at or before the feed's
// current newest item invisible forever, so Create refuses it.
func TestItemCreate_RejectsBackdatedPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-backdate-create")

	newest := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "First", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(newest),
	}}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"equal to newest", newest},
		{"before newest", newest.Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
				FeedId: feedID, Title: "Backdated", SummaryText: "s", BodyHtml: "<p>b</p>",
				PublishedAt: timestamppb.New(tc.at),
			}})
			if err == nil {
				t.Fatal("expected backdated published_at to be refused")
			}
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v (%v)", got, err)
			}
		})
	}
}

// TestItemUpdate_RejectsBackdatedPublishedAt is the same rule, exercised on
// Update: moving an item's published_at back to or before another item's
// stamp must be refused, while re-saving an item's OWN unchanged timestamp
// must not be (it would otherwise always compare against itself).
func TestItemUpdate_RejectsBackdatedPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-backdate-update")

	first, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "First", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	}})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Second", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
	}})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	// Moving "first" to at-or-before "second" must be refused.
	_, err = srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: first.GetItem().GetId(), Title: "First", SummaryText: "s", BodyHtml: "<p>b</p>",
			PublishedAt: second.GetItem().GetPublishedAt(),
		},
		ExpectedVersion: first.GetItem().GetVersion(),
	})
	if err == nil {
		t.Fatal("expected backdated published_at to be refused on update")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v (%v)", got, err)
	}

	// Re-saving "second" with its OWN unchanged published_at must succeed —
	// it is not "backdated relative to itself".
	_, err = srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: second.GetItem().GetId(), Title: "Second edited", SummaryText: "s", BodyHtml: "<p>b</p>",
			PublishedAt: second.GetItem().GetPublishedAt(),
		},
		ExpectedVersion: second.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("expected no-op published_at re-save to succeed, got: %v", err)
	}
}

// TestItemPublishCorrection_CreatesNewItemLeavesOriginalUntouched proves
// PublishCorrection creates a NEW item, does not modify the original's guid
// or published_at, and links the two via `corrections` (PLAN.md §12.4).
func TestItemPublishCorrection_CreatesNewItemLeavesOriginalUntouched(t *testing.T) {
	srv, st, inv := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-correction")

	original, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Wrong fact", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	origItem := original.GetItem()

	before := inv.count()
	corr, err := srv.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: origItem.GetId(),
		Title:          "Actually correct fact",
		SummaryText:    "corrected",
		BodyHtml:       "<p>corrected</p>",
	})
	if err != nil {
		t.Fatalf("publish correction: %v", err)
	}
	if got := inv.count(); got != before+1 {
		t.Fatalf("expected invalidator to fire once for the correction, cumulative went %d -> %d", before, got)
	}

	if corr.GetItem().GetId() == origItem.GetId() {
		t.Fatal("correction must be a new item, not the original")
	}
	if corr.GetItem().GetOrigin() != affv1.Origin_ORIGIN_CORRECTION {
		t.Fatalf("correction origin = %v, want ORIGIN_CORRECTION", corr.GetItem().GetOrigin())
	}
	if !corr.GetItem().GetPublishedAt().AsTime().After(origItem.GetPublishedAt().AsTime()) {
		t.Fatal("correction must be published strictly after the original")
	}

	// The original, re-read from storage, must be untouched: same guid AND
	// the same published_at.
	reread, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: origItem.GetId()})
	if err != nil {
		t.Fatalf("get original: %v", err)
	}
	if got := reread.GetItem().GetItemKey(); got != origItem.GetItemKey() {
		t.Fatalf("original item_key changed: got %q, want %q", got, origItem.GetItemKey())
	}
	if !reread.GetItem().GetPublishedAt().AsTime().Equal(origItem.GetPublishedAt().AsTime()) {
		t.Fatal("original published_at changed")
	}
	if reread.GetItem().GetTitle() != origItem.GetTitle() {
		t.Fatal("original title changed")
	}

	// corrections row links them.
	var correctsID int64
	row := st.Writer().QueryRowContext(ctx, `SELECT corrects_item_id FROM corrections WHERE item_id = ?`, corr.GetItem().GetId())
	if err := row.Scan(&correctsID); err != nil {
		t.Fatalf("reading corrections row: %v", err)
	}
	if correctsID != origItem.GetId() {
		t.Fatalf("corrections row links to item %d, want %d", correctsID, origItem.GetId())
	}
}

// TestItemDeleteThenRestore proves the soft-delete/restore round trip: the
// item is tombstoned (deleted_at set) then un-tombstoned (deleted_at
// cleared), with no data loss in between (PLAN.md §12.4).
func TestItemDeleteThenRestore(t *testing.T) {
	srv, st, inv := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-delete-restore")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.GetItem().GetId()

	before := inv.count()
	if _, err := srv.Delete(ctx, &affv1.ItemServiceDeleteRequest{
		ItemId: id, ExpectedVersion: created.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := inv.count(); got != before+1 {
		t.Fatalf("expected invalidator to fire once for delete, cumulative went %d -> %d", before, got)
	}

	deleted, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: id})
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if deleted.GetItem().GetDeletedAt() == nil {
		t.Fatal("expected deleted_at to be set after delete")
	}
	if deleted.GetItem().GetTitle() != "T" {
		t.Fatal("soft delete must not touch item content")
	}

	before = inv.count()
	restored, err := srv.Restore(ctx, &affv1.ItemServiceRestoreRequest{
		ItemId: id, ExpectedVersion: deleted.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := inv.count(); got != before+1 {
		t.Fatalf("expected invalidator to fire once for restore, cumulative went %d -> %d", before, got)
	}
	if restored.GetItem().GetDeletedAt() != nil {
		t.Fatal("expected deleted_at to be cleared after restore")
	}
	if restored.GetItem().GetItemKey() != created.GetItem().GetItemKey() {
		t.Fatal("restore must not change item_key")
	}
}

// TestItemUpdate_VersionMismatch proves optimistic concurrency: an Update
// carrying a stale expected_version is refused with FailedPrecondition, not
// silently applied over another writer's change (PLAN.md §11).
func TestItemUpdate_VersionMismatch(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-version")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T2", SummaryText: "s2", BodyHtml: "<p>b2</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion() + 99,
	})
	if err == nil {
		t.Fatal("expected a version-conflict error")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v (%v)", got, err)
	}
}

// TestItemDelete_VersionMismatch is the same optimistic-concurrency check on
// the Delete path, which has its own hand-written WHERE id=? AND version=?
// (store/items.go's SoftDeleteItem has no version parameter at all, so this
// file cannot simply delegate to it — see the item.go file header).
func TestItemDelete_VersionMismatch(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-delete-version")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = srv.Delete(ctx, &affv1.ItemServiceDeleteRequest{
		ItemId: created.GetItem().GetId(), ExpectedVersion: created.GetItem().GetVersion() + 99,
	})
	if err == nil {
		t.Fatal("expected a version-conflict error")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v (%v)", got, err)
	}
}

// TestItemInvalidator_CalledOnEveryMutatingPath drives every mutating method
// once and asserts the injected Invalidator's call count increases by
// exactly one each time (PLAN.md §11: "every mutation ... invalidates that
// feed's render cache").
func TestItemInvalidator_CalledOnEveryMutatingPath(t *testing.T) {
	srv, st, inv := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-invalidate-all")

	calls := 0
	expect := func(t *testing.T, label string) {
		t.Helper()
		calls++
		if got := inv.count(); got != calls {
			t.Fatalf("%s: expected %d cumulative invalidator calls, got %d", label, calls, got)
		}
	}

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	expect(t, "create")

	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T2", SummaryText: "s2", BodyHtml: "<p>b2</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	expect(t, "update")

	if _, err := srv.Delete(ctx, &affv1.ItemServiceDeleteRequest{
		ItemId: updated.GetItem().GetId(), ExpectedVersion: updated.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	expect(t, "delete")

	deleted, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: updated.GetItem().GetId()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if _, err := srv.Restore(ctx, &affv1.ItemServiceRestoreRequest{
		ItemId: updated.GetItem().GetId(), ExpectedVersion: deleted.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	expect(t, "restore")

	if _, err := srv.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: created.GetItem().GetId(), Title: "Fix", SummaryText: "fixed", BodyHtml: "<p>fixed</p>",
	}); err != nil {
		t.Fatalf("publish correction: %v", err)
	}
	expect(t, "publish correction")

	// Field names are the generated struct's encoding/json tags, NOT protojson
	// aliases: sample.go writes this payload with encoding/json over
	// []*affv1.SampleCandidate, so the wire name is `candidate_id`. This
	// fixture asserted `candidateId` and passed for as long as the decoder
	// carried the same mistake — a hand-written fixture mirroring a
	// hand-written decoder agreed with each other and with nothing else.
	payload := `[{"candidate_id":"c1","title":"Promoted","summary_text":"promoted summary","body_html":"<p>promoted</p>"}]`
	sampleID, err := st.PutSample(ctx, feedID, []byte(payload), 10, 20, 0.01, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}
	if _, err := srv.PromoteSample(ctx, &affv1.ItemServicePromoteSampleRequest{
		SampleId: strconv.FormatInt(sampleID, 10), CandidateId: "c1",
	}); err != nil {
		t.Fatalf("promote sample: %v", err)
	}
	expect(t, "promote sample")

	// List and Get are reads and must never invalidate anything.
	if _, err := srv.List(ctx, &affv1.ItemServiceListRequest{FeedId: feedID}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := inv.count(); got != calls {
		t.Fatalf("List must not invalidate: cumulative calls went %d -> %d", calls, got)
	}
}

// TestItemService_NoPurgeDeletedMethod asserts by reflection that neither
// the generated ItemServiceServer interface nor this file's implementation
// has a PurgeDeleted method. PurgeDeleted was specified and then deliberately
// cut (PLAN.md §12.4): the permalink must 410 forever and the guid is never
// freed, and purging would leave nothing to 410 on. This test exists so a
// future proto regeneration that reintroduces it fails loudly here instead of
// silently reopening that hole.
func TestItemService_NoPurgeDeletedMethod(t *testing.T) {
	iface := reflect.TypeOf((*affv1.ItemServiceServer)(nil)).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		if iface.Method(i).Name == "PurgeDeleted" {
			t.Fatal("affv1.ItemServiceServer must not define PurgeDeleted (PLAN.md §12.4)")
		}
	}

	implType := reflect.TypeOf(&ItemServer{})
	for i := 0; i < implType.NumMethod(); i++ {
		if implType.Method(i).Name == "PurgeDeleted" {
			t.Fatal("*ItemServer must not implement PurgeDeleted (PLAN.md §12.4)")
		}
	}
}

// TestItemListRevisions_EmptyForItemWithNoEdits proves an item that has
// never been edited returns an empty list, not an error (PLAN.md §12.4).
func TestItemListRevisions_EmptyForItemWithNoEdits(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-no-revisions")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions on a never-edited item must not error: %v", err)
	}
	if len(resp.GetRevisions()) != 0 {
		t.Fatalf("expected 0 revisions, got %d", len(resp.GetRevisions()))
	}
	if resp.GetNextPageToken() != "" {
		t.Fatalf("expected no next_page_token, got %q", resp.GetNextPageToken())
	}
}

// TestItemListRevisions_NewestFirstWithPagination proves ListRevisions
// groups the per-field rows one entry per edit, orders them newest first,
// and paginates without splitting a group across pages (PLAN.md §12.4).
func TestItemListRevisions_NewestFirstWithPagination(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revisions-page")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "v0", SummaryText: "s0", BodyHtml: "<p>b0</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.GetItem().GetId()
	version := created.GetItem().GetVersion()

	// item_revisions groups rows sharing `at`, and `at` comes from srv.now()
	// at RFC3339Nano precision — back-to-back Updates in a tight test loop
	// can land on the same wall-clock instant on a coarse system clock and
	// wrongly merge into one group. An injected, strictly increasing clock
	// (the same pattern auth_test.go/interceptor_test.go use) makes each
	// edit's `at` distinct by construction rather than by hoping the clock
	// is fine-grained enough.
	clock := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	srv.now = func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}

	// Three edits, each touching both title and summary_text — two
	// item_revisions rows per edit, three edits total.
	const edits = 3
	for i := 1; i <= edits; i++ {
		updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
			Item: &affv1.Item{
				Id: id, Title: fmt.Sprintf("v%d", i), SummaryText: fmt.Sprintf("s%d", i), BodyHtml: "<p>b0</p>",
			},
			ExpectedVersion: version,
		})
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
		version = updated.GetItem().GetVersion()
	}

	// Page 1: size 2 of 3 edits.
	page1, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: id, PageSize: 2})
	if err != nil {
		t.Fatalf("list revisions page 1: %v", err)
	}
	if len(page1.GetRevisions()) != 2 {
		t.Fatalf("page 1: got %d revisions, want 2", len(page1.GetRevisions()))
	}
	if page1.GetNextPageToken() == "" {
		t.Fatal("page 1: expected a next_page_token, edits remain")
	}
	// Newest first: the most recent edit (title "v3") must come first.
	if got := page1.GetRevisions()[0].GetChanges(); !itemRevisionChangesContain(got, "title", "v2", "v3") {
		t.Fatalf("page 1[0] changes = %+v, want a title v2->v3 change", got)
	}
	if got := page1.GetRevisions()[1].GetChanges(); !itemRevisionChangesContain(got, "title", "v1", "v2") {
		t.Fatalf("page 1[1] changes = %+v, want a title v1->v2 change", got)
	}
	// Each revision bundles both fields changed in that edit — no second
	// call needed to render its diff.
	if got := len(page1.GetRevisions()[0].GetChanges()); got != 2 {
		t.Fatalf("page 1[0]: got %d field changes, want 2 (title + summary_text)", got)
	}

	// Page 2: the remaining oldest edit.
	page2, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{
		ItemId: id, PageSize: 2, PageToken: page1.GetNextPageToken(),
	})
	if err != nil {
		t.Fatalf("list revisions page 2: %v", err)
	}
	if len(page2.GetRevisions()) != 1 {
		t.Fatalf("page 2: got %d revisions, want 1", len(page2.GetRevisions()))
	}
	if page2.GetNextPageToken() != "" {
		t.Fatalf("page 2: expected no next_page_token, got %q", page2.GetNextPageToken())
	}
	if got := page2.GetRevisions()[0].GetChanges(); !itemRevisionChangesContain(got, "title", "v0", "v1") {
		t.Fatalf("page 2[0] changes = %+v, want a title v0->v1 change", got)
	}

	// Every returned revision id must be unique — proves grouping did not
	// collapse distinct edits together.
	seen := map[int64]bool{}
	for _, rev := range append(append([]*affv1.ItemRevision{}, page1.GetRevisions()...), page2.GetRevisions()...) {
		if seen[rev.GetId()] {
			t.Fatalf("duplicate revision id %d across pages", rev.GetId())
		}
		seen[rev.GetId()] = true
	}
}

func itemRevisionChangesContain(changes []*affv1.ItemRevisionChange, field, oldValue, newValue string) bool {
	for _, c := range changes {
		if c.GetField() == field && c.GetOldValue() == oldValue && c.GetNewValue() == newValue {
			return true
		}
	}
	return false
}

// TestItemRevertRevision_CreatesNewRevisionNeverDeletesOld proves revert is
// an ordinary edit: it produces a NEW item_revisions entry and leaves every
// prior revision row in place (PLAN.md §12.4 — history that could be erased
// by the thing it audits would not be history).
func TestItemRevertRevision_CreatesNewRevisionNeverDeletesOld(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revert-history")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original", SummaryText: "orig", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "Edited wrong", SummaryText: "wrong", BodyHtml: "<p>b</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	before, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions before revert: %v", err)
	}
	if len(before.GetRevisions()) != 1 {
		t.Fatalf("before revert: got %d revisions, want 1", len(before.GetRevisions()))
	}
	editRevisionID := before.GetRevisions()[0].GetId()

	reverted, err := srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: created.GetItem().GetId(), RevisionId: editRevisionID, ExpectedVersion: updated.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := reverted.GetItem().GetTitle(); got != "Original" {
		t.Fatalf("reverted title = %q, want %q", got, "Original")
	}
	if got := reverted.GetItem().GetSummaryText(); got != "orig" {
		t.Fatalf("reverted summary_text = %q, want %q", got, "orig")
	}

	after, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions after revert: %v", err)
	}
	if len(after.GetRevisions()) != 2 {
		t.Fatalf("after revert: got %d revisions, want 2 (original edit + the revert as a new edit)", len(after.GetRevisions()))
	}
	// The original edit's row must still be present, byte-identical.
	var stillPresent bool
	for _, rev := range after.GetRevisions() {
		if rev.GetId() == editRevisionID {
			stillPresent = true
			if !itemRevisionChangesContain(rev.GetChanges(), "title", "Original", "Edited wrong") {
				t.Fatalf("original revision %d's changes were mutated: %+v", editRevisionID, rev.GetChanges())
			}
		}
	}
	if !stillPresent {
		t.Fatalf("original revision %d was deleted by revert — history must never be erased", editRevisionID)
	}
	// The newest revision is the revert itself, recorded as an ordinary
	// forward edit (Edited wrong -> Original), not a rewind.
	newest := after.GetRevisions()[0]
	if newest.GetId() == editRevisionID {
		t.Fatal("revert did not write a new revision row")
	}
	if !itemRevisionChangesContain(newest.GetChanges(), "title", "Edited wrong", "Original") {
		t.Fatalf("revert's own revision changes = %+v, want a title Edited wrong->Original change", newest.GetChanges())
	}
}

// TestItemRevertRevision_PreservesItemKey is the specific guard the task
// calls out by name: "revert restores the old state" is exactly the
// reasoning that would wrongly restore an old guid too, so this is tested
// on the revert path directly rather than assumed from the Update test
// (PLAN.md §5.1).
func TestItemRevertRevision_PreservesItemKey(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revert-guid")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original", SummaryText: "orig", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	originalKey := created.GetItem().GetItemKey()
	if originalKey == "" {
		t.Fatal("expected item_key to be assigned on create")
	}

	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "Edited", SummaryText: "edited", BodyHtml: "<p>b</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	revisions, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	revisionID := revisions.GetRevisions()[0].GetId()

	reverted, err := srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: created.GetItem().GetId(), RevisionId: revisionID, ExpectedVersion: updated.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := reverted.GetItem().GetItemKey(); got != originalKey {
		t.Fatalf("item_key changed on revert: got %q, want %q (byte-identical, PLAN.md §5.1)", got, originalKey)
	}

	// Re-read from storage to rule out the response echoing a stale value.
	reread, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := reread.GetItem().GetItemKey(); got != originalKey {
		t.Fatalf("stored item_key changed on revert: got %q, want %q", got, originalKey)
	}
}

// TestItemRevertRevision_VersionMismatch proves a revert racing a concurrent
// edit conflicts loudly rather than silently winning (PLAN.md §11).
func TestItemRevertRevision_VersionMismatch(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revert-version")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original", SummaryText: "orig", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "Edited", SummaryText: "edited", BodyHtml: "<p>b</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	revisions, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	revisionID := revisions.GetRevisions()[0].GetId()

	// A second, concurrent edit bumps the version out from under the revert.
	if _, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "Raced ahead", SummaryText: "raced", BodyHtml: "<p>b</p>",
		},
		ExpectedVersion: updated.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("racing update: %v", err)
	}

	_, err = srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: created.GetItem().GetId(), RevisionId: revisionID, ExpectedVersion: updated.GetItem().GetVersion(), // now stale
	})
	if err == nil {
		t.Fatal("expected a version-conflict error on a revert racing a concurrent edit")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v (%v)", got, err)
	}

	// The racing edit's content must be untouched by the failed revert.
	reread, err := srv.Get(ctx, &affv1.ItemServiceGetRequest{Id: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := reread.GetItem().GetTitle(); got != "Raced ahead" {
		t.Fatalf("title = %q after failed revert, want the racing edit's %q untouched", got, "Raced ahead")
	}
}

// TestItemRevertRevision_InvalidatesCache proves RULE-6: a revert changes
// published output while looking like internal bookkeeping over
// item_revisions, so it must invalidate the feed's render cache exactly like
// any other write that changes rendered output (PLAN.md §11).
func TestItemRevertRevision_InvalidatesCache(t *testing.T) {
	srv, st, inv := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revert-invalidate")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original", SummaryText: "orig", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "Edited", SummaryText: "edited", BodyHtml: "<p>b</p>",
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	revisions, err := srv.ListRevisions(ctx, &affv1.ItemServiceListRevisionsRequest{ItemId: created.GetItem().GetId()})
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	revisionID := revisions.GetRevisions()[0].GetId()

	before := inv.count()
	if _, err := srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: created.GetItem().GetId(), RevisionId: revisionID, ExpectedVersion: updated.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := inv.count(); got != before+1 {
		t.Fatalf("expected invalidator to fire exactly once for revert, cumulative went %d -> %d", before, got)
	}
	if got := inv.slugs[len(inv.slugs)-1]; got != "feed-revert-invalidate" {
		t.Fatalf("invalidated slug = %q, want %q", got, "feed-revert-invalidate")
	}
}

// TestItemRevertRevision_UnknownItemOrRevision proves both "item does not
// exist" and "revision does not exist on this item" are refused with
// NotFound rather than panicking or silently no-op'ing.
func TestItemRevertRevision_UnknownItemOrRevision(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-revert-notfound")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Original", SummaryText: "orig", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: 999999, RevisionId: 1, ExpectedVersion: 1,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown item_id: got %v, want NotFound", err)
	}

	if _, err := srv.RevertRevision(ctx, &affv1.ItemServiceRevertRevisionRequest{
		ItemId: created.GetItem().GetId(), RevisionId: 999999, ExpectedVersion: created.GetItem().GetVersion(),
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown revision_id: got %v, want NotFound", err)
	}
}

// --- published_at range/zero validation -------------------------------
//
// Fuzzing found that RFC822 and RFC3339 (internal/render/dates.go) each
// format a year >= 10000 into a string their OWN reference layout then
// rejects on Parse: time.Time.Format writes as many digits as the year
// needs, but Go's reference-layout Parse always consumes exactly four. That
// gap is closed here, at the point published_at enters the system, rather
// than in the renderer — a renderer that silently clamped the value would
// produce a document that disagrees with the database.

// itemOutOfRangeYear is a year past the four-digit boundary every renderer
// requires (itemMaxPublishedAtYear); itemOrdinaryPublishedAt is an
// unremarkable in-range date used as a control in every test below to prove
// the new checks do not touch normal dates.
var (
	itemOutOfRangeYear      = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	itemOrdinaryPublishedAt = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
)

// itemAssertRangeRejection fails t unless err is an InvalidArgument whose
// message names the four-digit-year bound rather than reusing RULE-7's
// backdating wording — the two rejections must read differently, or an
// operator who gets "date rejected" with no reason tends to retry the exact
// same request without knowing which of the two distinct rules to fix.
func itemAssertRangeRejection(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected published_at to be refused")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v (%v)", got, err)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Fatalf("error %q does not mention %q — range rejection must be distinguishable from RULE-7 backdating", err.Error(), wantSubstr)
	}
	if strings.Contains(err.Error(), "backdated") || strings.Contains(err.Error(), "current newest") {
		t.Fatalf("error %q reads like the RULE-7 backdating rejection, not a range rejection", err.Error())
	}
}

// TestItemCreate_RejectsOutOfRangePublishedAt proves Create refuses a
// published_at with a year the four-digit date layouts (RFC 822/RFC 3339)
// cannot round-trip.
func TestItemCreate_RejectsOutOfRangePublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-create")

	_, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(itemOutOfRangeYear),
	}})
	itemAssertRangeRejection(t, err, "four-digit")
}

// TestItemCreate_RejectsZeroPublishedAt proves Create refuses the exact
// zero time.Time — treated distinctly from an out-of-range year, since the
// zero value is a plausible-looking in-range date (0001-01-01) far more
// likely to be an uninitialized value than an intended one.
func TestItemCreate_RejectsZeroPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-zero-create")

	_, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(time.Time{}),
	}})
	itemAssertRangeRejection(t, err, "zero time.Time")
}

// TestItemCreate_OrdinaryPublishedAtUnaffected proves the new checks leave
// an unremarkable date alone.
func TestItemCreate_OrdinaryPublishedAtUnaffected(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-create-ok")

	if _, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(itemOrdinaryPublishedAt),
	}}); err != nil {
		t.Fatalf("ordinary published_at was rejected: %v", err)
	}
}

// TestItemUpdate_RejectsOutOfRangePublishedAt is the same rule, exercised on
// Update.
func TestItemUpdate_RejectsOutOfRangePublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-update")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(itemOrdinaryPublishedAt),
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T2", SummaryText: "s2", BodyHtml: "<p>b2</p>",
			PublishedAt: timestamppb.New(itemOutOfRangeYear),
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	itemAssertRangeRejection(t, err, "four-digit")
}

// TestItemUpdate_RejectsZeroPublishedAt is the zero-value rule, exercised
// on Update.
func TestItemUpdate_RejectsZeroPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-zero-update")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(itemOrdinaryPublishedAt),
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T2", SummaryText: "s2", BodyHtml: "<p>b2</p>",
			PublishedAt: timestamppb.New(time.Time{}),
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	itemAssertRangeRejection(t, err, "zero time.Time")
}

// TestItemUpdate_UnrelatedEditNotBlockedByOmittedPublishedAt proves an
// Update that does not touch published_at at all (an ordinary title/body
// edit) is never rejected by the range check, even implicitly — the range
// check only runs when the caller actively supplies a published_at.
func TestItemUpdate_UnrelatedEditNotBlockedByOmittedPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-update-omit")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(itemOrdinaryPublishedAt),
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T2", SummaryText: "s2", BodyHtml: "<p>b2</p>",
			// PublishedAt deliberately omitted: Update must keep old.PublishedAt
			// and must not run the range check against it.
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	}); err != nil {
		t.Fatalf("update omitting published_at was rejected: %v", err)
	}
}

// TestItemPublishCorrection_RejectsOutOfRangeStamp proves PublishCorrection
// refuses to insert a correction when the stamp it derives (feed's stored
// newest + 1 second) would fall outside the four-digit-year range —
// PublishCorrectionRequest carries no published_at field, so this exercises
// the pre-write validation of the computed stamp rather than caller input.
func TestItemPublishCorrection_RejectsOutOfRangeStamp(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-correction")

	// The last representable instant within the four-digit-year bound: any
	// correction stamped strictly after it (newest + 1s, per PublishCorrection's
	// own no-backdating rule) necessarily overflows into year 10000.
	boundary := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	original, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(boundary),
	}})
	if err != nil {
		t.Fatalf("create at year-9999 boundary: %v", err)
	}

	_, err = srv.PublishCorrection(ctx, &affv1.ItemServicePublishCorrectionRequest{
		CorrectsItemId: original.GetItem().GetId(), Title: "Fix", SummaryText: "fixed", BodyHtml: "<p>fixed</p>",
	})
	itemAssertRangeRejection(t, err, "four-digit")
}

// TestItemPromoteSample_RejectsOutOfRangeCommittedPublishedAt proves
// PromoteSample surfaces an error rather than silently succeeding when the
// stamp it commits (entirely internal to store.Store.PromoteSample — the
// request has no published_at field) lands outside the four-digit-year
// range. Unlike Create/Update/PublishCorrection this cannot be a pre-write
// InvalidArgument: there is no caller-supplied field to blame, and the
// write has already committed by the time this package can see the result.
func TestItemPromoteSample_RejectsOutOfRangeCommittedPublishedAt(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-range-promote")

	boundary := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if _, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(boundary),
	}}); err != nil {
		t.Fatalf("create at year-9999 boundary: %v", err)
	}

	payload := `[{"candidate_id":"c1","title":"Promoted","summary_text":"promoted summary","body_html":"<p>promoted</p>"}]`
	sampleID, err := st.PutSample(ctx, feedID, []byte(payload), 10, 20, 0.01, time.Hour)
	if err != nil {
		t.Fatalf("put sample: %v", err)
	}

	_, err = srv.PromoteSample(ctx, &affv1.ItemServicePromoteSampleRequest{
		SampleId: strconv.FormatInt(sampleID, 10), CandidateId: "c1",
	})
	if err == nil {
		t.Fatal("expected promote of a sample stamped past the feed's four-digit-year boundary to be refused")
	}
	if got := status.Code(err); got != codes.Internal {
		t.Fatalf("expected Internal (this is a store-side data-consistency problem, not a caller mistake), got %v (%v)", got, err)
	}
}

// --- sub-second published_at / rendered-pubDate collisions -----------------
//
// RSS's RFC 822 pubDate (internal/render's RFC822, §5.1) has one-second
// resolution. Two items whose published_at differ only within the same
// second render byte-identical — the exact "one item is invisible forever,
// silently" failure §5.5 describes for Slack's bookmark model, which
// advances a watermark past the newer of the two and never revisits the
// other. These tests prove Create/Update close that gap at the point
// published_at enters the system, and that the collision-retry path (for
// the race Create's separate read-then-insert leaves open) actually runs.

// TestItemCreate_SubSecondPublishedAtTruncatedToWholeSecond proves a
// caller-supplied sub-second published_at does not survive Create: what
// comes back (and what is stored) has no sub-second component, so it
// round-trips through RFC 822 unchanged.
func TestItemCreate_SubSecondPublishedAtTruncatedToWholeSecond(t *testing.T) {
	srv, _, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, srv.st, "feed-subsecond-create")

	withFrac := time.Date(2026, 8, 9, 12, 0, 0, 750_000_000, time.UTC) // .75s
	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(withFrac),
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got := created.GetItem().GetPublishedAt().AsTime()
	if !got.Equal(withFrac.Truncate(time.Second)) {
		t.Fatalf("published_at = %v, want %v (truncated to whole seconds)", got, withFrac.Truncate(time.Second))
	}
	if got.Nanosecond() != 0 {
		t.Fatalf("published_at retained a sub-second component: %v", got)
	}
}

// TestItemCreate_RejectsSubSecondCollisionWithExistingItem proves two items
// whose explicit published_at values differ only within the same second
// cannot both exist in one feed: the second Create is refused as backdated
// (RULE-7, §5.5), because once truncated it is at-or-before the feed's
// current newest — exactly what would otherwise render as a duplicate
// pubDate.
func TestItemCreate_RejectsSubSecondCollisionWithExistingItem(t *testing.T) {
	srv, _, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, srv.st, "feed-subsecond-collide")

	base := time.Date(2026, 8, 9, 12, 0, 0, 100_000_000, time.UTC) // .1s
	if _, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "First", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(base),
	}}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	// 400ms later — a different instant, but the same second once truncated.
	_, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Second", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(base.Add(400 * time.Millisecond)),
	}})
	if err == nil {
		t.Fatal("expected a sub-second-apart published_at (same second once truncated) to be refused")
	}
	if got := status.Code(err); got != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v (%v)", got, err)
	}
}

// TestItemCreate_CollisionRetryBumpsByWholeSecond proves the +1-second
// collision retry (itemTimestampRetries, mirroring PromoteSample's and
// PublishCorrection's identical pattern) actually runs, not merely
// compiles: itemCreateAfterReadNewestHook opens the same race window
// PromoteSample's promoteAfterReadNewestHook does in samples_test.go —
// inserting a colliding row through the store directly, after Create has
// already read "newest" and validated against it, but before Create's own
// insert. Create must still succeed, landing one whole second after the
// row that snuck in ahead of it, rather than failing or silently landing on
// top of it.
func TestItemCreate_CollisionRetryBumpsByWholeSecond(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-collision-retry")

	pinned := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	srv.now = func() time.Time { return pinned }

	t.Cleanup(func() { itemCreateAfterReadNewestHook = nil })
	itemCreateAfterReadNewestHook = func() {
		itemCreateAfterReadNewestHook = nil // fire exactly once
		sneak := model.Item{
			FeedID:      feedID,
			ItemKey:     "snuck-in-ahead",
			ContentHash: "snuck-in-ahead-hash",
			Title:       "Snuck in ahead",
			SummaryText: "s",
			BodyHTML:    "<p>b</p>",
			PublishedAt: pinned,
			Origin:      model.OriginManual,
		}
		if _, err := st.InsertItem(ctx, sneak); err != nil {
			t.Fatalf("inserting colliding row inside the hook: %v", err)
		}
	}

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "Racing create", SummaryText: "s", BodyHtml: "<p>b</p>",
	}})
	if err != nil {
		t.Fatalf("create: %v (collision retry should have absorbed the race, not surfaced it)", err)
	}

	got := created.GetItem().GetPublishedAt().AsTime()
	want := pinned.Add(time.Second)
	if !got.Equal(want) {
		t.Fatalf("published_at = %v, want %v (one whole second after the row that won the race)", got, want)
	}

	// And the two rows are genuinely distinct once rendered, not merely
	// distinct in Go's in-memory representation.
	sneakIt, err := st.GetItem(ctx, "snuck-in-ahead")
	if err != nil {
		t.Fatalf("get snuck-in-ahead item: %v", err)
	}
	if got.Equal(sneakIt.PublishedAt) {
		t.Fatalf("racing create landed on the exact same published_at as the row that won the race: %v", got)
	}
}

// TestItemUpdate_SubSecondPublishedAtTruncatedToWholeSecond mirrors the
// Create test above, for Update.
func TestItemUpdate_SubSecondPublishedAtTruncatedToWholeSecond(t *testing.T) {
	srv, st, _ := newItemTestServer(t)
	ctx := t.Context()
	feedID := itemMustCreateFeed(t, st, "feed-subsecond-update")

	created, err := srv.Create(ctx, &affv1.ItemServiceCreateRequest{Item: &affv1.Item{
		FeedId: feedID, Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
		PublishedAt: timestamppb.New(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)),
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	withFrac := time.Date(2026, 8, 9, 13, 0, 0, 250_000_000, time.UTC) // .25s, strictly after
	updated, err := srv.Update(ctx, &affv1.ItemServiceUpdateRequest{
		Item: &affv1.Item{
			Id: created.GetItem().GetId(), Title: "T", SummaryText: "s", BodyHtml: "<p>b</p>",
			PublishedAt: timestamppb.New(withFrac),
		},
		ExpectedVersion: created.GetItem().GetVersion(),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	got := updated.GetItem().GetPublishedAt().AsTime()
	if !got.Equal(withFrac.Truncate(time.Second)) {
		t.Fatalf("published_at = %v, want %v (truncated to whole seconds)", got, withFrac.Truncate(time.Second))
	}
	if got.Nanosecond() != 0 {
		t.Fatalf("published_at retained a sub-second component: %v", got)
	}
}
