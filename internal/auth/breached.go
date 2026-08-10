package auth

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // SHA-1 is the corpus's index format, not a security control
	"encoding/hex"
	"io"
	"strings"
	"sync"
)

// The compromised-password blocklist (PLAN.md §4).
//
// NIST SP 800-63B replaces composition rules with this check, and the swap is
// the whole point: "not already breached" filters the passwords attackers
// actually try, while "must contain a symbol" filters nothing an attacker's
// wordlist does not already contain.
//
// Deliberately OFFLINE. The obvious implementation is a k-anonymity range query
// against Have I Been Pwned, and it is the wrong choice here: it puts a third
// party on the login path, so their outage becomes an outage of the only way
// into this system. It also leaks a five-character hash prefix of the admin's
// password on every enrolment. A local list cannot do either.
//
// The built-in list is small on purpose — it covers what a human actually
// reaches for when told "15 characters minimum", which is a passphrase built
// from common words or a padded common password. A fuller corpus (the HIBP
// downloadable SHA-1 set) can be loaded with LoadBreachedSHA1 without changing
// any caller.

var (
	breachedMu     sync.RWMutex
	breachedSHA1   map[string]struct{}
	breachedCommon = buildCommonSet()
)

// commonPasswords is the built-in floor. Every entry here is either a
// perennial top-100 password or a padded variant of one, which is exactly the
// shape a minimum-length rule produces when applied to someone who does not
// want to think of a passphrase.
var commonPasswords = []string{
	"password", "password1", "password123", "passw0rd", "p@ssw0rd", "p@ssword",
	"123456", "12345678", "123456789", "1234567890", "qwerty", "qwertyuiop",
	"letmein", "welcome", "admin", "administrator", "root", "iloveyou",
	"monkey", "dragon", "sunshine", "princess", "football", "baseball",
	"abc123", "111111", "000000", "trustno1", "changeme", "secret",
	"correcthorsebatterystaple", // famous enough to be in every wordlist
	"passwordpassword", "adminadmin", "letmeinplease", "thisisapassword",
	"qwertyuiopasdfgh", "1234567890123456", "aaaaaaaaaaaaaaaa",
}

func buildCommonSet() map[string]struct{} {
	m := make(map[string]struct{}, len(commonPasswords)*2)
	for _, p := range commonPasswords {
		m[p] = struct{}{}
	}
	return m
}

// IsBreached reports whether the password is known-compromised.
//
// Matching is case-insensitive and ignores surrounding whitespace, because
// "Password123" and "password123" are the same guess to an attacker even though
// they are different strings to us. Digit-suffix padding is stripped for the
// same reason: "monkey2026" is "monkey" with a year on it, and a wordlist has
// tried that.
func IsBreached(password string) bool {
	p := strings.ToLower(strings.TrimSpace(Normalize(password)))
	if p == "" {
		return false
	}

	breachedMu.RLock()
	loaded := breachedSHA1
	breachedMu.RUnlock()

	if loaded != nil {
		sum := sha1.Sum([]byte(password)) //nolint:gosec // corpus index format
		if _, ok := loaded[strings.ToUpper(hex.EncodeToString(sum[:]))]; ok {
			return true
		}
	}

	if _, ok := breachedCommon[p]; ok {
		return true
	}
	// A common password with a trailing year or counter is still a common
	// password; the padding is what people add to satisfy a length rule.
	if stripped := strings.TrimRight(p, "0123456789!@#$"); stripped != p && stripped != "" {
		if _, ok := breachedCommon[stripped]; ok {
			return true
		}
	}
	// Repetitive and sequential strings. NIST SP 800-63B explicitly lists these
	// as blocklist material ("aaaaaa", "1234abcd"), so rejecting them is not a
	// composition rule sneaking back in — a composition rule dictates what a
	// password must CONTAIN, whereas this rejects strings with almost no
	// entropy regardless of what they contain. "abababababababab" clears a
	// 15-character floor and is worth about as much as "ab".
	if isSingleRuneRepeat(p) || isShortCycle(p) || isSequential(p) {
		return true
	}
	return false
}

// isShortCycle reports whether s is a short unit repeated to length, such as
// "abababab" or "123123123".
func isShortCycle(s string) bool {
	r := []rune(s)
	for unit := 1; unit <= 4 && unit*2 <= len(r); unit++ {
		if len(r)%unit != 0 {
			continue
		}
		match := true
		for i := unit; i < len(r); i++ {
			if r[i] != r[i%unit] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// isSequential reports whether s is one run of consecutive codepoints, such as
// "abcdefghijklmnop" or "12345678901234567".
func isSequential(s string) bool {
	r := []rune(s)
	if len(r) < 4 {
		return false
	}
	up, down := true, true
	for i := 1; i < len(r); i++ {
		if r[i] != r[i-1]+1 {
			up = false
		}
		if r[i] != r[i-1]-1 {
			down = false
		}
		if !up && !down {
			return false
		}
	}
	return true
}

func isSingleRuneRepeat(s string) bool {
	var first rune
	for i, r := range s {
		if i == 0 {
			first = r
			continue
		}
		if r != first {
			return false
		}
	}
	return s != ""
}

// LoadBreachedSHA1 loads an uppercase-hex SHA-1 corpus, one hash per line, with
// an optional ":count" suffix (the format the HIBP downloadable list ships in).
//
// Safe to call at boot with a large file: the set is swapped in atomically, so
// a login racing the load sees either the old set or the new one, never a
// half-built map.
func LoadBreachedSHA1(r io.Reader) (int, error) {
	set := make(map[string]struct{}, 1<<16)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if i := strings.IndexByte(line, ':'); i >= 0 {
			line = line[:i]
		}
		set[strings.ToUpper(line)] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}

	breachedMu.Lock()
	breachedSHA1 = set
	breachedMu.Unlock()
	return len(set), nil
}
