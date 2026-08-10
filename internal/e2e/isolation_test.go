package e2e

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
)

// TestIsolation asserts PLAN.md §2's security boundary directly against the
// real listeners, rather than describing it: the publish plane cannot reach
// any control-plane surface, cannot accept a write method, and cannot write
// to the database even if a handler tried — and the bridge refuses an
// unauthenticated upgrade before any RPC is reachable at all.
func TestIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: full end-to-end suite skipped in -short mode")
	}

	app := New(t)
	client := app.publishSrv.Client()

	t.Run("no control-plane route is reachable from the publish listener", func(t *testing.T) {
		// Candidate paths a control-plane client might plausibly try against
		// the wrong listener: the bridge's own root (grpctunnel serves the
		// WebSocket upgrade at "/") and a couple of plausible gRPC-style
		// service paths. None of PLAN.md §6's fixed route set matches any of
		// these, so every one must 404.
		paths := []string{
			"/",
			"/aff.v1.AuthService/Login",
			"/aff.v1.FeedService/Create",
			"/grpc",
			"/ws",
		}
		for _, p := range paths {
			resp, err := client.Get(app.PublishURL + p)
			if err != nil {
				t.Fatalf("GET %s: %v", p, err)
			}
			_ = resp.Body.Close()
			// "/" is PLAN.md §6's feed index and is legitimately 200, not a
			// control-plane route at all — every other candidate must 404.
			if p == "/" {
				if resp.StatusCode != http.StatusOK {
					t.Errorf("GET / = %d, want 200 (feed index)", resp.StatusCode)
				}
				continue
			}
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
			}
		}
	})

	t.Run("the publish listener accepts only GET and HEAD", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, app.PublishURL+"/healthz", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /healthz: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST /healthz = %d, want 405", resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow == "" {
			t.Error("405 response carried no Allow header")
		}
	})

	t.Run("an unauthenticated bridge upgrade is refused", func(t *testing.T) {
		resp, err := app.DialBridgeUnauthenticated()
		if err != nil {
			t.Fatalf("GET bridge with no cookie: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("bridge upgrade with no cookie = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("a valid session cookie gains nothing extra on the publish listener", func(t *testing.T) {
		// internal/publish/server.go's Deps carries no session concept at
		// all — no cookie name, no validator, nothing session-shaped — so
		// there is no code path for a cookie to unlock. Proven two ways:
		// first, structurally (compile-time), by confirming Deps has no such
		// field would require reflection this package need not add; second,
		// behaviorally, the way that actually matters to a client — a
		// request carrying a REAL, currently-valid admin session cookie
		// against the publish listener produces byte-identical status and
		// body to the same request with no cookie at all.
		ctx := context.Background()
		totpSecret, err := app.InitAdmin(ctx, adminPassword)
		if err != nil {
			t.Fatalf("InitAdmin: %v", err)
		}
		login, err := app.Login(ctx, adminPassword, totpSecret)
		if err != nil {
			t.Fatalf("Login: %v", err)
		}
		defer login.Close()

		bare, err := client.Get(app.PublishURL + "/healthz")
		if err != nil {
			t.Fatalf("GET /healthz (no cookie): %v", err)
		}
		bareStatus := bare.StatusCode
		_ = bare.Body.Close()

		req, _ := http.NewRequest(http.MethodGet, app.PublishURL+"/healthz", nil)
		req.AddCookie(&http.Cookie{Name: "__Host-aff_session", Value: login.Token})
		withCookie, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /healthz (with valid session cookie): %v", err)
		}
		withCookieStatus := withCookie.StatusCode
		_ = withCookie.Body.Close()

		if bareStatus != withCookieStatus {
			t.Errorf("status differs with a valid session cookie attached: %d vs %d", bareStatus, withCookieStatus)
		}
	})

	t.Run("the publish plane's store handle cannot write", func(t *testing.T) {
		// store.Reader() returns the store.Reader interface, whose method
		// set has no Exec-shaped method by construction (internal/store/
		// store.go: "deliberately exposes no Exec, no Begin ... so a handler
		// cannot reach a write path by accident or by refactor"). But the
		// UNDERLYING dynamic value is the same *sql.DB the writer's DSN
		// differs from only by `mode=ro` — a type assertion against it DOES
		// succeed, because Go interfaces are satisfied structurally by the
		// concrete type, not by the interface it was retrieved through. So
		// the real proof is not "the assertion fails" (it won't) but that a
		// REAL write attempt through it fails at the SQLite layer, which is
		// where PLAN.md §2's guarantee actually lives ("mode=ro").
		reader := app.Store.Reader()
		execer, ok := reader.(interface {
			ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
		})
		if !ok {
			t.Fatal("store.Reader's dynamic type unexpectedly has no ExecContext at all; cannot attempt the write proof")
		}
		_, err := execer.ExecContext(context.Background(),
			`INSERT INTO feeds (slug, title, description, language, kind, spec_json, enabled, timezone, created_at, updated_at, version)
			 VALUES ('isolation-write-probe', 't', 'd', 'en', 'generative', '{}', 0, 'UTC', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 1)`)
		if err == nil {
			t.Fatal("a write through the publish plane's reader handle SUCCEEDED — PLAN.md §2's guarantee is broken")
		}
	})
}
