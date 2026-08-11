package rpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// --- StreamInterceptor ------------------------------------------------------
//
// The unary interceptor is well covered; the streaming one was not, and it
// gates the two long-lived calls in the system (SampleStream and
// RunService.Watch). A streaming RPC that opens without a session check is a
// hole that no amount of unary-side testing would find.

// stubServerStream is the minimum grpc.ServerStream a handler needs.
type stubServerStream struct {
	ctx context.Context
}

func (s *stubServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *stubServerStream) SendHeader(metadata.MD) error { return nil }
func (s *stubServerStream) SetTrailer(metadata.MD)       {}
func (s *stubServerStream) Context() context.Context     { return s.ctx }
func (s *stubServerStream) SendMsg(any) error            { return nil }
func (s *stubServerStream) RecvMsg(any) error            { return nil }

func TestStreamInterceptorRefusesToOpenWithoutASession(t *testing.T) {
	srv, _, _ := newTestServer(t)
	interceptor := srv.StreamInterceptor()

	called := false
	err := interceptor(nil, &stubServerStream{ctx: t.Context()},
		&grpc.StreamServerInfo{FullMethod: affv1.RunService_Watch_FullMethodName},
		func(any, grpc.ServerStream) error { called = true; return nil },
	)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("opening a stream with no token returned %v, want Unauthenticated", err)
	}
	if called {
		t.Error("the handler ran despite the stream being refused")
	}
}

func TestStreamInterceptorHandsTheSessionToTheHandler(t *testing.T) {
	// The wrapper exists so a streaming handler sees the same caller session
	// a unary one would. Without it, every downstream
	// callerSessionFromContext in a stream fails and the RPC behaves as
	// unauthenticated even though the interceptor let it through.
	srv, st, _ := newTestServer(t)

	raw, hash := issueTestSession(t, st)
	ctx := metadata.NewIncomingContext(withPeerIP(t.Context(), "203.0.113.7"),
		metadata.Pairs(SessionTokenHeader, raw))

	var seen callerSession
	var ok bool
	err := srv.StreamInterceptor()(nil, &stubServerStream{ctx: ctx},
		&grpc.StreamServerInfo{FullMethod: affv1.RunService_Watch_FullMethodName},
		func(_ any, ss grpc.ServerStream) error {
			seen, ok = callerSessionFromContext(ss.Context())
			return nil
		},
	)
	if err != nil {
		t.Fatalf("StreamInterceptor: %v", err)
	}
	if !ok {
		t.Fatal("the handler's stream context carries no caller session")
	}
	if seen.TokenHash != hash {
		t.Errorf("handler saw token hash %q, want %q", seen.TokenHash, hash)
	}
}

func TestStreamInterceptorRefusesARevokedSession(t *testing.T) {
	// §4: revocation takes effect on the very next request, and opening a
	// long-lived stream is exactly the request that must not slip through.
	srv, st, _ := newTestServer(t)

	raw, _ := issueTestSession(t, st)
	if err := st.RevokeAllSessions(t.Context()); err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}

	ctx := metadata.NewIncomingContext(withPeerIP(t.Context(), "203.0.113.7"),
		metadata.Pairs(SessionTokenHeader, raw))
	err := srv.StreamInterceptor()(nil, &stubServerStream{ctx: ctx},
		&grpc.StreamServerInfo{FullMethod: affv1.RunService_Watch_FullMethodName},
		func(any, grpc.ServerStream) error {
			t.Error("a revoked session opened a stream")
			return nil
		},
	)
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("a revoked session returned %v, want Unauthenticated", err)
	}
}

// issueTestSession mints a real session token and stores its hash, returning
// both — the same pair a successful Login produces, without needing a TOTP
// step that a second call in the same 30 seconds would replay.
func issueTestSession(t *testing.T, st sessionStore) (raw, hash string) {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	now := time.Now()
	if _, err := st.CreateSession(t.Context(), auth.Session{
		TokenHash: hash, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		IP: "203.0.113.7", UserAgent: "test",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return raw, hash
}

// sessionStore is the slice of *store.Store issueTestSession needs.
type sessionStore interface {
	CreateSession(context.Context, auth.Session) (int64, error)
}

// --- provider profile validation --------------------------------------------

func TestValidateProfilesRejectsWhatWouldFailAt4am(t *testing.T) {
	// A malformed base URL does not fail loudly at save time — it fails on
	// the next scheduled run, as a provider error nobody can explain.
	cases := []struct {
		name    string
		in      []*affv1.ProviderProfile
		active  string
		wantErr string
	}{
		{
			name:    "unnamed profile",
			in:      []*affv1.ProviderProfile{{BaseUrl: "https://api.openai.com/v1"}},
			wantErr: "needs a name",
		},
		{
			name: "duplicate names",
			in: []*affv1.ProviderProfile{
				{Name: "primary"}, {Name: "primary"},
			},
			wantErr: "both named",
		},
		{
			name:    "relative base URL",
			in:      []*affv1.ProviderProfile{{Name: "p", BaseUrl: "/v1"}},
			wantErr: "absolute",
		},
		{
			name:    "non-http scheme",
			in:      []*affv1.ProviderProfile{{Name: "p", BaseUrl: "ftp://example.com"}},
			wantErr: "http or https",
		},
		{
			name:    "active profile that does not exist",
			in:      []*affv1.ProviderProfile{{Name: "primary"}},
			active:  "secondary",
			wantErr: "not in the list",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := sysValidateProfiles(tc.in, tc.active)
			if err == nil {
				t.Fatalf("want an error containing %q, got profiles %+v", tc.wantErr, out)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateProfilesNormalisesAndDropsDerivedState(t *testing.T) {
	out, err := sysValidateProfiles([]*affv1.ProviderProfile{
		{Name: "  primary  ", BaseUrl: " https://api.openai.com/v1 ", ApiKeyEnv: " SCHEMAFLUX_API_KEY ", KeyPresent: true},
		{Name: "local"}, // no base URL is fine: the default endpoint applies
	}, "primary")
	if err != nil {
		t.Fatalf("sysValidateProfiles: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d profiles, want 2", len(out))
	}
	if out[0].GetName() != "primary" || out[0].GetBaseUrl() != "https://api.openai.com/v1" || out[0].GetApiKeyEnv() != "SCHEMAFLUX_API_KEY" {
		t.Errorf("surrounding whitespace survived: %+v", out[0])
	}
	// KeyPresent is derived from THIS process's environment on every read, so
	// a stored copy is a value that goes stale silently — an operator would
	// see "key present" for a variable that has since been removed.
	if out[0].GetKeyPresent() {
		t.Error("KeyPresent was persisted rather than recomputed on read")
	}
}

func TestValidateProfilesAcceptsAnEmptyList(t *testing.T) {
	out, err := sysValidateProfiles(nil, "")
	if err != nil {
		t.Fatalf("sysValidateProfiles(nil): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d profiles from nil input", len(out))
	}
}
