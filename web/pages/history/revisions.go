package history

import (
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// ItemSnapshot is the editable-field subset of an Item used to seed the
// create/edit form (forms_ui.go's formStateFromItem/snapshotOf) — PLAN.md
// §12.4: "Update — title, summary, body, link, tags, publish date".
// ItemKey is deliberately absent: it never changes on edit or revert
// (PLAN.md §5.1).
type ItemSnapshot struct {
	Title       string
	SummaryText string
	BodyHTML    string
	Link        string
	Tags        []string
	PublishedAt time.Time
}

// RevisionFieldDiffs turns one ItemRevision's already-recorded per-field
// old/new values into line-level diffs for display. PLAN.md §12.4: "each
// ItemRevision carries id, at and all changes, so a diff renders without a
// second call" — item_revisions stores only the fields that actually
// changed for that edit (internal/rpc/item.go's itemDiff only writes a row
// when old != new), so every entry in rev.Changes is, by construction, an
// actual change: no comparison is needed here, just DiffLines applied to
// each field's stored old/new pair.
func RevisionFieldDiffs(rev *affv1.ItemRevision) []FieldDiff {
	changes := rev.GetChanges()
	out := make([]FieldDiff, 0, len(changes))
	for _, ch := range changes {
		out = append(out, FieldDiff{
			Field: ch.GetField(),
			Lines: DiffLines(ch.GetOldValue(), ch.GetNewValue()),
		})
	}
	return out
}

// BuildListRevisionsRequest constructs ItemService.ListRevisions' request.
// Pagination is by `at` (PLAN.md §12.4; internal/rpc/item.go's
// ListRevisions doc comment: "paginated by at so a page boundary can never
// split one edit's field changes across two pages"), via the same opaque
// page_token/page_size contract ItemService.List already uses
// (pagination.go's ClampPageSize). Nothing on this page re-groups pages
// client-side — each returned page's Revisions are already complete edit
// groups, so honouring the boundary just means never merging two pages'
// results into one re-sorted list.
func BuildListRevisionsRequest(itemID int64, pageToken string, pageSize int32) *affv1.ItemServiceListRevisionsRequest {
	return &affv1.ItemServiceListRevisionsRequest{
		ItemId:    itemID,
		PageSize:  ClampPageSize(pageSize),
		PageToken: pageToken,
	}
}
