package settings

import (
	"testing"
	"time"
)

func TestSortSessionsForDisplay(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	rows := []SessionRow{
		{ID: "old", LastSeenAt: now.Add(-2 * time.Hour)},
		{ID: "current", LastSeenAt: now.Add(-10 * time.Hour), IsCurrent: true},
		{ID: "recent", LastSeenAt: now.Add(-1 * time.Minute)},
	}
	got := SortSessionsForDisplay(rows)
	wantOrder := []string{"current", "recent", "old"}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d rows, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
	// Original slice must be unmodified (defensive copy).
	if rows[0].ID != "old" {
		t.Error("SortSessionsForDisplay mutated its input slice")
	}
}

func TestSessionRow_Revoked(t *testing.T) {
	live := SessionRow{}
	if live.Revoked() {
		t.Error("zero-value RevokedAt must report not revoked")
	}
	dead := SessionRow{RevokedAt: time.Now()}
	if !dead.Revoked() {
		t.Error("set RevokedAt must report revoked")
	}
}

func TestRevocableSessionCount(t *testing.T) {
	rows := []SessionRow{
		{ID: "a"},
		{ID: "b", RevokedAt: time.Now()},
		{ID: "c"},
	}
	if got := RevocableSessionCount(rows); got != 2 {
		t.Errorf("RevocableSessionCount = %d, want 2", got)
	}
}

func TestRecoveryCodesLow(t *testing.T) {
	cases := map[int]bool{
		0:  true,
		1:  true,
		2:  true,
		3:  false,
		10: false,
	}
	for remaining, want := range cases {
		if got := RecoveryCodesLow(remaining); got != want {
			t.Errorf("RecoveryCodesLow(%d) = %v, want %v", remaining, got, want)
		}
	}
}
