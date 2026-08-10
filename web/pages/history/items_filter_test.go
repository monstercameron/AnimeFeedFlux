package history

import (
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func TestBuildItemListRequest(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	f := ItemFilter{
		Query:           "trivia",
		FeedID:          3,
		Origin:          affv1.Origin_ORIGIN_MANUAL,
		PublishedAfter:  &after,
		PublishedBefore: &before,
		DeletedFilter:   affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED,
	}

	req := BuildItemListRequest(f, "tok", 300)

	if req.Query != "trivia" {
		t.Errorf("Query = %q, want trivia", req.Query)
	}
	if req.FeedId != 3 {
		t.Errorf("FeedId = %d, want 3", req.FeedId)
	}
	if req.Origin != affv1.Origin_ORIGIN_MANUAL {
		t.Errorf("Origin = %v, want ORIGIN_MANUAL", req.Origin)
	}
	if req.DeletedFilter != affv1.DeletedFilter_DELETED_FILTER_ONLY_DELETED {
		t.Errorf("DeletedFilter = %v, want ONLY_DELETED", req.DeletedFilter)
	}
	if req.PageSize != MaxPageSize {
		t.Errorf("PageSize = %d, want clamped to %d", req.PageSize, MaxPageSize)
	}
	if !req.PublishedAfter.AsTime().Equal(after) {
		t.Errorf("PublishedAfter = %v, want %v", req.PublishedAfter.AsTime(), after)
	}
	if !req.PublishedBefore.AsTime().Equal(before) {
		t.Errorf("PublishedBefore = %v, want %v", req.PublishedBefore.AsTime(), before)
	}
}

func TestBuildItemListRequestUnfiltered(t *testing.T) {
	req := BuildItemListRequest(ItemFilter{}, "", 10)
	if req.Query != "" || req.FeedId != 0 {
		t.Errorf("expected unfiltered fields, got %+v", req)
	}
	if req.PublishedAfter != nil || req.PublishedBefore != nil {
		t.Errorf("expected nil timestamps when unset, got %+v", req)
	}
	if req.DeletedFilter != affv1.DeletedFilter_DELETED_FILTER_UNSPECIFIED {
		t.Errorf("DeletedFilter = %v, want UNSPECIFIED passthrough", req.DeletedFilter)
	}
}
