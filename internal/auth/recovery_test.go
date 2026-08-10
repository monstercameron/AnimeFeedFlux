package auth

import (
	"strings"
	"testing"
)

func TestGenerateCodesProducesDistinctCodes(t *testing.T) {
	const n = 10
	plain, hashes, err := GenerateCodes(n)
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}
	if len(plain) != n || len(hashes) != n {
		t.Fatalf("expected %d codes, got %d plain / %d hashes", n, len(plain), len(hashes))
	}

	seenPlain := map[string]bool{}
	seenHash := map[string]bool{}
	for i := range plain {
		if seenPlain[plain[i]] {
			t.Fatalf("duplicate plaintext code at index %d: %q", i, plain[i])
		}
		seenPlain[plain[i]] = true

		if seenHash[hashes[i]] {
			t.Fatalf("duplicate hash at index %d: %q", i, hashes[i])
		}
		seenHash[hashes[i]] = true

		if plain[i] == hashes[i] {
			t.Fatalf("hash equals plaintext at index %d", i)
		}
	}
}

func TestGenerateCodesAlphabetExcludesConfusables(t *testing.T) {
	plain, _, err := GenerateCodes(25)
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}

	const confusable = "0O1IL"
	for _, code := range plain {
		for _, c := range confusable {
			if strings.ContainsRune(code, c) {
				t.Fatalf("code %q contains confusable character %q", code, c)
			}
		}
	}
}

func TestGenerateCodesRejectsNonPositiveN(t *testing.T) {
	if _, _, err := GenerateCodes(0); err == nil {
		t.Fatal("expected error for n=0")
	}
	if _, _, err := GenerateCodes(-1); err == nil {
		t.Fatal("expected error for n=-1")
	}
}

func TestVerifyCodeAcceptsCaseAndSeparatorVariants(t *testing.T) {
	plain, hashes, err := GenerateCodes(3)
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}

	target := plain[1]

	variants := []string{
		target,
		strings.ToLower(target),
		strings.ReplaceAll(target, "-", ""),
		strings.ReplaceAll(strings.ToLower(target), "-", " "),
	}

	for _, v := range variants {
		idx, ok := VerifyCode(v, hashes)
		if !ok {
			t.Errorf("VerifyCode rejected variant %q of code %q", v, target)
			continue
		}
		if idx != 1 {
			t.Errorf("VerifyCode variant %q returned index %d, want 1", v, idx)
		}
	}
}

func TestVerifyCodeRejectsWrongCode(t *testing.T) {
	_, hashes, err := GenerateCodes(3)
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}

	if _, ok := VerifyCode("ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ", hashes); ok {
		t.Fatal("VerifyCode accepted a code that was never generated")
	}
}

func TestVerifyCodeReturnsCorrectIndex(t *testing.T) {
	plain, hashes, err := GenerateCodes(5)
	if err != nil {
		t.Fatalf("GenerateCodes: %v", err)
	}

	for wantIdx, code := range plain {
		gotIdx, ok := VerifyCode(code, hashes)
		if !ok {
			t.Fatalf("VerifyCode rejected valid code at index %d", wantIdx)
		}
		if gotIdx != wantIdx {
			t.Fatalf("VerifyCode returned index %d, want %d", gotIdx, wantIdx)
		}
	}
}
