package history

import (
	"strings"
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
)

// The mapping is only useful if it is complete: a reason with no entry falls
// back to the raw identifier, which is exactly the state A8-30 exists to end.
// This walks the server's own list rather than a copy of it.
func TestEveryGenerateReasonHasALabel(t *testing.T) {
	for _, reason := range generate.AllRejectReasons() {
		key, ok := RejectReasonKey(reason)
		if !ok {
			t.Errorf("reject reason %q has no label — it would render to an operator as a raw identifier", reason)
			continue
		}
		if want := "history.runs.reject." + reason; key != want {
			t.Errorf("reject reason %q maps to key %q, want %q", reason, key, want)
		}
	}
}

// No entry may point at a reason the server can no longer emit: a dead label
// is a sentence nobody will ever see, kept alive by nothing.
func TestNoLabelDescribesAReasonThatNoLongerExists(t *testing.T) {
	live := map[string]bool{}
	for _, reason := range generate.AllRejectReasons() {
		live[reason] = true
	}
	for reason := range rejectReasonKeys {
		if !live[reason] {
			t.Errorf("there is a label for %q, which internal/generate no longer emits", reason)
		}
	}
}

func TestUnknownReasonHasNoKey(t *testing.T) {
	if key, ok := RejectReasonKey("a_reason_from_a_newer_server"); ok {
		t.Fatalf("an unknown reason returned key %q; the caller must be told to print the identifier instead", key)
	}
}

// Guards the key namespace: these strings are also written out by hand in
// web/i18n, which cannot import this package, so a typo here is a key that
// resolves to itself on screen.
func TestLabelKeysShareOnePrefix(t *testing.T) {
	for reason, key := range rejectReasonKeys {
		if !strings.HasPrefix(key, "history.runs.reject.") {
			t.Errorf("reason %q has key %q, which is outside the history.runs.reject. namespace", reason, key)
		}
	}
}
