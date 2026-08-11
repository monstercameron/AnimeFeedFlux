# Trivia spoiler hiding — verified defect and open options

Status: **open decision**. §5.5 states the constraint; this document is the evidence and the
options. **No option below has been formally adopted as the design decision** — but see the
2026-08-10 update immediately below: code now exists, and it does not implement any single option
from this document coherently. Read that update before relying on anything else here being current.

## 2026-08-10 update: code now exists, and disagrees with itself

This document's "What was NOT changed" section (bottom) originally said "No Go code was touched
(none exists to touch, per `CLAUDE.md`)." That is no longer true — the repo has since built the
generation and rendering pipeline — and this section records what was found on an audit pass through
`internal/render`, so a future reader does not trust the stale claim below it.

**Three places now disagree with each other, and none went through the decision this document exists
to support:**

1. **`internal/render/rss.go`'s `itemBodyWithAnswer`** — what RSS `content:encoded`, Atom `content`,
   and JSON Feed `content_html` actually ship (all three renderers call this one helper, per its own
   doc comment) — appends `<hr class="spoiler-break"/><p><strong>Answer:</strong> ...</p>` after the
   body. This is closest to **Option 1** below (whitespace/scroll distance), which this document's
   own table rates "weak guarantee."
2. **`internal/render/permalink.go`'s `Permalink`** — a *different* surface, the `/items/{item_key}`
   page — wraps the answer in `<details><summary>Reveal the answer</summary>...</details>`. This is
   not one of the four options below at all. It is also, specifically, the exact tag pair this
   document's own evidence (§ "The finding," directly below) proves is silently unwrapped by
   ArticleFlux's sanitizer, leaving the answer as plain visible text with no toggle — the same defect
   class this whole document exists to warn against, now shipped on a second surface without anyone
   checking it against the evidence already gathered here.
3. **This document** still says no option has been adopted.

None of the three is "wrong" in isolation — a `<details>` element genuinely does hide content from
an unfurler that only reads `<meta>` tags (permalink.go's own comment makes exactly that narrower,
accurate claim) — but the combination means there is no single, deliberate, cross-checked answer to
"how is the trivia answer hidden," just two different implementations that happened to get written
for two different renderers, one of which reuses a mechanism already shown broken elsewhere in this
same file. See TODOS.md `A2-16` for the task-level note. Reported for a decision, not fixed here.

## The finding

PLAN.md originally specified (§5.5) that a trivia item's answer goes into `content:encoded`
"behind a spoiler break," on the assumption that some HTML disclosure mechanism (e.g.
`<details>`/`<summary>`) would keep the answer hidden from a reader until they chose to reveal it.

That assumption does not survive a real full-content reader. Verified by reading source, not by
assuming the technique works:

- ArticleFlux ingests `content:encoded` into `items.content_html`
  (`ArticleFlux/internal/store/ingest.go`, `IngestItem.ContentHTML`; confirmed the RSS parser maps
  `content:encoded` to `ContentHTML` — `ArticleFlux/internal/feed/feed_test.go:26,75`, "content:encoded
  is the article; description is the summary").
- The reading pane renders that field through `html.RawHTML`
  (`ArticleFlux/client/view/panes.go`, `parsedBody`/`articleBody`, ~line 4061-4067:
  `sanitize.Sanitize(raw)` to check emptiness, then `html.RawHTML(raw)` to render). The doc comment
  there is explicit that `RawHTML sanitises and then rebuilds the markup` — sanitizing is not
  optional or skippable on this path.
- The sanitizer is `GoWebComponents/v5/sanitize` (`GoWebComponents/sanitize/sanitize.go`).
  `DefaultPolicy()` (lines 34-54) allows `a, p, div, span, b, strong, i, em, u, code, pre,
  blockquote, ul, ol, li, h1-h6, br, hr, table, thead, tbody, tr, td, th, img, figure, figcaption`.
  **Neither `details` nor `summary` is in that list.**
- `writeAttr` (lines 178-225) strips the `style` attribute **unconditionally, regardless of
  policy** ("Strip the style attribute regardless of policy — it's a script vector.") — so a
  CSS-only hide (`display:none` / a "spoiler" class relying on inline style) is stripped too, not
  just the semantic tag.
- Critically, `walk` (lines 125-173) does not drop disallowed tags — it **unwraps** them: "Disallowed
  non-drop tag: unwrap — keep sanitized children, lose the tag" (line 161). Only a fixed set —
  `script, style, iframe, object, embed, form, svg, math, link, meta, base, template`
  (`dropWithContents`, lines 58-68) — has its subtree removed entirely. `details`/`summary` are not
  in that set either.

Net effect: `<details><summary>Reveal</summary>THE ANSWER</details>` sent through this sanitizer
becomes plain `THE ANSWER` text, inline, with no toggle and no way to re-hide it. The disclosure
markup is gone; the text it was protecting is not.

**Why this was masked, not absent.** PLAN.md §5.5 already documents that Slack "does not render
rich HTML" and shows only `description` plus a link/OG unfurl — Slack never renders
`content:encoded` at all, spoiler markup or otherwise. So the one consumer this project explicitly
tested against (§5.5 "Practical check") cannot reveal the bug: the answer never reaches Slack's
render path in the first place, because it was never supposed to be there. The failure only shows
up in a consumer that renders the full article body — which ArticleFlux does, and which is the
second consumer this plan names by name (§1: "ArticleFlux, which becomes a front end for free").

Also worth recording: AnimeFeedFlux's own outbound allowlist (PLAN.md §9, `body_html` schema field)
is `p, em, strong, a, ul, li, blockquote, code` — narrower than ArticleFlux's, and also without
`details`/`summary`. Nothing in this repository's own generation pipeline has ever emitted or
sanitized a `<details>` tag; the "spoiler break" mechanism was named in prose but never specified
down to a concrete tag or verified against any renderer. There is no code in this repository today
(per `CLAUDE.md`: "this repository is currently a specification ... with no code").

## Options

None of these is selected. Each is described by actual, verified behavior where it could be
verified, not by how the technique is supposed to work.

### 1. Whitespace / scroll distance before the answer

Push the answer far enough down the item (blank paragraphs, a horizontal rule, "scroll for the
answer") that it is not on screen with the question.

- **Slack**: irrelevant — Slack never renders `content:encoded`, only `description` (§5.5). No
  change from status quo.
- **ArticleFlux**: survives the sanitizer completely — it's just `p`/`br`/`hr`, all allowed tags,
  nothing to unwrap. But `client/view/panes.go`'s `articleBody` renders the whole sanitized body
  into one `article-body` div in the reading pane; nothing was found that lazy-loads or paginates
  within an item. A reading pane that shows the full item (which is what "full-content reader"
  means) shows the answer in the same scroll, just further down. Confirmed defeated by design, not
  by an edge case.
- **Generic reader**: same logic — any client that renders `content_html` as one block defeats
  this. Only a client that literally paginates or truncates within an item would preserve the
  hiding, and that behavior can't be assumed.
- **Cost**: cheapest to build, weakest guarantee. Reliably hides from nothing except a glance at
  the very top of the render.

### 2. The answer as a separate, later item, linked to the question

Publish the question as item N; publish the answer as its own item N+1 (e.g. an hour or a day
later), each linking to the other.

- **Slack**: works as intended — the answer posts as a distinct message at its own time, so the
  channel literally shows the question, then later, the answer.
- **ArticleFlux**: works — it's two ordinary items, no sanitizer interaction at all.
- **Generic reader**: works, for the same reason.
- **Cost**: this is a real model change, not a rendering trick, and it interacts with rules already
  in this plan:
  - §5.5 "**Never backdate**" and the strictly-increasing, never-earlier-than-current-newest
    `published_at` rule — the answer item must be a genuine new item stamped later than the
    question, which is compatible with the rule but means the answer cannot be pre-generated and
    silently attached; it has to be scheduled and published as its own event.
  - **Doubles item volume** per trivia recipe (every question becomes two items in the feed
    window, §5.4's window cap and §7's "feed window" bookkeping now count two items per question).
  - Interacts with dedup/novelty (§9.5): the answer item's text is derived from the question and
    would need to be excluded from, or handled specially by, the novelty embedding comparison so it
    isn't flagged as near-duplicate of the question it belongs to.
  - Slack's per-item duplicate-`pubDate` rule (§5.5 point 3) already requires distinct increasing
    timestamps across a run, so a same-run question+answer pair needs deliberate spacing, not just
    +1 second, so it doesn't parse as accidental.

### 3. Answer only behind the permalink; the feed carries the question and a link

`content:encoded` (and `description`) carry the question and a "reveal the answer" link to the
permalink page; the permalink page alone carries the answer.

- **Slack**: `description` already is question-only per current §5.5 text, and Slack's unfurl of
  `link` already goes to the permalink — so this is close to the currently-intended Slack behavior
  regardless of what happens with `content:encoded`. Strongest hiding of the three for Slack,
  because Slack was never going to show the answer anyway.
- **ArticleFlux**: also strong — nothing sanitizer-related to defeat, because the answer text is
  simply never sent in the feed payload at all. The reader has to leave the app (or ArticleFlux
  would need its own "open permalink" affordance) to see it.
  - Confirmed cost, not assumed: `ArticleFlux/client/view/panes.go` is a full reading-pane app —
    the whole point of a front-content reader is to avoid clicking out to the source. Sending
    readers to an external permalink for the answer works against that app's own design goal, which
    is why the task description calls this "worse for Slack where the unfurl is the whole
    experience" — it is also a step down for ArticleFlux specifically, which exists to keep the
    reader inside the app.
- **Generic reader**: same as ArticleFlux — strongest hiding, but requires a click-through, and
  some generic readers (and Slack's own unfurl) may not make that click obviously worth taking
  without seeing "there's an answer here" context.
- **Cost**: no item-model or dedup changes (unlike option 2); the cost is entirely UX — every
  answer requires leaving the feed reader.

### 4. Accept it is visible in full-content readers; design the item to read acceptably that way

Stop pretending the answer is hidden in `content:encoded`. Put the question first, then a visual
break (e.g. "⸻ Answer ⸻"), then the answer — written so that a reader who sees both at once still
gets a "trivia item" experience (question posed, answer given), rather than treating the exposure
as a bug to route around.

- **Slack**: unaffected — still governed entirely by `description`, which stays question-only per
  existing §5.5 text. No behavior change for Slack either way.
- **ArticleFlux**: the answer is visible immediately in the reading pane, same as today's broken
  assumption — the difference is this option makes that the accepted, documented behavior rather
  than an accidental one, so nothing in the item is a broken disclosure widget.
- **Generic reader**: same — visible immediately, by design rather than by defect.
- **Cost**: zero engineering cost beyond wording; it concedes the actual product goal ("the answer
  is not visible until the reader chooses," per the task framing) for every full-content reader.
  Whether that's acceptable is a product call, not a technical one — recorded here, not decided.

## Summary table

| Option | Slack | ArticleFlux | Generic full-content reader | Structural cost |
|---|---|---|---|---|
| 1. Scroll distance | N/A (never sees `content:encoded`) | Defeated — whole item renders as one block | Defeated, same reason | Low; weak guarantee |
| 2. Separate later item | Works | Works | Works | High — item model, `published_at`/dedup/window interactions |
| 3. Permalink-only answer | Strong (already closest to today's design) | Strong, but works against the app's front-end-reader goal | Strong, but requires click-through | Medium — no schema change, but a UX regression for in-app readers |
| 4. Accept visibility | Unaffected | Answer visible by design, not by defect | Answer visible by design | Low — but concedes the spoiler goal for full-content readers |

## What was NOT changed

**As originally written (superseded — see the 2026-08-10 update at the top of this document):** no
Go code was touched (none exists to touch, per `CLAUDE.md`). No option above has been selected.
PLAN.md §5.5 now records the constraint and points here rather than silently keeping the original
`<details>`-based assumption.

**Current status, 2026-08-10:** Go code now exists and implements two different, uncoordinated
mechanisms (see the top of this document) — but neither was arrived at by selecting one of the four
options above, and no option above is formally adopted as of this writing. This audit pass corrected
the document to match what the code actually does; it did not decide the open question, which is
still Cam's call per the framing already in this document and in PLAN.md §5.5/§7.
