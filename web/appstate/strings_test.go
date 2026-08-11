package appstate

import "testing"

// These names are not decoration: they are what the shell logs and what a
// D-FLOW trace reads back as. A state or event added without a String case
// silently becomes "UNKNOWN"/"UnknownEvent" in every log line that mentions
// it, which is worst precisely when someone is reading logs to work out how
// the machine reached a state it should not be in.

func TestStateStringNamesEveryState(t *testing.T) {
	want := map[State]string{
		Anon:         "ANON",
		Auth:         "AUTH",
		Elevated:     "ELEVATED",
		Disconnected: "DISCONNECTED",
		Killed:       "KILLED",
	}
	for s, name := range want {
		if got := s.String(); got != name {
			t.Errorf("State(%d).String() = %q, want %q", s, got, name)
		}
	}
	if got := State(99).String(); got != "UNKNOWN" {
		t.Errorf("an out-of-range State stringified as %q, want UNKNOWN", got)
	}
	// Every declared state is covered above. Killed is the highest, so a new
	// state appended after it fails here until it is named.
	if int(Killed) != len(want)-1 {
		t.Errorf("a State was added without a name: highest is %d, %d are named", Killed, len(want))
	}
}

func TestEventStringNamesEveryEvent(t *testing.T) {
	want := map[Event]string{
		EvLoginSuccess:         "LoginSuccess",
		EvRecoveryCodeAccepted: "RecoveryCodeAccepted",
		EvRecoveryComplete:     "RecoveryComplete",
		EvLogout:               "Logout",
		EvSessionExpired:       "SessionExpired",
		EvWSDropped:            "WSDropped",
		EvWSReconnected:        "WSReconnected",
		EvKillSwitchOff:        "KillSwitchOff",
		EvKillSwitchOn:         "KillSwitchOn",
	}
	seen := map[string]Event{}
	for e, name := range want {
		got := e.String()
		if got != name {
			t.Errorf("Event(%d).String() = %q, want %q", e, got, name)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("events %d and %d share the name %q", prev, e, got)
		}
		seen[got] = e
	}
	if got := Event(99).String(); got != "UnknownEvent" {
		t.Errorf("an out-of-range Event stringified as %q, want UnknownEvent", got)
	}
	if int(EvKillSwitchOn) != len(want)-1 {
		t.Errorf("an Event was added without a name: highest is %d, %d are named", EvKillSwitchOn, len(want))
	}
}
