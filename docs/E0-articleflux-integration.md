# E0: ArticleFlux integration

`PLAN.md` §18 phase E: ArticleFlux — Cam's own multi-tenant RSS reader, a separate project at
`C:\Users\mreca\Desktop\ArticleFlux` — subscribes to an AnimeFeedFlux feed. It is the first real
consumer that is not Slack, and the first where Cam controls both ends. This doc works out what
AnimeFeedFlux must expose, where the two systems' assumptions actually disagree, and the concrete
test plan for `E0-01`/`E0-02`.

**Status as of this writing: still nothing production to subscribe to.** `TODOS.md` shows C1 (CI/CD
to GHCR) not started, C2 (staging DNS/vhost) not started, C5 (production DNS/vhost) not started.
E0-01's own text says "subscribe ArticleFlux to the production feeds," so the actual subscribe/verify
run still targets `anime.earlcameron.com` (`PLAN.md` §2 table, line 32) — reachable only after C5.
E0-01/E0-02 remain unticked.

**Update (2026-08-10): the three seeded feeds are live on the dev publish plane**
(`http://127.0.0.1:8081`) — `daily-anime-trivia`, `weekly-anime-news`, `character-spotlight-weekly`,
each in `.xml` (RSS 2.0), `.atom`, and `.json` (JSON Feed 1.1). This is real, fetched output, not the
hypothetical bytes the rest of this doc originally reasoned about — §3 below has been rewritten from
INFERRED to CONFIRMED against it, and it overturned the original guess. Everything else (parser
behavior, dedup keying, conditional GET) was reasoned from ArticleFlux's source and is unchanged;
those claims still aren't runtime-verified because there's still no ArticleFlux subscription to run
against a live source — dev-plane bytes prove what AnimeFeedFlux *serves*, not what ArticleFlux does
with it once ingested. That gap closes only once C1/C2/C5 land and E0-01/E0-02 actually execute.

Every claim about ArticleFlux below cites the file read. Where inferred rather than verified against
ArticleFlux's actual runtime behavior, it's flagged **INFERRED**; where checked against the live dev
feeds' actual bytes, it's flagged **CONFIRMED**.

## 1. What ArticleFlux actually does with a feed

Read from `ArticleFlux/internal/feed/feed.go`, `ArticleFlux/internal/store/ingest.go`, and
`ArticleFlux/internal/reader/service.go`.

- **Parser**: `gofeed` v1.4.0 (`go.mod` line 11), which handles RSS 0.9x–2.0, RDF, Atom 0.3/1.0, and
  JSON Feed (`feed.go` lines 1–8). All three of AnimeFeedFlux's formats (`PLAN.md` §5.1–§5.3) are
  accepted; any one is a valid subscribe target.
- **guid extraction**: `gofeed/translator.go` line 147–148 sets `item.GUID = rssItem.GUID.Value`
  directly from the RSS `<guid>` element's text — it does **not** consult `isPermaLink`
  (`gofeed/rss/parser.go` lines 437–444 parses the attribute but the RSS→common translator never
  reads it). So AnimeFeedFlux's `isPermaLink="false"` is spec-correct but inert as far as ArticleFlux
  is concerned; only the guid *text* — the Tag URI — matters.
- **content:encoded vs description**: `gofeed/rss/parser.go` line 241–246 maps `<content:encoded>`
  to `item.Content`; line 239–240 maps `<description>` to `item.Description`, confirmed by
  `gofeed/translator.go`'s `translateItemDescription` (lines 288–303, description first, falls back
  to `dc:description`/`itunes:summary`). In `ArticleFlux/internal/feed/feed.go`'s `normalizeItem`
  (lines 257–332): `content := firstNonEmpty(it.Content, it.Description)` becomes `ContentHTML`, and
  `summarySrc := firstNonEmpty(it.Description, it.Content)` (stripped, truncated to 280 chars, see
  `summarizeText`/`truncate`, lines 391–428) becomes `Summary`. This lines up with AnimeFeedFlux's
  own split in `PLAN.md` §5.5: `description` is the short plain-text field, `content:encoded` the
  full HTML body — ArticleFlux's `Summary` will be built from our `description`, its `ContentHTML`
  from our `content:encoded`, exactly as intended.
- **Identity/dedup key**: `stableGUID` (`feed.go` lines 341–353) prefers the feed's own `<guid>`,
  falling back to link, then a content hash — but AnimeFeedFlux always emits a `<guid>`
  (`PLAN.md` §5.1), so the fallback rungs never engage for us.
- **Conditional GET**: honoured. `Fetch` (`feed.go` lines 148–205) sets `If-None-Match` /
  `If-Modified-Since` from the source's stored `ETag`/`LastModified` and returns
  `Parsed{NotModified: true}` on a `304` (lines 181–183). `pollOneWithParsed`
  (`ArticleFlux/internal/reader/service.go` lines 466–524) passes `src.ETag`/`src.LastModified` in
  (line 479) and, on `304`, records the fetch outcome and ingests nothing (lines 487–492) — matches
  AnimeFeedFlux's §5.4 mandate exactly.
- **Poll cadence**: default `fetch_interval_s` is 1800s / 30 min (`ingest.go` line 284 comment,
  `min` enforced at 300s / 5 min — `repo.go` line 2377). Comfortably above AnimeFeedFlux's `ttl=15`
  and `max-age=900` (`PLAN.md` §5.4), and conditional GET means an unchanged feed costs one `304`
  regardless of interval — no rate-limit risk even if a user sets the 5-minute floor.
- **Date handling**: `it.PublishedParsed`/`it.Published` from `gofeed`, run through
  `timeutil.ClampPublished` (`ArticleFlux/internal/timeutil/timeutil.go` lines 36–60): a claimed date
  is trusted unless it's zero, before 1990, or more than 24h after first-seen, in which case
  first-seen wins. AnimeFeedFlux's RFC 822 dates (§5.1) are always real, current-ish timestamps, so
  this never engages — **not expected to matter, not separately verified against a live feed.**

## 2. Edit handling and dedup — the two systems actually agree

The brief for this doc assumed ArticleFlux might dedup on link or a content hash instead of guid,
which would make an edited item duplicate or silently fail to update. That is **not** what the code
does, and this is the first finding worth stating plainly because it contradicts the starting
assumption:

`ArticleFlux/internal/store/ingest.go`, `IngestItems` (lines 57–185): the existing-row lookup is

```sql
SELECT id, coalesce(content_hash,''), title, coalesce(summary,''), coalesce(content_html,'')
  FROM items WHERE source_id = ? AND guid = ?
```

(lines 68–71) — keyed on `(source_id, guid)`, nothing else. When AnimeFeedFlux re-serves an edited
item under the same guid (opaque ULID, never changes per `PLAN.md` §5.1), ArticleFlux finds the
existing row, computes `contentHash(title, summary, content_html)` (line 133), and:

- if the hash changed: archives the pre-edit version into `item_revisions` (lines 117–124, 140–145,
  `INSERT OR IGNORE` against `(item_id, content_hash)` so a reverted edit doesn't fabricate a second
  "change"), updates the row in place, bumps `revision` and stamps `edited_at` (lines 146–157),
  counts it in `res.Edited`;
- if the hash is unchanged: just counts it in `res.Updated` and touches nothing else;
- **`published_at` is never touched by an update** (comment at line 90–92, confirmed by the `UPDATE`
  statement at lines 93–101 which has no `published_at` column) — matching AnimeFeedFlux's own rule
  that editing must not resurrect an item's position (`PLAN.md` §5.5, "editing does not repost").

So: one guid in, one row in ArticleFlux, forever, with a visible edit history. **No disagreement on
guid-keyed dedup or on edit-does-not-repost — the two systems were built around the same
assumption**, and this holds across all three formats: Atom's `id` is read the same way
(`gofeed/translator.go` line 618, `GUID: entry.ID`) and JSON Feed's `id` likewise (line 780), and
AnimeFeedFlux emits the byte-identical Tag URI in all three (`PLAN.md` §5.2). Subscribing to the
`.xml` vs `.atom` vs `.json` variant of the same feed does not change dedup identity.

One second-order note, not a defect: `ArticleFlux/internal/urlnorm/urlnorm.go`'s `NaturalKey`
(`feed.go` line 501–503, `"feed:" + urlnorm.DupeKey(feedURL)`) is how ArticleFlux dedups the
*source* row itself, keyed on a normalized feed URL — so subscribing the same feed via two
differently-styled URLs (e.g. with a stray tracking parameter) converges on one polled source. Not a
concern for our slug URLs (`/feeds/{slug}.xml`, no query string), noted only because it's a different
key from item guid and it would be a mistake to conflate the two.

## 3. Where the two systems actually disagree: the trivia spoiler is not honoured

This is the real finding, and it is now **CONFIRMED against a real seeded item's actual bytes**, not
the earlier guess. `PLAN.md` §5.5 describes the trivia design: `description` carries the question
only, and "the answer lives in `content:encoded` behind a spoiler break and on the permalink page,
with the link reading as 'reveal the answer.'" The original version of this doc **inferred** the
spoiler break might be a `<details>`/`<summary>` disclosure, since the renderer that would decide
this didn't exist yet. It doesn't — but the actual seed content does, and it settles the question:

`daily-anime-trivia.xml`, every item's `<content:encoded>` (e.g. guid
`.../01KZKATED0QNBERH2EF6B719Y8`, fetched from `http://127.0.0.1:8081/feeds/daily-anime-trivia.xml`):

```html
<p>In the fictional series used for this seed item, what color is Yui Fictional's signature scarf?</p>
<p><em>This item is placeholder seed data generated by cmd/affseed for local development...</em></p>
<hr class="spoiler-break"/>
<p><strong>Answer:</strong> <p>Violet. (This answer is invented for seed data and has no real-world source.)</p></p>
```

So the real mechanism is **not a disclosure tag at all** — it's a bare `<hr class="spoiler-break"/>`
marker, presumably meant to be a hook for a stylesheet the permalink page supplies (hide/reveal
everything after that rule via CSS + JS), with the answer sitting in plain, unwrapped `<p>`/`<strong>`
markup right after it. This is a **stronger** version of the finding than the original guess: there
is no tag for ArticleFlux's sanitizer to unwrap — `hr`, `p`, and `strong` are all in `DefaultPolicy`'s
allowlist (§ below), so **every byte of the answer passes straight through unmodified**, not merely
"unwrapped" from a stripped container. It was never gated for a full-content reader to begin with;
only a CSS rule scoped to AnimeFeedFlux's own permalink page implements the hiding, and that CSS
travels with neither the RSS payload nor ArticleFlux's client.

One separate, smaller thing this same snippet surfaces: `<p><strong>Answer:</strong> <p>Violet...
</p></p>` nests a `<p>` inside a `<p>`, which is invalid HTML (block content inside a `<p>` auto-closes
the outer one per the HTML5 parsing algorithm — browsers will silently fix it, but it's not
well-formed markup as authored). Not in scope to fix here (that's `cmd/affseed`/generation code, and
this doc may only touch itself and `TODOS.md`), just flagged since it was sitting right there in the
bytes being cited.

**A second, more actionable finding: the three formats do not agree with each other on this content.**
Checked by grepping "Answer" and "spoiler-break" across all 19 seeded trivia items in each format:

| Format | `<hr class="spoiler-break">` present | Answer text present |
|---|---|---|
| `.xml` (RSS, `content:encoded`) | yes, all 19 items | yes, all 19 items |
| `.atom` (`<content type="html">`) | **no, 0 of 19** | yes, all 19 items |
| `.json` (JSON Feed, `content_html`) | no (tag never existed there) | **no, 0 of 19 items** |

The Atom variant drops the `<hr class="spoiler-break"/>` marker somewhere in generation (so even the
hook a future stylesheet could target is gone) but still carries the answer text plainly in the
following `<p>`. The JSON Feed variant's `content_html` is generated short — it stops after the
"placeholder seed data" disclaimer paragraph and never includes the spoiler-break rule or the answer
at all. **This means the JSON Feed variant is, right now, with zero code changes on either side, the
one subscription URL that does not spoil trivia in ArticleFlux** — not because JSON Feed sanitizes
anything, but because AnimeFeedFlux's own JSON renderer doesn't emit the answer into `content_html`
in the first place. Worth stating in the subscribe procedure below as a concrete, immediately usable
mitigation, distinct from "fix it properly later in A4/A9."

ArticleFlux's reading pane renders `content:encoded`/`content_html` directly and in full regardless of
format:

- Server side, `ContentHtml` is handed to the RPC client **unsanitized** at this layer —
  `ArticleFlux/internal/transport/grpcsrv/reader.go` `toPBItem` (lines 803–818) does
  `out.ContentHtml = it.ContentHTML` with no `sanitize.HTML` call, and RSS-ingested content never
  passes through `internal/sanitize`'s `Feed` policy on the way in either (that policy is only
  invoked for scraped content — `ArticleFlux/internal/scrapesel/scrapesel.go` line 259 — and for
  outbound feed re-export — `ArticleFlux/internal/app/share.go` line 236 — not for RSS ingest, per a
  full grep of `sanitize.HTML(` call sites).
- Client side, the reading pane (`ArticleFlux/client/view/panes.go`) calls `html.RawHTML(raw)`
  (line 4064, inside `parsedBody`) on that content, which sanitizes under
  `GoWebComponents/v5/sanitize`'s **`DefaultPolicy`** (`GoWebComponents/html/raw_html.go` line 32 →
  `sanitize.Sanitize` → `DefaultPolicy()`, `GoWebComponents/sanitize/sanitize.go` lines 32–54).
  `DefaultPolicy`'s `AllowedTags` has no `details` or `summary` (lines 36–45), and its walk
  (`sanitize.go` lines 125–173) **unwraps** any disallowed, non-drop tag — keeps the sanitized
  children, discards the tag itself (line 161, comment: "keep sanitized children, lose the tag"). The
  `style` attribute is stripped unconditionally regardless of policy (lines 186–189,
  "regardless of policy — it's a script vector").

Concretely, now CONFIRMED rather than hypothesized: the real markup is `<hr class="spoiler-break"/>`
followed by plain `<p><strong>Answer:</strong> …</p>`, all tags allowed, nothing to unwrap. **A
trivia item opened in ArticleFlux via `.xml` or `.atom` shows the answer immediately, next to the
question, the first time the item is opened — the "reveal the answer" gate that `PLAN.md` §5.5
designed for Slack's lack of a spoiler mechanism does not survive into a full-content reader.** The
`.json` variant is the one exception, and only incidentally (§3 table above) — its `content_html`
never includes the answer at all, so it happens to reproduce the intended spoiler boundary today
without any fix on either side.

This is worth fixing at the AnimeFeedFlux end, not ArticleFlux's — nobody should patch a general
sanitizer to special-case one anime-trivia feed's markup. The permalink page (`/items/{item_key}`)
already does the reveal-gating correctly per §5.5; the fix, when the renderer is built, is likely
either (a) never putting the answer in `content:encoded` at all — link to the permalink page for the
reveal instead of embedding it — or (b) accepting that any full-content RSS reader will spoil trivia
on open and treating Slack's `description`-only rendering as the actual spoiler boundary, with
`content:encoded` documented as "spoiled, for readers that show it." That's a product decision for
whoever builds A2/A4 (item rendering / generation contract), flagged here because it only becomes
visible once you trace a *second* real consumer's actual rendering path — Slack's own opacity to
`content:encoded` was hiding it.

## 4. Test plan — E0-01 (subscribe)

Precondition: C5 done (production `anime.earlcameron.com` live, TLS, at least one feed generating),
or, if run against staging instead (`staging.anime.earlcameron.com`, C2), state that substitution
explicitly in the run notes — the two hosts are not interchangeable subscriptions. **The three real
feed slugs are `daily-anime-trivia`, `weekly-anime-news`, `character-spotlight-weekly`** — the names
this doc originally guessed (`anime-trivia-daily`, `anime-fact-daily`, `anime-news-daily`, from
`PLAN.md` §19) do not match what's actually seeded; `anime-fact-daily`/`character-spotlight-weekly`
in particular are different feeds, not a rename — use the real slugs below, not the §19 ones,
whatever ends up deployed to production. Once C1/C2/C5 land this whole checklist runs in minutes:

1. Pick the target feed and format. Start with a non-trivia feed to prove the mechanism first:
   `https://anime.earlcameron.com/feeds/character-spotlight-weekly.xml` (or `weekly-anime-news.xml`)
   — no spoiler concern (§3 only applies to trivia). For `daily-anime-trivia`, subscribe via
   **`.json` specifically**, not `.xml`/`.atom` — per §3's confirmed cross-format table, only the
   JSON Feed variant's `content_html` omits the answer today; that's a real, zero-code mitigation,
   not just a note for later.
2. `curl -sI` the URL first: expect `Content-Type: application/rss+xml; charset=utf-8` (`.xml`),
   `application/atom+xml; charset=utf-8` (`.atom`), or `application/feed+json` (`.json`, no charset
   param on the dev instance — confirmed, harmless for gofeed but worth a glance if production's
   differs), plus `ETag`, `Last-Modified`, `Vary: Accept-Encoding`, `Cache-Control: max-age=900`
   (`PLAN.md` §5.4). Then `curl -s | xmllint --noout` (xml/atom) or `| jq .` (json). Failure here
   means E0-01 is blocked upstream of ArticleFlux; don't proceed.
3. In ArticleFlux, subscribe via whatever the current client exposes for `Service.Subscribe`
   (`ArticleFlux/internal/reader/service.go` lines 293–363) — paste the feed URL. Expect: a new
   source row, a synchronous first poll (line 319, `!existed || f.LastSuccess == ""` triggers an
   immediate `pollOneWithParsed`), and either the feed's items appear or a rollback with a
   surfaced error (lines 320–350).
   - **Failure mode — not a feed**: `feed.ErrNotAFeed` → subscription rolled back
     (`s.rollback`, lines 346–348), reported in the URL field. Would mean AnimeFeedFlux served
     something gofeed can't parse — check the `Content-Type` and that the body is well-formed XML/JSON
     first (step 2 should have caught this).
   - **Failure mode — blocked address**: `netguard.ErrBlockedIP`/`ErrScheme` (lines 334–337) —
     would only fire on a private/link-local address, not expected for a real public droplet URL;
     if it fires, DNS or the URL is wrong, not a feed-content problem.
   - **Failure mode — transient**: any other error leaves the subscription in place
     (line 350, `return f, existed, "", nil`) with `LastSuccess` still empty; the source retries on
     the scheduler's normal cadence. Re-run `Refresh` manually to retry sooner.
4. Confirm items appear with correct title, summary (from `description`), and the full body under
   `content:encoded`/`content_html` rendering as expected HTML (paragraphs, emphasis; images if the
   recipe includes any, subject to `Feed`-policy-equivalent tag stripping — `DefaultPolicy` at §3
   above, so anything outside `a/p/div/span/b/strong/i/em/u/code/pre/blockquote/ul/ol/li/h1–h6/br/hr/
   table.../img/figure/figcaption` will silently vanish or unwrap; note this if any generated markup
   uses tags outside that set — the seeded feeds don't, everything they use is in the allowlist).

## 5. Test plan — E0-02 (rendering, guid dedup, refresh)

Run in order; each step's failure signature is stated so a partial pass is diagnosable rather than
just "it didn't work."

1. **Rendering.** Open an ingested item in ArticleFlux's reading pane. Check: title matches, summary
   is the plain-text `description` (not raw HTML, not truncated mid-word — `truncate`,
   `feed.go` lines 418–428, prefers a word boundary), body is the `content:encoded` HTML with markup
   intact for allowed tags. **Failure looks like**: an empty body (means both `Content` and
   `Description` were empty on the gofeed side — check the raw feed response), or visibly stripped
   formatting (tag outside `DefaultPolicy`'s allowlist — expected and not a bug, see §4 note above).
2. **guid dedup, unedited re-poll.** Trigger a manual refresh (`Service.Refresh`) with no change on
   the AnimeFeedFlux side between polls. Expect: `IngestResult.New == 0`, `Updated == (item count)`,
   `Edited == 0` (`ingest.go` lines 158, `res.Updated++` on every seen row, `res.Edited++` only on a
   hash change). **Failure looks like**: item count in ArticleFlux growing — would mean guids are
   *not* stable across polls, i.e. AnimeFeedFlux is minting a new ULID per render rather than per
   insert, which would be a regression against `PLAN.md` §5.1's explicit design and should block
   ship, not just this ticket.
3. **guid dedup, an edited item.** Correct or edit a live item on the AnimeFeedFlux admin side (once
   that exists — B-phase) or, until then, via direct DB manipulation of a seeded item's title/summary
   for the purpose of this test. Re-poll ArticleFlux. Expect: same row, `revision` incremented by 1,
   `edited_at` set, the pre-edit version recoverable via `item_revisions` (§2 above), `published_at`
   unchanged (so the item's position in ArticleFlux's chronological list does **not** move).
   **Failure looks like**: a second row appearing (guid instability — same root cause as step 2's
   failure) or the item jumping to "now" in sort order (`published_at` leaking through on update,
   which would be an ArticleFlux regression against its own `ingest.go` lines 90–92 comment, not an
   AnimeFeedFlux problem).
4. **Trivia spoiler, if testing `daily-anime-trivia`.** If subscribed via `.xml` or `.atom`: open a
   trivia item, expect the answer visible immediately right after the question (§3, confirmed against
   the real `<hr class="spoiler-break"/><p><strong>Answer:</strong>…` markup) — not a bug in
   ArticleFlux to fix as part of E0, the documented consequence to confirm and note for whoever picks
   up the generation-contract work (A4/A9). If subscribed via `.json` per §4 step 1's recommendation:
   expect the answer **absent** — confirm that too, since it's the one case where dev-plane behavior
   might diverge from what production ends up serving (§3's table was built from the dev seed data;
   re-check it against whatever A2/A4 ship, since the JSON-omits-answer behavior could easily change
   with the real renderer and isn't a documented contract, just an observed accident of the seeder).
5. **Conditional GET / refresh cost.** After step 2's clean re-poll, check ArticleFlux's fetch log or
   `internal/store` `fetch_outcomes`-equivalent (`recordFetch`, `service.go` line 520) for whether the
   second poll actually hit `304` — compare against AnimeFeedFlux's own access log for a `304` at
   that timestamp, or, cheaper, just re-run the `curl -I` from §4 step 2 with `If-None-Match` set to
   the `ETag` observed the first time and confirm a `304` comes back with an empty body.
   **Failure looks like**: a `200` every time — would mean AnimeFeedFlux's `ETag`/`Last-Modified`
   aren't stable across identical renders (e.g. a timestamp baked into the ETag hash that changes on
   every request even with unchanged content), which defeats §5.4's entire point and would hammer
   SQLite on every one of ArticleFlux's 30-minute polls for no reason.
6. **New item delivery.** Trigger a real generation run (or promote a sample) that adds one new item.
   Re-poll ArticleFlux. Expect `IngestResult.New == 1`, `NewIDs` containing it, and — per `deliver`
   (`ingest.go` line 176, `internal/app/events.go` `onIngested`) — the item showing as unread for the
   subscribing account without a full list reload.

## 6. Summary of findings for whoever picks this up

- ArticleFlux is a clean, spec-appropriate consumer: three formats accepted, conditional GET
  honoured, guid-keyed dedup, edits update in place with history, `published_at` never reset by an
  edit. AnimeFeedFlux's `PLAN.md` §5.1/§5.4/§5.5 design and ArticleFlux's actual ingest code
  (as read, 2026-08-10) agree on every point checked except one.
- The one real disagreement, now **CONFIRMED against actual seeded bytes (2026-08-10)**, not just
  inferred from code: **AnimeFeedFlux's trivia spoiler mechanism (§5.5) is a bare
  `<hr class="spoiler-break"/>` marker, not a disclosure tag** — there is nothing for ArticleFlux's
  sanitizer to unwrap, every byte of the answer passes through both its server (no RSS-ingest
  sanitization at all) and client (`DefaultPolicy` allows `hr`/`p`/`strong`) layers unchanged, and the
  answer renders immediately on open in `.xml` and `.atom`. This should inform how A4/A9's item
  renderer actually implements the "spoiler break," or be accepted as a known limitation of full-body
  subscription for trivia specifically.
- **New, more actionable finding from the real bytes**: the three output formats do not agree with
  each other on trivia content. RSS keeps both the `<hr class="spoiler-break">` marker and the answer;
  Atom drops the marker but keeps the answer; **JSON Feed's `content_html` omits the answer entirely**
  (0 of 19 seeded items). Subscribing `daily-anime-trivia.json` specifically is, today, a working
  spoiler mitigation with zero code changes anywhere — folded into the E0-01 checklist (§4) above.
  This is almost certainly an accident of what the seeder (`cmd/affseed`) chose to put in each
  format's renderer rather than a designed contract, so it should be re-verified once A2/A4 build the
  real generation path, not assumed to hold forever.
- Nothing else in §5.1–§5.6 or §14.1 required a design change on the AnimeFeedFlux side based on
  ArticleFlux's actual code; the requirements already written are sufficient for a clean
  subscription once C1/C2/C5 land.

## 7. E1 deferral — reconsidered against three live feeds

`TODOS.md` defers `E1-01`–`E1-05` (aggregate feeds, shared upstream cache, bounded LRU render cache,
rail search/filter/pagination past ~40 feeds, per-feed published-identity overrides) until a 4th or
5th feed exists. The dev publish plane now has exactly three feeds live — `PLAN.md` §14.2/§14.3's
own stated threshold's edge, not past it. Checked whether anything about the real instance changes
that calculus:

- **No aggregate route exists.** The dev root (`http://127.0.0.1:8081/`) is a static per-feed index
  page (`<link rel="alternate">` autodiscovery tags plus a human-readable list) — one entry per feed,
  linking to each format, nothing that merges items across feeds into one combined view or feed. This
  is the natural precursor UI to `E1-01` but is not `E1-01` — no aggregate feed URL is served, no
  combined item stream exists to test rendering, dedup, or pagination against. **`E1-01` is correctly
  still deferred**, and there's nothing live to build it against differently than the doc already
  assumed.
- **Three feeds produce no cache-pressure signal.** `E1-02` (shared upstream source cache) and
  `E1-03` (bounded LRU render cache) exist to bound work/memory that scales with feed *and consumer*
  count; three feeds fetched once by a dev curl each is not remotely close to a load profile that
  would motivate either. **Still correctly deferred** — nothing observed changes this.
- **`E1-04`'s own threshold (~40 feeds) is untouched** by going from a hypothetical zero to a real
  three; three is still two orders of magnitude below the stated trigger. **Still correctly deferred.**
- **`E1-05` (per-feed published-identity overrides)** — checked whether the three seeded feeds' own
  identity fields (`<title>`, `<author>`/`AnimeFeedFlux Seed Bot`, `<generator>AnimeFeedFlux dev</generator>`)
  show any per-feed divergence that would make an override mechanism suddenly necessary: they don't —
  all three share the same generator string and author identity, differing only in title/description/
  content, which is exactly what the current design (one identity, N feeds) already handles.
  **Still correctly deferred.**

**Conclusion: none of `E1-01`–`E1-05` should be promoted out of deferral.** Three feeds is the
threshold's stated edge in name only — the actual triggers (an aggregate consumer surface being
built, a real cache-pressure signal, approaching ~40 feeds, or observed per-feed identity divergence)
are all still absent. The honest update to make is narrower than "promote something": if/when a 4th
feed is added, re-run this same check rather than treating "4th feed exists" as itself sufficient —
the doc's own phrasing already hedges with "or 5th," which this pass confirms was the right hedge.
