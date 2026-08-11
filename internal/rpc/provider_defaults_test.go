package rpc

import (
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// A fresh install rendered /settings/provider with "Choose a model…" under
// both model pickers while Effort read correctly, because sysLoadProvider
// seeded Effort and nothing else. An empty picker is not a neutral state: it
// tells the operator no model is configured, on a page whose whole job is
// saying which model runs.
//
// These assertions are what a fresh install shows. They are deliberately
// exact rather than "non-empty": the settings dropdown only renders options
// SystemService.ListModels returned, so a default that does not match a real
// provider model id renders as the same empty box, with a value behind it —
// a regression that a non-empty check would sail straight past.
func TestGetSettingsSeedsProviderDefaultsOnAFreshInstall(t *testing.T) {
	srv := NewSystemServer(sysTestStore(t), nil)
	ctx := t.Context()

	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	p := resp.GetSettings().GetProvider()

	if got := p.GetDefaultModel(); got != DefaultProviderModel {
		t.Errorf("default model = %q, want %q — an empty value renders as \"Choose a model…\"", got, DefaultProviderModel)
	}
	if got := p.GetEmbeddingModel(); got != DefaultProviderEmbeddingModel {
		t.Errorf("embedding model = %q, want %q", got, DefaultProviderEmbeddingModel)
	}
	if got := p.GetEffort(); got != defaultProviderEffort {
		t.Errorf("effort = %q, want %q", got, defaultProviderEffort)
	}
	// Whatever the tiers are called, the seeded one has to be one the
	// validator accepts, or a fresh install cannot save its own settings back.
	if !validProviderEfforts[defaultProviderEffort] {
		t.Errorf("the seeded effort %q is not in validProviderEfforts", defaultProviderEffort)
	}
}

// A stored choice must win over the seed. Seeding happens on read, so the
// failure mode is overwriting what the operator picked — the same shape as
// the kill-switch bug sysLoadGeneration's comment warns about, where a
// persisted value that serialises as an absent key gets silently replaced.
func TestStoredProviderModelsSurviveTheDefaults(t *testing.T) {
	srv := NewSystemServer(sysTestStore(t), nil)
	ctx := t.Context()

	_, err := srv.UpdateSettings(ctx, &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{
			DefaultModel:   "operator-picked-chat",
			EmbeddingModel: "operator-picked-embed",
			Effort:         "fast",
		}},
	})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	resp, err := srv.GetSettings(ctx, &affv1.SystemServiceGetSettingsRequest{})
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	p := resp.GetSettings().GetProvider()
	if p.GetDefaultModel() != "operator-picked-chat" {
		t.Errorf("default model = %q, want the stored choice — the seed overwrote it", p.GetDefaultModel())
	}
	if p.GetEmbeddingModel() != "operator-picked-embed" {
		t.Errorf("embedding model = %q, want the stored choice", p.GetEmbeddingModel())
	}
	if p.GetEffort() != "fast" {
		t.Errorf("effort = %q, want the stored choice", p.GetEffort())
	}
}
