package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

// recoveryAlphabet excludes characters humans commonly confuse when
// transcribing a code off a screen or a printed sheet: 0/O, 1/I/L. PLAN.md
// §12.2 calls these "one of only two ways back in" (the other is SSH
// break-glass), so legibility under stress matters as much as entropy.
//
// It holds 30 symbols. This comment used to add that "the rest of the vowels
// are dropped too so the codes don't spell words", which is not what the
// string does — A and E are both in it, so a code can spell one. Corrected
// rather than changed: dropping A and E now would invalidate nothing (codes
// are hashed, and verification reads the stored hash), but it would shrink
// the alphabet for a property the codes were never actually getting, and the
// entropy is what matters here (A8-39).
const recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

const (
	recoveryGroupLen   = 5 // characters per group
	recoveryGroupCount = 4 // groups per code
	recoveryCodeRunes  = recoveryGroupLen * recoveryGroupCount
	recoverySeparator  = "-"
)

// GenerateCodes produces n single-use recovery codes for enrollment display.
// plain holds the human-transcribable codes (format: XXXXX-XXXXX-XXXXX-XXXXX
// from recoveryAlphabet) to be shown to the operator exactly once; hashes
// holds the corresponding SHA-256 hashes to persist. Only hashes are ever
// meant to reach storage — PLAN.md §12.2 requires codes be "shown once, only
// hashes stored" so a stolen DB dump can't be used to regenerate valid codes.
func GenerateCodes(n int) (plain []string, hashes []string, err error) {
	if n <= 0 {
		return nil, nil, errors.New("auth: n must be positive")
	}

	plain = make([]string, n)
	hashes = make([]string, n)

	for i := 0; i < n; i++ {
		code, err := generateOneCode()
		if err != nil {
			return nil, nil, err
		}
		plain[i] = code
		hashes[i] = hashRecoveryCode(code)
	}

	return plain, hashes, nil
}

func generateOneCode() (string, error) {
	raw := make([]byte, recoveryCodeRunes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}

	var b strings.Builder
	alphaLen := len(recoveryAlphabet)
	for i, v := range raw {
		if i > 0 && i%recoveryGroupLen == 0 {
			b.WriteString(recoverySeparator)
		}
		// v is a uniform random byte in [0,255]; recoveryAlphabet has 30
		// symbols, which does not evenly divide 256. 256 = 8*30 + 16, so a
		// single mod-30 pass makes the first 16 symbols land 9 times per 256
		// values against 8 for the other 14 — 12.5% more likely, not the
		// "~1/6" this note claimed while computing off 31 symbols it does not
		// have. The conclusion is unchanged: recovery codes are shown once
		// and never reused, and that bias is far too small to matter for
		// 20-character codes drawn from crypto/rand, so plain modulo is used
		// rather than rejection sampling (A8-39).
		b.WriteByte(recoveryAlphabet[int(v)%alphaLen])
	}

	return b.String(), nil
}

// hashRecoveryCode normalizes then hashes a code for storage/comparison.
func hashRecoveryCode(code string) string {
	norm := normalizeCode(code)
	sum := sha256.Sum256([]byte(norm))
	return fmt.Sprintf("%x", sum)
}

// normalizeCode uppercases and strips whitespace/separators so a human
// retyping a code (with or without dashes, in whatever case) still matches
// what was hashed at generation time.
func normalizeCode(code string) string {
	var b strings.Builder
	b.Grow(len(code))
	for _, r := range code {
		switch r {
		case '-', '_', ' ', '\t', '\n', '\r':
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// VerifyCode checks code against the stored hashes, normalizing case and
// separators first so "ab3de-fg7hj-..." and "AB3DEFG7HJ..." both match the
// hash produced at generation. It returns the index of the matching hash so
// the caller can mark that specific code used.
//
// Codes are SINGLE USE: the caller MUST mark the returned index as used
// (e.g. delete or flag the hash) immediately upon a successful verification.
// VerifyCode itself is stateless and has no way to enforce this — it will
// happily "verify" the same code twice — because leaving a recovery code
// valid after first use turns a one-time escape hatch into a permanent
// backdoor that survives password and TOTP resets.
func VerifyCode(code string, hashes []string) (index int, ok bool) {
	target := hashRecoveryCode(code)
	targetBytes := []byte(target)

	found := -1
	// Constant-time-ish: compare against every hash rather than
	// short-circuiting on first match, so the response time doesn't leak
	// which position (if any) matched via early return.
	for i, h := range hashes {
		if subtle.ConstantTimeCompare([]byte(h), targetBytes) == 1 {
			found = i
		}
	}

	if found == -1 {
		return 0, false
	}
	return found, true
}
