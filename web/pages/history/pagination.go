package history

// Page-size bounds for the paginated RunService.History and
// ItemService.List RPCs (TODOS.md D3-10; PLAN.md §11: "list RPCs paginate
// with an opaque cursor").
const (
	DefaultPageSize int32 = 25
	MinPageSize     int32 = 1
	MaxPageSize     int32 = 200
)

// ClampPageSize is the pagination-boundary predicate under test: a
// non-positive request falls back to the default, and anything above
// MaxPageSize is capped rather than trusted verbatim from a UI control an
// operator could otherwise set arbitrarily high against the server.
func ClampPageSize(requested int32) int32 {
	switch {
	case requested <= 0:
		return DefaultPageSize
	case requested > MaxPageSize:
		return MaxPageSize
	default:
		return requested
	}
}

// PageCursor is the client-side page stack needed to support a "previous
// page" control against an opaque server cursor: the server only ever
// hands back a next_page_token, so going back means replaying the tokens
// already seen rather than asking the server for something it does not
// offer.
type PageCursor struct {
	tokens []string // tokens[i] is the token that produced page i; tokens[0] == ""
	index  int
}

// NewPageCursor starts at the first page (empty token).
func NewPageCursor() *PageCursor {
	return &PageCursor{tokens: []string{""}, index: 0}
}

// Current returns the page_token to request for the current page.
func (c *PageCursor) Current() string {
	return c.tokens[c.index]
}

// Advance records nextToken as the token for the next page and moves to
// it. Calling Advance with an empty nextToken is a no-op guard for "no
// more pages" — callers should check that condition themselves before
// advancing, but Advance stays safe either way.
func (c *PageCursor) Advance(nextToken string) {
	if nextToken == "" {
		return
	}
	// If we're not at the tail (the operator went back and is now moving
	// forward again), truncate the stale forward history rather than
	// appending a duplicate branch.
	if c.index < len(c.tokens)-1 {
		c.tokens = c.tokens[:c.index+1]
	}
	c.tokens = append(c.tokens, nextToken)
	c.index++
}

// HasPrevious reports whether Back has anywhere to go.
func (c *PageCursor) HasPrevious() bool {
	return c.index > 0
}

// Back moves to the previous page, if any.
func (c *PageCursor) Back() {
	if c.HasPrevious() {
		c.index--
	}
}

// Reset returns the cursor to the first page, discarding history — used
// whenever a filter or query changes, since the old token stack no longer
// corresponds to the new result set.
func (c *PageCursor) Reset() {
	c.tokens = []string{""}
	c.index = 0
}
