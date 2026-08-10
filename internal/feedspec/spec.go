// Package feedspec is the recipe layer (PLAN.md §7): the editable, versionable
// description of a feed — everything the admin UI's editor pane exposes and
// everything the generation pipeline needs to run a feed, short of the
// database identity (ID, timestamps, enabled flag) that belongs to
// internal/model.Feed and the store instead.
//
// SQLite is the source of truth for a live recipe (§7); Spec is what gets
// marshaled into feeds.spec_json and what Export/Import round-trip for the
// "aff recipe export|import" versioning and disaster-recovery path.
package feedspec

import (
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
)

// ModelParams is the model call configuration a recipe pins (§8.1, §12.5).
type ModelParams struct {
	// Model is the provider model id, pinned per recipe and recorded per item
	// so a deprecation or drift is traceable (§17.6 concerns).
	Model string
	// Temperature is accepted and stored, but SchemaFlux v1.1.0 exposes no
	// per-call temperature knob — only Mode and Speed tiers — so this field is
	// a documented no-op until that changes (§8.1). Kept in the type now so
	// existing recipes do not need a migration when it does.
	Temperature float64
	// Mode and Speed are SchemaFlux's actual per-call tuning knobs (§8.1).
	Mode  string
	Speed string
}

// Novelty holds the generative-feed repetition guard (§7, §9 step 5).
type Novelty struct {
	// ExcludeLast is how many recent item titles are listed in
	// {{.RecentTitles}} as an exclusion prompt to the model. Distinct from the
	// embedding novelty window (§7: "last 500 embeddings"), which is a global
	// comparison depth, not a per-recipe setting.
	ExcludeLast int
	// Threshold is the cosine-similarity cutoff above which a candidate is
	// treated as a repeat and discarded (§9 step 5).
	Threshold float64
	// MaxRetries bounds the discard-and-retry loop before the run is skipped
	// and logged rather than looping forever (§9 step 5).
	MaxRetries int
}

// Budgets are the per-feed daily ceilings enforced before each call (§13).
type Budgets struct {
	DailyTokens int
	DailyRuns   int
}

// Source is one upstream URL a grounded feed fetches candidates from (§9
// step 1, `sources` table).
type Source struct {
	URL  string
	Kind string
}

// Spec is the full recipe (§7): everything editable in the admin UI's
// editor pane, independent of the feed's database identity.
type Spec struct {
	Slug        string
	Title       string
	Description string
	Language    string
	Kind        model.FeedKind

	// Cron plus Timezone together, never Cron alone — a UTC-only cron
	// silently shifts a "7am daily" feed by an hour twice a year (§7).
	Cron     string
	Timezone string

	ItemsPerRun int
	// FeedWindow is how many items appear in the rendered XML (§5.4, §7),
	// distinct from the novelty window and from archive retention.
	FeedWindow int

	Model ModelParams

	// SystemPrompt and UserPrompt are text/template sources over
	// generate.Data. Empty for aggregate feeds, which have no generator
	// (§14.2).
	SystemPrompt string
	UserPrompt   string

	Novelty Novelty
	Budgets Budgets

	// Sources is populated for grounded feeds only.
	Sources []Source

	// Members holds member slugs for aggregate feeds only (§14.2). Order is
	// significant: it is the collision tie-break the aggregate renderer uses
	// when two members' items land on the same published_at (§14.2).
	Members []string
}

// Defaults returns a starting-point Spec for a new recipe in the admin UI.
//
// Slug, Title and prompts are deliberately left blank — there is no sane
// default for identity or generation intent, and a validator that only
// checks non-defaults would let a template Spec quietly pass without the
// admin ever having filled anything in. Numeric fields follow the plan where
// it states one (feed window 50, §5.4/§7) and otherwise pick a conservative
// engineering default that PLAN.md does not itself pin — noted per field,
// since §16 fixes server-wide environment defaults (jitter window, worker
// pool size, ...) but not per-recipe numbers.
func Defaults() Spec {
	return Spec{
		Kind:     model.KindGenerative,
		Language: "en",
		Cron:     "0 12 * * *",
		Timezone: "UTC",

		ItemsPerRun: 1,
		FeedWindow:  50, // §5.4/§7: "Feed window capped (default 50 items ...)"

		Model: ModelParams{
			Mode: "balanced",
		},

		Novelty: Novelty{
			ExcludeLast: 50,   // not pinned by PLAN.md; a generous exclusion list without hauling the whole 500-item novelty window into every prompt
			Threshold:   0.92, // not pinned by PLAN.md; conservative cosine cutoff, tune from evidence (§8.1's "evaluate, do not assume" spirit)
			MaxRetries:  3,
		},

		Budgets: Budgets{
			DailyTokens: 200_000, // not pinned by PLAN.md; a per-feed slice of a plausible global ceiling (§13)
			DailyRuns:   24,      // not pinned by PLAN.md; generous enough for hourly schedules without being unbounded
		},
	}
}

// JitterOffset delegates to schedule.Offset so no caller ever recomputes the
// same jitter a second, possibly divergent, way — the admin UI's "next three
// runs" readback must match exactly what the scheduler actually does
// (§14.3).
func (s Spec) JitterOffset(window time.Duration) time.Duration {
	return schedule.Offset(s.Slug, window)
}
