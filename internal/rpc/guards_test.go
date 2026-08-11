package rpc

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// nopGenStore satisfies smpGenStore without a database — these tests are
// about the wrapper's refusal, not about what it wraps.
type nopGenStore struct{}

func (nopGenStore) RecentTitles(context.Context, int64, int) ([]string, error) { return nil, nil }
func (nopGenStore) NewestPublished(context.Context, int64) (time.Time, error) {
	return time.Time{}, nil
}
func (nopGenStore) CommitRun(context.Context, generate.RunRecord, []model.Item) error { return nil }

func TestSamplingCanNeverPublish(t *testing.T) {
	// A sample that publishes is the single worst failure this surface can
	// have: the operator is previewing, and the feed changes under real
	// subscribers. generate.Sample's own contract already never calls
	// CommitRun — this wrapper is the second, independent line of defense,
	// and it fails loudly rather than discarding the call silently, so a
	// regression cannot pass as a no-op.
	var counter atomic.Int64
	g := smpGenerateStore{inner: nopGenStore{}, counter: &counter}

	err := g.CommitRun(t.Context(), generate.RunRecord{FeedID: 7}, []model.Item{{ItemKey: "k"}})
	if err == nil {
		t.Fatal("the sampling wrapper accepted a CommitRun")
	}
	if !strings.Contains(err.Error(), "must never happen") {
		t.Errorf("the refusal does not say what went wrong: %v", err)
	}
	if got := counter.Load(); got != 1 {
		t.Errorf("commitRunCalls = %d, want 1 — an unrecorded attempt is an invisible one", got)
	}

	// The read-only halves pass straight through: the guard is on writes.
	if _, err := g.RecentTitles(t.Context(), 7, 5); err != nil {
		t.Errorf("RecentTitles was blocked: %v", err)
	}
	if _, err := g.NewestPublished(t.Context(), 7); err != nil {
		t.Errorf("NewestPublished was blocked: %v", err)
	}
}

func TestDevInsecureAuthStillRequiresRealCredentials(t *testing.T) {
	// The option drops TOTP replay rejection and login backoff. It must not
	// drop authentication: "a dev box is as strong as password + a valid
	// TOTP, with no rate limit" is the documented blast radius, and anything
	// weaker would make a copied config catastrophic rather than merely bad.
	st := openTestStore(t)
	secret := seedAdminWithTOTP(t, st)

	srv, err := NewAuthServer(st, testSecretKey, WithDevInsecureAuth())
	if err != nil {
		t.Fatalf("NewAuthServer: %v", err)
	}

	ctx, _ := withTransportStream(withPeerIP(t.Context(), "127.0.0.1"), affv1.AuthService_Login_FullMethodName)

	// Wrong password: still refused.
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: "not the password", TotpCode: validCode(t, secret, time.Now()),
	}); err == nil {
		t.Fatal("dev-insecure auth accepted a wrong password")
	}

	// Wrong TOTP: still refused.
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword, TotpCode: "000000",
	}); err == nil {
		t.Fatal("dev-insecure auth accepted a wrong TOTP code")
	}

	// Correct credentials twice in the same step: accepted, because replay
	// rejection is exactly what this option removes.
	code := validCode(t, secret, time.Now())
	for i := range 2 {
		if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
			Password: testPassword, TotpCode: code,
		}); err != nil {
			t.Fatalf("login %d with correct credentials failed: %v", i+1, err)
		}
	}
}

func TestReplayIsRejectedWithoutTheDevOption(t *testing.T) {
	// The other half of the pair above: with the default configuration, the
	// second use of the same code inside its window is refused. Without this
	// assertion the test above could pass against a server that never
	// rejected replays at all.
	srv, _, secret := newTestServer(t)
	ctx, _ := withTransportStream(withPeerIP(t.Context(), "203.0.113.9"), affv1.AuthService_Login_FullMethodName)

	code := validCode(t, secret, time.Now())
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code}); err != nil {
		t.Fatalf("first login: %v", err)
	}
	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{Password: testPassword, TotpCode: code}); err == nil {
		t.Fatal("a replayed TOTP code was accepted")
	}
}

// seedAdminWithTOTP enrolls an admin at testPassword with a known TOTP
// secret — the same fixture newTestServer builds, exposed separately so a
// test can construct the server with its own options.
func seedAdminWithTOTP(t *testing.T, st *store.Store) string {
	t.Helper()
	hash, err := auth.Hash(testPassword, auth.DefaultParams())
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := st.InitAdmin(t.Context(), hash, kdfParamsString(auth.DefaultParams())); err != nil {
		t.Fatalf("InitAdmin: %v", err)
	}
	secret, _, err := auth.Enroll("admin", "AnimeFeedFlux-test")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	enc, err := auth.EncryptSecret(secret, testSecretKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := st.SetTOTPSecret(t.Context(), enc); err != nil {
		t.Fatalf("SetTOTPSecret: %v", err)
	}
	return secret
}
