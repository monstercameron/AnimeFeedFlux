// runner.go executes the eight-step generation pipeline PLAN.md §9 specifies
// (cited here as §9.1 … §9.8), plus the sampler that shares the same steps
// through link integrity (§9.6) but never persists (§11, §12.3).
//
// This file depends on Store, Novelty, and Fetcher only as INTERFACES
// declared right here — not on internal/store, internal/sources' fetch
// plumbing beyond the Candidate shape it already returns, or any concrete
// novelty implementation. internal/store is being built concurrently by
// another agent; the whole point of depending on an interface rather than a
// package is that this file compiles and is fully testable without it ever
// existing.
package generate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monstercameron/AnimeFeedFlux/internal/ids"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/novelty"
	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
)

// --- Interfaces this package depends on (not internal/store) ---

// Store is the persistence surface Run needs. Its production implementation
// lives in internal/store; this package never imports that package, so a
// store bug cannot break generation tests and a generation bug cannot touch
// a real database in a test.
type Store interface {
	// RecentTitles returns up to n of the feed's most recent item titles,
	// newest first — the exclusion list for a generative feed's prompt
	// (PLAN.md §9 step 1, §7 {{.RecentTitles}}).
	RecentTitles(ctx context.Context, feedID int64, n int) ([]string, error)
	// NewestPublished returns the feed's current newest published_at, or the
	// zero Time if the feed has no items yet. Run's stamped timestamps must
	// never be earlier than this (§5.5, §9 step 7).
	NewestPublished(ctx context.Context, feedID int64) (time.Time, error)
	// CommitRun persists items and the closed run row in ONE call. §9's
	// non-negotiable: a crash between "items committed" and "run closed"
	// leaves live items beside a run the watchdog marks interrupted, and the
	// history then lies about what happened. Run always calls this exactly
	// once per invocation, for every outcome (completed, skipped, failed) —
	// runs is the operational spine (§10) and a failed attempt is still an
	// attempt worth a row. Sample calls this ZERO times, ever; see Sample's
	// doc comment.
	CommitRun(ctx context.Context, run RunRecord, items []model.Item) error
}

// Novelty is the generative-feed dedup gate (PLAN.md §9 step 5). Its
// production implementation embeds title+summary and compares against the
// last 500 embeddings (§8.1); this package only needs the verdict.
type Novelty interface {
	Check(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, err error)
}

// VectorNovelty is an OPTIONAL extension of Novelty: a Check implementation
// that already embeds the candidate internally (the production adapter does,
// via novelty.Gate) can also hand that vector back, so Run can persist it
// through CommitRun instead of embedding the same text a second time just to
// get something to write. This is a separate interface, not a change to
// Novelty's own signature, because generate.Novelty is implemented outside
// this package too (internal/rpc's Sample path, and every test double in
// internal/flowtest/internal/e2e) and none of those need or want to carry a
// vector — Sample never persists one, and widening the required signature
// would force every one of those unrelated implementers to grow a field they
// have no use for. Run type-asserts deps.Novelty against this interface
// (filterNovel) and falls back to plain Check when it is absent.
type VectorNovelty interface {
	Novelty
	CheckVector(ctx context.Context, feedID int64, text string) (dup bool, nearest string, score float64, vec novelty.Vector, err error)
}

// ItemEmbedding pairs a stamped item's key with the novelty vector computed
// for it during THIS run, so RunRecord can carry it to Store.CommitRun
// without Store (an interface declared in this file, not internal/store)
// needing to know anything about how embeddings are persisted. Mirrors
// internal/store's own ItemEmbedding (embeddings.go) field-for-field —
// deliberately a separate type rather than an import of internal/store's,
// because this package must not depend on internal/store (this file's own
// header).
type ItemEmbedding struct {
	ItemKey string
	Vector  novelty.Vector
}

// Fetcher supplies the normalized candidate set for a grounded feed (PLAN.md
// §9 step 1). Candidate URLs are expected already normalized — the same
// contract internal/sources.Fetcher.FetchCandidates keeps — because §9.6
// depends on both sides of the link-equality check going through
// normalization exactly once, not twice and not zero times.
type Fetcher interface {
	Candidates(ctx context.Context, feedID int64) ([]sources.Candidate, error)
}

// --- Inputs ---

// Deps bundles everything Run and Sample need beyond the feed and spec
// themselves. Now is injectable so tests get deterministic timestamps
// without wall-clock races; a nil Now falls back to time.Now.
type Deps struct {
	Store    Store
	Novelty  Novelty
	Fetcher  Fetcher
	Provider llm.Provider
	IDs      ids.Source
	Now      func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Spec is the per-run slice of a recipe (PLAN.md §7) that the generation
// pipeline actually consumes. It intentionally does not carry the whole
// recipe (schedule, budgets enforcement, TOML export) — those belong to the
// caller, not to the pipeline.
type Spec struct {
	SystemPrompt       string
	UserPromptTemplate string

	Model       string
	Temperature float64 // accepted for forward-compat; llm.Request notes SchemaFlux has no per-call knob for it yet (PLAN.md §8.1)

	ItemsPerRun int

	// RecentTitlesN bounds the generative exclusion list (§9 step 1). <= 0
	// uses defaultRecentTitles.
	RecentTitlesN int
	// MaxCandidates bounds the grounded candidate set (§9 step 1: "cap at
	// ~40 candidates"). <= 0 uses defaultMaxCandidates.
	MaxCandidates int

	// MaxNoveltyRetries is N in "reject and retry up to N times, then skip
	// the run" (§9 step 5). Generative feeds only.
	MaxNoveltyRetries int

	// Price*PerMToken price the cost ESTIMATE (§8.1: SchemaFlux's
	// Generating[T] reports zero usage/cost today, so tokens are counted by
	// this package's own heuristic and multiplied by these prices). Every
	// field this produces is labeled "Est"/"estimate" so nothing downstream
	// mistakes it for a measured number.
	PriceInputPerMToken  float64
	PriceOutputPerMToken float64

	// Trigger records who caused this run ("cron" | "manual"), per §10's
	// runs.trigger column. Sample does not use it.
	Trigger string
}

const (
	defaultRecentTitles  = 50
	defaultMaxCandidates = 40
)

// --- Outputs ---

const (
	StatusCompleted = "completed"
	StatusSkipped   = "skipped"
	StatusFailed    = "failed"
)

// Extra reject reason tokens beyond the ones contract.go already defines —
// same discipline: short, stable, grouped and counted (PLAN.md §10
// runs.reject_reasons_json), never a full sentence.
const (
	ReasonNoveltyDuplicate   = "novelty_duplicate"
	ReasonNoveltyCheckFailed = "novelty_check_failed"
)

// ErrMalformedOutput is returned by Run/Sample when a repair attempt (§9.3)
// still produced zero valid items. The run/sample outcome around it (failed
// vs partially reported) is decided by the caller of runAttempt, not by this
// sentinel alone.
var ErrMalformedOutput = errors.New("generate: model output failed validation twice (one repair attempt used)")

// RunRecord is the closed run row Store.CommitRun persists alongside items
// (PLAN.md §10 runs table). EstCostUSD is explicitly an ESTIMATE — see
// Spec's price fields.
type RunRecord struct {
	FeedID     int64
	StartedAt  time.Time
	FinishedAt time.Time

	Status    string // StatusCompleted | StatusSkipped | StatusFailed
	ErrorKind string
	Error     string

	ItemsAdded    int
	ItemsRejected int
	RejectReasons map[string]int // reason token -> count (§10 reject_reasons_json)

	// Embeddings is the novelty vector computed for each item this run
	// published, keyed by the item's own ItemKey (stampItems assigns that
	// key; the vector is captured earlier, during filterNovel's novelty
	// check, on the SAME embed call — never a second one). Empty whenever
	// there is nothing to publish (skipped/failed runs) or the configured
	// Novelty does not implement VectorNovelty (grounded feeds have no
	// novelty gate at all).
	Embeddings []ItemEmbedding

	TokensIn  int // estimated, see EstCostUSD
	TokensOut int // estimated, see EstCostUSD

	// EstCostUSD is an ESTIMATE (PLAN.md §8.1). Never presented as measured.
	EstCostUSD float64

	Trigger string
}

// RunResult is what Run returns: the closed run row and, on success, the
// items it just persisted (empty on skip/fail).
type RunResult struct {
	Run   RunRecord
	Items []model.Item
}

// SampleResult is what Sample returns: candidates it WOULD publish, plus
// telemetry — but nothing was written anywhere (see Sample's doc comment).
type SampleResult struct {
	Items         []model.Item
	RejectReasons map[string]int
	TokensIn      int
	TokensOut     int
	// EstCostUSD is an ESTIMATE (PLAN.md §8.1), same caveat as RunRecord's.
	EstCostUSD float64
}

// --- Run: the scheduled/manual generation pipeline (§9.1-§9.8) ---

// Run executes PLAN.md §9's eight steps in order and returns the closed run
// row Store.CommitRun persisted. It ALWAYS calls Store.CommitRun exactly
// once — for a context-acquisition failure, a malformed-output failure, a
// novelty-exhausted skip, or a successful publish alike — because runs is the
// operational spine (§10, §12.4) and every attempt gets a row, not only the
// ones that worked. A skipped run is not a failed run: it is reported via
// RunResult with a nil error, distinguishable only by RunRecord.Status.
func Run(ctx context.Context, deps Deps, feed model.Feed, spec Spec) (RunResult, error) {
	if feed.Kind == model.KindAggregate {
		return RunResult{}, fmt.Errorf("generate: aggregate feeds have no generator to run (PLAN.md §14.2)")
	}

	started := deps.now()
	rejectTotals := map[string]int{}
	var tokensIn, tokensOut int

	// commit is the single exit point: every return from Run goes through
	// here so Store.CommitRun is called exactly once regardless of which
	// step failed, satisfying §9's "items and the closed run row commit in
	// one call" for every outcome, not only success.
	commit := func(status, errorKind string, runErr error, items []model.Item, embeddings []ItemEmbedding) (RunResult, error) {
		run := RunRecord{
			FeedID:        feed.ID,
			StartedAt:     started,
			FinishedAt:    deps.now(),
			Status:        status,
			ErrorKind:     errorKind,
			ItemsAdded:    len(items),
			ItemsRejected: sumReasons(rejectTotals),
			RejectReasons: rejectTotals,
			Embeddings:    embeddings,
			TokensIn:      tokensIn,
			TokensOut:     tokensOut,
			EstCostUSD:    estimateCostUSD(tokensIn, tokensOut, spec),
			Trigger:       spec.Trigger,
		}
		if runErr != nil {
			run.Error = runErr.Error()
		}
		if cerr := deps.Store.CommitRun(ctx, run, items); cerr != nil {
			if runErr != nil {
				return RunResult{Run: run}, fmt.Errorf("generate: run failed (%v) and committing it also failed: %w", runErr, cerr)
			}
			return RunResult{Run: run}, fmt.Errorf("generate: committing run: %w", cerr)
		}
		return RunResult{Run: run, Items: items}, runErr
	}

	data, opts, err := acquireContext(ctx, deps, feed, spec, started)
	if err != nil {
		return commit(StatusFailed, "context_acquire_failed", err, nil, nil)
	}

	promptText, err := Render(spec.UserPromptTemplate, data)
	if err != nil {
		return commit(StatusFailed, "prompt_render_failed", err, nil, nil)
	}

	requestID := fmt.Sprintf("%s-run-%d", feed.Slug, started.UnixNano())

	// Only generative feeds retry: §9 step 5's "reject and retry up to N"
	// applies to novelty, which only exists for generative. Grounded feeds
	// get exactly one attempt (link-integrity rejects individual items, it
	// does not warrant regenerating the whole batch).
	maxAttempts := 1
	if feed.Kind == model.KindGenerative && spec.MaxNoveltyRetries > 0 {
		maxAttempts = 1 + spec.MaxNoveltyRetries
	}

	var novelItems []model.Item
	var novelVecs []novelty.Vector
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ar, aerr := runAttempt(ctx, deps, spec, opts, spec.SystemPrompt, promptText, requestID)
		tokensIn += ar.tokensIn
		tokensOut += ar.tokensOut
		for k, v := range ar.rejectedReasons {
			rejectTotals[k] += v
		}
		if aerr != nil {
			if errors.Is(aerr, ErrMalformedOutput) {
				return commit(StatusFailed, "malformed_output", aerr, nil, nil)
			}
			return commit(StatusFailed, errorKindFromProvider(aerr), aerr, nil, nil)
		}

		batch := ar.valid
		var batchVecs []novelty.Vector
		if feed.Kind == model.KindGenerative {
			batch, batchVecs = filterNovel(ctx, deps, feed.ID, batch, rejectTotals)
		}
		novelItems = append(novelItems, batch...)
		novelVecs = append(novelVecs, batchVecs...)

		if len(novelItems) > 0 || feed.Kind != model.KindGenerative {
			break
		}
		// Generative and still empty: loop again, a fresh model call, up to
		// maxAttempts total (§9 step 5).
	}

	if len(novelItems) == 0 {
		errKind := "no_valid_items"
		if feed.Kind == model.KindGenerative {
			errKind = "novelty_exhausted"
		}
		// Not an error: §9 step 5 is explicit that a skipped run is not a
		// failed one.
		return commit(StatusSkipped, errKind, nil, nil, nil)
	}

	if spec.ItemsPerRun > 0 && len(novelItems) > spec.ItemsPerRun {
		novelItems = novelItems[:spec.ItemsPerRun]
		if len(novelVecs) > spec.ItemsPerRun {
			novelVecs = novelVecs[:spec.ItemsPerRun]
		}
	}

	newest, err := deps.Store.NewestPublished(ctx, feed.ID)
	if err != nil {
		return commit(StatusFailed, "store_error", err, nil, nil)
	}

	items := stampItems(deps, feed, novelItems, newest, deps.now())

	return commit(StatusCompleted, "", nil, items, zipEmbeddings(items, novelVecs))
}

// --- Sample: the dry run (§11 "Sample", §12.3) ---

// Sample runs the identical steps 1-6 pipeline — acquire context, call the
// model, validate with one repair attempt, sanitize/absolutize (inside
// Validate), novelty check, link integrity (inside Validate for grounded) —
// then returns the candidates it WOULD publish, without persisting.
//
// This is deliberately the one function in the package that must never call
// Store.CommitRun, under any outcome, including its own failure paths. A
// sampler that writes is indistinguishable from a feature working until the
// day someone notices duplicate items nobody promoted — the worst bug this
// design can produce, per the task brief. There is no code path below that
// references deps.Store.CommitRun; that is the guarantee, not a promise kept
// by discipline.
func Sample(ctx context.Context, deps Deps, feed model.Feed, spec Spec) (SampleResult, error) {
	if feed.Kind == model.KindAggregate {
		return SampleResult{}, fmt.Errorf("generate: aggregate feeds have no generator to sample (PLAN.md §14.2)")
	}

	now := deps.now()
	data, opts, err := acquireContext(ctx, deps, feed, spec, now)
	if err != nil {
		return SampleResult{}, err
	}

	promptText, err := Render(spec.UserPromptTemplate, data)
	if err != nil {
		return SampleResult{}, err
	}

	requestID := fmt.Sprintf("%s-sample-%d", feed.Slug, now.UnixNano())

	rejectTotals := map[string]int{}
	ar, aerr := runAttempt(ctx, deps, spec, opts, spec.SystemPrompt, promptText, requestID)
	for k, v := range ar.rejectedReasons {
		rejectTotals[k] += v
	}
	cost := estimateCostUSD(ar.tokensIn, ar.tokensOut, spec)
	if aerr != nil {
		return SampleResult{
			RejectReasons: rejectTotals,
			TokensIn:      ar.tokensIn,
			TokensOut:     ar.tokensOut,
			EstCostUSD:    cost,
		}, aerr
	}

	items := ar.valid
	if feed.Kind == model.KindGenerative {
		// Sample never persists (this function's own doc comment), so the
		// vector filterNovel may have captured is discarded here — there is
		// nothing for it to be written against.
		items, _ = filterNovel(ctx, deps, feed.ID, items, rejectTotals)
	}
	if spec.ItemsPerRun > 0 && len(items) > spec.ItemsPerRun {
		items = items[:spec.ItemsPerRun]
	}

	return SampleResult{
		Items:         items,
		RejectReasons: rejectTotals,
		TokensIn:      ar.tokensIn,
		TokensOut:     ar.tokensOut,
		EstCostUSD:    cost,
	}, nil
}

// --- Shared pipeline steps ---

// acquireContext implements §9 step 1: generative feeds get the recent-title
// exclusion list, grounded feeds get the fetched-and-capped candidate set.
// Both the prompt.Data (what the model sees) and the Options.CandidateURLs
// (what CheckLink compares against) are built here, from the SAME fetch, so
// they can never disagree about what a "candidate" was for this run.
func acquireContext(ctx context.Context, deps Deps, feed model.Feed, spec Spec, now time.Time) (Data, Options, error) {
	loc := time.UTC
	if tz := strings.TrimSpace(feed.Timezone); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	local := now.In(loc)

	data := Data{
		Today:       local.Format("2006-01-02"),
		Weekday:     local.Weekday().String(),
		Season:      Season(local),
		FeedTitle:   feed.Title,
		ItemsPerRun: spec.ItemsPerRun,
	}
	opts := Options{Kind: feed.Kind}

	switch feed.Kind {
	case model.KindGenerative:
		n := spec.RecentTitlesN
		if n <= 0 {
			n = defaultRecentTitles
		}
		titles, err := deps.Store.RecentTitles(ctx, feed.ID, n)
		if err != nil {
			return Data{}, Options{}, fmt.Errorf("generate: acquiring recent titles: %w", err)
		}
		data.RecentTitles = titles

	case model.KindGrounded:
		cands, err := deps.Fetcher.Candidates(ctx, feed.ID)
		if err != nil {
			return Data{}, Options{}, fmt.Errorf("generate: fetching candidates: %w", err)
		}
		max := spec.MaxCandidates
		if max <= 0 {
			max = defaultMaxCandidates
		}
		if len(cands) > max {
			cands = cands[:max]
		}
		articles := make([]SourceArticle, 0, len(cands))
		urls := make([]string, 0, len(cands))
		for _, c := range cands {
			articles = append(articles, SourceArticle{
				Title:     c.Title,
				URL:       c.URL,
				Published: c.Published.Format("2006-01-02"),
				Excerpt:   c.Excerpt,
			})
			urls = append(urls, c.URL)
		}
		data.Candidates = articles
		opts.CandidateURLs = urls

	default:
		return Data{}, Options{}, fmt.Errorf("generate: unsupported feed kind %q for generation", feed.Kind)
	}

	return data, opts, nil
}

// attemptResult is what one call to runAttempt produced: the items that
// passed Validate (possibly after the one repair attempt), every rejection
// reason seen along the way, and the token estimate for whatever calls were
// made.
type attemptResult struct {
	valid           []model.Item
	rejectedReasons map[string]int
	tokensIn        int
	tokensOut       int
}

// runAttempt implements §9 steps 2-4 and 6 for a single generation attempt:
// call the model, validate every returned candidate (sanitizing,
// absolutizing, and — for grounded — checking link integrity, all inside
// Validate itself), and if NOTHING in the batch validated, make exactly one
// repair call with the validation errors fed back into the prompt (§9.3)
// before giving up. Items that validate on the first pass are never subject
// to repair — repair only fires when the whole attempt produced zero usable
// items, which is what "malformed output" means here.
func runAttempt(ctx context.Context, deps Deps, spec Spec, opts Options, system, basePrompt, requestID string) (attemptResult, error) {
	reasons := map[string]int{}
	systemTokens := estimateTokens(system)

	call := func(promptText string) ([]model.Item, int, int, error) {
		req := llm.Request{
			Prompt:    promptText,
			System:    system,
			Model:     spec.Model,
			MaxItems:  spec.ItemsPerRun,
			RequestID: requestID,
		}
		res, err := deps.Provider.Generate(ctx, req)
		tokensIn := systemTokens + estimateTokens(promptText)
		if err != nil {
			return nil, tokensIn, 0, err
		}
		tokensOut := estimateTokens(res.Raw)

		var valid []model.Item
		for _, gi := range res.Items {
			item, rej, verr := Validate(toCandidate(gi), opts)
			if verr != nil {
				// Only an unrecognized Kind reaches here (contract.go), a
				// caller/programmer mistake, not an everyday rejection.
				return nil, tokensIn, tokensOut, verr
			}
			if len(rej) > 0 {
				for _, r := range rej {
					reasons[r.Reason]++
				}
				continue
			}
			valid = append(valid, item)
		}
		return valid, tokensIn, tokensOut, nil
	}

	valid, tokensIn, tokensOut, err := call(basePrompt)
	if err != nil {
		return attemptResult{rejectedReasons: reasons, tokensIn: tokensIn, tokensOut: tokensOut}, err
	}
	if len(valid) > 0 {
		return attemptResult{valid: valid, rejectedReasons: reasons, tokensIn: tokensIn, tokensOut: tokensOut}, nil
	}

	// Malformed: zero usable items out of the whole batch. §9.3's one
	// repair attempt, with the validation error fed back.
	repairPrompt := basePrompt + "\n\n" + repairNote(reasons)
	valid2, tokensIn2, tokensOut2, err2 := call(repairPrompt)
	totalTokensIn := tokensIn + tokensIn2
	totalTokensOut := tokensOut + tokensOut2
	if err2 != nil {
		return attemptResult{rejectedReasons: reasons, tokensIn: totalTokensIn, tokensOut: totalTokensOut}, err2
	}
	if len(valid2) == 0 {
		return attemptResult{rejectedReasons: reasons, tokensIn: totalTokensIn, tokensOut: totalTokensOut}, ErrMalformedOutput
	}
	return attemptResult{valid: valid2, rejectedReasons: reasons, tokensIn: totalTokensIn, tokensOut: totalTokensOut}, nil
}

// repairNote builds the feedback text for the one repair attempt §9.3
// allows. Reasons are sorted so the message (and therefore prompt_hash, §7)
// is deterministic for the same failure set rather than depending on map
// iteration order.
func repairNote(reasons map[string]int) string {
	if len(reasons) == 0 {
		return "Your previous response was empty or unusable. Respond again, following the schema exactly."
	}
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "Your previous response was invalid: " + strings.Join(keys, ", ") +
		". Fix these issues and respond again, following the schema exactly."
}

// filterNovel implements §9 step 5 for one batch: check each item's
// title+summary against the novelty gate, dropping duplicates and counting
// why. A Check error is treated as a rejection rather than silently letting
// an unchecked item through as "novel" — an item that skipped the dedup
// check because the check itself broke is exactly the failure mode §9 step 5
// exists to prevent.
//
// The second return value is the embedding vector captured for each SURVIVING
// item, in the same order, ONLY when deps.Novelty implements VectorNovelty
// (the production adapter does). It is nil whenever there is no gate, the
// gate is the plain Novelty shape (tests, Sample callers that don't care),
// or every candidate in items was rejected — callers must not assume its
// length equals len(items) coming in, only that it lines up with what this
// function returns as the first value.
func filterNovel(ctx context.Context, deps Deps, feedID int64, items []model.Item, rejectTotals map[string]int) ([]model.Item, []novelty.Vector) {
	if deps.Novelty == nil {
		return items, nil
	}
	vn, hasVectors := deps.Novelty.(VectorNovelty)

	out := make([]model.Item, 0, len(items))
	var vecs []novelty.Vector
	if hasVectors {
		vecs = make([]novelty.Vector, 0, len(items))
	}
	for _, it := range items {
		text := it.Title + " " + it.SummaryText

		var (
			dup bool
			vec novelty.Vector
			err error
		)
		if hasVectors {
			dup, _, _, vec, err = vn.CheckVector(ctx, feedID, text)
		} else {
			dup, _, _, err = deps.Novelty.Check(ctx, feedID, text)
		}
		if err != nil {
			rejectTotals[ReasonNoveltyCheckFailed]++
			continue
		}
		if dup {
			rejectTotals[ReasonNoveltyDuplicate]++
			continue
		}
		out = append(out, it)
		if hasVectors {
			vecs = append(vecs, vec)
		}
	}
	return out, vecs
}

// zipEmbeddings pairs stamped items with the novelty vectors filterNovel
// captured for them, by position — valid because stampItems (called just
// before this) preserves order and never drops or reorders entries. A length
// mismatch means vecs came from a Novelty that does not implement
// VectorNovelty (or there was no gate at all, e.g. a grounded feed); in that
// case there is nothing to zip, so this returns nil rather than guessing —
// CommitRun's embeddings argument is optional for exactly this reason.
func zipEmbeddings(items []model.Item, vecs []novelty.Vector) []ItemEmbedding {
	if len(vecs) == 0 || len(vecs) != len(items) {
		return nil
	}
	out := make([]ItemEmbedding, len(items))
	for i, it := range items {
		out[i] = ItemEmbedding{ItemKey: it.ItemKey, Vector: vecs[i]}
	}
	return out
}

// stampItems implements §9 step 7's persistence prep: an opaque ULID
// item_key, a content_hash for idempotency only (never identity, §5.1), and
// a distinct, strictly increasing published_at that starts after the feed's
// current newest — the Slack rule (§5.5) enforced here because this package
// is the only thing that knows both "how many items in this run" and "what
// order the model ranked them in".
func stampItems(deps Deps, feed model.Feed, valid []model.Item, newest, now time.Time) []model.Item {
	base := now
	if !newest.IsZero() && !base.After(newest) {
		// now is not already past the feed's newest item (a slow run, a
		// backdated clock in tests, or two runs in the same second) — the
		// only way to guarantee "never earlier than newest" is to anchor
		// strictly after it rather than trusting now().
		base = newest.Add(time.Second)
	}

	out := make([]model.Item, len(valid))
	for i, it := range valid {
		ts := base.Add(time.Duration(i) * time.Second)
		it.FeedID = feed.ID
		it.PublishedAt = ts
		it.ItemKey = deps.IDs.NewItemKey(ts)
		it.ContentHash = contentHash(feed.Slug, it.Title, ts)
		it.Origin = model.OriginGenerated
		it.CreatedAt = now
		it.UpdatedAt = now
		out[i] = it
	}
	return out
}

// contentHash implements §9 step 7's `sha256(slug | normalized_title |
// date)`, kept separate from item_key/guid identity (§5.1): this is only for
// idempotency (UNIQUE(feed_id, content_hash), §10), never derived-from-content
// identity.
func contentHash(slug, title string, ts time.Time) string {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	sum := sha256.Sum256([]byte(slug + "|" + normalizedTitle + "|" + ts.Format("2006-01-02")))
	return hex.EncodeToString(sum[:])
}

// toCandidate adapts SchemaFlux's typed result (via internal/llm) into this
// package's re-validation input. Validate (contract.go) never sees an
// llm.GeneratedItem directly, keeping SchemaFlux's shape from spreading past
// internal/llm, per PLAN.md §8.
func toCandidate(gi llm.GeneratedItem) Candidate {
	return Candidate{
		Title:       gi.Title,
		SummaryText: gi.SummaryText,
		BodyHTML:    gi.BodyHTML,
		AnswerHTML:  gi.AnswerHTML,
		Link:        gi.Link,
		SourceName:  gi.SourceName,
		Tags:        gi.Tags,
	}
}

// errorKindFromProvider classifies a Generate failure for RunRecord.ErrorKind.
// llm.Classify already sorted it into Transient/Invalid/Fatal (llm/errors.go)
// before this package ever sees it; this just names that classification for
// the run row rather than re-deriving it.
func errorKindFromProvider(err error) string {
	var lerr *llm.Error
	if errors.As(err, &lerr) {
		return lerr.Kind.String()
	}
	return "provider_error"
}

// estimateTokens is a cheap, deliberately crude token estimate (~4 chars per
// token) used ONLY because SchemaFlux's Generating[T] reports zero usage for
// this operation today (PLAN.md §8.1). It exists so RunRecord.TokensIn/Out
// and EstCostUSD are never silently zero; every field they feed is labeled
// as an estimate, never presented as measured.
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// estimateCostUSD prices the token ESTIMATE against Spec's editable price
// table (PLAN.md §12.5, §13). Same estimate caveat as estimateTokens.
func estimateCostUSD(tokensIn, tokensOut int, spec Spec) float64 {
	return float64(tokensIn)/1_000_000*spec.PriceInputPerMToken +
		float64(tokensOut)/1_000_000*spec.PriceOutputPerMToken
}

func sumReasons(reasons map[string]int) int {
	total := 0
	for _, v := range reasons {
		total += v
	}
	return total
}
