// Login tickets: the mechanism that lets a browser go from an anonymous
// WebSocket upgrade to an authenticated one WITHOUT ever putting the real
// session token where JavaScript or WASM could read it (PLAN.md §4's "the
// token never touches JavaScript or WASM").
//
// Why this exists at all: internal/rpc.AuthServer.Login lives behind THIS
// bridge (PLAN.md §2/§3 — one transport, no HTTP side door), so a browser
// with no session cookie yet must be able to open a socket, call Login, and
// come away with a cookie — but Login cannot hand the raw session token back
// over that same socket (it would land in the WASM gRPC client's memory,
// exactly the credential exposure PLAN.md §4 forbids). A ticket is the
// answer: a SEPARATE, single-use, seconds-lived opaque value that identifies
// the session Login already minted, safe to return in an ordinary gRPC
// response header because possessing it for longer than one reconnect, or
// using it twice, buys an attacker nothing.
//
// Construction deliberately mirrors internal/auth/reset.go's password reset
// token — 256 bits from the OS CSPRNG, hashed at rest, single-use enforced
// by one atomic operation rather than a check-then-act pair — but lives here
// instead of internal/store/internal/auth because it is bridge-transport
// state, not account state: a ticket only ever matters for the few seconds
// between a Login response and the reconnect that redeems it, ties to no
// admin-account row, and losing every outstanding ticket on a process
// restart (this store is in-memory, not persisted) is the safe direction to
// fail in — exactly the same reasoning interceptor.go's elevatedTracker
// gives for its own process-local, restart-resets-to-safe state.
package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// TicketBytes is 256 bits of entropy, matching internal/auth's session token
// and password-reset token construction (NewToken/NewResetToken) — a plain
// opaque random value, not a JWT, for the same reason those two aren't:
// there is one backend and one admin, so a signed, self-describing token
// buys nothing a server-side lookup doesn't already give more simply.
const TicketBytes = 32

// DefaultTicketTTL is deliberately measured in SECONDS, not minutes: a
// ticket exists to survive exactly one reconnect (Login's response arriving,
// the client tearing down the anonymous socket, and the next upgrade
// presenting the ticket), a round trip that happens within the same tick of
// the event loop in practice. A long-lived ticket would just be a second,
// weaker session token; keeping the window to seconds is what makes "single-
// use, worthless the moment it's used" also true in the sense of "worthless
// almost immediately even if unused."
const DefaultTicketTTL = 20 * time.Second

// ticketEntry is what a live ticket resolves to: the raw session token the
// upgrade handler needs to (a) validate the underlying session is still good
// and (b) set as the __Host- cookie's value on the 101 response — see
// server.go's ServeHTTP.
type ticketEntry struct {
	rawSessionToken string
	expiresAt       time.Time
}

// TicketStore is an in-memory, mutex-guarded single-use ticket table. Every
// method is safe for concurrent use, which matters here specifically because
// "single-use under concurrency" (two upgrade attempts racing the same
// ticket) has to be decided by ONE atomic operation, not a lookup followed by
// a separate delete a second goroutine could interleave with.
type TicketStore struct {
	mu sync.Mutex
	m  map[string]ticketEntry
}

// NewTicketStore returns an empty store.
func NewTicketStore() *TicketStore {
	return &TicketStore{m: make(map[string]ticketEntry)}
}

// hashTicket hashes a raw ticket value the same way internal/auth/session.go's
// HashToken and reset.go's hashResetToken hash THEIR raw values: SHA-256,
// base64 (RawURLEncoding) — never argon2id, for the identical reason those
// two give (256 bits of CSPRNG output has no low-entropy human input to
// stretch against). "Hashed at rest" here means hashed in this process's
// memory, the same property those two mean by it for their on-disk rows: a
// heap dump or a debugger attached to this process yields the hash, not the
// raw ticket a client would need to redeem it.
func hashTicket(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Issue mints a fresh ticket bound to rawSessionToken (the raw session token
// AuthServer.mintSession just produced) and returns the raw ticket value —
// the ONLY time it is ever available in cleartext; only its hash is kept.
// now.Add(DefaultTicketTTL) is stored as the expiry Redeem checks against.
func (s *TicketStore) Issue(now time.Time, rawSessionToken string) (ticket string, expiresAt time.Time, err error) {
	if rawSessionToken == "" {
		return "", time.Time{}, fmt.Errorf("bridge: issuing ticket: rawSessionToken is empty")
	}
	buf := make([]byte, TicketBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, fmt.Errorf("bridge: generating ticket: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	expiresAt = now.Add(DefaultTicketTTL)

	s.mu.Lock()
	s.m[hashTicket(raw)] = ticketEntry{rawSessionToken: rawSessionToken, expiresAt: expiresAt}
	s.mu.Unlock()

	return raw, expiresAt, nil
}

// Redeem consumes ticket if — and only if — it is still known and unexpired
// as of now, returning the raw session token it was issued for.
//
// Single-use under real concurrency is enforced by doing the lookup AND the
// delete inside one critical section, mirroring store.CompletePasswordReset's
// `UPDATE ... WHERE used_at IS NULL` / RowsAffected pattern (PLAN.md's own
// instruction for this change: "decided by the database... not check-then-
// act"). internal/store is out of scope for this change, so there is no
// database row to make that guarantee — this mutex-guarded map is the exact
// in-memory analog: of two goroutines calling Redeem with the same ticket at
// the same instant, Go's sync.Mutex serializes them, the first to acquire
// the lock finds the entry AND deletes it before releasing, and the second
// finds nothing. Neither goroutine can observe "found" without also being
// the one that deleted it, so two callers can never both redeem successfully
// — exactly the property a losing racer against the reset-token UPDATE gets
// from RowsAffected == 0.
//
// An expired-but-still-present ticket is deleted here too (not left for a
// separate sweep), so a client retrying a stale ticket gets a clean "not
// found" on every attempt after the first, not merely the first.
func (s *TicketStore) Redeem(now time.Time, ticket string) (rawSessionToken string, ok bool) {
	if ticket == "" {
		return "", false
	}
	hash := hashTicket(ticket)

	s.mu.Lock()
	entry, found := s.m[hash]
	if found {
		delete(s.m, hash)
	}
	s.mu.Unlock()

	if !found {
		return "", false
	}
	if !now.Before(entry.expiresAt) {
		return "", false
	}
	return entry.rawSessionToken, true
}
