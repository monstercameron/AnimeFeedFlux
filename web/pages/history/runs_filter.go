package history

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// RunFilter is the Runs tab's filter state (PLAN.md §12.4: "Filter by
// feed, status, date range"). Zero values mean unfiltered on every field.
type RunFilter struct {
	FeedID        int64
	Status        affv1.RunStatus
	StartedAfter  *time.Time
	StartedBefore *time.Time
}

// BuildRunHistoryRequest constructs the RunService.History request from a
// filter, page token, and requested page size, kept as a pure function so
// filter-to-request wiring is testable without a client or a component.
func BuildRunHistoryRequest(f RunFilter, pageToken string, pageSize int32) *affv1.RunServiceHistoryRequest {
	req := &affv1.RunServiceHistoryRequest{
		PageSize:  ClampPageSize(pageSize),
		PageToken: pageToken,
		FeedId:    f.FeedID,
		Status:    f.Status,
	}
	if f.StartedAfter != nil {
		req.StartedAfter = timestamppb.New(*f.StartedAfter)
	}
	if f.StartedBefore != nil {
		req.StartedBefore = timestamppb.New(*f.StartedBefore)
	}
	return req
}
