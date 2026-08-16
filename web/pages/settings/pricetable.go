//go:build js && wasm

package settings

import (
	"math"
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// floatToStr renders a price-table cell's float64 for a controlled
// <input type="number">'s Value prop. Kept separate from Formatters'
// Currency (which is for DISPLAY-only cost figures elsewhere on this
// page) because a controlled numeric input's Value must round-trip
// through strconv.ParseFloat exactly, which a locale-grouped currency
// string ("$1,234.5000") would not.
func floatToStr(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// perMillionStr renders a stored per-1K rate as the per-1M value the table
// displays. The app standardized on $ per 1M tokens (operator directive
// 2026-08-15) — the published unit every provider quotes — but the WIRE and
// STORED unit remains per-1K (PriceEntry.usd_per_1k_tokens_*: the field
// name is the unit, and the settings row persists as that proto's JSON, so
// changing the stored unit would silently re-scale every existing
// deployment's rates by 1000×). The conversion lives entirely at this
// display boundary, paired with updatePriceCell's ÷1000 on the way in.
// Rounded to 9 decimals so ×1000 float noise (0.00015 → 0.15000000000000002)
// never reaches the input.
func perMillionStr(vPer1K float64) string {
	return strconv.FormatFloat(math.Round(vPer1K*1000*1e9)/1e9, 'f', -1, 64)
}

// updatePriceIn/updatePriceOut apply one edited cell of the price table
// back into priceTable's state without mutating the slice or its
// elements in place (GWC discipline: controlled inputs are re-rendered
// from state, so state itself must change identity for the new value to
// stick — the "controlled inputs get clobbered on re-render unless held
// in state" trap generalizes to "and that state must actually be
// replaced, not mutated, or the framework has nothing to diff against").
func updatePriceIn(priceTable ui.State[[]*affv1.PriceEntry], index int, raw string) {
	updatePriceCell(priceTable, index, raw, true)
}

func updatePriceOut(priceTable ui.State[[]*affv1.PriceEntry], index int, raw string) {
	updatePriceCell(priceTable, index, raw, false)
}

// priceProblemText turns a validation problem into the sentence shown above
// the table, naming the row so the operator does not have to hunt for it.
func priceProblemText(t func(string, ...any) string, p PriceTableProblem) string {
	row := strconv.Itoa(p.Index + 1)
	switch p.Reason {
	case PriceProblemEmptyModel:
		return t("settings.provider.priceTable.err.emptyModel", row)
	case PriceProblemDuplicate:
		return t("settings.provider.priceTable.err.duplicate", row)
	default:
		return t("settings.provider.priceTable.err.negative", row)
	}
}

// removePriceRow deletes one rate. Same replace-not-mutate discipline as
// updatePriceCell. Until this existed a rate could be added but never
// removed — a retired model's row could only be repurposed by renaming it.
func removePriceRow(priceTable ui.State[[]*affv1.PriceEntry], index int) {
	current := priceTable.Get()
	if index < 0 || index >= len(current) {
		return
	}
	next := make([]*affv1.PriceEntry, 0, len(current)-1)
	next = append(next, current[:index]...)
	next = append(next, current[index+1:]...)
	priceTable.Set(next)
}

// updatePriceModel renames the model a rate applies to.
func updatePriceModel(priceTable ui.State[[]*affv1.PriceEntry], index int, raw string) {
	current := priceTable.Get()
	if index < 0 || index >= len(current) {
		return
	}
	next := make([]*affv1.PriceEntry, len(current))
	copy(next, current)
	old := next[index]
	next[index] = &affv1.PriceEntry{
		Model:              raw,
		UsdPer_1KTokensIn:  old.GetUsdPer_1KTokensIn(),
		UsdPer_1KTokensOut: old.GetUsdPer_1KTokensOut(),
	}
	priceTable.Set(next)
}

// PriceTableProblem reports why a price table cannot be saved.
//
// Validated client-side because the server accepted anything: a negative
// rate, two rows for the same model, or a rate attached to no model at all.
// The first two produce a cost model that is quietly wrong; the third
// produces a row that can never match anything.
type PriceTableProblem struct {
	Index  int
	Reason string // one of the priceProblem* constants
}

const (
	PriceProblemEmptyModel = "empty_model"
	PriceProblemDuplicate  = "duplicate_model"
	PriceProblemNegative   = "negative_rate"
)

// ValidatePriceTable returns every problem in the table, in row order.
func ValidatePriceTable(entries []*affv1.PriceEntry) []PriceTableProblem {
	var problems []PriceTableProblem
	seen := make(map[string]int, len(entries))
	for i, e := range entries {
		model := strings.TrimSpace(e.GetModel())
		switch {
		case model == "":
			problems = append(problems, PriceTableProblem{Index: i, Reason: PriceProblemEmptyModel})
		default:
			if _, dup := seen[model]; dup {
				problems = append(problems, PriceTableProblem{Index: i, Reason: PriceProblemDuplicate})
			} else {
				seen[model] = i
			}
		}
		if e.GetUsdPer_1KTokensIn() < 0 || e.GetUsdPer_1KTokensOut() < 0 {
			problems = append(problems, PriceTableProblem{Index: i, Reason: PriceProblemNegative})
		}
	}
	return problems
}

func updatePriceCell(priceTable ui.State[[]*affv1.PriceEntry], index int, raw string, isIn bool) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return
	}
	// The input is $ per 1M tokens (see perMillionStr); stored is per 1K.
	v /= 1000
	current := priceTable.Get()
	if index < 0 || index >= len(current) {
		return
	}
	next := make([]*affv1.PriceEntry, len(current))
	copy(next, current)
	old := next[index]
	updated := &affv1.PriceEntry{
		Model:              old.GetModel(),
		UsdPer_1KTokensIn:  old.GetUsdPer_1KTokensIn(),
		UsdPer_1KTokensOut: old.GetUsdPer_1KTokensOut(),
	}
	if isIn {
		updated.UsdPer_1KTokensIn = v
	} else {
		updated.UsdPer_1KTokensOut = v
	}
	next[index] = updated
	priceTable.Set(next)
}
