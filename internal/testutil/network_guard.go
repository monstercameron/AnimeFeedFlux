package testutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
)

// LiveLLMEnv is the escape hatch that opts a test run into real network
// access, per RULE-1 (PLAN.md §17.1, TODOS.md A0-T07): "the default
// `go test ./...` must not need a network or a key." Nothing in this
// repository's default test suite should read this variable's VALUE — only
// whether it is set — so no provider key ever needs to live in a test file
// to make a live run possible.
const LiveLLMEnv = "AFF_LIVE_LLM"

// LiveLLM reports whether this test run has opted into live network/LLM
// calls.
func LiveLLM() bool {
	return os.Getenv(LiveLLMEnv) != ""
}

// allowDial decides whether a dial to addr should proceed. Loopback
// destinations are allowed unconditionally — httptest.Server binds
// 127.0.0.1/::1, and that is a local, in-process fixture, not "the network"
// in the sense RULE-1 cares about — and everything else is blocked unless
// LiveLLM() is true.
//
// It deliberately does NOT resolve hostnames to check whether they happen to
// point at loopback: resolving would itself be a DNS lookup, which is
// exactly the kind of incidental network access this guard exists to catch
// before it happens. A non-numeric, non-"localhost" host is always treated
// as outbound.
func allowDial(addr string) error {
	if LiveLLM() {
		return nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}

	return fmt.Errorf("testutil: blocked outbound network dial to %q (RULE-1: %s is unset) — "+
		"use testutil.StaticClient/FileClient for fixtures, or set %s to explicitly opt into a live run",
		addr, LiveLLMEnv, LiveLLMEnv)
}

// guardedDialContext is a net.Dialer.DialContext-shaped function that
// enforces allowDial before the network is ever touched — the block happens
// on the raw address string, before any DNS resolution or connect syscall.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if err := allowDial(addr); err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// InstallNetworkGuard replaces http.DefaultTransport's dialer with one that
// enforces RULE-1 (blocks any non-loopback dial unless AFF_LIVE_LLM is set),
// and returns a func that restores the original dialer. Call sites should
// defer the restore even from a TestMain that is about to exit anyway, so a
// later refactor into ordinary per-test setup does not inherit a leaked
// global.
//
// Scope: this guards http.DefaultTransport, which is what http.DefaultClient
// and any bare http.Client{} (no Transport set) dial through. Production and
// test code that builds its own *http.Transport with an explicit Dialer, or
// that calls net.Dial directly, bypasses it. That is a deliberate, narrow
// target: the failure mode RULE-1 exists to prevent is an accidental
// `http.Get` (or an SDK client that defaults to http.DefaultClient) added to
// a test later, not a determined attempt to reach the network — this is a
// tripwire, not a sandbox.
func InstallNetworkGuard() (restore func()) {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return func() {}
	}
	original := t.DialContext
	t.DialContext = guardedDialContext
	return func() { t.DialContext = original }
}
