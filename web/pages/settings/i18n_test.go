package settings

import (
	"testing"
	"time"
)

func TestFallbackTranslator_MissingKeyRendersKeyItself(t *testing.T) {
	// Mirrors D6-07's rule for the real catalogue: a missing key renders
	// the key, never an empty string.
	got := DefaultTranslator.T("settings.security.title")
	if got != "settings.security.title" {
		t.Errorf("T() = %q, want the bare key", got)
	}
}

func TestFallbackTranslator_WithArgs(t *testing.T) {
	got := DefaultTranslator.T("settings.security.passwordPolicy.hint", 15, 128)
	if got == "" {
		t.Error("T() with args must not be empty")
	}
}

func TestFallbackFormatters_ByteSizeUsesScale(t *testing.T) {
	got := DefaultFormatters.ByteSize(1024)
	if got != "1 KB" {
		t.Errorf("ByteSize(1024) = %q, want %q", got, "1 KB")
	}
}

func TestFallbackFormatters_RelativeTimeMonotone(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cases := []time.Time{
		now,
		now.Add(-30 * time.Second),
		now.Add(-5 * time.Minute),
		now.Add(-3 * time.Hour),
		now.Add(-2 * 24 * time.Hour),
	}
	for _, ts := range cases {
		if got := DefaultFormatters.RelativeTime(ts, now); got == "" {
			t.Errorf("RelativeTime(%v) returned empty string", ts)
		}
	}
}
