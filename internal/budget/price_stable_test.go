package budget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The on-disk price table is a flat list precisely so an operator can hand-
// edit it and so a change to it reviews as a change to it (§12.5). Building
// that list by ranging the internal map gave a different byte sequence every
// call — Go randomizes map iteration — so every Save rewrote the file into a
// new order and produced a full-file diff containing no actual edit.
func TestTableMarshalIsByteStable(t *testing.T) {
	tb := NewTable()
	for _, m := range []string{"gpt-5", "claude-opus-5", "gemini-3", "llama-4", "mistral-2", "qwen-3"} {
		tb.Set(Price{Model: m, InputPerMTok: 1.25, OutputPerMTok: 10})
	}

	first, err := json.Marshal(tb)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := range 50 {
		got, err := json.Marshal(tb)
		if err != nil {
			t.Fatalf("marshal #%d: %v", i, err)
		}
		if string(got) != string(first) {
			t.Fatalf("marshal #%d is not byte-identical to the first — every Save would diff the whole file:\nfirst = %s\ngot   = %s",
				i, first, got)
		}
	}

	// Stable AND sorted: an operator looking for a model should find it where
	// alphabetical order says it is.
	var decoded tableJSON
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.IsSortedFunc(decoded.Prices, func(a, b Price) int {
		if a.Model < b.Model {
			return -1
		} else if a.Model > b.Model {
			return 1
		}
		return 0
	}) {
		t.Errorf("prices are not sorted by model: %+v", decoded.Prices)
	}
}

// TestTableSaveLoadRoundTripsAndIsStable exercises the pair through a real
// file: what Save wrote must load back to the same table, and saving that
// loaded table again must produce the identical bytes.
func TestTableSaveLoadRoundTripsAndIsStable(t *testing.T) {
	tb := NewTable()
	tb.Set(Price{Model: "zeta", InputPerMTok: 3, OutputPerMTok: 6})
	tb.Set(Price{Model: "alpha", InputPerMTok: 1, OutputPerMTok: 2})
	tb.Default = &Price{Model: "", InputPerMTok: 9, OutputPerMTok: 9}

	path := filepath.Join(t.TempDir(), "prices.json")
	if err := tb.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadTable(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	for _, model := range []string{"alpha", "zeta"} {
		want, _ := tb.Lookup(model)
		got, ok := loaded.Lookup(model)
		if !ok || got != want {
			t.Errorf("after round trip %q = %+v (ok=%v), want %+v", model, got, ok, want)
		}
	}
	// The default must survive, or an operator's "price unknown models
	// conservatively" choice silently becomes "refuse to estimate".
	if got, ok := loaded.Lookup("never-entered"); !ok || got.InputPerMTok != 9 {
		t.Errorf("default lost across the round trip: %+v (ok=%v)", got, ok)
	}

	second := filepath.Join(t.TempDir(), "prices.json")
	if err := loaded.Save(second); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	a, b := mustRead(t, path), mustRead(t, second)
	if a != b {
		t.Errorf("save -> load -> save is not byte-identical:\n%s\n---\n%s", a, b)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
