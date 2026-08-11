package bridge

import (
	"bufio"
	"bytes"
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

	// upgradeHeader, when non-nil, is spliced into the raw 101 Switching
	// Protocols response gorilla/websocket writes directly to the hijacked
	// net.Conn — see SetUpgradeResponseHeader's doc comment for why this
	// exists at all (in short: w.Header()/http.SetCookie are silently
	// ineffective for this response, and there is no library hook for it).
	upgradeHeader http.Header
}

func newHijackCapture(w http.ResponseWriter) *hijackCapture {
	return &hijackCapture{ResponseWriter: w, ready: make(chan struct{})}
}

// SetUpgradeResponseHeader registers extra header lines (e.g. a ticket
// redemption's Set-Cookie) to be spliced into the WebSocket upgrade's 101
// response. Must be called BEFORE the wrapped handler runs (i.e. before
// Hijack is invoked) — see headerInjectingConn's doc comment for the
// mechanics and why a plain http.ResponseWriter.Header()/http.SetCookie
// call does not work here.
func (h *hijackCapture) SetUpgradeResponseHeader(header http.Header) {
	h.upgradeHeader = header
}

// Hijack implements http.Hijacker, delegating to the wrapped ResponseWriter,
// recording the resulting net.Conn (for the revalidation loop's
// CloseWhenReady), and — when SetUpgradeResponseHeader was called first —
// wrapping the returned net.Conn so the upgrade response gorilla/websocket
// is about to write gets that header spliced in.
func (h *hijackCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errNotHijackable
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}
	if len(h.upgradeHeader) > 0 {
		conn = &headerInjectingConn{Conn: conn, extra: h.upgradeHeader}
	}
	h.mu.Lock()
	h.conn = conn
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })
	return conn, rw, nil
}

// headerInjectingConn wraps a hijacked net.Conn so its FIRST Write is
// inspected for gorilla/websocket's raw upgrade-response bytes and, if
// found, has extra header lines spliced in just before the terminating
// blank line. Every later Write (WebSocket frames, once the connection is
// actually tunneling gRPC) passes through completely unchanged — this only
// ever touches the one Write that carries the HTTP response.
//
// Why this indirection is necessary at all: gorilla/websocket's own
// Upgrade(w, r, responseHeader) is the library's ONLY sanctioned way to add
// response headers to a WebSocket upgrade, and grpctunnel (this project's
// pinned v1.1.1) calls it as upgrader.Upgrade(w, r, nil) — always nil, with
// no BridgeConfig hook to override it. Worse, Upgrade does not consult
// w.Header() either: once it hijacks the connection (http.Hijacker), it
// composes the ENTIRE "HTTP/1.1 101 Switching Protocols\r\n...\r\n\r\n"
// response as a byte slice by hand and writes it with ONE netConn.Write(p)
// call straight to the raw net.Conn, bypassing the ResponseWriter (and
// therefore anything set on it, including http.SetCookie) entirely. That
// was verified directly against this project's own pinned gorilla/websocket
// v1.5.3 (server.go's Upgrade) and grpctunnel v1.1.1 (server.go's Upgrade
// call site) — an earlier version of server.go's ServeHTTP called
// http.SetCookie(w, cookie) before invoking the bridge handler and the
// cookie silently never reached the client, exactly because of this. So the
// only remaining seam is the net.Conn Upgrade hijacks and writes that exact
// byte slice to, which is what this type intercepts.
type headerInjectingConn struct {
	net.Conn

	extra    http.Header
	injected bool
}

// upgradeStatusLinePrefix is what every write this type is willing to
// splice into starts with — gorilla/websocket's literal, hard-coded status
// line (server.go: `p = append(p, "HTTP/1.1 101 Switching Protocols\r\n...`).
// A Write that does not start with this is passed through untouched: this
// type would rather silently fail to inject a header than risk corrupting a
// response it does not recognize (an error response written some other way,
// for instance, if the connection ever reaches this point without actually
// upgrading).
const upgradeStatusLinePrefix = "HTTP/1.1 101"

// headerBlockTerminator is the blank line ending an HTTP header block.
const headerBlockTerminator = "\r\n\r\n"

func (c *headerInjectingConn) Write(p []byte) (int, error) {
	if c.injected {
		return c.Conn.Write(p)
	}
	// Only the FIRST write is ever a candidate — everything after the
	// upgrade response is binary WebSocket framing, which could coincidentally
	// contain either byte pattern this check looks for.
	c.injected = true

	if !bytes.HasPrefix(p, []byte(upgradeStatusLinePrefix)) {
		return c.Conn.Write(p)
	}
	idx := bytes.Index(p, []byte(headerBlockTerminator))
	if idx == -1 {
		return c.Conn.Write(p)
	}

	var extraBuf bytes.Buffer
	if err := c.extra.Write(&extraBuf); err != nil {
		// http.Header.Write only fails writing to extraBuf, which cannot
		// fail (bytes.Buffer.Write never returns an error) — kept as a
		// belt-and-suspenders fallback that degrades to "send the
		// unmodified response" rather than a panic or a corrupted write.
		return c.Conn.Write(p)
	}

	// idx+2 lands just after the single \r\n ending the last real header
	// (gorilla/websocket's Sec-WebSocket-Accept line, or a
	// Sec-WebSocket-Protocol line if one was negotiated) and before the
	// second \r\n that starts the blank line — i.e. exactly where one more
	// "Key: Value\r\n" header belongs.
	spliced := make([]byte, 0, len(p)+extraBuf.Len())
	spliced = append(spliced, p[:idx+2]...)
	spliced = append(spliced, extraBuf.Bytes()...)
	spliced = append(spliced, p[idx+2:]...)

	n, err := c.Conn.Write(spliced)
	// Report a length relative to the CALLER's original p, per io.Writer's
	// contract (n must be <= len(p), and == len(p) on a nil error) — the
	// splice is this type's own implementation detail, invisible to
	// gorilla/websocket, which only ever checks err here (server.go: `if _,
	// err = netConn.Write(p); err != nil`).
	if err != nil {
		if n > len(p) {
			n = len(p)
		}
		return n, err
	}
	return len(p), nil
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
