# Changelog

Notable changes to AnimeFeedFlux. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

This file answers **"what changed, and do I need to care?"** For *why* a thing changed — including
the decisions that were made and then reversed — see [`DEVLOG.md`](DEVLOG.md). Keeping the two apart
is deliberate: a changelog that carries reasoning becomes unreadable, and a narrative that carries
version diffs becomes unmaintainable.

No software is released yet — nothing built here has been deployed anywhere, and no feed has ever
been published. See `README.md` → "Status" for what is actually built versus what is still planned.

`0.0.1-dev` versioned the **specification**, not an implementation; `-dev` sorts below any real
release under semver precedence, so tagging a real build later cannot be shadowed by an early one.
The number stays in the `0.0.x-dev` range through the build phases, and the first version that means
"you can run this" is the one cut at the end of Phase C.

## [Unreleased]

### Changed — 2026-08-10, /generate rebuilt as a workbench: the prompt and its output, side by side

- **The page is now a strip, two equal columns and a collapsed drawer.** The three-column layout
  (an 18rem feed rail, a recipe form, a 22rem sampler) spent a permanent quarter of the screen on a
  list of three feeds and gave the prompt a third of the remainder, while interleaving fields
  retuned every few minutes (prompts, model, effort) with fields set once and never touched again
  (slug, cron, timezone, budgets, sources). The loop the page exists for — write, preview, judge,
  adjust — was the thing it made hardest. The strip now holds every input that changes what a
  preview produces (feed, model, effort, candidate count, temperature override) next to the button
  that spends the money and the estimate of what it will cost; the prompts and the preview split the
  full width and height; the recipe fields are behind a collapsed `Recipe settings` disclosure.
- **Template-variable chips.** `{{.Today}}`, `{{.Season}}`, `{{.RecentTitles}}` and the rest of §7's
  variables insert at the cursor rather than being listed as text to retype. The failure they remove
  is real: a typo like `{{.Titles}}` parses fine and only fails at `Execute`, so it surfaced as a
  validation error *after* a paid provider call.
- **Preview no longer requires saving first.** `SampleService.Sample`/`SampleStream` take a
  `SampleDraft` (system prompt, user prompt, model, effort), so the strip previews exactly what is on
  screen, including unsaved edits.
- **The model field is a menu of the provider's own models,** hydrated from `SystemService.ListModels`
  — the same RPC the Settings provider screen uses, so the API key stays server-side. Chat models
  group first; nothing is removed from the list, and a model the recipe names but the provider does
  not list is pinned in as its own option. When the list cannot be fetched the field degrades to the
  text input it used to be.

### Fixed — 2026-08-10, /generate loaded no data at all when opened directly

- **Every `fetch.UseResource` loader on the page ran while the session was still `ANON`.** On a hard
  load the WebSocket has not finished its handshake at mount, so the feed list, settings and model
  list all failed against a socket that could not carry them, and nothing re-ran them. The picker sat
  empty, which reads as "no feeds exist" — the one thing it did not mean. The loaders now re-fetch
  when the session reaches `AUTH`, keyed on the session state itself: `appstate.Anon` is the zero
  value, so a `state != Disconnected` boolean is already true there and would never change.
- **The feed picker displayed the wrong feed.** Its placeholder option was rendered only while
  nothing was selected, so choosing a feed shifted every option's index by one and the browser kept
  its selection by index — the picker showed the second feed's name above the first feed's prompts.
  The placeholder is now unconditional.
- **The preview pane carried a second copy of the strip's controls** (size, temperature, estimate and
  a Sample button), so two size fields and two Sample buttons had to agree. What remains there is
  what is about a run rather than an input to one: the remaining budget, the disabled reason, and
  Cancel — which now appears only while a sample is actually in flight instead of sitting at the top
  of the output pane permanently disabled.

### Fixed — 2026-08-10, every session reported itself revoked, so session revoke was unreachable

- **`timestamppb`'s `AsTime()` maps a NIL message to 1970, and `time.Time.IsZero()` is only true for
  the year-1 zero value.** `SessionRow.RevokedAt` was built with a bare `.AsTime()`, so a session the
  server had never revoked read as "revoked in 1970" — `Revoked()` was true for every row including
  the caller's own live one. The Actions column was consequently empty on every session and
  **individual revoke (§12.5, D4-04) could not be reached from the UI at all**.
  It looked like correct behaviour — "these are all revoked, so no revoke button" — which is how it
  survived; it only became visible on a row that was self-evidently live (current, last seen 0
  seconds ago) and still said Revoked. Fixed with a `protoTime` helper that maps absent to the zero
  time, plus two tests, one of which asserts the `timestamppb` trap itself so the helper explains why
  it exists.
- **The recovery-code count is now readable.** §12.5 requires Settings to show "regenerate recovery
  codes with remaining count" and §12.2 nags at ≤2, but the count was only ever reported in the
  response to `RecoverWithCode` — i.e. only to someone already locked out and spending one. The
  Security card therefore rendered its heading and a kebab and nothing else. `AuthService.Session`
  now carries `remaining_recovery_codes` (`store.CountUnusedRecoveryCodes` already existed and had no
  caller), and the panel states it on load.
- **A bulk revoke buried the live sessions.** Rows were ordered by last-seen with revoked ones
  interleaved, so after one `aff admin reset` the panel listed 126 rows of which exactly one was
  active. Revoked rows now sort below every live one, are capped at 10 with an explicit "N older
  revoked sessions are not shown" note rather than silently truncated, and say **Revoked** in the
  Actions column — previously a revoked row was visually identical to a live one you simply could
  not revoke.
- **The Recovery codes kebab moved into a card header**, matching Active sessions. It sat after the
  body text while the identical control one card down sat beside its heading.

### Fixed — 2026-08-10, generation had never worked against a real provider (`internal/llm`)

- **`Generating[[]GeneratedItem]` asked OpenAI for a top-level ARRAY schema**, which structured
  outputs reject outright: `invalid_json_schema: schema must be a JSON Schema of 'type: "object"',
  got 'type: "array"'`. Every real generation call failed with a 400. `internal/llm` now generates a
  `generatedBatch{ Items []GeneratedItem }` object and unwraps it, and the steering text names the
  `items` field so the prose and the schema ask for the same shape.
  - Nothing in the test suite could have caught this: `FakeProvider` replays canned JSON and never
    builds a schema, so every test exercised the decode path and none of the contract the provider
    actually enforces. Found by the first live run (TODOS.md `A4-30`), which is what that ticket is
    for.
- **"model output failed validation twice" now logs WHICH rules rejected the items.** The reject
  reason counts were collected and dropped at the boundary, leaving the only actionable question
  unanswerable.
- **`ListModels` timeout raised 10s → 30s, and the last good list is cached and re-served on a later
  failure.** A cold TLS handshake to OpenAI can take most of ten seconds, so the old timeout cut off
  calls that were merely slow and reported them as "check the API key" — observed live, with curl
  against the same endpoint and key returning 200.

### Added — 2026-08-10, the Provider settings screen, rethought (`proto`, `internal/rpc`, `internal/store`, `web/`)

`/settings/provider` is now four groups instead of a flat form:

- **Connection** — any OpenAI-compatible endpoint, recorded as a named profile with a base URL and
  **the name of the environment variable holding its key, never the key** (§4 keeps key material in
  the environment; a profile is therefore safe in SQLite, in a browser, and in a backup). The server
  reports whether each named variable is actually set. **Not yet wired to the runtime** — see
  TODOS.md `A4-42`.
- **Model and effort** — the two model menus, plus **effort**, which maps to SchemaFlux's Speed tier
  (`smart`/`fast`/`quick`), the only such knob its API exposes (§8.1). `internal/llm` previously set
  no tier at all; it now sets it from this setting.
- **Rates** — the editable price table gained an "Add rate" control. An empty table is why a run can
  report `$0.0000` while spending real money (`A4-41`).
- **Spend** — new `SystemService.CostHistory` RPC and `store.SpendByDay`, drawn as a daily column
  chart over a selectable 7/30/90-day window with the window total as a hero number. Days with no
  runs are drawn as empty slots rather than skipped, because a gap is what an operator reads this to
  find. Palette validated against both theme surfaces.

### Changed — 2026-08-10, settings sections are addressable; the second sign-out is gone

- `/settings/security`, `/settings/provider`, … are real routes. The active section was component
  state, so a reload always dropped you back on Security and no one could link at a panel.
- The sign-out button at the foot of Settings is removed — `web/shell/header.go` carries one on every
  authenticated route, and two controls performing the same action is one too many.

### Added — 2026-08-10, subscribe URLs, a virtualized session list, and provider-hydrated model menus

- **Subscribe URLs on `/generate`** (`web/pages/generate/render_urls.go`). The URL is this product's
  entire deliverable (§1: "a URL that returns valid XML and never lies") and nothing on the
  authoring page told an operator where the finished feed lives — you had to join a base URL from
  Settings to a slug from the editor and remember which extension each format takes. The panel lists
  the public index first (§14.1: "the page you paste into Slack when you have forgotten a slug")
  then the selected feed's RSS/Atom/JSON, each with a copy-to-clipboard button. The URL is also
  selectable text, because a clipboard write can be refused by the browser.
  - It shows the **saved** slug, never the editor's draft: a subscribe URL for an unsaved slug is a
    URL that 404s.
- **`SystemService.ListModels`** (`proto`, `internal/llm/models.go`, `internal/rpc/models.go`): asks
  OpenAI which models this deployment's key can use, **server-side** — the key never reaches the
  browser (§4). Settings → Provider's two model fields are now menus hydrated from that list
  (127 models against a real key), grouped into "for this field" and "other" by a heuristic over the
  model id, since the API reports no capability information.
  - **Never fails the request.** No key, an unreachable provider or a rate limit come back as
    `unavailable` with a reason, and the fields fall back to the free-text inputs they used to be. A
    settings screen that cannot be configured while a third party is down is worse than one that
    asks you to type the id.
  - The saved value is always present and selectable even when the provider does not list it, so
    opening the page cannot silently re-point a working feed at whatever the browser picked first.
- **`web/ui.VirtualTable`**, and Settings' active-sessions list now uses it. That list gains a row on
  every sign-in and never loses one; it had reached ~120 on a dev box in an afternoon, all of them
  built, reconciled and laid out for the ten a person can see. Now 17 rows are in the DOM for 121
  sessions. It is a CSS-grid list with ARIA table roles rather than a `<table>`, because
  `html.VirtualList`'s spacer `<div>`s are not valid `<tbody>` children — the browser hoists them out
  and the scrollbar starts lying.

### Fixed — 2026-08-10, settings' public base URL read as unset on a correctly configured server

- `GetSettings` reported an empty `publishing.public_base_url` whenever nothing had been saved, even
  though `AFF_PUBLIC_BASE_URL` is **required at boot** (§16) and is what the publish plane bakes into
  every guid. Anything reading that field treated a working deployment as unconfigured — the new
  subscribe-URL panel showed "set a public base URL" on a server that knew its own. It now seeds from
  the environment when unsaved, the same cold-start pattern `defaultGenerationEnabled` already used,
  and still writes nothing back.

### Fixed — 2026-08-10, a signed-in browser pass over all five surfaces (`web/`, TODOS.md D0-25, D6-22)

Driving the real UI while signed in, in both themes, at 1280px and the 320px floor. Everything here
was found by looking at a rendered page; none of it was visible from Go.

- **`/settings` had no stylesheet at all.** `web/pages/settings` emitted nine `af-*` class names and
  contained zero `css.Global` calls, so the page rendered as browser defaults edge to edge and its
  active-sessions table pushed the whole document into horizontal scroll at 1280px. Added
  `web/pages/settings/styles.go` — token-only rules, ruled sections rather than boxes, a measure on
  the forms, the wide table scrolling inside its own box, and the narrow breakpoint in the same file.
- **History's runs table overflowed the document at 320px** (viewport 320, scrollWidth 752).
  `.history-table` set `overflow-x: auto` on a `<table>`, where overflow is ignored; it now also
  sets `display: block`, which is what makes the property apply.
- **Every row of the runs and items tables read "Expand Actions".** The `⋯` kebab trigger rendered
  its label as visible text inside a button sized 32×32 for a glyph. It is now a real `⋯` with
  "Actions" as its accessible name (PLAN.md §12.6).
- **History could not reach page 2.** Both tables rendered Previous and Refresh and no forward
  control, and `load-ok` called `cursor.Advance()` — so merely loading a page moved the cursor onto
  the next one, making Refresh fetch the wrong page and Previous live on page 1. The cursor now
  records the next token without moving, and a **Next** button exists.
- **Seven i18n keys were referenced but never defined**, each rendering its own name as interface
  text: 17 `settings.*` entries (including the sign-out button, which read
  `settings.settings.security.signOut.action`), plus `common.connectionUnreachable`,
  `common.backoffCleared`, `auth.recoverSavedConfirmLabel` and `history.notWired`. The
  `common.connectionUnreachable` one is what an operator saw instead of "Couldn't reach the server"
  on every failed sign-in against an unreachable server.
- **New test: `web/i18n`'s `TestEveryCallSiteKeyIsDefined`.** Parses the real source of `web/pages`
  and `web/shell` and asserts every key it can find is defined somewhere. It catches the bare
  `const keyFoo = "foo"` form that every previous check was structurally blind to.
- `.devrun/` is now gitignored — it holds `AFF_SECRET_KEY` and the provider key in plaintext and was
  untracked only by luck (RULE-2).

### Changed — 2026-08-10, new brand mark, and dark mode actually turns on (`internal/brand`, `web/`, TODOS.md D0-12, D6-22)

- **New brand artwork.** The mark is now the kitsune crest (a fox over three broadcast arcs in a hex
  shield), replacing the hand-drawn timesheet-cel SVG. It ships as PNG — `internal/brand/`
  `favicon-{32,180,512}.png` and `favicon.ico`, plus a 1200×630 `og-default.png` for Slack unfurls —
  because the artwork is a raster render and a hand-traced vector would be a different mark. The
  package embeds them, so the publish plane serves a real `/favicon.ico` (previously a `204`) from
  the binary rather than from disk, which matters on a read-only root filesystem. `web/build.sh`
  stages the same files into the admin bundle, so the tab icon and the public icon cannot diverge.
- **There is no lockup image**, deliberately: the source wordmark is dark navy inside its own glow,
  which neither keys out cleanly nor survives dark mode. The wordmark is HTML text beside the crest.
- **`accent` moved to the crest's blue** — `#2A5FD8` light / `#5B8CFF` dark, replacing the muted
  teal-blue `#2E6E8E`/`#5FA8CE`, at equivalent contrast headroom. `danger` still owns the redline.
- **`theme-color` was carrying the danger swatch** and now tracks `accent` — the browser's own
  chrome was being painted in the colour reserved for destructive actions.
- **Dark mode works.** It never had: `web/tokens` has shipped a full dark palette under
  `:root[data-theme="dark"]` for some time, but nothing ever called `ui.SetTheme`, so the attribute
  that selector needs was never set and every operator got light theme regardless of their OS. New
  `web/shell/theme.go` resolves a three-state preference (`Match system` default / `Light` / `Dark`)
  against `prefers-color-scheme`, persists an explicit choice in `localStorage`, applies it before
  the first render (so there is no light-theme flash), and keeps following the OS while the
  preference is `system`. The control is in the header — `/settings` is behind the session, and the
  login screen is where a night-shift operator meets the app first.
- **Four missing `shell` i18n keys defined** (`header.brand.label`, `header.brand.homeLabel`,
  `header.signOut.busy`, `header.signOut.error`). Until now a screen reader announced the raw key
  string on the brand link, and a failed sign-out rendered `header.signOut.error` as visible text.
- **`/login` no longer shows the brand twice.** The header renders the mark alone in the ANON and
  ELEVATED states, which is what `web/shell/header.go`'s own doc comment has always claimed it did.
- **`[hidden]` now beats class rules.** `h.Show(false, node)` does not drop the node — it sets
  `hidden` and relies on the UA sheet's single-selector `[hidden] { display: none }`, which any
  class rule setting `display` outranks. One global `!important` rule closes a whole class of
  silent "hidden element is visible" bugs.

### Added — 2026-08-10, monthly spend ceiling for scheduled generation (`internal/budget`, `internal/config`, TODOS.md DOD-7)

- **New env var `AFF_MONTHLY_SPEND_CEILING_USD`**: a calendar-month provider-spend ceiling
  (`internal/config.Config.MonthlySpendCeilingUSD`), default `0` (unlimited — deliberately not read as
  "zero budget", which would stop generation on the first run). Enforced today for **scheduled runs
  only**: `cmd/animefeedflux/wire.go`'s `genGate.Allowed` (every scheduled run's gate) sets
  `budget.Limits.MonthlyUSDCeiling` from it and checks real month-to-date spend
  (`budget.MonthStart`) independently of the existing daily caps, so a month of small daily spends
  under the daily ceiling can still trip the monthly one. **Not yet enforced for interactive
  sampling** — `sampleBudget.CheckSample` builds its own `budget.Limits{}` and does not set this
  field, so a sampling burst is bounded only by the daily caps both paths share. See PLAN.md §16 and
  `docs/definition-of-done.md`'s DOD-7 section for the full picture, including why this section
  originally shipped with the opposite conclusion twice in one day.

### Added — 2026-08-10, configurable staleness grace factor (`internal/ops`, TODOS.md C4-08)

- **New env var `AFF_STALE_GRACE_FACTOR`**: overrides the staleness grace multiplier (how many
  multiples of a feed's own schedule interval it may go quiet before being flagged stale) that was
  previously a hardcoded `2.0` in `internal/ops/schedule.go`. Any positive float; invalid or
  non-positive values are ignored and the default (`2.0`) is used. Affects the nightly Slack
  staleness webhook and `aff doctor`'s "feeds running on schedule" check immediately — neither caller
  sets the field explicitly, so the env var takes effect without further wiring. **Corrected
  2026-08-10, later same day:** the note originally shipped here said `/healthz` was not yet wired to
  this env var; `cmd/animefeedflux/wire.go:1259` now sets `HealthGrace: ops.ResolveStaleGrace()`, so
  all three surfaces (`/healthz`, the nightly webhook, `aff doctor`) resolve the same value — see
  TODOS.md C4-08's "GAP CLOSED" note and PLAN.md §16 for the variable's full description.

Everything below landed on `dev` since `0.0.2-dev`. It is a large amount of code — the engine,
Phase A, and the headless control surface, Phase B, are both substantially built — but **none of it
is running anywhere.** There is no staging host, no production deploy, and Slack has never been
pointed at a real instance. If you're deciding whether to upgrade or deploy: there is nothing yet to
deploy *to*; this is still development-only.

### Added — Phase A, the core engine

- **Store** (`internal/store`): schema, migrations, a reader/writer split, WAL boot ordering, and
  FTS5 search, all with migration tests. Complete.
- **Renderers** (`internal/render`): RSS 2.0, Atom, JSON Feed, and permalink HTML from seeded items,
  checked against golden files and three fuzz targets for well-formedness. Complete.
- **Compliance** (`internal/feedvalidate`): external-validator-clean output and Slack-compatibility
  checks — the date tag, item ordering, and no-duplicate-timestamp rules Slack's RSS app enforces
  silently. Complete.
- **Generation** (`internal/generate`): SchemaFlux wired for typed model output, trivia and grounded
  news paths exercised end-to-end on recorded cassettes, plus a soak test. Most of it is done; a
  handful of tasks remain.
- **Novelty** (`internal/novelty`): embedding-based dedup and retry, proven against a seeded
  near-duplicate corpus. Nearly complete.
- **Grounded news** (`internal/sources`, `internal/urlnorm`): source fetch, candidate
  normalization, and the structural link-integrity rule — a published link must be byte-equal to a
  URL actually fetched, never model-generated. Most of it is done.
- **Scheduler** (`internal/schedule`): cron with timezone/DST handling, jitter, a worker pool,
  budget accounting, and a kill switch. Most of it is done.
- **Sampling**: a dry-run pipeline that returns items, rendered XML, and validator verdicts without
  writing to the store. Complete.
- **Publish plane** (`internal/publish`): conditional GET, HEAD, gzip, caching, the 404/410/405
  cases, rate limiting, and a poll-load check proving it stays read-only under load. Complete.
- **Cross-cutting**: fuzz targets for the renderers, the HTML sanitizer, and URL normalization; the
  generation soak test; the publish-plane poll-load check; all wired into CI. Complete.
- **Test reliability** (`internal/rpc`, `internal/e2e`; TODOS.md A0-T04/A0-T07): removed padding
  `time.Sleep` calls from `internal/rpc/run_test.go` and `internal/e2e/watch_test.go`'s Watch tests
  that guarded nothing (the assertions already hold regardless of interleaving); replaced the two
  sleeps that genuinely raced a hub subscription with a bounded poll on real internal state instead of
  a guessed duration. Wired `testutil.InstallNetworkGuard` into `internal/e2e/main_test.go` (RULE-1),
  proved mechanical by deliberately running a scratch test that violates it and watching it fail before
  reverting. Both TODOS items remain PARTIAL — `internal/ops`, `internal/flowtest`, `internal/obs`,
  and `internal/bridge` still have unguarded sleeps/network calls, out of reach this pass under this
  task's file-ownership rules, not overlooked. Also surfaced, not fixed: `internal/e2e`'s
  `TestItemRevisions` flakes (`internal/rpc/item.go` groups revisions by a real-clock timestamp with
  no injected clock, so a fast edit+revert can collide) — a production bug, not a test issue.
- **Generation pipeline tracing/metrics** (`internal/generate`, `internal/schedule`): a
  `generation.run` root span with `feed_slug`/`trigger`/`outcome`; SchemaFlux's own `llm.generate`-
  equivalent span nests under it via context propagation rather than a second, overlapping span; a
  `validate` child span records rejected count and reason tokens; `aff_runs_total` and
  `aff_run_duration_seconds` fire on every terminal state (completed, failed, skipped), including a
  Gate/budget refusal in the scheduler, which lands in `outcome="skipped"`, never as an error;
  `aff_tokens_total`/`aff_cost_usd_total` come from the run's own recorded (estimated) usage; and the
  canonical `run.finished` wide event now fires from the pipeline itself. See the task report for a
  gap this surfaced: SchemaFlux's span name is `schemaflux.<operation>`, not literally `llm.generate`
  as §15.0a's diagram assumed, and full parent/child nesting additionally needs
  `schemaflux/telemetry/otel.Install(obs.GetTracerProvider())` called once at process startup, which
  is outside `internal/generate`/`internal/schedule`.
- **Publish-plane tracing** (`internal/publish`, TODOS A9-20/A9-21/A9-23): every route now opens a
  root `http.request` span (`obs.KindRequest`) carrying `route`/`status`/`cache`, closed from the same
  `(*server).observe` call that already emits the wide event and the two HTTP metrics, so all three
  stay in lockstep by construction. A `render.feed` child span (`format`/`items`/`bytes`) opens only
  on a feed-document cache miss — the hit path returns before it is ever reached, so a hit stays as
  cheap as A9-04 already required. A 5xx response marks its span `codes.Error`, which is what
  `internal/obs`'s tail sampler already keys "always sample errors" off — that sampler and its ratio
  logic were built and tested for `generation.run` earlier and needed no changes here, only a caller
  that actually sets span status on failure. `internal/publish/otel_test.go` is new.

### Fixed — observability (`internal/sources`, `internal/llm`)

- **`sources.fetch` span now carries item count** (TODOS A6-17): the span used to close before
  `Parse` ran, so a conditional GET that came back 200 with zero usable candidates was
  indistinguishable, in the span alone, from one that came back with a full batch. `FetchCandidates`
  now keeps the span open through parsing and normalization and attaches `items` (0 on a 304, the
  post-normalization candidate count otherwise, omitted on a failure that never parsed). A bare
  `Fetch` call still emits the same span with no `items` attribute, correctly — it never parses.
  Note: `internal/sources.Fetcher` is not wired to any real caller yet (`cmd/animefeedflux/wire.go`'s
  `noFetcher` stands in for grounded-feed fetching), so this span does not fire in production today.
- **A retried transient provider error now logs WARN, not ERROR** (TODOS A0-L07):
  `SchemaFluxProvider.Generate` logs on every failure, at a level derived from `llm.Classify`'s
  `Kind` — `Transient` is WARN (expected to resolve on the next attempt without a human doing
  anything), `Invalid`/`Fatal` are ERROR. Only `Kind` reaches the log line, never the wrapped error's
  message, which can echo the model's raw output back (RULE-3). `llm.Config` gained an optional
  `Logger` field, defaulting to `slog.Default()`.

### Added — Phase B, the control surface (still headless)

- **Auth** (`internal/auth`): argon2id password hashing, TOTP, recovery codes, sessions, and
  backoff, plus `aff admin init`. Argon2's memory parameter is now capped at a small multiple of
  the default and rejected before allocation, closing a memory-exhaustion path on the login
  endpoint. New env vars `AFF_PASSWORD_PEPPER` and `AFF_PASSWORD_PEPPER_VERSION`: an optional
  server-side pepper applied to the argon2id output (not the password), rotatable via the version
  field; unset behaves byte-identically to no pepper. Password reset tokens are now issued and
  consumed end-to-end (single-use, database-enforced) and revoke every session on completion.
  Most of it is done.
- **RPC services** (`internal/rpc`, `gen/aff/v1`): all six services, an auth interceptor,
  optimistic concurrency, and pagination. The session-token header is unified on
  `x-aff-session-token` on both sides (the CLI previously sent a different name the server never
  read, so CLI login silently failed). Complete.
- **Bridge** (`internal/bridge`): GoGRPCBridge wired in, `Origin` checking, and streaming RPCs
  verified. Every RPC call now carries the validated session unconditionally. Keepalive
  enforcement is configured but currently inert — GoGRPCBridge's native transport, which would make
  it apply, is incompatible with session propagation as currently wired — so a keepalive-based
  disconnect is not yet enforced. Complete apart from that.
- **CLI** (`cmd/aff`): drives create, sample, promote, run, history, backup, restore, verify,
  encrypt, decrypt, prune, stale, and doctor end to end. `admin init`/`admin reset` are the only
  privileged local-only commands. Nearly complete.
- **Operations** (`internal/ops`): scheduled backup (`VACUUM INTO` plus an integrity check on the
  copy, not a file copy), restore, nightly prune with a dry-run default, AES-256-GCM off-box
  encryption (key from environment only, never a flag), and a watchdog that alerts on backup
  *absence* as well as backup failure. New env vars `AFF_BACKUP_DIR` and
  `AFF_BACKUP_ENCRYPTION_KEY`. Off-box transport is not wired yet — the copy stays local, and that
  absence itself now alerts nightly instead of being silent.
- **Flow sanity tests** (`internal/flowtest`): headless suites that drive a whole user flow and
  assert on resulting system state rather than a mock's call log. Mostly green.

### Added — the admin listener can now actually be logged into

- **Admin listener routing change**: the admin listener (`AFF_ADMIN_ADDR`) now routes each request by
  shape instead of handing everything to the WebSocket bridge. An HTTP/2 request with a
  `Content-Type` beginning `application/grpc` is served by a real `*grpc.Server` (the six RPC
  services, session-checked by the same interceptor the bridge uses) — this is the transport `cmd/aff`
  dials and previously had nothing listening on it. `POST /auth/login` and `POST /auth/logout` are
  now plain HTTP endpoints, matched ahead of both the bridge and the admin UI's asset/route serving.
  Everything else falls through to the bridge, or to the admin static bundle with an SPA fallback
  (any path that isn't a real asset now serves the app shell instead of a 404). Still one listener,
  one port.
- **New RPCs**: `SystemService.ListAuditEvents` (paginated, newest-first read of the `auth_events`
  audit trail — id, timestamp, kind, IP, success) and `SystemService.Vacuum` (runs `VACUUM`, returns
  size before/after and how long the exclusive lock was held).
- **New CLI commands** (`cmd/aff`): `aff recover` (consume a recovery code to reset the password or
  re-enroll TOTP), `aff auth change-password`, `aff auth reenroll-totp`, and `aff system settings
  get`/`aff system settings set` (`--base-url`, `--author`, `--contact`, `--copyright`,
  `--ttl-minutes`, `--cache-control`, `--og-image`, `--spend-ceiling-usd`, `--token-ceiling`,
  `--default-token-budget`, `--default-run-budget`, `--default-feed-window`,
  `--staleness-minutes`).
- **New env vars**: `AFF_OFFSITE_DIR` (off-box backup destination), `AFF_OTEL_ENABLED`,
  `AFF_OTEL_EXPORTER` (`otlp` or `stdout`, defaults to `otlp` once enabled), `AFF_TRACE_SAMPLE_RATIO`,
  and `OTEL_SERVICE_NAME` (unprefixed — the standard OTel variable).
- **Dev-only login prefill**: a `devui` build tag (`web/pages/auth/devfill_on.go` /
  `devfill_off.go`) that, only when the admin bundle is built with `-tags devui` and `DEV=1` is set
  for `web/build.sh`, prefills the login form with a dev password and a live TOTP code supplied via
  `-ldflags` at build time. Never present in a default build — the credential is absent from the
  compiled binary entirely, not merely unreachable behind a runtime flag.

### Tests — bridge server-streaming, deeper than "dial and read one message" (TODOS.md B2-06)

- **`internal/bridge`**: three new tests (`stream_test.go`) exercise the gRPC-over-WebSocket relay's
  streaming behavior directly, rather than trusting a single terminal message: multiple pushes over
  the stock gRPC health service's `Watch` arrive one at a time, in order, each one proven absent
  until the server actually sends it (a bounded per-step Recv timeout on an already in-flight call,
  not a sleep) — ruling out a relay that buffers and flushes only once an RPC completes. A
  hand-built streaming RPC (no new `.proto`, it reuses `healthpb`'s message types purely as
  envelopes) proves a clean, handler-initiated end reaches the client as a prompt `io.EOF`, and that
  a raw connection severed mid-stream — captured directly via a wrapped `net.Listener`, not
  `httptest.Server.CloseClientConnections` (confirmed to be a silent no-op against an
  already-upgraded WebSocket connection: `net/http/httptest` stops tracking a conn the instant it
  transitions to `http.StateHijacked`) — surfaces to the caller as an error distinct from `io.EOF`,
  not a false "the run finished" (PLAN.md §22 J9). `SampleStream` and `RunService.Watch` both ride
  this exact relay, so this is direct transport-layer evidence for both; TODOS.md B2-06 stays open
  because an application-level `SampleStream`-over-the-real-bridge test still requires
  `internal/e2e` to expose `SampleService`, outside this change's file scope.

### Added — Phase C, shipping (just started)

- `Dockerfile`: multi-stage build, `CGO_ENABLED=0`, distroless runtime, no shell in the final image.
- `deploy/`: a `compose.yaml` and nginx config, for later use — nothing here is running against
  them yet. The CI/CD pipeline (GHCR push, tag scheme), staging host, Slack proof, ops runbook, and
  production deploy are all still ahead.
- Admin static bundle serving (`internal/publish/static.go`): the WASM admin UI is built to a
  scratch directory and moved into place atomically, precompressed at build time (not per
  request), and served gzip-only to clients that ask for it, on the admin listener only — never on
  the public publish plane. New env var `AFF_ADMIN_STATIC_DIR`.

### Added — Phase D, the admin UI (now reachable)

- The admin UI is built on `GoWebComponents/v5` (not the `@latest` tag, which resolves to an
  abandoned, unusable v1) and is now actually reachable: `web/main.go` is the client's composition
  root, dialing once and registering all five routes (`/login`, `/recover`, `/generate`,
  `/history`, `/settings`) against real page components rather than the shell's placeholder.
  Session-expiry handling, a DISCONNECTED banner with backoff, and an auth guard are wired in.
  Every user-visible string is drawn from an `en` i18n catalogue, and the i18n provider is now
  mounted (it previously was not, so every translated string silently rendered its fallback).
- Dark mode now actually applies — the previous token composition emitted a selector that could
  never match.
- The i18n literal gate (D6-20/D6-21) now actually gates: CI's `i18n` job runs `make i18n-lint` and
  `make i18n-ratchet` unconditionally instead of the previous reporting-only `|| echo "::warning"`
  fallback and the `if [ -f .github/i18n-baseline.txt ]`-guarded skip. `.github/i18n-baseline.txt`
  (confirmed `0`, matching the real current literal count in `web/`) is added so a fresh checkout
  can't silently skip the ratchet by lacking the file. `cmd/affi18n lint`/`ratchet` themselves were
  already correct — narrow classification (GWC `Text`/`Textf`/`TextIf` calls, bare-string children of
  prose-bearing tag builders, and the `Title`/`Placeholder`/`Alt` attribute setters only; class names,
  CSS/ARIA-role/route/struct-tag/log-format literals untouched) and a `//nolint:i18n` escape that
  requires a reason, plus a ratchet that never fails when the count drops — only the CI wiring was
  missing.

### Added — repository

- CI workflow (`.github/workflows/ci.yml`) covering docs/hygiene checks, hook and script tests,
  `go build`/`go vet`/`staticcheck`/`go test -race`/`govulncheck`, a coverage ratchet, the fuzz
  targets, and `make validate` against the external feed validator — gated by a single `CI gate`
  aggregating job, so branch protection never needs updating when a job is added.
- New real dependencies: `modernc.org/sqlite` (pure-Go SQLite with FTS5), `schemaflux`,
  `GoGRPCBridge`, `GoWebComponents/v5` (the `@latest` tag resolves to an abandoned, unusable v1 —
  the module path matters), `go.opentelemetry.io/otel` and its exporters, `golang.org/x/crypto`
  and `github.com/pquerna/otp` (auth), `github.com/BurntSushi/toml`, `google.golang.org/grpc`.

### Decided

- **SQLite driver: `modernc.org/sqlite`** (A0-16). `CGO_ENABLED=0` and a `distroless/static` runtime
  rule out a cgo driver; `modernc` is pure Go and ships FTS5.

## [0.0.2-dev] — 2026-08-09

Repository process, not product. Still no application code.

### Added

- `DEVLOG.md` — the narrative record, including reversals.
- Commit message convention, derived from the existing history rather than imported, with
  `.gitmessage` as an opt-in template. Conventional Commits was considered and rejected.
- `dev`/`main` branch model. `dev` is the working branch and the GitHub default; `main` is what gets
  released and, once Phase C lands, deployed.
- Git hooks in `.githooks/`, opt-in per clone via `scripts/setup-hooks.sh`. `pre-commit` refuses
  staged secrets and databases, then runs gofmt, build, vet, staticcheck and `go test -short` on
  staged Go. `pre-push` guards `dev` → `main` promotion behind `AFF_PROMOTE=1`.
- `scripts/test-hooks.sh` — 18 cases across both hooks, in a throwaway repository.
- Server-side protection on `main`: force push, deletion and non-linear history refused, admins
  included.
- Repository description and topics.

### Fixed

- The credential guard matched bare *mentions* of key variable names, which blocked its own commit
  and would have blocked `PLAN.md` §16 and `SECURITY.md`. It now matches an assignment with a
  plausible value, with narrow named exclusions for hook tooling.
- `.gitattributes` did not reach extensionless hook files, so they would have checked out with CRLF
  and failed as `bad interpreter: /bin/sh^M`.
- `AGENTS.md` still described a two-document spec and a repository containing only `PLAN.md` and
  `TODOS.md`.

## [0.0.1-dev] — 2026-08-09

### Added

- `PLAN.md` — the specification of record: two-plane architecture, feed-format compliance researched
  against the RSS 2.0 spec, the RSS Best Practices Profile, RFC 4287 and JSON Feed 1.1, Slack as a
  first-class consumer, data model, RPC surface, admin UI, operations, testing strategy, phased
  milestones, risks, open questions, and the ten user flows with their sanity assertions.
- `TODOS.md` — atomic build order across phases A–E, each task citing a plan section; standing
  rules; flow sanity suites; fuzz, soak and load tasks; an operational runbook and drills.
- Repository scaffolding — licence, contribution and security policy, code of conduct, agent
  orientation, issue and pull-request templates, dependabot, line-ending policy.
- `DEVLOG.md` — the narrative record, including the reversals: a content-derived guid replaced by an
  opaque ULID, Docker rejected on evidence and then adopted for learning value, `PurgeDeleted`
  specified and cut, multi-feed scaling deferred.

### Decided

- **Slack's RSS app is stricter than the RSS specification** and fails silently. It requires a date
  tag, in-sequence items, and no duplicate timestamps, and it advances a bookmark past the newest
  item it has seen. This forced distinct strictly-increasing timestamps behind a database
  constraint, a no-backdating rule, plain-text `description` with HTML in `content:encoded`,
  OpenGraph tags for unfurls, and corrections instead of silent edits.
- **Item identity is an opaque ULID, not a content hash.** A title-derived guid is stable under edit
  only by convention; an opaque key makes it true by construction. Idempotency moved to a separate
  `content_hash`.
- **Hallucinated links are prevented structurally.** Grounded items must carry a link byte-equal to
  a URL actually fetched, with both sides normalized by the same function.
- **SchemaFlux** supplies typed LLM operations; the business-rule validation layer stays ours,
  because typed is not valid.
- **Core engine first, UI last.** Every RPC is exercised by the CLI before a component exists.
- **Docker was rejected on the evidence and then adopted anyway**, for the learning value of a real
  container pipeline. The trade — a second deployment model on a 2 GB box — is recorded in §15
  rather than left to be rediscovered.
- **No hard delete.** A `PurgeDeleted` RPC was specified and then removed: it contradicted the
  promise that a permalink resolves forever.
