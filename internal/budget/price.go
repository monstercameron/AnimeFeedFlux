// Package budget enforces cost and usage caps on LLM calls.
//
// SchemaFlux reports zero token usage and zero cost (PLAN.md §8.1): its
// Generating[T].RunResult() leaves Usage, Cost, Attempts and Model at their
// zero values. So every number this package produces is an ESTIMATE derived
// from token counts computed ourselves at the prompt/response boundary
// (estimate.go), priced against an editable table (this file). That estimate
// is labelled as such everywhere it surfaces (§13, §12.5) — an unlabelled
// wrong number is worse than an honest approximate one.
package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Price is the per-model rate used to turn a token count into an estimated
// USD cost. Rates are USD per one million tokens, matching how providers
// publish pricing, so the table can be copy-pasted from a pricing page
// without a units conversion step.
type Price struct {
	Model         string  `json:"model"`
	InputPerMTok  float64 `json:"input_per_mtok"`
	OutputPerMTok float64 `json:"output_per_mtok"`
}

// Table is a lookup of Price by model name, editable at runtime (§12.5)
// because published prices change and a stale table silently makes every
// cost number wrong.
type Table struct {
	mu     sync.RWMutex
	prices map[string]Price
	// Default, if non-nil, prices any model not found in prices. It exists
	// so an operator can choose "estimate conservatively" over "refuse to
	// estimate" for models they haven't entered yet — but it is opt-in: by
	// default there is no fallback, and an unknown model reports known=false
	// rather than silently costing zero (which would let an unbudgeted model
	// run free).
	Default *Price `json:"default,omitempty"`
}

// NewTable returns an empty price table with no default.
func NewTable() *Table {
	return &Table{prices: make(map[string]Price)}
}

// Set inserts or replaces the price for a model.
func (t *Table) Set(p Price) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.prices == nil {
		t.prices = make(map[string]Price)
	}
	t.prices[p.Model] = p
}

// Lookup returns the price for a model and whether one is known for it
// (either an explicit entry or the table's default).
func (t *Table) Lookup(model string) (Price, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if p, ok := t.prices[model]; ok {
		return p, true
	}
	if t.Default != nil {
		return *t.Default, true
	}
	return Price{}, false
}

// Cost estimates the USD cost of a call given input and output token counts.
// known reports whether a price was found for model. When known is false,
// usd is always 0 and MUST NOT be treated as "this call costs nothing" —
// it means the cost is unknown, which is a reason to refuse the call, not a
// reason to let it through for free. Callers that need "let it run but flag
// it" must do so explicitly; Cost never makes that choice implicitly.
func (t *Table) Cost(model string, in, out int) (usd float64, known bool) {
	p, ok := t.Lookup(model)
	if !ok {
		return 0, false
	}
	usd = float64(in)/1_000_000*p.InputPerMTok + float64(out)/1_000_000*p.OutputPerMTok
	return usd, true
}

// tableJSON is the on-disk shape: a flat list rather than the internal map,
// so the file is stable to hand-edit and diff regardless of map iteration
// order.
type tableJSON struct {
	Prices  []Price `json:"prices"`
	Default *Price  `json:"default,omitempty"`
}

// MarshalJSON implements a stable, hand-editable encoding.
func (t *Table) MarshalJSON() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	tj := tableJSON{Default: t.Default}
	for _, p := range t.prices {
		tj.Prices = append(tj.Prices, p)
	}
	return json.Marshal(tj)
}

// UnmarshalJSON implements the reverse of MarshalJSON.
func (t *Table) UnmarshalJSON(data []byte) error {
	var tj tableJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prices = make(map[string]Price, len(tj.Prices))
	for _, p := range tj.Prices {
		t.prices[p.Model] = p
	}
	t.Default = tj.Default
	return nil
}

// LoadTable reads a price table from a JSON file at path.
func LoadTable(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("budget: read price table %s: %w", path, err)
	}
	t := NewTable()
	if err := json.Unmarshal(data, t); err != nil {
		return nil, fmt.Errorf("budget: parse price table %s: %w", path, err)
	}
	return t, nil
}

// Save writes the table to path as indented JSON, so §12.5's runtime editor
// has a diffable, human-readable file rather than a compact blob.
func (t *Table) Save(path string) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("budget: marshal price table: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("budget: write price table %s: %w", path, err)
	}
	return nil
}
