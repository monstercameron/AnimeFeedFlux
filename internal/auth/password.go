// Package auth implements the credential, session, and second-factor primitives that
// constitute the entire defense of AnimeFeedFlux's admin surface (PLAN.md §4). There is a
// single admin and no authorization model, so every function in this package is a security
// boundary, not a convenience.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Params holds the tunable cost of argon2id. They travel with every hash (PHC-style encoding
// below) rather than living only in code, because PLAN.md §4 requires raising the cost later and
// rehashing on next successful login — a hash that doesn't record its own params can't be told
// apart from a stronger one, and with a single admin account, guessing wrong locks them out.
type Params struct {
	Time    uint32 // number of passes over memory
	Memory  uint32 // KiB of memory
	Threads uint8  // degree of parallelism
	SaltLen uint32 // bytes of random salt
	KeyLen  uint32 // bytes of derived key
}

// DefaultParams are the 2026 baseline for a single-admin, publicly reachable login. They exceed
// the OWASP floor (m=19MiB,t=2,p=1) because this process only ever verifies one login at a time
// (no multi-tenant throughput concern), so we can spend more memory per attempt to raise the cost
// of an offline crack against a stolen hash.
func DefaultParams() Params {
	return Params{
		Time:    3,
		Memory:  64 * 1024, // 64 MiB
		Threads: 4,
		SaltLen: 16, // 128-bit salt
		KeyLen:  32, // 256-bit derived key
	}
}

const argon2Variant = "argon2id"

// Hash derives an argon2id key for password under p and returns it PHC-encoded:
//
//	$argon2id$v=19$m=<memory>,t=<time>,p=<threads>$<base64 salt>$<base64 key>
//
// Encoding the params alongside the salt and key is load-bearing, not cosmetic: Verify reads
// them back out to reproduce the same derivation, and the rehash-on-login migration path in §4
// depends on being able to compare a stored hash's params against DefaultParams() without a
// separate side-channel table linking hash -> params-used.
func Hash(password string, p Params) (string, error) {
	if err := IsWeak(password); err != nil {
		return "", err
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	encoded := fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Variant, argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify checks password against encoded, an argon2id PHC string produced by Hash. ok reports
// whether the password matches; needsRehash reports whether the stored params are weaker than
// DefaultParams(), so a caller can transparently re-Hash and persist a stronger hash on this
// successful login without a separate maintenance job.
//
// err is returned only for a malformed encoded string, never for a wrong password — callers must
// not distinguish "bad password" from "corrupt hash" in what they show a user, but they may want
// to distinguish it in logs.
func Verify(password, encoded string) (ok bool, needsRehash bool, err error) {
	variant, version, p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	if variant != argon2Variant {
		return false, false, fmt.Errorf("auth: unsupported hash variant %q", variant)
	}
	if version != argon2.Version {
		return false, false, fmt.Errorf("auth: unsupported argon2 version %d", version)
	}

	candidate := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))

	// subtle.ConstantTimeCompare requires equal-length slices to give its constant-time
	// guarantee; a length mismatch alone would otherwise leak via early return, so it's folded
	// into the comparison result rather than short-circuited.
	match := subtle.ConstantTimeCompare(candidate, key) == 1

	def := DefaultParams()
	weaker := p.Time < def.Time || p.Memory < def.Memory || p.Threads < def.Threads || uint32(len(key)) < def.KeyLen
	return match, match && weaker, nil
}

// decode parses a PHC-style argon2id string back into its components. It never panics on
// malformed input — every index/parse step is guarded — because Verify must return an error, not
// crash the login handler, when handed garbage (a corrupted row, a manual DB edit, etc).
func decode(encoded string) (variant string, version int, p Params, salt, key []byte, err error) {
	// Expected shape: "$argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>"
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return "", 0, Params{}, nil, nil, errors.New("auth: malformed hash encoding")
	}
	variant = parts[1]

	if !strings.HasPrefix(parts[2], "v=") {
		return "", 0, Params{}, nil, nil, errors.New("auth: malformed hash version field")
	}
	version, err = strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil {
		return "", 0, Params{}, nil, nil, fmt.Errorf("auth: malformed hash version: %w", err)
	}

	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return "", 0, Params{}, nil, nil, errors.New("auth: malformed hash params field")
	}
	m, t, threads := int64(0), int64(0), int64(0)
	for _, f := range fields {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			return "", 0, Params{}, nil, nil, errors.New("auth: malformed hash param")
		}
		v, perr := strconv.ParseInt(kv[1], 10, 64)
		if perr != nil {
			return "", 0, Params{}, nil, nil, fmt.Errorf("auth: malformed hash param %q: %w", kv[0], perr)
		}
		switch kv[0] {
		case "m":
			m = v
		case "t":
			t = v
		case "p":
			threads = v
		default:
			return "", 0, Params{}, nil, nil, fmt.Errorf("auth: unknown hash param %q", kv[0])
		}
	}
	if m <= 0 || t <= 0 || threads <= 0 || m > 1<<32-1 || t > 1<<32-1 || threads > 1<<8-1 {
		return "", 0, Params{}, nil, nil, errors.New("auth: hash params out of range")
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return "", 0, Params{}, nil, nil, fmt.Errorf("auth: malformed hash salt: %w", err)
	}
	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return "", 0, Params{}, nil, nil, fmt.Errorf("auth: malformed hash key: %w", err)
	}
	if len(salt) == 0 || len(key) == 0 {
		return "", 0, Params{}, nil, nil, errors.New("auth: empty salt or key")
	}

	p = Params{
		Time:    uint32(t),
		Memory:  uint32(m),
		Threads: uint8(threads),
		SaltLen: uint32(len(salt)),
		KeyLen:  uint32(len(key)),
	}
	return variant, version, p, salt, key, nil
}

// minPasswordLen is deliberately a length floor, not a composition rule ("must contain a
// symbol"): composition rules push users toward predictable substitutions (Password1! patterns)
// that don't actually raise entropy, while length is the dominant factor in resisting both
// online guessing and an offline crack of a stolen argon2id hash.
const minPasswordLen = 12

// IsWeak rejects passwords that are too short or drawn from an obvious low-entropy set (e.g. the
// same character repeated) before they're ever hashed. This runs at enrolment (`aff admin init`)
// and inside Hash, so a caller cannot accidentally bypass it by calling Hash directly.
func IsWeak(password string) error {
	if len(password) < minPasswordLen {
		return fmt.Errorf("auth: password must be at least %d characters", minPasswordLen)
	}

	runes := []rune(password)
	distinct := make(map[rune]struct{}, len(runes))
	var hasLetter, hasDigit, hasOther bool
	for _, r := range runes {
		distinct[r] = struct{}{}
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		default:
			hasOther = true
		}
	}
	// A handful of distinct characters over a long string ("aaaaaaaaaaaaaa" or "abababababab")
	// clears the length bar but not the entropy bar.
	if len(distinct) < 6 {
		return errors.New("auth: password has too little variety")
	}
	if !hasLetter || (!hasDigit && !hasOther) {
		return errors.New("auth: password must mix letters with digits or symbols")
	}
	return nil
}
