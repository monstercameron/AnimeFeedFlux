//go:build js && wasm

// conn_js_test.go is js-only (like conn.go/clients.go themselves), so
// `go test ./web/...` on the host skips it entirely — it is exercised only
// by `GOOS=js GOARCH=wasm go test -c ./web/wsconn/...` (compiles a wasm
// test binary; running it needs a JS host this repo's toolchain does not
// wire up, so this is a COMPILE-time check only, same as the assertions
// themselves). What that leaves genuinely untested on this task: the
// actual dial/handshake against a live bridge, watchConnectivity's
// EvWSDropped/EvWSReconnected transitions off a real *grpc.ClientConn, and
// the guarded clients' behavior once Ready() is true (this file can only
// force Ready() false, via a zero-value Conn whose cc is nil — Ready
// short-circuits on cc == nil before ever calling cc.GetState(), so no
// real connection is required to exercise the "refuse" path below).
package wsconn

import (
	"context"
	"errors"
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc"
)

// --- local copies of each page's declared interface --------------------
//
// These are copy-pasted from the page packages named in this task's
// instructions (web/pages/settings/client.go, web/pages/history/client.go,
// web/pages/generate/deps.go) rather than imported from them, because
// importing a page package from here would invert the dependency this
// package is deliberately on the wrong side of (pages depend on
// interfaces; this package must not depend on pages). CONSEQUENCE: if any
// of those three files' interfaces change — a method added, removed, or
// its signature edited — this file's copies go stale silently; nothing
// forces them back into sync automatically. Whoever next edits one of
// those three files' interfaces should grep this file too.

// localSettingsSystemClient mirrors web/pages/settings/client.go's
// SystemClient exactly (as of this change).
type localSettingsSystemClient interface {
	Stats(ctx context.Context, in *affv1.SystemServiceStatsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error)
	SetGenerationEnabled(ctx context.Context, in *affv1.SystemServiceSetGenerationEnabledRequest, opts ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error)
	GetSettings(ctx context.Context, in *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error)
	UpdateSettings(ctx context.Context, in *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error)
	Version(ctx context.Context, in *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error)
	Backup(ctx context.Context, in *affv1.SystemServiceBackupRequest, opts ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error)
}

// localSettingsAuthClient mirrors web/pages/settings/client.go's
// AuthClient exactly (as of this change).
type localSettingsAuthClient interface {
	ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error)
	ListSessions(ctx context.Context, in *affv1.AuthServiceListSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceListSessionsResponse, error)
	RevokeSession(ctx context.Context, in *affv1.AuthServiceRevokeSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeSessionResponse, error)
	RevokeAllSessions(ctx context.Context, in *affv1.AuthServiceRevokeAllSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeAllSessionsResponse, error)
	ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error)
	RegenerateRecoveryCodes(ctx context.Context, in *affv1.AuthServiceRegenerateRecoveryCodesRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error)
}

// localSettingsFeedClient mirrors web/pages/settings/client.go's
// FeedClient exactly (as of this change).
type localSettingsFeedClient interface {
	List(ctx context.Context, in *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error)
	ExportTOML(ctx context.Context, in *affv1.FeedServiceExportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error)
	ImportTOML(ctx context.Context, in *affv1.FeedServiceImportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error)
}

// localHistoryRunsClient/localHistoryItemsClient mirror
// web/pages/history/client.go's RunsClient/ItemsClient, which are
// themselves plain type aliases to the full generated interfaces — copied
// as aliases here too so a future change to history/client.go narrowing
// them to a hand-picked method subset (instead of the full generated
// interface) is still caught by the same mechanism as the other four.
type localHistoryRunsClient = affv1.RunServiceClient
type localHistoryItemsClient = affv1.ItemServiceClient

// localGenerateFeedClient etc. mirror web/pages/generate/deps.go's Deps
// struct fields, which are likewise the bare generated interfaces, not
// hand-picked subsets.
type localGenerateFeedClient = affv1.FeedServiceClient
type localGenerateSampleClient = affv1.SampleServiceClient
type localGenerateRunClient = affv1.RunServiceClient
type localGenerateSystemClient = affv1.SystemServiceClient
type localGenerateItemClient = affv1.ItemServiceClient

// connZeroForAssertions is a zero-value Conn (cc == nil, never Connect-ed)
// used only so the field-type assertions below have a Conn value to read
// fields from without dialing anything. Reading a struct field off a
// value (not a nil pointer) never panics, so this is safe at package-init
// time even though the Conn was never wired to a real connection.
var connZeroForAssertions Conn

// Compile-time assertions that every field Conn exposes structurally
// satisfies the corresponding page-declared interface, without this
// package importing the page package that declares it.
var (
	_ localSettingsSystemClient = connZeroForAssertions.System
	_ localSettingsAuthClient   = connZeroForAssertions.Auth
	_ localSettingsFeedClient   = connZeroForAssertions.Feed

	_ localHistoryRunsClient  = connZeroForAssertions.Run
	_ localHistoryItemsClient = connZeroForAssertions.Item

	_ localGenerateFeedClient   = connZeroForAssertions.Feed
	_ localGenerateSampleClient = connZeroForAssertions.Sample
	_ localGenerateRunClient    = connZeroForAssertions.Run
	_ localGenerateSystemClient = connZeroForAssertions.System
	_ localGenerateItemClient   = connZeroForAssertions.Item
)

// TestGuardRefusesWhenDisconnected exercises Guard directly: a zero-value
// Conn (cc == nil) is never Ready, so every call must be refused with the
// distinguishable ErrDisconnected sentinel rather than attempting fn (which
// would nil-pointer-panic here if actually invoked, since it closes over
// nothing usable) or hanging.
func TestGuardRefusesWhenDisconnected(t *testing.T) {
	c := &Conn{}
	called := false
	err := c.Guard(func() error {
		called = true
		return nil
	})
	if called {
		t.Fatal("Guard invoked fn while disconnected; want refused before attempting")
	}
	if !errors.Is(err, ErrDisconnected) {
		t.Fatalf("Guard error = %v, want errors.Is(err, ErrDisconnected)", err)
	}
}

// TestGuardedClientRefusesWhenDisconnected exercises the same "refuse, not
// hang, distinguishable error" contract through guardCall directly — the
// mechanism every one of the six guarded*Client wrapper types (clients.go)
// routes every single RPC method through, not just the ones a page author
// remembers to wrap in Guard.
func TestGuardedClientRefusesWhenDisconnected(t *testing.T) {
	c := &Conn{}
	called := false
	_, err := guardCall(c, func() (int, error) {
		called = true
		return 0, nil
	})
	if called {
		t.Fatal("guardCall invoked the RPC while disconnected; want refused before attempting")
	}
	if !errors.Is(err, ErrDisconnected) {
		t.Fatalf("guardCall error = %v, want errors.Is(err, ErrDisconnected)", err)
	}
}
