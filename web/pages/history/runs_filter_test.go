package history

import (
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func TestBuildRunHistoryRequest(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	f := RunFilter{
		FeedID:        7,
		Status:        affv1.RunStatus_RUN_STATUS_FAILED,
		StartedAfter:  &after,
		StartedBefore: &before,
	}

	req := BuildRunHistoryRequest(f, "cursor-tok", 50)

	if req.FeedId != 7 {
		t.Errorf("FeedId = %d, want 7", req.FeedId)
	}
	if req.Status != affv1.RunStatus_RUN_STATUS_FAILED {
		t.Errorf("Status = %v, want RUN_STATUS_FAILED", req.Status)
	}
	if req.PageToken != "cursor-tok" {
		t.Errorf("PageToken = %q, want cursor-tok", req.PageToken)
	}
	if req.PageSize != 50 {
		t.Errorf("PageSize = %d, want 50", req.PageSize)
	}
	if !req.StartedAfter.AsTime().Equal(after) {
		t.Errorf("StartedAfter = %v, want %v", req.StartedAfter.AsTime(), after)
	}
	if !req.StartedBefore.AsTime().Equal(before) {
		t.Errorf("StartedBefore = %v, want %v", req.StartedBefore.AsTime(), before)
	}
}

func TestBuildRunHistoryRequestUnfiltered(t *testing.T) {
	req := BuildRunHistoryRequest(RunFilter{}, "", 0)
	if req.FeedId != 0 || req.Status != affv1.RunStatus_RUN_STATUS_UNSPECIFIED {
		t.Errorf("expected unfiltered fields, got %+v", req)
	}
	if req.StartedAfter != nil || req.StartedBefore != nil {
		t.Errorf("expected nil timestamps when unset, got %+v", req)
	}
	if req.PageSize != DefaultPageSize {
		t.Errorf("PageSize = %d, want default %d", req.PageSize, DefaultPageSize)
	}
}
