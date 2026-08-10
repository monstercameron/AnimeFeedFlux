package bridge

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"
)

// errNotHijackable is returned when the underlying ResponseWriter passed to
// NewServer's handler cannot be hijacked (e.g. an http.ResponseRecorder in a
// test that isn't exercising the websocket path).
var errNotHijackable = errors.New("bridge: response writer does not support hijacking")

// hijackCapture wraps an http.ResponseWriter to capture the raw net.Conn the
// moment something downstream (gorilla/websocket, inside grpctunnel) hijacks
// it for the WebSocket upgrade.
//
// This exists because grpctunnel owns the upgrade end-to-end — BuildBridgeHandler
// takes an http.Handler and never hands back the socket it creates, so there is
// no library-level way to force-close one connection later from the outside.
// Wrapping the ResponseWriter is the standard technique for observing a
// Hijack() before it happens: we still pass the real Hijack() through
// unchanged, we just also keep the net.Conn it returns, giving the
// revalidation loop something concrete to call Close() on.
type hijackCapture struct {
	http.ResponseWriter

	ready chan struct{}
	once  sync.Once

	mu   sync.Mutex
	conn net.Conn
}

func newHijackCapture(w http.ResponseWriter) *hijackCapture {
	return &hijackCapture{ResponseWriter: w, ready: make(chan struct{})}
}

// Hijack implements http.Hijacker, delegating to the wrapped ResponseWriter
// and recording the resulting net.Conn before returning it.
func (h *hijackCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errNotHijackable
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })
	return conn, rw, nil
}

// Close force-closes the hijacked connection. No-op if Hijack never ran.
func (h *hijackCapture) Close() error {
	h.mu.Lock()
	conn := h.conn
	h.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// CloseWhenReady closes the hijacked connection once Hijack has run,
// unless stop fires first. The revalidation loop calls this rather than
// Close directly: a revalidation failure can in principle race the upgrade
// itself (e.g. a pathologically short RevalidateInterval), and without this
// wait a Close() that lands before Hijack() would silently no-op and leave
// the socket open. stop guards the other side of that race — if the
// connection was never upgraded at all (origin check failed, client hung
// up), ready never closes and this would otherwise block forever.
func (h *hijackCapture) CloseWhenReady(stop <-chan struct{}) {
	select {
	case <-h.ready:
	case <-stop:
		return
	}
	_ = h.Close()
}
