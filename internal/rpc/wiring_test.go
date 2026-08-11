package rpc

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
)

func TestPasswordPepperFromEnvRefusesAHalfConfiguredPepper(t *testing.T) {
	// Both variables or neither. A pepper with no usable version would be
	// mixed into new hashes and recorded as generation 0, i.e. "not
	// peppered" — and every one of those rows would then fail to verify.
	cases := []struct {
		name    string
		env     map[string]string
		wantOK  bool
		wantVer int
	}{
		{"neither set", map[string]string{}, false, 0},
		{"key with no version", map[string]string{"AFF_PASSWORD_PEPPER": "secret"}, false, 0},
		{"key with a non-numeric version", map[string]string{
			"AFF_PASSWORD_PEPPER": "secret", "AFF_PASSWORD_PEPPER_VERSION": "one",
		}, false, 0},
		{"key with version zero", map[string]string{
			"AFF_PASSWORD_PEPPER": "secret", "AFF_PASSWORD_PEPPER_VERSION": "0",
		}, false, 0},
		{"key with a negative version", map[string]string{
			"AFF_PASSWORD_PEPPER": "secret", "AFF_PASSWORD_PEPPER_VERSION": "-1",
		}, false, 0},
		{"version with no key", map[string]string{"AFF_PASSWORD_PEPPER_VERSION": "2"}, false, 0},
		{"whitespace-only key", map[string]string{
			"AFF_PASSWORD_PEPPER": "   ", "AFF_PASSWORD_PEPPER_VERSION": "2",
		}, false, 0},
		{"both set", map[string]string{
			"AFF_PASSWORD_PEPPER": "  secret  ", "AFF_PASSWORD_PEPPER_VERSION": " 2 ",
		}, true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, version, ok := passwordPepperFromEnv(sysFakeGetenv(tc.env))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if version != tc.wantVer {
				t.Errorf("version = %d, want %d", version, tc.wantVer)
			}
			if !tc.wantOK && len(key) != 0 {
				t.Errorf("a rejected configuration still returned a key of %d bytes", len(key))
			}
			if tc.wantOK && string(key) != "secret" {
				t.Errorf("key = %q, want the trimmed value", key)
			}
		})
	}
}

func TestAuthServerOptionsApply(t *testing.T) {
	st := openTestStore(t)

	tickets := bridge.NewTicketStore()
	srv, err := NewAuthServer(st, testSecretKey, WithTicketStore(tickets), WithDevInsecureAuth())
	if err != nil {
		t.Fatalf("NewAuthServer: %v", err)
	}
	if srv.tickets != tickets {
		t.Error("WithTicketStore did not take effect")
	}
	if !srv.devInsecureAuth {
		t.Error("WithDevInsecureAuth did not take effect")
	}

	// The defaults matter as much: a server built without the options must
	// have replay rejection and backoff ON, since that is what every real
	// deployment gets.
	plain, err := NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("NewAuthServer: %v", err)
	}
	if plain.devInsecureAuth {
		t.Error("a default server has the insecure dev mode enabled")
	}
	if plain.tickets != nil {
		t.Error("a default server has a ticket store it was never given")
	}
}

func TestFeedRequireFeedBySlugDistinguishesMissingFromBroken(t *testing.T) {
	// NotFound is a normal answer the CLI prints as "no such feed"; Internal
	// means something is wrong with the database. Collapsing the two would
	// have an operator hunting for a typo during an outage.
	st := feedOpenTestStore(t)
	s := NewFeedServer(st, nil, nil)

	created := mustCreateFeed(t, s, feedTestFeed("trivia-daily"))

	rec, err := s.feedRequireFeedBySlug(t.Context(), "trivia-daily")
	if err != nil {
		t.Fatalf("feedRequireFeedBySlug: %v", err)
	}
	if rec.ID != created.GetId() || rec.Slug != "trivia-daily" {
		t.Errorf("record = %+v, want the created feed", rec)
	}

	_, err = s.feedRequireFeedBySlug(t.Context(), "never-existed")
	if status.Code(err) != codes.NotFound {
		t.Errorf("an unknown slug returned %v, want NotFound", err)
	}

	// A soft-deleted feed is gone as far as this lookup is concerned — its
	// row survives so its items keep 410ing, but the recipe is not addressable.
	if _, err := s.Delete(t.Context(), &affv1.FeedServiceDeleteRequest{
		FeedId: created.GetId(), ExpectedVersion: created.GetVersion(),
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.feedRequireFeedBySlug(t.Context(), "trivia-daily"); status.Code(err) != codes.NotFound {
		t.Errorf("a deleted feed resolved by slug: %v", err)
	}
}

func TestRunGetReportsAnUnknownRun(t *testing.T) {
	s := runTestStore(t)
	srv := NewRunServer(s, nil)

	if _, err := srv.Get(t.Context(), &affv1.RunServiceGetRequest{RunId: 99999}); err == nil {
		t.Fatal("Get returned a run that does not exist")
	} else if status.Code(err) != codes.NotFound {
		t.Errorf("code = %v, want NotFound", status.Code(err))
	}

	ctx := t.Context()
	feedID := runTestFeed(t, s, "trivia")
	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	resp, err := srv.Get(ctx, &affv1.RunServiceGetRequest{RunId: runID})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.GetRun().GetId() != runID || resp.GetRun().GetFeedId() != feedID {
		t.Errorf("run = %+v", resp.GetRun())
	}
	if resp.GetRun().GetStatus() != affv1.RunStatus_RUN_STATUS_RUNNING {
		t.Errorf("status = %v, want RUNNING", resp.GetRun().GetStatus())
	}
}

func TestLoginOverTheBridgeHandsBackATicketNotTheRawToken(t *testing.T) {
	// §4: the raw session token never touches JavaScript or WASM. A caller on
	// the bridge transport is a browser, so it gets a single-use TICKET it can
	// redeem over the socket; a caller on the plain gRPC listener (cmd/aff,
	// the e2e suite) is a trusted local process reading its own response and
	// keeps getting the token itself.
	st := openTestStore(t)
	secret := seedAdminWithTOTP(t, st)
	tickets := bridge.NewTicketStore()

	srv, err := NewAuthServer(st, testSecretKey, WithTicketStore(tickets))
	if err != nil {
		t.Fatalf("NewAuthServer: %v", err)
	}

	// A bridge-transport call: internal/bridge attaches a Session to every
	// request context it serves, anonymous ones included, and its mere
	// presence is the "this is a browser" signal.
	ctx, fts := withTransportStream(
		bridge.WithSession(withPeerIP(t.Context(), "203.0.113.7"), bridge.Session{}),
		affv1.AuthService_Login_FullMethodName)

	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}

	rawHeaders := fts.header.Get(SessionTokenHeader)
	ticketHeaders := fts.header.Get(SessionTicketHeader)
	if len(rawHeaders) != 0 {
		t.Errorf("the raw session token was sent to a browser transport: %v", rawHeaders)
	}
	if len(ticketHeaders) != 1 || ticketHeaders[0] == "" {
		t.Fatalf("no login ticket was issued: %v", ticketHeaders)
	}

	// The ticket is redeemable exactly once, and what it redeems to is a
	// session token that actually works.
	raw, ok := tickets.Redeem(time.Now(), ticketHeaders[0])
	if !ok || raw == "" {
		t.Fatal("the issued ticket did not redeem")
	}
	if _, ok := tickets.Redeem(time.Now(), ticketHeaders[0]); ok {
		t.Error("the ticket redeemed a second time")
	}
	if _, err := st.GetSessionByTokenHash(t.Context(), auth.HashToken(raw)); err != nil {
		t.Errorf("the redeemed token does not match a stored session: %v", err)
	}
}

func TestLoginOverTheBridgeWithNoTicketStoreStillSucceeds(t *testing.T) {
	// A server built without a ticket store is a degraded state, not a
	// failure: the session is committed and valid, the browser simply cannot
	// upgrade its socket and has to retry. Failing the login instead would
	// turn a wiring omission into "nobody can sign in".
	st := openTestStore(t)
	secret := seedAdminWithTOTP(t, st)

	srv, err := NewAuthServer(st, testSecretKey)
	if err != nil {
		t.Fatalf("NewAuthServer: %v", err)
	}
	ctx, fts := withTransportStream(
		bridge.WithSession(withPeerIP(t.Context(), "203.0.113.7"), bridge.Session{}),
		affv1.AuthService_Login_FullMethodName)

	if _, err := srv.Login(ctx, &affv1.AuthServiceLoginRequest{
		Password: testPassword,
		TotpCode: validCode(t, secret, time.Now()),
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := fts.header.Get(SessionTokenHeader); len(got) != 0 {
		t.Errorf("the raw token leaked to a browser transport: %v", got)
	}
	if got := fts.header.Get(SessionTicketHeader); len(got) != 0 {
		t.Errorf("a ticket was issued with no ticket store: %v", got)
	}
}
