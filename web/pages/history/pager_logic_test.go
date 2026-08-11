package history

import (
	"fmt"
	"testing"
)

func TestTotalPagesFromServerCount(t *testing.T) {
	for _, tc := range []struct {
		total int64
		size  int32
		want  int
	}{
		{0, 25, 1},   // empty table is page 1 of 1, never "of 0"
		{1, 25, 1},   //
		{25, 25, 1},  // exactly full
		{26, 25, 2},  // one over rolls to a second page
		{200, 25, 8}, //
		{201, 25, 9}, //
		{-5, 25, 1},  // a nonsense count still renders something coherent
		{100, 0, 4},  // pageSize 0 would divide by zero; falls back to default
	} {
		if got := TotalPages(tc.total, tc.size); got != tc.want {
			t.Errorf("TotalPages(%d, %d) = %d, want %d", tc.total, tc.size, got, tc.want)
		}
	}
}

// The window is what keeps the control the same width whether the table has
// nine pages or four hundred — and runs accumulate one row per feed per day
// forever, so four hundred is the ordinary case, not the extreme.
func TestJumpWindowStaysTheSameSize(t *testing.T) {
	const size = 7
	for _, tc := range []struct{ page, total, wantFirst, wantLast int }{
		{1, 3, 1, 3},      // fewer pages than slots: show them all
		{1, 100, 1, 7},    // at the start, no wasted slots to the left
		{2, 100, 1, 7},    //
		{50, 100, 47, 53}, // centred in the middle
		{100, 100, 94, 100},
		{99, 100, 94, 100}, // near the end, no wasted slots to the right
	} {
		first, last := jumpWindow(tc.page, tc.total, size)
		if first != tc.wantFirst || last != tc.wantLast {
			t.Errorf("jumpWindow(page=%d,total=%d) = %d..%d, want %d..%d",
				tc.page, tc.total, first, last, tc.wantFirst, tc.wantLast)
		}
		if n := last - first + 1; tc.total >= size && n != size {
			t.Errorf("jumpWindow(page=%d,total=%d) drew %d buttons, want %d",
				tc.page, tc.total, n, size)
		}
	}
}

func TestJumpWindowDegenerateInputs(t *testing.T) {
	if f, l := jumpWindow(1, 0, 7); f != 1 || l != 1 {
		t.Errorf("zero total = %d..%d, want 1..1", f, l)
	}
	if f, l := jumpWindow(1, 10, 0); f != 1 || l != 1 {
		t.Errorf("zero size = %d..%d, want 1..1", f, l)
	}
}

// A jump is only offered for a page the cursor already holds a token for.
// Requesting a page it does not have would send the wrong opaque token and
// return a valid-looking page of the WRONG rows — the one failure a pager
// must not have — so JumpTo refuses rather than approximating.
func TestCursorJumpOnlyToVisitedPages(t *testing.T) {
	c := NewPageCursor()
	if got := c.PageNumber(); got != 1 {
		t.Fatalf("fresh cursor is page %d, want 1", got)
	}
	if c.Visited() != 1 {
		t.Fatalf("fresh cursor visited %d, want 1", c.Visited())
	}

	// Walk forward three pages, as pressing Next would.
	for i := 2; i <= 4; i++ {
		c.Advance(fmt.Sprintf("tok-%d", i))
		if c.PageNumber() != i {
			t.Fatalf("after advancing to page %d, PageNumber = %d", i, c.PageNumber())
		}
	}
	if c.Visited() != 4 {
		t.Errorf("visited = %d, want 4", c.Visited())
	}

	// Backward jumps to visited pages work and select that page's token.
	if !c.JumpTo(2) {
		t.Fatal("JumpTo(2) refused a visited page")
	}
	if got, want := c.Current(), "tok-2"; got != want {
		t.Errorf("after JumpTo(2), token = %q, want %q", got, want)
	}
	if got := c.PageNumber(); got != 2 {
		t.Errorf("PageNumber after JumpTo(2) = %d, want 2", got)
	}

	// Forward to a page never fetched is refused, and leaves the cursor put.
	if c.JumpTo(9) {
		t.Error("JumpTo(9) accepted a page the cursor has no token for")
	}
	if got := c.PageNumber(); got != 2 {
		t.Errorf("a refused jump moved the cursor to page %d", got)
	}
	if c.CanJumpTo(0) || c.CanJumpTo(-1) {
		t.Error("CanJumpTo accepted a non-positive page")
	}

	// Reset drops the stack, as a filter change must.
	c.Reset()
	if c.PageNumber() != 1 || c.Visited() != 1 || c.Current() != "" {
		t.Errorf("after Reset: page=%d visited=%d token=%q", c.PageNumber(), c.Visited(), c.Current())
	}
}
