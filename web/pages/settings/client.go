package settings

import (
	"context"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"google.golang.org/grpc"
)

// SystemClient, AuthClient, and FeedClient are the minimal per-service RPC
// surfaces this page depends on, satisfied structurally by
// affv1.SystemServiceClient/AuthServiceClient/FeedServiceClient (gen/aff/v1
// carries no build tag, so it is safe to depend on from this package's
// host-testable half too) WITHOUT this package importing web/wsconn —
// doc.go explains why that import is unavailable to this page as things
// stand. deps.go's Init wires the concrete *grpc.ClientConn-backed
// clients in; tests use hand-written fakes.
//
// Each interface lists only the RPCs this page's sections actually call,
// not the full generated service — Provider/Generation/Publishing/About
// happen to need every SystemService method, but Security only needs six
// of AuthService's ten, and Data only needs three of FeedService's ten.

// SystemClient backs Provider, Generation, Publishing, and About
// (PLAN.md §12.5).
type SystemClient interface {
	Stats(ctx context.Context, in *affv1.SystemServiceStatsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error)
	SetGenerationEnabled(ctx context.Context, in *affv1.SystemServiceSetGenerationEnabledRequest, opts ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error)
	GetSettings(ctx context.Context, in *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error)
	// ListModels hydrates the two model fields from the provider itself —
	// see modelselect.go for why a typed model id is a per-feed outage
	// waiting to happen, and for what happens when this call cannot be made.
	ListModels(ctx context.Context, in *affv1.SystemServiceListModelsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceListModelsResponse, error)
	// CostHistory backs the spend chart (costchart.go).
	CostHistory(ctx context.Context, in *affv1.SystemServiceCostHistoryRequest, opts ...grpc.CallOption) (*affv1.SystemServiceCostHistoryResponse, error)
	UpdateSettings(ctx context.Context, in *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error)
	Version(ctx context.Context, in *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error)
	Backup(ctx context.Context, in *affv1.SystemServiceBackupRequest, opts ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error)
	// Vacuum runs SQLite's VACUUM against the live database (D4-10). It
	// BLOCKS for a real, size-dependent duration and refuses outright
	// (codes.FailedPrecondition) while a generation run is in flight — see
	// internal/rpc/system.go's Vacuum doc comment.
	Vacuum(ctx context.Context, in *affv1.SystemServiceVacuumRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVacuumResponse, error)
}

// AuthClient backs the Security section (PLAN.md §12.5): change
// password, TOTP re-enrollment, recovery codes, session list/revoke.
// Login/RecoverWithCode/Logout/Session belong to the /login and /recover
// pages, not here.
type AuthClient interface {
	ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error)
	// Session is called here for exactly one field: RemainingRecoveryCodes.
	// §12.5 requires the Security panel to show the remaining count, and
	// this is the only read path for it — see auth.proto's field comment for
	// why it rides on the session lookup rather than getting its own RPC.
	Session(ctx context.Context, in *affv1.AuthServiceSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceSessionResponse, error)
	ListSessions(ctx context.Context, in *affv1.AuthServiceListSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceListSessionsResponse, error)
	RevokeSession(ctx context.Context, in *affv1.AuthServiceRevokeSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeSessionResponse, error)
	RevokeAllSessions(ctx context.Context, in *affv1.AuthServiceRevokeAllSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeAllSessionsResponse, error)
	ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error)
	RegenerateRecoveryCodes(ctx context.Context, in *affv1.AuthServiceRegenerateRecoveryCodesRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error)
}

// FeedClient backs the Data section's per-feed TOML export/import
// (D4-10; doc.go assumption #2 explains why this is per-feed, not a
// single whole-database export) and the feed picker that selects which
// feed's recipe is being exported or imported.
type FeedClient interface {
	List(ctx context.Context, in *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error)
	ExportTOML(ctx context.Context, in *affv1.FeedServiceExportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error)
	ImportTOML(ctx context.Context, in *affv1.FeedServiceImportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error)
}

// Compile-time assertions that the generated clients satisfy these
// interfaces structurally, so a future gen/aff/v1 regeneration that
// removes a method this page depends on fails the build here rather than
// only at the eventual Init(Deps) call site.
var (
	_ SystemClient = affv1.SystemServiceClient(nil)
	_ AuthClient   = affv1.AuthServiceClient(nil)
	_ FeedClient   = affv1.FeedServiceClient(nil)
)
