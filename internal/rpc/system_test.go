package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

func sysTestStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "aff.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// sysFakeGetenv returns a Getenv closure backed by a plain map, so API-key
// presence can be flipped per test without touching the real process
// environment (which would make tests order-dependent under -shuffle).
func sysFakeGetenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestSystemGetSettingsNeverLeaksAPIKeyOnlyPresence(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()

	t.Run("key set in environment reports present, never the value", func(t *testing.T) {
		srv := NewSystemServer(s, nil, WithGetenv(sysFakeGetenv(map[string]string{
			sysProviderAPIKeyEnv: "sk-super-secret-value",
		})))
		resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
		if err != nil {
			t.Fatalf("get settings: %v", err)
		}
		if !resp.GetSettings().GetProvider().GetApiKeyPresent() {
			t.Fatal("api_key_present should be true when the env var is set")
		}
		// There is no field on Settings.Provider that could carry the key
		// itself (proto/aff/v1/system.proto only has api_key_present), so
		// the structural guarantee is that GetSettings never even sees the
		// raw value: assert the secret string appears nowhere in the
		// serialized response.
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		if bytes.Contains(raw, []byte("sk-super-secret-value")) {
			t.Fatal("GetSettings response leaked the raw API key")
		}
	})

	t.Run("key absent reports not present", func(t *testing.T) {
		srv := NewSystemServer(s, nil, WithGetenv(sysFakeGetenv(nil)))
		resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
		if err != nil {
			t.Fatalf("get settings: %v", err)
		}
		if resp.GetSettings().GetProvider().GetApiKeyPresent() {
			t.Fatal("api_key_present should be false when the env var is unset")
		}
	})
}

// TestUpdateSettingsCannotSetAPIKey confirms there is structurally no way to
// write a key through this RPC: whatever a client sends for
// api_key_present is discarded, and presence is always recomputed live.
func TestSystemUpdateSettingsCannotSetAPIKey(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil, WithGetenv(sysFakeGetenv(nil)))

	_, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Provider: &affv1.Settings_Provider{
				ActiveProvider: "openai",
				ApiKeyPresent:  true, // a hostile or confused client claiming presence
			},
		},
	})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	// api_key_present must still reflect the (unset) environment, not what
	// the client sent.
	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if resp.GetSettings().GetProvider().GetApiKeyPresent() {
		t.Fatal("api_key_present must be derived from the environment, never accepted from a client")
	}
	if got := resp.GetSettings().GetProvider().GetActiveProvider(); got != "openai" {
		t.Fatalf("active_provider = %q, want %q (the rest of the provider section should still save)", got, "openai")
	}
}

func TestSystemUpdateSettingsRejectsRelativeBaseURL(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	_, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Publishing: &affv1.Settings_Publishing{
				PublicBaseUrl: "/feeds", // relative: no scheme, no host
			},
		},
	})
	if err == nil {
		t.Fatal("expected an error for a relative public_base_url")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("error code = %v, want InvalidArgument", status.Code(err))
	}

	// Nothing should have been persisted: a rejected save must not leave a
	// half-applied publishing section behind.
	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got := resp.GetSettings().GetPublishing().GetPublicBaseUrl(); got != "" {
		t.Fatalf("public_base_url = %q after a rejected update, want unset", got)
	}
}

func TestSystemUpdateSettingsAcceptsAbsoluteBaseURLAndRejectsNonHTTPScheme(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	if _, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Publishing: &affv1.Settings_Publishing{PublicBaseUrl: "https://feeds.example.com"},
		},
	}); err != nil {
		t.Fatalf("update with absolute https url: %v", err)
	}
	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got := resp.GetSettings().GetPublishing().GetPublicBaseUrl(); got != "https://feeds.example.com" {
		t.Fatalf("public_base_url = %q, want the saved value", got)
	}

	_, err = srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Publishing: &affv1.Settings_Publishing{PublicBaseUrl: "ftp://feeds.example.com"},
		},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ftp scheme: error code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestSystemUpdateSettingsOnlyReplacesSectionsPresentInRequest(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	if _, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Generation: &affv1.Settings_Generation{DefaultDailyRunBudget: 7},
		},
	}); err != nil {
		t.Fatalf("first update (generation only): %v", err)
	}
	if _, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{
			Publishing: &affv1.Settings_Publishing{PublicBaseUrl: "https://feeds.example.com"},
		},
	}); err != nil {
		t.Fatalf("second update (publishing only): %v", err)
	}

	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got := resp.GetSettings().GetGeneration().GetDefaultDailyRunBudget(); got != 7 {
		t.Fatalf("default_daily_run_budget = %d, want 7 (the earlier section-only update must survive)", got)
	}
	if got := resp.GetSettings().GetPublishing().GetPublicBaseUrl(); got != "https://feeds.example.com" {
		t.Fatalf("public_base_url = %q, want the second update's value", got)
	}
}

// TestSetGenerationEnabledBlocksGenerationNotServing exercises PLAN.md §13's
// kill switch: it flips settings.generation.enabled and nothing else. This
// RPC has no dependency on and makes no call into the publish plane at all
// (it only writes one `settings` row), which is the structural guarantee
// behind "existing feeds keep serving, nothing generates" — there is no code
// path here that could touch feed serving even by accident.
func TestSystemSetGenerationEnabledBlocksGenerationNotServing(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil, WithDefaultGenerationEnabled(true))

	stats, err := srv.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !stats.GetGenerationEnabled() {
		t.Fatal("generation should default to enabled (WithDefaultGenerationEnabled(true), no settings row yet)")
	}

	resp, err := srv.SetGenerationEnabled(ctx, &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: false})
	if err != nil {
		t.Fatalf("set generation enabled: %v", err)
	}
	if resp.GetEnabled() {
		t.Fatal("response should echo the new (disabled) state")
	}

	stats, err = srv.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		t.Fatalf("stats after disable: %v", err)
	}
	if stats.GetGenerationEnabled() {
		t.Fatal("kill switch did not persist: Stats still reports generation enabled")
	}

	settings, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("get settings after disable: %v", err)
	}
	if settings.GetSettings().GetGeneration().GetEnabled() {
		t.Fatal("GetSettings should agree with Stats and SetGenerationEnabled about the kill switch")
	}

	// Flipping it back on must also persist.
	if _, err := srv.SetGenerationEnabled(ctx, &affv1.SystemServiceSetGenerationEnabledRequest{Enabled: true}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	stats, err = srv.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		t.Fatalf("stats after re-enable: %v", err)
	}
	if !stats.GetGenerationEnabled() {
		t.Fatal("re-enabling did not persist")
	}
}

// TestStatsCostFieldsAreEstimatesAndSumCorrectly checks both halves of
// PLAN.md §8.1/§13's cost rule: the arithmetic (Stats.today_spend_usd sums
// exactly what runs recorded) and the label (Run.est_cost_usd's own proto
// doc, "Estimated, not authoritative", is what Stats is built from — there
// is no second, unlabelled cost number anywhere in this file; TodaySpendUsd
// is a straight sum of that same estimated column via Store.SpendSince).
func TestSystemStatsCostFieldsAreEstimatesAndSumCorrectly(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	feedID, err := s.CreateFeed(ctx, model.Feed{
		Slug: "trivia", Title: "Trivia", Kind: model.KindGenerative, Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	runID, err := s.StartRun(ctx, feedID, "manual", "w1")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := s.CommitRun(ctx, runID, nil, store.RunSummary{TokensIn: 1000, TokensOut: 500, CostUSD: 0.42}); err != nil {
		t.Fatalf("commit run: %v", err)
	}

	failRunID, err := s.StartRun(ctx, feedID, "manual", "w1")
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	// PLAN.md §22 J5: "tokens and cost are recorded even for failed runs".
	if err := s.FailRun(ctx, failRunID, "transient", "timeout", store.RunSummary{CostUSD: 0.08}); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	srv := NewSystemServer(s, nil)
	if _, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Generation: &affv1.Settings_Generation{GlobalDailySpendCeilingUsd: 1.0}},
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	stats, err := srv.Stats(ctx, &affv1.SystemServiceStatsRequest{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	const want = 0.42 + 0.08
	if diff := stats.GetTodaySpendUsd() - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("today_spend_usd = %v, want %v (must include the failed run's cost per §22 J5)", stats.GetTodaySpendUsd(), want)
	}
	wantRemaining := 1.0 - want
	if diff := stats.GetTodayRemainingBudgetUsd() - wantRemaining; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("today_remaining_budget_usd = %v, want %v", stats.GetTodayRemainingBudgetUsd(), wantRemaining)
	}
}

func TestSystemVersionReportsConfiguredInfo(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	started := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	srv := NewSystemServer(s, nil, WithVersionInfo("v1.2.3", "abc123", started))

	resp, err := srv.Version(ctx, &affv1.SystemServiceVersionRequest{})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if resp.GetVersion() != "v1.2.3" || resp.GetBuild() != "abc123" {
		t.Fatalf("version/build = %q/%q, want v1.2.3/abc123", resp.GetVersion(), resp.GetBuild())
	}
	if !resp.GetStartedAt().AsTime().Equal(started) {
		t.Fatalf("started_at = %v, want %v", resp.GetStartedAt().AsTime(), started)
	}
}

// fakeCredentialVerifier accepts exactly one password/code pair and records
// what it was asked, so the tests below can tell "the gate ran and passed"
// apart from "the gate was never consulted".
type fakeCredentialVerifier struct {
	wantPassword string
	wantCode     string
	calls        int
}

func (f *fakeCredentialVerifier) VerifyCurrentCredentials(_ context.Context, password, totpCode string, _ time.Time) error {
	f.calls++
	if password != f.wantPassword || totpCode != f.wantCode {
		return status.Error(codes.PermissionDenied, "rpc: bad credentials")
	}
	return nil
}

func TestSystemBackupProducesAValidSQLiteFile(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	v := &fakeCredentialVerifier{wantPassword: "correct horse battery staple", wantCode: "123456"}
	srv := NewSystemServer(s, nil, WithCredentialVerifier(v))

	resp, err := srv.Backup(ctx, &affv1.SystemServiceBackupRequest{
		CurrentPassword: v.wantPassword,
		TotpCode:        v.wantCode,
	})
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if resp.GetFilename() == "" {
		t.Fatal("backup response has no filename")
	}
	// SQLite's on-disk format always starts with this 16-byte magic header.
	const sqliteMagic = "SQLite format 3\x00"
	if len(resp.GetDbFile()) < len(sqliteMagic) || string(resp.GetDbFile()[:len(sqliteMagic)]) != sqliteMagic {
		t.Fatal("backup file does not start with the SQLite header")
	}
}

// The backup file is every credential in the database in one download, so a
// live session alone must not produce one: a stolen-but-not-yet-expired
// session would otherwise be a full credential exfiltration (A8-40).
func TestSystemBackupRefusesWithoutCredentialReproof(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	v := &fakeCredentialVerifier{wantPassword: "correct horse battery staple", wantCode: "123456"}
	srv := NewSystemServer(s, nil, WithCredentialVerifier(v))

	for _, tc := range []struct {
		name     string
		password string
		code     string
	}{
		{"nothing at all", "", ""},
		{"right password, wrong code", "correct horse battery staple", "000000"},
		{"wrong password, right code", "hunter2hunter2hunter2", "123456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := srv.Backup(ctx, &affv1.SystemServiceBackupRequest{
				CurrentPassword: tc.password,
				TotpCode:        tc.code,
			})
			if status.Code(err) != codes.PermissionDenied {
				t.Fatalf("backup = %v, want PermissionDenied", err)
			}
			// Not merely "an error": no bytes may come back alongside one.
			if len(resp.GetDbFile()) != 0 {
				t.Fatalf("a denied backup still returned %d bytes", len(resp.GetDbFile()))
			}
		})
	}
	if v.calls != 3 {
		t.Fatalf("verifier consulted %d times, want 3 — a case skipped the gate", v.calls)
	}
}

// With no verifier wired at all, Backup must fail closed. Defaulting to
// "allow" here would mean one missing option in wire.go silently reopens
// exactly the hole the gate exists to close.
func TestSystemBackupFailsClosedWithNoVerifier(t *testing.T) {
	s := sysTestStore(t)
	srv := NewSystemServer(s, nil)

	_, err := srv.Backup(t.Context(), &affv1.SystemServiceBackupRequest{
		CurrentPassword: "correct horse battery staple",
		TotpCode:        "123456",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("backup with no verifier = %v, want FailedPrecondition", err)
	}
}

// --- ListAuditEvents -------------------------------------------------

func TestSystemListAuditEventsEmptyLogReturnsEmptyNotError(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	resp, err := srv.ListAuditEvents(ctx, &affv1.SystemServiceListAuditEventsRequest{})
	if err != nil {
		t.Fatalf("list audit events on an empty log: %v", err)
	}
	if len(resp.GetEvents()) != 0 {
		t.Fatalf("events = %d, want 0 on a fresh database", len(resp.GetEvents()))
	}
	if resp.GetNextPageToken() != "" {
		t.Fatalf("next_page_token = %q, want empty on an empty log", resp.GetNextPageToken())
	}
}

func TestSystemListAuditEventsNewestFirstWithWorkingPagination(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	// Five events, alternating outcome, distinguishable by kind so the
	// order assertion below is unambiguous.
	kinds := []string{"login", "recover", "login", "change_password", "reenroll_totp"}
	for i, kind := range kinds {
		if err := s.RecordAuthEvent(ctx, kind, "203.0.113.1", i%2 == 0, "seed"); err != nil {
			t.Fatalf("seeding auth event %d: %v", i, err)
		}
	}

	// page_size=2 forces three pages for five rows.
	var allKinds []string
	token := ""
	for page := 0; page < 10; page++ { // bounded loop: never trust pagination to terminate on its own in a test
		resp, err := srv.ListAuditEvents(ctx, &affv1.SystemServiceListAuditEventsRequest{
			PageSize:  2,
			PageToken: token,
		})
		if err != nil {
			t.Fatalf("list audit events page %d: %v", page, err)
		}
		for _, e := range resp.GetEvents() {
			allKinds = append(allKinds, e.GetKind())
		}
		if resp.GetNextPageToken() == "" {
			break
		}
		token = resp.GetNextPageToken()
	}

	// Insertion order was login, recover, login, change_password,
	// reenroll_totp — newest-first across all pages reverses that with no
	// duplicate and no drop at a page boundary.
	want := []string{"reenroll_totp", "change_password", "login", "recover", "login"}
	if len(allKinds) != len(want) {
		t.Fatalf("collected %d events across pages, want %d: %v", len(allKinds), len(want), allKinds)
	}
	for i := range want {
		if allKinds[i] != want[i] {
			t.Fatalf("event %d = %q, want %q (full order %v)", i, allKinds[i], want[i], allKinds)
		}
	}
}

func TestSystemListAuditEventsRejectsInvalidPageToken(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	_, err := srv.ListAuditEvents(ctx, &affv1.SystemServiceListAuditEventsRequest{
		PageToken: "not a real cursor",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("error = %v, want codes.InvalidArgument", err)
	}
}

// TestSystemListAuditEventsNoSecretMaterial is the load-bearing security
// assertion for this RPC (see AuditEvent's doc comment in
// proto/aff/v1/system.proto): even if a write site somehow put something
// sensitive in auth_events.detail, this RPC could not leak it, because
// AuditEvent has no field for detail at all. Simulated here with a
// deliberately planted "secret" in detail — RecordAuthEvent's real call
// sites never do this (internal/rpc/auth_test.go asserts that separately),
// this test is about what ListAuditEvents's wire shape can and cannot
// carry, independent of write-side discipline.
func TestSystemListAuditEventsNoSecretMaterial(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	const planted = "sk-totally-secret-value-should-never-appear"
	if err := s.RecordAuthEvent(ctx, "login", "203.0.113.1", true, planted); err != nil {
		t.Fatalf("seeding auth event: %v", err)
	}

	resp, err := srv.ListAuditEvents(ctx, &affv1.SystemServiceListAuditEventsRequest{})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if bytes.Contains(raw, []byte(planted)) {
		t.Fatal("ListAuditEvents response leaked auth_events.detail")
	}
	if len(resp.GetEvents()) != 1 {
		t.Fatalf("events = %d, want 1", len(resp.GetEvents()))
	}
	e := resp.GetEvents()[0]
	if e.GetKind() != "login" || e.GetIp() != "203.0.113.1" || !e.GetOk() {
		t.Fatalf("event = %+v, want kind=login ip=203.0.113.1 ok=true", e)
	}
	if e.GetAt() == nil {
		t.Fatal("event has no `at` timestamp")
	}
}

// --- Vacuum -------------------------------------------------------------

func TestSystemVacuumReportsSizesBeforeAndAfter(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	// Give VACUUM something to compact.
	for i := 0; i < 100; i++ {
		if _, err := s.CreateFeed(ctx, model.Feed{
			Slug: fmt.Sprintf("vacuum-rpc-fill-%d", i), Title: "Fill",
			Kind: model.KindGenerative, Timezone: "UTC", Enabled: true,
		}); err != nil {
			t.Fatalf("seed feed %d: %v", i, err)
		}
	}

	resp, err := srv.Vacuum(ctx, &affv1.SystemServiceVacuumRequest{})
	if err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	if resp.GetSizeBeforeBytes() <= 0 {
		t.Fatalf("size_before_bytes = %d, want > 0", resp.GetSizeBeforeBytes())
	}
	if resp.GetSizeAfterBytes() <= 0 {
		t.Fatalf("size_after_bytes = %d, want > 0", resp.GetSizeAfterBytes())
	}
	if resp.GetDurationMs() < 0 {
		t.Fatalf("duration_ms = %d, want >= 0", resp.GetDurationMs())
	}
}

// TestSystemVacuumRefusesWhileRunInFlight is the concurrent-write behavior
// this RPC must have (task requirement: "vacuum reports before/after sizes
// and behaves as you decided under a concurrent write"). The decision: a
// generation run holding the 'running' status refuses VACUUM outright
// rather than letting the two contend for SQLite's single writer
// connection — see Vacuum's doc comment in internal/rpc/system.go.
func TestSystemVacuumRefusesWhileRunInFlight(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	feedID, err := s.CreateFeed(ctx, model.Feed{
		Slug: "vacuum-refuse-test", Title: "Vacuum Refuse Test",
		Kind: model.KindGenerative, Timezone: "UTC", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateFeed: %v", err)
	}
	if _, err := s.StartRun(ctx, feedID, "manual", "test-holder"); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, err = srv.Vacuum(ctx, &affv1.SystemServiceVacuumRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("vacuum error = %v, want codes.FailedPrecondition while a run is in flight", err)
	}
}
