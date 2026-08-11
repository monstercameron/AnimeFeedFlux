package settings

import (
	"google.golang.org/protobuf/types/known/timestamppb"
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

// TestProtoTimeAbsentIsZero locks in the distinction that made every session
// report itself revoked: timestamppb's own AsTime() maps a nil message to
// 1970, and IsZero() is false for 1970.
func TestProtoTimeAbsentIsZero(t *testing.T) {
	if got := protoTime(nil); !got.IsZero() {
		t.Fatalf("protoTime(nil) = %v, want the zero time (IsZero) — a nil timestamp must not read as 1970", got)
	}
	// The trap itself, asserted directly so this test explains why the
	// helper exists rather than merely exercising it.
	if (&timestamppb.Timestamp{}).AsTime().IsZero() {
		t.Fatal("timestamppb zero AsTime() is now IsZero(); protoTime may be unnecessary — re-check before deleting it")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := protoTime(timestamppb.New(now)); !got.Equal(now) {
		t.Fatalf("protoTime(now) = %v, want %v", got, now)
	}
}

// TestRevokedRequiresARealTimestamp is the end of the same bug: a row built
// from a session the server never revoked must not report itself revoked.
func TestRevokedRequiresARealTimestamp(t *testing.T) {
	live := SessionRow{RevokedAt: protoTime(nil)}
	if live.Revoked() {
		t.Fatal("a session with no revoked_at reports Revoked() — the Actions column would offer no revoke on any row")
	}
	dead := SessionRow{RevokedAt: protoTime(timestamppb.New(time.Now()))}
	if !dead.Revoked() {
		t.Fatal("a session with a real revoked_at does not report Revoked()")
	}
}
