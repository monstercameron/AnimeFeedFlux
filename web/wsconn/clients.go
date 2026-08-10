//go:build js && wasm

// clients.go wraps every generated affv1 service client Conn exposes
// (Auth, Feed, Item, Run, Sample, System) in a thin guard that refuses an
// RPC with the distinguishable ErrDisconnected sentinel, instead of
// attempting it, whenever the socket is not currently Ready.
//
// Why every method on every client, not just Conn.Guard used ad hoc by
// callers that remember to: PLAN.md's D0-09/D0-10 requirements ("a call
// made while disconnected must fail in a way a page can render honestly —
// not hang forever, and not return a generic error indistinguishable from
// a server rejection" / "queue or refuse mutations while DISCONNECTED —
// never fail silently") apply to every call this package hands a page, not
// only the ones a page author thought to wrap. Routing every method
// through guardCall makes that uniform and impossible to forget from a
// page package, which by design cannot see this package's internals (it
// depends on structural interfaces, per doc.go/deps.go/client.go in
// web/pages/{settings,history,generate}).
//
// This package chose REFUSE, not queue, for mutations: see Conn.Guard's
// doc comment for why (no mutation-replay/dedup semantics exist yet to
// queue correctly). Every wrapped method below refuses identically,
// whether the RPC is a read or a write — refusing a read while
// disconnected is equally honest, and a single rule is easier to reason
// about than a read/write split with no behavioral difference today.
package wsconn

import (
	"context"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// guardCall runs call only if conn is currently Ready, returning the zero
// value of T and ErrDisconnected without attempting the RPC otherwise.
// Generic over the response type so every wrapped method below is a
// one-line call to this rather than its own copy of the same three-line
// branch.
func guardCall[T any](conn *Conn, call func() (T, error)) (T, error) {
	if !conn.Ready() {
		var zero T
		return zero, ErrDisconnected
	}
	return call()
}

// ---------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------

type guardedAuthClient struct {
	conn *Conn
	real affv1.AuthServiceClient
}

var _ affv1.AuthServiceClient = guardedAuthClient{}

func (g guardedAuthClient) Login(ctx context.Context, in *affv1.AuthServiceLoginRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLoginResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceLoginResponse, error) { return g.real.Login(ctx, in, opts...) })
}

func (g guardedAuthClient) RecoverWithCode(ctx context.Context, in *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceRecoverWithCodeResponse, error) {
		return g.real.RecoverWithCode(ctx, in, opts...)
	})
}

func (g guardedAuthClient) Logout(ctx context.Context, in *affv1.AuthServiceLogoutRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLogoutResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceLogoutResponse, error) { return g.real.Logout(ctx, in, opts...) })
}

func (g guardedAuthClient) Session(ctx context.Context, in *affv1.AuthServiceSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceSessionResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceSessionResponse, error) { return g.real.Session(ctx, in, opts...) })
}

func (g guardedAuthClient) ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceChangePasswordResponse, error) {
		return g.real.ChangePassword(ctx, in, opts...)
	})
}

func (g guardedAuthClient) ListSessions(ctx context.Context, in *affv1.AuthServiceListSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceListSessionsResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceListSessionsResponse, error) { return g.real.ListSessions(ctx, in, opts...) })
}

func (g guardedAuthClient) RevokeSession(ctx context.Context, in *affv1.AuthServiceRevokeSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeSessionResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceRevokeSessionResponse, error) { return g.real.RevokeSession(ctx, in, opts...) })
}

func (g guardedAuthClient) RevokeAllSessions(ctx context.Context, in *affv1.AuthServiceRevokeAllSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeAllSessionsResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceRevokeAllSessionsResponse, error) {
		return g.real.RevokeAllSessions(ctx, in, opts...)
	})
}

func (g guardedAuthClient) ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceReenrollTOTPResponse, error) { return g.real.ReenrollTOTP(ctx, in, opts...) })
}

func (g guardedAuthClient) RegenerateRecoveryCodes(ctx context.Context, in *affv1.AuthServiceRegenerateRecoveryCodesRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error) {
	return guardCall(g.conn, func() (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error) {
		return g.real.RegenerateRecoveryCodes(ctx, in, opts...)
	})
}

// ---------------------------------------------------------------------
// Feed
// ---------------------------------------------------------------------

type guardedFeedClient struct {
	conn *Conn
	real affv1.FeedServiceClient
}

var _ affv1.FeedServiceClient = guardedFeedClient{}

func (g guardedFeedClient) List(ctx context.Context, in *affv1.FeedServiceListRequest, opts ...grpc.CallOption) (*affv1.FeedServiceListResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceListResponse, error) { return g.real.List(ctx, in, opts...) })
}

func (g guardedFeedClient) Get(ctx context.Context, in *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedFeedClient) Create(ctx context.Context, in *affv1.FeedServiceCreateRequest, opts ...grpc.CallOption) (*affv1.FeedServiceCreateResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceCreateResponse, error) { return g.real.Create(ctx, in, opts...) })
}

func (g guardedFeedClient) Update(ctx context.Context, in *affv1.FeedServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceUpdateResponse, error) { return g.real.Update(ctx, in, opts...) })
}

func (g guardedFeedClient) SetEnabled(ctx context.Context, in *affv1.FeedServiceSetEnabledRequest, opts ...grpc.CallOption) (*affv1.FeedServiceSetEnabledResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceSetEnabledResponse, error) { return g.real.SetEnabled(ctx, in, opts...) })
}

func (g guardedFeedClient) Delete(ctx context.Context, in *affv1.FeedServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.FeedServiceDeleteResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceDeleteResponse, error) { return g.real.Delete(ctx, in, opts...) })
}

func (g guardedFeedClient) RunNow(ctx context.Context, in *affv1.FeedServiceRunNowRequest, opts ...grpc.CallOption) (*affv1.FeedServiceRunNowResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceRunNowResponse, error) { return g.real.RunNow(ctx, in, opts...) })
}

func (g guardedFeedClient) ValidateSpec(ctx context.Context, in *affv1.FeedServiceValidateSpecRequest, opts ...grpc.CallOption) (*affv1.FeedServiceValidateSpecResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceValidateSpecResponse, error) { return g.real.ValidateSpec(ctx, in, opts...) })
}

func (g guardedFeedClient) SetMembers(ctx context.Context, in *affv1.FeedServiceSetMembersRequest, opts ...grpc.CallOption) (*affv1.FeedServiceSetMembersResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceSetMembersResponse, error) { return g.real.SetMembers(ctx, in, opts...) })
}

func (g guardedFeedClient) ExportTOML(ctx context.Context, in *affv1.FeedServiceExportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceExportTOMLResponse, error) { return g.real.ExportTOML(ctx, in, opts...) })
}

func (g guardedFeedClient) ImportTOML(ctx context.Context, in *affv1.FeedServiceImportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
	return guardCall(g.conn, func() (*affv1.FeedServiceImportTOMLResponse, error) { return g.real.ImportTOML(ctx, in, opts...) })
}

// ---------------------------------------------------------------------
// Item
// ---------------------------------------------------------------------

type guardedItemClient struct {
	conn *Conn
	real affv1.ItemServiceClient
}

var _ affv1.ItemServiceClient = guardedItemClient{}

func (g guardedItemClient) List(ctx context.Context, in *affv1.ItemServiceListRequest, opts ...grpc.CallOption) (*affv1.ItemServiceListResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceListResponse, error) { return g.real.List(ctx, in, opts...) })
}

func (g guardedItemClient) Get(ctx context.Context, in *affv1.ItemServiceGetRequest, opts ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedItemClient) Create(ctx context.Context, in *affv1.ItemServiceCreateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceCreateResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceCreateResponse, error) { return g.real.Create(ctx, in, opts...) })
}

func (g guardedItemClient) Update(ctx context.Context, in *affv1.ItemServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceUpdateResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceUpdateResponse, error) { return g.real.Update(ctx, in, opts...) })
}

func (g guardedItemClient) Delete(ctx context.Context, in *affv1.ItemServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceDeleteResponse, error) { return g.real.Delete(ctx, in, opts...) })
}

func (g guardedItemClient) Restore(ctx context.Context, in *affv1.ItemServiceRestoreRequest, opts ...grpc.CallOption) (*affv1.ItemServiceRestoreResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceRestoreResponse, error) { return g.real.Restore(ctx, in, opts...) })
}

func (g guardedItemClient) PromoteSample(ctx context.Context, in *affv1.ItemServicePromoteSampleRequest, opts ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServicePromoteSampleResponse, error) { return g.real.PromoteSample(ctx, in, opts...) })
}

func (g guardedItemClient) PublishCorrection(ctx context.Context, in *affv1.ItemServicePublishCorrectionRequest, opts ...grpc.CallOption) (*affv1.ItemServicePublishCorrectionResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServicePublishCorrectionResponse, error) {
		return g.real.PublishCorrection(ctx, in, opts...)
	})
}

func (g guardedItemClient) ListRevisions(ctx context.Context, in *affv1.ItemServiceListRevisionsRequest, opts ...grpc.CallOption) (*affv1.ItemServiceListRevisionsResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceListRevisionsResponse, error) { return g.real.ListRevisions(ctx, in, opts...) })
}

func (g guardedItemClient) RevertRevision(ctx context.Context, in *affv1.ItemServiceRevertRevisionRequest, opts ...grpc.CallOption) (*affv1.ItemServiceRevertRevisionResponse, error) {
	return guardCall(g.conn, func() (*affv1.ItemServiceRevertRevisionResponse, error) {
		return g.real.RevertRevision(ctx, in, opts...)
	})
}

// ---------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------

type guardedRunClient struct {
	conn *Conn
	real affv1.RunServiceClient
}

var _ affv1.RunServiceClient = guardedRunClient{}

func (g guardedRunClient) History(ctx context.Context, in *affv1.RunServiceHistoryRequest, opts ...grpc.CallOption) (*affv1.RunServiceHistoryResponse, error) {
	return guardCall(g.conn, func() (*affv1.RunServiceHistoryResponse, error) { return g.real.History(ctx, in, opts...) })
}

func (g guardedRunClient) Get(ctx context.Context, in *affv1.RunServiceGetRequest, opts ...grpc.CallOption) (*affv1.RunServiceGetResponse, error) {
	return guardCall(g.conn, func() (*affv1.RunServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedRunClient) Watch(ctx context.Context, in *affv1.RunServiceWatchRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[affv1.RunServiceWatchResponse], error) {
	return guardCall(g.conn, func() (grpc.ServerStreamingClient[affv1.RunServiceWatchResponse], error) {
		return g.real.Watch(ctx, in, opts...)
	})
}

func (g guardedRunClient) Delete(ctx context.Context, in *affv1.RunServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.RunServiceDeleteResponse, error) {
	return guardCall(g.conn, func() (*affv1.RunServiceDeleteResponse, error) { return g.real.Delete(ctx, in, opts...) })
}

// ---------------------------------------------------------------------
// Sample
// ---------------------------------------------------------------------

type guardedSampleClient struct {
	conn *Conn
	real affv1.SampleServiceClient
}

var _ affv1.SampleServiceClient = guardedSampleClient{}

func (g guardedSampleClient) Sample(ctx context.Context, in *affv1.SampleServiceSampleRequest, opts ...grpc.CallOption) (*affv1.SampleServiceSampleResponse, error) {
	return guardCall(g.conn, func() (*affv1.SampleServiceSampleResponse, error) { return g.real.Sample(ctx, in, opts...) })
}

func (g guardedSampleClient) SampleStream(ctx context.Context, in *affv1.SampleServiceSampleStreamRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[affv1.SampleServiceSampleStreamResponse], error) {
	return guardCall(g.conn, func() (grpc.ServerStreamingClient[affv1.SampleServiceSampleStreamResponse], error) {
		return g.real.SampleStream(ctx, in, opts...)
	})
}

func (g guardedSampleClient) ListSamples(ctx context.Context, in *affv1.SampleServiceListSamplesRequest, opts ...grpc.CallOption) (*affv1.SampleServiceListSamplesResponse, error) {
	return guardCall(g.conn, func() (*affv1.SampleServiceListSamplesResponse, error) { return g.real.ListSamples(ctx, in, opts...) })
}

func (g guardedSampleClient) DiscardSample(ctx context.Context, in *affv1.SampleServiceDiscardSampleRequest, opts ...grpc.CallOption) (*affv1.SampleServiceDiscardSampleResponse, error) {
	return guardCall(g.conn, func() (*affv1.SampleServiceDiscardSampleResponse, error) {
		return g.real.DiscardSample(ctx, in, opts...)
	})
}

// ---------------------------------------------------------------------
// System
// ---------------------------------------------------------------------

type guardedSystemClient struct {
	conn *Conn
	real affv1.SystemServiceClient
}

var _ affv1.SystemServiceClient = guardedSystemClient{}

func (g guardedSystemClient) Stats(ctx context.Context, in *affv1.SystemServiceStatsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceStatsResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceStatsResponse, error) { return g.real.Stats(ctx, in, opts...) })
}

func (g guardedSystemClient) SetGenerationEnabled(ctx context.Context, in *affv1.SystemServiceSetGenerationEnabledRequest, opts ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
		return g.real.SetGenerationEnabled(ctx, in, opts...)
	})
}

func (g guardedSystemClient) GetSettings(ctx context.Context, in *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceGetSettingsResponse, error) { return g.real.GetSettings(ctx, in, opts...) })
}

func (g guardedSystemClient) UpdateSettings(ctx context.Context, in *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceUpdateSettingsResponse, error) {
		return g.real.UpdateSettings(ctx, in, opts...)
	})
}

func (g guardedSystemClient) Version(ctx context.Context, in *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceVersionResponse, error) { return g.real.Version(ctx, in, opts...) })
}

func (g guardedSystemClient) Backup(ctx context.Context, in *affv1.SystemServiceBackupRequest, opts ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
	return guardCall(g.conn, func() (*affv1.SystemServiceBackupResponse, error) { return g.real.Backup(ctx, in, opts...) })
}
