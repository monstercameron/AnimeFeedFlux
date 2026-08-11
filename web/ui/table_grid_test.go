package ui

import (
	"math"
	"strings"
	"testing"
)

// gridTemplate is what makes a VirtualTable's header and every body row agree
// on where each column starts. It is pure string logic, and it is emitted
// verbatim into a stylesheet, so both what it produces and what it refuses to
// produce matter.

func TestGridTemplateWeightsColumns(t *testing.T) {
	got := gridTemplate([]TableColumn{
		{ID: "device", Weight: 4},
		{ID: "ip", Weight: 2},
		{ID: "current", Weight: 1.2},
		{ID: "unset"}, // zero means 1
	})
	want := "minmax(0, 4fr) minmax(0, 2fr) minmax(0, 1.2fr) minmax(0, 1fr)"
	if got != want {
		t.Errorf("gridTemplate = %q, want %q", got, want)
	}
}

// Every column equal is what tables did before Weight existed, so a caller
// that sets no weights must be completely unaffected.
func TestGridTemplateUnweightedIsAllEqual(t *testing.T) {
	got := gridTemplate([]TableColumn{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	if want := "minmax(0, 1fr) minmax(0, 1fr) minmax(0, 1fr)"; got != want {
		t.Errorf("gridTemplate = %q, want %q", got, want)
	}
}

// A weight that cannot be rendered as a number must not reach the stylesheet.
// grid-template-columns is a single declaration: one malformed track makes the
// browser drop the whole thing, so every column loses its alignment, not just
// the offending one.
func TestColumnFractionNeverEmitsAnInvalidTrack(t *testing.T) {
	for _, w := range []float64{
		0, -1, -0.0001,
		math.NaN(), math.Inf(1), math.Inf(-1),
	} {
		if got := columnFraction(w); got != "1" {
			t.Errorf("columnFraction(%v) = %q, want \"1\"", w, got)
		}
	}

	// And the whole template stays parseable for those inputs.
	tpl := gridTemplate([]TableColumn{{Weight: math.NaN()}, {Weight: math.Inf(1)}, {Weight: -3}})
	for _, bad := range []string{"NaN", "Inf", "-"} {
		if strings.Contains(tpl, bad) {
			t.Errorf("gridTemplate emitted %q in %q", bad, tpl)
		}
	}
}

func TestGridTemplateEmptyColumns(t *testing.T) {
	if got := gridTemplate(nil); got != "1fr" {
		t.Errorf("gridTemplate(nil) = %q, want \"1fr\"", got)
	}
}
