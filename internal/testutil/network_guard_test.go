package testutil

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAllowDialBlocksOutbound(t *testing.T) {
	os.Unsetenv(LiveLLMEnv)

	for _, addr := range []string{"api.openai.com:443", "93.184.216.34:443", "example.invalid:80"} {
		if err := allowDial(addr); err == nil {
			t.Errorf("allowDial(%q): expected outbound dial to be blocked, got nil error", addr)
		}
	}
}

func TestAllowDialAllowsLoopback(t *testing.T) {
	os.Unsetenv(LiveLLMEnv)

	for _, addr := range []string{"127.0.0.1:8080", "localhost:9000", "[::1]:1234"} {
		if err := allowDial(addr); err != nil {
			t.Errorf("allowDial(%q): expected loopback to be allowed, got %v", addr, err)
		}
	}
}

func TestAllowDialLiveLLMBypasses(t *testing.T) {
	t.Setenv(LiveLLMEnv, "1")

	if err := allowDial("api.openai.com:443"); err != nil {
		t.Fatalf("expected %s to bypass the guard, got %v", LiveLLMEnv, err)
	}
}

// TestInstallNetworkGuardBlocksDefaultClientOutbound proves the guard is
// wired into http.DefaultClient, not just the standalone allowDial function
// — this is what makes A0-T07 structural: a future test that quietly adds
// `http.Get("https://...")` fails here rather than silently billing a
// provider.
func TestInstallNetworkGuardBlocksDefaultClientOutbound(t *testing.T) {
	os.Unsetenv(LiveLLMEnv)
	restore := InstallNetworkGuard()
	defer restore()

	_, err := http.Get("http://example.invalid/")
	if err == nil {
		t.Fatal("expected the guarded default client to block an outbound request")
	}
	if !strings.Contains(err.Error(), "blocked outbound network dial") {
		t.Fatalf("expected the guard's error to surface, got: %v", err)
	}
}

// TestInstallNetworkGuardAllowsLoopback proves the guard does not break the
// loopback httptest.Server pattern used throughout this repo's suites.
func TestInstallNetworkGuardAllowsLoopback(t *testing.T) {
	os.Unsetenv(LiveLLMEnv)
	restore := InstallNetworkGuard()
	defer restore()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected the guarded default client to allow a loopback httptest server, got %v", err)
	}
	resp.Body.Close()
}
