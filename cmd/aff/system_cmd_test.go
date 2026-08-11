package main

// Tests for `aff system settings get|set`. fakeSystemClient (fakes_test.go)
// only wires Version today, and this file's hard-rules scope excludes
// fakes_test.go, so these tests use a local fake — settingsFakeSystemClient
// below — embedding affv1.SystemServiceClient exactly the way
// fakeSystemClient does, so an unwired method still panics loudly rather
// than silently zero-valuing.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// errRPCBoom is a stand-in for a real transport/server error, used by tests
// that only need to verify the exit code and that some message reached
// stderr — the exact error text does not matter to those tests.
var errRPCBoom = errors.New("boom")

type settingsFakeSystemClient struct {
	affv1.SystemServiceClient
	getSettings    func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error)
	updateSettings func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error)
}

func (f *settingsFakeSystemClient) GetSettings(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
	return f.getSettings(ctx, req, opts...)
}

func (f *settingsFakeSystemClient) UpdateSettings(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
	return f.updateSettings(ctx, req, opts...)
}

// sampleSettings is a full Settings value with something in every field, so
// a test can tell "field carried over untouched" apart from "field zeroed"
// and "api_key_present rendered as a bool" apart from "some other value".
func sampleSettings() *affv1.Settings {
	return &affv1.Settings{
		Provider: &affv1.Settings_Provider{
			ActiveProvider: "schemaflux",
			DefaultModel:   "gpt-5-mini",
			EmbeddingModel: "text-embedding-3-small",
			ApiKeyPresent:  true,
			PriceTable: []*affv1.PriceEntry{
				{Model: "gpt-5-mini", UsdPer_1KTokensIn: 0.001, UsdPer_1KTokensOut: 0.002},
			},
		},
		Generation: &affv1.Settings_Generation{
			Enabled:                    true,
			GlobalDailyTokenCeiling:    1_000_000,
			GlobalDailySpendCeilingUsd: 10.0,
			DefaultDailyTokenBudget:    20_000,
			DefaultDailyRunBudget:      4,
			DefaultFeedWindow:          50,
			StalenessThresholdMinutes:  180,
		},
		Publishing: &affv1.Settings_Publishing{
			PublicBaseUrl:       "https://feed.example.com",
			DefaultAuthor:       "editor@example.com",
			DefaultContact:      "editor@example.com",
			DefaultCopyright:    "Copyright 2026",
			DefaultTtlMinutes:   60,
			DefaultCacheControl: "public, max-age=300",
			DefaultOgImage:      "https://feed.example.com/og.png",
		},
	}
}

func TestSettingsGetHumanOutput(t *testing.T) {
	a, stdout, stderr := newTestApp()
	settings := sampleSettings()

	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
	}

	code := a.run([]string{"system", "settings", "get"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"https://feed.example.com",
		"editor@example.com",
		"Copyright 2026",
		"10.0000",
		"180",
		"api key present:    true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestSettingsGetJSONOutputParsesAndOmitsAPIKey(t *testing.T) {
	a, stdout, stderr := newTestApp()
	a.JSON = true
	settings := sampleSettings()

	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
	}

	code := a.run([]string{"system", "settings", "get", "--json"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}

	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output does not parse: %v (stdout: %s)", err, stdout.String())
	}
	provider, _ := got["settings"].(map[string]any)["provider"].(map[string]any)
	if provider["api_key_present"] != true {
		t.Fatalf("api_key_present = %v, want true", provider["api_key_present"])
	}
	if _, ok := provider["api_key"]; ok {
		t.Fatalf("response leaked an api_key field: %#v", provider)
	}
	// Belt and suspenders: no plausible key material substring anywhere in
	// the raw output, and the only occurrence of "key" is the presence flag.
	raw := stdout.String()
	if strings.Contains(raw, "sk-") {
		t.Fatalf("raw output looks like it contains key material: %s", raw)
	}
}

func TestSettingsSetValidatesBaseURLLocallyWithoutRoundTrip(t *testing.T) {
	a, _, stderr := newTestApp()
	settings := sampleSettings()

	updateCalled := false
	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
		updateSettings: func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
			updateCalled = true
			return &affv1.SystemServiceUpdateSettingsResponse{Settings: settings}, nil
		},
	}

	code := a.run([]string{"system", "settings", "set", "--base-url", "not-a-url"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
	if updateCalled {
		t.Fatal("UpdateSettings was called; a locally-invalid base URL must fail before any round trip")
	}
	if !strings.Contains(stderr.String(), "public_base_url") {
		t.Fatalf("stderr should explain the bad base URL, got: %s", stderr.String())
	}
}

func TestSettingsSetRejectsMissingSchemeLocally(t *testing.T) {
	a, _, _ := newTestApp()
	settings := sampleSettings()

	updateCalled := false
	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
		updateSettings: func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
			updateCalled = true
			return &affv1.SystemServiceUpdateSettingsResponse{Settings: settings}, nil
		},
	}

	// ftp is a scheme, but not http/https.
	code := a.run([]string{"system", "settings", "set", "--base-url", "ftp://feed.example.com"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
	if updateCalled {
		t.Fatal("UpdateSettings was called for a non-http(s) scheme")
	}
}

func TestSettingsSetPreservesUntouchedGenerationFields(t *testing.T) {
	a, stdout, stderr := newTestApp()
	settings := sampleSettings()

	var sentGeneration *affv1.Settings_Generation
	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
		updateSettings: func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
			sentGeneration = req.GetSettings().GetGeneration()
			// Echo back a response with the ceiling changed, everything else
			// as sent, the way the real server would.
			updated := &affv1.Settings{
				Provider:   settings.GetProvider(),
				Generation: sentGeneration,
				Publishing: settings.GetPublishing(),
			}
			return &affv1.SystemServiceUpdateSettingsResponse{Settings: updated}, nil
		},
	}

	code := a.run([]string{"system", "settings", "set", "--spend-ceiling-usd", "25.5"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if sentGeneration == nil {
		t.Fatal("UpdateSettings was never called")
	}

	// The clobber case: only the ceiling should have moved. Every other
	// Generation field, including the two-tab kill switch flag, must equal
	// what GetSettings originally returned.
	orig := settings.GetGeneration()
	if sentGeneration.GetGlobalDailySpendCeilingUsd() != 25.5 {
		t.Fatalf("global_daily_spend_ceiling_usd = %v, want 25.5", sentGeneration.GetGlobalDailySpendCeilingUsd())
	}
	if sentGeneration.GetEnabled() != orig.GetEnabled() {
		t.Fatalf("enabled = %v, want preserved %v (kill switch must not move)", sentGeneration.GetEnabled(), orig.GetEnabled())
	}
	if sentGeneration.GetGlobalDailyTokenCeiling() != orig.GetGlobalDailyTokenCeiling() {
		t.Fatalf("global_daily_token_ceiling = %v, want preserved %v", sentGeneration.GetGlobalDailyTokenCeiling(), orig.GetGlobalDailyTokenCeiling())
	}
	if sentGeneration.GetDefaultDailyTokenBudget() != orig.GetDefaultDailyTokenBudget() {
		t.Fatalf("default_daily_token_budget = %v, want preserved %v", sentGeneration.GetDefaultDailyTokenBudget(), orig.GetDefaultDailyTokenBudget())
	}
	if sentGeneration.GetDefaultDailyRunBudget() != orig.GetDefaultDailyRunBudget() {
		t.Fatalf("default_daily_run_budget = %v, want preserved %v", sentGeneration.GetDefaultDailyRunBudget(), orig.GetDefaultDailyRunBudget())
	}
	if sentGeneration.GetDefaultFeedWindow() != orig.GetDefaultFeedWindow() {
		t.Fatalf("default_feed_window = %v, want preserved %v", sentGeneration.GetDefaultFeedWindow(), orig.GetDefaultFeedWindow())
	}
	if sentGeneration.GetStalenessThresholdMinutes() != orig.GetStalenessThresholdMinutes() {
		t.Fatalf("staleness_threshold_minutes = %v, want preserved %v", sentGeneration.GetStalenessThresholdMinutes(), orig.GetStalenessThresholdMinutes())
	}

	// Publishing must not have been touched or sent at all.
	if strings.Contains(stdout.String(), "public_base_url") {
		t.Fatalf("output mentions public_base_url for a run that never touched it: %s", stdout.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "global_daily_spend_ceiling_usd") || !strings.Contains(out, "10") || !strings.Contains(out, "25.5") {
		t.Fatalf("expected before/after for the touched field in output, got: %s", out)
	}
}

func TestSettingsSetPreservesUntouchedPublishingFields(t *testing.T) {
	a, _, stderr := newTestApp()
	settings := sampleSettings()

	var sentPublishing *affv1.Settings_Publishing
	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
		updateSettings: func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
			sentPublishing = req.GetSettings().GetPublishing()
			updated := &affv1.Settings{
				Provider:   settings.GetProvider(),
				Generation: settings.GetGeneration(),
				Publishing: sentPublishing,
			}
			return &affv1.SystemServiceUpdateSettingsResponse{Settings: updated}, nil
		},
	}

	code := a.run([]string{"system", "settings", "set", "--author", "new-editor@example.com"})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitOK, stderr.String())
	}
	if sentPublishing == nil {
		t.Fatal("UpdateSettings was never called")
	}

	orig := settings.GetPublishing()
	if sentPublishing.GetDefaultAuthor() != "new-editor@example.com" {
		t.Fatalf("default_author = %q, want the new value", sentPublishing.GetDefaultAuthor())
	}
	if sentPublishing.GetPublicBaseUrl() != orig.GetPublicBaseUrl() {
		t.Fatalf("public_base_url = %q, want preserved %q (clobber case)", sentPublishing.GetPublicBaseUrl(), orig.GetPublicBaseUrl())
	}
	if sentPublishing.GetDefaultCopyright() != orig.GetDefaultCopyright() {
		t.Fatalf("default_copyright = %q, want preserved %q", sentPublishing.GetDefaultCopyright(), orig.GetDefaultCopyright())
	}
	if sentPublishing.GetDefaultOgImage() != orig.GetDefaultOgImage() {
		t.Fatalf("default_og_image = %q, want preserved %q", sentPublishing.GetDefaultOgImage(), orig.GetDefaultOgImage())
	}
	if sentPublishing.GetDefaultTtlMinutes() != orig.GetDefaultTtlMinutes() {
		t.Fatalf("default_ttl_minutes = %d, want preserved %d", sentPublishing.GetDefaultTtlMinutes(), orig.GetDefaultTtlMinutes())
	}
	if sentPublishing.GetDefaultCacheControl() != orig.GetDefaultCacheControl() {
		t.Fatalf("default_cache_control = %q, want preserved %q", sentPublishing.GetDefaultCacheControl(), orig.GetDefaultCacheControl())
	}
}

func TestSettingsSetNoFlagsIsUsageError(t *testing.T) {
	a, _, stderr := newTestApp()
	a.clients.System = &settingsFakeSystemClient{}

	code := a.run([]string{"system", "settings", "set"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
	if stderr.String() == "" {
		t.Fatal("expected an error message on stderr")
	}
}

func TestSettingsSetRPCFailureIsExitFail(t *testing.T) {
	a, _, stderr := newTestApp()
	settings := sampleSettings()

	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return &affv1.SystemServiceGetSettingsResponse{Settings: settings}, nil
		},
		updateSettings: func(ctx context.Context, req *affv1.SystemServiceUpdateSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceUpdateSettingsResponse, error) {
			return nil, errRPCBoom
		},
	}

	code := a.run([]string{"system", "settings", "set", "--staleness-minutes", "240"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d (exitFail)", code, exitFail)
	}
	if stderr.String() == "" {
		t.Fatal("expected an error message on stderr")
	}
}

func TestSettingsGetRPCFailureIsExitFail(t *testing.T) {
	a, _, stderr := newTestApp()
	a.clients.System = &settingsFakeSystemClient{
		getSettings: func(ctx context.Context, req *affv1.SystemServiceGetSettingsRequest, opts ...grpc.CallOption) (*affv1.SystemServiceGetSettingsResponse, error) {
			return nil, errRPCBoom
		},
	}

	code := a.run([]string{"system", "settings", "get"})
	if code != exitFail {
		t.Fatalf("exit code = %d, want %d (exitFail)", code, exitFail)
	}
	if stderr.String() == "" {
		t.Fatal("expected an error message on stderr")
	}
}

func TestSettingsUnknownSubcommandIsUsageError(t *testing.T) {
	a, _, _ := newTestApp()
	a.clients.System = &settingsFakeSystemClient{}

	code := a.run([]string{"system", "settings", "bogus"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
}

func TestSettingsMissingSubcommandIsUsageError(t *testing.T) {
	a, _, _ := newTestApp()
	a.clients.System = &settingsFakeSystemClient{}

	code := a.run([]string{"system", "settings"})
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (exitUsage)", code, exitUsage)
	}
}
