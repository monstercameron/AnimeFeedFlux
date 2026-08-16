package generate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
)

// formattingFake wraps llm.Fake with the Formatter capability. A separate
// test type rather than a change to llm.Fake itself, so every existing test
// keeps exercising exactly what it exercised before: a provider WITHOUT the
// capability, which is stage 2's skip path.
type formattingFake struct {
	*llm.Fake
	formats llm.ItemFormats
	err     error
	calls   []llm.FormatRequest
}

func (f *formattingFake) Format(_ context.Context, req llm.FormatRequest) (llm.FormatResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return llm.FormatResult{}, f.err
	}
	return llm.FormatResult{Formats: f.formats, Raw: "{}", Model: req.Model}, nil
}

func formatTestItem() model.Item {
	return model.Item{
		Title:       "A perfectly valid title",
		SummaryText: "raw summary",
		BodyHTML:    `<p>raw body</p>`,
		PublishedAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestFormatItemsSetsValidatedVariants(t *testing.T) {
	fake := &formattingFake{Fake: llm.NewFake(), formats: llm.ItemFormats{
		FeedHTML:  `<p>reader <em>optimized</em></p>`,
		CardText:  "tight card teaser",
		EmbedText: "widget line",
		PageHTML:  `<h3>Section</h3><p>page body</p>`,
	}}
	deps := Deps{Provider: fake}

	items, in, out := formatItems(t.Context(), deps, model.Feed{Slug: "f"}, Spec{Model: "m"}, []model.Item{formatTestItem()}, "req")
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	f := items[0].Formats
	if f.FeedHTML == "" || f.CardText != "tight card teaser" || f.EmbedText != "widget line" || f.PageHTML == "" {
		t.Fatalf("variants not applied: %+v", f)
	}
	if in <= 0 || out <= 0 {
		t.Fatalf("format token estimates not counted: in=%d out=%d", in, out)
	}
	if len(fake.calls) != 1 || fake.calls[0].Model != "m" {
		t.Fatalf("format call = %+v, want one call on the run's model", fake.calls)
	}
}

func TestFormatItemsDegradesOnFailureAndAbsence(t *testing.T) {
	// A failing Format call publishes on raw fields, never errors the run.
	failing := &formattingFake{Fake: llm.NewFake(), err: errors.New("provider down")}
	items, _, _ := formatItems(t.Context(), Deps{Provider: failing}, model.Feed{Slug: "f"}, Spec{}, []model.Item{formatTestItem()}, "req")
	if !items[0].Formats.Empty() {
		t.Fatalf("a failed format call must leave variants empty, got %+v", items[0].Formats)
	}

	// A provider without the capability is a no-op.
	items, in, out := formatItems(t.Context(), Deps{Provider: llm.NewFake()}, model.Feed{Slug: "f"}, Spec{}, []model.Item{formatTestItem()}, "req")
	if !items[0].Formats.Empty() || in != 0 || out != 0 {
		t.Fatalf("a capability-less provider must skip formatting entirely: %+v in=%d out=%d", items[0].Formats, in, out)
	}
}

func TestValidateFormatsDropsRuleBreakers(t *testing.T) {
	it := formatTestItem()
	it.AnswerHTML = "<p>Spike Spiegel</p>"

	f := validateFormats(llm.ItemFormats{
		// Script must be sanitized out, not dropped wholesale.
		FeedHTML: `<p>fine</p><script>alert(1)</script>`,
		// A relative href drops the variant — RSS has no base URL.
		PageHTML: `<p><a href="/relative">x</a></p>`,
		// Markup in a text surface drops it.
		CardText: `real <b>markup</b> here`,
		// The answer leaking into a text surface drops it (§5.5).
		EmbedText: "the answer is spike spiegel",
	}, it)

	if f.FeedHTML == "" || f.FeedHTML == `<p>fine</p><script>alert(1)</script>` {
		t.Fatalf("feed html should be sanitized, not dropped or passed raw: %q", f.FeedHTML)
	}
	if f.PageHTML != "" {
		t.Fatalf("relative link survived: %q", f.PageHTML)
	}
	if f.CardText != "" {
		t.Fatalf("markup in card text survived: %q", f.CardText)
	}
	if f.EmbedText != "" {
		t.Fatalf("answer leak in embed text survived: %q", f.EmbedText)
	}

	// Over triple the cap means the brief was ignored; the raw summary is
	// the safer render.
	long := make([]byte, 900)
	for i := range long {
		long[i] = 'a'
	}
	f = validateFormats(llm.ItemFormats{CardText: string(long)}, formatTestItem())
	if f.CardText != "" {
		t.Fatalf("a 900-char card text survived the 280-char cap")
	}

	// A variant identical to its raw field carries no information.
	raw := formatTestItem()
	f = validateFormats(llm.ItemFormats{CardText: raw.SummaryText, FeedHTML: raw.BodyHTML}, raw)
	if f.CardText != "" || f.FeedHTML != "" {
		t.Fatalf("identical variants should drop: %+v", f)
	}
}

func TestRenderAccessorsPreferVariants(t *testing.T) {
	it := formatTestItem()
	if it.RenderCardText() != "raw summary" || it.RenderFeedHTML() != "<p>raw body</p>" {
		t.Fatal("empty formats must fall back to raw fields")
	}
	it.Formats = model.ItemFormats{CardText: "card", FeedHTML: "<p>feed</p>", EmbedText: "embed", PageHTML: "<p>page</p>"}
	if it.RenderCardText() != "card" || it.RenderFeedHTML() != "<p>feed</p>" ||
		it.RenderEmbedText() != "embed" || it.RenderPageHTML() != "<p>page</p>" {
		t.Fatal("set variants must win over raw fields")
	}
}
