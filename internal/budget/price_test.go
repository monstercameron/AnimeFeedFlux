package budget

import (
	"path/filepath"
	"testing"
)

func TestCost_UnknownModelDoesNotCostZeroSilently(t *testing.T) {
	tbl := NewTable()
	tbl.Set(Price{Model: "gpt-4o", InputPerMTok: 5, OutputPerMTok: 15})

	usd, known := tbl.Cost("some-other-model", 1000, 1000)
	if known {
		t.Fatalf("expected known=false for a model not in the table")
	}
	if usd != 0 {
		t.Fatalf("unknown model must report usd=0 alongside known=false, got %v", usd)
	}

	// The known model prices normally, so the zero above is distinguishable
	// as "unknown", not "this table always returns zero".
	usd, known = tbl.Cost("gpt-4o", 1_000_000, 1_000_000)
	if !known {
		t.Fatalf("expected known=true for a priced model")
	}
	if usd != 20 {
		t.Fatalf("expected 5+15=20 usd for 1M in + 1M out, got %v", usd)
	}
}

func TestCost_DefaultFallbackPrices(t *testing.T) {
	tbl := NewTable()
	tbl.Default = &Price{Model: "default", InputPerMTok: 1, OutputPerMTok: 2}

	usd, known := tbl.Cost("anything", 1_000_000, 0)
	if !known {
		t.Fatalf("expected default to make every model known")
	}
	if usd != 1 {
		t.Fatalf("expected 1 usd, got %v", usd)
	}
}

func TestTable_JSONRoundTrip(t *testing.T) {
	tbl := NewTable()
	tbl.Set(Price{Model: "gpt-4o", InputPerMTok: 5, OutputPerMTok: 15})
	tbl.Set(Price{Model: "gpt-4o-mini", InputPerMTok: 0.15, OutputPerMTok: 0.6})
	tbl.Default = &Price{Model: "fallback", InputPerMTok: 10, OutputPerMTok: 30}

	dir := t.TempDir()
	path := filepath.Join(dir, "prices.json")

	if err := tbl.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadTable(path)
	if err != nil {
		t.Fatalf("LoadTable: %v", err)
	}

	for _, model := range []string{"gpt-4o", "gpt-4o-mini"} {
		want, _ := tbl.Lookup(model)
		got, ok := loaded.Lookup(model)
		if !ok {
			t.Fatalf("loaded table missing model %s", model)
		}
		if got != want {
			t.Fatalf("model %s: got %+v, want %+v", model, got, want)
		}
	}

	if loaded.Default == nil || *loaded.Default != *tbl.Default {
		t.Fatalf("default did not round-trip: got %+v", loaded.Default)
	}
}

func TestTable_LoadMissingFile(t *testing.T) {
	if _, err := LoadTable(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("expected an error loading a nonexistent file")
	}
}
