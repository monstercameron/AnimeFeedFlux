package ui

import "testing"

func TestConfirmMatches(t *testing.T) {
	tests := []struct {
		name     string
		typed    string
		required string
		want     bool
	}{
		{"exact match", "delete-my-feed", "delete-my-feed", true},
		{"trailing newline from paste is forgiven", "delete-my-feed\n", "delete-my-feed", true},
		{"surrounding spaces are forgiven", "  delete-my-feed  ", "delete-my-feed", true},
		{"case mismatch fails", "Delete-My-Feed", "delete-my-feed", false},
		{"partial match fails", "delete-my", "delete-my-feed", false},
		{"empty typed fails", "", "delete-my-feed", false},
		{"internal whitespace is NOT collapsed", "delete  my feed", "delete my feed", false},
		{"empty required matches only empty/whitespace typed", "", "", true},
		{"whitespace-only typed against empty required matches", "   ", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConfirmMatches(tc.typed, tc.required); got != tc.want {
				t.Errorf("ConfirmMatches(%q, %q) = %v, want %v", tc.typed, tc.required, got, tc.want)
			}
		})
	}
}
