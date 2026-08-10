package settings

import "testing"

func TestConfirmationMatches(t *testing.T) {
	cases := []struct {
		name  string
		typed string
		want  string
		match bool
	}{
		{"exact match", "delete", "delete", true},
		{"case mismatch rejected", "DELETE", "delete", false},
		{"trailing space rejected", "delete ", "delete", false},
		{"leading space rejected", " delete", "delete", false},
		{"partial rejected", "delet", "delete", false},
		{"empty typed against empty want matches", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ConfirmationMatches(c.typed, c.want); got != c.match {
				t.Errorf("ConfirmationMatches(%q, %q) = %v, want %v", c.typed, c.want, got, c.match)
			}
		})
	}
}

func TestRequiresTypedConfirmation(t *testing.T) {
	irreversible := map[DestructiveAction]bool{
		ActionRevokeOneSession:        false,
		ActionRevokeAllSessions:       true,
		ActionRegenerateRecoveryCodes: true,
		ActionReenrollTOTP:            false,
		ActionChangePassword:          false,
		ActionVacuum:                  false,
		ActionImportTOML:              true,
	}
	for action, want := range irreversible {
		if got := RequiresTypedConfirmation(action); got != want {
			t.Errorf("RequiresTypedConfirmation(%v) = %v, want %v", action, got, want)
		}
	}
}

func TestConfirmationWordKey_OnlyForTypedActions(t *testing.T) {
	for action := ActionRevokeOneSession; action <= ActionImportTOML; action++ {
		key := ConfirmationWordKey(action)
		if RequiresTypedConfirmation(action) && key == "" {
			t.Errorf("action %v requires typed confirmation but has no confirmation word key", action)
		}
		if !RequiresTypedConfirmation(action) && key != "" {
			t.Errorf("action %v does not require typed confirmation but has key %q", action, key)
		}
	}
}

func TestRevokeAllWarningKey_SEC46(t *testing.T) {
	// SEC-46: changing the password must warn BEFORE it revokes every
	// other session, and revoking all sessions must warn it includes the
	// caller's own.
	if RevokeAllWarningKey(ActionChangePassword) == "" {
		t.Error("ChangePassword must carry a pre-action revoke warning (SEC-46)")
	}
	if RevokeAllWarningKey(ActionRevokeAllSessions) == "" {
		t.Error("RevokeAllSessions must carry a pre-action warning")
	}
	if RevokeAllWarningKey(ActionVacuum) != "" {
		t.Error("Vacuum does not revoke sessions and must not carry a revoke warning")
	}
}
