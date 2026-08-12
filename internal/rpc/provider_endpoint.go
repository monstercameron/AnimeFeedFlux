package rpc

import (
	"context"
	"strings"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// ProviderEndpoint is the resolved answer to "where does a provider call go,
// and with which key" — the one question every caller of an OpenAI-compatible
// API has to answer, and the one that /settings/provider's profile list was
// storing without anybody asking it (TODOS.md A4-42).
type ProviderEndpoint struct {
	// BaseURL is the API root, or "" for the library's own default
	// (api.openai.com). Never has a trailing slash.
	BaseURL string
	// APIKey is the credential for BaseURL, read from the environment at
	// resolve time. Empty means the caller must degrade rather than call:
	// see ResolveProviderEndpoint for why this is never filled in from the
	// default key.
	APIKey string
	// Profile is the name of the profile that produced this, or "" if the
	// deployment-wide default did. For logs and error messages, so an
	// operator can tell WHICH endpoint refused them.
	Profile string
	// Backend is the SchemaFlux provider name — "anthropic", "openrouter",
	// and so on — or "" for the library default, "openai". It selects which
	// wire protocol and default endpoint the library uses, which is a
	// different axis from BaseURL: a backend can be chosen with no base URL
	// (use its own default) and a base URL with no backend (an
	// OpenAI-compatible shim).
	Backend string
}

// ResolveProviderEndpoint picks the endpoint and credential for a call.
//
// With no active profile, or a name matching no profile, it returns the
// deployment default: the library's own base URL and the key in the
// process-wide env var. A profile that exists supplies its base URL and reads
// ITS OWN named env var.
//
// A profile whose env var is unset yields an empty APIKey rather than falling
// back to the default key. The fallback is the tempting behaviour and it is
// the wrong one: it would send the OpenAI credential to whatever third-party
// base URL the profile names, which is a key disclosure dressed up as a
// convenience. An empty key makes the caller report "no key configured",
// which is true and fixable.
//
// getenv is injected because the whole point of api_key_env is that key
// material lives in the environment and never in the database (PLAN.md §4).
func ResolveProviderEndpoint(p *affv1.Settings_Provider, getenv func(string) string) ProviderEndpoint {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	backend := strings.ToLower(strings.TrimSpace(p.GetActiveProvider()))
	active := strings.TrimSpace(p.GetActiveProfile())
	if active != "" {
		for _, prof := range p.GetProfiles() {
			if prof.GetName() != active {
				continue
			}
			return ProviderEndpoint{
				BaseURL: strings.TrimRight(strings.TrimSpace(prof.GetBaseUrl()), "/"),
				APIKey:  getenv(prof.GetApiKeyEnv()),
				Profile: prof.GetName(),
				Backend: backend,
			}
		}
	}
	return ProviderEndpoint{APIKey: getenv(sysProviderAPIKeyEnv), Backend: backend}
}

// ProviderEndpointResolver reads the stored provider settings and resolves
// them, so a caller outside this package (cmd/animefeedflux's provider
// wrapper) can ask the same question this package answers internally.
//
// It reads on every call by design. Switching endpoints in the UI has to take
// effect on the next run, not on the next restart — an operator who points
// the app at a local shim, sees nothing change, and restarts the process to
// find out has been told a lie by the settings screen.
type ProviderEndpointResolver struct {
	srv *SystemServer
	r   store.Reader
}

// NewProviderEndpointResolver builds a resolver over the same settings the
// system service reads. r is passed explicitly because the caller outside
// this package already holds the reader it wants used.
func NewProviderEndpointResolver(srv *SystemServer, r store.Reader) *ProviderEndpointResolver {
	return &ProviderEndpointResolver{srv: srv, r: r}
}

// Resolve returns the endpoint in force right now. A read failure returns the
// deployment default and the error: callers should log it and carry on
// against the default rather than fail a run, because the alternative is that
// one unreadable settings row stops all generation.
func (r *ProviderEndpointResolver) Resolve(ctx context.Context) (ProviderEndpoint, error) {
	if r == nil || r.srv == nil || r.r == nil {
		return ProviderEndpoint{}, nil
	}
	p, err := r.srv.sysLoadProvider(ctx, r.r)
	if err != nil {
		return ProviderEndpoint{APIKey: r.srv.getenv(sysProviderAPIKeyEnv)}, err
	}
	return ResolveProviderEndpoint(p, r.srv.getenv), nil
}
