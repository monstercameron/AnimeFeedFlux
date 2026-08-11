package store

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/auth"
)

// Every method here wraps its database error and returns it. That is easy to
// write and easy to get wrong in exactly one direction: a swallowed error on a
// WRITE reports success for something that did not happen — a revoked session
// that is still live, an item that was never soft-deleted, a run that still
// looks running. Nothing else in the suite exercised those branches, because
// making SQLite fail on demand normally means a fault-injection driver.
//
// A canceled context is the cheap, deterministic way in: database/sql checks
// it before handing the query to the driver, so every one of these calls takes
// its error path without any fixture needing to break.

func TestEveryOperationReportsACanceledContext(t *testing.T) {
	s := newTestStore(t)
	setup := t.Context()
	seedAdmin(t, s)

	// Real ids, so nothing below can pass merely by failing to find a row.
	feedID, err := s.CreateFeed(setup, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	if _, err := s.InsertItem(setup, makeItem(feedID, "item-1", time.Now().UTC())); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}
	runID, err := s.StartRun(setup, feedID, "manual", "worker-1")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	sessID, err := s.CreateSession(setup, auth.Session{
		TokenHash: "hash-1", CreatedAt: time.Now(), LastSeenAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sampleID, err := s.PutSample(setup, feedID, []byte(`{"items":[]}`), 1, 1, 0.01, time.Hour)
	if err != nil {
		t.Fatalf("PutSample: %v", err)
	}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	now := time.Now().UTC()
	ops := map[string]func() error{
		// admin / auth
		"InitAdmin":                func() error { return s.InitAdmin(dead, "h", "kdf") },
		"GetAdmin":                 func() error { _, err := s.GetAdmin(dead); return err },
		"UpdatePassword":           func() error { return s.UpdatePassword(dead, "h", "kdf") },
		"UpdatePasswordAndPepper":  func() error { return s.UpdatePasswordAndPepper(dead, "h", "kdf", 1) },
		"SetTOTPSecret":            func() error { return s.SetTOTPSecret(dead, []byte("enc")) },
		"GetTOTPSecret":            func() error { _, err := s.GetTOTPSecret(dead); return err },
		"MarkTOTPStepUsed":         func() error { return s.MarkTOTPStepUsed(dead, 1, "code") },
		"StoreRecoveryCodes":       func() error { return s.StoreRecoveryCodes(dead, []string{"a", "b"}) },
		"UseRecoveryCode":          func() error { return s.UseRecoveryCode(dead, 0) },
		"CountUnusedRecoveryCodes": func() error { _, err := s.CountUnusedRecoveryCodes(dead); return err },
		"RecordAuthEvent":          func() error { return s.RecordAuthEvent(dead, "login", "127.0.0.1", true, "") },
		"ListAuthEvents":           func() error { _, err := s.ListAuthEvents(dead, 10); return err },
		"RecentFailures":           func() error { _, err := s.RecentFailures(dead, "127.0.0.1", now); return err },

		// sessions
		"CreateSession":         func() error { _, err := s.CreateSession(dead, auth.Session{TokenHash: "x"}); return err },
		"GetSessionByTokenHash": func() error { _, err := s.GetSessionByTokenHash(dead, "hash-1"); return err },
		"TouchSession":          func() error { return s.TouchSession(dead, sessID, now) },
		"RevokeSession":         func() error { return s.RevokeSession(dead, sessID) },
		"RevokeAllSessions":     func() error { return s.RevokeAllSessions(dead) },
		"ListSessions":          func() error { _, err := s.ListSessions(dead); return err },
		"SetSessionScope":       func() error { return s.SetSessionScope(dead, sessID, "full") },
		"SessionScope":          func() error { _, err := s.SessionScope(dead, sessID); return err },
		"PurgeExpiredSessions":  func() error { _, err := s.PurgeExpiredSessions(dead, now); return err },
		"CreatePasswordResetToken": func() error {
			return s.CreatePasswordResetToken(dead, "h", now.Add(time.Hour))
		},
		"ActiveResetTokenHashes": func() error { _, err := s.ActiveResetTokenHashes(dead, now); return err },
		"CompletePasswordReset": func() error {
			return s.CompletePasswordReset(dead, "h", now, "new", "kdf", 0)
		},

		// feeds and items
		"CreateFeed":     func() error { _, err := s.CreateFeed(dead, makeFeed("other")); return err },
		"GetFeedBySlug":  func() error { _, err := s.GetFeedBySlug(dead, "trivia"); return err },
		"InsertItem":     func() error { _, err := s.InsertItem(dead, makeItem(feedID, "item-2", now)); return err },
		"GetItem":        func() error { _, err := s.GetItem(dead, "item-1"); return err },
		"ListItems":      func() error { _, err := s.ListItems(dead, feedID, 10, false); return err },
		"UpdateItem":     func() error { return s.UpdateItem(dead, makeItem(feedID, "item-1", now), 1) },
		"SoftDeleteItem": func() error { return s.SoftDeleteItem(dead, "item-1") },
		"RestoreItem":    func() error { return s.RestoreItem(dead, "item-1") },

		// runs
		"StartRun":         func() error { _, err := s.StartRun(dead, feedID, "manual", "w"); return err },
		"Heartbeat":        func() error { return s.Heartbeat(dead, runID) },
		"CommitRun":        func() error { return s.CommitRun(dead, runID, nil, RunSummary{}) },
		"FailRun":          func() error { return s.FailRun(dead, runID, "transient", "boom", RunSummary{}) },
		"SkipRun":          func() error { return s.SkipRun(dead, runID, "budget") },
		"ReclaimStaleRuns": func() error { _, _, err := s.ReclaimStaleRuns(dead, time.Minute); return err },
		"ListRuns":         func() error { _, err := s.ListRuns(dead, feedID, 10); return err },
		"SpendSince":       func() error { _, _, _, err := s.SpendSince(dead, feedID, now.AddDate(0, 0, -1)); return err },
		"SpendByDay":       func() error { _, err := s.SpendByDay(dead, now.AddDate(0, 0, -1)); return err },

		// samples
		"PutSample":           func() error { _, err := s.PutSample(dead, feedID, []byte("{}"), 0, 0, 0, time.Hour); return err },
		"GetSample":           func() error { _, err := s.GetSample(dead, sampleID); return err },
		"ListSamples":         func() error { _, err := s.ListSamples(dead, feedID); return err },
		"DiscardSample":       func() error { return s.DiscardSample(dead, sampleID) },
		"PruneExpiredSamples": func() error { _, err := s.PruneExpiredSamples(dead, now); return err },
		"PromoteSample": func() error {
			_, err := s.PromoteSample(dead, sampleID, makeItem(feedID, "item-3", now))
			return err
		},

		// embeddings and system
		"EmbeddingsForFeed":   func() error { _, err := s.EmbeddingsForFeed(dead, feedID, 10); return err },
		"ListAuditEventsPage": func() error { _, err := s.ListAuditEventsPage(dead, 0, 10); return err },
		"RunInFlight":         func() error { _, err := s.RunInFlight(dead); return err },
	}

	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			if err := op(); err == nil {
				t.Errorf("%s reported success on a canceled context — a caller would believe the write landed", name)
			}
		})
	}
}

// The reads above may legitimately report "not found" instead of the context
// error for a missing row; what they may never do is report SUCCESS. This
// separate check pins the one case where a false success is silently
// destructive rather than merely wrong: the writes that report how many rows
// they touched.
func TestFailedWritesDoNotReportRowsTouched(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	feedID, err := s.CreateFeed(ctx, makeFeed("trivia"))
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	if _, err := s.InsertItem(ctx, makeItem(feedID, "item-1", time.Now().UTC())); err != nil {
		t.Fatalf("InsertItem: %v", err)
	}

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	if n, err := s.PurgeExpiredSessions(dead, time.Now()); err == nil || n != 0 {
		t.Errorf("PurgeExpiredSessions returned (%d, %v), want (0, error)", n, err)
	}
	if n, err := s.PruneExpiredSamples(dead, time.Now()); err == nil || n != 0 {
		t.Errorf("PruneExpiredSamples returned (%d, %v), want (0, error)", n, err)
	}
	if reclaimed, unconfirmed, err := s.ReclaimStaleRuns(dead, time.Minute); err == nil || reclaimed != 0 || unconfirmed != 0 {
		t.Errorf("ReclaimStaleRuns returned (%d, %d, %v), want (0, 0, error)", reclaimed, unconfirmed, err)
	}

	// And the item really is untouched by the failed delete.
	if err := s.SoftDeleteItem(dead, "item-1"); err == nil {
		t.Error("SoftDeleteItem reported success on a canceled context")
	}
	it, err := s.GetItem(ctx, "item-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if it.IsDeleted() {
		t.Error("a failed SoftDeleteItem still deleted the item")
	}
}
