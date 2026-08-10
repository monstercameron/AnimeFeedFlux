package i18nlint

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBaseline(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "i18n-baseline.txt")
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRatchet_EqualsBaseline_OK(t *testing.T) {
	p := writeBaseline(t, "5\n")
	if err := Ratchet(5, p); err != nil {
		t.Fatalf("Ratchet(5, baseline=5) = %v, want nil", err)
	}
}

func TestRatchet_BelowBaseline_OK(t *testing.T) {
	p := writeBaseline(t, "5\n")
	if err := Ratchet(2, p); err != nil {
		t.Fatalf("Ratchet(2, baseline=5) = %v, want nil (improvement is accepted)", err)
	}
}

func TestRatchet_AboveBaseline_Rejected(t *testing.T) {
	p := writeBaseline(t, "5\n")
	if err := Ratchet(6, p); err == nil {
		t.Fatal("Ratchet(6, baseline=5) = nil, want an error — the ratchet must never rise")
	}
}

func TestRatchet_ZeroBaseline_AnyLiteralRejected(t *testing.T) {
	p := writeBaseline(t, "0")
	if err := Ratchet(0, p); err != nil {
		t.Fatalf("Ratchet(0, baseline=0) = %v, want nil", err)
	}
	if err := Ratchet(1, p); err == nil {
		t.Fatal("Ratchet(1, baseline=0) = nil, want an error")
	}
}

func TestRatchet_MissingBaseline_Errors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "does-not-exist.txt")
	if err := Ratchet(0, p); err == nil {
		t.Fatal("Ratchet with missing baseline file = nil, want an error instructing how to seed it")
	}
}

func TestRatchet_CorruptBaseline_Rejected(t *testing.T) {
	p := writeBaseline(t, "not-a-number")
	if err := Ratchet(0, p); err == nil {
		t.Fatal("Ratchet with corrupt baseline = nil, want an error")
	}
}

func TestRatchet_NegativeBaseline_Rejected(t *testing.T) {
	p := writeBaseline(t, "-1")
	if err := Ratchet(0, p); err == nil {
		t.Fatal("Ratchet with negative baseline = nil, want an error")
	}
}

func TestRatchet_NegativeCount_Rejected(t *testing.T) {
	p := writeBaseline(t, "5")
	if err := Ratchet(-1, p); err == nil {
		t.Fatal("Ratchet with negative count = nil, want an error")
	}
}

// Ratchet must never write the baseline file itself, in either direction —
// raising or lowering the floor is a deliberate, reviewable human edit.
func TestRatchet_NeverWritesBaseline(t *testing.T) {
	p := writeBaseline(t, "5")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	_ = Ratchet(2, p) // improvement — must not rewrite the file downward
	_ = Ratchet(9, p) // regression — must not rewrite the file at all

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("baseline file changed from %q to %q; Ratchet must never write it", before, after)
	}
}
