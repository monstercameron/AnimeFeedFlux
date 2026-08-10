package i18nlint

import (
	"reflect"
	"testing"
)

func TestCheckCatalogue(t *testing.T) {
	cat := map[string]string{
		"nav.dashboard": "Dashboard",
		"nav.feeds":     "Feeds",
		"nav.orphaned":  "This key is never referenced",
	}
	refs := []string{
		"nav.dashboard",
		"nav.feeds",
		"nav.dashboard", // referenced twice — must not duplicate in output
		"nav.missing",   // referenced but not in the catalogue
	}

	missing, unused := CheckCatalogue(cat, refs)

	if want := []string{"nav.missing"}; !reflect.DeepEqual(missing, want) {
		t.Errorf("missing = %v, want %v", missing, want)
	}
	if want := []string{"nav.orphaned"}; !reflect.DeepEqual(unused, want) {
		t.Errorf("unused = %v, want %v", unused, want)
	}
}

func TestCheckCatalogue_Clean(t *testing.T) {
	cat := map[string]string{"a": "A", "b": "B"}
	refs := []string{"a", "b"}

	missing, unused := CheckCatalogue(cat, refs)
	if len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
	if len(unused) != 0 {
		t.Errorf("unused = %v, want none", unused)
	}
}

func TestCheckCatalogue_Empty(t *testing.T) {
	missing, unused := CheckCatalogue(nil, nil)
	if len(missing) != 0 || len(unused) != 0 {
		t.Errorf("expected no findings on empty input, got missing=%v unused=%v", missing, unused)
	}
}
