package bridge

import (
	"net"
	"net/http"
	"strings"
)

// defaultIsTLS decides the Secure cookie attribute (PLAN.md §4: Secure is
// one of the four mandatory flags, and the __Host- prefix — which
// auth.CookieName() always uses — makes this load-bearing beyond "defense in
// depth": a browser silently REFUSES to store a __Host- cookie that lacks
// Secure, so getting this wrong doesn't weaken the cookie, it makes the
// ticket-redeeming upgrade silently not deliver one at all).
//
// r.TLS is set only when THIS process terminates TLS itself. In this
// deployment it usually does not: PLAN.md §4/§15 has the admin listener bind
// 127.0.0.1 behind nginx, which is the TLS terminator, forwarding plain HTTP
// to this process. Deriving Secure from r.TLS alone would therefore be false
// in production — the one place it most needs to be true — so this also
// honours X-Forwarded-Proto: https from the request. That is safe to trust
// unconditionally here, without a new "trusted proxy" config flag
// (internal/config is off-limits to this change), because the admin
// listener is loopback-only and reachable ONLY through the local nginx PLAN.md
// §4 already requires — nothing else can open a connection to this listener
// to forge the header.
//
// This function, and isLoopbackHost below, previously lived in
// internal/bridge/httpauth.go (the now-removed HTTP `/auth/login` side door
// — see server.go's package doc comment for why it had to go). The cookie
// this decides Secure for is now set at the WebSocket upgrade itself
// (server.go's ServeHTTP, on a successful ticket redemption), which is the
// only place internal/bridge sets a Set-Cookie header at all, so this moved
// with it rather than being duplicated.
func defaultIsTLS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	// A loopback host counts too, and this is not a dev bypass.
	//
	// Browsers define localhost and 127.0.0.1/::1 as "potentially trustworthy
	// origins" (W3C Secure Contexts): they accept and send a Secure cookie
	// over plain http there, because the traffic never leaves the machine.
	// So Secure=true on loopback is both storable by the browser and
	// accurate about the transport's actual exposure.
	return isLoopbackHost(r.Host)
}

// isLoopbackHost reports whether the request's Host is a loopback address.
// The port is irrelevant and may be absent, so a SplitHostPort failure is
// treated as "the whole string is the host" rather than an error.
func isLoopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.TrimSpace(strings.Trim(h, "[]"))
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
