// Package llm is a thin adapter over SchemaFlux (PLAN.md §8). It builds the
// typed generation call, captures whatever accounting SchemaFlux exposes, and
// maps SchemaFlux's failures onto the taxonomy in errors.go. SchemaFlux's own
// types (schemaflux.Client, schemaflux.Result, and friends) stop here and
// never reach internal/generate.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monstercameron/schemaflux"
)

// GeneratedItem is the generation contract every recipe produces (PLAN.md
// §9). internal/generate re-validates every field in Go; SchemaFlux only
// guarantees the shape typed here, never the business rules.
type GeneratedItem struct {
	Title       string   `json:"title"`
	SummaryText string   `json:"summary_text"`
	BodyHTML    string   `json:"body_html"`
	Link        string   `json:"link,omitempty"`
	SourceName  string   `json:"source_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	AnswerHTML  string   `json:"answer_html,omitempty"`
}

// Request is one generation call. System and MaxItems are folded into
// SchemaFlux's Steer() text: the fluent Generate API has no separate
// system-prompt slot and Count>1 is unimplemented upstream (batch generation
// returns an error), so the item cap has to be asked for in prose rather than
// enforced by a parameter. See the task report for both gaps.
type Request struct {
	Prompt string
	System string

	Model       string
	Temperature float64
	MaxItems    int
	RequestID   string
}

// Result is what a Provider call produced, with SchemaFlux's own result
// envelope stripped away. Raw is a best-effort JSON re-encoding of Items --
// SchemaFlux's Generate operation does not return the model's raw text, only
// the decoded value, so there is no truer "raw" to report. TokensIn,
// TokensOut, CostUSD, and FinishReason are zero: SchemaFlux v1.1.0 has no
// result twin for Generate (only Extract does), so its envelope carries no
// usage or cost for this operation. See the task report.
type Result struct {
	Items        []GeneratedItem
	Raw          string
	Model        string
	TokensIn     int
	TokensOut    int
	CostUSD      float64
	FinishReason string
}

// Provider is the surface internal/generate depends on. SchemaFlux's types
// never appear in this interface.
type Provider interface {
	Generate(ctx context.Context, req Request) (Result, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Name() string
}

// SchemaFlux owns timeouts, budgets and retries. This adapter deliberately
// adds none of its own.
//
// An earlier draft wrapped every call in its own context.WithTimeout. That is
// exactly the layering this package is supposed to avoid: two timeout budgets
// on one call means the shorter one silently wins, and which one that is
// depends on configuration nobody is looking at. The caller's context deadline
// and SchemaFlux's own limits are the whole story.

// Config configures a SchemaFluxProvider. Model and Temperature are supplied
// per Request, not here -- PLAN.md §8 requires model and parameters to vary
// per recipe, and pinning them on the client would let one feed's settings
// leak into another's run.
type Config struct {
	// APIKey is the provider credential. Required.
	APIKey string

	// ProviderName selects the SchemaFlux-registered backend. Empty defaults
	// to "openai", the only one PLAN.md §8 calls live-verified.
	ProviderName string
}

// SchemaFluxProvider implements Provider over an explicit, per-instance
// *schemaflux.Client.
//
// PLAN.md §8 warns that SchemaFlux keeps process-wide state
// (ops.defaultProvider, observer, cache policy) and requires an explicit
// per-call Client so one feed's settings cannot leak into another's run.
// Building a *schemaflux.Client does still overwrite that process-wide
// default as a side effect (every With* method on Client calls
// ops.SetDefaultProvider) -- so constructing one SchemaFluxProvider per feed
// is not, by itself, isolation. The isolation comes from client.Context(ctx):
// it snapshots this client's provider/budget/scheduler under a lock and
// returns a ctx carrying that snapshot, and every call below passes that ctx
// into the fluent builder's Run/RunResult rather than relying on whatever the
// global happens to be at call time. See the task report for why this
// distinction matters and how it was confirmed against the library source.
type SchemaFluxProvider struct {
	client *schemaflux.Client
	name   string
}

// NewSchemaFluxProvider builds a provider bound to one explicit Client.
// Construct one per feed/recipe that needs its own model or parameters.
func NewSchemaFluxProvider(cfg Config) (*SchemaFluxProvider, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, &Error{Kind: Fatal, Scope: ScopeAccount, Err: fmt.Errorf("llm: no API key configured")}
	}

	providerName := cfg.ProviderName
	if providerName == "" {
		providerName = "openai"
	}

	client := schemaflux.NewClient(cfg.APIKey)
	if providerName != "openai" {
		client = client.WithProviderConfig(providerName, schemaflux.ProviderConfig{APIKey: cfg.APIKey})
	}
	if err := client.Err(); err != nil {
		return nil, &Error{Kind: Fatal, Scope: ScopeRecipe, Err: fmt.Errorf("llm: configuring provider %q: %w", providerName, err)}
	}

	return &SchemaFluxProvider{client: client, name: providerName}, nil
}

// Name implements Provider.
func (p *SchemaFluxProvider) Name() string { return p.name }

// Generate implements Provider by building the typed SchemaFlux call PLAN.md
// §8 specifies: schemaflux.Generating[[]GeneratedItem](prompt).Strict()....
//
// Temperature is accepted on Request for interface completeness but is not
// wired to anything: SchemaFlux's public Generate API exposes no per-call
// temperature knob at all, only the Mode (Strict/Creative) and Speed
// (Smart/Fast/Quick) abstractions. See the task report.
func (p *SchemaFluxProvider) Generate(ctx context.Context, req Request) (Result, error) {
	// client.Context snapshots this client's provider into the context under a
	// lock. It is NOT optional and it is NOT the same as building a per-call
	// Client: every Client.With* method mutates process-wide state
	// (ops.SetDefaultProvider), so without this snapshot one feed's model
	// settings leak into another feed's concurrent run.
	ctx = p.client.Context(ctx)

	steer := buildSteering(req)

	builder := schemaflux.Generating[[]GeneratedItem](req.Prompt).Strict()
	if steer != "" {
		builder = builder.Steer(steer)
	}
	if req.Model != "" {
		builder = builder.Model(req.Model)
	}
	if req.RequestID != "" {
		builder = builder.RequestID(req.RequestID)
	}

	items, err := builder.Run(ctx)
	if err != nil {
		return Result{}, Classify(err)
	}

	raw, _ := json.Marshal(items)
	return Result{
		Items: items,
		Raw:   string(raw),
		Model: req.Model,
	}, nil
}

// buildSteering folds Request.System and Request.MaxItems into SchemaFlux's
// one steering slot -- see Request's doc comment for why there is no
// dedicated place for either.
func buildSteering(req Request) string {
	var parts []string
	if req.System != "" {
		parts = append(parts, req.System)
	}
	if req.MaxItems > 0 {
		parts = append(parts, fmt.Sprintf("Generate at most %d items.", req.MaxItems))
	}
	return strings.Join(parts, " ")
}

// Embed implements Provider. It always fails: SchemaFlux v1.1.0 does not
// re-export its embeddings capability. internal/llm.EmbeddingProvider,
// EmbeddingRequest, EmbeddingResponse, and RequestEmbedding all live in
// SchemaFlux's internal/llm package, which is not importable outside the
// SchemaFlux module -- there is no public entry point to reach them. §9 step
// 5's novelty check therefore cannot be built on SchemaFlux as it stands
// today; see the task report for what this blocks and the options for
// closing the gap.
func (p *SchemaFluxProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, &Error{
		Kind:  Fatal,
		Scope: ScopeRecipe,
		Err:   fmt.Errorf("llm: embeddings are not available through SchemaFlux's public API (v1.1.0)"),
	}
}
