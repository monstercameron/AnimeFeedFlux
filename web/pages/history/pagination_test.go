package history

import "testing"

func TestClampPageSize(t *testing.T) {
	cases := []struct {
		in   int32
		want int32
	}{
		{0, DefaultPageSize},
		{-5, DefaultPageSize},
		{1, 1},
		{MaxPageSize, MaxPageSize},
		{MaxPageSize + 1, MaxPageSize},
		{1_000_000, MaxPageSize},
	}
	for _, c := range cases {
		if got := ClampPageSize(c.in); got != c.want {
			t.Errorf("ClampPageSize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPageCursorForwardAndBack(t *testing.T) {
	c := NewPageCursor()
	if c.Current() != "" {
		t.Fatalf("initial Current() = %q, want empty", c.Current())
	}
	if c.HasPrevious() {
		t.Fatalf("fresh cursor should have no previous page")
	}

	c.Advance("tok1")
	if c.Current() != "tok1" {
		t.Fatalf("Current() = %q, want tok1", c.Current())
	}
	if !c.HasPrevious() {
		t.Fatalf("expected HasPrevious after one Advance")
	}

	c.Advance("tok2")
	if c.Current() != "tok2" {
		t.Fatalf("Current() = %q, want tok2", c.Current())
	}

	c.Back()
	if c.Current() != "tok1" {
		t.Fatalf("after Back, Current() = %q, want tok1", c.Current())
	}

	c.Back()
	if c.Current() != "" {
		t.Fatalf("after second Back, Current() = %q, want empty", c.Current())
	}
	if c.HasPrevious() {
		t.Fatalf("expected no previous page at the start")
	}

	// Back past the start is a no-op.
	c.Back()
	if c.Current() != "" {
		t.Fatalf("Back past start moved cursor: Current() = %q", c.Current())
	}
}

func TestPageCursorAdvanceAfterBackTruncatesForwardHistory(t *testing.T) {
	c := NewPageCursor()
	c.Advance("tok1")
	c.Advance("tok2")
	c.Back() // now at tok1, tok2 still in forward history

	c.Advance("tokNew") // new filter/page produced a different next token
	if c.Current() != "tokNew" {
		t.Fatalf("Current() = %q, want tokNew", c.Current())
	}
	c.Back()
	if c.Current() != "tok1" {
		t.Fatalf("after Back, Current() = %q, want tok1", c.Current())
	}
	// tok2 should be gone from history now.
	c.Advance("tokNew")
	if c.Current() != "tokNew" {
		t.Fatalf("stale forward history was not truncated: Current() = %q", c.Current())
	}
}

func TestPageCursorAdvanceEmptyTokenIsNoOp(t *testing.T) {
	c := NewPageCursor()
	c.Advance("")
	if c.Current() != "" || c.HasPrevious() {
		t.Fatalf("Advance(\"\") should be a no-op, got Current()=%q HasPrevious()=%v", c.Current(), c.HasPrevious())
	}
}

func TestPageCursorReset(t *testing.T) {
	c := NewPageCursor()
	c.Advance("tok1")
	c.Advance("tok2")
	c.Reset()
	if c.Current() != "" || c.HasPrevious() {
		t.Fatalf("Reset did not return to the first page: Current()=%q HasPrevious()=%v", c.Current(), c.HasPrevious())
	}
}

// ApplyPageNav is the whole of page navigation for both history tabs, which
// is why it is a plain function in an untagged file rather than four cases
// inside two //go:build js reducers: those cases were unreachable from a host
// test, and the duplicated pair had already drifted apart once (A5-41).

func TestApplyPageNavReturnsANewCursorRatherThanMutating(t *testing.T) {
	c := NewPageCursor()
	next, moved := ApplyPageNav(c, NavNext, "tok2", 0)
	if !moved {
		t.Fatal("advancing with a token did not move")
	}
	if next == c {
		t.Fatal("ApplyPageNav returned the same cursor pointer — the reducer's " +
			"state would compare equal to itself and never re-render")
	}
	if c.PageNumber() != 1 {
		t.Fatalf("the original cursor moved to page %d; it must be untouched", c.PageNumber())
	}
	if next.PageNumber() != 2 || next.Current() != "tok2" {
		t.Fatalf("new cursor is on page %d with token %q, want page 2 / tok2",
			next.PageNumber(), next.Current())
	}
}

func TestApplyPageNavRefusesMovesThatWouldGoNowhere(t *testing.T) {
	first := NewPageCursor()
	for _, tc := range []struct {
		name  string
		kind  string
		token string
		page  int
	}{
		{"next with no token — the server said this is the last page", NavNext, "", 0},
		{"previous on page 1", NavPrev, "", 0},
		{"jump to a page no token was ever received for", NavJump, "", 4},
		{"jump to page 0", NavJump, "", 0},
		{"an action this function does not handle", "toggle-expand", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, moved := ApplyPageNav(first, tc.kind, tc.token, tc.page)
			if moved {
				t.Fatal("reported a move")
			}
			// The unchanged cursor comes back identically, so the reducer can
			// return its state untouched instead of manufacturing a new one.
			if got != first {
				t.Fatal("a refused move still allocated a new cursor")
			}
		})
	}
}

func TestApplyPageNavWalksForwardBackAndJumps(t *testing.T) {
	c := NewPageCursor()
	for i, tok := range []string{"tok2", "tok3", "tok4"} {
		var moved bool
		c, moved = ApplyPageNav(c, NavNext, tok, 0)
		if !moved {
			t.Fatalf("advance %d refused", i)
		}
	}
	if c.PageNumber() != 4 {
		t.Fatalf("after three advances the cursor is on page %d, want 4", c.PageNumber())
	}

	c, _ = ApplyPageNav(c, NavPrev, "", 0)
	if c.PageNumber() != 3 || c.Current() != "tok3" {
		t.Fatalf("back landed on page %d / %q, want 3 / tok3", c.PageNumber(), c.Current())
	}

	// A jump backwards must not discard the forward history: the tokens for
	// pages 2..4 were already paid for and are still valid.
	c, moved := ApplyPageNav(c, NavJump, "", 1)
	if !moved || c.PageNumber() != 1 || c.Current() != "" {
		t.Fatalf("jump to page 1 landed on page %d / %q", c.PageNumber(), c.Current())
	}
	if c.Visited() != 4 {
		t.Fatalf("jumping back left %d visited pages, want 4 — forward history was discarded",
			c.Visited())
	}
	if c, _ = ApplyPageNav(c, NavJump, "", 4); c.Current() != "tok4" {
		t.Fatalf("jumping forward again gave token %q, want tok4", c.Current())
	}

	// Reset is what a filter change dispatches: the old tokens describe a
	// result set that no longer exists, so they must all go.
	c, moved = ApplyPageNav(c, NavReset, "", 0)
	if !moved || c.PageNumber() != 1 || c.Visited() != 1 {
		t.Fatalf("after reset: page %d, %d visited; want page 1, 1 visited",
			c.PageNumber(), c.Visited())
	}
}
