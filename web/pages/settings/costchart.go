//go:build js && wasm

package settings

import (
	"strconv"

	h "github.com/monstercameron/GoWebComponents/v5/html/shorthand"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// costchart.go draws daily provider spend.
//
// # Why a column chart and not a line
//
// Daily spend is a set of discrete totals, not a continuous quantity
// sampled once a day. A line implies a value existed between Tuesday and
// Wednesday and invites the eye to read the slope as a rate; columns say
// "this is what Tuesday cost", which is the actual question (PLAN.md §13).
// Days with no runs are drawn as empty slots rather than skipped, because a
// GAP is the thing worth seeing — §15 calls a feed that silently stops "the
// real failure mode", and a line drawn across the gap hides exactly that.
//
// # Color
//
// One series, so no categorical palette and no legend — the panel heading
// names it. The bars are the accent role; validated against both theme
// surfaces (light #2A5FD8 on #F3F6F1, dark #5B8CFF on #1A2126) for the
// lightness band, chroma floor and ≥3:1 contrast. Text stays in the text
// roles and never takes the series colour.
//
// # The hero number carries the answer
//
// §13's real question is "am I within the ceiling", which is a number, not a
// shape. The window total is stated at full size above the plot; the columns
// are there to answer the follow-up — when did it change, and did anything
// stop.

// chartHeight is the plot area in SVG user units. The viewBox is unitless
// and the element scales to its container, so this is a proportion, not a
// pixel commitment.
const (
	chartHeight   = 120.0
	chartTopPad   = 6.0
	chartBarGap   = 2.0 // the surface gap the mark spec calls for between fills
	chartRadius   = 2.0
	chartBaseline = 1.0 // a hairline so an all-zero window still reads as a chart
)

// renderCostChart draws the buckets as columns. An empty or all-zero window
// renders the frame and says so, rather than dividing by a zero maximum and
// drawing nothing.
func renderCostChart(buckets []*affv1.CostBucket, hovered int, onHover func(int)) ui.Node {
	if len(buckets) == 0 {
		return h.P(h.ClassStr("af-cost__empty"), h.Text(t("settings.provider.cost.empty")))
	}

	maxUSD := 0.0
	for _, b := range buckets {
		if b.GetUsd() > maxUSD {
			maxUSD = b.GetUsd()
		}
	}

	n := float64(len(buckets))
	// One unit of width per bucket; the bar takes that minus the gap, which
	// is what keeps adjacent fills from reading as one block.
	slot := 100.0 / n
	barW := slot - chartBarGap
	if barW < 0.5 {
		barW = slot * 0.7 // very long windows: keep a visible gap proportionally
	}

	bars := make([]any, 0, len(buckets)+2)
	bars = append(bars,
		// The viewBox must include the baseline's own height, or the
		// hairline is drawn one unit past the bottom edge and clipped —
		// which is what happened: an all-but-empty window rendered as blank
		// space with no chart under it at all.
		h.Attr("viewBox", "0 0 100 "+fmtNum(chartTopPad+chartHeight+chartBaseline)),
		h.Attr("preserveAspectRatio", "none"),
		h.ClassStr("af-cost__svg"),
		h.Role("img"),
		h.Aria("label", t("settings.provider.cost.chartLabel",
			strconv.Itoa(len(buckets)), fmts().Currency(totalOf(buckets)))),
	)

	for i, b := range buckets {
		x := float64(i) * slot
		height := 0.0
		if maxUSD > 0 {
			height = (b.GetUsd() / maxUSD) * chartHeight
		}
		// A day with real but tiny spend must not vanish: floor a non-zero
		// value at the corner radius so it still reads as a bar rather than
		// as an empty day, which would be a different fact entirely.
		if b.GetUsd() > 0 && height < chartRadius*2 {
			height = chartRadius * 2
		}
		y := chartTopPad + chartHeight - height

		cls := "af-cost__bar"
		if i == hovered {
			cls += " af-cost__bar--hovered"
		}
		idx := i
		bars = append(bars, h.Tag("rect",
			h.Attr("x", fmtNum(x)),
			h.Attr("y", fmtNum(y)),
			h.Attr("width", fmtNum(barW)),
			h.Attr("height", fmtNum(height)),
			h.Attr("rx", fmtNum(chartRadius)),
			h.ClassStr(cls),
			h.OnMouseEnter(ui.UseEvent(func() { onHover(idx) })),
			// The native tooltip is the baseline hover layer: it needs no
			// JS, survives with the pointer, and is what a keyboard or
			// screen-reader user gets from the <title> element itself.
			h.Tag("title", nil, h.Text(bucketLabel(b))),
		))
	}

	// The baseline. Drawn after the bars so it sits on top of a zero-height
	// day and the row still reads as a chart when nothing was spent at all.
	bars = append(bars, h.Tag("rect",
		h.Attr("x", "0"),
		h.Attr("y", fmtNum(chartTopPad+chartHeight)),
		h.Attr("width", "100"),
		h.Attr("height", fmtNum(chartBaseline)),
		h.ClassStr("af-cost__baseline"),
	))

	return h.Tag("svg", bars...)
}

// bucketLabel is one day's readout: the date, what it cost, and how many
// runs produced that. Runs are included because "$0.00 across 3 runs" and
// "$0.00 across 0 runs" are completely different situations — the second is
// a feed that stopped.
func bucketLabel(b *affv1.CostBucket) string {
	return t("settings.provider.cost.bucket",
		b.GetDate(),
		fmts().Currency(b.GetUsd()),
		strconv.FormatInt(b.GetRuns(), 10),
	)
}

func totalOf(buckets []*affv1.CostBucket) float64 {
	var sum float64
	for _, b := range buckets {
		sum += b.GetUsd()
	}
	return sum
}

// fmtNum formats an SVG coordinate compactly — these are geometry, not
// values a person reads, so they do not go through the locale formatters
// PLAN.md §12.6 requires for displayed numbers.
func fmtNum(f float64) string {
	return strconv.FormatFloat(f, 'f', 2, 64)
}
