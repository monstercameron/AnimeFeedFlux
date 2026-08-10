package history

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// ItemFilter is the Items tab's filter state (PLAN.md §12.4: "FTS5 search
// plus filters on feed, origin, date range, deleted state"). Zero values
// mean unfiltered, except DeletedFilter which defaults to "exclude
// deleted" at the call site (BuildItemListRequest does not invent that
// default itself — see the DeletedFilter field doc).
type ItemFilter struct {
	Query           string
	FeedID          int64
	Origin          affv1.Origin
	PublishedAfter  *time.Time
	PublishedBefore *time.Time
	// DeletedFilter is DELETED_FILTER_UNSPECIFIED (0) at the zero value;
	// BuildItemListRequest passes it through as-is rather than defaulting
	// it, because "what unspecified means" is a server-contract decision
	// this package should not silently make on the server's behalf. The
	// Items UI sets it explicitly (defaulting its own control to "exclude
	// deleted" per PLAN.md §12.4's normal list view).
	DeletedFilter affv1.DeletedFilter
}

// BuildItemListRequest constructs the ItemService.List request from a
// filter, page token, and requested page size.
func BuildItemListRequest(f ItemFilter, pageToken string, pageSize int32) *affv1.ItemServiceListRequest {
	req := &affv1.ItemServiceListRequest{
		PageSize:      ClampPageSize(pageSize),
		PageToken:     pageToken,
		Query:         f.Query,
		FeedId:        f.FeedID,
		Origin:        f.Origin,
		DeletedFilter: f.DeletedFilter,
	}
	if f.PublishedAfter != nil {
		req.PublishedAfter = timestamppb.New(*f.PublishedAfter)
	}
	if f.PublishedBefore != nil {
		req.PublishedBefore = timestamppb.New(*f.PublishedBefore)
	}
	return req
}
