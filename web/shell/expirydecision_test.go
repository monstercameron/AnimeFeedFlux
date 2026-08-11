package shell

import "testing"

func TestShouldHoldExpiry(t *testing.T) {
	cases := []struct {
		name       string
		dirtyCheck func() bool
		want       bool
	}{
		{"nil predicate never holds", nil, false},
		{"predicate reports clean", func() bool { return false }, false},
		{"predicate reports dirty", func() bool { return true }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldHoldExpiry(c.dirtyCheck); got != c.want {
				t.Errorf("ShouldHoldExpiry(...) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestShouldHoldExpiryCallsPredicateAtMostOnce(t *testing.T) {
	calls := 0
	predicate := func() bool {
		calls++
		return true
	}
	if !ShouldHoldExpiry(predicate) {
		t.Fatal("expected hold=true")
	}
	if calls != 1 {
		t.Errorf("predicate called %d times, want exactly 1", calls)
	}
}
