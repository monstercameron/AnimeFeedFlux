// Package appstate implements the five application states from TODOS.md
// D-FLOW / PLAN.md §12 as a pure, host-testable state machine. It has no
// dependency on the browser or the network client — callers (the js-only
// shell) feed it Events as things happen (login succeeds, the socket
// drops, the session expires) and read back the resulting State.
//
// "The UI is always in exactly one" of:
//
//	ANON          no session, or session expired         -> /login, /recover
//	AUTH          password + TOTP accepted                -> /generate, /history, /settings
//	ELEVATED      valid recovery code, 10-minute window    -> password change + TOTP re-enroll only
//	DISCONNECTED  WebSocket dropped while in AUTH          -> read-only view, reconnect banner
//	KILLED        generation kill switch is off            -> everything except generate/sample actions
package appstate

// State is one of the five exclusive application states named in D-FLOW.
type State int

const (
	Anon State = iota
	Auth
	Elevated
	Disconnected
	Killed
)

func (s State) String() string {
	switch s {
	case Anon:
		return "ANON"
	case Auth:
		return "AUTH"
	case Elevated:
		return "ELEVATED"
	case Disconnected:
		return "DISCONNECTED"
	case Killed:
		return "KILLED"
	default:
		return "UNKNOWN"
	}
}

// Event is something that happened that may move the state machine.
type Event int

const (
	// EvLoginSuccess: password + TOTP both accepted (PLAN.md §12.1).
	// ANON -> AUTH.
	EvLoginSuccess Event = iota
	// EvRecoveryCodeAccepted: a single-use recovery code was consumed
	// (PLAN.md §12.2). ANON -> ELEVATED.
	EvRecoveryCodeAccepted
	// EvRecoveryComplete: password reset (and/or TOTP re-enrollment) done
	// from ELEVATED. §12.2 requires this "forces a full re-login and
	// revokes every other session" — so it lands back on ANON, not AUTH;
	// the admin must log in again with the new credential.
	// ELEVATED -> ANON.
	EvRecoveryComplete
	// EvLogout: the admin signed out. Any authed-ish state -> ANON.
	EvLogout
	// EvSessionExpired: the session's absolute or idle lifetime elapsed,
	// or the server otherwise revoked it (D0-08). Any authed-ish state
	// (AUTH, DISCONNECTED, KILLED) or ELEVATED -> ANON. This is the one
	// event the shell must route through the "don't silently lose
	// unsaved work" hook (see web/shell) before acting on it.
	EvSessionExpired
	// EvWSDropped: the single control-plane WebSocket dropped while in
	// AUTH or KILLED (laptop sleep, nginx timeout, a deploy — PLAN.md
	// §12/D-FLOW: "not an edge case"). -> DISCONNECTED.
	EvWSDropped
	// EvWSReconnected: the socket came back and the session re-validated.
	// DISCONNECTED -> AUTH. If the kill switch is off server-side, the
	// caller is expected to immediately follow this with
	// EvKillSwitchOff once that is known (see doc note below) rather
	// than this package guessing at connectivity-vs-kill-switch
	// interaction.
	EvWSReconnected
	// EvKillSwitchOff: the generation kill switch was turned off
	// (globally, via Settings — PLAN.md §12.5). AUTH -> KILLED.
	EvKillSwitchOff
	// EvKillSwitchOn: the kill switch was turned back on. KILLED -> AUTH.
	EvKillSwitchOn
)

func (e Event) String() string {
	switch e {
	case EvLoginSuccess:
		return "LoginSuccess"
	case EvRecoveryCodeAccepted:
		return "RecoveryCodeAccepted"
	case EvRecoveryComplete:
		return "RecoveryComplete"
	case EvLogout:
		return "Logout"
	case EvSessionExpired:
		return "SessionExpired"
	case EvWSDropped:
		return "WSDropped"
	case EvWSReconnected:
		return "WSReconnected"
	case EvKillSwitchOff:
		return "KillSwitchOff"
	case EvKillSwitchOn:
		return "KillSwitchOn"
	default:
		return "UnknownEvent"
	}
}

// Modeling note on DISCONNECTED vs KILLED overlap: D-FLOW's table names
// five *exclusive* states, but describes two independent real-world
// conditions (socket connectivity, and the server-side kill switch) that
// can in principle co-occur. This package resolves the overlap by giving
// connectivity priority: EvWSDropped always lands on DISCONNECTED
// (including from KILLED), because "read-only, reconnect banner" is a
// strictly more urgent thing to tell the admin than the kill switch
// state, and because KILLED-ness cannot be freshly confirmed once the one
// channel that would confirm it is gone. Reconnecting always lands back
// on AUTH; if the kill switch is off, the caller re-derives that from the
// next server response (e.g. a Settings read or a stream push) and sends
// EvKillSwitchOff again. This is a documented modeling choice, not
// something PLAN.md specifies explicitly.

// Transition applies event to current and returns the resulting state.
// ok is false for a combination this package considers invalid (e.g.
// EvLoginSuccess while already Auth) — Transition never panics, and an
// invalid transition leaves the state unchanged so a caller can log or
// assert on it rather than have the UI silently jump somewhere wrong.
func Transition(current State, ev Event) (State, bool) {
	switch ev {
	case EvLoginSuccess:
		if current == Anon {
			return Auth, true
		}
	case EvRecoveryCodeAccepted:
		if current == Anon {
			return Elevated, true
		}
	case EvRecoveryComplete:
		if current == Elevated {
			return Anon, true
		}
	case EvLogout:
		if current == Auth || current == Disconnected || current == Killed || current == Elevated {
			return Anon, true
		}
	case EvSessionExpired:
		if current == Auth || current == Disconnected || current == Killed || current == Elevated {
			return Anon, true
		}
	case EvWSDropped:
		if current == Auth || current == Killed {
			return Disconnected, true
		}
	case EvWSReconnected:
		if current == Disconnected {
			return Auth, true
		}
	case EvKillSwitchOff:
		if current == Auth {
			return Killed, true
		}
	case EvKillSwitchOn:
		if current == Killed {
			return Auth, true
		}
	}
	return current, false
}

// CanNavigate reports whether state permits navigating to a route with
// the given RequiresAuth flag, per D-FLOW's per-state "what is reachable"
// column:
//
//	ANON          -> only !RequiresAuth routes (/login, /recover)
//	ELEVATED      -> neither /login,/recover-style nor the three authed
//	                 routes; ELEVATED has its own single reachable
//	                 surface (password reset / TOTP re-enrollment on
//	                 /recover) handled by the caller, not by route auth
//	                 alone — see web/guard.
//	AUTH/DISCONNECTED/KILLED -> RequiresAuth routes (/generate,
//	                 /history, /settings); DISCONNECTED and KILLED
//	                 restrict what those pages let you *do*, not which
//	                 of the three you can *navigate to*.
func CanNavigate(s State, requiresAuth bool) bool {
	switch s {
	case Anon:
		return !requiresAuth
	case Auth, Disconnected, Killed:
		return requiresAuth
	case Elevated:
		return false
	default:
		return false
	}
}

// IsAuthedIsh reports whether s is one of the three states that hold a
// live session in some form (AUTH, DISCONNECTED, KILLED) as opposed to
// having none (ANON) or a restricted recovery-only one (ELEVATED).
func IsAuthedIsh(s State) bool {
	return s == Auth || s == Disconnected || s == Killed
}
