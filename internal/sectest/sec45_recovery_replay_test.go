package sectest

import (
	"errors"
	"sync"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// SEC-45: a single-use recovery code (PLAN.md §12.2 — one of only two ways
// back into the account) must not be usable twice, even under concurrent
// attempts. A recovery code that survives a race becomes a standing
// backdoor, not a one-time escape hatch.

// TestRecoveryCode_ConcurrentUseOnlyOneSucceeds drives the race through the
// real RecoverWithCode RPC: many goroutines present the SAME plaintext code
// simultaneously.
func TestRecoveryCode_ConcurrentUseOnlyOneSucceeds(t *testing.T) {
	srv, st, _ := newTestServer(t)
	plain, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := withPeerIP(t.Context(), ipFor("recoveryrace", i))
			_, err := srv.RecoverWithCode(ctx, &affv1.AuthServiceRecoverWithCodeRequest{RecoveryCode: plain[0]})
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
		t.Fatalf("exactly one concurrent use of the same recovery code should succeed, got %d out of %d", won, attempts)
	}
}

// TestRecoveryCode_ConcurrentUseAtStoreLayer isolates the mechanism: many
// goroutines call store.UseRecoveryCode for the SAME index concurrently.
// UseRecoveryCode's `WHERE used_at IS NULL` makes only one UPDATE actually
// flip a row from unused to used; every other caller sees RowsAffected = 0
// and gets store.ErrRecoveryCodeUsed even if its SELECT ran before the
// winner's UPDATE committed (see UseRecoveryCode's doc comment) — this test
// runs enough concurrent attempts that a plain check-then-update without the
// WHERE guard would eventually let more than one through.
func TestRecoveryCode_ConcurrentUseAtStoreLayer(t *testing.T) {
	st := openTestStore(t)
	_, hashes, err := auth.GenerateCodes(1)
	if err != nil {
		t.Fatalf("generate codes: %v", err)
	}
	if err := st.StoreRecoveryCodes(t.Context(), hashes); err != nil {
		t.Fatalf("store recovery codes: %v", err)
	}

	const attempts = 16
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = st.UseRecoveryCode(t.Context(), 0)
		}(i)
	}
	wg.Wait()

	wins, used, other := 0, 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, store.ErrRecoveryCodeUsed):
			used++
		default:
			other++
			t.Errorf("unexpected error (not nil, not ErrRecoveryCodeUsed): %v", err)
		}
	}

	if wins != 1 {
		t.Errorf("wins = %d, want exactly 1", wins)
	}
	if used != attempts-1 {
		t.Errorf("ErrRecoveryCodeUsed count = %d, want %d", used, attempts-1)
	}
	if other != 0 {
		t.Errorf("%d attempt(s) failed with something other than nil or ErrRecoveryCodeUsed", other)
	}
}
