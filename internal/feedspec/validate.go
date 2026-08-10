package feedspec

import (
	"regexp"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
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

	ReasonCronInvalid     = "cron_invalid"
	ReasonTimezoneInvalid = "timezone_invalid"

	ReasonTemplateInvalid = "template_invalid"

	ReasonKindInvalid = "kind_invalid"

	ReasonGroundedRequiresSource  = "grounded_requires_source"
	ReasonGenerativeForbidsSource = "generative_forbids_source"

	ReasonAggregateRequiresMember = "aggregate_requires_member"
	ReasonAggregateForbidsSource  = "aggregate_forbids_source"
	ReasonAggregateForbidsPrompt  = "aggregate_forbids_prompt"
	ReasonAggregateSelfMember     = "aggregate_self_member"

	ReasonBudgetDailyTokensZero = "budget_daily_tokens_zero"
	ReasonBudgetDailyRunsZero   = "budget_daily_runs_zero"

	ReasonItemsPerRunRange = "items_per_run_out_of_range"
	ReasonFeedWindowRange  = "feed_window_out_of_range"
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
	problems = append(problems, validateSchedule(s.Cron, s.Timezone)...)
	problems = append(problems, validateKind(s)...)
	problems = append(problems, validateBudgets(s.Budgets)...)
	problems = append(problems, validateRanges(s)...)

	return problems
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

func validateSchedule(cron, tz string) []Problem {
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

	default:
		problems = append(problems, Problem{Field: "kind", Reason: ReasonKindInvalid})
	}

	return problems
}

// validatePrompts executes both templates against generate's fully populated
// dummy context (§7: validation is by EXECUTION, not Parse — Parse alone
// does not catch {{.Bogus}}). Uses generate.ValidateTemplate so this package
// never grows a second, possibly divergent, template-checking code path.
func validatePrompts(s Spec) []Problem {
	var problems []Problem
	if err := generate.ValidateTemplate(s.SystemPrompt); err != nil {
		problems = append(problems, Problem{Field: "system_prompt", Reason: ReasonTemplateInvalid})
	}
	if err := generate.ValidateTemplate(s.UserPrompt); err != nil {
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
	return problems
}
