package flowtest

// Shared, minimal-but-valid fixtures for every flow test in this package.
// "Valid" here means "passes internal/generate.Validate" (contract.go's
// title length/punctuation/summary/body rules) — a fixture that Validate
// rejects would make every flow test about the rejection path instead of
// the flow it's named for.

import (
	"github.com/monstercameron/AnimeFeedFlux/internal/generate"
	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// validGeneratedItem returns a GeneratedItem that clears every contract.go
// rule for a generative feed: title 10-200 runes with no trailing
// punctuation, a plain-text summary under the 500-rune cap, and BodyHTML
// whose only link is absolute.
func validGeneratedItem(title string) llm.GeneratedItem {
	return llm.GeneratedItem{
		Title:       title,
		SummaryText: "A short plain-text summary of the item, safely under the cap",
		BodyHTML:    `<p>Full body with an <a href="https://example.com/x">absolute link</a></p>`,
		Tags:        []string{"anime"},
	}
}

// validGenerateResult wraps validGeneratedItem for each title into an
// llm.Result ready to queue on a *llm.Fake via QueueResult.
func validGenerateResult(titles ...string) llm.Result {
	items := make([]llm.GeneratedItem, len(titles))
	for i, title := range titles {
		items[i] = validGeneratedItem(title)
	}
	return llm.Result{Items: items, Raw: "{}"}
}

// validSampleSpec is a minimal generate.Spec for a generative feed: one item
// per call, a template that only needs {{.ItemsPerRun}} and
// {{.RecentTitles}} (both of which generate.Data always populates for a
// generative feed, per acquireContext in runner.go), and non-zero prices so
// EstCostUSD is never trivially zero (§22 J3's "cost is non-zero").
func validSampleSpec() generate.Spec {
	return generate.Spec{
		SystemPrompt:         "You write short anime trivia questions.",
		UserPromptTemplate:   "Write {{.ItemsPerRun}} item(s) for {{.Today}}. Avoid: {{range .RecentTitles}}{{.}} {{end}}",
		Model:                "flowtest-model",
		ItemsPerRun:          1,
		PriceInputPerMToken:  1.00,
		PriceOutputPerMToken: 2.00,
	}
}

// validGenerativeFeed returns a fresh, unsaved generative feed for tests
// that don't care about the specific slug.
func validGenerativeFeed(slug string) model.Feed {
	return model.Feed{
		Slug:     slug,
		Title:    "Flow Test Feed",
		Kind:     model.KindGenerative,
		Timezone: "UTC",
		Enabled:  true,
	}
}
