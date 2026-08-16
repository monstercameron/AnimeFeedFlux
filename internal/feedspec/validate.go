package feedspec

import (
	"regexp"
	"time"

	// prompttmpl and modelclass, NOT generate and llm: this package is
	// compiled into the BROWSER (the /generate editor validates recipes
	// client-side), and importing generate/llm here linked the entire
	// server pipeline — SchemaFlux, go-openai — into the WASM bundle
	// (check-wasm-secrets caught it on go-openai's authToken field,
	// 2026-08-15). Both leaves carry exactly the two functions this file
	// needs, stdlib-only.
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/modelclass"
	"github.com/monstercameron/AnimeFeedFlux/internal/prompttmpl"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
)

// Problem is one validation failure. Reason is a stable, machine-readable
// token (never the human message) so the RPC layer can render it against the
// offending field (§11: "errors ... with a machine-readable detail so the UI
// can render against the offending field") and so a test can assert on the
// rule rather than on prose that is free to change.
type Problem struct {
	Field  string
	Reason string
}

// Reason tokens. Exported so callers (RPC handlers, tests) can switch on
// them without restating the string.
const (
	ReasonSlugShape    = "slug_shape"
	ReasonSlugReserved = "slug_reserved"

	ReasonCronInvalid = "cron_invalid"
	// ReasonScheduleModeUnknown: schedule_mode carried something other than
	// "", "scheduled", "adhoc" or "watch". Rejected rather than defaulted:
	// a typo'd "addhoc" silently running on its schedule is the exact
	// surprise the mode exists to prevent.
	ReasonScheduleModeUnknown = "schedule_mode_unknown"
	// ReasonRecurrenceInvalid covers every way a structured schedule can be
	// unschedulable — an interval below 1, a weekly rule with no weekdays, a
	// set position of 5. One reason rather than a dozen because the editor
	// builds the recurrence from controls that cannot produce most of them;
	// the ones a hand-edited recipe file can still produce all mean the same
	// thing to the operator: this schedule would never run.
	ReasonRecurrenceInvalid = "recurrence_invalid"
	ReasonTimezoneInvalid   = "timezone_invalid"

	ReasonTemplateInvalid = "template_invalid"

	ReasonKindInvalid = "kind_invalid"

	ReasonGroundedRequiresSource  = "grounded_requires_source"
	ReasonGenerativeForbidsSource = "generative_forbids_source"
	// Grounded link integrity (§9) means a model-searched URL could only
	// ever be rejected as link_not_in_candidate_set, so offering the tool
	// to a grounded feed is a misconfiguration, not an option. Aggregates
	// have no generator at all (§14.2).
	ReasonGroundedForbidsWebSearch  = "grounded_forbids_web_search"
	ReasonAggregateForbidsWebSearch = "aggregate_forbids_web_search"

	ReasonAggregateRequiresMember = "aggregate_requires_member"
	ReasonAggregateForbidsSource  = "aggregate_forbids_source"
	ReasonAggregateForbidsPrompt  = "aggregate_forbids_prompt"
	ReasonAggregateSelfMember     = "aggregate_self_member"

	ReasonBudgetDailyTokensZero = "budget_daily_tokens_zero"
	ReasonBudgetDailyRunsZero   = "budget_daily_runs_zero"

	ReasonItemsPerRunRange = "items_per_run_out_of_range"
	ReasonFeedWindowRange  = "feed_window_out_of_range"

	// Novelty had no validation at all, despite max_retries directly
	// multiplying provider calls within one run. See the bounds' own comments.
	ReasonNoveltyMaxRetriesRange  = "novelty_max_retries_out_of_range"
	ReasonNoveltyExcludeLastRange = "novelty_exclude_last_out_of_range"
	ReasonNoveltyThresholdRange   = "novelty_threshold_out_of_range"

	// ReasonModelNotChat: a pinned model id that internal/llm.ClassifyModel
	// positively identifies as an embedding model, not a text-generation one.
	// This is the field the UI's own model-picker doc comment calls out as
	// the one where a bad value costs the most (§8: "model not found" is a
	// recipe-scoped Fatal that disables the feed, and looks exactly like a
	// deprecation until a run fails at 4am) — so it is validated server-side
	// rather than trusted from whatever the client sent, regardless of how a
	// bad id got there (a stale UI default, a hand-edited import, `recipe
	// import`, a typo). Deliberately narrow: ClassifyModel's "chat" bucket is
	// a generous heuristic and a real, unrecognised future chat model must
	// still validate, so this only rejects the case ClassifyModel is
	// confident about, not everything it fails to positively classify as chat.
	ReasonModelNotChat = "model_not_chat"
)

// slugPattern is PLAN.md §14.1's namespace rule.
var slugPattern = regexp.MustCompile(`^[a-z0-9-]{3,48}$`)

// reservedSlugs are the routes a feed slug would otherwise collide with
// (§14.1: `/feeds/{slug}...` plus the top-level routes `/`, `/healthz`, ...).
var reservedSlugs = map[string]bool{
	"all":     true,
	"index":   true,
	"healthz": true,
	"robots":  true,
	"favicon": true,
	"items":   true,
	"feeds":   true,
}

// Bounds not pinned by a specific number in PLAN.md; chosen here as
// conservative engineering limits and documented as such rather than left
// unbounded. §5.4 states a 512 KB *byte* ceiling on the rendered feed window,
// which is a render-time concern, not this item-count bound.
const (
	itemsPerRunMin = 1
	itemsPerRunMax = 50
	feedWindowMin  = 1
	feedWindowMax  = 200

	// novelty.max_retries multiplies PROVIDER CALLS inside a single run:
	// generate.Run does `maxAttempts = 1 + MaxNoveltyRetries` and calls the
	// model once per attempt. Nothing re-checks the budget mid-run — §13's
	// gate is consulted before dispatch, deliberately, so "a refusal costs
	// nothing" — which means whatever this number says is spent by a run the
	// gate has already waved through. Unvalidated, a typo of 300 for 3 was a
	// run that could exhaust a daily ceiling on its own.
	//
	// 10 is generous against a default of 3: enough that a feed with a
	// genuinely repetitive corpus can keep trying, far short of a number that
	// turns one dispatch into a spending spree.
	noveltyMaxRetriesMin = 0
	noveltyMaxRetriesMax = 10

	// novelty.exclude_last is how many recent titles are put in the prompt to
	// steer away from (generate.Spec.RecentTitlesN), so it is paid for in
	// input tokens on every single call. Capped at the novelty comparison
	// window §8 names, which is the largest number that could mean anything
	// here.
	noveltyExcludeLastMin = 0
	noveltyExcludeLastMax = 500

	// novelty.threshold is a cosine similarity, so [0,1] is the whole
	// meaningful range. Outside it the gate degenerates: above 1 nothing is
	// ever a repeat, at or below 0 everything is, and the second case burns
	// every retry on every run and then skips — a feed that spends daily and
	// publishes nothing.
	noveltyThresholdMin = 0.0
	noveltyThresholdMax = 1.0
)

// Validate checks s against every rule PLAN.md §7 and §14.1/§14.2 assign to
// this layer, returning every problem found rather than stopping at the
// first — the admin editor renders one error per field, not one at a time.
//
// Two rules are explicitly NOT enforced here, by design:
//   - A slug change after first publish is refused at the store/RPC layer,
//     which is the only place that knows whether the feed has published
//     anything yet (§14.1: changing it would orphan every Tag URI guid
//     already emitted). Validate only checks the slug's *shape*.
//   - Aggregate membership may not nest (§14.2: an aggregate cannot contain
//     another aggregate). Validate rejects an aggregate listing itself, which
//     is all the information available here; the RPC's SetMembers enforces
//     the full rule because only it knows the other feeds' kinds.
func Validate(s Spec) []Problem {
	var problems []Problem

	problems = append(problems, validateSlug(s.Slug)...)
	problems = append(problems, validateSchedule(s)...)
	problems = append(problems, validateKind(s)...)
	problems = append(problems, validateBudgets(s.Budgets)...)
	problems = append(problems, validateRanges(s)...)
	problems = append(problems, validateModel(s.Model)...)

	return problems
}

// validateModel rejects a pinned model id that is unambiguously not a
// text-generation model. Empty is fine — it means "use the deployment
// default" (§12.5) — so this only fires on an explicit, wrong choice.
func validateModel(m ModelParams) []Problem {
	if m.Model == "" {
		return nil
	}
	if _, embedding := modelclass.ClassifyModel(m.Model); embedding {
		return []Problem{{Field: "model", Reason: ReasonModelNotChat}}
	}
	return nil
}

func validateSlug(slug string) []Problem {
	var problems []Problem
	if !slugPattern.MatchString(slug) {
		problems = append(problems, Problem{Field: "slug", Reason: ReasonSlugShape})
	}
	if reservedSlugs[slug] {
		problems = append(problems, Problem{Field: "slug", Reason: ReasonSlugReserved})
	}
	return problems
}

// validateSchedule checks whichever schedule the feed will actually run on.
//
// Precedence matters here as much as shape: a feed with a structured
// recurrence never evaluates its cron string, so validating the cron in that
// case would reject feeds for a field nothing reads, and — worse — would pass
// feeds whose recurrence is nonsense as long as an old cron string happened
// to be well-formed. Spec.Firing owns the precedence; this mirrors it.
func validateSchedule(s Spec) []Problem {
	switch s.ScheduleMode {
	case "", ScheduleModeScheduled, ScheduleModeWatch:
		// Fires on the schedule — it must be valid.
	case ScheduleModeAdhoc:
		// Manual-only: nothing ever evaluates the schedule fields, so a
		// stale cron string or half-built recurrence must not block saving
		// the feed. (They stay stored: switching back to scheduled revives
		// them, and THEN they get validated again.)
		return nil
	default:
		return []Problem{{Field: "schedule_mode", Reason: ReasonScheduleModeUnknown}}
	}
	if s.Recurrence != nil {
		return validateRecurrence(*s.Recurrence, s.Timezone)
	}
	return validateCron(s.Cron, s.Timezone)
}

func validateRecurrence(r Recurrence, tz string) []Problem {
	var problems []Problem
	if _, err := time.LoadLocation(tz); err != nil {
		problems = append(problems, Problem{Field: "timezone", Reason: ReasonTimezoneInvalid})
		// Resolve would fail on the zone alone below, masking any separate
		// problem with the recurrence itself. Check it against UTC instead so
		// both are reported in one pass.
		tz = "UTC"
	}
	if _, err := r.Resolve(tz); err != nil {
		problems = append(problems, Problem{Field: "recurrence", Reason: ReasonRecurrenceInvalid})
	}
	return problems
}

func validateCron(cron, tz string) []Problem {
	var problems []Problem

	loc, err := time.LoadLocation(tz)
	if err != nil {
		problems = append(problems, Problem{Field: "timezone", Reason: ReasonTimezoneInvalid})
		// Fall back to UTC purely so Parse below can still report a
		// cron-shape problem independently; a bad timezone must not mask a
		// separately bad cron expression.
		loc = time.UTC
	}

	if _, err := schedule.Parse(cron, loc); err != nil {
		problems = append(problems, Problem{Field: "cron", Reason: ReasonCronInvalid})
	}

	return problems
}

func validateKind(s Spec) []Problem {
	var problems []Problem

	switch s.Kind {
	case model.KindGenerative:
		if len(s.Sources) > 0 {
			problems = append(problems, Problem{Field: "sources", Reason: ReasonGenerativeForbidsSource})
		}
		problems = append(problems, validatePrompts(s)...)

	case model.KindGrounded:
		if len(s.Sources) == 0 {
			problems = append(problems, Problem{Field: "sources", Reason: ReasonGroundedRequiresSource})
		}
		if s.Model.WebSearch {
			problems = append(problems, Problem{Field: "model", Reason: ReasonGroundedForbidsWebSearch})
		}
		problems = append(problems, validatePrompts(s)...)

	case model.KindAggregate:
		if len(s.Members) == 0 {
			problems = append(problems, Problem{Field: "members", Reason: ReasonAggregateRequiresMember})
		}
		for _, m := range s.Members {
			if m == s.Slug && s.Slug != "" {
				problems = append(problems, Problem{Field: "members", Reason: ReasonAggregateSelfMember})
			}
		}
		if len(s.Sources) > 0 {
			problems = append(problems, Problem{Field: "sources", Reason: ReasonAggregateForbidsSource})
		}
		if s.SystemPrompt != "" || s.UserPrompt != "" {
			problems = append(problems, Problem{Field: "prompts", Reason: ReasonAggregateForbidsPrompt})
		}
		if s.Model.WebSearch {
			problems = append(problems, Problem{Field: "model", Reason: ReasonAggregateForbidsWebSearch})
		}

	default:
		problems = append(problems, Problem{Field: "kind", Reason: ReasonKindInvalid})
	}

	return problems
}

// validatePrompts executes both templates against generate's fully populated
// dummy context (§7: validation is by EXECUTION, not Parse — Parse alone
// does not catch {{.Bogus}}). Uses prompttmpl.ValidateTemplate so this package
// never grows a second, possibly divergent, template-checking code path.
func validatePrompts(s Spec) []Problem {
	var problems []Problem
	if err := prompttmpl.ValidateTemplate(s.SystemPrompt); err != nil {
		problems = append(problems, Problem{Field: "system_prompt", Reason: ReasonTemplateInvalid})
	}
	if err := prompttmpl.ValidateTemplate(s.UserPrompt); err != nil {
		problems = append(problems, Problem{Field: "user_prompt", Reason: ReasonTemplateInvalid})
	}
	return problems
}

func validateBudgets(b Budgets) []Problem {
	var problems []Problem
	if b.DailyTokens <= 0 {
		problems = append(problems, Problem{Field: "budgets.daily_tokens", Reason: ReasonBudgetDailyTokensZero})
	}
	if b.DailyRuns <= 0 {
		problems = append(problems, Problem{Field: "budgets.daily_runs", Reason: ReasonBudgetDailyRunsZero})
	}
	return problems
}

func validateRanges(s Spec) []Problem {
	var problems []Problem
	if s.ItemsPerRun < itemsPerRunMin || s.ItemsPerRun > itemsPerRunMax {
		problems = append(problems, Problem{Field: "items_per_run", Reason: ReasonItemsPerRunRange})
	}
	if s.FeedWindow < feedWindowMin || s.FeedWindow > feedWindowMax {
		problems = append(problems, Problem{Field: "feed_window", Reason: ReasonFeedWindowRange})
	}

	// Novelty was not range-checked at all. max_retries is the one that
	// costs: it sets how many times a single run may call the provider, and
	// the budget gate runs before dispatch, never during — see the bounds.
	if s.Novelty.MaxRetries < noveltyMaxRetriesMin || s.Novelty.MaxRetries > noveltyMaxRetriesMax {
		problems = append(problems, Problem{Field: "novelty.max_retries", Reason: ReasonNoveltyMaxRetriesRange})
	}
	if s.Novelty.ExcludeLast < noveltyExcludeLastMin || s.Novelty.ExcludeLast > noveltyExcludeLastMax {
		problems = append(problems, Problem{Field: "novelty.exclude_last", Reason: ReasonNoveltyExcludeLastRange})
	}
	// A zero threshold is the unset case, not a configured one: a recipe with
	// no novelty section decodes to 0, and cmd/animefeedflux substitutes
	// defaultNoveltyThreshold for it rather than treating everything as a
	// repeat. Only a value someone actually wrote is range-checked.
	if s.Novelty.Threshold != 0 &&
		(s.Novelty.Threshold < noveltyThresholdMin || s.Novelty.Threshold > noveltyThresholdMax) {
		problems = append(problems, Problem{Field: "novelty.threshold", Reason: ReasonNoveltyThresholdRange})
	}
	return problems
}
