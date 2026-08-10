package history

import (
	"testing"
	"time"
)

func TestValidatePublishedAt(t *testing.T) {
	newest := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	t.Run("after newest is always allowed", func(t *testing.T) {
		proposed := newest.Add(time.Second)
		got := ValidatePublishedAt(proposed, newest, false)
		if got.Blocked || got.WarnKey != "" {
			t.Fatalf("got %+v, want no block and no warning", got)
		}
	})

	t.Run("equal to newest is blocked without override", func(t *testing.T) {
		got := ValidatePublishedAt(newest, newest, false)
		if !got.Blocked {
			t.Fatalf("got %+v, want Blocked=true", got)
		}
	})

	t.Run("before newest is blocked without override", func(t *testing.T) {
		proposed := newest.Add(-time.Hour)
		got := ValidatePublishedAt(proposed, newest, false)
		if !got.Blocked {
			t.Fatalf("got %+v, want Blocked=true", got)
		}
	})

	t.Run("before newest with override proceeds but warns loudly", func(t *testing.T) {
		proposed := newest.Add(-time.Hour)
		got := ValidatePublishedAt(proposed, newest, true)
		if got.Blocked {
			t.Fatalf("got %+v, want Blocked=false when overridden", got)
		}
		if got.WarnKey == "" {
			t.Fatalf("got %+v, want a non-empty WarnKey on override", got)
		}
	})

	t.Run("equal to newest with override still warns", func(t *testing.T) {
		got := ValidatePublishedAt(newest, newest, true)
		if got.Blocked || got.WarnKey == "" {
			t.Fatalf("got %+v, want Blocked=false and WarnKey set", got)
		}
	})
}
