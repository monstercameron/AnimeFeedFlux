package history

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func TestRevisionFieldDiffs(t *testing.T) {
	at := timestamppb.New(time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC))
	rev := &affv1.ItemRevision{
		Id:     7,
		ItemId: 42,
		At:     at,
		Changes: []*affv1.ItemRevisionChange{
			{Field: "title", OldValue: "Old title", NewValue: "New title"},
			{Field: "link", OldValue: "https://x/1", NewValue: "https://x/2"},
		},
	}

	diffs := RevisionFieldDiffs(rev)
	if len(diffs) != 2 {
		t.Fatalf("len(diffs) = %d, want 2", len(diffs))
	}
	if diffs[0].Field != "title" || !diffs[0].Changed() {
		t.Errorf("diffs[0] = %+v, want changed title diff", diffs[0])
	}
	if diffs[1].Field != "link" || !diffs[1].Changed() {
		t.Errorf("diffs[1] = %+v, want changed link diff", diffs[1])
	}

	// Every recorded change is a real change by construction (item_revisions
	// only ever gets a row when old != new — internal/rpc/item.go's
	// itemDiff), so the diff for each field must contain at least one
	// non-equal line.
	for _, d := range diffs {
		if !d.Changed() {
			t.Errorf("field %q: expected a non-equal diff line, got %+v", d.Field, d.Lines)
		}
	}
}

func TestRevisionFieldDiffsEmpty(t *testing.T) {
	rev := &affv1.ItemRevision{Id: 1, ItemId: 1}
	if diffs := RevisionFieldDiffs(rev); len(diffs) != 0 {
		t.Fatalf("len(diffs) = %d, want 0 for a revision with no changes", len(diffs))
	}
}

func TestRevisionFieldDiffsNilRevision(t *testing.T) {
	if diffs := RevisionFieldDiffs(nil); len(diffs) != 0 {
		t.Fatalf("RevisionFieldDiffs(nil) = %+v, want empty", diffs)
	}
}

func TestBuildListRevisionsRequest(t *testing.T) {
	req := BuildListRevisionsRequest(42, "tok", 10)
	if req.ItemId != 42 {
		t.Errorf("ItemId = %d, want 42", req.ItemId)
	}
	if req.PageToken != "tok" {
		t.Errorf("PageToken = %q, want tok", req.PageToken)
	}
	if req.PageSize != 10 {
		t.Errorf("PageSize = %d, want 10", req.PageSize)
	}
}

func TestBuildListRevisionsRequestClampsPageSize(t *testing.T) {
	req := BuildListRevisionsRequest(42, "", 0)
	if req.PageSize != DefaultPageSize {
		t.Errorf("PageSize = %d, want DefaultPageSize %d for a non-positive request", req.PageSize, DefaultPageSize)
	}

	req = BuildListRevisionsRequest(42, "", MaxPageSize+50)
	if req.PageSize != MaxPageSize {
		t.Errorf("PageSize = %d, want MaxPageSize %d for an oversized request", req.PageSize, MaxPageSize)
	}
}
