package history

import "testing"

func TestSelectionToggleAndCount(t *testing.T) {
	s := NewSelection()
	if s.Count() != 0 {
		t.Fatalf("new selection count = %d, want 0", s.Count())
	}
	if !s.Toggle(1) {
		t.Fatalf("Toggle(1) first call should select")
	}
	if !s.IsSelected(1) {
		t.Fatalf("expected 1 selected")
	}
	if s.Count() != 1 {
		t.Fatalf("count = %d, want 1", s.Count())
	}
	if s.Toggle(1) {
		t.Fatalf("Toggle(1) second call should deselect")
	}
	if s.IsSelected(1) {
		t.Fatalf("expected 1 deselected")
	}
	if s.Count() != 0 {
		t.Fatalf("count = %d, want 0", s.Count())
	}
}

func TestSelectionClear(t *testing.T) {
	s := NewSelection()
	s.Select(1)
	s.Select(2)
	s.Clear()
	if s.Count() != 0 {
		t.Fatalf("count after Clear = %d, want 0", s.Count())
	}
}

func TestStateForVisible(t *testing.T) {
	s := NewSelection()
	visible := []int64{1, 2, 3}

	if got := StateForVisible(s, visible); got != SelectAllNone {
		t.Fatalf("empty selection = %v, want SelectAllNone", got)
	}

	s.Select(1)
	if got := StateForVisible(s, visible); got != SelectAllSome {
		t.Fatalf("partial selection = %v, want SelectAllSome", got)
	}

	s.Select(2)
	s.Select(3)
	if got := StateForVisible(s, visible); got != SelectAllAll {
		t.Fatalf("full selection = %v, want SelectAllAll", got)
	}

	// A selection outside the visible page must not count toward it.
	s2 := NewSelection()
	s2.Select(99)
	if got := StateForVisible(s2, visible); got != SelectAllNone {
		t.Fatalf("off-page selection = %v, want SelectAllNone", got)
	}

	if got := StateForVisible(s2, nil); got != SelectAllNone {
		t.Fatalf("no visible ids = %v, want SelectAllNone", got)
	}
}

func TestSetAllVisible(t *testing.T) {
	s := NewSelection()
	s.Select(99) // pre-existing, off-page

	SetAllVisible(s, []int64{1, 2, 3}, true, false)
	for _, id := range []int64{1, 2, 3} {
		if !s.IsSelected(id) {
			t.Fatalf("expected %d selected after select-all", id)
		}
	}
	if !s.IsSelected(99) {
		t.Fatalf("expected off-page selection preserved when clearOthers=false")
	}

	SetAllVisible(s, []int64{1, 2, 3}, false, false)
	for _, id := range []int64{1, 2, 3} {
		if s.IsSelected(id) {
			t.Fatalf("expected %d deselected after deselect-all", id)
		}
	}
	if !s.IsSelected(99) {
		t.Fatalf("expected off-page selection preserved on deselect-all too")
	}

	SetAllVisible(s, []int64{1, 2}, true, true)
	if s.IsSelected(99) {
		t.Fatalf("expected clearOthers=true to drop the off-page selection")
	}
	if s.Count() != 2 {
		t.Fatalf("count = %d, want 2", s.Count())
	}
}

func TestSelectionIDs(t *testing.T) {
	s := NewSelection()
	s.Select(1)
	s.Select(2)
	ids := s.IDs()
	if len(ids) != 2 {
		t.Fatalf("len(IDs()) = %d, want 2", len(ids))
	}
}
