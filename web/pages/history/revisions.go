package history

import "time"

// ItemSnapshot is the editable-field subset of an Item (PLAN.md §12.4:
// "Update — title, summary, body, link, tags, publish date"). ItemKey is
// deliberately absent: it never changes on edit (PLAN.md §5.1) and is not
// part of any revision.
type ItemSnapshot struct {
	Title       string
	SummaryText string
	BodyHTML    string
	Link        string
	Tags        []string
	PublishedAt time.Time
}

// Revision is one recorded edit: the state immediately before the edit
// (Before), the state it produced (After), and when it happened. See
// doc.go for why this is a client-side, in-session stand-in for the
// server-side item_revisions table rather than a read of it — no RPC
// exists yet to list or revert that table's rows.
type Revision struct {
	At     time.Time
	Before ItemSnapshot
	After  ItemSnapshot
}

// Diff computes the per-field diff view for this revision, omitting
// fields that did not change.
func (r Revision) Diff() []FieldDiff {
	candidates := []FieldDiff{
		{Field: "title", Lines: DiffLines(r.Before.Title, r.After.Title)},
		{Field: "summary_text", Lines: DiffLines(r.Before.SummaryText, r.After.SummaryText)},
		{Field: "body_html", Lines: DiffLines(r.Before.BodyHTML, r.After.BodyHTML)},
		{Field: "link", Lines: DiffLines(r.Before.Link, r.After.Link)},
		{Field: "tags", Lines: DiffLines(joinTags(r.Before.Tags), joinTags(r.After.Tags))},
		{Field: "published_at", Lines: DiffLines(r.Before.PublishedAt.Format(time.RFC3339), r.After.PublishedAt.Format(time.RFC3339))},
	}
	out := make([]FieldDiff, 0, len(candidates))
	for _, c := range candidates {
		if c.Changed() {
			out = append(out, c)
		}
	}
	return out
}

func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += "\n"
		}
		out += t
	}
	return out
}

// RevisionStore holds session-local revision history per item id. It is
// not persisted (a reload loses it, honestly reflecting that it is not
// backed by the server's item_revisions table — see doc.go).
type RevisionStore struct {
	byItem map[int64][]Revision
}

// NewRevisionStore returns an empty store.
func NewRevisionStore() *RevisionStore {
	return &RevisionStore{byItem: make(map[int64][]Revision)}
}

// Record appends a revision for itemID. Call it immediately before
// submitting an Update RPC, with before = the snapshot as loaded and
// after = the snapshot about to be submitted.
func (s *RevisionStore) Record(itemID int64, before, after ItemSnapshot, at time.Time) {
	s.byItem[itemID] = append(s.byItem[itemID], Revision{At: at, Before: before, After: after})
}

// List returns itemID's revisions, oldest first.
func (s *RevisionStore) List(itemID int64) []Revision {
	return s.byItem[itemID]
}

// RevertTarget returns the snapshot to restore an item to when reverting
// revision index revIndex (0-based, oldest first) in itemID's history: the
// state that revision's edit overwrote. ok is false if itemID/revIndex is
// out of range.
func RevertTarget(s *RevisionStore, itemID int64, revIndex int) (snap ItemSnapshot, ok bool) {
	revs := s.byItem[itemID]
	if revIndex < 0 || revIndex >= len(revs) {
		return ItemSnapshot{}, false
	}
	return revs[revIndex].Before, true
}
