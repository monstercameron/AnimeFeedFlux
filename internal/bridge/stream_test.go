package bridge_test

// TODOS.md B2-06 ("verify SampleStream and RunService.Watch actually stream
// through the gRPC-over-WebSocket bridge") is a transport-layer claim: does
// grpctunnel's server-streaming relay actually deliver messages one at a
// time, in order, as the handler sends them — or does something buffer and
// flush them together, which would pass a naive "dial and read one message"
// test while failing the real point (watching a run progress live, PLAN.md
// §22 J9)?
//
// That question belongs to this package (internal/bridge), not to
// internal/rpc's SampleStream/RunService.Watch handlers themselves — both of
// those ride the exact same grpctunnel relay this package configures in
// NewServer, so proving the relay's own streaming behavior here is direct
// evidence for both call sites, independent of which application RPC uses
// it. (internal/rpc/sample.go's own SampleStream doc comment already
// documents a separate, narrower limitation: its underlying
// generate.Sample pipeline makes one blocking call and returns every
// candidate at once, so there is no true incremental *provider* stream for
// it to relay — that is a generate.Sample/runner.go constraint, out of
// scope for this package and for this task's editable file list, and
// distinct from the transport question this file answers.)
//
// Two fixtures are used, both intentionally NOT the application's own proto
// services (SampleService/RunService), because this package must not import
// internal/rpc and must not depend on internal/e2e's private wiring:
//
//   - the stock gRPC health service (already used by server_test.go) for
//     the incremental-delivery/ordering proof: its Watch RPC is a real
//     push-driven server stream (a channel per watcher, fed by
//     SetServingStatus — see google.golang.org/grpc/health/server.go) with
//     no artificial pacing this file has to fake.
//   - a hand-built minimal server-streaming RPC (no .proto needed — it
//     reuses healthpb's existing message types purely as wire envelopes)
//     for the clean-termination and mid-stream-disconnect proofs, because
//     health.Server's own Watch never returns except on cancellation, so it
//     cannot demonstrate a handler-initiated clean end.
import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/bridge"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// --- shared plumbing --------------------------------------------------

// registerHealthServerWithHandle is registerHealthServer (server_test.go)
// but also hands back the *health.Server itself, so a test can push status
// changes with SetServingStatus after the socket is already open.
func registerHealthServerWithHandle(t *testing.T) (register func(*grpc.Server), hs *health.Server) {
	t.Helper()
	hs = health.NewServer()
	return func(s *grpc.Server) {
		healthpb.RegisterHealthServer(s, hs)
	}, hs
}

// recvOutcome is one stream.Recv() result, delivered over a channel so a
// test can wait on it with a bounded select instead of blocking the test
// goroutine directly — see recvAsync's doc comment.
type recvOutcome struct {
	msg *healthpb.HealthCheckResponse
	err error
}

// recvAsync starts exactly one stream.Recv() call in its own goroutine and
// returns a channel that receives its result once it completes. Used
// (instead of calling Recv() inline) so a test can first assert NOTHING has
// arrived yet within a bounded window (proving the bridge is not
// pre-buffering a future send) and then, on the very same in-flight call,
// wait for the message that arrives once the server actually pushes one —
// without ever issuing a second, overlapping Recv() on the same stream
// (gRPC streams are single-reader).
func recvAsync(stream healthpb.Health_WatchClient) <-chan recvOutcome {
	ch := make(chan recvOutcome, 1)
	go func() {
		msg, err := stream.Recv()
		ch <- recvOutcome{msg: msg, err: err}
	}()
	return ch
}

// assertNoMessageYet fails the test if a message (or error) is already
// sitting on ch within window — i.e. it proves the send this call precedes
// has NOT already happened, ruling out a buffered-then-flushed relay where
// every message would already be available. window only bounds how long
// this negative check waits; it is not what makes the later positive
// assertion (the real synchronization) correct — see the caller.
func assertNoMessageYet(t *testing.T, ch <-chan recvOutcome, window time.Duration) {
	t.Helper()
	select {
	case out := <-ch:
		t.Fatalf("a message was already available before the server pushed one (msg=%v err=%v) — "+
			"suggests the bridge buffers/flushes instead of streaming incrementally", out.msg, out.err)
	case <-time.After(window):
	}
}

// --- incremental delivery + ordering ------------------------------------

// TestWatchOverBridge_MessagesArriveIncrementallyInOrder is B2-06's core
// claim, proven at the transport layer: three distinct pushes, each
// triggered only after the test has confirmed the PREVIOUS message already
// arrived and the NEXT one has not, arrive over the real WebSocket bridge
// one at a time, in the exact order sent — not as a single batch delivered
// only once the RPC itself ends (health.Server's Watch never ends on its
// own, so a buffer-until-RPC-completion implementation would simply hang
// here forever, which the per-message timeout below would catch).
func TestWatchOverBridge_MessagesArriveIncrementallyInOrder(t *testing.T) {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})
	register, hs := registerHealthServerWithHandle(t)

	cfg := bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
	}
	srv := mustNewServer(t, cfg, register)
	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := client.Watch(ctx, &healthpb.HealthCheckRequest{Service: "b2-06-streamtest"})
	if err != nil {
		t.Fatalf("Watch (over the real bridge): %v", err)
	}

	// The initial snapshot: the service was never registered in the health
	// server's statusMap, so the first push is SERVICE_UNKNOWN — proves a
	// watcher genuinely attached before any status was ever set, so what
	// follows is real incremental delivery, not a connection that only ever
	// saw one lucky message.
	first, err := stream.Recv()
	if err != nil {
		t.Fatalf("receiving the initial snapshot over the bridge: %v", err)
	}
	if first.GetStatus() != healthpb.HealthCheckResponse_SERVICE_UNKNOWN {
		t.Fatalf("initial status = %v, want SERVICE_UNKNOWN", first.GetStatus())
	}

	wantSequence := []healthpb.HealthCheckResponse_ServingStatus{
		healthpb.HealthCheckResponse_SERVING,
		healthpb.HealthCheckResponse_NOT_SERVING,
		healthpb.HealthCheckResponse_SERVING,
	}
	var gotSequence []healthpb.HealthCheckResponse_ServingStatus
	for i, want := range wantSequence {
		ch := recvAsync(stream)
		// Nothing has been pushed for this step yet — if the bridge already
		// had this message (or any message) ready before the push below,
		// that is exactly the buffered-relay failure mode B2-06 warns
		// against.
		assertNoMessageYet(t, ch, 200*time.Millisecond)

		hs.SetServingStatus("b2-06-streamtest", want)

		select {
		case out := <-ch:
			if out.err != nil {
				t.Fatalf("Recv #%d (over the bridge): %v", i, out.err)
			}
			gotSequence = append(gotSequence, out.msg.GetStatus())
		case <-time.After(10 * time.Second):
			t.Fatalf("Recv #%d: no message arrived over the bridge within 10s of the server pushing one", i)
		}
	}

	if !reflect.DeepEqual(gotSequence, wantSequence) {
		t.Fatalf("sequence received over the bridge = %v, want %v (order must be preserved)", gotSequence, wantSequence)
	}
}

// --- clean termination + mid-stream disconnect --------------------------

// controlledStreamCmd is one instruction to the hand-built streaming
// handler below: send a status, or end the stream cleanly.
type controlledStreamCmd struct {
	status healthpb.HealthCheckResponse_ServingStatus
	done   bool
}

// controlledStreamServiceName/MethodName name the hand-built RPC registered
// directly on the *grpc.Server NewServer builds — no .proto/codegen needed,
// since it only ever needs an envelope type, and healthpb's already-vendored
// messages serve that purpose here (see this file's package doc comment for
// why this fixture exists instead of reusing SampleService/RunService).
const (
	controlledStreamServiceName = "bridgetest.ControlledStream"
	controlledStreamMethodName  = "/" + controlledStreamServiceName + "/Produce"
)

// newControlledStreamRegister builds the register func mustNewServer wants,
// wiring a single server-streaming RPC whose entire behavior is driven by
// cmds — sent one at a time by the test, so the handler's pacing is exactly
// what the test dictates, no sleeps on either side.
func newControlledStreamRegister(cmds <-chan controlledStreamCmd) func(*grpc.Server) {
	return func(s *grpc.Server) {
		desc := grpc.ServiceDesc{
			ServiceName: controlledStreamServiceName,
			HandlerType: (*any)(nil),
			Streams: []grpc.StreamDesc{{
				StreamName: "Produce",
				Handler: func(_ any, stream grpc.ServerStream) error {
					var req healthpb.HealthCheckRequest
					if err := stream.RecvMsg(&req); err != nil {
						return err
					}
					for cmd := range cmds {
						if cmd.done {
							return nil
						}
						if err := stream.SendMsg(&healthpb.HealthCheckResponse{Status: cmd.status}); err != nil {
							return err
						}
					}
					return nil
				},
				ServerStreams: true,
			}},
			Metadata: "internal/bridge/stream_test.go",
		}
		s.RegisterService(&desc, nil)
	}
}

// dialControlledStream opens the hand-built RPC over conn (real bridge
// tunnel), sends the one (unused, but ServerStream RPCs still start with a
// client message on this wire shape) request message, and closes the send
// side — the same shape a generated ServerStreamClient's constructor
// produces.
func dialControlledStream(t *testing.T, conn *grpc.ClientConn) grpc.ClientStream {
	t.Helper()
	stream, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{StreamName: "Produce", ServerStreams: true},
		controlledStreamMethodName)
	if err != nil {
		t.Fatalf("NewStream (over the bridge): %v", err)
	}
	if err := stream.SendMsg(&healthpb.HealthCheckRequest{Service: "controlled"}); err != nil {
		t.Fatalf("SendMsg (request envelope): %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	return stream
}

// recordingListener wraps a net.Listener so a test can reach in and forcibly
// close the exact raw TCP connection an already-upgraded WebSocket is
// running over. This exists because httptest.Server.CloseClientConnections
// does NOT work for this purpose: net/http/httptest's ConnState hook
// deletes a connection from its tracked set the instant it transitions to
// http.StateHijacked (net/http/httptest/server.go's wrap() — "case
// http.StateHijacked, http.StateClosed: ... delete(s.conns, c)"), and every
// bridge connection is hijacked the moment the WebSocket upgrade completes
// (internal/bridge/hijack.go). CloseClientConnections was tried first here
// and confirmed to be a silent no-op against a live bridge socket — it
// closed nothing, and the client's Recv() simply hung, which would have
// been indistinguishable from a genuine "the bridge cannot surface a
// dropped connection" bug without this direct-conn mechanism ruling out a
// broken test fixture first.
type recordingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *recordingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return c, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, c)
	l.mu.Unlock()
	return c, nil
}

// closeLast force-closes the most recently accepted raw connection — in
// every test using this fixture there is exactly one client connection, so
// "most recent" unambiguously means "the one under test."
func (l *recordingListener) closeLast(t *testing.T) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.conns) == 0 {
		t.Fatal("recordingListener: no connection has been accepted yet")
	}
	if err := l.conns[len(l.conns)-1].Close(); err != nil {
		t.Fatalf("closing the underlying TCP connection: %v", err)
	}
}

// mustNewServerWithConnCapture is mustNewServer (server_test.go) plus a
// *recordingListener so a test can sever the raw connection directly — see
// recordingListener's doc comment for why CloseClientConnections cannot do
// this for an already-upgraded bridge socket.
func mustNewServerWithConnCapture(t *testing.T, cfg bridge.Config, register func(*grpc.Server)) (*httptest.Server, *recordingListener) {
	t.Helper()
	handler, err := bridge.NewServer(cfg, register)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	rl := &recordingListener{Listener: srv.Listener}
	srv.Listener = rl
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, rl
}

func controlledStreamCfg() bridge.Config {
	validator := newTestValidator()
	validator.put("good-token", bridge.Session{UserID: "cam", ExpiresAt: time.Now().Add(time.Hour)})
	return bridge.Config{
		SessionCookieName: testCookieName,
		AllowedOrigins:    []string{allowedOrigin},
		Validator:         validator,
	}
}

// TestControlledStreamOverBridge_CleanTerminationSeenByClient proves the
// "terminates cleanly, client sees the end, not a hang" half of B2-06: once
// the handler returns nil after N sends, the client's next Recv observes
// io.EOF promptly — the well-defined "stream is over, nothing was lost"
// signal — rather than blocking forever or surfacing an error.
func TestControlledStreamOverBridge_CleanTerminationSeenByClient(t *testing.T) {
	cmds := make(chan controlledStreamCmd, 1)
	srv := mustNewServer(t, controlledStreamCfg(), newControlledStreamRegister(cmds))
	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)
	stream := dialControlledStream(t, conn)

	const n = 3
	for i := 0; i < n; i++ {
		cmds <- controlledStreamCmd{status: healthpb.HealthCheckResponse_SERVING}
		var resp healthpb.HealthCheckResponse
		recvErrCh := make(chan error, 1)
		go func() { recvErrCh <- stream.RecvMsg(&resp) }()
		select {
		case err := <-recvErrCh:
			if err != nil {
				t.Fatalf("Recv #%d (over the bridge): %v", i, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("Recv #%d: no message arrived over the bridge within 10s", i)
		}
	}

	cmds <- controlledStreamCmd{done: true}
	var resp healthpb.HealthCheckResponse
	finalErrCh := make(chan error, 1)
	go func() { finalErrCh <- stream.RecvMsg(&resp) }()
	select {
	case err := <-finalErrCh:
		if err != io.EOF {
			t.Fatalf("after a clean handler return, Recv over the bridge = %v, want io.EOF", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream never terminated over the bridge after the handler returned — hung instead of delivering io.EOF")
	}
}

// TestControlledStreamOverBridge_MidStreamDisconnectSurfacesError is B2-06's
// sharpest claim, and the one J9 ("drop the WebSocket mid-run and
// reconnect") depends on: a connection severed mid-stream — never told
// "done", never cancelled by the client — must surface to the caller as an
// ERROR distinct from io.EOF. If it collapsed to io.EOF, a caller watching
// a run would read a truncated connection as "the run finished", exactly
// the false-completion PLAN.md's J9 calls out.
func TestControlledStreamOverBridge_MidStreamDisconnectSurfacesError(t *testing.T) {
	cmds := make(chan controlledStreamCmd, 1)
	srv, rl := mustNewServerWithConnCapture(t, controlledStreamCfg(), newControlledStreamRegister(cmds))
	conn := dialTunnel(t, srv.URL, "good-token", allowedOrigin)
	stream := dialControlledStream(t, conn)

	// One real message first, so this is provably a MID-stream drop, not a
	// connection that never delivered anything.
	cmds <- controlledStreamCmd{status: healthpb.HealthCheckResponse_SERVING}
	var resp healthpb.HealthCheckResponse
	firstErrCh := make(chan error, 1)
	go func() { firstErrCh <- stream.RecvMsg(&resp) }()
	select {
	case err := <-firstErrCh:
		if err != nil {
			t.Fatalf("first Recv (over the bridge): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first message never arrived over the bridge")
	}

	// The mid-stream disconnect: sever the raw TCP connection the upgraded
	// WebSocket is running over — the same thing a laptop closing its lid, a
	// killed tab, or a flaky network does at the transport level. The
	// handler is left parked on cmds (no 'done' was ever sent), so a clean
	// io.EOF here would be a lie about what actually happened.
	rl.closeLast(t)

	finalErrCh := make(chan error, 1)
	go func() { finalErrCh <- stream.RecvMsg(&resp) }()
	select {
	case err := <-finalErrCh:
		if err == nil {
			t.Fatal("Recv after a mid-stream disconnect unexpectedly succeeded")
		}
		if err == io.EOF {
			t.Fatal("Recv after a mid-stream disconnect returned io.EOF — " +
				"a truncated stream must surface as an error, not look like a clean end (PLAN.md §22 J9)")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Recv after a mid-stream disconnect hung instead of surfacing an error")
	}
}
