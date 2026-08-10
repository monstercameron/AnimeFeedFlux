//go:build js && wasm

package settings

import (
	"strconv"

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

func updatePriceCell(priceTable ui.State[[]*affv1.PriceEntry], index int, raw string, isIn bool) {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return
	}
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
