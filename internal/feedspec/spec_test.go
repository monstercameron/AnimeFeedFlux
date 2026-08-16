package feedspec

import (
	"reflect"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/schedule"
)

// --- fixtures -------------------------------------------------------------

func validGenerative() Spec {
	s := Defaults()
	s.Slug = "daily-trivia"
	s.Title = "Daily Anime Trivia"
	s.Description = "One trivia question a day."
	s.Kind = model.KindGenerative
	s.SystemPrompt = "You write trivia for {{.FeedTitle}}."
	s.UserPrompt = "Today is {{.Today}} ({{.Weekday}}), season {{.Season}}. Write {{.ItemsPerRun}} question(s). Avoid: {{range .RecentTitles}}{{.}} {{end}}"
	return s
}

func validGrounded() Spec {
	s := Defaults()
	s.Slug = "ann-digest"
	s.Title = "ANN Digest"
	s.Kind = model.KindGrounded
	s.SystemPrompt = "You summarize anime news."
	s.UserPrompt = "Candidates: {{range .Candidates}}{{.Title}} ({{.URL}}) {{end}}"
	s.Sources = []Source{{URL: "https://www.animenewsnetwork.com/all/rss.xml", Kind: "rss"}}
	return s
}

func validAggregate() Spec {
	s := Defaults()
	s.Slug = "everything"
	s.Title = "Everything"
	s.Kind = model.KindAggregate
	s.SystemPrompt = ""
	s.UserPrompt = ""
	s.Sources = nil
	s.Members = []string{"daily-trivia", "ann-digest"}
	return s
}

// --- Validate: happy paths --------------------------------------------------

func TestValidate_GenerativeSpecPasses(t *testing.T) {
	if problems := Validate(validGenerative()); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
}

func TestValidate_GroundedSpecPasses(t *testing.T) {
	if problems := Validate(validGrounded()); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
}

func TestValidate_AggregateSpecPasses(t *testing.T) {
	if problems := Validate(validAggregate()); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}
}

// --- Validate: individual rules --------------------------------------------

func hasProblem(problems []Problem, field, reason string) bool {
	for _, p := range problems {
		if p.Field == field && p.Reason == reason {
			return true
		}
	}
	return false
}

func TestValidate_SlugTooShort(t *testing.T) {
	s := validGenerative()
	s.Slug = "ab"
	if problems := Validate(s); !hasProblem(problems, "slug", ReasonSlugShape) {
		t.Fatalf("expected %s on slug, got %+v", ReasonSlugShape, problems)
	}
}

func TestValidate_SlugUppercaseRejected(t *testing.T) {
	s := validGenerative()
	s.Slug = "Daily-Trivia"
	if problems := Validate(s); !hasProblem(problems, "slug", ReasonSlugShape) {
		t.Fatalf("expected %s on slug, got %+v", ReasonSlugShape, problems)
	}
}

func TestValidate_ReservedSlugRejected(t *testing.T) {
	for _, slug := range []string{"all", "index", "healthz", "robots", "favicon", "items", "feeds"} {
		s := validGenerative()
		s.Slug = slug
		problems := Validate(s)
		if !hasProblem(problems, "slug", ReasonSlugReserved) {
			t.Errorf("slug %q: expected %s, got %+v", slug, ReasonSlugReserved, problems)
		}
	}
}

func TestValidate_BadCronRejected(t *testing.T) {
	s := validGenerative()
	s.Cron = "not a cron"
	if problems := Validate(s); !hasProblem(problems, "cron", ReasonCronInvalid) {
		t.Fatalf("expected %s on cron, got %+v", ReasonCronInvalid, problems)
	}
}

func TestValidate_BadTimezoneRejected(t *testing.T) {
	s := validGenerative()
	s.Timezone = "Mars/Olympus_Mons"
	problems := Validate(s)
	if !hasProblem(problems, "timezone", ReasonTimezoneInvalid) {
		t.Fatalf("expected %s on timezone, got %+v", ReasonTimezoneInvalid, problems)
	}
}

func TestValidate_BadTimezoneDoesNotMaskCronProblem(t *testing.T) {
	// A bad timezone must not swallow an independently bad cron expression -
	// Validate falls back to UTC internally only to keep checking the cron.
	s := validGenerative()
	s.Timezone = "Mars/Olympus_Mons"
	s.Cron = "not a cron"
	problems := Validate(s)
	if !hasProblem(problems, "timezone", ReasonTimezoneInvalid) {
		t.Errorf("expected timezone problem, got %+v", problems)
	}
	if !hasProblem(problems, "cron", ReasonCronInvalid) {
		t.Errorf("expected cron problem, got %+v", problems)
	}
}

func TestValidate_DSTRelevantCronAccepted(t *testing.T) {
	// "7am daily, America/New_York" is exactly the case §7 calls out: it must
	// validate cleanly, and the resolved schedule must actually be usable
	// (Parse succeeds and Next returns a real instant) across a DST
	// transition rather than merely "not erroring".
	s := validGenerative()
	s.Cron = "0 7 * * *"
	s.Timezone = "America/New_York"
	if problems := Validate(s); len(problems) != 0 {
		t.Fatalf("expected no problems, got %+v", problems)
	}

	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	sched, err := schedule.Parse(s.Cron, loc)
	if err != nil {
		t.Fatalf("schedule.Parse: %v", err)
	}
	// 2026-03-08 is the US spring-forward date; the next 7am firing after
	// midnight that day must still land, not silently vanish.
	before := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	next := sched.Next(before)
	if next.IsZero() {
		t.Fatal("expected a firing after spring-forward, got zero time")
	}
}

func TestValidate_UnknownTemplateVariableRejected(t *testing.T) {
	s := validGenerative()
	s.UserPrompt = "Bogus: {{.NotARealField}}"
	if problems := Validate(s); !hasProblem(problems, "user_prompt", ReasonTemplateInvalid) {
		t.Fatalf("expected %s on user_prompt, got %+v", ReasonTemplateInvalid, problems)
	}
}

func TestValidate_UnknownSystemTemplateVariableRejected(t *testing.T) {
	s := validGenerative()
	s.SystemPrompt = "Bogus: {{.NotARealField}}"
	if problems := Validate(s); !hasProblem(problems, "system_prompt", ReasonTemplateInvalid) {
		t.Fatalf("expected %s on system_prompt, got %+v", ReasonTemplateInvalid, problems)
	}
}

func TestValidate_GroundedRequiresSource(t *testing.T) {
	s := validGrounded()
	s.Sources = nil
	if problems := Validate(s); !hasProblem(problems, "sources", ReasonGroundedRequiresSource) {
		t.Fatalf("expected %s on sources, got %+v", ReasonGroundedRequiresSource, problems)
	}
}

func TestValidate_GenerativeForbidsSource(t *testing.T) {
	s := validGenerative()
	s.Sources = []Source{{URL: "https://example.com/feed.xml", Kind: "rss"}}
	if problems := Validate(s); !hasProblem(problems, "sources", ReasonGenerativeForbidsSource) {
		t.Fatalf("expected %s on sources, got %+v", ReasonGenerativeForbidsSource, problems)
	}
}

func TestValidate_AggregateRequiresMember(t *testing.T) {
	s := validAggregate()
	s.Members = nil
	if problems := Validate(s); !hasProblem(problems, "members", ReasonAggregateRequiresMember) {
		t.Fatalf("expected %s on members, got %+v", ReasonAggregateRequiresMember, problems)
	}
}

func TestValidate_AggregateForbidsSource(t *testing.T) {
	s := validAggregate()
	s.Sources = []Source{{URL: "https://example.com/feed.xml", Kind: "rss"}}
	if problems := Validate(s); !hasProblem(problems, "sources", ReasonAggregateForbidsSource) {
		t.Fatalf("expected %s on sources, got %+v", ReasonAggregateForbidsSource, problems)
	}
}

func TestValidate_AggregateForbidsPrompt(t *testing.T) {
	s := validAggregate()
	s.SystemPrompt = "I should not exist"
	if problems := Validate(s); !hasProblem(problems, "prompts", ReasonAggregateForbidsPrompt) {
		t.Fatalf("expected %s on prompts, got %+v", ReasonAggregateForbidsPrompt, problems)
	}
}

func TestValidate_AggregateCannotListItself(t *testing.T) {
	s := validAggregate()
	s.Members = append(s.Members, s.Slug)
	if problems := Validate(s); !hasProblem(problems, "members", ReasonAggregateSelfMember) {
		t.Fatalf("expected %s on members, got %+v", ReasonAggregateSelfMember, problems)
	}
}

func TestValidate_UnknownKindRejected(t *testing.T) {
	s := validGenerative()
	s.Kind = model.FeedKind("bogus")
	if problems := Validate(s); !hasProblem(problems, "kind", ReasonKindInvalid) {
		t.Fatalf("expected %s on kind, got %+v", ReasonKindInvalid, problems)
	}
}

func TestValidate_ZeroBudgetsRejected(t *testing.T) {
	s := validGenerative()
	s.Budgets = Budgets{}
	problems := Validate(s)
	if !hasProblem(problems, "budgets.daily_tokens", ReasonBudgetDailyTokensZero) {
		t.Errorf("expected %s, got %+v", ReasonBudgetDailyTokensZero, problems)
	}
	if !hasProblem(problems, "budgets.daily_runs", ReasonBudgetDailyRunsZero) {
		t.Errorf("expected %s, got %+v", ReasonBudgetDailyRunsZero, problems)
	}
}

func TestValidate_ItemsPerRunOutOfRange(t *testing.T) {
	s := validGenerative()
	s.ItemsPerRun = 0
	if problems := Validate(s); !hasProblem(problems, "items_per_run", ReasonItemsPerRunRange) {
		t.Fatalf("expected %s, got %+v", ReasonItemsPerRunRange, problems)
	}

	s = validGenerative()
	s.ItemsPerRun = itemsPerRunMax + 1
	if problems := Validate(s); !hasProblem(problems, "items_per_run", ReasonItemsPerRunRange) {
		t.Fatalf("expected %s, got %+v", ReasonItemsPerRunRange, problems)
	}
}

func TestValidate_EmptyModelPasses(t *testing.T) {
	s := validGenerative()
	s.Model.Model = ""
	if problems := Validate(s); hasProblem(problems, "model", ReasonModelNotChat) {
		t.Errorf("empty model (deployment default) must not be rejected, got %+v", problems)
	}
}

func TestValidate_EmbeddingModelRejected(t *testing.T) {
	s := validGenerative()
	s.Model.Model = "text-embedding-3-large"
	if problems := Validate(s); !hasProblem(problems, "model", ReasonModelNotChat) {
		t.Fatalf("expected %s, got %+v", ReasonModelNotChat, problems)
	}
}

func TestValidate_ChatModelPasses(t *testing.T) {
	s := validGenerative()
	s.Model.Model = "gpt-5.4-mini"
	if problems := Validate(s); hasProblem(problems, "model", ReasonModelNotChat) {
		t.Errorf("expected a chat model to pass, got %+v", problems)
	}
}

func TestValidate_UnclassifiedModelPasses(t *testing.T) {
	// ClassifyModel is deliberately generous: an id it cannot positively
	// classify either way must still validate, or a real but unrecognised
	// future chat model would be rejected outright.
	s := validGenerative()
	s.Model.Model = "some-future-model-family-9"
	if problems := Validate(s); hasProblem(problems, "model", ReasonModelNotChat) {
		t.Errorf("expected an unclassified model id to pass, got %+v", problems)
	}
}

func TestValidate_FeedWindowOutOfRange(t *testing.T) {
	s := validGenerative()
	s.FeedWindow = 0
	if problems := Validate(s); !hasProblem(problems, "feed_window", ReasonFeedWindowRange) {
		t.Fatalf("expected %s, got %+v", ReasonFeedWindowRange, problems)
	}

	s = validGenerative()
	s.FeedWindow = feedWindowMax + 1
	if problems := Validate(s); !hasProblem(problems, "feed_window", ReasonFeedWindowRange) {
		t.Fatalf("expected %s, got %+v", ReasonFeedWindowRange, problems)
	}
}

func TestValidate_MultipleProblemsAllReported(t *testing.T) {
	s := validGenerative()
	s.Slug = "AB"
	s.Cron = "garbage"
	s.Budgets = Budgets{}
	problems := Validate(s)
	if len(problems) < 4 {
		t.Fatalf("expected at least 4 problems (slug shape + cron + 2 budgets), got %+v", problems)
	}
}

// --- Export/Import ----------------------------------------------------------

func TestExportImport_RoundTripsGenerative(t *testing.T) {
	roundTrip(t, validGenerative())
}

func TestExportImport_RoundTripsGrounded(t *testing.T) {
	roundTrip(t, validGrounded())
}

func TestExportImport_RoundTripsAggregate(t *testing.T) {
	roundTrip(t, validAggregate())
}

func roundTrip(t *testing.T, s Spec) {
	t.Helper()
	b, err := Export(s)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got, err := Import(b)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !reflect.DeepEqual(s, got) {
		t.Fatalf("round trip mismatch:\n original: %+v\n got:      %+v", s, got)
	}
}

func TestImport_RejectsGarbage(t *testing.T) {
	if _, err := Import([]byte("not json")); err == nil {
		t.Fatal("expected an error importing malformed input")
	}
}

// --- Defaults ---------------------------------------------------------------

func TestDefaults_PassValidationOnceIdentityIsFilled(t *testing.T) {
	// Defaults() itself is an incomplete recipe (no slug/title/prompt - see
	// its doc comment on why that's deliberate); filling in only identity
	// and prompts should be enough to pass, proving every numeric default is
	// itself within the validated bounds.
	s := Defaults()
	s.Slug = "some-feed"
	s.Title = "Some Feed"
	s.SystemPrompt = "sys"
	s.UserPrompt = "user for {{.FeedTitle}}"
	if problems := Validate(s); len(problems) != 0 {
		t.Fatalf("expected Defaults() to be valid once identity is filled, got %+v", problems)
	}
}

// --- JitterOffset -------------------------------------------------------------

func TestJitterOffset_MatchesScheduleOffset(t *testing.T) {
	s := validGenerative()
	window := 10 * time.Minute
	got := s.JitterOffset(window)
	want := schedule.Offset(s.Slug, window)
	if got != want {
		t.Fatalf("JitterOffset() = %v, want %v (schedule.Offset)", got, want)
	}
}

func TestJitterOffset_DeterministicAcrossCalls(t *testing.T) {
	s := validGenerative()
	window := 10 * time.Minute
	a := s.JitterOffset(window)
	b := s.JitterOffset(window)
	if a != b {
		t.Fatalf("JitterOffset() not deterministic: %v != %v", a, b)
	}
}
