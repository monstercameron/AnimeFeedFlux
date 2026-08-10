package history

// Selection is pure bulk-selection state for Items' "bulk select for
// delete and restore" (PLAN.md §12.4, TODOS.md D3-19). It is keyed by
// item id rather than row index so a selection survives paging, sorting,
// or a row leaving the current filtered page without silently selecting
// the wrong row that slides into its old position.
type Selection struct {
	ids map[int64]struct{}
}

// NewSelection returns an empty selection.
func NewSelection() *Selection {
	return &Selection{ids: make(map[int64]struct{})}
}

// Select adds id to the selection.
func (s *Selection) Select(id int64) {
	s.ids[id] = struct{}{}
}

// Deselect removes id from the selection. A no-op if absent.
func (s *Selection) Deselect(id int64) {
	delete(s.ids, id)
}

// Toggle flips id's membership and reports the resulting state.
func (s *Selection) Toggle(id int64) (selected bool) {
	if s.IsSelected(id) {
		s.Deselect(id)
		return false
	}
	s.Select(id)
	return true
}

// IsSelected reports whether id is currently selected.
func (s *Selection) IsSelected(id int64) bool {
	_, ok := s.ids[id]
	return ok
}

// Clear empties the selection (e.g. after a bulk action completes, or the
// filter query changes and the previous selection no longer applies).
func (s *Selection) Clear() {
	s.ids = make(map[int64]struct{})
}

// Count returns the number of selected ids.
func (s *Selection) Count() int {
	return len(s.ids)
}

// IDs returns the selected ids. The order is unspecified; callers that
// need a stable order (e.g. for a confirmation list) should sort it.
func (s *Selection) IDs() []int64 {
	out := make([]int64, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	return out
}

// SelectAllState is the tri-state a page-level "select all" checkbox must
// render, computed purely from the selection against the ids actually
// visible on the current page — never "select every item ever", which
// would silently reach past what the operator can see and confirm.
type SelectAllState int

const (
	SelectAllNone SelectAllState = iota
	SelectAllSome
	SelectAllAll
)

// StateForVisible computes the tri-state for a "select all" control that
// only ever targets the given visible ids.
func StateForVisible(s *Selection, visibleIDs []int64) SelectAllState {
	if len(visibleIDs) == 0 {
		return SelectAllNone
	}
	selectedCount := 0
	for _, id := range visibleIDs {
		if s.IsSelected(id) {
			selectedCount++
		}
	}
	switch {
	case selectedCount == 0:
		return SelectAllNone
	case selectedCount == len(visibleIDs):
		return SelectAllAll
	default:
		return SelectAllSome
	}
}

// SetAllVisible selects or deselects exactly the visible ids, leaving any
// selection outside the current page (e.g. from a previous filter) alone
// unless clearOthers is set.
func SetAllVisible(s *Selection, visibleIDs []int64, selected bool, clearOthers bool) {
	if clearOthers {
		s.Clear()
	}
	if !selected {
		for _, id := range visibleIDs {
			s.Deselect(id)
		}
		return
	}
	for _, id := range visibleIDs {
		s.Select(id)
	}
}
