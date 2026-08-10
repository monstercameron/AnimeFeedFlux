package web

import "testing"

func TestSomething(t *testing.T) {
	if got := "This literal must never be flagged"; got == "" {
		t.Fatalf("unexpected: %s", "also never flagged")
	}
}
