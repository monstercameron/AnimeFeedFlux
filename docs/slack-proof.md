# Slack proof — how to run the live check when a workspace exists

Companion to `TODOS.md` C3 and `PLAN.md` §5.5 ("Slack as a first-class consumer") and §17's J10
sanity list. **No real Slack workspace exists yet, so none of C3-01 through C3-11 is checked off by
this document** — it is the procedure for when one does, plus the mechanical proof this project can
already run without one. Everything below is UNVERIFIED against real Slack, marked as such, and
checked instead against `internal/feedvalidate`, `internal/render/slack_test.go`, and
`internal/render/permalink_test.go` as of 2026-08-10.

## Why this document exists instead of just doing C3 now

§5.5's whole premise is that Slack's RSS app **fails silently**: a violated rule does not error, log,
or notify anyone — an item simply never appears in the channel, forever, and nothing else in the
pipeline reports it. That means the live test's job is not "does it work" (a green run tells you
almost nothing on its own) — it's "would we notice if it *didn't*". This document exists so the
first live subscribe is a confirmation of rules already checked mechanically, not the first time any
of them is checked at all.

## Part 1 — What is mechanically enforced today (no Slack required)

`make validate` / `affvalidate` (`internal/feedvalidate`) is an offline, renderer-independent
re-check of already-rendered documents. `internal/render/slack_test.go` and
`internal/render/permalink_test.go` are renderer-level tests that additionally have an oracle for
"what is the trivia answer", which `feedvalidate` structurally cannot have (it only ever sees bytes
that already left the renderer). Between the two:

| §5.5 rule | Mechanically enforced today | Where |
|---|---|---|
| 1. Every item needs a supported date tag | Yes | `feedvalidate.RSS` (`§5.1 pubdate-rfc822`, missing/unparseable/zero pubDate all fail); `render.TestSlack_EveryItemHasParseableDate` |
| 2. Items strictly newest-first | Yes | `feedvalidate.RSS` (`§5.5 pubdate-strictly-descending-unique`); `render.TestSlack_PubDatesStrictlyDescendingAndUnique` |
| 3. No duplicate timestamps | Yes — same check as #2, since both are "not `pubDates[i-1].After(pubDates[i])`" | same as above. **Store-level enforcement** (`UNIQUE(feed_id, published_at)`, PLAN.md §5.5/§10) is out of this doc's editable scope (`internal/store`) but is the thing that's supposed to prevent this state from ever reaching the renderer in the first place |
| 4. Feed must validate | Yes, that's this gate's entire purpose | `affvalidate` exit code; CI wiring is `make validate`'s job, not `feedvalidate`'s |
| Never backdate relative to the feed's newest item | **No** — see Part 3 | n/a (needs cross-run/store state a single rendered document doesn't carry) |
| Editing does not repost | **No**, and can't be, at this layer | n/a (needs comparing two runs over time; a document-validator sees one snapshot) |
| Description is plain text | Yes | `feedvalidate.RSS` (`§5.5 description-plain-text`, raw-tag detection on undecoded XML bytes); `render.TestSlack_DescriptionIsPlainTextUnderCap` |
| Description never leaks the trivia answer | **Structurally, partially.** `feedvalidate` catches a raw-HTML-tag class of leak but has no oracle for "this specific substring is the answer" — the semantic leak check only exists where the answer is known, `render.TestSlack_DescriptionNeverLeaksAnswer` (uses a token literal, `ANSWER-COWBOY-BEBOP`) | `internal/render/slack_test.go`, `internal/render/rss.go`'s "SummaryText never contains AnswerHTML" construction guarantee |
| Link unfurling (OG tags on the permalink page) | Yes, added by this change | `feedvalidate.Permalink` (new) — checks `og:title`, `og:description`, `og:type=article`, `og:url` absolute, `article:published_time` RFC 3339, `og:description` plain text. Wired into `affvalidate` for `permalink_*.golden` and `.html`/`.htm` files, which were previously **skipped entirely** — the unfurl surface had zero mechanical coverage from `make validate` before this change |

Everything in the left column that isn't marked "No" is checked automatically on every `go test`
and every `make validate` run, against real rendered bytes, not against hand-reasoned assumptions
about what the renderer does.

### What `feedvalidate.Permalink` closes

Before this change, `cmd/affvalidate`'s golden dispatcher treated every `permalink_*.golden` file —
the one page Slack's unfurler actually fetches for a shared link — as "not a feed, skip it", the
same as the unrelated feed-index HTML page. That meant the entire OG/unfurl contract in §5.5 ("Link
unfurling") had no independent, renderer-blind re-check at all; only `internal/render`'s own unit
tests (which share code with the thing being tested) covered it. `feedvalidate.Permalink` now parses
the rendered `<meta>` tags directly and fails the gate if `og:title`, `og:description`, `og:type`,
`og:url`, or `article:published_time` is missing, if `og:type` isn't `"article"`, if `og:url`/
`og:image` aren't absolute URLs, if `article:published_time` doesn't parse as RFC 3339, or if
`og:description` contains a raw HTML tag. Verified against the real goldens
(`internal/render/testdata/golden/permalink_basic.golden`, `permalink_trivia.golden`) via
`go run ./cmd/affvalidate internal/render/testdata/golden/*.golden` — both report `ok`.

### The related, already-recorded finding this doc does not re-litigate

`docs/spoiler-design.md` records that the trivia answer's `<details>`/`<summary>` spoiler markup
does not survive a sanitizing full-content reader (verified against ArticleFlux) — it gets unwrapped
to plain visible text. Slack is not exposed to that defect **only because Slack never renders
`content:encoded` or the permalink `<body>` at all**; its unfurl reads exclusively the `<meta>` tags
this document's Part 1 table covers, and `og:description`/`description` are built from `SummaryText`
only, which never contains the answer. That is a real interaction between §5.5 and the spoiler
defect — the two are masking each other, not independent — but the resolution belongs to
`docs/spoiler-design.md`, not here.

## Part 2 — The live proof procedure (UNVERIFIED — no workspace exists)

Run this once a staging host is reachable over public HTTPS (`C2` in `TODOS.md` — DNS, TLS, and a
publicly fetchable feed are prerequisites C3 cannot substitute for) and a private Slack workspace or
channel exists (`C3-01`).

### Setup

1. **C3-01** — Create a private Slack workspace or a private channel in an existing one, used for
   nothing else. Keeping it dedicated matters: a channel with other traffic makes "did an item post"
   a matter of scrolling back through noise instead of an unambiguous count.
2. **C3-02** — In that channel: `/feed subscribe https://staging.anime.earlcameron.com/feeds/<slug>.xml`
   (RSS; Slack's own help page names RSS as what it supports — Atom/JSON Feed are for other readers,
   not Slack). Confirm Slack's own acknowledgment message appears before doing anything else; if it
   doesn't, nothing past this point can be attributed to the feed rather than the subscribe step.
3. Note the exact wall-clock time of subscribe. Every timing observation below is relative to it.

### C3-03 — Confirm a generated item posts at all

Trigger one run against the staging feed (`aff run` or the scheduled cron, whichever is live).
Watch the channel. Record: time posted vs. time of run completion (this is the first data point for
**C3-11**, the poll interval).

**How it would silently fail:** the run completes, the item is in the database and in the rendered
XML (confirm independently via `curl` + `affvalidate` on the live URL, per `C2-08`), but nothing
posts, ever. There is no error anywhere — check the item's `pubDate` against every rule in Part 1's
table by hand if this happens, since that's the actual failure surface.

### C3-04 — Confirm multiple items from one run all post (the duplicate-timestamp trap)

Trigger a run that produces 2+ items in one pass — a grounded or aggregate recipe is the sharpest
test, since those are exactly the shapes PLAN.md §5.5 calls out as naturally producing identical
timestamps before the +1-second store discipline was added. Count Slack messages against count of
items actually published (`aff item` / the admin UI's run detail).

**How it would silently fail:** N items generate, N items appear in the rendered feed and pass
`affvalidate`, but fewer than N post to Slack. This is the one failure mode that a clean
`affvalidate` run does **not** rule out on its own — `affvalidate` proves the pubDates are distinct
and ordered in the document Slack fetches, but only a real subscribe proves Slack's bookmark
actually advanced past every one of them rather than, say, coalescing near-simultaneous items in its
own poll batching. Treat a mismatch here as a live discovery, not a `feedvalidate` gap, unless
`affvalidate` also failed on the same document (in which case it was already caught pre-publish and
this run should not have gone out).

### C3-05 — Confirm no item posts twice across a week of polls

Let the subscription run for at least a week without manual intervention. Record every message
timestamp against every item's `published_at`. At the end of the week: message count must equal
item count published in that window, exactly — not more, not fewer.

**How it would silently fail two different ways, and they look identical from the channel:**
- **Under-count** (an item never posts) — the backdate/duplicate-timestamp failure modes above,
  caught by watching for it. The channel just looks a little quiet; nothing flags a gap.
- **Over-count** (an item posts twice) — would mean Slack's bookmark somehow regressed, or a
  correction (`C3-09`) was mistaken for a fresh post of the original. Cross-reference every
  duplicate-looking message's permalink URL against the item table; two messages linking to the
  *same* `item_key` is the tell.

Either way, the channel itself gives no signal that something is wrong — only a count reconciled
against the database does.

### C3-06 — Confirm the unfurl renders title, summary, and image from the OG tags

For at least one item of each kind the feed produces (generative, grounded, trivia if applicable),
visually confirm the Slack card shows: title (matches `og:title`), the plain-text summary (matches
`og:description`), and an image (matches `og:image`, if the feed sets one).

**How it would silently fail:** the message posts (so C3-03 looks fine), but the card is a bare link
with no title/summary/image — this happens if `og:*` tags are missing or malformed on the permalink
page, which is exactly what `feedvalidate.Permalink` (Part 1) now checks pre-publish. If this fails
live despite `affvalidate` passing, the gap is almost certainly Slack-specific unfurl caching
(Slack caches unfurls per-URL for a period; re-share the exact URL, or check Slack's own unfurl
debug tools) rather than a rendering defect — but confirm the permalink URL passes `affvalidate`
standalone before assuming that.

### C3-07 — Confirm a trivia answer is not visible in the channel

For a trivia item: read the channel message (both the description snippet and any card text) and
confirm the answer string never appears, only the question.

**How it would silently fail:** this is the one C3 item where "it works" is not enough confirmation
by itself — see `docs/spoiler-design.md`. Even if the answer never appears in Slack (expected,
because Slack never renders `content:encoded` or the permalink body), that does **not** mean the
answer is hidden anywhere else; it only means Slack specifically can't expose the defect. Do not
read a clean C3-07 as "the spoiler problem is solved" — cross-check against
`docs/spoiler-design.md`'s open decision instead.

### C3-08 / C3-09 — Editing does not repost; a correction does appear

Edit an already-published item's title/body directly (same `guid`, same `published_at`) and confirm
nothing posts. Separately, publish a correction as a **new** item with a new, later `published_at`
and confirm it posts as a distinct message.

**How C3-08 would silently fail:** an edit *does* repost — this would mean Slack is keying on
content rather than the bookmark date, contradicting §5.5's documented model; if seen, treat it as
new information about Slack's actual behavior, not a bug in this feed, since the render layer has no
mechanism to "resend" anything.

**How C3-09 would silently fail:** the correction never posts because it accidentally shares (or is
earlier than) the original item's `published_at` — this is the same backdate/duplicate-timestamp
class as C3-04/C3-10, just triggered by the correction workflow specifically. Confirm the
correction's rendered `pubDate` independently before assuming Slack dropped it.

### C3-10 — Confirm a backdated item never appears, then stop creating them

Deliberately create one item stamped at or before the feed's current newest `published_at` (the
admin UI blocks this by default per §5.5 — use whatever override path exists, since this is the one
test in this list that requires *disabling* a safeguard on purpose). Confirm it never posts, across
several poll cycles, then delete the override path's output/never repeat this in production.

**How it would silently fail — the whole point of this test:** there is no failure signal to look
for other than absence. The bookmark model means Slack simply never asks for anything at or before a
date it has already advanced past; there's no rejection, no error, no partial delivery. The only way
to "catch" this is a negative: watch the channel for the number of poll cycles established during
C3-11 and confirm the message count did **not** increase. A single missed check here just looks like
a normal cycle where the feed happened not to have news — which is exactly what makes this the
sharpest of the eleven C3 items and the reason §5.5 calls the bookmark model out by name.

### C3-11 — Record the observed Slack poll interval

From the timestamps gathered in C3-03/C3-04/C3-05, compute: median and max delay between an item's
`published_at` and its Slack post time. Record both here (append below) and feed the max into the
`§15` staleness-grace-factor config once it exists — a staleness alert firing because Slack simply
hasn't polled yet, rather than because the feed is actually stuck, is a false alarm this number is
meant to prevent.

**Observed poll interval: not yet recorded — no live subscription exists.**

## Part 3 — What only the live proof can establish

Two §5.5 requirements are outside what any document-validator (offline or renderer-level) can check,
by construction, and stay open until C3 actually runs:

1. **Never-backdate, enforced across runs.** `feedvalidate` and `render`'s tests validate ordering
   *within one rendered document*. Whether a newly published item is backdated relative to the feed's
   **already-delivered** newest item is a property of the store's `UNIQUE(feed_id, published_at)`
   constraint and the admin UI's backdate guard (`internal/store`, `internal/publish` — outside this
   change's editable scope) plus, ultimately, Slack's actual bookmark behavior. C3-10 is the only
   place this gets checked end to end.
2. **Editing truly never reposts, on Slack's actual bookmark implementation.** This project's render
   layer guarantees `guid`/`pubDate` are unchanged on an edit (a property `internal/render`'s tests
   can and do check), but whether Slack's RSS app keys purely off `pubDate` the way §5.5 documents,
   versus some content hash or ETag Slack doesn't publish, is an assumption about a third party's
   undocumented internals. C3-08 is the only place this gets checked at all.

Both are exactly the reason C3 cannot be skipped even though every other §5.5 rule now has
pre-publish mechanical coverage: they are properties of Slack's behavior, not of this codebase's
output, and no amount of local testing substitutes for observing them.
