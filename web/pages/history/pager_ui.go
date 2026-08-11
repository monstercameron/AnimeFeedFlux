//go:build js

package history

import (
	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// pagerProps configures renderPager. Both tables pass the same shape, which is
// the point: the Runs and Items pagers were two hand-rolled rows of buttons
// that had already drifted (Items grew a Next control on a different day than
// Runs), and a third table would have made three.
type pagerProps struct {
	T Catalog
	// Page is 1-based and TotalPages comes from the server's total_count.
	Page       int
	TotalPages int
	// VisitedN is how many pages the cursor holds tokens for — the highest
	// page reachable in one click. See PageCursor.Visited.
	VisitedN  int
	HasPrev   bool
	HasNext   bool
	Disabled  bool
	OnPrev    func()
	OnNext    func()
	OnJump    func(page int)
	OnRefresh func()
}

// maxJumpButtons caps how many numbered buttons are rendered.
//
// A jump control that draws one button per page is fine at nine pages and
// unusable at four hundred — and four hundred is the ordinary case here, since
// runs accumulate one row per feed per day forever. Past the cap the numbers
// window around the current page, so the control stays the same size whatever
// the table's length.
const maxJumpButtons = 7

// renderPager draws Previous / numbered jumps / Next, plus "Page X of Y".
//
// # Why the numbers are not simply 1..TotalPages
//
// These tables page by an opaque server cursor, and a cursor is only
// obtainable by having been handed it. Page 9 is therefore unreachable until
// pages 4 through 8 have each been fetched for their tokens — jumping there
// directly is not something the client can do, however much a button implies
// it can. So a numbered button is only offered for a page the cursor already
// holds a token for (PageCursor.CanJumpTo); pages beyond that render as
// disabled, still counted in "of Y" so the operator can see how far the table
// goes, but honest about needing Next to get there.
//
// The alternative — render every page as clickable and silently reload the
// current one when the token is missing — is the failure this avoids: a
// control that appears to work and doesn't.
func renderPager(p pagerProps) ui.Node {
	t := p.T

	nodes := []any{h.ClassStr("history-pager")}

	nodes = append(nodes,
		h.Button(h.Type("button"),
			h.ClassStr("history-pager__step"),
			h.Disabled(p.Disabled || !p.HasPrev),
			h.OnClick(func() {
				if p.OnPrev != nil {
					p.OnPrev()
				}
			}),
			t.T("history.pager.previous", nil)),
	)

	// The numbered window.
	first, last := jumpWindow(p.Page, p.TotalPages, maxJumpButtons)
	if first > 1 {
		nodes = append(nodes, h.Span(h.ClassStr("history-pager__gap"), h.Text("…")))
	}
	for page := first; page <= last; page++ {
		page := page
		current := page == p.Page
		reachable := page <= p.VisitedN
		nodes = append(nodes, h.Button(
			h.Type("button"),
			h.ClassStr(h.ClassMap(map[string]bool{
				"history-pager__page":            true,
				"history-pager__page--current":   current,
				"history-pager__page--unreached": !reachable && !current,
			})),
			// aria-current marks the page you are on for a screen reader,
			// which cannot see the highlight.
			h.Aria("current", pagerAriaCurrent(current)),
			h.Disabled(p.Disabled || current || !reachable),
			h.OnClick(func() {
				if p.OnJump != nil {
					p.OnJump(page)
				}
			}),
			h.Text(intToStr(page)),
		))
	}
	if last < p.TotalPages {
		nodes = append(nodes, h.Span(h.ClassStr("history-pager__gap"), h.Text("…")))
	}

	nodes = append(nodes,
		h.Button(h.Type("button"),
			h.ClassStr("history-pager__step"),
			h.Disabled(p.Disabled || !p.HasNext),
			h.OnClick(func() {
				if p.OnNext != nil {
					p.OnNext()
				}
			}),
			t.T("history.pager.next", nil)),
		// Stated in words as well as drawn, because "3" highlighted among
		// numbers does not say how much is left, and role=status announces a
		// page change to a screen reader without moving focus.
		h.Span(h.ClassStr("history-pager__status"), h.Role("status"),
			t.T("history.pager.status", map[string]any{
				"page":  intToStr(p.Page),
				"total": intToStr(p.TotalPages),
			})),
		h.Button(h.Type("button"),
			h.ClassStr("history-pager__step"),
			h.Disabled(p.Disabled),
			h.OnClick(func() {
				if p.OnRefresh != nil {
					p.OnRefresh()
				}
			}),
			t.T("history.pager.refresh", nil)),
	)

	return h.Div(nodes...)
}

func pagerAriaCurrent(current bool) string {
	if current {
		return "page"
	}
	return "false"
}
