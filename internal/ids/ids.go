// Package ids mints item_key values: the opaque identity assigned once to
// every published item (PLAN.md §5.1).
package ids

import (
	crand "crypto/rand"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Source produces item keys.
//
// A key is a ULID in canonical 26-character uppercase Crockford base32. Only
// the timestamp component is meaningful; the entropy component is opaque.
//
// Critically, a key must NEVER be derived from the item's content (title,
// summary, link, ...). The RSS guid and Atom id are built directly from the
// item_key (see render.TagURI), and a guid must be stable forever. If a key
// were instead computed from content, any future refactor of the renderer or
// a repair script that re-derives the key from the (now-edited) title would
// silently mint a *new* guid for the same item, and every subscriber's
// reader would show it as a brand-new entry — a duplicate resurfacing a
// story they already saw. Keeping the key opaque and assigned once, at
// insert, is what makes "the guid never changes" true by construction rather
// than by every future call site remembering not to touch it.
type Source interface {
	// NewItemKey returns a fresh item_key. now controls only the ULID's
	// timestamp component (and therefore lexicographic ordering); it has no
	// bearing on the entropy, which is what keeps two keys minted in the
	// same millisecond from colliding.
	NewItemKey(now time.Time) string
}

// cryptoSource is the production Source. It draws entropy from crypto/rand
// via ulid.Monotonic, which also guarantees that keys minted within the same
// millisecond on the same Source still sort strictly increasing — useful for
// keeping insert order and key order aligned without it being a documented
// contract callers rely on.
type cryptoSource struct {
	mu      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// NewSource returns the production Source: cryptographically random entropy,
// safe for concurrent use by many goroutines.
func NewSource() Source {
	return &cryptoSource{entropy: ulid.Monotonic(crand.Reader, 0)}
}

func (s *cryptoSource) NewItemKey(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ulid.MustNew(ulid.Timestamp(now), s.entropy)
	return id.String()
}

// deterministicSource is the test Source: the entropy stream is seeded, so a
// golden file capturing generated keys stays byte-stable across test runs
// (and across machines) instead of changing every time crypto/rand is
// consulted.
type deterministicSource struct {
	mu  sync.Mutex
	rng *rand.Rand
}

// NewDeterministicSource returns a Source whose entropy is derived from a
// math/rand/v2 PRNG seeded with seed. Two sources built from the same seed
// produce the same sequence of keys for the same sequence of timestamps;
// different seeds diverge. This exists for tests and golden files only — do
// not use it in production, where key entropy must be unguessable.
func NewDeterministicSource(seed int64) Source {
	return &deterministicSource{rng: rand.New(rand.NewPCG(uint64(seed), uint64(seed)))}
}

func (s *deterministicSource) NewItemKey(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entropy [10]byte
	for i := range entropy {
		entropy[i] = byte(s.rng.IntN(256))
	}
	id := ulid.ULID{}
	if err := id.SetTime(ulid.Timestamp(now)); err != nil {
		panic(err) // only possible if now overflows ULID's 48-bit timestamp
	}
	if err := id.SetEntropy(entropy[:]); err != nil {
		panic(err) // entropy is always exactly 10 bytes
	}
	return id.String()
}
