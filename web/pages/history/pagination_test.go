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
