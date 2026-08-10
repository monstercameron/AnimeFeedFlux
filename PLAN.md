# AnimeFeedFlux — Plan

## 1. The bet

Any RSS reader can subscribe to a feed. Nothing stops that feed from being *written on demand*.
AnimeFeedFlux is a feed **generator**: you declare a recipe ("a daily anime trivia question",
"today's anime news, ranked"), it runs on a schedule, calls an LLM, and publishes the result at a
stable URL as spec-compliant RSS/Atom/JSON Feed. The consumer is any reader — including ArticleFlux,
which becomes the front end for free.

The product surface is: **a URL that returns valid XML and never lies.**

Two item classes, with different rules. Conflating them is the main way this project fails.

- **Generative** (trivia, fact-of-the-day, "on this day in anime"). The LLM *is* the source.
  Content is authored by us, hosted by us, permalinked to us. Risk = repetition and wrong facts.
- **Grounded** (news, releases, seasonal roundups). The LLM is only an editor. Every link must
  resolve to a real article we fetched ourselves. Risk = hallucinated URLs, handled architecturally
  rather than by prompting.

## 2. Two planes (the architectural spine)

"gRPC, no HTTP" holds for the *application*, but it cannot hold for the feeds themselves — RSS
clients speak HTTP/XML and nothing else. So the system splits cleanly in two, and the split is the
security boundary:

| | **Publish plane** | **Control plane** |
|---|---|---|
| Transport | Plain HTTPS, `GET`/`HEAD` only | gRPC over WebSocket (GoGRPCBridge) |
| Payload | RSS 2.0 / Atom 1.0 / JSON Feed | protobuf |
| Client | Any feed reader, unauthenticated | GWC WASM admin app, authenticated |
| Host | `anime.earlcameron.com` | `admin.anime.earlcameron.com`, IP-allowlisted |
| Writes | none — physically impossible | all of them |
| Process | same binary, separate listener + mux | same binary |

There is **no REST/JSON API**. The publish plane serves exactly four route shapes
(`/feeds/{slug}.{xml,atom,json}`, `/items/{id}`, `/healthz`, `/favicon.ico`) and holds a read-only
handle on the store. Everything mutable — recipes, prompts, manual runs, the kill switch — is an
RPC. A bug in the publish plane cannot write to the database because that code path has no writer.

## 3. Stack

Standard earlcameron shape, so the ops story is boring:

- **UI:** GoWebComponents (pin v5, per the existing `earlcameron` pin) compiled to WASM. Admin only.
- **Transport:** GoGRPCBridge — gRPC-over-WebSocket, so the WASM client speaks real gRPC. Watch the
  known keepalive/GOAWAY flap: pair client keepalive with server `EnforcementPolicy` explicitly.
- **Backend:** Go, `net/http` for the publish listener, `grpc-go` behind the bridge for control.
- **Store:** SQLite (single file, WAL, `_busy_timeout`), FTS5 registered for item search in the
  admin UI.
- **LLM:** `internal/llm` provider interface; the only v1 implementation is the official OpenAI Go
  SDK (`openai-go`), using structured outputs (JSON schema) and the embeddings endpoint.

### Layout

```
proto/aff/v1/            feed.proto, run.proto, auth.proto  (buf, generated into gen/)
cmd/animefeedflux/       server: publish listener + bridge listener + scheduler
cmd/aff/                 CLI (gRPC client — same API as the UI, no back door)
internal/auth/           argon2id, sessions, TOTP, rate limiting
internal/config/         env only, no secrets on disk
internal/store/          SQLite schema, migrations, reader/writer split
internal/feedspec/       recipe validation, prompt templates
internal/llm/            Provider interface; llm/openai implementation
internal/generate/       generators: trivia, fact, onthisday, news
internal/sources/        upstream feed fetch, parse, URL verification
internal/render/         RSS 2.0 / Atom 1.0 / JSON Feed serializers
internal/scheduler/      cron loop, single-flight, injectable clock
internal/publish/        the read-only HTTP plane + cache
internal/rpc/            gRPC service implementations
web/                     GWC admin app (WASM) + shell
testdata/                golden feeds, canned LLM responses, canned upstream RSS
```

## 4. Security and authn

Single human user, who is the admin. **No authorization model** — one role, everything or nothing.
That makes authentication the entire defense, so it gets built properly rather than sketched.

- **Credential:** password hashed with **argon2id** (not bcrypt — no 72-byte truncation, memory-hard
  params tunable), stored in SQLite. Verification is constant-time. No default password; the account
  is created by a `aff admin init` command that reads from stdin and refuses a weak passphrase.
- **Second factor: TOTP (RFC 6238), required, not optional.** With a single admin and a public
  droplet, a leaked password otherwise loses everything. Recovery codes are generated once at
  enrollment, shown once, stored hashed.
- **Sessions:** 256-bit random token, hashed at rest, delivered as `HttpOnly; Secure; SameSite=Strict`
  cookie scoped to the admin host. Absolute lifetime 12h, idle timeout 60m, rotation on login.
- **Bridge auth:** the WebSocket upgrade validates the session cookie *and* checks `Origin`. Every
  RPC additionally passes the session through a server interceptor — no "authenticated at upgrade,
  trusted forever". Session revocation kills in-flight streams.
- **Network:** the admin listener is bound to its own port behind Caddy with an IP allowlist (home
  IP, same pattern as the droplet SSH rule). The publish listener is the only thing open to the
  world, and it is read-only and unauthenticated by design.
- **Login hardening:** per-IP and per-account exponential backoff, generic failure message, uniform
  timing on unknown-user vs bad-password, all attempts logged to an `auth_events` table.
- **Secrets:** `OPENAI_API_KEY` from the environment (systemd `EnvironmentFile`, mode 0600), never
  in the DB, never in a recipe, never logged. Redaction filter on the log writer.
- **Untrusted input:** LLM output and upstream RSS are both hostile input. Sanitize HTML through an
  allowlist before storage, escape again at render, and never interpolate model text into XML
  without going through the encoder.

Explicitly out of scope: roles, per-feed permissions, OAuth, user management, invite flows.

## 5. Feed specification compliance

Researched against the RSS 2.0 spec, the RSS Advisory Board's Best Practices Profile, and RFC 4287.
These are implementation requirements, not aspirations, and each gets a golden-file test.

### RSS 2.0

- `channel` **requires** `title`, `link`, `description`. We also emit `language`, `pubDate`,
  `lastBuildDate`, `generator`, `docs`, `ttl`, and `atom:link rel="self"`.
- `item`: the spec says *all* item elements are optional but **at least one of `title` or
  `description` must be present**. We always emit both, plus `guid`, `pubDate`, `link`.
- **`guid`**: `isPermaLink` **defaults to `true`** — a silent default that bites people. We emit an
  explicit `isPermaLink="false"` and use a **Tag URI** (`tag:anime.earlcameron.com,2026:<slug>/<hash>`),
  which the Best Practices Profile endorses. Rationale: a guid must be stable forever, and neither
  our permalink scheme nor an upstream article URL is guaranteed stable. The human-facing permalink
  lives in `link`, where it belongs.
- **Dates**: RFC 822 with **four-digit years**, UTC only — `Thu, 04 Oct 2007 23:59:45 +0000`. No
  military timezone abbreviations, no double spaces, no comments. One formatter function, used
  everywhere, unit-tested against the profile's examples.
- **Escaping**: HTML in `description` goes in a CDATA section; plain-text elements use **hexadecimal
  character references** (`&#x26;`, `&#x3C;`), which the profile found more widely supported than
  named entities. **No relative URLs anywhere** — RSS has no base-URL mechanism, so every href in
  generated HTML is absolutized before storage.
- `content:encoded` (namespace `http://purl.org/rss/1.0/modules/content/`) carries full item HTML,
  with `description` holding the summary and **ordered first** in the item, per the profile.
- If a channel `image` is used: max 144×400 px, requires `url`/`title`/`link`.
- `atom:link rel="self"` carries `type="application/rss+xml"` and the feed's absolute URL.

### Atom 1.0 (RFC 4287)

- `feed` MUST have exactly one `id`, `title`, `updated`, and MUST have `author` unless every entry
  carries its own. `entry` MUST have exactly one `id`, `title`, `updated`.
- `id` MUST be an IRI created so as to be unique, and is compared **character-by-character,
  case-sensitively** by consumers — the same Tag URI as the RSS guid, byte-identical.
- Dates are **RFC 3339** with an uppercase `T` separator and uppercase `Z` when there is no numeric
  offset. This is a *different* format from RSS's RFC 822; two formatters, never crossed.
- An entry with no `content` MUST carry a `link rel="alternate"`. We always emit both.
- `feed` SHOULD carry `link rel="self"`.

### Transport

- `Content-Type: application/rss+xml; charset=utf-8`, `application/atom+xml; charset=utf-8`,
  `application/feed+json` respectively — not `text/xml`.
- XML declaration with explicit `encoding="utf-8"`; UTF-8 output; no BOM.
- **Conditional GET is mandatory.** Strong `ETag` (hash of the exact rendered body) plus
  `Last-Modified`; honor `If-None-Match` and `If-Modified-Since` with `304`. Readers poll
  relentlessly and a 304 must not touch SQLite, let alone the LLM.
- `Cache-Control: max-age=900`, gzip, `<ttl>15</ttl>` — consistent numbers, not three guesses.
- Feed window capped (default 50 items / ~512 KB); older items stay in the DB and are reachable via
  their permalinks.

### Verification

A `make validate` target runs the rendered golden files through the W3C/RSS Advisory Board feed
validator, and CI asserts zero errors *and* zero warnings for RSS, Atom, and JSON Feed. Golden files
include the ugly cases: ampersands, `<` in titles, CJK, emoji, an item with HTML in the summary.

## 6. Recipes

With an admin UI editing prompts, **SQLite is the source of truth** for recipes. TOML import/export
(`aff recipe export|import`) exists for versioning and disaster recovery, not as the live path.

A recipe carries: slug, title, description, language, kind (`generative` | `grounded`), cron
schedule (UTC), items per run, retention window, model params, system+user prompt templates, novelty
settings, per-day token/run budgets, and — for grounded feeds — one or more source URLs.

Validation runs on save (server side, in the RPC — the UI's copy is a convenience, never the
gate): slug URL-safe and unique, cron parses, templates compile, grounded implies ≥1 source,
budgets non-zero. An invalid recipe disables that feed alone and never takes the server down.

## 7. Generation contract

Every generator returns the same shape, enforced by JSON-schema structured output on the model call
and then **re-validated in Go** — never trust the model to honor its own schema.

```
Item {
  title        required, 10..200 chars
  body_html    required, allowlist-sanitized (p, em, strong, a, ul, li, blockquote)
  link         optional for generative, REQUIRED for grounded
  source_name  grounded only
  tags         0..6
  answer_html  trivia only — rendered below a spoiler break
}
```

Per run:

1. **Acquire context.** Generative: the last N item titles as an exclusion list. Grounded: fetch all
   sources (conditional GET against *them* too, storing their ETags), parse, keep entries newer than
   the last run, cap at ~40 candidates.
2. **Call the model**, structured output. Record tokens in/out and estimated cost on the run.
3. **Validate** against the Go schema. Malformed output rejects the whole run; retry once.
4. **Sanitize** HTML through the allowlist; absolutize every URL.
5. **Novelty check** (generative): embed title+body, compare against the last 500 embeddings, cosine
   above threshold → discard and retry up to N times, then skip the run and log it. Repetition is
   what kills a daily trivia feed, and prompting alone will not prevent it at 200 items.
6. **Link integrity** (grounded): the item's `link` MUST be byte-equal to a URL in the fetched
   candidate set. Not "similar to" — present. Failures are dropped and counted. Optional second
   check: `GET` returns 200 with a non-empty title.
7. **Persist** with the Tag URI guid derived from `sha256(slug | normalized_title | date)`.
8. **Invalidate** the render cache for that slug; bump `lastBuildDate`.

Failure mode is always "the feed keeps its previous items and logs an error run" — never a partial
feed, never an invented link.

## 8. Data model

```
admin(id, password_hash, totp_secret_enc, created_at, password_changed_at)
recovery_codes(id, code_hash, used_at)
sessions(id, token_hash, created_at, last_seen_at, expires_at, ip, user_agent, revoked_at)
auth_events(id, at, kind, ip, ok, detail)

feeds(id, slug UNIQUE, title, description, language, kind, spec_json, enabled,
      last_built_at, created_at, updated_at)
items(id, feed_id, guid UNIQUE, title, summary_html, body_html, link, source_name,
      published_at, model, prompt_hash, tokens_in, tokens_out, created_at)
item_embeddings(item_id, vec BLOB, dim)
runs(id, feed_id, started_at, finished_at, status, error, items_added, items_rejected,
     tokens_in, tokens_out, est_cost_usd, trigger)     -- trigger: cron | manual | backfill
sources(id, feed_id, url, kind, last_fetched_at, last_etag, last_modified)
```

`runs` is the operational spine — cost, drift, and "why is this feed stale" all resolve there, and
it is the main screen of the admin UI.

## 9. Control-plane API (proto sketch)

- `AuthService`: `Login` (password + TOTP), `Logout`, `Session`, `ChangePassword`.
- `FeedService`: `List`, `Get`, `Create`, `Update`, `SetEnabled`, `Delete`, `Preview`, `RunNow`.
- `RunService`: `History`, `Watch` (server-stream of live run progress/log lines).
- `SystemService`: `Stats`, `SetGenerationEnabled` (the global kill switch), `Version`.

`Preview` is the important one: a full dry run that returns the item it *would* publish, plus the
rendered XML fragment, writing nothing. That is the loop for iterating prompts, and it is what keeps
spend down. The CLI is a gRPC client against these same services — no privileged back door that
bypasses auth or validation.

## 10. Admin UI (GWC)

Four screens, no more:

1. **Feeds** — table with status, last build, next run, item count, 7-day spend; toggle and Run Now.
2. **Recipe editor** — prompt templates, model params, schedule, budgets, sources; **Preview** pane
   showing the generated item and the rendered XML side by side.
3. **Runs** — history with status/tokens/cost/rejections, filterable; live `Watch` stream.
4. **System** — kill switch, totals, session management, version.

GWC discipline applies as usual: hooks unconditional and positional, no `UseAtom` in render-only
paths, effect deps declared, and no reading browser state from the render body. Responsive
breakpoints land in the same commit as the layout.

## 11. Cost and blast radius

- Per-feed daily token and run caps enforced **before** the call; exceeding logs a skipped run.
- Global kill switch (`SystemService.SetGenerationEnabled`, plus `AFF_GENERATION_ENABLED=0` for a
  cold start): existing feeds keep serving, nothing generates.
- Scheduler is single-flight per feed via a DB run lock, so a slow run cannot stack.
- `Preview` and the CLI's `--dry-run` never write.

## 12. Testing

- `internal/llm` ships a `FakeProvider` replaying `testdata/*.json`. **The default test run never
  calls a paid API.** Live-provider tests are gated behind `AFF_LIVE_LLM=1` and excluded from CI.
- Upstream fetches are served from `testdata/` through an injected `http.Client`.
- Golden-file tests for all three renderers, plus the external validator in `make validate`.
- Scheduler tests use an injected clock — no sleeping.
- Auth tests: TOTP replay rejection, session expiry/rotation/revocation, backoff, timing uniformity,
  cookie flags, `Origin` rejection on the WS upgrade.
- Adversarial generator tests, each of which must reject rather than publish: malformed JSON; a URL
  absent from the source set; a near-duplicate of yesterday; `<script>` in `body_html`; a relative
  URL in an anchor; a title containing `]]>`.

## 13. Milestones

| # | Milestone | Done when |
|---|-----------|-----------|
| M0 | Skeleton | Module, layout, buf/proto codegen, config, `/healthz`, CI green |
| M1 | Store + render | Schema + migrations; seeded items render as RSS/Atom/JSON; goldens pass |
| M2 | Compliance | External validator clean on all three formats, ugly cases included |
| M3 | Publish plane | Conditional GET, ETag, gzip, cache, permalink pages, read-only handle |
| M4 | Auth | argon2id + TOTP + sessions + backoff; `aff admin init`; auth tests green |
| M5 | Bridge + RPC | GoGRPCBridge wired, interceptor auth, `FeedService`/`SystemService`, CLI client |
| M6 | Generative feed | Trivia + fact end-to-end on `FakeProvider`, then one real OpenAI run |
| M7 | Novelty | Embeddings, dedup, retry; 30-day simulated backfill yields no near-duplicates |
| M8 | Scheduler | Cron, single-flight, budgets, `runs` accounting, kill switch |
| M9 | Grounded news | Source fetch, candidate set, link-integrity enforcement, ranking prompt |
| M10 | Admin UI | Four GWC screens against the real RPCs, responsive |
| M11 | Deploy | Caddy, systemd, IP allowlist on admin host, log rotation, feeds live |
| M12 | Integration | ArticleFlux subscribes; verify rendering, dedup, refresh behavior there |

M1–M3 are plumbing and should land fast. M7 and M9 are the actual engineering. M4/M5 are where
mistakes are expensive, so they come before anything is exposed.

## 14. Risks

- **Repetition** in daily generative feeds. M7 mitigates, but embeddings only catch surface
  similarity; expect to add a topic-coverage ledger (series/decade/genre already used) later.
- **Wrong facts.** Trivia will sometimes be wrong and there is no cheap oracle. Mitigation: keep
  claims narrow, cite a source when the model can name one, add a report link in the feed footer.
  Accepting a nonzero error rate is the honest position.
- **Hallucinated links** — structurally prevented (§7.6), not prompted away.
- **Bridge fragility.** gRPC-over-WS through Caddy is the least standard piece; the keepalive/GOAWAY
  flap is a known failure. Budget real time for M5, and keep the CLI working as an independent check.
- **Upstream ToS.** Summarize-and-link only; never republish full article text.
- **Cost creep** — capped per feed per day, tracked in `runs`.

## 15. Open questions

1. **Which feeds ship first?** Assumption: `anime-trivia-daily`, `anime-fact-daily`,
   `anime-news-daily` (grounded). Confirm or swap.
2. **Are the published feeds public?** Plan assumes public read-only on `anime.earlcameron.com`,
   which is what makes §2's unauthenticated read-only plane safe. If they must be private, feeds
   need per-subscriber tokens in the URL and §5's caching changes.
3. **Grounded sources** — starting set is ANN + Crunchyroll News RSS. Others in or out?

## References

- [RSS 2.0 Specification](https://www.rssboard.org/rss-specification)
- [RSS Best Practices Profile](https://www.rssboard.org/rss-profile)
- [RFC 4287 — The Atom Syndication Format](https://datatracker.ietf.org/doc/html/rfc4287)
- [The Proper Content Type for XML Feeds](https://www.petefreitag.com/blog/content-type-xml-feeds/)
- [RSS Feed Best Practices — Kevin Cox](https://kevincox.ca/2022/05/06/rss-feed-best-practices/)
