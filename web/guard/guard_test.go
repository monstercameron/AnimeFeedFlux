package guard

import (
	"testing"

	"github.com/monstercameron/AnimeFeedFlux/web/appstate"
)

// routeTable mirrors web/shell's registered route table (the single
// source of truth now lives there, since GWC's router owns path
// matching) closely enough for these pure-decision tests.
var routeTable = map[string]RouteInfo{
	"/login":    {Path: "/login", RequiresAuth: false},
	"/recover":  {Path: "/recover", RequiresAuth: false},
	"/generate": {Path: "/generate", RequiresAuth: true},
	"/history":  {Path: "/history", RequiresAuth: true},
	"/settings": {Path: "/settings", RequiresAuth: true},
}

func mustRoute(t *testing.T, path string) RouteInfo {
	t.Helper()
	r, ok := routeTable[path]
	if !ok {
		t.Fatalf("test setup: %q not in routeTable", path)
	}
	return r
}

func TestDecide_AnonToAuthedRedirectsLogin(t *testing.T) {
	d := Decide(appstate.Anon, mustRoute(t, "/generate"))
	if d.Allow || d.RedirectTo != "/login" {
		t.Fatalf("got %+v, want redirect to /login", d)
	}
}

func TestDecide_AnonToPublicAllowed(t *testing.T) {
	for _, p := range []string{"/login", "/recover"} {
		d := Decide(appstate.Anon, mustRoute(t, p))
		if !d.Allow {
			t.Errorf("Anon -> %s: got %+v, want Allow", p, d)
		}
	}
}

func TestDecide_AuthedToPublicRedirectsGenerate(t *testing.T) {
	for _, s := range []appstate.State{appstate.Auth, appstate.Disconnected, appstate.Killed} {
		for _, p := range []string{"/login", "/recover"} {
			d := Decide(s, mustRoute(t, p))
			if d.Allow || d.RedirectTo != "/generate" {
				t.Errorf("%v -> %s: got %+v, want redirect to /generate", s, p, d)
			}
		}
	}
}

func TestDecide_AuthedToAuthedAllowed(t *testing.T) {
	for _, s := range []appstate.State{appstate.Auth, appstate.Disconnected, appstate.Killed} {
		for _, p := range []string{"/generate", "/history", "/settings"} {
			d := Decide(s, mustRoute(t, p))
			if !d.Allow {
				t.Errorf("%v -> %s: got %+v, want Allow", s, p, d)
			}
		}
	}
}

func TestDecide_ElevatedOnlyRecover(t *testing.T) {
	d := Decide(appstate.Elevated, mustRoute(t, "/recover"))
	if !d.Allow {
		t.Fatalf("Elevated -> /recover: got %+v, want Allow", d)
	}
	for _, p := range []string{"/login", "/generate", "/history", "/settings"} {
		d := Decide(appstate.Elevated, mustRoute(t, p))
		if d.Allow || d.RedirectTo != "/recover" {
			t.Errorf("Elevated -> %s: got %+v, want redirect to /recover", p, d)
		}
	}
}

func TestDecideUnknown(t *testing.T) {
	cases := []struct {
		s    appstate.State
		want string
	}{
		{appstate.Anon, "/login"},
		{appstate.Auth, "/generate"},
		{appstate.Disconnected, "/generate"},
		{appstate.Killed, "/generate"},
		{appstate.Elevated, "/recover"},
	}
	for _, c := range cases {
		if got := DecideUnknown(c.s); got != c.want {
			t.Errorf("DecideUnknown(%v) = %q, want %q", c.s, got, c.want)
		}
	}
}
