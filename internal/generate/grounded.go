// grounded.go implements the grounded-feed-specific pieces of PLAN.md §9:
// rendering the fetched candidate set into the prompt, the ranking system
// prompt itself, source-failure degradation (§19/§20's "a dead source must
// degrade the feed, not break it"), and a plain-text excerpt helper. None of
// this replaces contract.go's Go-side re-validation (§9.6's link-integrity
// check is still the actual guarantee) — everything here is either prompt
// construction or pre-generation plumbing.
package generate

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/monstercameron/AnimeFeedFlux/internal/sources"
)

// defaultExcerptChars bounds each candidate's excerpt inside the rendered
// block. This is independent of summary_text's 500-char hard cap
// (contract.go) — it only keeps the prompt itself from ballooning to ~40
// full article descriptions.
const defaultExcerptChars = 220

// candidateDateFormat is used only for rendering a candidate's Published
// field into the block. It is deliberately NOT one of the RFC 822/RFC 3339
// formatters §5.1/§5.2 specify for feed output — those are for what we
// EMIT to subscribers; this is a human/model-readable date inside a prompt
// and has no spec obligation.
const candidateDateFormat = "2006-01-02"

// BuildCandidateBlock renders cands into the text handed to the model as
// {{.Candidates}} (PLAN.md §7): title, URL, published date, and a short
// excerpt, one candidate per entry, capped at max.
//
// Ordering is deterministic — newest Published first, then URL ascending as
// a stable tiebreak (including when Published is equal or zero on both
// sides) — so the same fetched candidate set always renders the same
// prompt text, and therefore the same prompt_hash (§7: "Rendered prompts
// are hashed and stored on each item"). Without a defined order, two runs
// over an identical candidate set could hash differently for no reason
// tied to content, which would make prompt_hash useless for tracing a
// quality regression back to "what changed."
//
// cands is not mutated: a copy is sorted, so the caller's slice (which may
// also be used to build Options.CandidateURLs, per runner.go's
// acquireContext) keeps its own order.
func BuildCandidateBlock(cands []sources.Candidate, max int) string {
	if len(cands) == 0 {
		return "(no candidate articles available)"
	}

	ordered := make([]sources.Candidate, len(cands))
	copy(ordered, cands)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, pj := ordered[i].Published, ordered[j].Published
		if !pi.Equal(pj) {
			return pi.After(pj) // newest first
		}
		return ordered[i].URL < ordered[j].URL
	})

	if max > 0 && len(ordered) > max {
		ordered = ordered[:max]
	}

	var b strings.Builder
	for i, c := range ordered {
		if i > 0 {
			b.WriteByte('\n')
		}
		published := "(undated)"
		if !c.Published.IsZero() {
			published = c.Published.Format(candidateDateFormat)
		}
		fmt.Fprintf(&b, "%d. Title: %s\n   URL: %s\n   Published: %s\n   Excerpt: %s\n",
			i+1, strings.TrimSpace(c.Title), strings.TrimSpace(c.URL), published,
			ExcerptOf(c.Excerpt, defaultExcerptChars))
	}
	return strings.TrimRight(b.String(), "\n")
}

// RankingSystemPrompt returns the system prompt for grounded (news) feeds.
//
// The URL instruction below ("use only URLs from the supplied list") is
// belt-and-braces, not the control. PLAN.md §9 step 6 is explicit that the
// actual guarantee is the Go-side byte-equality check in contract.go
// (CheckLink) run AFTER generation — every candidate's link is verified
// against the fetched set regardless of what the model does. This prompt
// text exists only to make compliance likely (fewer items get silently
// dropped by CheckLink, which is better for yield and cost) and must never
// be treated as a substitute for that check, or read as if prompting alone
// were the defense against a hallucinated URL.
func RankingSystemPrompt() string {
	return rankingSystemPrompt
}

const rankingSystemPrompt = `You are the editor for an anime news feed. You will be given a numbered
list of candidate articles, each with a title, URL, published date, and a
short excerpt, fetched moments ago from real news sources.

Your job:

1. Select and rank the candidates by newsworthiness to an anime-audience
   reader: major announcements, releases, and industry news rank above
   minor or speculative items. Do not invent items that are not in the
   candidate list.

2. For every item you select, WRITE A SUMMARY IN YOUR OWN WORDS. Never
   copy or closely paraphrase sentences from the excerpt or reproduce the
   source article's text. This feed summarizes and links to the original
   source; it does not republish it. Keep each summary short, factual, and
   clearly distinct in wording from the candidate excerpt it is based on.

3. The link you return for each item MUST be copied EXACTLY, character
   for character, from the URL field of the candidate list you were given.
   Never construct, guess, shorten, or modify a URL, and never return a
   URL that is not present in the candidate list above.

If the candidate list is empty or contains nothing newsworthy, say so
rather than inventing content to fill the response.`

// SourceResult is one upstream source's outcome for a single grounded run:
// either the candidates it produced, or the error that fetching/parsing it
// hit. It mirrors what internal/sources.Fetcher.FetchCandidates already
// returns per-URL, gathered here across all of a feed's sources so
// DegradeOnSourceFailure can reason about the whole set at once.
type SourceResult struct {
	URL        string
	Candidates []sources.Candidate
	Err        error
}

// DegradeOnSourceFailure implements PLAN.md §19/§20's grounded-feed
// resilience rule: "a source that changes format or dies must degrade the
// news feed, not break it."
//
// The instinct here is to fail fast — one source erroring looks like it
// should fail the run, the same way a malformed model response does. That
// instinct is wrong for this specific case: a source failure is not a
// generation defect, it is a fact about the world (a publisher reformatted
// their RSS, a domain is briefly down, a feed 404s after a redesign), and
// with 2-3 sources configured (§21.3), treating any one of them as fatal
// means a single publisher's outage takes the whole news feed offline even
// though the other sources are fine and have candidates ready to publish.
// So the rule is: proceed with whatever usable candidates exist, report
// which sources were dead so it shows up in the run log (§10
// reject_reasons_json-adjacent bookkeeping, surfaced by the caller), and
// reserve failure for the one case that is genuinely unrecoverable —
// EVERY configured source failed, leaving nothing to generate from at all.
func DegradeOnSourceFailure(results []SourceResult) (usable []sources.Candidate, degraded []string, err error) {
	if len(results) == 0 {
		return nil, nil, nil
	}

	failures := 0
	for _, r := range results {
		if r.Err != nil {
			failures++
			degraded = append(degraded, r.URL)
			continue
		}
		usable = append(usable, r.Candidates...)
	}

	if failures == len(results) {
		return nil, degraded, fmt.Errorf("generate: all %d source(s) failed: %s", failures, strings.Join(degraded, "; "))
	}
	return usable, degraded, nil
}

// ExcerptOf returns a clean, plain-text excerpt of s capped at max runes,
// breaking only at a word boundary (never mid-word) and stripping any HTML
// markup first.
//
// stripTagsRe and plainText are contract.go's tag-stripping helpers
// (defined for the answer-leak check); reused here rather than duplicated,
// since "strip markup for a plain-text comparison/display" is the same
// operation in both places and contract.go's doc comment already explains
// why it is deliberately crude (no entity decoding) rather than a real
// sanitizer.
func ExcerptOf(s string, max int) string {
	clean := strings.Join(strings.Fields(plainText(s)), " ")
	if max <= 0 {
		return ""
	}
	runes := []rune(clean)
	if len(runes) <= max {
		return clean
	}

	// Walk back from the cap to the nearest preceding space so the cut
	// never lands inside a word.
	end := max
	for end > 0 && !unicode.IsSpace(runes[end]) {
		end--
	}
	if end == 0 {
		// No whitespace anywhere within the cap — a single "word" (a long
		// URL-like token, CJK text with no ASCII spaces, etc.) alone
		// exceeds max. Extend forward to its end instead of emitting a
		// broken fragment: a caption a little longer than requested beats
		// one that stops mid-character-sequence.
		end = max
		for end < len(runes) && !unicode.IsSpace(runes[end]) {
			end++
		}
	}

	truncated := end < len(runes)
	out := strings.TrimRightFunc(string(runes[:end]), unicode.IsSpace)
	if truncated {
		out += "…"
	}
	return out
}

// --- Dedupe assessment (task A6-16) ---
//
// PLAN.md §8 rejects SchemaFlux's Deduplicate(items, threshold) for the
// generative novelty gate because it is O(n²) MODEL CALLS against a
// 500-item window, and asks whether it is worth it at the grounded set's
// much smaller n (~40, §9 step 1's cap) — semantically-duplicate articles
// from *different* publishers covering the same story, which URL-based
// sources.Dedupe (already used at fetch time, see fetch.go) cannot catch
// because the URLs genuinely differ.
//
// Assessment: not worth adding for A6, and the honest reasoning is cost
// against a benefit that is mostly already covered elsewhere.
//
//   - Cost, even at n=40: Deduplicate compares pairs, so a full pass is
//     40*39/2 = 780 model calls. That is per grounded run, and this feed
//     runs on a schedule (daily-ish per §7), not once — 780 extra billable
//     calls to save the model from occasionally ranking two wire-service
//     rewrites of the same announcement is a bad trade against §13's cost
//     model, and RANKING is already a single model call over the whole
//     candidate block built by BuildCandidateBlock above.
//   - The benefit is smaller than it first looks: RankingSystemPrompt
//     already asks the model to select and RANK by newsworthiness across
//     the whole visible candidate list in one pass — the model reads all
//     ~40 titles/excerpts together and is far better positioned to notice
//     "these two are the same story" *while ranking* than a separate
//     pairwise dedup pass run blind to ranking would be. A dedicated
//     dedupe step duplicates work the ranking call is already doing as a
//     side effect of seeing everything at once.
//   - A cheap embedding-based version (one embedding per candidate, O(n)
//     calls, then O(n²) dot products — the same pattern §8 already uses
//     for the 500-item novelty window) would be far cheaper than
//     Deduplicate's model-call version, and is the right shape IF this
//     ever needs solving. But there is no evidence yet that it needs
//     solving: cross-publisher near-duplicates at n=40 for a niche
//     (anime-news) source set are the exception, not the norm, and
//     building a dedup pass against a suspected problem rather than an
//     observed one is exactly the premature-complexity trap §8 already
//     called out once for the 500-item case.
//
// Revisit if a production audit (the same kind §19's definition-of-done
// audit already runs for hallucinated-URL checking) shows the ranked feed
// repeatedly publishing near-identical items from separate sources. Until
// then: URL-level sources.Dedupe at fetch time, plus the ranking prompt's
// single pass over the full visible set, is the mechanism.
