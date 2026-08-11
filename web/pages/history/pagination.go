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

// PageNumber is the 1-based page the cursor is on, for display.
func (c *PageCursor) PageNumber() int { return c.index + 1 }

// Visited is how many pages the cursor holds tokens for, i.e. the highest
// page reachable without fetching anything new.
//
// This is NOT the number of pages that exist — the server's total_count
// answers that. It is the number that can be jumped to INSTANTLY, which is a
// different and equally necessary fact: an opaque cursor is only obtainable
// by having received it, so page 9 is unreachable until pages 4 through 8
// have each been fetched for their tokens. A jump control that offered 9 and
// then silently did nothing would be worse than one that shows what it can
// actually do.
func (c *PageCursor) Visited() int { return len(c.tokens) }

// CanJumpTo reports whether page (1-based) is one this cursor already holds a
// token for.
func (c *PageCursor) CanJumpTo(page int) bool {
	return page >= 1 && page <= len(c.tokens)
}

// JumpTo moves to a already-visited page (1-based), reporting whether it
// moved. A page the cursor has no token for is refused rather than
// approximated: requesting the wrong token returns a valid-looking page of
// the wrong rows, which is the one failure mode a pager must not have.
func (c *PageCursor) JumpTo(page int) bool {
	if !c.CanJumpTo(page) {
		return false
	}
	c.index = page - 1
	return true
}

// TotalPages converts a server total_count into a page count.
//
// Zero rows is one page, not zero: the table still renders, with its empty
// state, and "page 1 of 0" is a self-contradiction an operator has to stop
// and parse. A non-positive pageSize would divide by zero, so it falls back
// to the same default the request itself would have been clamped to.
func TotalPages(totalCount int64, pageSize int32) int {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if totalCount <= 0 {
		return 1
	}
	pages := totalCount / int64(pageSize)
	if totalCount%int64(pageSize) != 0 {
		pages++
	}
	return int(pages)
}

// jumpWindow picks which page numbers to draw, centred on the current page and
// clamped to the ends so the control never renders fewer buttons than it could
// (a window centred on page 1 would otherwise waste half its slots on pages
// that do not exist).
func jumpWindow(page, total, size int) (first, last int) {
	if total < 1 {
		total = 1
	}
	if size < 1 {
		size = 1
	}
	if total <= size {
		return 1, total
	}
	half := size / 2
	first = page - half
	if first < 1 {
		first = 1
	}
	last = first + size - 1
	if last > total {
		last = total
		first = last - size + 1
		if first < 1 {
			first = 1
		}
	}
	return first, last
}
