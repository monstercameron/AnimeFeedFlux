package rpc

import (
	"path/filepath"
	"reflect"
	"strconv"
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

	payload := `[{"candidateId":"c1","title":"Promoted","summaryText":"promoted summary","bodyHtml":"<p>promoted</p>"}]`
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
