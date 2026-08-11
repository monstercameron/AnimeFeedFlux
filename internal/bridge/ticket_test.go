package bridge_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
)

// TestTicketStore_RedeemSingleUseUnderConcurrency is the "decided by the
// database, not check-then-act" requirement applied to the in-memory ticket
// store: many goroutines race Redeem against the SAME ticket at the same
// instant, and exactly one may ever see ok == true.
func TestTicketStore_RedeemSingleUseUnderConcurrency(t *testing.T) {
	store := bridge.NewTicketStore()
	now := time.Now()
	ticket, _, err := store.Issue(now, "the-one-real-session-token")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const racers = 64
	var successes int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if _, ok := store.Redeem(now, ticket); ok {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("successful redemptions = %d, want exactly 1", successes)
	}
}

// TestTicketStore_RedeemUnknownRefused covers a ticket that was never
// issued.
func TestTicketStore_RedeemUnknownRefused(t *testing.T) {
	store := bridge.NewTicketStore()
	if _, ok := store.Redeem(time.Now(), "never-issued"); ok {
		t.Fatal("Redeem of an unknown ticket unexpectedly succeeded")
	}
}

// TestTicketStore_RedeemEmptyRefused covers the empty-string edge case
// explicitly, since an empty ticket query parameter is exactly what a
// request with no ticket at all produces.
func TestTicketStore_RedeemEmptyRefused(t *testing.T) {
	store := bridge.NewTicketStore()
	if _, ok := store.Redeem(time.Now(), ""); ok {
		t.Fatal("Redeem of an empty ticket unexpectedly succeeded")
	}
}

// TestTicketStore_RedeemExpiredRefused proves an expired-but-still-present
// ticket is refused, and — per Redeem's doc comment — is also removed by
// that attempt, so a second try after expiry behaves identically rather than
// resurrecting a stale ticket.
func TestTicketStore_RedeemExpiredRefused(t *testing.T) {
	store := bridge.NewTicketStore()
	start := time.Now()
	ticket, expiresAt, err := store.Issue(start, "a-session-token")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !expiresAt.After(start) {
		t.Fatalf("expiresAt = %v, want after issue time %v", expiresAt, start)
	}

	afterExpiry := expiresAt.Add(time.Millisecond)
	if _, ok := store.Redeem(afterExpiry, ticket); ok {
		t.Fatal("Redeem of an expired ticket unexpectedly succeeded")
	}
	// Retrying the same (now-consumed-by-the-expiry-check) ticket must still
	// fail, not merely "fail differently."
	if _, ok := store.Redeem(afterExpiry, ticket); ok {
		t.Fatal("Redeem of an already-checked expired ticket unexpectedly succeeded on retry")
	}
}

// TestTicketStore_IssueThenRedeemBeforeExpiry proves the ordinary success
// path: issue, redeem immediately, get the exact raw session token back.
func TestTicketStore_IssueThenRedeemBeforeExpiry(t *testing.T) {
	store := bridge.NewTicketStore()
	now := time.Now()
	ticket, _, err := store.Issue(now, "session-token-abc")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	raw, ok := store.Redeem(now, ticket)
	if !ok {
		t.Fatal("Redeem of a fresh ticket failed")
	}
	if raw != "session-token-abc" {
		t.Errorf("Redeem returned %q, want %q", raw, "session-token-abc")
	}
}

// TestTicketStore_DefaultTTLIsSeconds pins DefaultTicketTTL to the "seconds,
// not minutes" requirement directly, so a future edit that casually changes
// it to e.g. 5*time.Minute fails a test instead of only a code review.
func TestTicketStore_DefaultTTLIsSeconds(t *testing.T) {
	if bridge.DefaultTicketTTL <= 0 {
		t.Fatal("DefaultTicketTTL must be positive")
	}
	if bridge.DefaultTicketTTL > time.Minute {
		t.Fatalf("DefaultTicketTTL = %v, want at most a minute (it exists to survive one reconnect, not to be a second session lifetime)", bridge.DefaultTicketTTL)
	}
}
