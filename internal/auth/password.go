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
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

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
		Time:   3,
		Memory: 64 * 1024, // 64 MiB
		// Parallelism 1, per OWASP's recommendation for this memory profile.
		// It was 4; that is not "more secure", it just splits the same memory
		// across lanes and makes the cost easier to parallelise for an attacker
		// with GPUs than for us on one droplet core.
		Threads: 1,
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
	// NFKC before derivation, which is what Normalize's own doc has always
	// claimed happened and did not: Hash passed the raw string to argon2 while
	// only IsWeak and IsBreached normalised. The policy was therefore enforced
	// against one string and the credential derived from another, and the
	// stated cross-platform property — "the same passphrase typed on a
	// different keyboard or platform produces different bytes and fails to
	// verify" — did not hold (A8-36).
	key := argon2.IDKey([]byte(Normalize(password)), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
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

	derive := func(pw string) []byte {
		return argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	}

	// subtle.ConstantTimeCompare requires equal-length slices to give its constant-time
	// guarantee; a length mismatch alone would otherwise leak via early return, so it's folded
	// into the comparison result rather than short-circuited.
	normalized := Normalize(password)
	match := subtle.ConstantTimeCompare(derive(normalized), key) == 1

	// Legacy fallback, and the reason applying NFKC to Hash is not a lockout.
	//
	// Every hash written before A8-36 was derived from the RAW string. For an
	// ASCII passphrase NFKC is the identity and nothing changes, but for one
	// containing, say, a composed accent or a full-width character, the
	// normalised form derives a different key — and the admin would simply
	// stop being able to log in, with no way back in short of `aff admin
	// reset`. So a raw match is still accepted, and reported as needing a
	// rehash so the very next successful login rewrites the stored hash in
	// normalised form and the fallback stops being reachable for that
	// credential.
	legacy := false
	if !match && normalized != password {
		if subtle.ConstantTimeCompare(derive(password), key) == 1 {
			match, legacy = true, true
		}
	}

	def := DefaultParams()
	weaker := p.Time < def.Time || p.Memory < def.Memory || p.Threads < def.Threads || uint32(len(key)) < def.KeyLen
	return match, match && (weaker || legacy), nil
}

// HashPeppered is Hash, but additionally mixes the optional pepper (PLAN.md §4) into the raw
// argon2id output — via Pepper, i.e. HMAC-SHA256(pepper, argonOutput) — before it is PHC-encoded,
// rather than into the password beforehand. That ordering is the one PLAN.md §4 and Pepper's own
// doc comment specify: it is what lets a stolen database (salt + params + this stored value) stay
// insufficient on its own, since reproducing what was actually compared requires the pepper, which
// never touches the database.
//
// A nil/empty pepper makes this byte-for-byte identical to Hash: Pepper(x, nil) is defined as a
// no-op, so an unconfigured deployment's stored hashes are completely unaffected by this function
// existing. Verify (unpeppered) still reads a HashPeppered-with-nil-pepper hash correctly, because
// the two produce identical encoded strings in that case.
func HashPeppered(password string, p Params, pepper []byte) (string, error) {
	if err := IsWeak(password); err != nil {
		return "", err
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	// Normalised, exactly as Hash does — see the note there (A8-36).
	key := argon2.IDKey([]byte(Normalize(password)), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	key = Pepper(key, pepper)
	encoded := fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Variant, argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// VerifyPasswordPeppered is Verify, but reverses HashPeppered's Pepper step before comparing: it
// re-derives the raw argon2id candidate exactly as Verify does, then compares it against the stored
// value via pepper.go's VerifyPeppered — which applies the identical Pepper(candidate, pepper) step
// HashPeppered applied before storing. This is what gives that byte-level primitive its first real
// caller; previously nothing in the codebase invoked it, even though pepper.go's doc comment
// describes exactly this post-hash ordering.
//
// Named VerifyPasswordPeppered rather than reusing "VerifyPeppered" because pepper.go already
// exports a VerifyPeppered operating on raw byte slices (candidate, stored []byte) — Go has no
// overloading, and the two operate at different layers (PHC string vs. raw argon2id output), so
// giving them the same name would make call sites ambiguous about which layer they're at.
//
// A nil/empty pepper makes this byte-for-byte identical to Verify, for the same reason HashPeppered
// with a nil/empty pepper is identical to Hash: Pepper is a documented no-op for that case, so an
// unconfigured deployment's login path is unaffected by this function existing.
func VerifyPasswordPeppered(password, encoded string, pepper []byte) (ok bool, needsRehash bool, err error) {
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

	derive := func(pw string) []byte {
		return argon2.IDKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	}
	normalized := Normalize(password)
	match := VerifyPeppered(derive(normalized), key, pepper)

	// Same legacy fallback as Verify: hashes written before A8-36 derived from
	// the raw string, and refusing them would lock the admin out of a
	// credential that has not changed. A raw match reports needsRehash so the
	// next successful login rewrites it normalised.
	legacy := false
	if !match && normalized != password {
		if VerifyPeppered(derive(password), key, pepper) {
			match, legacy = true, true
		}
	}

	def := DefaultParams()
	weaker := p.Time < def.Time || p.Memory < def.Memory || p.Threads < def.Threads || uint32(len(key)) < def.KeyLen
	return match, match && (weaker || legacy), nil
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

	// SEC-49: the range check above still permits m up to ~4 TiB — decode() would happily hand
	// Verify a hash whose m/t/p forces a multi-gigabyte-or-worse argon2.IDKey allocation before any
	// password comparison happens. Nothing legitimate ever needs that: every hash this process
	// verifies was produced by Hash() in this same package, so the ceiling only ever has to cover
	// DefaultParams() plus headroom for a future tuning bump, not an attacker's choice. Capping here,
	// inside decode(), guarantees rejection happens before Verify ever calls argon2.IDKey — a
	// corrupted DB row or tampered hash string can't force the allocation at all.
	//
	// The ceiling is computed from DefaultParams() rather than hardcoded, so raising DefaultParams()
	// later (PLAN.md §4's rehash-on-login cost bump) raises the ceiling with it automatically. If it
	// were hardcoded instead, a future tuning change could silently put the new default above the old
	// ceiling and lock the admin out of their own account — the worst failure mode for a single-admin
	// system with no account recovery path.
	def := DefaultParams()
	const paramCeilingMultiple = 4 // generous multiple of DefaultParams(): room to tune, not room to attack
	if m > int64(def.Memory)*paramCeilingMultiple ||
		t > int64(def.Time)*paramCeilingMultiple ||
		threads > int64(def.Threads)*paramCeilingMultiple {
		return "", 0, Params{}, nil, nil, errors.New("auth: hash params exceed sanity ceiling")
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

// Password policy follows NIST SP 800-63B (PLAN.md §4), which looks unfamiliar
// next to the rules most systems still use.
//
// Length is the whole of it: 15 minimum, 128 maximum, spaces and Unicode
// allowed. There are deliberately NO composition rules and NO expiry.
//
// Both of those were here and were removed. A mandatory letters-plus-digits mix
// does not raise entropy — it pushes people toward P@ssw0rd2026! and away from
// "correct battery dinosaur tennis", which is far stronger and would have been
// REJECTED by the old check for containing neither a digit nor a symbol. That
// is the rule actively selecting the weaker password, which is why NIST dropped
// it. Expiry fails the same way: a human asked to rotate on a schedule
// increments a digit.
//
// What replaces them is length plus a compromised-password blocklist, since
// "long" and "not already breached" are the two properties that actually
// resist an offline crack of a stolen argon2id hash.
const (
	minPasswordLen = 15
	maxPasswordLen = 128
)

// Normalize applies NFKC before hashing. Without it the same passphrase typed
// on a different keyboard or platform produces different bytes and fails to
// verify — a support burden with no security benefit.
func Normalize(password string) string { return norm.NFKC.String(password) }

// IsWeak rejects a password on length, on normalisation, or because it appears
// in the compromised-password blocklist. It runs at enrolment and inside Hash,
// so a caller cannot bypass it by calling Hash directly.
func IsWeak(password string) error {
	p := Normalize(password)

	// Counted in runes, not bytes. A 15-character Japanese passphrase is 45
	// bytes and would sail past a byte-length check while a 15-byte ASCII one
	// would not — the same rule applied inconsistently.
	n := utf8.RuneCountInString(p)
	if n < minPasswordLen {
		return fmt.Errorf("auth: password must be at least %d characters", minPasswordLen)
	}
	if n > maxPasswordLen {
		return fmt.Errorf("auth: password must be at most %d characters", maxPasswordLen)
	}
	if IsBreached(p) {
		return errors.New("auth: password appears in a compromised-password list; choose another")
	}
	return nil
}
