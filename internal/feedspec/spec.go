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
	// WebSearch declares the provider's built-in web-search tool on this
	// feed's generation calls (SchemaFlux v1.2.0's Generating.WebSearch),
	// giving the model live web access it otherwise has none of; the model
	// still decides per-run whether to search. Generative feeds only —
	// ValidateSpec refuses it on grounded feeds, whose published links must
	// come from the fetched candidate set (§9), so a model-searched URL
	// could only ever be rejected as link_not_in_candidate_set.
	WebSearch bool `toml:"web_search,omitempty" json:"web_search,omitempty"`
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
	//
	// Cron is the fallback and the escape hatch now, not the primary: when
	// Recurrence is set it wins (see Spec.Firing). Kept because every recipe
	// exported before 2026-08-11 carries one, and because a few shapes —
	// "every 15 minutes", "weekdays at 9 and 17" — are still easier to say in
	// cron than in an interval.
	Cron     string
	Timezone string

	// Recurrence is the structured schedule the editor writes: an interval
	// from an anchor date rather than a set of calendar fields to match.
	//
	// Nil means "use Cron". It exists because cron cannot express most of the
	// schedules people actually want — "every other Thursday", "every 3
	// weeks", "the second Tuesday" all have no cron form at all. See
	// internal/schedule/recurrence.go.
	Recurrence *Recurrence

	// ScheduleMode is how Cron/Recurrence are interpreted (§7 revision
	// 2026-08-15): "" or "scheduled" fires runs on the schedule (the only
	// pre-revision behaviour, so every existing recipe keeps meaning what it
	// meant); "adhoc" never fires automatically — Run Now is the feed's only
	// trigger, the schedule fields are ignored, and staleness monitoring
	// exempts it; "watch" fires on the schedule as a CHECK, and the model
	// answering "nothing noteworthy" is a quiet, expected skip rather than a
	// failure (generate.Spec.WatchMode carries this into the pipeline).
	ScheduleMode string `toml:"schedule_mode,omitempty" json:"schedule_mode,omitempty"`

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

// The schedule modes Spec.ScheduleMode accepts. "scheduled" and "" are the
// same mode — "" is what every recipe written before the field existed
// carries, and normalizing it away would dirty every export for nothing.
const (
	ScheduleModeScheduled = "scheduled"
	ScheduleModeAdhoc     = "adhoc"
	ScheduleModeWatch     = "watch"
)

// IsAdhoc reports whether this feed only ever runs manually.
func (s Spec) IsAdhoc() bool { return s.ScheduleMode == ScheduleModeAdhoc }

// IsWatch reports whether this feed's schedule is a check cadence — runs
// fire on it, but "nothing noteworthy" is an expected quiet outcome.
func (s Spec) IsWatch() bool { return s.ScheduleMode == ScheduleModeWatch }

// IsScheduled reports whether the schedule actually fires runs (the
// default). Watch feeds are ALSO fired by the scheduler — this is about
// firing, so it is true for both; only adhoc opts out.
func (s Spec) IsScheduled() bool { return !s.IsAdhoc() }

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
