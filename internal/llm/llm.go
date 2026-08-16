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
	"log/slog"
	"strings"

	"github.com/monstercameron/schemaflux"

	"github.com/monstercameron/AnimeFeedFlux/internal/obs"
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

// generatedBatch wraps the item slice in an object.
//
// It exists because OpenAI's structured-output contract REQUIRES the
// response_format schema to have `"type": "object"` at the root, and
// schemaflux.Generating[[]GeneratedItem] derives its schema from the type
// parameter — a slice, so an array. Every call therefore came back:
//
//	invalid_json_schema: schema must be a JSON Schema of 'type: "object"',
//	got 'type: "array"' (param text.format.schema)
//
// which means generation had NEVER worked against a real provider. Nothing
// caught it: internal/llm's FakeProvider replays canned JSON and never
// builds a schema, so the whole default test suite exercises the decode path
// and none of the contract the provider actually enforces. Found by the
// first live call (TODOS.md A4-30), which is precisely what that ticket
// exists for.
type generatedBatch struct {
	// Items is the field the model fills. Named in the schema, so the
	// steering text below and the schema agree on what is being asked for.
	Items []GeneratedItem `json:"items"`
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

	// Effort selects SchemaFlux's Speed tier: "smart" (thorough, the
	// default), "fast", or "quick". It is the only per-call effort knob
	// SchemaFlux's public API exposes — PLAN.md §8.1 records that there is
	// no temperature control at all, "only Mode and Speed tiers" — so it is
	// named for what it actually selects rather than for a scale this
	// package would then have to invent a mapping for.
	//
	// Empty means smart, which is what this package did implicitly before
	// the setting existed by setting no tier.
	Effort string
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

// ItemFormats is stage 2's typed output (§9, two-stage generation,
// 2026-08-15): the same raw item formatted once per output surface. Field
// meanings and fallbacks are documented on internal/model.ItemFormats,
// which internal/generate maps this onto; the two structs are separate so
// this package keeps its no-model-imports layering.
type ItemFormats struct {
	FeedHTML  string `json:"feed_html"`
	CardText  string `json:"card_text"`
	EmbedText string `json:"embed_text"`
	PageHTML  string `json:"page_html"`
}

// FormatRequest is one stage-2 formatting call: raw fields in the prompt,
// four surface-optimized variants out.
type FormatRequest struct {
	Prompt    string
	System    string
	Model     string
	Effort    string
	RequestID string
}

// FormatResult carries the variants plus the same best-effort Raw
// re-encoding Result documents.
type FormatResult struct {
	Formats ItemFormats
	Raw     string
	Model   string
}

// Formatter is the OPTIONAL stage-2 capability. A separate interface rather
// than a fourth Provider method so every existing Provider implementation —
// the test fakes above all, but also any future minimal provider — keeps
// compiling unchanged, and internal/generate degrades to raw-field
// rendering (a correct, complete feed, §9's stage-1 output) when the
// capability is absent, exactly as it does when a Format call fails.
type Formatter interface {
	Format(ctx context.Context, req FormatRequest) (FormatResult, error)
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

	// BaseURL points calls at an OpenAI-COMPATIBLE endpoint instead of the
	// provider's own — a local llama.cpp/Ollama shim, an Azure deployment, a
	// gateway. Empty means the library default, which is what every caller
	// did before provider profiles were wired (TODOS.md A4-42).
	//
	// SchemaFlux honours this through ProviderConfig.BaseURL, which the
	// library uses verbatim; no endpoint policy is applied by default, so
	// the URL that reaches here is the URL that gets called. It is validated
	// on the way in (internal/rpc's sysValidateProfiles), not here.
	BaseURL string

	// Logger receives the WARN/ERROR line Generate emits on failure (§15.0,
	// TODOS A0-L07). Nil defaults to slog.Default() so a caller that has not
	// wired its own logger through still gets the level distinction rather
	// than silence; see logGenerateFailure's doc comment for why the level
	// choice is the whole point of this field existing.
	Logger *slog.Logger
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
	logger *slog.Logger
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
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	// A base URL has to go through WithProviderConfig even for "openai" —
	// NewClient alone takes only a key, so without this an operator pointing
	// the app at a local shim would have kept talking to api.openai.com with
	// the setting saved and apparently in force.
	if providerName != "openai" || baseURL != "" {
		client = client.WithProviderConfig(providerName, schemaflux.ProviderConfig{
			APIKey:  cfg.APIKey,
			BaseURL: baseURL,
		})
	}
	if err := client.Err(); err != nil {
		return nil, &Error{Kind: Fatal, Scope: ScopeRecipe, Err: fmt.Errorf("llm: configuring provider %q: %w", providerName, err)}
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SchemaFluxProvider{client: client, name: providerName, logger: logger}, nil
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

	// Generating[generatedBatch], not Generating[[]GeneratedItem]: the
	// provider rejects a top-level array schema outright. See
	// generatedBatch's doc comment.
	builder := schemaflux.Generating[generatedBatch](req.Prompt).Strict()
	if steer != "" {
		builder = builder.Steer(steer)
	}
	if req.Model != "" {
		builder = builder.Model(req.Model)
	}
	// The Speed tier. Unknown values fall through to the library default
	// rather than erroring here: the RPC layer already rejects anything
	// outside the three tiers on save (internal/rpc's validProviderEfforts),
	// so a bad value reaching this point means a config path that bypassed
	// validation, and refusing to generate at all would be a worse outcome
	// than generating at the default effort.
	switch req.Effort {
	case "fast":
		builder = builder.Fast()
	case "quick":
		builder = builder.Quick()
	case "smart":
		builder = builder.Smart()
	}
	if req.RequestID != "" {
		builder = builder.RequestID(req.RequestID)
	}

	batch, err := builder.Run(ctx)
	if err != nil {
		classified := Classify(err)
		logGenerateFailure(ctx, p.logger, req.Model, classified)
		return Result{}, classified
	}
	items := batch.Items

	raw, _ := json.Marshal(items)
	return Result{
		Items: items,
		Raw:   string(raw),
		Model: req.Model,
	}, nil
}

// logGenerateFailure logs a Generate failure at the level PLAN.md §15.0
// requires (TODOS A0-L07): ERROR means a human must look, WARN means it
// self-healed and only recurrence matters.
//
// Why the level is decided by Kind alone, not by counting retries: SchemaFlux
// already retries a Transient failure internally (decorrelated-jitter
// backoff, mw.Retry) before Run ever returns to this call site, so an error
// reaching here has already survived every retry SchemaFlux was going to
// make -- there is no "recovered mid-flight" case for this function to see,
// only "still transient after retrying" or "not retryable at all". The
// distinction §15.0 actually cares about still holds at that boundary:
// Kind == Transient means the *next* attempt -- this generation run's own
// retry if internal/generate has one, or simply the feed's next scheduled
// run -- is expected to succeed without anyone doing anything, which is
// WARN's definition ("failed but self-healed, recurrence matters"). Kind ==
// Invalid or Fatal means nothing about the next attempt is expected to be
// different, which is ERROR's definition ("a human must look now").
//
// A per-attempt hook into SchemaFlux's own retry loop was investigated and
// does not exist in v1.1.0: mw.Retry (github.com/monstercameron/schemaflux/
// mw/retry.go) is unexported from the fluent Generate path with no
// caller-supplied callback, and the library's own extension point for this
// -- telemetry.Observer's OperationStarted/OperationFinished, installed via
// telemetry/otel.Install -- is never invoked by the real Generate/Extract/
// Transform call path (internal/ops/core.go logs through its own private
// logger.GetLogger() instead); grepping the vendored source for
// OperationStarted(/OperationFinished( call sites outside their own
// definition and tests returns nothing. Wiring Install would tick a box
// with no span ever opening, which is the "wired to nothing" failure mode
// this repo has shipped before -- see the task report.
//
// RULE-3 (never log model output): only Kind, a closed-set enum, ever
// reaches the log line. err.Err's message is never logged -- a schema-
// violation or malformed-output error can and does echo the model's raw
// text back (errors.go's Classify), the same reason otel.go's
// OperationFinished records "the kind, not the message" on its own span.
func logGenerateFailure(ctx context.Context, logger *slog.Logger, model string, err *Error) {
	if logger == nil || err == nil {
		return
	}
	level := slog.LevelError
	if err.Kind == Transient {
		level = slog.LevelWarn
	}
	logger.LogAttrs(ctx, level, "llm.generate_failed",
		slog.String(obs.FieldModel, model),
		slog.String(obs.FieldReason, "llm_"+err.Kind.String()),
	)
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
		// Names the "items" field explicitly: the schema now nests the
		// slice under an object (generatedBatch), and the steering text has
		// to ask for the same shape the schema describes or the model fills
		// the wrapper and leaves the array empty.
		parts = append(parts, fmt.Sprintf("Return at most %d items in the \"items\" array.", req.MaxItems))
	}
	return strings.Join(parts, " ")
}

// Format implements Formatter: the stage-2 call, same client-snapshot and
// classification discipline as Generate. ItemFormats is an object at the
// schema root, so it needs no generatedBatch-style wrapper.
func (p *SchemaFluxProvider) Format(ctx context.Context, req FormatRequest) (FormatResult, error) {
	ctx = p.client.Context(ctx)

	builder := schemaflux.Generating[ItemFormats](req.Prompt).Strict()
	if req.System != "" {
		builder = builder.Steer(req.System)
	}
	if req.Model != "" {
		builder = builder.Model(req.Model)
	}
	switch req.Effort {
	case "fast":
		builder = builder.Fast()
	case "quick":
		builder = builder.Quick()
	case "smart":
		builder = builder.Smart()
	}
	if req.RequestID != "" {
		builder = builder.RequestID(req.RequestID)
	}

	formats, err := builder.Run(ctx)
	if err != nil {
		classified := Classify(err)
		logGenerateFailure(ctx, p.logger, req.Model, classified)
		return FormatResult{}, classified
	}
	raw, _ := json.Marshal(formats)
	return FormatResult{Formats: formats, Raw: string(raw), Model: req.Model}, nil
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
