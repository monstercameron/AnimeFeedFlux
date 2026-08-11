package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// --- password reset tokens --------------------------------------------------
//
// The reset path is the one way into the account that does not need the
// current password, so its three obligations — single-use, expiring, and
// revoking every session on completion — are the ones worth pinning
// mechanically rather than by reading the SQL.

func TestPasswordResetTokenLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	now := time.Now().UTC()
	if err := s.CreatePasswordResetToken(ctx, "hash-live", now.Add(time.Hour)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}
	if err := s.CreatePasswordResetToken(ctx, "hash-expired", now.Add(-time.Minute)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	live, err := s.ActiveResetTokenHashes(ctx, now)
	if err != nil {
		t.Fatalf("ActiveResetTokenHashes: %v", err)
	}
	if len(live) != 1 || live[0] != "hash-live" {
		t.Fatalf("active hashes = %v, want [hash-live] — an expired token must not be a candidate", live)
	}
}

func TestCompletePasswordResetIsSingleUse(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	now := time.Now().UTC()
	if err := s.CreatePasswordResetToken(ctx, "hash-live", now.Add(time.Hour)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	if err := s.CompletePasswordReset(ctx, "hash-live", now, "new-hash", "kdf=new", 2); err != nil {
		t.Fatalf("CompletePasswordReset: %v", err)
	}

	// The password moved...
	admin, err := s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if admin.PasswordHash != "new-hash" || admin.PepperVersion != 2 {
		t.Errorf("admin = %+v, want the new hash and pepper version 2", admin)
	}

	// ...the token is spent...
	live, err := s.ActiveResetTokenHashes(ctx, now)
	if err != nil {
		t.Fatalf("ActiveResetTokenHashes: %v", err)
	}
	if len(live) != 0 {
		t.Errorf("a consumed token is still an active candidate: %v", live)
	}

	// ...and replaying it fails rather than resetting the password again.
	err = s.CompletePasswordReset(ctx, "hash-live", now, "second-hash", "kdf=new", 2)
	if !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("replaying a consumed token returned %v, want ErrResetTokenInvalid", err)
	}
	admin, err = s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if admin.PasswordHash != "new-hash" {
		t.Errorf("a rejected replay still wrote a password: %q", admin.PasswordHash)
	}
}

func TestCompletePasswordResetRevokesEverySession(t *testing.T) {
	// Skipping this leaves whoever caused the reset — the plausible reason a
	// reset is happening at all — signed in on their existing session.
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	now := time.Now().UTC()
	for _, hash := range []string{"sess-a", "sess-b"} {
		if _, err := s.CreateSession(ctx, auth.Session{
			TokenHash: hash, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
			IP: "127.0.0.1", UserAgent: "test-agent",
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	if err := s.CreatePasswordResetToken(ctx, "hash-live", now.Add(time.Hour)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}
	if err := s.CompletePasswordReset(ctx, "hash-live", now, "new-hash", "kdf=new", 0); err != nil {
		t.Fatalf("CompletePasswordReset: %v", err)
	}

	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no sessions came back at all")
	}
	for _, sess := range sessions {
		if sess.RevokedAt.IsZero() {
			t.Errorf("session %q survived a password reset", sess.TokenHash)
		}
	}
}

func TestCompletePasswordResetRejectsAnExpiredToken(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	now := time.Now().UTC()
	if err := s.CreatePasswordResetToken(ctx, "hash-old", now.Add(-time.Second)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}
	if err := s.CompletePasswordReset(ctx, "hash-old", now, "new-hash", "kdf", 0); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("an expired token returned %v, want ErrResetTokenInvalid", err)
	}
}

func TestCompletePasswordResetRejectsAnUnknownToken(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	if err := s.CompletePasswordReset(ctx, "never-issued", time.Now().UTC(), "h", "kdf", 0); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("an unknown token returned %v, want ErrResetTokenInvalid", err)
	}
}

func TestUpdatePasswordAndPepperRecordsTheGeneration(t *testing.T) {
	// The pepper version is what a later login uses to decide whether — and
	// under which key — to pepper the candidate. Writing the hash without it
	// makes the stored hash unverifiable after a rotation.
	s := newTestStore(t)
	ctx := t.Context()
	seedAdmin(t, s)

	if err := s.UpdatePasswordAndPepper(ctx, "peppered-hash", "kdf=v2", 3); err != nil {
		t.Fatalf("UpdatePasswordAndPepper: %v", err)
	}
	admin, err := s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if admin.PasswordHash != "peppered-hash" || admin.KDFParams != "kdf=v2" || admin.PepperVersion != 3 {
		t.Errorf("admin = %+v, want hash/kdf/pepperVersion to all have moved", admin)
	}
	if admin.PasswordChangedAt.IsZero() {
		t.Error("password_changed_at was not stamped")
	}

	// Version 0 means "no pepper", and must be writable — that is how a
	// rotation back to unpeppered is expressed.
	if err := s.UpdatePasswordAndPepper(ctx, "plain-hash", "kdf=v2", 0); err != nil {
		t.Fatalf("UpdatePasswordAndPepper: %v", err)
	}
	admin, err = s.GetAdmin(ctx)
	if err != nil {
		t.Fatalf("GetAdmin: %v", err)
	}
	if admin.PepperVersion != 0 {
		t.Errorf("pepper version = %d, want 0", admin.PepperVersion)
	}
}

// --- run heartbeat ----------------------------------------------------------

func TestHeartbeatRenewsOnlyARunningRun(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	before := runHeartbeat(t, s, runID)

	time.Sleep(2 * time.Millisecond) // the stored timestamp has sub-second precision
	if err := s.Heartbeat(ctx, runID); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if after := runHeartbeat(t, s, runID); !after.After(before) {
		t.Errorf("heartbeat did not advance: %s -> %s", before, after)
	}

	// A finished run has nothing left to renew — this is the case that keeps
	// a late heartbeat from resurrecting a run the watchdog already reclaimed.
	if err := s.CommitRun(ctx, runID, nil, RunSummary{}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}
	if err := s.Heartbeat(ctx, runID); !errors.Is(err, ErrNotFound) {
		t.Errorf("heartbeat on a finished run returned %v, want ErrNotFound", err)
	}

	if err := s.Heartbeat(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("heartbeat on a nonexistent run returned %v, want ErrNotFound", err)
	}
}

// --- spend by day -----------------------------------------------------------

func TestSpendByDayFillsEveryDayInTheWindow(t *testing.T) {
	// A day with no runs is drawn as an empty slot rather than skipped: a gap
	// is exactly what an operator reads this chart to find, and a chart that
	// silently omits quiet days shows a busy month.
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}

	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.CommitRun(ctx, runID, nil, RunSummary{TokensIn: 100, TokensOut: 50, CostUSD: 0.25}); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	since := time.Now().UTC().AddDate(0, 0, -3)
	days, err := s.SpendByDay(ctx, since)
	if err != nil {
		t.Fatalf("SpendByDay: %v", err)
	}
	if len(days) != 4 { // three days back, inclusive of both ends
		t.Fatalf("got %d days for a 3-day window, want 4: %+v", len(days), days)
	}

	var total float64
	var runs int64
	for i, d := range days {
		if d.Date == "" {
			t.Errorf("day %d has no date: %+v", i, d)
		}
		total += d.CostUSD
		runs += d.Runs
	}
	if total != 0.25 || runs != 1 {
		t.Errorf("window totals cost=%v runs=%d, want 0.25 and 1", total, runs)
	}

	// The days come back in calendar order, which is what makes the chart
	// readable without the caller sorting.
	for i := 1; i < len(days); i++ {
		if days[i-1].Date >= days[i].Date {
			t.Errorf("days are not in ascending order: %v", dayDates(days))
			break
		}
	}

	// Today carries the spend; every other slot is a real zero, not a gap.
	today := time.Now().UTC().Format("2006-01-02")
	for _, d := range days {
		if d.Date == today {
			if d.CostUSD != 0.25 || d.TokensIn != 100 || d.TokensOut != 50 {
				t.Errorf("today = %+v, want the committed run's figures", d)
			}
			continue
		}
		if d.Runs != 0 || d.CostUSD != 0 {
			t.Errorf("day %+v should be empty", d)
		}
	}
}

func TestSpendByDayCountsFailedRunsToo(t *testing.T) {
	// Money already spent is spent: a run that failed after paying the
	// provider must still show the money (§22 J5).
	s := newTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	runID, err := s.StartRun(ctx, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := s.FailRun(ctx, runID, "transient", "provider refused", RunSummary{CostUSD: 0.10, TokensIn: 40}); err != nil {
		t.Fatalf("FailRun: %v", err)
	}

	days, err := s.SpendByDay(ctx, time.Now().UTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("SpendByDay: %v", err)
	}
	var total float64
	for _, d := range days {
		total += d.CostUSD
	}
	if total != 0.10 {
		t.Errorf("window cost = %v, want 0.10 from the failed run", total)
	}
}

func TestSpendByDayWithNoRunsIsAllZeroes(t *testing.T) {
	s := newTestStore(t)
	days, err := s.SpendByDay(t.Context(), time.Now().UTC().AddDate(0, 0, -2))
	if err != nil {
		t.Fatalf("SpendByDay: %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("got %d days, want 3", len(days))
	}
	for _, d := range days {
		if d.Runs != 0 || d.CostUSD != 0 || d.TokensIn != 0 || d.TokensOut != 0 {
			t.Errorf("day %+v is not empty", d)
		}
	}
}

// --- helpers ----------------------------------------------------------------

// seedAdmin writes the single admin row these tests update. The migration
// creates the table, not the row.
func seedAdmin(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.Writer().ExecContext(context.Background(),
		`INSERT OR IGNORE INTO admin (id, password_hash, kdf_params, created_at)
		 VALUES (1, 'seed-hash', 'kdf=seed', ?)`, formatTime(time.Now()))
	if err != nil {
		t.Fatalf("seeding admin: %v", err)
	}
}

func runHeartbeat(t *testing.T, s *Store, runID int64) time.Time {
	t.Helper()
	var raw string
	if err := s.Reader().QueryRowContext(context.Background(),
		`SELECT heartbeat_at FROM runs WHERE id = ?`, runID).Scan(&raw); err != nil {
		t.Fatalf("reading heartbeat: %v", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parsing heartbeat %q: %v", raw, err)
	}
	return ts
}

func dayDates(days []DailySpend) []string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		out = append(out, d.Date)
	}
	return out
}
