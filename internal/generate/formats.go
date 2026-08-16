// Stage 2 of the two-stage generation strategy (PLAN.md §9, revised
// 2026-08-15): stage 1 produced and validated the RAW item fields; this
// file asks the model to FORMAT those fields once per output surface —
// content:encoded for feed readers, the Slack card text, the embed-widget
// line, and the item's own page — so each surface gets content optimized
// for how it actually renders, instead of every surface sharing one string
// that fits none of them perfectly.
//
// # Degradation is the contract
//
// Formatting is an ENHANCEMENT of an already-valid item, never a gate in
// front of it. Every failure path here — provider without the Formatter
// capability, a failed call, a variant that flunks its own validation —
// ends in "that variant stays empty and the renderer falls back to the raw
// field", which is exactly what every item published before this stage
// existed already does. A run must never skip or fail because formatting
// did; stage 1's output is a complete, correct feed on its own.
package generate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/monstercameron/AnimeFeedFlux/internal/llm"
	"github.com/monstercameron/AnimeFeedFlux/internal/model"
	"github.com/monstercameron/AnimeFeedFlux/internal/sanitize"
)

// utf8ValidNoControls applies the same encoding floor contract.go holds the
// raw fields to (§9's "encoding is part of validation"): valid UTF-8 and no
// XML-illegal control characters. "" passes — an empty variant is the
// fallback state, not a value.
func utf8ValidNoControls(v string) bool {
	if !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if isForbiddenControl(r) {
			return false
		}
	}
	return true
}

// formatSystemPrompt is the stage-2 system prompt. It is OURS, not part of
// the recipe: the recipe's prompts describe what to write (stage 1); how
// each surface renders is a property of this application, identical for
// every feed, and letting recipes override it would let one feed quietly
// break the Slack card rules §5.5 exists to protect.
const formatSystemPrompt = `You reformat one finished feed item for four output surfaces. ` +
	`Never change facts, never add information, never contradict the original. ` +
	`feed_html: the item body as clean rich HTML for RSS reader apps — simple markup only ` +
	`(p, em, strong, a, ul, ol, li, blockquote, h3), every link absolute, no scripts or styles or images. ` +
	`card_text: PLAIN TEXT, no markup, at most 280 characters — one tight, scannable teaser for a chat-app card; ` +
	`it must read complete on its own and make someone want to open the item. ` +
	`embed_text: PLAIN TEXT, no markup, at most 180 characters — one self-contained line for a tiny embedded widget; ` +
	`no teaser phrasing, just the essence. ` +
	`page_html: the item as a standalone reading page body — same HTML rules as feed_html but structured for ` +
	`comfortable reading (short paragraphs, subheadings where they help). ` +
	`If a quiz answer is provided it may appear in feed_html and page_html exactly as the original placed it, ` +
	`but card_text and embed_text must NEVER reveal or hint at it.`

// formatItemPrompt carries the raw fields as JSON — unambiguous quoting,
// and the model sees exactly what stage 1 produced, nothing re-worded.
func formatItemPrompt(it model.Item) string {
	payload := map[string]string{
		"title":        it.Title,
		"summary_text": it.SummaryText,
		"body_html":    it.BodyHTML,
	}
	if it.AnswerHTML != "" {
		payload["answer_html"] = it.AnswerHTML
	}
	if it.Link != "" {
		payload["link"] = it.Link
	}
	raw, _ := json.Marshal(payload)
	return "Reformat this item for the four surfaces:\n" + string(raw)
}

// formatItems runs stage 2 over items, in place semantically but on copies
// mechanically, and returns the items plus the estimated token spend of the
// calls it made (same estimateTokens heuristic stage 1 uses — SchemaFlux
// reports no usage for these calls either).
func formatItems(ctx context.Context, deps Deps, feed model.Feed, spec Spec, items []model.Item, requestID string) ([]model.Item, int, int) {
	formatter, ok := deps.Provider.(llm.Formatter)
	if !ok || len(items) == 0 {
		return items, 0, 0
	}

	systemTokens := estimateTokens(formatSystemPrompt)
	tokensIn, tokensOut := 0, 0
	out := make([]model.Item, len(items))
	copy(out, items)

	for i := range out {
		prompt := formatItemPrompt(out[i])
		tokensIn += systemTokens + estimateTokens(prompt)
		res, err := formatter.Format(ctx, llm.FormatRequest{
			Prompt:    prompt,
			System:    formatSystemPrompt,
			Model:     spec.Model,
			Effort:    spec.Effort,
			RequestID: fmt.Sprintf("%s-fmt-%d", requestID, i),
		})
		if err != nil {
			// RULE-3: never the model output or the raw error text (Classify
			// can echo model text back); the item publishes on raw fields.
			slog.WarnContext(ctx, "generate: formatting call failed; item renders from raw fields",
				"feed_slug", feed.Slug, "reason", "format_call_failed")
			continue
		}
		tokensOut += estimateTokens(res.Raw)
		out[i].Formats = validateFormats(res.Formats, out[i])
	}
	return out, tokensIn, tokensOut
}

// validateFormats applies each variant's own rules and drops — never
// repairs — anything that breaks them. The rules mirror the raw fields'
// contract (contract.go) per surface:
//
//   - HTML variants are sanitized (same sanitizer as body/answer), and a
//     relative href drops the variant — RSS has no base URL (§5.1).
//   - Text variants must be plain: anything tag-shaped drops them (§5.5,
//     same rule as summary_text).
//   - For trivia, a text variant containing the answer drops — the §5.5
//     spoiler rule, checked exactly the way Validate checks summary_text.
//   - A variant that is byte-identical to the raw field it would replace is
//     dropped as carrying no information — the fallback renders the same
//     thing without storing a copy.
func validateFormats(f llm.ItemFormats, it model.Item) model.ItemFormats {
	answerText := strings.ToLower(plainText(it.AnswerHTML))

	cleanHTML := func(v string) string {
		v = strings.TrimSpace(v)
		if v == "" {
			return ""
		}
		// The link check runs on the RAW value, before sanitizing — the
		// same order contract.go checks body_html, and for the same reason:
		// the sanitizer strips a relative href rather than rejecting it, so
		// checking afterwards would let a variant whose links the model got
		// wrong ship as dead anchors instead of falling back to a body
		// whose links work.
		for _, m := range anchorHrefRe.FindAllStringSubmatch(v, -1) {
			if !isAbsoluteURL(firstNonEmpty(m[1], m[2], m[3])) {
				return ""
			}
		}
		return sanitize.HTML(v)
	}
	cleanText := func(v string, max int) string {
		v = strings.TrimSpace(v)
		if v == "" || htmlTagRe.MatchString(v) || !utf8ValidNoControls(v) {
			return ""
		}
		// The caps are asked for in the prompt and enforced loosely here
		// (1.5x) — a model a few words over produces a slightly long card,
		// not a broken one, but triple the cap means it ignored the brief
		// and the raw summary is the safer render.
		if max > 0 && len(v) > max*3/2 {
			return ""
		}
		if answerText != "" && strings.Contains(strings.ToLower(v), answerText) {
			return ""
		}
		return v
	}
	dropIfSame := func(v, raw string) string {
		if v == raw {
			return ""
		}
		return v
	}

	out := model.ItemFormats{
		FeedHTML:  cleanHTML(f.FeedHTML),
		PageHTML:  cleanHTML(f.PageHTML),
		CardText:  cleanText(f.CardText, 280),
		EmbedText: cleanText(f.EmbedText, 180),
	}
	if !utf8ValidNoControls(out.FeedHTML) {
		out.FeedHTML = ""
	}
	if !utf8ValidNoControls(out.PageHTML) {
		out.PageHTML = ""
	}
	out.FeedHTML = dropIfSame(out.FeedHTML, it.BodyHTML)
	out.PageHTML = dropIfSame(out.PageHTML, it.BodyHTML)
	out.CardText = dropIfSame(out.CardText, it.SummaryText)
	out.EmbedText = dropIfSame(out.EmbedText, it.SummaryText)
	return out
}
