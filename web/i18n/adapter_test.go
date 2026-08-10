package i18n

import (
	"strings"
	"testing"
	"time"
)

// TestNewLabelResolverSatisfiesUIT is a compile-time + behavioral check
// that NewLabelResolver's return value is assignable to web/ui's T type
// (`func(key string, args ...any) string`) without importing web/ui (this
// package deliberately does not — see adapter.go's doc comment). A local
// type alias with the identical underlying signature stands in for it: if
// this line stops compiling, NewLabelResolver's signature drifted from the
// contract web/ui/labels.go declares.
type uiTShape func(key string, args ...any) string

func TestNewLabelResolverSatisfiesUIT(t *testing.T) {
	b := NewBundle()
	var fn uiTShape = NewLabelResolver(b)

	got := fn(KeyActionSave)
	if got != "Save" {
		t.Errorf("NewLabelResolver(b)(%q) = %q, want %q", KeyActionSave, got, "Save")
	}
}

// TestNewLabelResolverInterpolatesPositionalArgs verifies the arg1/arg2/...
// convention documented on keys_common.go: ui.T's positional args map onto
// {arg1}, {arg2}, ... in call order.
func TestNewLabelResolverInterpolatesPositionalArgs(t *testing.T) {
	b := NewBundle()
	fn := NewLabelResolver(b)

	got := fn(KeyConfirmTypePhrase, "delete-my-feed")
	want := "Type delete-my-feed to confirm."
	if got != want {
		t.Errorf("NewLabelResolver interpolation: got %q, want %q", got, want)
	}

	got2 := fn(KeyKebabActionsFor, "my-feed")
	if !strings.Contains(got2, "my-feed") {
		t.Errorf("NewLabelResolver(%q, %q) = %q, does not contain the interpolated arg", KeyKebabActionsFor, "my-feed", got2)
	}
}

// TestNewLabelResolverMissingKeyStillLogs guards D6-07 through the
// adapter specifically: a key with no common.* entry must still render
// visibly (never empty), same as a direct Bundle.Translate call.
func TestNewLabelResolverMissingKeyStillLogs(t *testing.T) {
	b := NewBundle()
	fn := NewLabelResolver(b)
	got := fn("action.thisDoesNotExist")
	want := "common.action.thisDoesNotExist"
	if got != want {
		t.Errorf("missing key via adapter: got %q, want %q", got, want)
	}
}

// TestFormatHelpers is a light smoke test for the locale-aware formatting
// helpers (D6-08) — not exhaustive locale coverage (this app ships only
// "en", per DefaultLocale's doc comment), just "does not panic, produces
// plausible non-fmt.Sprintf-shaped output".
func TestFormatHelpers(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	ts := time.Date(2026, time.March, 4, 15, 30, 0, 0, ny)

	if got := FormatDateTime(ts, "America/New_York"); got == "" {
		t.Error("FormatDateTime returned empty string")
	}
	if got := FormatDateTime(ts, ""); got == "" {
		t.Error("FormatDateTime with empty tz (UTC fallback) returned empty string")
	}
	if got := FormatDateTime(ts, "not-a-real-zone"); got == "" {
		t.Error("FormatDateTime with an invalid tz should fall back to UTC, not panic/empty")
	}

	if got := FormatRelativeTime(ts, ts.Add(2*time.Hour)); !strings.Contains(got, "2") {
		t.Errorf("FormatRelativeTime = %q, want it to mention 2 hours", got)
	}

	if got := FormatCurrencyUSD(1234.5); !strings.HasPrefix(got, "$") {
		t.Errorf("FormatCurrencyUSD = %q, want a $ prefix", got)
	}

	if got := FormatCount(1234); got == "" {
		t.Error("FormatCount returned empty string")
	}

	if got := FormatByteSize(1536); !strings.Contains(got, "KB") {
		t.Errorf("FormatByteSize(1536) = %q, want it scaled to KB", got)
	}
	if got := FormatByteSize(-5); !strings.Contains(got, "0") {
		t.Errorf("FormatByteSize(-5) = %q, want negative n clamped to zero", got)
	}
	if got := FormatByteSize(0); !strings.Contains(got, "B") {
		t.Errorf("FormatByteSize(0) = %q, want bytes unit", got)
	}

	if got := FormatPercent(0.755, 1); got != "75.5%" {
		t.Errorf("FormatPercent(0.755, 1) = %q, want %q", got, "75.5%")
	}
}
