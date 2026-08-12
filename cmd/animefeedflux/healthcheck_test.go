package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// healthcheck_test.go covers `animefeedflux healthcheck`, which had no tests
// and is the only way the container can tell whether it is alive.
//
// The runtime image is distroless (§15.4): no shell, no curl, nothing to exec
// but this binary. So if this subcommand is wrong, the container either never
// reports healthy — and an orchestrator restarts a working server forever —
// or always reports healthy, and a dead server is never restarted. Both
// failures are silent in every environment except production.

func TestHealthcheckRequiresThePublishAddress(t *testing.T) {
	t.Setenv("AFF_PUBLISH_ADDR", "")
	if code := healthcheckCmd(); code != exitBadConfig {
		t.Errorf("healthcheckCmd() = %d with no AFF_PUBLISH_ADDR, want %d", code, exitBadConfig)
	}
}

func TestHealthcheckSucceedsAgainstAHealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("healthcheck probed %q, want /healthz", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("AFF_PUBLISH_ADDR", srv.Listener.Addr().String())
	if code := healthcheckCmd(); code != exitOK {
		t.Errorf("healthcheckCmd() = %d against a healthy server, want %d", code, exitOK)
	}
}

func TestHealthcheckFailsOnANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	t.Setenv("AFF_PUBLISH_ADDR", srv.Listener.Addr().String())
	if code := healthcheckCmd(); code != exitRuntimeFail {
		t.Errorf("healthcheckCmd() = %d against a 503, want %d", code, exitRuntimeFail)
	}
}

func TestHealthcheckFailsWhenNothingIsListening(t *testing.T) {
	// Bind a port, learn its number, then release it: the probe is then
	// guaranteed to hit a closed port rather than a port some other test or
	// process happens to own.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	t.Setenv("AFF_PUBLISH_ADDR", addr)
	if code := healthcheckCmd(); code != exitRuntimeFail {
		t.Errorf("healthcheckCmd() = %d against a closed port, want %d", code, exitRuntimeFail)
	}
}

// TestHealthcheckRewritesWildcardBindAddresses covers the container-specific
// case the code exists to handle: the server binds 0.0.0.0, and 0.0.0.0 is
// not a usable DESTINATION. Probing it literally fails, so the address has to
// be rewritten to loopback on the same port before dialling. Without this the
// healthcheck fails inside every container it is meant to serve.
func TestHealthcheckRewritesWildcardBindAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}

	for _, bind := range []string{"0.0.0.0:" + port, ":" + port} {
		t.Run(bind, func(t *testing.T) {
			t.Setenv("AFF_PUBLISH_ADDR", bind)
			if code := healthcheckCmd(); code != exitOK {
				t.Errorf("healthcheckCmd() = %d for bind address %q, want %d — "+
					"a wildcard bind must be probed on loopback", code, bind, exitOK)
			}
		})
	}
}

// TestRunRejectsAnUnloadableConfig pins the one branch of run() that does not
// require a whole server: configuration failure must exit exitBadConfig
// (distinct from a runtime failure) and must report through stderr rather
// than the logger, since no logger exists yet at that point.
func TestRunRejectsAnUnloadableConfig(t *testing.T) {
	// A required setting deliberately made invalid. config.Load reads the
	// process environment, so this exercises the real loader.
	t.Setenv("AFF_PUBLIC_BASE_URL", "://not-a-url")
	if code := run(); code != exitBadConfig {
		t.Errorf("run() = %d with an invalid config, want %d", code, exitBadConfig)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:8080", true},
		{"[::1]:8080", true},
		{"127.0.0.53:9000", true},
		{"0.0.0.0:8080", false},
		{"192.168.1.10:8080", false},
		{"example.com:80", false},
		{":8080", false},       // no host at all
		{"not-an-addr", false}, // unparseable
		{"", false},
	}
	for _, tc := range cases {
		if got := isLoopbackAddr(tc.addr); got != tc.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
