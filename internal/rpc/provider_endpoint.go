package rpc

import (
	"context"
	"regexp"
	"strings"

	affv1 "github.com/monstercameron/AnimeFeedFlux/gen/aff/v1"
	"github.com/monstercameron/AnimeFeedFlux/internal/store"
)

// apiKeyEnvPattern is the allowlist a profile's api_key_env must match before
// this package will read it out of the process environment.
//
// Without it, api_key_env was an arbitrary, operator-supplied env var NAME
// paired with an arbitrary, operator-supplied base_url — which is a general
// "read any environment variable and POST it to a host of my choosing"
// primitive wearing a settings form. A profile with
// api_key_env=AFF_PASSWORD_PEPPER and a base_url the caller controls ships,
// as a Bearer token, the one secret whose entire purpose is to live nowhere
// the database does. Same for AFF_SECRET_KEY (the TOTP-at-rest key) and
// AFF_BACKUP_ENCRYPTION_KEY. Those are precisely the values designed to
// survive a database leak, so handing them over on a session compromise
// inverts the §4 threat model rather than merely extending it.
//
// An allowlist rather than a denylist of known-secret names, matching the
// discipline internal/sanitize's package doc argues for at length: a denylist
// has to be updated every time a new secret joins the environment, and the
// failure mode of forgetting is silent disclosure. This pattern instead
// describes what a third-party provider credential actually looks like —
// OPENROUTER_API_KEY, LOCAL_KEY, OR_KEY, the shapes this package's own tests
// already use — and everything else is refused whether or not anyone
// anticipated it.
//
// The `AFF_` exclusion below is the second half, and it is what does the real
// work: this application's own variables share one process environment, and
// AFF_SECRET_KEY / AFF_BACKUP_ENCRYPTION_KEY both end in _KEY and would
// otherwise sail through. Between the two halves, every secret this
// deployment actually holds is unreachable — AFF_PASSWORD_PEPPER and
// OTEL_EXPORTER_OTLP_HEADERS fail the suffix rule, the AFF_ keys fail the
// prefix rule.
//
// Residual, stated plainly rather than implied: this bounds the damage to
// "this deployment's own secrets", NOT to "one specific variable". A name
// like SOME_VENDOR_API_KEY is accepted by design, because that is exactly
// what a legitimate second provider profile looks like — so if a future
// change puts an unrelated third-party credential into this process's
// environment under a *_KEY or *_TOKEN name, it becomes readable here again.
// The rule to keep is the one deploy/animefeedflux.env.example already
// follows: this application's own secrets are AFF_-prefixed. Anything added
// there that is secret and not AFF_-prefixed needs a second look at this
// pattern.
var apiKeyEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*_(KEY|TOKEN)$`)

// allowedAPIKeyEnv reports whether name may be read as a profile credential.
// An empty name is allowed and simply resolves to an empty key — that is the
// existing "profile has no credential configured" state, not a violation.
func allowedAPIKeyEnv(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "AFF_") {
		return false
	}
	return apiKeyEnvPattern.MatchString(name)
}

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
			// A name outside the allowlist yields an EMPTY key, never the
			// deployment default one — the same fail-closed shape this
			// function's doc comment already argues for the "profile's env
			// var is unset" case, and for the same reason: an empty key makes
			// the caller report "no key configured", which is true and
			// fixable, whereas any fallback here would be a key disclosure
			// dressed up as a convenience.
			keyEnv := strings.TrimSpace(prof.GetApiKeyEnv())
			apiKey := ""
			if allowedAPIKeyEnv(keyEnv) {
				apiKey = getenv(keyEnv)
			}
			return ProviderEndpoint{
				BaseURL: strings.TrimRight(strings.TrimSpace(prof.GetBaseUrl()), "/"),
				APIKey:  apiKey,
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
	endpoint := ResolveProviderEndpoint(p, r.srv.getenv)
	r.srv.applyStoredProviderKey(ctx, r.r, &endpoint)
	return endpoint, nil
}

// applyStoredProviderKey overrides endpoint.APIKey with the STORED key for
// the resolved profile — including the built-in default, whose stored key
// lives under the reserved empty name — when one exists and decrypts.
// Stored wins over the env var: the UI-stored key is the production path
// and the env var only a dev/bootstrap fallback (operator directive
// 2026-08-15), so a key typed into /settings/provider must take effect even
// on a process whose environment still carries an older one.
func (s *SystemServer) applyStoredProviderKey(ctx context.Context, r sysRowQuerier, endpoint *ProviderEndpoint) {
	if endpoint == nil {
		return
	}
	if key, ok := s.storedProviderKey(ctx, r, endpoint.Profile); ok {
		endpoint.APIKey = key
	}
}
