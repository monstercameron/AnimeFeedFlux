package auth

import (
	"context"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// LoginClient/RecoverClient are the minimal request/response surfaces
// login.go and recover.go depend on. gen/aff/v1 carries no `//go:build`
// tag (checked directly — it is plain generated protobuf/grpc code), so
// these interfaces, and a fake satisfying them, compile and run under
// `go test` on the host exactly as they do under GOOS=js GOARCH=wasm.
//
// affv1.AuthServiceClient (what web/wsconn's Conn.Auth field actually is —
// see that package's doc comment) satisfies both interfaces structurally:
// nothing in this package imports web/wsconn or otherwise depends on how
// the shell wires the tunnel, only on the method shapes it needs. Whoever
// mounts these pages (the shell wave, D0) passes conn.Auth directly as
// both LoginPageProps.Client and RecoverPageProps.Client.
// SetupClient is the one RPC /setup depends on. Like the other two
// interfaces below, affv1.AuthServiceClient satisfies it structurally.
type SetupClient interface {
	Setup(ctx context.Context, in *affv1.AuthServiceSetupRequest, opts ...grpc.CallOption) (*affv1.AuthServiceSetupResponse, error)
}

type LoginClient interface {
	Login(ctx context.Context, in *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error)
}

// RecoverClient covers every RPC the /recover page can reach: consuming a
// recovery code, then EITHER ChangePassword OR ReenrollTOTP (never both —
// see doc.go assumption #3).
type RecoverClient interface {
	RecoverWithCode(ctx context.Context, in *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error)
	ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error)
	ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error)
}
