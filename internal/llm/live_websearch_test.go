package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_WebSearchReachesTheLiveWeb is the paid, opt-in proof that
// Request.WebSearch actually grants the model web access end to end —
// SchemaFlux v1.2.0's Generating.WebSearch through the Responses API's
// built-in web_search tool. It never runs in CI or a default `go test`:
// it costs real money and needs a real key, so it is gated behind
// AFF_LIVE_LLM=1 plus OPENAI_KEY, the same discipline as every other
// provider-touching check in this repository.
//
// The probe is an A/B on a fact that is volatile and post-dates any
// training cutoff: the current price of Bitcoin. The test itself asserts
// only mechanics (both calls succeed, both return one item — proving the
// tool declaration does not break schema-strict decoding); the freshness
// judgment is the operator's, which is why both answers are logged. A
// searched answer tracks the live price; an unsearched one can only guess
// from training data.
func TestLive_WebSearchReachesTheLiveWeb(t *testing.T) {
	if os.Getenv("AFF_LIVE_LLM") == "" {
		t.Skip("live provider test; set AFF_LIVE_LLM=1 and OPENAI_KEY to run")
	}
	key := os.Getenv("OPENAI_KEY")
	if key == "" {
		t.Skip("OPENAI_KEY not set")
	}

	p, err := NewSchemaFluxProvider(Config{APIKey: key})
	if err != nil {
		t.Fatalf("NewSchemaFluxProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	run := func(webSearch bool) Result {
		res, err := p.Generate(ctx, Request{
			Prompt: "What is the current price of Bitcoin in US dollars right now? " +
				"Return exactly one item. Title: only the number, no symbols. " +
				"Summary: one sentence naming your source and the price's timestamp. " +
				"Body: the same sentence as plain prose.",
			Model:     "gpt-5.6-luna",
			MaxItems:  1,
			WebSearch: webSearch,
		})
		if err != nil {
			t.Fatalf("Generate(webSearch=%v): %v", webSearch, err)
		}
		if len(res.Items) != 1 {
			t.Fatalf("Generate(webSearch=%v) returned %d items, want 1", webSearch, len(res.Items))
		}
		return res
	}

	with := run(true)
	without := run(false)

	t.Logf("WITH web search:    title=%q summary=%q", with.Items[0].Title, with.Items[0].SummaryText)
	t.Logf("WITHOUT web search: title=%q summary=%q", without.Items[0].Title, without.Items[0].SummaryText)
}
