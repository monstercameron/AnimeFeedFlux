package main

import (
	"context"
	"log/slog"
	"sync"

	openai "github.com/sashabaranov/go-openai"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
	"github.com/monstercameron/AnimeFeedFlux/internal/rpc"
)

// A provider profile chosen in /settings/provider used to change a stored
// setting and nothing else: the process built one llm.Provider and one
// novelty.Embedder at boot, against the library's default base URL and the
// single deployment-wide key, and never looked at the setting again
// (TODOS.md A4-42).
//
// The two wrappers here close that gap. Each resolves the active profile on
// every call and delegates to a real client built for that endpoint, so
// switching endpoints takes effect on the next run rather than the next
// restart — which matters because the operator has no way to tell the two
// apart from the settings screen, and "it didn't work" and "it didn't work
// yet" look identical.
//
// Both cache by endpoint. Rebuilding a client per call would be wasteful and,
// for SchemaFlux, actively wrong: every With* method on its Client mutates
// process-wide default state, so churning clients churns that too.

// endpointKey identifies a built client. The key is included because a
// profile can point two endpoints at the same URL with different credentials,
// and because rotating a key must produce a new client rather than reuse one
// holding the old one.
type endpointKey struct {
	baseURL string
	apiKey  string
	backend string
}

// resolvingProvider is an llm.Provider that picks its endpoint per call.
type resolvingProvider struct {
	resolver *rpc.ProviderEndpointResolver
	fallback llm.Provider
	log      *slog.Logger

	mu    sync.Mutex
	cache map[endpointKey]llm.Provider
}

// newResolvingProvider wraps fallback — the provider built from the
// deployment-wide key, used whenever no profile is active or the settings
// read fails.
func newResolvingProvider(resolver *rpc.ProviderEndpointResolver, fallback llm.Provider, log *slog.Logger) *resolvingProvider {
	if log == nil {
		log = slog.Default()
	}
	return &resolvingProvider{
		resolver: resolver,
		fallback: fallback,
		log:      log,
		cache:    map[endpointKey]llm.Provider{},
	}
}

// current returns the provider for the endpoint in force right now.
//
// Every failure path returns the fallback rather than an error. A profile
// that cannot be resolved must not stop generation: the deployment-wide key
// and endpoint are the configuration that was working before anyone touched
// the profile list, and degrading to it is recoverable in a way that
// refusing every run is not.
func (p *resolvingProvider) current(ctx context.Context) llm.Provider {
	if p.resolver == nil {
		return p.fallback
	}
	endpoint, err := p.resolver.Resolve(ctx)
	if err != nil {
		p.log.WarnContext(ctx, "reading the active provider profile; using the default endpoint",
			slog.Any("error", err))
		return p.fallback
	}
	if endpoint.APIKey == "" {
		// A profile whose key env var is unset. The resolver deliberately
		// does NOT substitute the default key for a custom base URL (that
		// would disclose it), so an incomplete profile lands here.
		if endpoint.Profile != "" {
			p.log.WarnContext(ctx, "the active provider profile names an environment variable that is not set; using the default endpoint",
				slog.String("profile", endpoint.Profile))
		}
		return p.fallback
	}
	if endpoint.BaseURL == "" && (endpoint.Backend == "" || endpoint.Backend == "openai") {
		// Nothing to override: this IS the boot-time configuration.
		return p.fallback
	}

	key := endpointKey{baseURL: endpoint.BaseURL, apiKey: endpoint.APIKey, backend: endpoint.Backend}
	p.mu.Lock()
	defer p.mu.Unlock()
	if built, ok := p.cache[key]; ok {
		return built
	}
	built, err := llm.NewSchemaFluxProvider(llm.Config{
		APIKey:       endpoint.APIKey,
		BaseURL:      endpoint.BaseURL,
		ProviderName: endpoint.Backend,
		Logger:       p.log,
	})
	if err != nil {
		p.log.WarnContext(ctx, "building a provider for the active profile; using the default endpoint",
			slog.String("profile", endpoint.Profile), slog.Any("error", err))
		return p.fallback
	}
	p.cache[key] = built
	return built
}

// Generate implements llm.Provider.
func (p *resolvingProvider) Generate(ctx context.Context, req llm.Request) (llm.Result, error) {
	return p.current(ctx).Generate(ctx, req)
}

// Embed implements llm.Provider. SchemaFlux exposes no public embedding API,
// so this delegates to whatever the underlying provider says about that —
// today, a Fatal error. Embeddings go through novelty.Embedder instead.
func (p *resolvingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return p.current(ctx).Embed(ctx, texts)
}

// Name implements llm.Provider. It reports the backend a call would reach
// right now, because the name is recorded against runs and "openai" on a run
// that went to Anthropic is a lie in the audit trail. Resolution failures
// report the fallback's name, which is what the call would actually use.
func (p *resolvingProvider) Name() string {
	if p.resolver == nil {
		return p.fallback.Name()
	}
	endpoint, err := p.resolver.Resolve(context.Background())
	if err != nil || endpoint.Backend == "" {
		return p.fallback.Name()
	}
	return endpoint.Backend
}

// resolvingEmbedder is the same idea for novelty.Embedder.
//
// Model and Dim deliberately do NOT vary with the endpoint: they describe the
// vectors in the store, and the store holds vectors produced by one model.
// Two models' vectors are not comparable — cosine similarity between them is
// a number with no meaning — so a profile switch changes WHERE the embedding
// call goes, never what it claims to have produced.
type resolvingEmbedder struct {
	resolver *rpc.ProviderEndpointResolver
	fallback novelty.Embedder
	model    openai.EmbeddingModel
	dim      int
	log      *slog.Logger

	mu    sync.Mutex
	cache map[endpointKey]novelty.Embedder
}

func newResolvingEmbedder(resolver *rpc.ProviderEndpointResolver, fallback novelty.Embedder, model openai.EmbeddingModel, dim int, log *slog.Logger) *resolvingEmbedder {
	if log == nil {
		log = slog.Default()
	}
	return &resolvingEmbedder{
		resolver: resolver,
		fallback: fallback,
		model:    model,
		dim:      dim,
		log:      log,
		cache:    map[endpointKey]novelty.Embedder{},
	}
}

func (e *resolvingEmbedder) current(ctx context.Context) novelty.Embedder {
	if e.resolver == nil {
		return e.fallback
	}
	endpoint, err := e.resolver.Resolve(ctx)
	if err != nil {
		e.log.WarnContext(ctx, "reading the active provider profile for embeddings; using the default endpoint",
			slog.Any("error", err))
		return e.fallback
	}
	if endpoint.BaseURL == "" || endpoint.APIKey == "" {
		return e.fallback
	}
	key := endpointKey{baseURL: endpoint.BaseURL, apiKey: endpoint.APIKey}
	e.mu.Lock()
	defer e.mu.Unlock()
	if built, ok := e.cache[key]; ok {
		return built
	}
	built := novelty.NewOpenAIEmbedder(endpoint.APIKey, endpoint.BaseURL, e.model, e.dim)
	e.cache[key] = built
	return built
}

// Embed implements novelty.Embedder.
func (e *resolvingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.current(ctx).Embed(ctx, texts)
}

// Model implements novelty.Embedder.
func (e *resolvingEmbedder) Model() string { return string(e.model) }

// Dim implements novelty.Embedder.
func (e *resolvingEmbedder) Dim() int { return e.dim }
