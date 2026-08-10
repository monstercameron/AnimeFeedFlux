package sectest

import (
	"errors"
	"sync"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SEC-44: two concurrent logins presenting the SAME TOTP code must not both
// succeed. Exactly one must win the race, and — this is the load-bearing
// part — the loser must be rejected because store.MarkTOTPStepUsed's INSERT
// hit the totp_used PRIMARY KEY on `step` (a database-enforced race), not
// because application code did a SELECT-then-decide check that could itself
// race.

// TestTOTPReplay_ConcurrentLoginsOnlyOneSucceeds drives the race through the
// real Login RPC: two goroutines fire the identical valid TOTP code at the
// same instant. At most one can succeed.
func TestTOTPReplay_ConcurrentLoginsOnlyOneSucceeds(t *testing.T) {
	srv, _, secret := newTestServer(t)
	now := time.Now()
	code := validCode(t, secret, now)

	const attempts = 8
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := withPeerIP(t.Context(), ipFor("totprace", i))
			_, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code})
			successes[i] = err == nil
		}(i)
	}
	wg.Wait()

	won := 0
	for _, ok := range successes {
		if ok {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("exactly one concurrent login with a replayed TOTP code should succeed, got %d out of %d", won, attempts)
	}
}

// TestTOTPReplay_LoserFailsOnDatabasePrimaryKey isolates the mechanism: two
// goroutines call store.MarkTOTPStepUsed for the IDENTICAL step concurrently.
// Exactly one succeeds; the other must get store.ErrTOTPReplay, which
// store.go documents as coming from the totp_used PRIMARY KEY constraint
// (see ErrTOTPReplay's doc comment) rather than a check-then-insert in
// application code. A check-then-insert race would be able to let both
// goroutines through under the right interleaving; this test runs enough
// concurrent attempts that a non-atomic implementation would eventually show
// more than one winner.
func TestTOTPReplay_LoserFailsOnDatabasePrimaryKey(t *testing.T) {
	st := openTestStore(t)
	const step = int64(123456789)
	const attempts = 16

	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = st.MarkTOTPStepUsed(t.Context(), step, "same-code-hash")
		}(i)
	}
	wg.Wait()

	wins, replays, other := 0, 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, store.ErrTOTPReplay):
			replays++
		default:
			other++
			t.Errorf("unexpected error (not nil, not ErrTOTPReplay): %v", err)
		}
	}

	if wins != 1 {
		t.Errorf("wins = %d, want exactly 1 (the step primary key should admit exactly one writer)", wins)
	}
	if replays != attempts-1 {
		t.Errorf("replays = %d, want %d (every loser should surface store.ErrTOTPReplay)", replays, attempts-1)
	}
	if other != 0 {
		t.Errorf("%d attempt(s) failed with something other than nil or ErrTOTPReplay", other)
	}
}
