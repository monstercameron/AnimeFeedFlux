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

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
	"time"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// unaryTimeout bounds every non-streaming RPC this package hands a page.
//
// This exists because the promise at the top of this file — "not hang
// forever" — was only half kept. Conn.Usable deliberately attempts calls
// while the ClientConn is Idle or Connecting (refusing them is what broke
// login; see its doc comment), and gRPC will then park a call on a
// connection that is still coming up. When that connection never arrives —
// a socket replaced by a reconnect, a handshake that stalls — the call has
// no deadline of its own and waits forever. Every page in this app reached
// that state at once: /history and /generate both mount their loaders
// immediately on a hard page load, before the socket is up, and both sat on
// "Loading…" with no error and no timeout, which is precisely the "blank
// box on failure" PLAN.md §12.6 calls out as how a stale feed goes unnoticed
// for a week.
//
// 30s is chosen to be far longer than any healthy control-plane call (the
// slowest, ListModels, bounds itself server-side at 30s and returns
// "unavailable" rather than an error) and far shorter than a human's
// patience with a spinner.
const unaryTimeout = 30 * time.Second

// generationTimeout is the exception: SampleService.Sample runs a real
// provider generation, with a repair attempt and up to five candidates, so
// it is legitimately minutes long. Bounded anyway — an unbounded call is
// the bug this file is fixing — just bounded where a generation actually
// gives up rather than where a page load does.
const generationTimeout = 10 * time.Minute

// guardUnary is guardCall for non-streaming RPCs: same disconnected refusal,
// plus a deadline so a call that gets parked on a connection that never
// arrives fails with a real error instead of spinning forever.
func guardUnary[T any](conn *Conn, ctx context.Context, timeout time.Duration, call func(context.Context) (T, error)) (T, error) {
	if !conn.Usable() {
		var zero T
		return zero, ErrDisconnected
	}
	// WithTimeout takes the earlier of the two deadlines, so a caller that
	// set its own shorter one keeps it.
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for the transport to be READY before invoking.
	//
	// Conn.Usable answers "should this be attempted", and it answers yes while
	// the ClientConn is still Idle or Connecting — deliberately, because
	// refusing there is what broke login (see its doc comment). But a unary
	// call issued in that window is handed to a transport that is still coming
	// up.
	//
	// WaitReady is bounded by callCtx, so this cannot itself hang.
	if err := conn.WaitReady(callCtx); err != nil {
		var zero T
		return zero, ErrDisconnected
	}

	// The watchdog, and why the ctx deadline above is not enough.
	//
	// A per-call gRPC deadline assumes the transport can be interrupted. This
	// one cannot: GoGRPCBridge's browser WebSocket conn documents its own
	// SetDeadline/SetReadDeadline/SetWriteDeadline as NO-OPS ("browser
	// WebSockets expose no deadline controls"), so a call that gets wedged in
	// the transport is not waiting on anything a context can cancel.
	//
	// This was not theoretical. /history's first load, issued moments after
	// login while the tunnel was being replaced with the authenticated one,
	// never returned — not in 5 seconds, not in 45, with a 5-second context
	// deadline on it the whole time. The identical call from a button click
	// two seconds later returned 25 runs instantly. The page sat on "Loading…"
	// forever with no error to show and no way to know anything was wrong.
	//
	// So the timeout is enforced HERE, where it can be: the call runs in its
	// own goroutine and this one stops waiting when the clock says so.
	//
	// The wedged goroutine is leaked, and that is a real cost, stated plainly
	// rather than hidden: it holds its request and response until the
	// transport gives up on it, if it ever does. It is the lesser cost. The
	// alternative is what this replaced — a UI that waits forever on a reply
	// that is never coming. The underlying transport wedge is filed as its own
	// ticket; this keeps the app honest until that is fixed.
	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := call(callCtx)
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			// A call that just succeeded is proof the socket is up. The state
			// machine only learns about reconnection from the connectivity
			// watcher, and if it misses the transition — a socket replaced
			// during startup, an Idle→Ready hop it does not treat as a
			// reconnect — the app sits in DISCONNECTED forever: every page
			// showing "Reconnecting to the server", every Save button
			// disabled, while its data loads perfectly. Observed on a cold
			// load of /settings, permanent across 30 seconds.
			//
			// EvWSReconnected is a no-op from any state except DISCONNECTED
			// (see appstate.Next), so this is a correction, never a
			// transition on its own.
			conn.emit(appstate.EvWSReconnected)
		}
		return r.val, r.err
	case <-time.After(timeout):
		var zero T
		return zero, ErrDisconnected
	}
}

// guardCall runs call only if conn is currently Ready, returning the zero
// value of T and ErrDisconnected without attempting the RPC otherwise.
// Generic over the response type so every wrapped method below is a
// one-line call to this rather than its own copy of the same three-line
// branch.
func guardCall[T any](conn *Conn, call func() (T, error)) (T, error) {
	// Usable, not Ready — see Conn.Usable. Gating on Ready refused the very
	// first RPC, which is what would have connected the lazy ClientConn, so
	// login could never succeed.
	if !conn.Usable() {
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
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceLoginResponse, error) {
		return g.real.Login(ctx, in, opts...)
	})
}

func (g guardedAuthClient) RecoverWithCode(ctx context.Context, in *affv1.AuthServiceRecoverWithCodeRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceRecoverWithCodeResponse, error) {
		return g.real.RecoverWithCode(ctx, in, opts...)
	})
}

func (g guardedAuthClient) Logout(ctx context.Context, in *affv1.AuthServiceLogoutRequest, opts ...grpc.CallOption) (*affv1.AuthServiceLogoutResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceLogoutResponse, error) {
		return g.real.Logout(ctx, in, opts...)
	})
}

func (g guardedAuthClient) Session(ctx context.Context, in *affv1.AuthServiceSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceSessionResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceSessionResponse, error) {
		return g.real.Session(ctx, in, opts...)
	})
}

func (g guardedAuthClient) ChangePassword(ctx context.Context, in *affv1.AuthServiceChangePasswordRequest, opts ...grpc.CallOption) (*affv1.AuthServiceChangePasswordResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceChangePasswordResponse, error) {
		return g.real.ChangePassword(ctx, in, opts...)
	})
}

func (g guardedAuthClient) ListSessions(ctx context.Context, in *affv1.AuthServiceListSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceListSessionsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceListSessionsResponse, error) {
		return g.real.ListSessions(ctx, in, opts...)
	})
}

func (g guardedAuthClient) RevokeSession(ctx context.Context, in *affv1.AuthServiceRevokeSessionRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeSessionResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceRevokeSessionResponse, error) {
		return g.real.RevokeSession(ctx, in, opts...)
	})
}

func (g guardedAuthClient) RevokeAllSessions(ctx context.Context, in *affv1.AuthServiceRevokeAllSessionsRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRevokeAllSessionsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceRevokeAllSessionsResponse, error) {
		return g.real.RevokeAllSessions(ctx, in, opts...)
	})
}

func (g guardedAuthClient) ReenrollTOTP(ctx context.Context, in *affv1.AuthServiceReenrollTOTPRequest, opts ...grpc.CallOption) (*affv1.AuthServiceReenrollTOTPResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceReenrollTOTPResponse, error) {
		return g.real.ReenrollTOTP(ctx, in, opts...)
	})
}

func (g guardedAuthClient) RegenerateRecoveryCodes(ctx context.Context, in *affv1.AuthServiceRegenerateRecoveryCodesRequest, opts ...grpc.CallOption) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.AuthServiceRegenerateRecoveryCodesResponse, error) {
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
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceListResponse, error) {
		return g.real.List(ctx, in, opts...)
	})
}

func (g guardedFeedClient) Get(ctx context.Context, in *affv1.FeedServiceGetRequest, opts ...grpc.CallOption) (*affv1.FeedServiceGetResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedFeedClient) Create(ctx context.Context, in *affv1.FeedServiceCreateRequest, opts ...grpc.CallOption) (*affv1.FeedServiceCreateResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceCreateResponse, error) {
		return g.real.Create(ctx, in, opts...)
	})
}

func (g guardedFeedClient) Update(ctx context.Context, in *affv1.FeedServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.FeedServiceUpdateResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceUpdateResponse, error) {
		return g.real.Update(ctx, in, opts...)
	})
}

func (g guardedFeedClient) SetEnabled(ctx context.Context, in *affv1.FeedServiceSetEnabledRequest, opts ...grpc.CallOption) (*affv1.FeedServiceSetEnabledResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceSetEnabledResponse, error) {
		return g.real.SetEnabled(ctx, in, opts...)
	})
}

func (g guardedFeedClient) Delete(ctx context.Context, in *affv1.FeedServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.FeedServiceDeleteResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceDeleteResponse, error) {
		return g.real.Delete(ctx, in, opts...)
	})
}

func (g guardedFeedClient) RunNow(ctx context.Context, in *affv1.FeedServiceRunNowRequest, opts ...grpc.CallOption) (*affv1.FeedServiceRunNowResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceRunNowResponse, error) {
		return g.real.RunNow(ctx, in, opts...)
	})
}

func (g guardedFeedClient) ValidateSpec(ctx context.Context, in *affv1.FeedServiceValidateSpecRequest, opts ...grpc.CallOption) (*affv1.FeedServiceValidateSpecResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceValidateSpecResponse, error) {
		return g.real.ValidateSpec(ctx, in, opts...)
	})
}

func (g guardedFeedClient) SetMembers(ctx context.Context, in *affv1.FeedServiceSetMembersRequest, opts ...grpc.CallOption) (*affv1.FeedServiceSetMembersResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceSetMembersResponse, error) {
		return g.real.SetMembers(ctx, in, opts...)
	})
}

func (g guardedFeedClient) ExportTOML(ctx context.Context, in *affv1.FeedServiceExportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceExportTOMLResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceExportTOMLResponse, error) {
		return g.real.ExportTOML(ctx, in, opts...)
	})
}

func (g guardedFeedClient) ImportTOML(ctx context.Context, in *affv1.FeedServiceImportTOMLRequest, opts ...grpc.CallOption) (*affv1.FeedServiceImportTOMLResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.FeedServiceImportTOMLResponse, error) {
		return g.real.ImportTOML(ctx, in, opts...)
	})
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
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceListResponse, error) {
		return g.real.List(ctx, in, opts...)
	})
}

func (g guardedItemClient) Get(ctx context.Context, in *affv1.ItemServiceGetRequest, opts ...grpc.CallOption) (*affv1.ItemServiceGetResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedItemClient) Create(ctx context.Context, in *affv1.ItemServiceCreateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceCreateResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceCreateResponse, error) {
		return g.real.Create(ctx, in, opts...)
	})
}

func (g guardedItemClient) Update(ctx context.Context, in *affv1.ItemServiceUpdateRequest, opts ...grpc.CallOption) (*affv1.ItemServiceUpdateResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceUpdateResponse, error) {
		return g.real.Update(ctx, in, opts...)
	})
}

func (g guardedItemClient) Delete(ctx context.Context, in *affv1.ItemServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.ItemServiceDeleteResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceDeleteResponse, error) {
		return g.real.Delete(ctx, in, opts...)
	})
}

func (g guardedItemClient) Restore(ctx context.Context, in *affv1.ItemServiceRestoreRequest, opts ...grpc.CallOption) (*affv1.ItemServiceRestoreResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceRestoreResponse, error) {
		return g.real.Restore(ctx, in, opts...)
	})
}

func (g guardedItemClient) PromoteSample(ctx context.Context, in *affv1.ItemServicePromoteSampleRequest, opts ...grpc.CallOption) (*affv1.ItemServicePromoteSampleResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServicePromoteSampleResponse, error) {
		return g.real.PromoteSample(ctx, in, opts...)
	})
}

func (g guardedItemClient) PublishCorrection(ctx context.Context, in *affv1.ItemServicePublishCorrectionRequest, opts ...grpc.CallOption) (*affv1.ItemServicePublishCorrectionResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServicePublishCorrectionResponse, error) {
		return g.real.PublishCorrection(ctx, in, opts...)
	})
}

func (g guardedItemClient) ListRevisions(ctx context.Context, in *affv1.ItemServiceListRevisionsRequest, opts ...grpc.CallOption) (*affv1.ItemServiceListRevisionsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceListRevisionsResponse, error) {
		return g.real.ListRevisions(ctx, in, opts...)
	})
}

func (g guardedItemClient) RevertRevision(ctx context.Context, in *affv1.ItemServiceRevertRevisionRequest, opts ...grpc.CallOption) (*affv1.ItemServiceRevertRevisionResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.ItemServiceRevertRevisionResponse, error) {
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
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.RunServiceHistoryResponse, error) {
		return g.real.History(ctx, in, opts...)
	})
}

func (g guardedRunClient) Get(ctx context.Context, in *affv1.RunServiceGetRequest, opts ...grpc.CallOption) (*affv1.RunServiceGetResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.RunServiceGetResponse, error) { return g.real.Get(ctx, in, opts...) })
}

func (g guardedRunClient) Watch(ctx context.Context, in *affv1.RunServiceWatchRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[affv1.RunServiceWatchResponse], error) {
	return guardCall(g.conn, func() (grpc.ServerStreamingClient[affv1.RunServiceWatchResponse], error) {
		return g.real.Watch(ctx, in, opts...)
	})
}

func (g guardedRunClient) Delete(ctx context.Context, in *affv1.RunServiceDeleteRequest, opts ...grpc.CallOption) (*affv1.RunServiceDeleteResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.RunServiceDeleteResponse, error) {
		return g.real.Delete(ctx, in, opts...)
	})
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
	return guardUnary(g.conn, ctx, generationTimeout, func(ctx context.Context) (*affv1.SampleServiceSampleResponse, error) {
		return g.real.Sample(ctx, in, opts...)
	})
}

func (g guardedSampleClient) SampleStream(ctx context.Context, in *affv1.SampleServiceSampleStreamRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[affv1.SampleServiceSampleStreamResponse], error) {
	return guardCall(g.conn, func() (grpc.ServerStreamingClient[affv1.SampleServiceSampleStreamResponse], error) {
		return g.real.SampleStream(ctx, in, opts...)
	})
}

func (g guardedSampleClient) ListSamples(ctx context.Context, in *affv1.SampleServiceListSamplesRequest, opts ...grpc.CallOption) (*affv1.SampleServiceListSamplesResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SampleServiceListSamplesResponse, error) {
		return g.real.ListSamples(ctx, in, opts...)
	})
}

func (g guardedSampleClient) DiscardSample(ctx context.Context, in *affv1.SampleServiceDiscardSampleRequest, opts ...grpc.CallOption) (*affv1.SampleServiceDiscardSampleResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SampleServiceDiscardSampleResponse, error) {
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
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceStatsResponse, error) {
		return g.real.Stats(ctx, in, opts...)
	})
}

func (g guardedSystemClient) SetGenerationEnabled(ctx context.Context, in *affv1.SystemServiceSetGenerationEnabledRequest, opts ...grpc.CallOption) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceSetGenerationEnabledResponse, error) {
		return g.real.SetGenerationEnabled(ctx, in, opts...)
	})
}

func (g guardedSystemClient) GetSettings(ctx context.Context, in *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceGetSettingsResponse, error) {
		return g.real.GetSettings(ctx, in, opts...)
	})
}

func (g guardedSystemClient) UpdateSettings(ctx context.Context, in *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceUpdateSettingsResponse, error) {
		return g.real.UpdateSettings(ctx, in, opts...)
	})
}

func (g guardedSystemClient) ListAuditEvents(ctx context.Context, in *affv1.SystemServiceListAuditEventsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceListAuditEventsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceListAuditEventsResponse, error) {
		return g.real.ListAuditEvents(ctx, in, opts...)
	})
}

func (g guardedSystemClient) Vacuum(ctx context.Context, in *affv1.SystemServiceVacuumRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVacuumResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceVacuumResponse, error) {
		return g.real.Vacuum(ctx, in, opts...)
	})
}

// ListModels goes through the same disconnect guard as every other call:
// while the socket is down there is nothing to ask, and the Settings screen
// treats the refusal exactly as it treats a provider failure — it falls back
// to a text field (web/pages/settings/modelselect.go).
// CostHistory is a read, and goes through the same disconnect guard as the
// rest — a chart of stale numbers with no indication they are stale is worse
// than an empty panel that says the connection is down.
func (g guardedSystemClient) CostHistory(ctx context.Context, in *affv1.SystemServiceCostHistoryRequest, opts ...grpc.CallOption) (*affv1.SystemServiceCostHistoryResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceCostHistoryResponse, error) {
		return g.real.CostHistory(ctx, in, opts...)
	})
}

func (g guardedSystemClient) ListModels(ctx context.Context, in *affv1.SystemServiceListModelsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceListModelsResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceListModelsResponse, error) {
		return g.real.ListModels(ctx, in, opts...)
	})
}

func (g guardedSystemClient) Version(ctx context.Context, in *affv1.SystemServiceVersionRequest, opts ...grpc.CallOption) (*affv1.SystemServiceVersionResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceVersionResponse, error) {
		return g.real.Version(ctx, in, opts...)
	})
}

func (g guardedSystemClient) Backup(ctx context.Context, in *affv1.SystemServiceBackupRequest, opts ...grpc.CallOption) (*affv1.SystemServiceBackupResponse, error) {
	return guardUnary(g.conn, ctx, unaryTimeout, func(ctx context.Context) (*affv1.SystemServiceBackupResponse, error) {
		return g.real.Backup(ctx, in, opts...)
	})
}
