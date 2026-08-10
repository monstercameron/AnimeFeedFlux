# AnimeFeedFlux — Plan

## 1. The bet

Any RSS reader can subscribe to a feed. Nothing stops that feed from being *written on demand*.
AnimeFeedFlux is a feed **generator**: you declare a recipe ("a daily anime trivia question",
"today's anime news, ranked"), it runs on a schedule, calls an LLM, and publishes the result as a
standards-compliant RSS/Atom endpoint. The consumer is any reader — including ArticleFlux, which
becomes the front end for free.

The whole product surface is: **a URL that returns valid XML and never lies.**

Two item classes, and they have different rules:

- **Generative items** (trivia, fact-of-the-day, "on this day in anime"). The LLM *is* the source.
  Content is authored by us, hosted by us, permalinked to us. Risk = repetition and wrong facts.
- **Grounded items** (news, releases, seasonal roundups). The LLM is only an editor. Every link
  must resolve to a real article we fetched ourselves. Risk = hallucinated URLs, and that risk is
  handled architecturally, not by prompting.

Conflating these two is the main way this project fails.

## 2. Non-goals (v1)

- Not a reader/UI. ArticleFlux already is one.
- Not a scraper of paywalled or ToS-hostile sources.
- Not multi-tenant. Single owner (me), public read-only feeds.
- No comments, no social, no personalization per subscriber.
- Not a chat agent. Generation is a batch job with a fixed contract.

## 3. Stack

Consistent with CashFlux/ArticleFlux so the ops story is boring:

- Go backend, `net/http`, no framework.
- SQLite (`modernc` or mattn+FTS5, matching ArticleFlux) for feeds/items/runs.
- OpenAI via a thin `internal/llm` interface (structured outputs / JSON schema mode). Provider is
  behind an interface so it can be swapped, but v1 only ships the OpenAI impl.
- No web UI in v1. Operations happen through a headless CLI (`aff`).
- Deploy: the droplet, behind Caddy, on its own host (`anime.earlcameron.com` — `feed.` is taken by
  ArticleFlux).

### Layout

```
cmd/animefeedflux/   HTTP server + scheduler
cmd/aff/             CLI: validate, run-once, preview, list, backfill
internal/config/     env + flags
internal/feedspec/   recipe parse + validation
internal/store/      SQLite schema, queries, migrations
internal/llm/        OpenAI client, structured output, token accounting
internal/generate/   generators: trivia, fact, news, onthisday
internal/sources/    upstream RSS fetch, URL verification
internal/render/     RSS 2.0 / Atom / JSON Feed serialization
internal/scheduler/  cron loop, injectable clock
internal/httpapi/    /feeds/{slug}.xml, /items/{id}, /healthz
feeds/               *.toml recipes (source of truth, synced into DB)
testdata/            golden feeds, canned LLM responses, canned upstream RSS
```

## 4. The feed recipe

A recipe is a file. Files are the source of truth; boot syncs them into SQLite keyed by slug, and a
`spec_hash` records which version produced which items.

```toml
slug        = "anime-trivia-daily"
title       = "Daily Anime Trivia"
description = "One anime trivia question every morning."
language    = "en"
kind        = "generative"        # generative | grounded
schedule    = "0 12 * * *"        # UTC cron
items_per_run = 1
retain_items  = 200               # feed window; older rows stay in DB

[model]
name        = "gpt-5-mini"        # pin the exact id at implementation time
temperature = 0.9
max_output_tokens = 800

[prompt]
system = """You write anime trivia for a daily feed..."""
user   = """Produce one trivia question about anime that aired before {{.Today}}..."""

[novelty]
exclude_last   = 200              # prior titles injected as a do-not-repeat list
embedding_threshold = 0.90        # cosine above this = duplicate, retry
max_retries    = 2

[budget]
max_tokens_per_day = 20000
max_runs_per_day   = 2
```

Grounded recipes add:

```toml
[[sources]]
url  = "https://www.animenewsnetwork.com/newsroom/rss.xml"
kind = "rss"
```

Validation at load time (and in `aff validate`, and in CI): slug is URL-safe and unique, cron
parses, prompt templates compile, `grounded` implies ≥1 source, budgets are present and non-zero.
A recipe that fails validation disables that feed only — it never takes the server down.

## 5. Generation contract

Every generator returns the same shape, enforced by JSON schema on the model call, then re-validated
in Go (never trust the model to honor the schema):

```
Item {
  title        string   required, 10..200 chars
  body_html    string   required, sanitized allowlist (p, em, strong, a, ul, li, blockquote)
  link         string   optional for generative, REQUIRED for grounded
  source_name  string   grounded only
  tags         []string 0..6
  answer_html  string   trivia only — rendered below a spoiler break
}
```

Pipeline per run:

1. **Acquire context.** Generative: last N item titles for the exclusion list. Grounded: fetch all
   configured sources, parse, keep entries newer than the last run, cap at ~40 candidates.
2. **Call the model** with structured output. Log tokens in/out and estimated cost to `runs`.
3. **Validate** against the Go-side schema. Reject the whole run on malformed output; retry once.
4. **Sanitize HTML.** Allowlist only. This text is authored by a model and rendered in third-party
   readers — treat it as untrusted input.
5. **Novelty check** (generative): embed the title+body, compare against the last 500 embeddings,
   cosine > threshold → discard and retry, up to `max_retries`, then skip the run and log it.
   Repetition is the failure mode that kills a daily trivia feed; prompting alone won't prevent it.
6. **Link integrity** (grounded): the item's `link` MUST be byte-equal to a URL present in the
   fetched candidate set. Not "similar to" — present. Items failing this are dropped, and the drop
   is counted. Optionally re-verify with a HEAD/GET (200 + non-empty title).
7. **Persist** items with a stable `guid` = `sha256(feed_slug | normalized_title | date)`.
8. **Invalidate** the rendered-feed cache for that slug.

Failure is always "the feed keeps its previous items and logs an error run" — never a partial or
corrupt feed, never an item with an invented link.

## 6. Data model

```
feeds(id, slug UNIQUE, title, description, language, kind, spec_hash, enabled, created_at)
items(id, feed_id, guid UNIQUE, title, body_html, link, source_name, published_at,
      model, prompt_hash, tokens_in, tokens_out, created_at)
item_embeddings(item_id, vec BLOB, dim)
runs(id, feed_id, started_at, finished_at, status, error, items_added, items_rejected,
     tokens_in, tokens_out, est_cost_usd)
sources(id, feed_id, url, kind, last_fetched_at, last_etag)
```

`runs` is the operational spine: cost, drift, and "why is this feed stale" all get answered there,
and `aff runs --feed x` prints it.

## 7. Serving

- `GET /feeds/{slug}.xml` → RSS 2.0. `.atom` and `.json` from the same item set.
- `GET /items/{id}` → minimal HTML permalink page (needed so generative items have a real `link`).
- `GET /healthz`, `GET /metrics` (plain text: last run per feed, age, error count).

Readers poll hard, so serving is cache-first: render on write into an in-memory blob per slug,
serve with strong `ETag` (hash of body), `Last-Modified`, honor `If-None-Match`/`If-Modified-Since`
with 304, `Cache-Control: max-age=900`, gzip. A 304 must never touch SQLite or the LLM.

RSS correctness is a first-class requirement, not an afterthought: RFC-822 dates, `guid` with
`isPermaLink`, `atom:link rel="self"`, proper escaping/CDATA, valid `pubDate` ordering. Golden-file
tests plus a validator pass in CI.

## 8. Cost and blast radius

- `OPENAI_API_KEY` from env only. Never in a recipe, never committed. `.gitignore` already covers
  `.env`.
- Per-feed daily token and run caps enforced *before* the call; exceeding logs a skipped run.
- Global kill switch: `AFF_GENERATION_ENABLED=0` serves existing feeds and runs nothing.
- `aff preview <slug>` does a full dry run — renders the item it *would* publish, writes nothing.
  This is the loop I'll actually iterate prompts in.
- Scheduler is single-flight per feed with a DB-level run lock, so a slow run can't stack.

## 9. Testing

- `internal/llm` has a `FakeClient` replaying `testdata/*.json`. **The default test run never calls
  a paid API.** Live-provider tests are gated behind `AFF_LIVE_LLM=1` and excluded from CI.
- Upstream RSS fetches are served from `testdata/` via an injected `http.Client` in tests.
- Golden-file tests for RSS/Atom/JSON rendering, including escaping edge cases (ampersands, CJK
  titles, emoji, HTML in titles).
- Scheduler tests use an injected clock — no sleeping.
- Adversarial generator tests: model returns malformed JSON; model returns a URL not in the source
  set; model returns a near-duplicate of yesterday; model returns `<script>` in `body_html`. Each
  must be rejected, not published.

## 10. Milestones

| # | Milestone | Done when |
|---|-----------|-----------|
| M0 | Skeleton | Module, layout, config, `/healthz`, CI runs `go test ./...` |
| M1 | Store + render | Schema + migrations; hand-seeded items render as valid RSS/Atom/JSON; golden tests pass |
| M2 | Serving | Conditional GET, ETag, gzip, cache; item permalink pages |
| M3 | Recipes | `feeds/*.toml` parse, validate, sync to DB; `aff validate` / `aff list` |
| M4 | Generative feed | Trivia + fact-of-the-day end to end with FakeClient; then one real run |
| M5 | Novelty | Embeddings, dedup, retry; 30-day simulated backfill produces no near-duplicates |
| M6 | Scheduler | Cron loop, single-flight, budgets, `runs` accounting, kill switch |
| M7 | Grounded news | Source fetch, candidate set, link-integrity enforcement, ranking prompt |
| M8 | Deploy | `anime.earlcameron.com` behind Caddy, systemd unit, log rotation, feeds live |
| M9 | Integration | ArticleFlux subscribes; verify rendering, dedup, and refresh behavior there |

M1–M3 are pure plumbing and should land fast. M5 and M7 are where the actual engineering is.

## 11. Risks

- **Repetition** in daily generative feeds — mitigated by M5, but embeddings only catch surface
  similarity. Expect to add a topic-coverage ledger (which series/decades/genres were used) later.
- **Wrong facts.** Trivia will occasionally be wrong and there's no cheap oracle. Mitigation: keep
  claims narrow, cite a source when the model can name one, and add a "report" mailto in the feed
  footer. Accepting some error rate is the honest position; pretending otherwise isn't.
- **Hallucinated links** — structurally prevented (§5 step 6), not prompted away.
- **Upstream ToS.** Summarize-and-link only; never republish full article text.
- **Cost creep** — capped per feed per day, tracked in `runs`.

## 12. Open questions

1. **Which feeds ship first?** My assumption: `anime-trivia-daily`, `anime-fact-daily`,
   `anime-news-daily` (grounded). Confirm or swap.
2. **Public or private?** Plan assumes public read-only feeds on `anime.earlcameron.com`. If these
   should be private, feeds need per-subscriber tokens in the URL, which changes §7 caching.
3. **Grounded news sources** — starting set is ANN + Crunchyroll News RSS. Any others you want in,
   or any you want excluded?
