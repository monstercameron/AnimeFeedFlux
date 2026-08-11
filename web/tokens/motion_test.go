package tokens

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/css"
)

// TestReducedMotion_HonouredAtTokenLayer is the "while you are here" check
// from D5-04: prefers-reduced-motion must be honoured once, here, for every
// motion token — not left as a per-component opt-in that the next component
// forgets. Every duration a component can reach (DurationFast/Base/Slow, via
// tokens.Duration) must collapse under the media query without that
// component doing anything itself.
func TestReducedMotion_HonouredAtTokenLayer(t *testing.T) {
	css.Reset()
	Emit()
	out := css.Harvest()

	if !strings.Contains(out, "@media (prefers-reduced-motion:reduce)") {
		t.Fatalf("expected a prefers-reduced-motion:reduce block in emitted CSS, got:\n%s", out)
	}

	mediaIdx := strings.Index(out, "@media (prefers-reduced-motion:reduce)")
	block := out[mediaIdx:]
	// Scope check: the override must land on :root (matching where Emit
	// declares the tokens), not on some component class — a selector other
	// than :root here would mean this is a per-component opt-in in disguise.
	if !strings.Contains(block, ":root") {
		t.Fatalf("prefers-reduced-motion block is not scoped to :root, got:\n%s", block)
	}

	for _, name := range []string{DurationFast, DurationBase, DurationSlow} {
		if !strings.Contains(block, "--"+name+":0.01ms") {
			t.Errorf("prefers-reduced-motion block does not neutralize --%s, block:\n%s", name, block)
		}
	}
}

// TestReducedMotion_DoesNotAlterDefaultDurations confirms the override is
// additive (scoped inside @media) rather than replacing the unconditional
// :root durations components read the rest of the time.
func TestReducedMotion_DoesNotAlterDefaultDurations(t *testing.T) {
	css.Reset()
	Emit()
	out := css.Harvest()

	mediaIdx := strings.Index(out, "@media (prefers-reduced-motion:reduce)")
	if mediaIdx < 0 {
		t.Fatalf("expected a prefers-reduced-motion block, got:\n%s", out)
	}
	unconditional := out[:mediaIdx]
	for name, want := range map[string]string{
		DurationFast: "--" + DurationFast + ":120ms",
		DurationBase: "--" + DurationBase + ":200ms",
		DurationSlow: "--" + DurationSlow + ":280ms",
	} {
		if !strings.Contains(unconditional, want) {
			t.Errorf("unconditional :root block missing default %s (%s), got:\n%s", name, want, unconditional)
		}
	}
}
