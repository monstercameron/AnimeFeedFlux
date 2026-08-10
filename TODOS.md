# AnimeFeedFlux — TODOS

Companion to `PLAN.md`. The plan says *why*; this file says *what next*, atomically.

## How to use this file

- **One task = one commit-sized change** with a verifiable outcome. If a task cannot be checked off
  by running something or reading a diff, it is too vague and should be split.
- **Every task cites a plan section.** `§9.6` means section 9, step 6. Read the cited section before
  starting — the plan carries the reasoning and the traps, and several tasks look trivial until you
  read why they exist.
- **IDs are stable.** Never renumber. Insert `A4-12b` rather than shifting everything below.
- **Phase order is a dependency order, not a preference.** Phase D (UI) is last on purpose (§18).
- Markers: `[ ]` open · `[x]` done · `[~]` in progress · `[!]` blocked · `[-]` dropped (say why).
- **Do not mark a task done on the strength of code existing.** Done means its stated check passes.

## Standing rules that apply to every task

- `RULE-1` The default test run never calls a paid API. Paid tests sit behind `AFF_LIVE_LLM=1`. §17
- `RULE-2` No secret ever enters the repo, an image layer, a log line, or a recipe. §4
- `RULE-3` Model output and upstream RSS are hostile input. Sanitize, then escape at render. §4
- `RULE-4` Typed is not valid. SchemaFlux guarantees shape; business rules stay ours. §8
- `RULE-5` Two date formatters exist (RFC 822 for RSS, RFC 3339 for Atom/JSON). Never cross them. §5.2
- `RULE-6` Every write that changes a feed's published representation invalidates its cache. §11
- `RULE-7` Never backdate an item. Slack's bookmark makes it invisible forever. §5.5

---

# Phase A — Core engine

No network surface, no auth, no UI. Everything here is proven by tests and, from A8, the CLI.

## A0 — Skeleton

- [x] `A0-01` Create `go.mod` as `github.com/monstercameron/AnimeFeedFlux`, Go 1.26. §3
- [ ] `A0-02` Create the directory layout exactly as listed in §3, with a doc comment per package.
- [x] `A0-03` Add `github.com/monstercameron/schemaflux` as a dependency; record the version. §8
- [x] `A0-04` Write `internal/config`: one struct, parsed from env, no globals. §16
- [x] `A0-05` Validate config at boot; fail fast with one clear message per bad variable. §16
- [x] `A0-06` Validate `AFF_PUBLIC_BASE_URL` is absolute with a scheme — it is baked into guids. §16
- [x] `A0-07` Structured logging via `log/slog` to **stdout** (container requirement). §15
- [x] `A0-08` Add the secret-redaction filter on the log writer as a backstop. §4
- [x] `A0-09` Thread a request/run id through the logger context. §15
- [x] `A0-10` `cmd/animefeedflux` boots, serves `/healthz`, exits cleanly on SIGTERM.
- [x] `A0-11` `Makefile`: `build`, `test`, `lint`, `validate`, `run`.
- [x] `A0-12` CI workflow: `go build`, `go vet`, `go test ./...` on ubuntu-latest.
- [x] `A0-13` Add `-race` to CI (it cannot run on windows/arm64 locally — CI is the only place). §8
- [x] `A0-14` Add `govulncheck` to CI. §4
- [ ] `A0-15` Commit `go.sum`; pin dependencies. §4
- [x] `A0-16` Decide and record the SQLite driver (cgo vs pure-Go vs wasm). Blocks A1-01. §15.1
- [x] `A0-17` Record the `MemoryDenyWriteExecute` / `CGO_ENABLED=0` consequence of A0-16. §15.1

### A0-L — Structured logging (§15.0)

The field names are the product here. Three packages spelling the same thing differently is three
fields, and no query finds all of them.

- [ ] `A0-L01` Define the canonical field-name constants in one place; nothing logs a bare string key. §15.0
- [ ] `A0-L02` `feed_slug`, `item_key`, `model`, `outcome`, `reason`, `duration_ms` all fixed there. §15.0
- [ ] `A0-L03` `duration_ms` is emitted as a **number**, never a formatted string. §15.0
- [ ] `A0-L04` `outcome` is constrained to `success|skipped|rejected|failed`. §15.0
- [ ] `A0-L05` `reason` is a short stable token, not a sentence — it gets grouped on. §15.0
- [ ] `A0-L06` Document the level policy: ERROR means a human must look; WARN self-healed. §15.0
- [ ] `A0-L07` A retried transient provider error logs **WARN**, not ERROR. §15.0
- [ ] `A0-L08` Helper that emits the single canonical `run.finished` wide event. §15.0
- [ ] `A0-L09` Helper that emits the single canonical `http.request` event. §15.0
- [ ] `A0-L10` **No chatty INFO.** Progress detail is DEBUG only. §15.0
- [ ] `A0-L11` Test: a completed run emits exactly one `run.finished` carrying every required field.
- [ ] `A0-L12` Test: no log record is emitted with a field name outside the canonical set.
- [ ] `A0-L13` Test: model output never reaches a log field verbatim (RULE-3 + cardinality).

### A0-O — OpenTelemetry (§15.0a)

Instrumentation is written unconditionally; only the **exporter** is conditional. Code that only
creates spans when a flag is set has never run, and breaks the first time it is switched on during
an incident — which is exactly when it is switched on.

- [x] `A0-O01` `internal/obs`: build a `TracerProvider` with resource attributes (service, version, commit). §15.0a
- [x] `A0-O02` Exporter selection: `otlp` \| `stdout` \| none, from config. §16
- [x] `A0-O03` **Default off** (`AFF_OTEL_ENABLED=0`) with a genuine no-op provider, not a nil check at each call site. §15.0a
- [x] `A0-O04` Honour the standard `OTEL_EXPORTER_OTLP_*` variables rather than inventing new ones. §16
- [x] `A0-O05` Treat `OTEL_EXPORTER_OTLP_HEADERS` as a **secret** — it carries the backend token. RULE-2
- [x] `A0-O06` Sampler: always-sample generation runs, ratio-sample publish requests. §15.0a
- [x] `A0-O07` Always sample a trace that contains an error, whatever the ratio says. §15.0a
- [x] `A0-O08` **Put `trace_id` and `span_id` on every log record** from the active span. §15.0a
- [x] `A0-O09` `MeterProvider` alongside, same exporter lifecycle.
- [x] `A0-O10` Register the metric set from §15.0a and no more.
- [x] `A0-O11` **Cardinality guard**: a helper that panics in tests if a label value is unbounded. §15.0a
- [x] `A0-O12` Never label a metric with `item_key`, a URL, a title, or model output. §15.0a
- [x] `A0-O13` Flush and shut the providers down cleanly on SIGTERM, before the process exits. §15
- [x] `A0-O14` A failing exporter must **never** block or crash the app — telemetry is not the product.
- [x] `A0-O15` Test: with OTel disabled, no exporter is constructed and no goroutine leaks.
- [x] `A0-O16` Test: with the stdout exporter, a run produces the §15.0a span tree, parented correctly.
- [x] `A0-O17` Test: `trace_id` in a log record matches the span that produced it.
- [x] `A0-O18` Test: an unbounded label is rejected by the cardinality guard.

### A0-T — Test infrastructure (build it before the suites that need it)

- [x] `A0-T01` Golden-file helper with an `-update` flag; a format change is one flag + a diff. §17.1
- [x] `A0-T02` Seeded store builder producing a deterministic feed with known items. §17.1
- [x] `A0-T03` Injected `http.Client` serving `testdata/` for every upstream fetch. §17.1
- [ ] `A0-T04` Injected clock; no test ever sleeps. §17.1
- [x] `A0-T05` Deterministic ULID source so goldens containing guids are stable. §17.1
- [x] `A0-T06` `testdata/` layout convention documented in-repo. §17.1
- [ ] `A0-T07` Assert the default `go test ./...` needs no network and no API key. RULE-1
- [x] `A0-T08` CI runs `go test -race` on ubuntu — the only place `-race` can run. §17.2
- [x] `A0-T09` `-shuffle=on` for local runs, knowing it is weaker than `-race`. §17.2
- [x] `A0-T10` Per-package coverage measured and reported. §17.2
- [ ] `A0-T11` Coverage **ratchet** — the number may not go down. Not a target. §17.2
- [x] `A0-T12` `go vet` + linter + `govulncheck` gate the build. §17.2

## A1 — Store

- [x] `A1-01` Open SQLite with WAL, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`. §3
- [x] `A1-02` Register FTS5 (needs explicit build tag / registration — known trap). §3
- [x] `A1-03` Forward-only migration runner: numbered SQL, in a transaction, version recorded. §10
- [x] `A1-04` **Open the writer connection before the `mode=ro` reader** and assert the order. §2
- [x] `A1-05` Reader interface with **no write methods**; publish plane gets only this. §2
- [x] `A1-06` Migration `0001`: `admin`, `recovery_codes`, `totp_used`, `sessions`, `auth_events`. §10
- [x] `A1-07` `totp_used.step` is the PRIMARY KEY — the DB rejects the replay race, not the app. §4
- [x] `A1-08` Migration `0002`: `settings` singleton key/value table. §10
- [x] `A1-09` Migration `0003`: `feeds` with `kind`, `timezone`, `jitter_offset`, per-feed identity. §10
- [x] `A1-10` Migration `0004`: `items` with `item_key UNIQUE` (opaque ULID) and `content_hash`. §5.1
- [x] `A1-11` Add `UNIQUE(feed_id, content_hash)` for idempotency — separate from identity. §5.1
- [x] `A1-12` Add `UNIQUE(feed_id, published_at)` — Slack drops items sharing a timestamp. §5.5
- [x] `A1-13` Migration `0005`: `item_revisions`, `item_embeddings`, `corrections`. §10
- [x] `A1-14` Migration `0006`: `runs` with `lock_holder`, `heartbeat_at`, `reject_reasons_json`. §10
- [x] `A1-15` Constrain `runs.trigger` to `cron | manual` — no other producer exists. §10
- [x] `A1-16` Migration `0007`: `samples`, `sources`, `feed_members`. §10
- [x] `A1-17` Migration `0008`: `items_fts` external-content FTS5 table plus sync triggers. §10
- [x] `A1-18` ULID generation for `item_key`; assert it is never derived from content. §5.1
- [x] `A1-19` Soft-delete helpers: `Delete` sets `deleted_at`, `Restore` clears it. No hard delete. §12.4
- [x] `A1-20` Optimistic concurrency: every mutable row carries a version, bumped on write. §11
- [x] `A1-21` Test: migrate from empty; migrate twice is idempotent; migrate onto seeded prior schema.
- [x] `A1-22` Test: cold boot opens writer before reader on a fresh WAL database. §2
- [x] `A1-23` Test: `UNIQUE(feed_id, content_hash)` makes a repeated run add nothing. §5.1
- [x] `A1-24` Test: **guid is stable across a title edit** — the whole point of the ULID. §5.1
- [x] `A1-25` Test: FTS5 triggers stay in sync on insert, update, and soft delete.
- [x] `A1-26` Test: the read-only handle **rejects writes**. Proves the §2 claim.

## A2 — Renderers

- [x] `A2-01` RFC 822 formatter: four-digit year, UTC, `+0000`, no military zones. §5.1
- [x] `A2-02` RFC 3339 formatter: uppercase `T`, uppercase `Z`. §5.2
- [x] `A2-03` Test asserting neither formatter can produce the other's output. RULE-5
- [x] `A2-04` Tag URI builder: `tag:<host>,<year>:<slug>/<item_key>`, used by RSS and Atom alike. §5.1
- [x] `A2-05` Hex character-reference escaper for plain-text elements (`&#x26;`). §5.1
- [x] `A2-06` CDATA writer that splits `]]>` across sections. §5.1
- [x] `A2-07` URL absolutizer; reject or rewrite every relative href before storage. §5.1
- [x] `A2-08` RSS 2.0 renderer: required channel elements plus language, ttl, generator, docs. §5.1
- [x] `A2-09` Emit `atom:link rel="self"` with `type="application/rss+xml"`. §5.1
- [x] `A2-10` Emit `guid isPermaLink="false"` — never rely on the `true` default. §5.1
- [x] `A2-11` Emit `description` as **plain text**, `content:encoded` after it as HTML. §5.5
- [x] `A2-12` Atom renderer: one `id`/`title`/`updated` per feed and entry, `author` present. §5.2
- [x] `A2-13` Atom entries carry both `content` and `link rel="alternate"`. §5.2
- [x] `A2-14` JSON Feed 1.1: exact version URL, string `id`, `content_html`, `authors` not `author`. §5.3
- [x] `A2-15` Permalink HTML page with OpenGraph and `twitter:card` tags. §5.5
- [x] `A2-16` Trivia permalink renders the answer behind a spoiler break. §5.5
- [x] `A2-17` **`og:description` is the question, never the answer** — enforced at the renderer. §5.5
- [x] `A2-18` Feed index page at `/` with `<link rel="alternate">` autodiscovery. §6
- [x] `A2-19` Golden files for all three formats plus the permalink page.
- [x] `A2-20` Goldens include: ampersands, `<` in a title, CJK, emoji, `]]>` in body, 500-char summary.
- [x] `A2-21` Golden for an item with no link (generative) and one with an external link (grounded).

## A3 — Compliance

- [x] `A3-01` `make validate` renders goldens and runs the W3C / RSS Advisory Board validator. §5.6
- [x] `A3-02` CI fails on validator **warnings**, not only errors. §5.6
- [x] `A3-03` Slack test: `pubDate`s strictly descending and unique across the feed. §5.5
- [x] `A3-04` Slack test: every item has a present, parseable date. §5.5
- [x] `A3-05` Slack test: `description` is plain text and under the hard cap. §5.5
- [x] `A3-06` Slack test: no answer text appears in `description` or `og:description`. §5.5
- [x] `A3-07` Slack test: OG tags present and populated on the permalink page. §5.5
- [x] `A3-08` Document in-repo which validator version CI pins, so a green run is reproducible.

## A4 — Generation with SchemaFlux

- [x] `A4-01` Define the `GeneratedItem` Go type matching the §9 contract exactly.
- [x] `A4-02` `internal/llm` adapter: build the typed SchemaFlux call, return our own result type. §8
- [x] `A4-03` Construct an explicit SchemaFlux `Client` per call — never package defaults. §8
- [x] `A4-04` Capture tokens in/out, model id, and cost from SchemaFlux onto the run. §8
- [x] `A4-05` Map SchemaFlux errors to `Transient` / `Invalid` / `Fatal`. §8
- [x] `A4-06` `Transient`: exponential backoff with jitter, honor `Retry-After`, cap 3 attempts. §8
- [x] `A4-07` `Invalid`: one repair attempt with the validation error fed back, then fail. §8
- [x] `A4-08` `Fatal` **account-wide** (bad key, quota) trips the global kill switch. §8
- [x] `A4-09` `Fatal` **recipe-scoped** (bad model id) disables only that feed. §8
- [x] `A4-10` Context-length overflow: shrink candidates/recent titles, retry once. §8
- [x] `A4-11` Wrap every provider call in a context timeout. §8
- [ ] `A4-12` **Decide cassettes vs a hand-built `FakeProvider`; record the choice.** §8, RULE-1
- [x] `A4-12a` Use `client.Context(ctx)` on every call — a per-call Client alone does NOT isolate; `With*` mutates process-wide state (§8.1)
- [x] `A4-12b` **Do not add retry, backoff or a call timeout.** SchemaFlux owns them; two budgets on one call means the shorter silently wins (§8)
- [x] `A4-12c` Cost is ESTIMATED, not reported: `Generating[T]` returns zero usage. Label it an estimate everywhere it is shown (§8.1, §13)
- [x] `A4-12d` Embeddings call `sashabaranov/go-openai` DIRECTLY — SchemaFlux keeps its embedding API internal (§8.1, §9.5)
- [x] `A4-13` Go-side revalidation of every field: lengths, required-ness, tag count. RULE-4
- [x] `A4-14` HTML allowlist sanitizer (`p, em, strong, a, ul, li, blockquote, code`). §9.4
- [x] `A4-15` Reject `<script>` and any attribute outside the allowlist. §17
- [x] `A4-16` Reject a relative URL in any anchor. §17
- [x] `A4-17` Reject an answer leaked into `summary_text`. §17
- [x] `A4-18` Prompt template engine with the §7 variable set.
- [x] `A4-19` `ValidateSpec` **executes** templates against a populated dummy context. §7
- [x] `A4-20` Store `prompt_hash` per item so quality changes trace to a prompt. §7
- [x] `A4-21` Trivia generator producing question, answer, and a spoiler-safe summary. §1
- [x] `A4-22` Fact-of-the-day generator. §1
- [x] `A4-23` Assign distinct, strictly increasing `published_at` per item in a run. §5.5
- [x] `A4-24` Never stamp earlier than the feed's current newest item. RULE-7
- [x] `A4-25` **Insert items and close the run in one transaction.** §9
- [x] `A4-26` Invalidate the render cache *outside* that transaction — it is idempotent. §9
- [x] `A4-27` Test: malformed model output rejects the run rather than publishing.
- [x] `A4-28` Test: two items in one run cannot share a timestamp.
- [x] `A4-29` Test: a backdated `published_at` is rejected.
- [ ] `A4-30` One real generation run against OpenAI, manually reviewed for quality. `AFF_LIVE_LLM=1`
- [ ] `A4-31` Span `llm.generate` comes from SchemaFlux — wire the provider, do not re-instrument. §15.0a
- [ ] `A4-32` Span `validate` records rejected count and reasons as attributes. §15.0a
- [ ] `A4-33` Emit `aff_tokens_total` and `aff_cost_usd_total` from the recorded usage. §15.0a
- [ ] `A4-34` Emit the canonical `run.finished` wide event with every §15.0 field. §15.0

## A5 — Novelty

- [x] `A5-01` Embedding call through SchemaFlux; record model and dimension per row. §8
- [x] `A5-02` Store vectors **L2-normalized** so similarity is a dot product. §8
- [x] `A5-03` Brute-force compare against the last 500 — **no vector index**, it would be premature. §8
- [x] `A5-04` Reject and retry on cosine above the per-recipe threshold, up to N times. §9.5
- [x] `A5-05` After N retries, skip the run and log it as skipped-for-novelty rather than failing.
- [x] `A5-06` Detect an embedding-model change and trigger a background re-embed. §8
- [x] `A5-07` Build the exclusion list (`{{.RecentTitles}}`) from the last N titles. §7
- [x] `A5-08` Seed a corpus of known near-duplicates and assert every one is caught. §18 A5
- [x] `A5-09` Assert genuinely distinct items are **not** rejected (false-positive guard).
- [x] `A5-10` Record the chosen threshold and the evidence for it, in-repo.
- [ ] `A5-11` Span `novelty.check` with `max_cosine` and the verdict as attributes. §15.0a
- [ ] `A5-12` `aff_items_rejected_total{reason="novelty"}` incremented on a rejection. §15.0a

## A6 — Grounded news

- [x] `A6-01` Upstream fetcher with conditional GET; store their ETag and Last-Modified. §9.1
- [x] `A6-02` Cap upstream body size and **disable XML entity expansion** (billion laughs). §4
- [x] `A6-03` Parse RSS and Atom sources into a common candidate shape.
- [x] `A6-04` **Normalize candidate URLs once at fetch** — absolutize, strip `utm_*`/`fbclid`. §9.1
- [x] `A6-05` Use that same normalizer on model output. One function, both sides. §9.6
- [x] `A6-06` Keep only entries newer than the last run; cap at ~40 candidates. §9.1
- [x] `A6-07` Render candidates into `{{.Candidates}}` with title, url, published, excerpt. §7
- [x] `A6-08` Enforce link byte-equality against the candidate set; drop and count failures. §9.6
- [x] `A6-09` Optional second check: link resolves 200 with a non-empty title. §9.6
- [x] `A6-10` Record reject reasons on the run so the sampler can show them. §10
- [x] `A6-11` **Test: candidate carries tracking params, model echoes it verbatim → accepted.** §9.6
- [x] `A6-12` Test: a URL absent from the candidate set is rejected.
- [x] `A6-13` Ranking prompt that orders candidates by newsworthiness. §1
- [x] `A6-14` A dead or reformatted source degrades the feed, never breaks the run. §19
- [x] `A6-15` Evaluate SchemaFlux `Deduplicate` on the ~40-candidate set; record the decision. §8
- [x] `A6-16` Summarize-and-link only; never store full upstream article text. §19
- [ ] `A6-17` Span `sources.fetch` per source: url, status, whether it 304'd, item count. §15.0a
- [ ] `A6-18` Span `link.integrity` with candidates, accepted, rejected. §15.0a
- [ ] `A6-19` A rejected link is logged with `reason`, never with the model's raw output. RULE-3

## A7 — Scheduler

- [x] `A7-01` Cron parser evaluating in the recipe's **IANA timezone**, not UTC. §7
- [x] `A7-02` DST: a run in the skipped hour fires at the next valid instant. §7
- [x] `A7-03` DST: a run in the repeated hour fires **once**, tracked by `last_fired_slot`. §7
- [x] `A7-04` Deterministic jitter from `hash(slug)` across the configured window. §14.3
- [ ] `A7-05` Persist `jitter_offset` so the UI readback matches reality. §14.3
- [x] `A7-06` Worker pool capped by `AFF_MAX_CONCURRENT_RUNS`. §14.3
- [x] `A7-07` Global provider semaphore **shared with sampling**. §13
- [x] `A7-08` Per-feed single-flight via a DB run lock with heartbeat. §13
- [x] `A7-09` Hard wall-clock timeout per run. §14.3
- [x] `A7-10` Auto-disable a feed after N consecutive failures, with a loud reason. §14.3
- [x] `A7-11` Recover panics at the worker boundary; record a failed run. §14.3
- [x] `A7-12` Enforce per-feed daily token and run caps **before** the call. §13
- [x] `A7-13` Enforce a global daily spend ceiling on top of per-feed caps. §13
- [x] `A7-14` Kill switch honored by both scheduled runs and sampling. §13
- [x] `A7-15` Editable price table; store `est_cost_usd` at the price in force. §13
- [x] `A7-16` Injectable clock; no sleeping in tests. §17
- [x] `A7-17` Test: both DST cases fire exactly once.
- [x] `A7-18` Test: 20 feeds on an identical cron spread across the jitter window. §17
- [ ] `A7-19` Root span `generation.run` with `feed_slug`, `trigger`, `outcome`. §15.0a
- [ ] `A7-20` `aff_runs_total` and `aff_run_duration_seconds` on every terminal state. §15.0a
- [ ] `A7-21` Budget refusals increment `aff_runs_total{outcome="skipped"}`, not an error. §13

## A8 — Sampling

- [x] `A8-01` Dry-run path reusing the **entire** generation pipeline, writing no items. §11
- [x] `A8-02` Return candidate items, rendered `<item>` XML, novelty verdict, and cost. §11
- [x] `A8-03` Return grounded link verdicts including the failing URL. §12.3
- [x] `A8-04` Persist samples for 24h with `expires_at`. §12.3
- [x] `A8-05` Streaming variant emitting deltas as they arrive. §11
- [x] `A8-06` Sample size 1–5 and an optional temperature override. §12.3
- [x] `A8-07` `PromoteSample` writes the item stamped **now**, retrying on timestamp collision. §11
- [x] `A8-08` Sampling draws from the same budget as scheduled generation. §13
- [x] `A8-09` Test: sampling writes nothing but a `samples` row.

## A9 — Publish plane

- [x] `A9-01` Dedicated listener with explicit read/write/idle timeouts and `MaxHeaderBytes`. §6
- [x] `A9-02` Route set exactly as §6 — nothing else exists.
- [x] `A9-03` In-memory render cache: body, gzip body, ETag, Last-Modified, keyed slug+format. §6
- [x] `A9-04` A cache hit never touches SQLite. §6
- [x] `A9-05` Strong `ETag` from a hash of the exact rendered body. §5.4
- [x] `A9-06` Honor `If-None-Match` and `If-Modified-Since` with `304`. §5.4
- [x] `A9-07` `HEAD` behaves as `GET` minus body, validators included. §5.4
- [x] `A9-08` Correct content types; never `text/xml`. §5.4
- [x] `A9-09` **`Vary: Accept-Encoding` on every feed response.** §5.4
- [x] `A9-10` `Cache-Control: max-age=900` consistent with `<ttl>15</ttl>`. §5.4
- [x] `A9-11` Feed window cap: 50 items / 512 KB ceiling. §5.4
- [x] `A9-12` Per-IP token-bucket rate limit; `429` with `Retry-After`. §6
- [x] `A9-13` `405` with `Allow` for any method beyond GET/HEAD. §6
- [x] `A9-14` `404` unknown slug; **`410 Gone`** for a soft-deleted item. §6
- [x] `A9-15` A disabled feed still serves its last built content; a deleted feed `410`s. §6
- [x] `A9-16` No stack traces, no version banner, no directory listing. §6
- [x] `A9-17` `robots.txt`. §6
- [x] `A9-18` Test: 304 on both validators; 405; 410; gzip correctness; `Vary` present. §17
- [x] `A9-19` End-to-end: generate → fetch → validator passes → item appears once over two polls. §17
- [x] `A9-20` Span `http.request` with route, status, and cache result (hit|miss|304). §15.0a
- [x] `A9-21` Child span `render.feed` on a cache miss only — a hit must stay cheap. §15.0a
- [x] `A9-22` `aff_http_requests_total` and `aff_cache_hits_total`; the 304 ratio is the number that matters. §15.0a
- [ ] `A9-23` Publish-plane requests are ratio-sampled; errors always sampled. §15.0a
- [ ] `A9-24` Emit the canonical `http.request` event once per request, not per stage. §15.0

## AF — Fuzz, soak, and load (cross-cutting; land as the pieces they target land)

- [x] `AF-01` Fuzz the HTML sanitizer, seeded with an XSS corpus. §17.3
- [x] `AF-02` Any sanitizer output containing a tag or attribute outside the allowlist fails. §17.3
- [x] `AF-03` Fuzz the URL normalizer for **idempotence**: `norm(norm(u)) == norm(u)`. §17.3
- [x] `AF-04` State why AF-03 matters: §9.6 byte-equality is only sound if normalization is stable.
- [x] `AF-05` Fuzz the RSS renderer: output must always parse as well-formed XML. §17.3
- [x] `AF-06` Fuzz the Atom renderer likewise. §17.3
- [x] `AF-07` Fuzz the JSON Feed renderer: output must always be valid JSON. §17.3
- [x] `AF-08` Renderer fuzz asserts text content round-trips — the cheapest escaping-bug guard. §17.3
- [x] `AF-09` **90-day simulated soak** on the fake provider with the clock advanced. §17.4
- [x] `AF-10` Soak asserts: no duplicate guids across the whole run. §17.4
- [x] `AF-11` Soak asserts: `pubDate`s strictly decreasing and unique throughout. §17.4
- [x] `AF-12` Soak asserts: novelty rejections occur and do not runaway-retry. §17.4
- [x] `AF-13` Soak asserts: budgets enforced every day, never exceeded. §17.4
- [x] `AF-14` Soak asserts: run history internally consistent end to end. §17.4
- [x] `AF-15` Poll-load check: many concurrent conditional GETs, 304s dominate. §17.4
- [x] `AF-16` Poll-load asserts **no SQLite query on a cache hit**. §17.4
- [x] `AF-17` Poll-load asserts memory stays flat across sustained polling. §17.4

---

# Phase B — Control surface (headless)

## B0 — Auth

- [x] `B0-01` argon2id hashing; store parameters beside the hash for later raising. §4
- [x] `B0-02` Rehash on next successful login when parameters change. §4
- [x] `B0-03` Constant-time verification; always run the KDF even for unknown users. §4
- [x] `B0-04` `aff admin init`: reads stdin, refuses weak passphrases, no default password. §4
- [x] `B0-05` TOTP (RFC 6238) enrollment with a ±1 step drift window. §4
- [x] `B0-06` Replay prevention via the `totp_used` primary key. §4
- [x] `B0-07` Encrypt the TOTP secret at rest with a key derived from `AFF_SECRET_KEY`. §4
- [x] `B0-08` Recovery codes: generated once, shown once, stored hashed, single use. §12.2
- [x] `B0-09` Sessions: 256-bit token, hashed at rest, 12h absolute / 60m idle, rotation on login. §4
- [x] `B0-10` `__Host-` prefixed cookie, `HttpOnly; Secure; SameSite=Strict`. §4
- [x] `B0-11` Per-IP and per-account exponential backoff with one generic failure message. §4
- [ ] `B0-12` Log every attempt to `auth_events`. §4
- [ ] `B0-13` `aff admin reset` break-glass, requiring local DB access. §12.2
- [ ] `B0-14` Recovery flow: consume a code → 10-minute elevated session → force re-login. §12.2
- [ ] `B0-15` Elevated session reaches **only** password change and TOTP re-enrollment. §12.2
- [ ] `B0-16` Recovery revokes every other session. §12.2
- [ ] `B0-17` Test: replayed TOTP rejected; drift-window edges behave.
- [ ] `B0-18` Test: session expiry, rotation, revocation.
- [ ] `B0-19` Test: timing uniformity between unknown user and bad password.
- [ ] `B0-20` Test: a recovery code cannot be used twice.

## B1 — RPC services

- [ ] `B1-01` `proto/aff/v1` definitions for all six services; buf codegen wired into the build. §11
- [ ] `B1-02` `AuthService`. §11
- [ ] `B1-03` `FeedService` including `ValidateSpec`, `SetMembers`, TOML export/import. §11
- [ ] `B1-04` `SampleService`. §11
- [ ] `B1-05` `ItemService` — **no `PurgeDeleted`; hard delete does not exist.** §12.4
- [ ] `B1-06` `RunService` with `Watch` streaming. §11
- [ ] `B1-07` `SystemService` including `Backup` and the kill switch. §11
- [ ] `B1-08` Auth interceptor validating the session on **every** RPC. §4
- [ ] `B1-09` `expected_version` optimistic concurrency on every mutation. §11
- [ ] `B1-10` Opaque cursor pagination on list RPCs. §11
- [ ] `B1-11` gRPC status codes with machine-readable detail for field-level errors. §11
- [ ] `B1-12` Cache invalidation on **feed-level** writes too: `Update`, `SetEnabled`, `SetMembers`. §11
- [ ] `B1-13` Invalidate any aggregate containing a changed feed. §11
- [ ] `B1-14` `PublishCorrection` creating a linked, later-stamped item. §12.4
- [ ] `B1-15` Record edits into `item_revisions`. §12.4
- [ ] `B1-16` Reject an aggregate as a member of an aggregate. §14.2
- [ ] `B1-17` Reject a slug change after first publish. §14.1
- [ ] `B1-18` Reject reserved slugs. §14.1
- [ ] `B1-19` Test: `PromoteSample` racing a scheduled run yields distinct timestamps, no raw error. §17

## B2 — Bridge

- [ ] `B2-01` Wire GoGRPCBridge over WebSocket for the control plane. §3
- [ ] `B2-02` Validate the session cookie at upgrade. §4
- [ ] `B2-03` Check `Origin` against `AFF_ALLOWED_ORIGINS` at upgrade. §4
- [ ] `B2-04` Pair client keepalive with server `EnforcementPolicy` — the known GOAWAY flap. §3
- [ ] `B2-05` Session revocation terminates in-flight streams. §4
- [ ] `B2-06` Verify `SampleStream` and `RunService.Watch` actually stream through the bridge. §11
- [ ] `B2-07` Test: an upgrade from a disallowed `Origin` is rejected. §17

## B3 — CLI

- [ ] `B3-01` `aff` as a gRPC client — **no privileged back door** past auth or validation. §11
- [ ] `B3-02` `aff login` storing a session locally.
- [ ] `B3-03` `aff feed list|get|create|update|enable|disable|delete`.
- [ ] `B3-04` `aff recipe export|import` (TOML). §7
- [ ] `B3-05` `aff sample <slug> [--size N] [--dry-run]` rendering results to the terminal. §11
- [ ] `B3-06` `aff promote <sample-id>`. §11
- [ ] `B3-07` `aff run <slug>` triggering a manual run and streaming progress. §11
- [ ] `B3-08` `aff runs [--feed] [--status]` history. §11
- [ ] `B3-09` `aff item list|get|create|update|delete|restore|correct`. §12.4
- [ ] `B3-10` `aff system stats|kill-switch|backup|version`. §11
- [ ] `B3-11` **Drive the full lifecycle of one feed end to end with only the CLI.** §18 B3

## BF — Flow sanity tests, headless (§22, §17.5)

These drive each flow end to end through the RPC layer, then assert the flow's invariants against
**resulting system state** — not against a mock's call log. This is the regression suite; it runs on
every commit. The UI walkthroughs in `DF` come later and do not replace these.

- [x] `BF-00` Harness: run a flow against a real store and RPC server, then assert on final state.
- [ ] `BF-01` **J1** login: exactly one unexpired session row exists. §22
- [ ] `BF-02` J1: cookie has `HttpOnly`, `Secure`, `SameSite=Strict`, `__Host-` prefix. §22
- [ ] `BF-03` J1: every attempt, success or failure, lands in `auth_events`. §22
- [ ] `BF-04` J1: wrong password and unknown user match in **message and timing**. §22
- [ ] `BF-05` J1: the TOTP step just used cannot be replayed. §22
- [ ] `BF-06` **J2** create: feed exists, disabled by default, zero items. §22
- [ ] `BF-07` J2: `jitter_offset` populated and deterministic from the slug. §22
- [ ] `BF-08` J2: next three runs are in the future and in the feed's timezone. §22
- [ ] `BF-09` J2: each of duplicate/reserved slug, bad cron, unknown tz, unknown template var,
      grounded-without-source, and zero budget is refused **server-side**. §22
- [ ] `BF-10` J2: no provider call was made and nothing was published. §22
- [x] `BF-11` **J3** sample: **`items` row count is unchanged.** The single most important one. §22
- [x] `BF-12` J3: a `samples` row exists with `expires_at` set. §22
- [x] `BF-13` J3: cost is non-zero and debited from the same budget scheduled runs use. §22
- [x] `BF-14` J3: returned XML fragment is byte-identical to what publishing emits. §22
- [x] `BF-15` J3: with the kill switch on, **no provider call is made at all**. §22
- [x] `BF-16` **J4** promote: exactly one new item, `origin = sampled`. §22
- [x] `BF-17` J4: `published_at` strictly greater than the previously newest item. §22
- [x] `BF-18` J4: fresh ULID, and the guid contains it. §22
- [ ] `BF-19` J4: render cache invalidated and `lastBuildDate` bumped. §22
- [x] `BF-20` J4: item appears exactly once in all three formats. §22
- [x] `BF-21` J4: a timestamp collision retries at +1s, no constraint error escapes. §22
- [ ] `BF-22` **J5** diagnose: every run reaches a terminal status; none left `running`. §22
- [ ] `BF-23` J5: `items_added + items_rejected` reconciles with recorded reasons. §22
- [ ] `BF-24` J5: a failed run has **zero** items attributable to it. §22
- [ ] `BF-25` J5: tokens and cost recorded even for failed runs — a failure that spent money shows it. §22
- [ ] `BF-26` **J6** correct: the original's guid and `published_at` are unchanged. §22
- [ ] `BF-27` J6: correction is a new item, new ULID, strictly later `published_at`. §22
- [ ] `BF-28` J6: the `corrections` row links the two. §22
- [ ] `BF-29` J6: the original is still resolvable at its permalink. §22
- [ ] `BF-30` J6: a plain edit produces no new guid and therefore no redelivery. §22
- [ ] `BF-31` **J7** recover: the consumed code is marked used and refused on reuse. §22
- [ ] `BF-32` J7: the elevated session reaches **only** password change and TOTP re-enrollment. §22
- [ ] `BF-33` J7: all other sessions were revoked. §22
- [ ] `BF-34` J7: remaining-code count decremented by exactly one. §22
- [ ] `BF-35` J7: the recovery attempt appears in `auth_events`. §22
- [ ] `BF-36` **J8** spend: sum of per-run `est_cost_usd` equals the reported total. §22
- [ ] `BF-37` J8: editing the price table does **not** rewrite historical run costs. §22
- [ ] `BF-38` J8: a feed at its cap logs a skipped run with a distinct status. §22
- [ ] `BF-39` J8: sampling spend appears in the same totals as scheduled spend. §22
- [ ] `BF-40` **J9** watch: the stream terminates when the run does, in every branch. §22
- [ ] `BF-41` J9: a dropped socket does **not** abort the run. §22
- [ ] `BF-42` J9: reconnecting shows true current state, not a stale snapshot. §22
- [ ] `BF-43` J9: progress events never claim items that were not committed. §22
- [ ] `BF-44` **J10** subscriber: feed validates with zero warnings in all three formats. §22
- [ ] `BF-45` J10: every item has a unique, strictly decreasing `pubDate`. §22
- [ ] `BF-46` J10: **each item delivered exactly once across many polls** — real HTTP, ≥2 cycles. §17.5
- [ ] `BF-47` J10: an unchanged feed answers `304`, touching neither SQLite nor the LLM. §22
- [ ] `BF-48` J10: a deleted item's permalink returns `410`, never `404`. §22
- [ ] `BF-49` J10: no trivia answer in `description` or `og:description`. §22
- [ ] `BF-50` J10: an edited item is not redelivered; a correction **is**. §22
- [ ] `BF-51` J10: a backdated item is delivered to nobody. RULE-7
- [ ] `BF-52` Wire the whole `BF` suite into CI as a required gate. §17.2

---

# Phase C — Ship the engine

## C0 — Container

- [x] `C0-01` Multi-stage `Dockerfile`; builder compiles server and WASM, runtime carries no toolchain. §15.1
- [x] `C0-02` `CGO_ENABLED=0` static build consistent with the A0-16 driver decision. §15.1
- [x] `C0-03` Stamp version, commit, and build date via `-ldflags -X`. §15.1
- [x] `C0-04` Runtime stage `gcr.io/distroless/static:nonroot`. §15.1
- [x] `C0-05` **Pre-create the data directory owned by the non-root user** — named-volume ownership. §15.1
- [x] `C0-06` `.dockerignore` excluding `.git`, local databases, and backups. §15.1
- [x] `C0-07` Build cache mounts so rebuilds are cheap. §15.1
- [ ] `C0-08` **Build locally with `--platform linux/amd64`** and confirm it runs. §15.2
- [ ] `C0-09` Confirm a native arm64 build fails on an amd64 host — know the error signature. §15.2
- [x] `C0-10` `compose.yaml`: publish to `127.0.0.1` only. §15.4, §19
- [x] `C0-11` Named volume for the database on local disk. §15.4
- [x] `C0-12` `env_file` at 0600 on the host; no secrets in compose or the image. §15.4
- [x] `C0-13` Healthcheck against `/healthz`. §15.4
- [x] `C0-14` `restart: unless-stopped`. §15.4
- [x] `C0-15` json-file log driver with size and file caps. §15.4
- [x] `C0-16` Memory limit. §15.4
- [x] `C0-17` `read_only: true` plus a tmpfs for `/tmp`. §15.4
- [x] `C0-18` `cap_drop: ALL` and `security_opt: no-new-privileges`. §15.4
- [ ] `C0-19` Verify the container survives a restart with the volume intact and the DB readable.

## C1 — Pipeline

- [ ] `C1-01` GitHub repo created and the local repo pushed. **Ask before creating.**
- [ ] `C1-02` Actions workflow: checkout, test, `make validate`, then build.
- [ ] `C1-03` **A failing test must fail the job before any push.** §15.3
- [ ] `C1-04` Push to GHCR authenticated with `GITHUB_TOKEN`. §15.2
- [ ] `C1-05` Tag with `sha-<short-commit>` on every build — the immutable one. §15.2
- [ ] `C1-06` Tag `v<semver>` on release tags only. §15.2
- [ ] `C1-07` Tag `latest` on `main`, and **never deploy it**. §15.2
- [ ] `C1-08` Layer caching between runs.
- [ ] `C1-09` **Deploy job in the same workflow run** — `GITHUB_TOKEN` pushes trigger nothing. §15.3
- [ ] `C1-10` Deploy over SSH with a scoped deploy user and a known host key. §16
- [ ] `C1-11` Deploy writes the new tag, then `docker compose pull && up -d`. §15.3
- [ ] `C1-12` **Deploy waits on the healthcheck and fails the job if it never goes healthy.** §15.3
- [ ] `C1-13` Scheduled `docker image prune` so a 60 GB disk does not fill quietly. §15.3
- [ ] `C1-14` Install Docker and the compose plugin on the droplet.
- [ ] `C1-15` Confirm Docker's DNAT rules do not expose anything past `ufw`. §19

## C2 — Staging

- [ ] `C2-01` DNS for `staging.anime.earlcameron.com`. §18 C2
- [ ] `C2-02` nginx vhost proxying to the container's loopback port. §15
- [ ] `C2-03` TLS certificate issued and renewing.
- [x] `C2-04` `proxy_http_version 1.1` with `Upgrade`/`Connection` headers. §20
- [x] `C2-05` `proxy_read_timeout` long enough to outlive an idle admin session. §20
- [x] `C2-06` **`proxy_buffering off`** or streaming RPCs arrive in one lump. §20
- [ ] `C2-07` Deploy the current image to staging and confirm feeds are publicly fetchable.
- [ ] `C2-08` Run the external feed validator against the **live** staging URL. §5.6

## C3 — Slack proof

- [ ] `C3-01` Create a private Slack workspace or channel for testing. §5.5
- [ ] `C3-02` `/feed subscribe` the staging RSS URL. §5.5
- [ ] `C3-03` Confirm a generated item posts at all. §5.5
- [ ] `C3-04` Confirm multiple items from one run **all** post (the duplicate-timestamp trap). §5.5
- [ ] `C3-05` Confirm no item posts twice across a week of polls. §5.5
- [ ] `C3-06` Confirm the unfurl renders title, summary, and image from the OG tags. §5.5
- [ ] `C3-07` Confirm a trivia answer is **not** visible in the channel. §5.5
- [ ] `C3-08` Confirm editing an item does **not** repost it. §5.5
- [ ] `C3-09` Confirm a correction **does** appear as a new item. §5.5
- [ ] `C3-10` Confirm a backdated item never appears — then stop creating them. RULE-7
- [ ] `C3-11` Record the observed Slack poll interval for the staleness grace factor. §15

## C4 — Ops

- [ ] `C4-01` In-process nightly backup: `VACUUM INTO` plus an integrity check on the copy. §15
- [ ] `C4-02` Retain 14 days locally. §15
- [ ] `C4-03` **Encrypt and ship the copy off the box.** §15
- [ ] `C4-04` `SystemService.Backup` on-demand download. §11
- [ ] `C4-05` Alert on backup failure — its failure is otherwise invisible. §15
- [ ] `C4-06` **Restore into a scratch instance and confirm identical feeds.** §19
- [ ] `C4-07` Staleness watchdog comparing last success against schedule plus grace. §15
- [ ] `C4-08` Surface stale feeds on `/healthz` and via the Slack webhook. §15
- [ ] `C4-09` Graceful shutdown: stop new runs, drain, checkpoint WAL, exit. §15
- [ ] `C4-10` Mark runs still active at the shutdown deadline as interrupted. §15
- [ ] `C4-11` Boot watchdog releases stale run locks. §15
- [ ] `C4-12` A stale run found **with** committed items is `completed_unconfirmed`, not a failure. §15
- [ ] `C4-13` Nightly prune: expired samples, old embeddings, `runs` past 180 days except failures. §15
- [ ] `C4-14` Test: kill after the model returns but before commit → interrupted, zero items. §17
- [ ] `C4-15` Expose per-feed last-success age and error counts on `/healthz`. §15
- [ ] `C4-16` Export `aff_feed_staleness_seconds` so the watchdog's number is graphable. §15.0a
- [ ] `C4-17` Point `OTEL_EXPORTER_OTLP_ENDPOINT` at a hosted backend; **no local collector** on a 2GB box. §15.0a
- [ ] `C4-18` Verify a failing exporter degrades silently and does not stall a run. §15.0a
- [ ] `C4-19` Confirm providers flush on SIGTERM before the container exits. §15

## C5 — Production deploy

- [ ] `C5-01` DNS for `anime.earlcameron.com` and `admin.anime.earlcameron.com`. §2
- [ ] `C5-02` nginx vhosts for both, TLS on both. §15
- [ ] `C5-03` **IP allowlist on the admin host** (home IP). §4
- [ ] `C5-04` Confirm the admin host is unreachable from off-allowlist.
- [ ] `C5-05` Production compose pins a `sha-` tag, never `latest`. §15.2
- [ ] `C5-06` Production `env_file` with real secrets at 0600. §15.4
- [ ] `C5-07` First production deploy; feeds live and validating.
- [ ] `C5-08` **Perform an actual rollback to the previous tag and confirm service.** §18
- [ ] `C5-09` Confirm a push to `main` reaches the running service with no manual step. §19
- [ ] `C5-10` Point Slack at production and confirm continuity. §5.5

---

# Phase D — UI, last

## D-FLOW — The internal UX flow these tasks derive from

The ten journeys `J1`–`J10` are defined canonically in **§22**, with their sanity assertions. This
section adds only what the *interface* needs on top of them: application states, the per-screen
state matrix, and navigation. Do not add a screen or control that serves no journey in §22; if one
is needed, amend §22 first so the flow stays the source of truth.

**Application states.** The UI is always in exactly one:

| State | How it is entered | What is reachable |
|---|---|---|
| `ANON` | No session, or session expired | `/login`, `/recover` |
| `AUTH` | Password + TOTP accepted | `/generate`, `/history`, `/settings` |
| `ELEVATED` | Valid recovery code, 10-minute window | Password change and TOTP re-enrollment **only** |
| `DISCONNECTED` | WebSocket dropped while in `AUTH` | Read-only view of loaded data, reconnect banner |
| `KILLED` | Generation kill switch is off | Everything except generate/sample actions |

`DISCONNECTED` is not an edge case. This is a WASM client whose entire API is one WebSocket; laptop
sleep, nginx timeout, and deploys all produce it. Designing it last produces an app that lies about
being connected.

**Journeys.** Summarized here; defined with preconditions, failure branches, and sanity assertions
in §22. `J10` is the subscriber's flow, not the admin's, and is the one that matters most.

| | Journey | Surface |
|---|---|---|
| `J1` | First login → land on `/generate` | `/login` |
| `J2` | Create a feed from nothing → validated recipe | `/generate` |
| `J3` | Iterate a prompt → sample → read verdicts → adjust | `/generate` |
| `J4` | Promote a good sample → item live in the feed | `/generate` |
| `J5` | Diagnose a bad run → read reject reasons | `/history` |
| `J6` | Correct a wrong item → publish a correction, not an edit | `/history` |
| `J7` | Locked out → recovery code → reset → re-login | `/recover` |
| `J8` | Review spend → adjust budgets → confirm enforcement | `/settings` |
| `J9` | Watch a run stream live | `/generate`, `/history` |
| `J10` | Subscriber: discover → subscribe → receive → unfurl | *no UI — the feed itself* |

Break-glass over SSH (§12.2) is deliberately **not** a journey: it exists precisely for when no
interface is reachable, and `/recover` documents it rather than implementing it.

**Per-screen state matrix.** Every list and panel implements all six, or it is not done:
`loading` · `empty` · `populated` · `error` · `disabled-with-reason` · `disconnected`.

**Navigation.** Three top-level destinations in `AUTH`. No nested navigation, no hamburger, no
breadcrumb — three pages do not need wayfinding.

## D0 — Shell

- [ ] `D0-01` GWC (v5 pin) project set up under `web/`, building to WASM. §3
- [ ] `D0-02` WASM build in an isolated scratch directory — the known concurrent-build race. §15
- [ ] `D0-03` Emit `.wasm.gz` and serve with correct `Content-Encoding`. §12
- [ ] `D0-04` HTML shell with a correct **`<base href>`** or deep links and refreshes break. §12
- [ ] `D0-05` Client-side router for the five routes. §12
- [ ] `D0-06` gRPC-over-WS client wired to the bridge. §3
- [ ] `D0-07` Auth guard: `ANON` reaching an authed route redirects to `/login`. D-FLOW
- [ ] `D0-08` Session expiry mid-session drops to `ANON` without losing unsaved work silently. D-FLOW
- [ ] `D0-09` **`DISCONNECTED` banner with automatic reconnect and backoff.** D-FLOW
- [ ] `D0-10` Queue or refuse mutations while `DISCONNECTED` — never fail silently. D-FLOW
- [ ] `D0-11` Design tokens: colour, type scale, spacing. Load the frontend-design skill first.
- [ ] `D0-12` Light and dark handling decided once, at the token layer.
- [ ] `D0-13` Shared primitives: button, input, select, toggle, table, tabs, modal, toast, kebab menu.
- [ ] `D0-14` Shared `loading` / `empty` / `error` components used by every list. D-FLOW
- [ ] `D0-15` Destructive actions live behind a `⋯` kebab, never a primary button. §12.6
- [ ] `D0-16` Typed-confirmation modal for irreversible actions. §12.6
- [ ] `D0-17` GWC discipline: hooks unconditional and positional. §12.6
- [ ] `D0-18` No `UseAtom` in render-only paths. §12.6
- [ ] `D0-19` Declared effect deps; no browser-state reads in the render body. §12.6
- [ ] `D0-20` No i18n — single user, English, explicitly out of scope. §12.6

## D1 — Auth pages (`J1`, `J7`)

- [ ] `D1-01` `/login`: password step then TOTP step, one page. §12.1
- [ ] `D1-02` **One generic error string for every failure** — no oracle. §12.1
- [ ] `D1-03` Surface backoff honestly ("try again in 30s"). §12.1
- [ ] `D1-04` Disable submit while in flight; no double-submit burning a TOTP window. §12.1
- [ ] `D1-05` Replace history state on success so Back cannot land on a stale form. §12.1
- [ ] `D1-06` No "remember me". §12.1
- [ ] `D1-07` `/recover`: state plainly that there are exactly two paths. §12.2
- [ ] `D1-08` Recovery-code entry → `ELEVATED` → password reset and TOTP re-enrollment. §12.2
- [ ] `D1-09` Show remaining recovery-code count after use. §12.2
- [ ] `D1-10` Document the `aff admin reset` break-glass **on the page**. §12.2
- [ ] `D1-11` `ELEVATED` cannot navigate to `/generate`, `/history`, or `/settings`. D-FLOW
- [ ] `D1-12` **Perform a full recovery drill against staging.** §19

## D2 — Generate (`J2`, `J3`, `J4`, `J9`)

- [ ] `D2-01` Three-pane layout: rail, editor, sampler. §12.3
- [ ] `D2-02` Rail: status, last build, next run in local time, item count, 7-day spend. §12.3
- [ ] `D2-03` Rail flags stale feeds inline. §15
- [ ] `D2-04` Rail: enable toggle, Run Now, new feed. §12.3
- [ ] `D2-05` Rail: all six states from the matrix. D-FLOW
- [ ] `D2-06` Editor: slug, title, description, language, kind. §12.3
- [ ] `D2-07` Slug is **immutable after first publish**, and the UI says why. §14.1
- [ ] `D2-08` Editor: cron plus timezone with plain-English readback. §12.3
- [ ] `D2-09` Show the next three runs in local time, **jittered**, not nominal. §14.3
- [ ] `D2-10` Editor: model and parameters. §12.3
- [ ] `D2-11` Editor: system and user prompt templates with the variable list inline. §7
- [ ] `D2-12` Editor: novelty settings and budgets. §12.3
- [ ] `D2-13` Editor: source list for grounded feeds. §12.3
- [ ] `D2-14` Server-side validation errors render **against the offending field**. §12.3
- [ ] `D2-15` Unsaved-changes guard on navigation. §12.3
- [ ] `D2-16` Optimistic-concurrency conflict shows a real merge choice, not a silent clobber. §11
- [ ] `D2-17` Sampler: size 1–5 and optional temperature override. §12.3
- [ ] `D2-18` Sampler streams output with a working cancel. §12.3
- [ ] `D2-19` Candidate view: **rendered**. §12.3
- [ ] `D2-20` Candidate view: **raw validated fields**. §12.3
- [ ] `D2-21` Candidate view: **exact feed XML**. §12.3
- [ ] `D2-22` Candidate view: **Slack card preview** — the thing that actually gets read. §12.3
- [ ] `D2-23` Novelty verdict with the nearest existing item shown. §12.3
- [ ] `D2-24` Grounded: candidate source set with failed links flagged and the URL shown. §12.3
- [ ] `D2-25` Cost per sample and remaining daily budget. §12.3
- [ ] `D2-26` Sample button always shows estimated cost before it is clicked. §12.3
- [ ] `D2-27` Kill switch disables sampling **with a visible reason**, not a dead control. §12.3
- [ ] `D2-28` Promote and Discard; nothing publishes implicitly. §12.3
- [ ] `D2-29` Samples survive a page refresh for 24h. §12.3
- [ ] `D2-30` Live run progress streams via `Watch`. §12.4

## D3 — History (`J5`, `J6`)

- [ ] `D3-01` Two tabs over one page: Runs and Items. §12.4
- [ ] `D3-02` Runs: status, trigger, duration, items added/rejected, tokens, cost, error kind. §12.4
- [ ] `D3-03` Runs: filter by feed, status, date range. §12.4
- [ ] `D3-04` Runs: expand to the full log. §12.4
- [ ] `D3-05` Runs: in-flight run streams live. §12.4
- [ ] `D3-06` Runs: delete allowed, **edit is not**. §12.4
- [ ] `D3-07` Runs: show reject reasons so `J5` can actually be completed. §10
- [ ] `D3-08` Items: FTS5 search. §12.4
- [ ] `D3-09` Items: filter by feed, origin, date, deleted state. §12.4
- [ ] `D3-10` Items: pagination. §11
- [ ] `D3-11` Items: create a manual item. §12.4
- [ ] `D3-12` Items: edit title, summary, body, link, tags, publish date. §12.4
- [ ] `D3-13` **State in the UI that the guid never changes on edit, and why.** §12.4
- [ ] `D3-14` Block backdating `published_at`; warn loudly on override. §5.5
- [ ] `D3-15` Items: revision history with a diff view and revert. §12.4
- [ ] `D3-16` Items: soft delete and restore. **No purge control exists.** §12.4
- [ ] `D3-17` **"Publish a correction" sits next to Delete, not three menus away.** §12.4
- [ ] `D3-18` State plainly that RSS has no retraction. §12.4
- [ ] `D3-19` Bulk select for delete and restore. §12.4
- [ ] `D3-20` Every mutation visibly refreshes the affected feed's state. RULE-6

## D4 — Settings (`J8`)

- [ ] `D4-01` Security: change password (current password + TOTP). §12.5
- [ ] `D4-02` Security: re-enroll TOTP. §12.5
- [ ] `D4-03` Security: regenerate recovery codes, showing remaining count. §12.5
- [ ] `D4-04` Security: active sessions with device, IP, last-seen; revoke one or all. §12.5
- [ ] `D4-05` Provider: active provider and default model for new feeds. §12.5
- [ ] `D4-06` Provider: **key presence only** — never displayed, never sent to the client. §12.5
- [ ] `D4-07` Provider: editable price table used for cost estimates. §12.5
- [ ] `D4-08` Generation: kill switch, global ceiling, default budgets, staleness threshold. §12.5
- [ ] `D4-09` Publishing: base URL, author, copyright, TTL, default `og:image`, validated on save. §12.5
- [ ] `D4-10` Data: TOML export/import, backup download, DB size, item counts, vacuum. §12.5
- [ ] `D4-11` About: version, build, uptime, last successful run per feed. §12.5

## D5 — Polish

- [ ] `D5-01` Responsive breakpoints land **in the same commit** as each layout. §12.6
- [ ] `D5-02` Audit every list against the six-state matrix. D-FLOW
- [ ] `D5-03` Keyboard path through every journey; visible focus states.
- [ ] `D5-04` Colour contrast checked in both themes.
- [ ] `D5-05` Walk `J1`–`J9` end to end in a browser and fix what is awkward.
- [ ] `D5-06` Confirm nothing in the UI can reach a state the flow table does not name. D-FLOW

## DF — Flow sanity walkthroughs, through the UI (§22, §17.5)

The `BF` suite already proves the system stays coherent. `DF` proves the **interface can actually
complete each flow a human is meant to complete, including its failure branches** — a different
question, and not answerable by the headless suite.

- [ ] `DF-01` `J1` login through the UI, including every failure branch. §22
- [ ] `DF-02` `J2` create a feed from nothing; every validation error renders on its field. §22
- [ ] `DF-03` `J3` iterate a prompt: sample, read all verdicts, adjust, sample again. §22
- [ ] `DF-04` `J3` failure branch: kill switch on shows a **reason**, not a dead control. §12.3
- [ ] `DF-05` `J4` promote a sample and see it appear in the feed. §22
- [ ] `DF-06` `J5` diagnose a deliberately broken run; reject reasons are readable. §22
- [ ] `DF-07` `J6` publish a correction **without** first being tempted to edit. §22
- [ ] `DF-08` `J7` full recovery drill through the UI. §22
- [ ] `DF-09` `J8` review spend and adjust a budget; enforcement is visible. §22
- [ ] `DF-10` `J9` watch a live run, **drop the WebSocket mid-run**, reconnect, see true state. §22
- [ ] `DF-11` `J10` subscribe a real reader to the real URL and observe two poll cycles. §17.5
- [ ] `DF-12` Every `DF` flow re-walked at the narrowest supported breakpoint. §12.6

---

# Phase E — After

- [ ] `E0-01` Subscribe ArticleFlux to the production feeds. §18
- [ ] `E0-02` Verify rendering, dedup by guid, and refresh behavior there. §18
- [ ] `E1-01` **Deferred until a 4th or 5th feed exists.** Aggregate feeds. §14.2
- [ ] `E1-02` Deferred. Shared upstream source cache. §14.3
- [ ] `E1-03` Deferred. Bounded LRU render cache with a byte ceiling. §14.3
- [ ] `E1-04` Deferred. Rail search, filter, and pagination past ~40 feeds. §14.3
- [ ] `E1-05` Deferred. Per-feed published identity overrides. §14.1

---

# Using the app

## U0 — First-run setup

- [ ] `U0-01` `aff admin init`; store the passphrase in a password manager.
- [ ] `U0-02` Enroll TOTP; **save the recovery codes somewhere that is not this machine.**
- [ ] `U0-03` Set the publishing defaults: base URL, author, copyright, `og:image`. §12.5
- [ ] `U0-04` Set the global daily spend ceiling before creating any feed. §13
- [ ] `U0-05` Create `anime-trivia-daily`; iterate the prompt with sampling until it is good. §20
- [ ] `U0-06` Create `anime-fact-daily`. §20
- [ ] `U0-07` Create `anime-news-daily` with ANN and Crunchyroll sources. §20
- [ ] `U0-08` Decide digest vs separate items for news — currently assumed 3 separate. §20
- [ ] `U0-09` Subscribe Slack to all three. §5.5
- [ ] `U0-10` Subscribe ArticleFlux to all three. §18
- [ ] `U0-11` Watch the first week daily; do not assume it is fine because it launched.

## U1 — Recurring operations

- [ ] `U1-01` Weekly: read the run history for failures and skipped-for-novelty runs. §12.4
- [ ] `U1-02` Weekly: skim published trivia for factual errors; correct what is wrong. §12.4
- [ ] `U1-03` Weekly: confirm every feed built and none is stale. §15
- [ ] `U1-04` Monthly: review spend against the ceiling and per-feed attribution. §13
- [ ] `U1-05` Monthly: confirm the nightly backup ran and the off-box copy exists. §15
- [ ] `U1-06` Monthly: check remaining recovery-code count. §12.5
- [ ] `U1-07` Quarterly: re-check the price table against published prices. §12.5
- [ ] `U1-08` Quarterly: audit grounded links for rot (advisory only — not a defect). §19
- [ ] `U1-09` Quarterly: confirm the novelty gate is still catching repeats as the corpus grows. §19
- [ ] `U1-10` On any model deprecation notice, re-pin the model and re-sample every recipe. §19

## U2 — Drills, performed not described

- [ ] `U2-01` Restore drill: restore a backup into a scratch instance; confirm identical feeds. §19
- [ ] `U2-02` Rollback drill: deploy, roll back to the previous tag, confirm service. §18
- [ ] `U2-03` Recovery drill: lock yourself out, recover with a code, reset, re-login. §19
- [ ] `U2-04` Break-glass drill: `aff admin reset` over SSH. §12.2
- [ ] `U2-05` Kill-switch drill: disable generation, confirm feeds still serve. §13
- [ ] `U2-06` Staleness drill: stop a feed generating and confirm the alert actually fires. §15
- [ ] `U2-07` Re-run each drill after any change to auth, deploy, or backup.

---

# OQ — Open questions that gate work (§21)

These are decisions, not tasks, and each one blocks something concrete. Left undecided they will be
resolved by accident, which is the worst way. Each names what it blocks.

- [ ] `OQ-01` Confirm the three launch feeds. Blocks `U0-05`…`U0-07`. §21.1
- [ ] `OQ-02` **Public or private feeds?** Private needs per-subscriber URL tokens and changes the
      §5.4 caching design and the §2 unauthenticated plane. Blocks `A9-01`. Decide before A9. §21.2
- [ ] `OQ-03` Confirm the grounded source list beyond ANN and Crunchyroll. Blocks `U0-07`. §21.3
- [ ] `OQ-04` News cadence: one digest item per day, or N separate items? Currently assumed 3
      separate, which reads better in Slack. Blocks `A6-13`. §21.4
- [ ] `OQ-05` Record each answer in `PLAN.md` §21 as decided, with the date and the reason.

# Definition of done — v1 (§19)

- [ ] `DOD-1` Three feeds live.
- [ ] `DOD-2` All three validate clean in all three formats, zero warnings.
- [ ] `DOD-3` Slack: 7 days, every item posts exactly once, no dupes, no misses, no spoilers.
- [ ] `DOD-4` 30 consecutive days of production trivia with no near-duplicate pairs.
- [ ] `DOD-5` Zero invented URLs, audited against the candidate set at generation time.
- [ ] `DOD-6` Admin reachable only from the allowlisted IP with password + TOTP; drill passed.
- [ ] `DOD-7` Monthly spend under the ceiling with per-feed attribution.
- [ ] `DOD-8` A backup has been restored and serves identical feeds.
- [ ] `DOD-9` A push to `main` reaches production, and a rollback has been performed.
