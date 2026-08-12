package rpc

import (
	"testing"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveProviderEndpointWithNoProfileUsesTheDeploymentDefault(t *testing.T) {
	env := envMap(map[string]string{sysProviderAPIKeyEnv: "sk-default"})

	for _, tc := range []struct {
		name string
		p    *affv1.Settings_Provider
	}{
		{"nothing configured at all", &affv1.Settings_Provider{}},
		{"nil settings", nil},
		{"profiles exist but none is active", &affv1.Settings_Provider{
			Profiles: []*affv1.ProviderProfile{{Name: "local", BaseUrl: "http://127.0.0.1:11434/v1", ApiKeyEnv: "LOCAL_KEY"}},
		}},
		{"active names a profile that does not exist", &affv1.Settings_Provider{
			ActiveProfile: "deleted",
			Profiles:      []*affv1.ProviderProfile{{Name: "local", BaseUrl: "http://127.0.0.1:11434/v1"}},
		}},
		{"active is only whitespace", &affv1.Settings_Provider{ActiveProfile: "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveProviderEndpoint(tc.p, env)
			if got.BaseURL != "" {
				t.Errorf("base URL = %q, want the library default (empty)", got.BaseURL)
			}
			if got.APIKey != "sk-default" {
				t.Errorf("api key = %q, want the deployment default", got.APIKey)
			}
			if got.Profile != "" {
				t.Errorf("profile = %q, want none", got.Profile)
			}
		})
	}
}

func TestResolveProviderEndpointUsesTheActiveProfile(t *testing.T) {
	p := &affv1.Settings_Provider{
		ActiveProfile: "local",
		Profiles: []*affv1.ProviderProfile{
			{Name: "openrouter", BaseUrl: "https://openrouter.ai/api/v1", ApiKeyEnv: "OPENROUTER_API_KEY"},
			{Name: "local", BaseUrl: "http://127.0.0.1:11434/v1/", ApiKeyEnv: "LOCAL_KEY"},
		},
	}
	got := ResolveProviderEndpoint(p, envMap(map[string]string{
		sysProviderAPIKeyEnv: "sk-default",
		"LOCAL_KEY":          "sk-local",
		"OPENROUTER_API_KEY": "sk-openrouter",
	}))

	// The trailing slash is stripped: "…/v1//chat/completions" is served by
	// some gateways and 404s on others, and the operator should not have to
	// know which kind theirs is.
	if got.BaseURL != "http://127.0.0.1:11434/v1" {
		t.Errorf("base URL = %q", got.BaseURL)
	}
	if got.APIKey != "sk-local" {
		t.Errorf("api key = %q, want the profile's own key", got.APIKey)
	}
	if got.Profile != "local" {
		t.Errorf("profile = %q, want local", got.Profile)
	}
}

// The security property this whole file exists to hold: a profile pointing at
// a third party must never be handed the deployment's own key because its own
// env var happens to be unset. That would be a credential disclosure to
// whatever host the profile names, arriving as a convenience.
func TestResolveProviderEndpointNeverLendsTheDefaultKeyToAProfile(t *testing.T) {
	p := &affv1.Settings_Provider{
		ActiveProfile: "somebody-elses-gateway",
		Profiles: []*affv1.ProviderProfile{
			{Name: "somebody-elses-gateway", BaseUrl: "https://gateway.example.com/v1", ApiKeyEnv: "NOT_SET"},
		},
	}
	got := ResolveProviderEndpoint(p, envMap(map[string]string{sysProviderAPIKeyEnv: "sk-default"}))

	if got.APIKey != "" {
		t.Fatalf("api key = %q; an unset profile env var must yield no key at all", got.APIKey)
	}
	if got.BaseURL != "https://gateway.example.com/v1" {
		t.Errorf("base URL = %q", got.BaseURL)
	}
	if got.Profile != "somebody-elses-gateway" {
		t.Errorf("profile = %q — the caller needs the name to say WHICH endpoint has no key", got.Profile)
	}
}

// A profile with no base URL is "the default endpoint, with my key", which is
// a legitimate thing to configure (two keys against api.openai.com).
func TestResolveProviderEndpointAllowsAProfileWithNoBaseURL(t *testing.T) {
	p := &affv1.Settings_Provider{
		ActiveProfile: "second-key",
		Profiles:      []*affv1.ProviderProfile{{Name: "second-key", ApiKeyEnv: "OTHER_KEY"}},
	}
	got := ResolveProviderEndpoint(p, envMap(map[string]string{"OTHER_KEY": "sk-other"}))
	if got.BaseURL != "" || got.APIKey != "sk-other" {
		t.Fatalf("base URL = %q, api key = %q", got.BaseURL, got.APIKey)
	}
}

func TestResolveProviderEndpointToleratesNoGetenv(t *testing.T) {
	got := ResolveProviderEndpoint(&affv1.Settings_Provider{}, nil)
	if got.APIKey != "" || got.BaseURL != "" {
		t.Fatalf("got %+v, want the zero endpoint", got)
	}
}
