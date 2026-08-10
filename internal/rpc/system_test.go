package rpc

import (
	"bytes"
	"encoding/json"
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

func TestSystemBackupProducesAValidSQLiteFile(t *testing.T) {
	s := sysTestStore(t)
	ctx := t.Context()
	srv := NewSystemServer(s, nil)

	resp, err := srv.Backup(ctx, &affv1.SystemServiceBackupRequest{})
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
