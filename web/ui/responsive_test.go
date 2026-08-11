package ui

import "testing"

func TestSelectBreakpoint(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  Breakpoint
	}{
		{"iPhone SE width is narrow", 375, BreakpointNarrow},
		{"320px (smallest supported) is narrow", 320, BreakpointNarrow},
		{"zero width is narrow", 0, BreakpointNarrow},
		{"negative width is narrow (defensive)", -1, BreakpointNarrow},
		{"exactly the threshold is narrow (inclusive)", NarrowMaxWidth, BreakpointNarrow},
		{"one px over the threshold is regular", NarrowMaxWidth + 1, BreakpointRegular},
		{"desktop width is regular", 1440, BreakpointRegular},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectBreakpoint(tc.width); got != tc.want {
				t.Errorf("SelectBreakpoint(%d) = %v, want %v", tc.width, got, tc.want)
			}
		})
	}
}
