package rpc

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

// keysServer builds a SystemServer with a secret key wired, one profile
// saved, and env supplying only the deployment default key.
func keysServer(t *testing.T) *SystemServer {
	t.Helper()
	return NewSystemServer(sysTestStore(t), nil,
		WithGetenv(sysFakeGetenv(map[string]string{sysProviderAPIKeyEnv: "env-default-key"})),
		WithSecretKey([]byte("unit-test-secret-key-not-real")),
	)
}

func saveProfile(t *testing.T, srv *SystemServer, prof *affv1.ProviderProfile, active string) *affv1.Settings_Provider {
	t.Helper()
	resp, err := srv.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{
			ActiveProfile: active,
			Profiles:      []*affv1.ProviderProfile{prof},
		}},
	})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}
	return resp.GetSettings().GetProvider()
}

// TestStoredProviderKeyRoundTrip is the core contract: a key written from
// the page is stored encrypted, reported only as booleans, never echoed,
// and resolution hands generation the decrypted plaintext — winning over
// the profile's env var.
func TestStoredProviderKeyRoundTrip(t *testing.T) {
	srv := keysServer(t)
	p := saveProfile(t, srv, &affv1.ProviderProfile{
		Name: "openai", ApiKey: "sk-super-secret",
	}, "openai")

	prof := p.GetProfiles()[0]
	if prof.GetApiKey() != "" {
		t.Fatalf("response echoed the API key %q; it must always come back empty", prof.GetApiKey())
	}
	if !prof.GetHasStoredKey() || !prof.GetKeyPresent() {
		t.Fatalf("stored key not reported: has_stored_key=%v key_present=%v",
			prof.GetHasStoredKey(), prof.GetKeyPresent())
	}

	// The persisted settings blob must not contain the plaintext.
	raw, ok, err := sysReadSetting(t.Context(), srv.st.Reader(), sysSettingsKeyProvider)
	if err != nil || !ok {
		t.Fatalf("reading persisted provider settings: ok=%v err=%v", ok, err)
	}
	if strings.Contains(raw, "sk-super-secret") {
		t.Fatal("plaintext key found in the persisted provider settings row")
	}
	rawKeys, ok, err := sysReadSetting(t.Context(), srv.st.Reader(), sysSettingsKeyProviderKeys)
	if err != nil || !ok {
		t.Fatalf("reading persisted provider keys: ok=%v err=%v", ok, err)
	}
	if strings.Contains(rawKeys, "sk-super-secret") {
		t.Fatal("plaintext key found in the stored-keys row; it must be ciphertext")
	}

	// Resolution decrypts it, and it wins over any env var.
	loaded, err := srv.sysLoadProvider(t.Context(), srv.st.Reader())
	if err != nil {
		t.Fatalf("load provider: %v", err)
	}
	endpoint := ResolveProviderEndpoint(loaded, srv.getenv)
	srv.applyStoredProviderKey(t.Context(), srv.st.Reader(), &endpoint)
	if endpoint.APIKey != "sk-super-secret" {
		t.Fatalf("resolved key = %q, want the stored plaintext", endpoint.APIKey)
	}
}

// TestStoredProviderKeySurvivesKeylessResave pins the "leave it blank to
// keep it" semantics: a later save that carries the profile with an empty
// api_key must not clear the stored one.
func TestStoredProviderKeySurvivesKeylessResave(t *testing.T) {
	srv := keysServer(t)
	saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai", ApiKey: "sk-super-secret"}, "openai")
	p := saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai"}, "openai")
	if !p.GetProfiles()[0].GetHasStoredKey() {
		t.Fatal("a keyless resave dropped the stored key")
	}
}

// TestClearStoredKey removes the key on request and reports the change.
func TestClearStoredKey(t *testing.T) {
	srv := keysServer(t)
	saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai", ApiKey: "sk-super-secret"}, "openai")
	p := saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai", ClearStoredKey: true}, "openai")
	prof := p.GetProfiles()[0]
	if prof.GetHasStoredKey() {
		t.Fatal("clear_stored_key left the key stored")
	}
	if prof.GetKeyPresent() {
		t.Fatal("key_present should be false with no stored key and no env var")
	}
}

// TestDeletedProfileDropsItsStoredKey: garbage collection — a stored key
// must not outlive its profile and be silently inherited by a future
// profile of the same name.
func TestDeletedProfileDropsItsStoredKey(t *testing.T) {
	srv := keysServer(t)
	saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai", ApiKey: "sk-super-secret"}, "openai")

	// Save with the profile removed entirely.
	if _, err := srv.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{}},
	}); err != nil {
		t.Fatalf("update settings without the profile: %v", err)
	}

	// Recreate the same name, keyless: it must arrive with no key.
	p := saveProfile(t, srv, &affv1.ProviderProfile{Name: "openai"}, "")
	if p.GetProfiles()[0].GetHasStoredKey() {
		t.Fatal("a recreated profile inherited the deleted profile's stored key")
	}
}

// TestDefaultProviderStoredKey covers the built-in provider's UI-stored key
// (the production path; the env var is only the dev fallback): stored
// encrypted under the reserved empty name, reported as booleans, never
// echoed, wins over SCHEMAFLUX_API_KEY at resolve time, and clears on
// request — falling back to the env var again.
func TestDefaultProviderStoredKey(t *testing.T) {
	srv := keysServer(t)

	resp, err := srv.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{
			DefaultApiKey: "sk-default-stored",
		}},
	})
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}
	p := resp.GetSettings().GetProvider()
	if p.GetDefaultApiKey() != "" {
		t.Fatal("response echoed the default API key")
	}
	if !p.GetDefaultKeyStored() || !p.GetApiKeyPresent() {
		t.Fatalf("stored default key not reported: stored=%v present=%v",
			p.GetDefaultKeyStored(), p.GetApiKeyPresent())
	}

	loaded, err := srv.sysLoadProvider(t.Context(), srv.st.Reader())
	if err != nil {
		t.Fatalf("load provider: %v", err)
	}
	endpoint := ResolveProviderEndpoint(loaded, srv.getenv)
	srv.applyStoredProviderKey(t.Context(), srv.st.Reader(), &endpoint)
	if endpoint.APIKey != "sk-default-stored" {
		t.Fatalf("resolved default key = %q, want the stored one to win over the env var", endpoint.APIKey)
	}

	// Clearing falls back to the env var.
	resp, err = srv.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{ClearDefaultKey: true}},
	})
	if err != nil {
		t.Fatalf("clear default key: %v", err)
	}
	p = resp.GetSettings().GetProvider()
	if p.GetDefaultKeyStored() {
		t.Fatal("clear_default_key left the key stored")
	}
	if !p.GetApiKeyPresent() {
		t.Fatal("api_key_present should still be true via the env fallback")
	}
	loaded, err = srv.sysLoadProvider(t.Context(), srv.st.Reader())
	if err != nil {
		t.Fatalf("reload provider: %v", err)
	}
	endpoint = ResolveProviderEndpoint(loaded, srv.getenv)
	srv.applyStoredProviderKey(t.Context(), srv.st.Reader(), &endpoint)
	if endpoint.APIKey != "env-default-key" {
		t.Fatalf("after clearing, resolved key = %q, want the env fallback", endpoint.APIKey)
	}
}

// TestStoreKeyWithoutSecretKeyRefusesLoudly: a server with no
// AFF_SECRET_KEY must refuse the save rather than store plaintext or
// silently drop the key.
func TestStoreKeyWithoutSecretKeyRefusesLoudly(t *testing.T) {
	srv := NewSystemServer(sysTestStore(t), nil, WithGetenv(sysFakeGetenv(nil)))
	_, err := srv.UpdateSettings(t.Context(), &affv1.SystemServiceUpdateSettingsRequest{
		Settings: &affv1.Settings{Provider: &affv1.Settings_Provider{
			Profiles: []*affv1.ProviderProfile{{Name: "openai", ApiKey: "sk-x"}},
		}},
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("got %v, want FailedPrecondition", err)
	}
}
