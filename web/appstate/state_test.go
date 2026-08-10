package appstate

import "testing"

func TestTransitionValid(t *testing.T) {
	cases := []struct {
		from State
		ev   Event
		want State
	}{
		{Anon, EvLoginSuccess, Auth},
		{Anon, EvRecoveryCodeAccepted, Elevated},
		{Elevated, EvRecoveryComplete, Anon},
		{Auth, EvLogout, Anon},
		{Disconnected, EvLogout, Anon},
		{Killed, EvLogout, Anon},
		{Elevated, EvLogout, Anon},
		{Auth, EvSessionExpired, Anon},
		{Disconnected, EvSessionExpired, Anon},
		{Killed, EvSessionExpired, Anon},
		{Elevated, EvSessionExpired, Anon},
		{Auth, EvWSDropped, Disconnected},
		{Killed, EvWSDropped, Disconnected},
		{Disconnected, EvWSReconnected, Auth},
		{Auth, EvKillSwitchOff, Killed},
		{Killed, EvKillSwitchOn, Auth},
	}
	for _, c := range cases {
		got, ok := Transition(c.from, c.ev)
		if !ok {
			t.Errorf("Transition(%v, %v) rejected, want %v", c.from, c.ev, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("Transition(%v, %v) = %v, want %v", c.from, c.ev, got, c.want)
		}
	}
}

func TestTransitionInvalidRejected(t *testing.T) {
	cases := []struct {
		from State
		ev   Event
	}{
		{Auth, EvLoginSuccess},         // already authed
		{Anon, EvWSDropped},            // no socket to drop
		{Anon, EvKillSwitchOff},        // not authed
		{Elevated, EvKillSwitchOff},    // wrong state
		{Auth, EvWSReconnected},        // not disconnected
		{Anon, EvRecoveryComplete},     // not elevated
		{Killed, EvKillSwitchOff},      // already killed
		{Disconnected, EvKillSwitchOn}, // must reconnect first
	}
	for _, c := range cases {
		got, ok := Transition(c.from, c.ev)
		if ok {
			t.Errorf("Transition(%v, %v) accepted -> %v, want rejected", c.from, c.ev, got)
		}
		if got != c.from {
			t.Errorf("Transition(%v, %v) changed state to %v on rejection, want unchanged", c.from, c.ev, got)
		}
	}
}

func TestCanNavigate(t *testing.T) {
	cases := []struct {
		s            State
		requiresAuth bool
		want         bool
	}{
		{Anon, false, true},
		{Anon, true, false},
		{Auth, true, true},
		{Auth, false, false},
		{Disconnected, true, true},
		{Killed, true, true},
		{Elevated, true, false},
		{Elevated, false, false},
	}
	for _, c := range cases {
		if got := CanNavigate(c.s, c.requiresAuth); got != c.want {
			t.Errorf("CanNavigate(%v, %v) = %v, want %v", c.s, c.requiresAuth, got, c.want)
		}
	}
}

func TestIsAuthedIsh(t *testing.T) {
	for _, s := range []State{Auth, Disconnected, Killed} {
		if !IsAuthedIsh(s) {
			t.Errorf("IsAuthedIsh(%v) = false, want true", s)
		}
	}
	for _, s := range []State{Anon, Elevated} {
		if IsAuthedIsh(s) {
			t.Errorf("IsAuthedIsh(%v) = true, want false", s)
		}
	}
}

func TestStringers(t *testing.T) {
	if Auth.String() != "AUTH" || Disconnected.String() != "DISCONNECTED" {
		t.Fatalf("State.String() mismatch: %q %q", Auth, Disconnected)
	}
	if EvWSDropped.String() != "WSDropped" {
		t.Fatalf("Event.String() mismatch: %q", EvWSDropped)
	}
}
