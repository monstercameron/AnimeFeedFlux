package ui

import "testing"

func TestSelectListState_Precedence(t *testing.T) {
	tests := []struct {
		name string
		in   ListStateInput
		want ListState
	}{
		{"nothing set is populated", ListStateInput{ItemCount: 3}, StatePopulated},
		{"zero items is empty", ListStateInput{ItemCount: 0}, StateEmpty},
		{"loading beats empty", ListStateInput{Loading: true, ItemCount: 0}, StateLoading},
		{"error beats loading", ListStateInput{ErrorKey: "e", Loading: true}, StateError},
		{"disconnected beats error", ListStateInput{Disconnected: true, ErrorKey: "e"}, StateDisconnected},
		{"disconnected beats populated", ListStateInput{Disconnected: true, ItemCount: 5}, StateDisconnected},
		{"disabled beats disconnected", ListStateInput{DisabledReasonKey: "k", Disconnected: true}, StateDisabledWithReason},
		{"disabled beats everything", ListStateInput{
			DisabledReasonKey: "k", Disconnected: true, ErrorKey: "e", Loading: true, ItemCount: 5,
		}, StateDisabledWithReason},
		{"loading beats populated-looking count", ListStateInput{Loading: true, ItemCount: 9}, StateLoading},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectListState(tc.in); got != tc.want {
				t.Errorf("SelectListState(%+v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestListState_String(t *testing.T) {
	states := []ListState{
		StateLoading, StateEmpty, StatePopulated, StateError, StateDisabledWithReason, StateDisconnected,
	}
	seen := map[string]bool{}
	for _, s := range states {
		str := s.String()
		if str == "" || str == "unknown" {
			t.Errorf("state %d has no String()", s)
		}
		if seen[str] {
			t.Errorf("duplicate String() %q", str)
		}
		seen[str] = true
	}
	if got := ListState(99).String(); got != "unknown" {
		t.Errorf("out-of-range ListState.String() = %q, want %q", got, "unknown")
	}
}
