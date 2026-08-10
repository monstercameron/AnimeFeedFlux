package history

import "time"

// BackdateDecision is the pure outcome of validating a proposed
// published_at against PLAN.md §5.5 / TODOS.md D3-14: "Block backdating
// published_at; warn loudly on any override." Slack stores the newest
// published_at it has fetched and only pulls items dated strictly later;
// a backdated item is invisible to it forever, silently, which §5.5 calls
// the worst failure mode in this product.
type BackdateDecision struct {
	// Blocked is true when the write must not proceed: proposedAt is not
	// after the feed's current newest published item, and no override was
	// given.
	Blocked bool
	// WarnKey is a non-empty Catalog key whenever the write is a backdate
	// that proceeded only because of an explicit override — the caller
	// must render this warning loudly next to the save action, not as a
	// dismissible toast that can be missed.
	WarnKey string
}

// ValidatePublishedAt decides whether proposedAt is acceptable for a feed
// whose current newest published (non-deleted) item is at
// newestPublishedAt. override is the operator's explicit, typed
// acknowledgement of a backdate (never a default).
//
// A proposedAt equal to newestPublishedAt is treated as a backdate too:
// UNIQUE(feed_id, published_at) (PLAN.md §10) means it cannot land as a
// second item at that exact timestamp regardless, and "strictly after the
// current newest" is the only timestamp guaranteed visible to Slack's
// bookmark (PLAN.md §5.5).
func ValidatePublishedAt(proposedAt, newestPublishedAt time.Time, override bool) BackdateDecision {
	if proposedAt.After(newestPublishedAt) {
		return BackdateDecision{}
	}
	if !override {
		return BackdateDecision{Blocked: true}
	}
	return BackdateDecision{WarnKey: "history.items.backdate_override_warning"}
}
