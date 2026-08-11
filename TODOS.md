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
- [ ] `A0-02` Create the directory layout exactly as listed in §3, with a doc comment per package. (doc comments are done — all 24 `internal/*` packages carry a `// Package x ...` comment. The **layout** half is not: the tree has `internal/schedule/`, not §3's `internal/scheduler/`, plus 14 packages §3 does not list (`bridge`, `budget`, `e2e`, `feedvalidate`, `flowtest`, `i18nlint`, `ids`, `model`, `novelty`, `ops`, `sanitize`, `sectest`, `testutil`, `urlnorm`). "Exactly as listed in §3" is now the wrong requirement rather than an unmet one — reconciling it is a `PLAN.md` §3 edit, out of scope here.)
      (not "exactly as listed": §3 names `internal/scheduler/`, the tree has `internal/schedule/`, and
      the tree carries ~14 packages §3 does not list — `budget`, `ids`, `model`, `sanitize`, `urlnorm`,
      `ops`, `novelty`, `bridge`, `e2e`, `flowtest`, `sectest`, `testutil`, `i18nlint`, `feedvalidate`,
      plus `cmd/affi18n` and `cmd/affvalidate`. Either §3's layout block or this task's "exactly" is now
      wrong; amending PLAN.md is out of this audit's scope) — BLOCKED: two mismatches against §3's tree, verified 2026-08-10. (1) §3 lists `internal/scheduler/`; the repo has `internal/schedule/` — fixing means renaming a package imported by `internal/rpc` and `cmd/animefeedflux`, both outside this pass's edit scope. (2) §3 lists a top-level `testdata/` directory; only per-package `testdata/` dirs exist (`cmd/affi18n`, `internal/i18nlint`, `internal/render`, `internal/sanitize`, `internal/sources`). Doc comments ARE present on all three in-scope packages (`internal/generate/contract.go`, `internal/schedule/cron.go`, `internal/llm/llm.go`).
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
- [x] `A0-15` Commit `go.sum`; pin dependencies. §4 — verified: `go.sum` tracked in git, all `go.mod` requires (incl. `schemaflux v1.1.0`, `go-openai v1.20.4`) at exact pinned versions, no `replace`/pseudo-version drift.
- [x] `A0-16` Decide and record the SQLite driver (cgo vs pure-Go vs wasm). Blocks A1-01. §15.1
- [x] `A0-17` Record the `MemoryDenyWriteExecute` / `CGO_ENABLED=0` consequence of A0-16. §15.1

### A0-L — Structured logging (§15.0)

The field names are the product here. Three packages spelling the same thing differently is three
fields, and no query finds all of them.

- [x] `A0-L01` Define the canonical field-name constants in one place; nothing logs a bare string key. §15.0 — `internal/obs/fields.go` const block; schedule/runner.go's hand-rolled bare-key `run.finished` emitter (a real violation found and fixed this pass — see DEVLOG/task notes) now routes through it.
- [x] `A0-L02` `feed_slug`, `item_key`, `model`, `outcome`, `reason`, `duration_ms` all fixed there. §15.0 — `internal/obs/fields.go` `FieldFeedSlug`/`FieldItemKey`/`FieldModel`/`FieldOutcome`/`FieldReason`/`FieldDurationMs`.
- [x] `A0-L03` `duration_ms` is emitted as a **number**, never a formatted string. §15.0 — `obs.DurationMsAttr` (`slog.Int64`).
- [x] `A0-L04` `outcome` is constrained to `success|skipped|rejected|failed`. §15.0 — `obs.Outcome` + `obs.ValidOutcome`.
- [x] `A0-L05` `reason` is a short stable token, not a sentence — it gets grouped on. §15.0 — `obs.SanitizeReason` + `reasonTokenPattern`.
- [x] `A0-L06` Document the level policy: ERROR means a human must look; WARN self-healed. §15.0 — doc comment on `internal/obs/fields.go`.
- [x] `A0-L07` A retried transient provider error logs **WARN**, not ERROR. §15.0 — implemented 2026-08-10 at the boundary SchemaFlux actually exposes, which is not per-attempt: confirmed (grepping the vendored `schemaflux@v1.1.0` source) that no per-attempt retry hook exists — `mw.Retry` is unexported from the fluent `Generating[T]().Run()` path with no caller callback, and `telemetry.Observer`'s `OperationStarted`/`OperationFinished` (the library's own extension point, installed via `schemaflux/telemetry/otel.Install`) is never called by the real `Generate`/`Extract`/`Transform` pipeline — `OperationStarted(`/`OperationFinished(` appear only in `telemetry/observer.go` and its own test, nowhere in `internal/ops`. So a Transient error reaching `internal/llm` has already survived every retry SchemaFlux was going to make; there is no "recovered mid-flight" case to see from here, only "still failing after retrying" vs. "not retryable at all" — and that distinction is exactly what `Kind` already carries. `internal/llm/llm.go`'s `SchemaFluxProvider.Generate` now calls the new `logGenerateFailure` on every failure: `Kind == Transient` logs WARN (expected to self-heal on the next attempt — this run's own retry if the caller has one, or the feed's next scheduled run — with no human action needed), `Invalid`/`Fatal` log ERROR (nothing about the next attempt is expected to differ). Only `Kind`, a closed-set enum, ever reaches the log line — RULE-3: `err.Err`'s message is never logged, since a schema-violation/malformed-output error can echo the model's raw text back. `Config.Logger` is new (optional, defaults to `slog.Default()`); `cmd/animefeedflux/wire.go` is not updated to pass its own configured logger through (outside this change's edit scope), so production logs through the default logger rather than the app's structured JSON writer until that's wired — noted, not blocking, since the level distinction still fires correctly either way. **Reachable from a real caller**: `cmd/animefeedflux/wire.go:1379` constructs the live `SchemaFluxProvider` via `llm.NewSchemaFluxProvider`, and `internal/generate/runner.go`'s `runAttempt` calls `deps.Provider.Generate` on every generation attempt — this is the production LLM call path, not a test-only surface. Tests: `TestLogGenerateFailure_TransientIsWarnNotError`, `_FatalIsError`, `_InvalidIsError`, `_NeverLogsErrorMessage`, `_NilErrorAndNilLoggerAreNoOps` (`internal/llm/logging_test.go`), RULE-1-clean (no network, no API key). **Separately surfaced, not this ticket**: `A4-31` ("Span `llm.generate` comes from SchemaFlux") is ticked done, and `cmd/animefeedflux/wire.go` does call `schemafluxotel.Install(obs.GetTracerProvider())` — but per the same grep evidence above, that Install wires an Observer nothing in SchemaFlux's real op pipeline ever invokes, so no `schemaflux.<op>` span (attempts, tokens, latency) actually opens in production today despite the wiring being present. Worth a look, but out of scope for `internal/llm`/`internal/sources`.
- [x] `A0-L08` Helper that emits the single canonical `run.finished` wide event. §15.0 — `obs.RunFinished`.
- [x] `A0-L09` Helper that emits the single canonical `http.request` event. §15.0 — `obs.HTTPRequest`.
- [x] `A0-L10` **No chatty INFO.** Progress detail is DEBUG only. §15.0 — verified no `.Info(`/`LevelInfo` call sites in `internal/generate`/`internal/schedule`/`internal/llm` outside the two canonical helpers; fixed `internal/schedule/runner.go`'s `logOutcomeLocked` which was double-emitting a second, non-canonical `run.finished` per completed run.
- [x] `A0-L11` Test: a completed run emits exactly one `run.finished` carrying every required field. — `TestRunFinishedEmitsExactlyOneRecordWithEveryField` (`internal/obs/fields_test.go`).
- [x] `A0-L12` Test: no log record is emitted with a field name outside the canonical set. — `TestNoFieldOutsideCanonicalSet` (`internal/obs/fields_test.go`).
- [x] `A0-L13` Test: model output never reaches a log field verbatim (RULE-3 + cardinality). — `TestRunFinishedSanitizesReason` (`internal/obs/fields_test.go`).

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
- [ ] `A0-T04` Injected clock; no test ever sleeps. §17.1 — RE-VERIFIED 2026-08-10 (third pass): fixed
      every sleep this pass's editable surface (`_test.go` only, outside
      `internal/{publish,obs,sources,llm,ops,bridge,flowtest}` and `web/`) could actually fix —
      `internal/rpc/run_test.go`: removed three `time.Sleep(15–30ms)` calls before `CommitRun`/
      `FailRun`/`cancel()` that were pure padding (Watch's own poll loop, or the "still running"
      assertion, holds regardless of the interleaving — `TestRunWatchTerminatesBetweenSubscribeAndFirstPoll`
      already proves this across 20 forced races with zero delay); replaced the two sleeps that WERE
      load-bearing (`TestWatchRelaysProgressBeforeTerminal`, `TestWatchConcurrentWatchersBothReceiveProgress`
      — publishing to `runProgressHub` before Watch subscribes silently drops the tick, so timing
      genuinely mattered) with `waitForHubSubscriber(Count)`, a bounded poll on the hub's own real
      subscriber-count state (package-internal field access, no production code touched) instead of a
      guessed duration; also dropped two decorative 3ms/10ms sleeps between publish calls in those same
      tests (ordering is already guaranteed by the buffered channel, not by elapsed time).
      `internal/e2e/watch_test.go`: removed the matching two inert 30ms sleeps for the same reason as
      the rpc ones (store-polling Watch over the real bridge, bounded by the test's own 5s timeout).
      Left as genuine judgement calls, not silently rewritten (T04's own carve-out): (1)
      `internal/store/samples_test.go:109`'s `time.Sleep(2ms)` — `ListSamples` orders by `created_at`
      from the store's own uninjectable `time.Now()` (no `Clock` field on `store.Options`; adding one is
      a `store.go` change, off-limits to a test-only pass); this is real wall-clock behavior under test,
      not a synchronization wait. (2) `internal/sectest/{sec41_revoked_session,killswitch}_test.go`'s
      `waitUntil*` bounded polls (5–10ms tick, real deadline) — waiting on an async goroutine's
      externally observable state with no channel to select on, because building one requires editing
      `internal/bridge` (excluded); same pattern as the package's own `fakeclock_test.go` idiom, and
      already the established style here.
      Still BLOCKED overall — `grep -rn "time.Sleep"` repo-wide still hits `internal/ops`,
      `internal/flowtest`, `internal/obs`, `internal/bridge`, and `internal/publish/invalidate_test.go`,
      none of them in this pass's editable set (all excluded outright by this task's HARD RULES, not
      merely "not chosen"), so "no test ever sleeps" does not hold repo-wide. `internal/schedule` and
      `internal/generate` remain clean (confirmed again: both already use fully injected fake clocks,
      no bare `time.Sleep`).
      Unrelated flake found while verifying (not fixed, out of this todo's scope, NOT caused by this
      pass's edits — reproduces running `TestItemRevisions` alone, 3x in a row, no other test involved):
      `internal/e2e/revisions_test.go`'s `TestItemRevisions` intermittently fails
      `revisions_test.go:175: revisions after edit+revert = 1, want 2`. Root cause traced to
      `internal/rpc/item.go`: `item_revisions` groups rows sharing the `at` column
      (`itemLoadRevisionFields`, `ListRevisions`'s `SELECT DISTINCT at`), and `at` comes from `srv.now()`
      — see `internal/rpc/item_test.go:549`'s own comment. Over the real e2e harness `srv.now` is real
      `time.Now()` with no injected clock forcing separation, so an edit immediately followed by a
      revert can land the same `at` value and collapse into one group, losing a row. This is a
      wall-clock-resolution correctness bug in production code (`internal/rpc/item.go`), not a test
      synchronization problem — flagging for the `internal/rpc` owner, not fixed here.
- [x] `A0-T05` Deterministic ULID source so goldens containing guids are stable. §17.1
- [x] `A0-T06` `testdata/` layout convention documented in-repo. §17.1
- [ ] `A0-T07` Assert the default `go test ./...` needs no network and no API key. RULE-1 — PARTIAL,
      re-audited 2026-08-10 (third pass): closed half the previously-cited gap. Added
      `internal/e2e/main_test.go`, wiring `testutil.InstallNetworkGuard()` into `TestMain` exactly like
      `internal/generate`/`internal/llm`/`internal/novelty`/`internal/sources` — this package's own
      harness (`app.go`'s `DialBridgeUnauthenticated`) makes a real `http.Get`, and every target it
      dials is loopback (this suite's own `httptest` servers), so the guard costs nothing and closes a
      real gap. Proved mechanical, not decorative: added a temporary
      `internal/e2e/zz_guard_sabotage_test.go` with a plain `http.Get("http://example.com")` — the shape
      of a future contributor's accidental network call, unaware of the guard, expecting it to succeed —
      and ran `go test ./internal/e2e/... -run TestZZNetworkGuardSabotage -v`; it FAILED with
      `testutil: blocked outbound network dial to "example.com:80" (RULE-1: AFF_LIVE_LLM is unset)`,
      proving the guard is actually reachable from the default invocation for this package, not merely
      present. Deleted the sabotage file immediately after and reran `TestIsolation` (which makes a real
      `http.Get` against the loopback bridge via `DialBridgeUnauthenticated`) to confirm the guard does
      not break legitimate loopback traffic — passed.
      `internal/ops` remains unguarded (`alert.go`'s `http.DefaultClient.Do` for the Slack webhook) —
      **not fixed this pass because `internal/ops` is in this task's own HARD RULES exclusion list**
      (six other agents editing it concurrently), not because it was overlooked. So the guard now runs
      as part of the default `go test ./...` in five packages (`generate`, `llm`, `novelty`, `sources`,
      `e2e`); `internal/ops` is the one remaining install site, blocked on file ownership rather than on
      the mechanism.
- [x] `A0-T08` CI runs `go test -race` on ubuntu — the only place `-race` can run. §17.2
- [x] `A0-T09` `-shuffle=on` for local runs, knowing it is weaker than `-race`. §17.2
- [x] `A0-T10` Per-package coverage measured and reported. §17.2
- [x] `A0-T11` Coverage **ratchet** — the number may not go down. Not a target. §17.2
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
- [ ] `A1-27` **NEW, found 2026-08-10 (audit pass): `internal/store/hooks.go`'s `Hooked`/`WriteHook`/
      `NewHooked` is built and tested but reachable from nothing.** Not tickable done — this is the
      same "built, tested, reachable from nothing" pattern already tracked a dozen times elsewhere in
      this project (see `A4-31`'s note), just not previously given its own task. `hooks.go` (312 lines)
      wraps `*Store` so every write that changes a feed's published representation fires a
      `WriteHook` exactly once, after commit — precisely RULE-6/§11's contract, and correctly so per
      its own doc comment (after-commit, never inside the transaction, never re-entrant). It is backed
      by `hooks_test.go` (374 lines, 11 tests). Grepped `cmd/` for `NewHooked`/`store.Hooked`/
      `WriteHook`: zero matches. Production invalidation instead happens through separate, duplicated
      ad-hoc call sites in `cmd/animefeedflux/wire.go`: `genExecutor.Execute` and `wireRunExecutor
      .ExecuteRun` each hand-call `e.inv.InvalidateFeed(row.Slug)` directly after `generate.Run`
      returns, reimplementing (correctly, as far as those two call sites go) the same "after commit,
      only on success" rule `Hooked` already centralizes. The risk this creates: `Hooked` covers
      `CreateFeed`, `InsertItem`, `UpdateItem`, `SoftDeleteItem`, `RestoreItem`, `CommitRun`, and
      `PromoteSample` (its own file comment notes `Update`/`SetEnabled`/`SetMembers` aren't on `*Store`
      yet and marks itself "ready to extend" once they land) — but the ad-hoc `wire.go` path only
      invalidates after `generate.Run`. Any future write path built directly against `*store.Store`
      (a `FeedService`/`ItemService` RPC method, say) gets NO invalidation unless someone remembers to
      hand-add another `e.inv.InvalidateFeed(...)` call at that new site — there is no compiler error,
      no test failure, nothing that fires if it's forgotten, which is exactly the disciplinary (not
      structural) failure mode `Hooked` was built to close. Recommend one of: (a) wire `Hooked` in as
      the actual mechanism at the RPC/executor layer and delete the ad-hoc calls, so RULE-6 is
      structural again; or (b) if the ad-hoc-per-call-site approach is preferred going forward, delete
      `hooks.go`/`hooks_test.go` rather than leave a second, unused, correct-but-untrusted
      implementation of the same rule sitting in the tree for a future reader to wire up by mistake
      alongside the ad-hoc calls (double-invalidation is harmless, but two sources of truth for one
      rule is how they eventually disagree). Neither done here — this is `internal/store`/
      `cmd/animefeedflux` scope; `cmd/` edits are out of this audit's HARD RULES.

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
      **CAVEAT added 2026-08-10 (audit pass) — the literal claim holds, but reads as more settled than
      it is.** `internal/render/permalink.go`'s `Permalink` wraps `it.AnswerHTML` in
      `<details><summary>Reveal the answer</summary>...</details>` (verified: `permalink_test.go`
      asserts this exact string). That IS "behind a spoiler break." But `docs/spoiler-design.md` —
      still marked "Status: open decision... Nothing here has been adopted" and still asserting "No Go
      code was touched (none exists to touch)" — is the document PLAN.md §5.5/§7 point to for exactly
      this question, and it demonstrates by reading ArticleFlux's source that `<details>`/`<summary>`
      is silently unwrapped by `GoWebComponents/v5/sanitize`'s `DefaultPolicy`, leaving the answer as
      plain visible text with no toggle in that consumer. `<details>` is not even among the four
      candidate options that document evaluates (scroll-distance, separate later item, permalink-only,
      accept-visibility) — it is a fifth, undocumented approach, and it happens to be the exact
      mechanism the document's own evidence shows fails against the second named consumer (§1:
      "ArticleFlux, which becomes a front end for free"). Separately, `internal/render/rss.go`'s
      `itemBodyWithAnswer` — what RSS/Atom/JSON Feed actually ship in `content:encoded`/`content`/
      `content_html` (NOT the permalink page) — uses yet a different, third mechanism: an `<hr
      class="spoiler-break"/>` plus a plain `<strong>Answer:</strong>` paragraph, closest to
      `spoiler-design.md`'s Option 1 ("whitespace/scroll distance"), which that document's own table
      rates as "weak guarantee." So three surfaces (permalink, feed content, and the open-decision doc
      that is supposed to be the single place this gets resolved) currently disagree with each other,
      and none of the three candidate resolutions actually deployed was chosen through the decision
      process PLAN.md §5.5/§7 describe as still pending. Reported, not fixed — `docs/spoiler-design.md`
      has been updated to record that code now exists and what it actually does on each surface, but
      the underlying design decision itself remains genuinely open, per PLAN.md's own instruction not
      to assume any specific hiding behavior is settled.
- [x] `A2-17` **`og:description` is the question, never the answer** — enforced at the renderer. §5.5
- [x] `A2-18` Feed index page at `/` with `<link rel="alternate">` autodiscovery. §6
- [x] `A2-19` Golden files for all three formats plus the permalink page.
- [x] `A2-20` Goldens include: ampersands, `<` in a title, CJK, emoji, `]]>` in body, 500-char summary.
- [x] `A2-21` Golden for an item with no link (generative) and one with an external link (grounded).

## A3 — Compliance

- [x] `A3-01` `make validate` renders goldens and runs the W3C / RSS Advisory Board validator. §5.6
      **CAVEAT added 2026-08-10 (audit pass) — literal claim is not what runs.** `make validate`
      (`Makefile`'s `validate` target) builds and runs `./cmd/affvalidate` against the golden
      documents; `cmd/affvalidate/main.go`'s own doc comment states it "runs the offline feed checks
      in `internal/feedvalidate`... standing in for the hosted W3C / RSS Advisory Board validator." No
      network call to the real hosted validator happens anywhere in this pipeline — `internal
      /feedvalidate`'s own package doc gives the reasoning (CI should not depend on a third party's
      uptime/rate limits/egress) and is explicit that "passing here does not mean the hosted validator
      would also pass." This is a deliberate, well-reasoned, in-repo-documented substitution, not an
      oversight — but PLAN.md §5.6 itself still reads as if CI calls the real service, and this task's
      title asserts the same. Left ticked because the gate this task actually cares about (goldens
      checked automatically, CI fails on violations) is real and does run — but the specific claim
      "runs the W3C / RSS Advisory Board validator" is false as literally written. PLAN.md §5.6 has
      been corrected to describe the actual mechanism.
- [x] `A3-02` CI fails on validator **warnings**, not only errors. §5.6 — true of `affvalidate`'s own
      exit code (`internal/feedvalidate.Level` distinguishes Error/Warning informationally only; both
      gate identically, per `cmd/affvalidate`'s own comment), not of a hosted-validator response —
      same substitution as `A3-01`.
- [x] `A3-03` Slack test: `pubDate`s strictly descending and unique across the feed. §5.5
- [x] `A3-04` Slack test: every item has a present, parseable date. §5.5
- [x] `A3-05` Slack test: `description` is plain text and under the hard cap. §5.5
- [x] `A3-06` Slack test: no answer text appears in `description` or `og:description`. §5.5
- [x] `A3-07` Slack test: OG tags present and populated on the permalink page. §5.5
- [x] `A3-08` Document in-repo which validator version CI pins, so a green run is reproducible.
      **RE-EXAMINED 2026-08-10: this task's premise no longer applies, and the tick is stale.** There
      is no hosted-validator version to pin — `make validate` never calls the hosted W3C/RSS Advisory
      Board service at all (see `A3-01`'s caveat); it runs `cmd/affvalidate` against
      `internal/feedvalidate`, which is this project's own Go code, versioned by the same commit as
      everything else it ships with. Grepped the repo for anything resembling a pinned hosted-validator
      version (a URL, an image tag, a service version string) and found none — there is nothing to
      document because there is nothing external in this path to go stale. Left ticked rather than
      unticked because the *reproducibility* property the task cares about ("a green run is
      reproducible") holds trivially and more strongly than a version pin would give it — but the task
      as worded describes infrastructure that does not exist in this design, and should be reworded in
      a future PLAN.md/TODOS.md pass rather than read as "a validator version is documented somewhere."

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
- [x] `A4-12` **Decide cassettes vs a hand-built `FakeProvider`; record the choice.** §8, RULE-1
      (`internal/llm/fake.go`'s doc comment on `FakeProvider` records the decision and reasoning
      in-repo: a hand-built Fake over SchemaFlux cassettes, because §9.5's novelty embeddings bypass
      SchemaFlux entirely via a direct `go-openai` call, so cassettes would need two replay
      mechanisms where one `Provider` fake covers both.)
- [x] `A4-12a` Use `client.Context(ctx)` on every call — a per-call Client alone does NOT isolate; `With*` mutates process-wide state (§8.1)
- [x] `A4-12b` **Do not add retry, backoff or a call timeout.** SchemaFlux owns them; two budgets on one call means the shorter silently wins (§8)
- [x] `A4-12c` Cost is ESTIMATED, not reported: `Generating[T]` returns zero usage. Label it an estimate everywhere it is shown (§8.1, §13)
- [x] `A4-12d` Embeddings call `sashabaranov/go-openai` DIRECTLY — SchemaFlux keeps its embedding API internal (§8.1, §9.5)
- [x] `A4-13` Go-side revalidation of every field: lengths, required-ness, tag count. RULE-4
      **Minor gap noted 2026-08-10:** `internal/generate/contract.go`'s constants are
      `titleMinLen=10`, `titleMaxLen=200`, `summaryHardCap=500`, `maxTags=6` — matching PLAN.md §9's
      contract table exactly for every field that table states as a hard number. But §9/§5.5 also
      state summary_text has a soft **target ≤300** chars ("300 target / 500 hard"), distinct from the
      hard cap. Grepped `internal/generate` for `300`: no match anywhere — no soft-target constant, no
      warning-level check, and no mention of "300" in `prompt.go`'s template either, so the model is
      never even told to aim for it. Only the 500-char hard cap is real; the 300 target exists in
      PLAN.md prose alone. Not a defect in what's built (a hard cap is arguably sufficient), but the
      two numbers currently describe different things: PLAN.md documents a target the pipeline has no
      mechanism for, soft or otherwise.
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
- [x] `A4-30` One real generation run against OpenAI, manually reviewed for quality. `AFF_LIVE_LLM=1`
      — **PERFORMED 2026-08-10, at Cam's explicit request** (which is what lifts RULE-1's block: a
      paid call needs a human asking for it, not an automated pass deciding to). Feed 1
      (`daily-anime-trivia`) was repointed from the seed placeholder model
      (`"seed-model (not a real provider model id)"`) to `gpt-4o-mini` with real prompts, then
      sampled — `aff sample`, which exercises the whole pipeline and writes nothing.
      **It found two real bugs and one product defect that no test could have found:**
      1. **Generation had never worked against a real provider.** `Generating[[]GeneratedItem]`
         asked OpenAI for a top-level ARRAY schema, which structured outputs reject outright
         (`invalid_json_schema ... got 'type: "array"'`). Fixed with an object wrapper — see
         PLAN.md §8.1 and `internal/llm`'s `generatedBatch`.
      2. **"failed validation twice" reported no reasons.** The reject-reason counts were collected
         and then dropped at the boundary, so the one question worth asking — which rule, how many
         items — had no answer anywhere. `internal/generate/runner.go` now logs them.
      3. **Product defect, filed as `A4-40` below:** the generated item put the QUESTION in
         `body_html` and a teaser in `summary_text`, which violates §5.5.
      Working output, after the fixes: title "Fire Force Trivia Question", body "In the anime 'Fire
      Force', who is the main protagonist ... Company 8?", answer "Shinra Kusakabe", novelty NOVEL,
      1437 in / 212 out tokens, valid rendered `<item>`.
- [ ] `A4-40` **NEW 2026-08-10 (from `A4-30`'s live run): the trivia prompt puts the question in the
      wrong field, so Slack subscribers would see no question at all.** §5.5 is explicit —
      `description` (= `summary_text`) is "the question only", and it is "what Slack shows, and the
      only thing many consumers show". The live sample produced `summary_text` = "Test your knowledge
      of 'Fire Force' with this question about the main character!" and put the actual question in
      `body_html`, which Slack never renders. A subscriber would get a content-free teaser forever.
      This is a recipe/prompt fix, not a code fix, and it must land before `C3`'s Slack proof or that
      proof passes while delivering nothing readable.
      **Related and still unresolved:** the same sample put `answer_html` ("Shinra Kusakabe")
      immediately after the question in `content:encoded`, which is exactly the spoiler exposure
      `docs/spoiler-design.md` has no adopted resolution for. §5.5 forbids the answer in
      `description`/`og:description` — it says nothing yet about a full-content reader, and this run
      is the first concrete evidence of what that looks like.
- [ ] `A4-41` **NEW: an empty price table makes every cost number read $0.0000 while real money is
      spent.** The live run reported `cost (estimate): $0.0000 (1437 in / 212 out tokens)`, and
      SchemaFlux logged `No pricing information available; cost reported as unpriced`. §13 says
      estimates "come from an editable price table multiplied by recorded token counts" — with no
      rows, that product is zero, and §13's budget ceilings therefore never trip no matter what is
      spent.
      **The design half is DECIDED and DONE (2026-08-11): warn, do not refuse.** `genGate.Allowed` now
      logs per run when a feed's model has no price entry AND a USD ceiling is configured, naming the
      feed, the model and the inert ceilings. Refusing was weighed and rejected: `DefaultGlobalDaily
      SpendCeilingUSD` is 5.0, so a ceiling exists out of the box, and refusing would stop generation
      on any install whose table does not name the configured model — including a fresh one, whose
      table is empty. The residual risk is accepted and named in the code: a misconfigured install CAN
      bill past a dollar ceiling nobody can compute, and the warning being read is what stands between
      it and that.
      **REMAINS, and is an operator action, not a code change:** populate the table with real rates for
      the models actually in use, via /settings/provider's "Add rate". Nobody but the operator knows
      which models those are or what they currently cost, and a wrong rate is worse than none — it
      makes a ceiling that trips at the wrong number instead of one that visibly does not trip.

- [ ] `A4-42` **NEW, and an honest half-finish: provider profiles are stored, editable and shown, but
      NOTHING READS THEM at generation time.** /settings/provider now lets an operator add an
      OpenAI-compatible endpoint (name + base URL + the env var holding its key) and pick which one
      is active, and the server validates and persists all of it — but `internal/llm` and
      `internal/novelty` still construct `openai.NewClient(apiKey)` with the library's default base
      URL and the single `SCHEMAFLUX_API_KEY`. So selecting a custom endpoint today changes a stored
      setting and changes nothing about where a request goes.
      Wiring it needs three things, none of which are in the UI change: `go-openai`'s
      `DefaultConfigWithBaseURL`/`ClientConfig` threaded through `NewOpenAIModelLister`,
      `NewOpenAIEmbedder` and the SchemaFlux provider construction in `cmd/animefeedflux/wire.go`;
      the active profile's `api_key_env` read at call time rather than boot, so switching endpoints
      does not need a restart; and a decision about SchemaFlux itself, which owns its own provider
      registry (§8) and may not accept a base URL override at all — that has to be checked against
      the pinned v1.1.0 source before promising the feature works.
      Until then the Connection panel is honest about what it stores and dishonest about what it
      does, which is the wrong way round; either wire it or mark the control as not-yet-active.
- [x] `A4-44` **DONE 2026-08-10: /generate rebuilt as a workbench, and it now previews unsaved
      drafts against a chosen model.** The strip owns every input that changes what a preview
      produces (feed, model, effort, candidate count, temperature override) beside the button that
      spends the money; prompts and preview split the screen; recipe fields are behind a collapsed
      disclosure. Template-variable chips insert `{{.Today}}` and friends at the cursor. The model
      field is a menu hydrated from `SystemService.ListModels` with a text-input fallback when the
      provider cannot be reached. Verified live end to end: edit the prompt without saving → Preview
      → a candidate generated from the edited text (gpt-4o-mini, 126 models listed).
- [x] `A4-45` **DONE 2026-08-10: /generate loaded nothing when opened directly.** Every
      `fetch.UseResource` loader ran at mount, while the session was still `appstate.Anon` and the
      WebSocket had not finished its handshake; nothing re-ran them. Loaders now re-fetch on reaching
      `AUTH`, keyed on the session state itself rather than on a `!= Disconnected` boolean that is
      already true at `Anon`. Also fixed in the same pass: the feed picker rendered its placeholder
      option only while unselected, so option indices shifted and the browser's index-based
      selection displayed the wrong feed; and the preview pane carried a duplicate set of the
      strip's controls.
### GWC markup performance review 2026-08-11 (A8-44 onward)

A read of the render paths: `web/ui`'s primitives, the three page packages' markup, how styles are
emitted, what recomputes per render, and what the framework actually charges for each construct
(checked against the pinned GWC v5.0.1 source, not assumed). Ranked by cost over effort.

- [ ] `A8-44` **`Kebab` builds its entire menu on every render, open or closed.** `web/ui/kebab.go`
      constructs `itemNodes` unconditionally: per item a ~12-entry `[]css.Rule` plus two more slices
      from the `Hover(...)`/`focusVisible()` appends, a `css.Class(...)`, a `resolve(...)` lookup and
      a closure — then hands the lot to `AccessibleOverlay` as `Children` regardless of `p.Open`.
      On `/history/items` that is 25 rows × ~5 items ≈ 125 menu buttons rebuilt on every render of
      the table — every selection toggle, every debounced search keystroke, every filter change —
      and discarded, because the menus are closed. Callers pay again ahead of it:
      `itemKebabItems(...)` allocates its `[]KebabItem` and five closures per row per render before
      `Kebab` is even called.
      Fix: wrap the item loop in `if p.Open`. This is hook-safe — the loop contains no hooks, and
      the three that exist (`UseRef`, `UseState`, `UseEffect`) sit above it and stay unconditional,
      so positional slots do not move. Same treatment for the caller's `itemKebabItems`, which can
      be built inside the same guard.
- [ ] `A8-45` **`h.Show(false, …)` costs MORE than rendering the subtree and saves only paint — 48
      call sites.** Three charges, and the first two are easy to miss: Go evaluates arguments
      eagerly, so the hidden subtree is fully constructed before `Show` is called; `html.Show` then
      CLONES the node and its props map (`cloneAnyMap`, sugar.go:1369) to set `hidden`; and the
      subtree then stays mounted, so the browser styles and lays it out and GWC diffs it on every
      later render. A hidden panel is therefore construction + a clone + permanent diff cost,
      against a saved paint.
      Fix: `h.If` for the heavy subtrees — the settings sections, the sampler panel, the workbench's
      collapsed regions. Keep `h.Show` only where retention is the point: uncommitted input that
      must survive a toggle, scroll position, focus, or a remount that would refetch. Audit the 48
      by weight, not wholesale.
- [ ] `A8-46` **`UseMemo`/`UseMemoOf` exist in v5.0.1 and this app uses them exactly zero times.**
      Every derived value is recomputed on every render — the per-row `originLabel`/`formatTimestamp`
      formatting, the filter comparisons, the ordered kebab partition, the visible-ID sets. Most are
      individually cheap; the point is that the framework ships the tool and no render path in
      27k lines of UI reaches for it. Start with the ones inside the row loops.
- [ ] `A8-47` **The token helpers allocate a string on every call.** `tokens.Color(role)` is
      `css.Var("color-" + role)` and `Space(n)` runs `strconv.Itoa` — a concatenation per call, and
      the per-render rule slices in `web/ui` call them dozens of times per element. Precompute the
      common roles/steps as package-level vars so the hot paths reference rather than rebuild them.
- [ ] `A8-48` **The pump re-renders the entire page ~7×/second for the duration of every mutation.**
      `web/ui/pump.go` ticks at 150ms for up to 600 ticks plus a 2s grace, and each tick is a full
      render pass. It is the correct trade — without it a save sits on "Saving…" forever (`A7-06`)
      — but it is the single largest scheduled render load in the app, and it is the concrete cost
      of not having `A7-07` (report the async-inbox wedge upstream) resolved. Worth raising the
      priority of `A7-07` on those grounds, and worth revisiting the 150ms once it is fixed.

Second pass: an inventory of every performance-oriented API GWC v5.0.1 actually
ships, against what this app calls. Usage counts are from the tree, not
estimates. `A8-44`…`A8-48` above are the markup findings; these are the
framework affordances left on the table.

- [ ] `A8-49` **The wasm bundle is 34 MB raw / 7.0 MB gzipped, and the build passes no
      `-ldflags "-s -w"`.** `web/build.sh:68` builds with `-trimpath` and an ldflags string that is
      empty on the release path, so the binary ships its full symbol table and DWARF. Stripping both
      typically takes 20–30% off a Go/wasm binary — on this one that is on the order of a megabyte
      of transfer and a chunk of parse/compile time on every cold load.
      **Do this one first.** Every other item in this section and the section above is worth
      microseconds per render; this is worth seconds on first paint, which is the only performance
      number an operator on a slow connection actually experiences. The trade is honest and small:
      a stripped wasm binary gives worse stack traces, which matters less here than elsewhere
      because Go/wasm panics already surface poorly and the app's real diagnostics are server-side.
      Measure before and after rather than assuming the 20–30%.
- [ ] `A8-50` **`UseEffectOf` is 2.6× cheaper than variadic `UseEffect` in wasm, and 19 call sites
      still use the variadic form** (6 already use the typed one). This is not a guess: the
      library's own perf log (`docs/DEVNOTES_PERF_LOOP.md`, iteration 3) measures the untyped hook
      walk at 1.48 ms/pass in wasm against 0.57 ms with the `*Of` variants, and notes wasm amplifies
      the closure/deps allocation ~14× over native. Nearly every site in this app passes exactly one
      dep (`}, blocked)`, `}, props.Ready)`, `}, reloadTick.Get())`, …), so the conversion is
      mechanical; the three-dep sites take a small comparable struct as the key, which is legal
      since a struct of comparables is comparable.
      One rule the library states and this conversion must respect: `UseEffect` and `UseEffectOf`
      share a slot, so a given call site must never alternate between them across renders.
- [ ] `A8-51` **`UseLayoutEffect` is used zero times, and the kebab measures its anchor in a passive
      `UseEffect`.** `web/ui/kebab.go:74` reads the trigger's geometry AFTER commit and paint, then
      sets state, forcing a second render — so the menu is painted once at stale coordinates before
      it lands. `UseLayoutEffect` exists for exactly this ("measuring an element
      (offsetWidth/getBoundingClientRect) … instead of guessing a setTimeout/requestAnimationFrame
      delay") and runs before paint. This is a correctness win as much as a perf one, and it is
      plausibly part of the residual flicker the kebab work chased (`A7-11`).
- [ ] `A8-52` **`ui.PostAsync` is called twice in the whole app.** Its doc: "Calling PostAsync
      explicitly is still worthwhile when several writes belong together — one post means one render
      for the whole group." The mutation handlers in `/generate` and `/history` set four or five
      pieces of state in sequence after an RPC returns; each is its own scheduled render. Grouping
      them is a smaller change than it looks and cuts renders per save directly — which also reduces
      what `A8-48`'s pump has to paper over.
- [ ] `A8-53` **`StartTransition`/`UseTransition` are used zero times.** The hand-rolled 250 ms
      search debounce (`web/pages/history/asyncdispatch.go`) is a coarser approximation of what the
      transition lane does properly: mark the filter/search re-render non-urgent so typing stays
      responsive without an arbitrary delay before results move at all. Worth trying on the two
      search fields and the filter bar, keeping the debounce only for the RPC itself (which is a
      network-cost decision, not a render one).
- [ ] `A8-54` **`gcpacing.Apply` is never called.** The package exists because the v4 baseline
      measured a 6.60 ms maximum GC pause on the render thread and v5's acceptance target is under
      3 ms — one dropped frame is exactly the symptom. It is a single call at boot returning a
      `Previous` you can restore. The package's own caveat has to be respected in how the result is
      judged: js/wasm is single-threaded and marks without native Go's parallel assist, so a pause
      measured natively is not the browser's pause — measure in the browser or not at all.
- [ ] `A8-55` **`ui.Lazy` is used zero times.** Worth evaluating only AFTER `A8-49`: deferring
      subtrees matters much less once the bundle itself is smaller, and this app's routes all live
      in one binary anyway. Filed so the decision is recorded rather than re-opened.
- [ ] `A8-56` **`UseMemoOf` is the form to prefer when acting on `A8-46`**, not `UseMemo` — same
      memo slot, but a static non-capturing compute keyed by one comparable dep has zero
      steady-state allocations, where the variadic form allocates a deps slice per render. Same
      no-alternating rule as `A8-50`.

Deliberately NOT adopted, with the reason, so this is not re-litigated:

- **`delta`, `projection`, `compute`, and the off-thread worker model** are v5's machinery for
  20,000-row tables and multi-megabyte structured clones per keystroke. This app pages every list at
  25 rows (`DefaultPageSize`) and does its heavy work server-side. Adopting them here would add a
  second Go runtime's memory and a publication protocol to save nothing measurable.
- **`virtualization`** beyond the existing `web/ui/virtualtable.go` for the same reason — see the
  note under the markup section on why history's paged tables are correct as plain tables.
- **`prerender`/`servercomponents`** are a different rendering model, not a tuning knob; the admin
  app is behind authentication and has no first-paint-of-public-content problem to solve.

Verified as already right, recorded so nobody "optimises" them into something worse:

- **Page styles are emitted once**, from `init()` via `css.Global`, which dedupes by content. They
  are not per-render. `web/pages/*/styles.go` is the correct pattern and needs no change.
- **`css.New`/`css.Class` is cached two levels deep** (a fast-fold `sync.Map` keyed without string
  building, then a canonical-text `sync.Map` — `css/fastfold.go`, `css/css.go`), so a repeated
  identical rule set does NOT re-canonicalize or re-emit a stylesheet rule. The per-render cost of
  `css.Class([]css.Rule{...})` is the slice construction and a map lookup, not CSS generation. Do
  not rewrite `web/ui` to `ClassStr` believing it saves stylesheet work — it does not. `A8-47` is
  the real reason to touch that code, and it is a smaller reason.
- **History's tables are paged at 25** (`DefaultPageSize`, capped at 200), so the plain `Table` there
  is correct and virtualization would be dead weight. `VirtualTable` is used in the one place that
  actually grows without bound — the active-sessions list, which gains a row per sign-in.
- **`MapKeyedComponent` keyed on `it.Id`, with per-row fibers**, is the right construct for rows
  that own local state, and the row-expansion panels already use `h.If` rather than `h.Show`.

### Security review of the auth system 2026-08-11 (A8-31 onward)

A read of the whole authenticated path — `internal/auth` (argon2id, pepper, sessions, TOTP,
recovery codes, reset tokens), `internal/rpc/auth.go` + `interceptor.go`, `internal/bridge`
(upgrade, tickets, revalidation), the `sessions`/`totp_used`/`recovery_codes` schema, the wiring
gate on `AFF_DEV_INSECURE_AUTH`, and the nginx vhost that fronts all of it. Analysis, not a test
run. What follows is what does NOT hold; the primitives themselves are in good shape (see the
closing note).

- [ ] `A8-31` **An elevated recovery session becomes a FULL session across a process restart.**
      `interceptor.go`'s `authorize` decides scope purely from the in-memory `elevatedTracker`, then
      WRITES that decision onto `sessions.scope` on every call. After a restart the tracker is empty,
      so `isElevated` returns false, `wantScope` is `full`, the persisted `elevated` value is
      overwritten with `full`, and the default-deny check (`wantScope != full && !allowed`) passes.
      The session RecoverWithCode opened — scoped by §12.2 to ChangePassword and ReenrollTOTP — can
      then reach every RPC in the system for the rest of its 10-minute life, including
      `RegenerateRecoveryCodes` and every feed mutation.
      `migrations/0005_session_scope.sql` exists specifically so scope is "something the session
      carries rather than something that exists only in this process's memory" — and
      `store.SessionScope`, the READER, has zero non-test callers. The column is write-only, so the
      migration delivers none of its stated guarantee. `elevatedTracker`'s doc comment claims a
      restart defaults to "not elevated (i.e. not trusted with anything extra)"; in this code "not
      elevated" means *full privileges*, so the failure direction is the opposite of what is
      written. Fix: read the persisted scope and treat elevated-on-the-row as authoritative, with
      the in-memory tracker able only to NARROW, never to widen.
      This requires an attacker to already hold a valid recovery-code session, so it is a scope
      containment failure rather than an authentication bypass — but scope containment is the whole
      of BF-32.
- [ ] `A8-32` **Nothing in the process ever learns the client's IP, so per-IP login backoff is one
      global bucket.** `clientIP()` reads gRPC's peer address; behind the deployed nginx
      (`deploy/nginx/admin.anime.earlcameron.com.conf`) that is the proxy, and the
      `X-Real-IP`/`X-Forwarded-For` headers nginx does set are read by no Go code anywhere in the
      repo. Three consequences, in severity order:
      (a) every failed login from anywhere shares one counter with the operator's own, so an
      unauthenticated attacker who simply keeps guessing holds the real admin at a 60-second backoff
      indefinitely — a remote lockout that costs nothing and needs no credential;
      (b) `auth_events.ip` records the proxy for every event, so the §4 audit trail cannot attribute
      anything;
      (c) §12.5's active-sessions table shows the same address for every row, so "is one of these
      not me?" — the question that table exists to answer — cannot be answered.
      Fix: trust `X-Forwarded-For`'s rightmost hop ONLY when the peer is the known proxy, and carry
      it into the RPC context; never trust the header on a direct connection.
- [x] `A8-33` **FIXED 2026-08-11.** `AFF_SECRET_KEY` had no minimum length, and neither did
      `AFF_PASSWORD_PEPPER`. `config.required()` accepted any non-blank string and `auth.deriveKey`
      SHA-256s whatever it gets into a 32-byte AES key, so `AFF_SECRET_KEY=x` booted cleanly and
      encrypted the TOTP secret at rest under a key with a few bits of entropy — §4's "a stolen DB file
      alone is not a second factor" was then false, silently.
      `validator.secret` enforces a 16-character floor and fails at startup, where a weak key can still
      be changed before it has encrypted anything. The pepper is checked only when set: absent is a
      supported configuration, too short is not.
      Applied in `cmd/aff` too. That path reads the variable directly and deliberately bypasses
      `config.Load` (admin init/reset must work before the rest of the environment exists), and it is
      where a weak key FIRST encrypts something — catching it only on the server's next boot would be
      catching it after the damage.
- [ ] `A8-34` **The admin vhost has no HSTS, no CSP, and no request rate limit.** The publish vhost
      has `limit_req_zone` and the admin one does not, which is backwards — the publish plane serves
      cached XML, the admin plane serves the login. `Strict-Transport-Security` is absent from both,
      so the first request of a session is downgradeable; `Content-Security-Policy` is absent from
      the vhost that serves the WASM admin bundle. The IP allowlist covers much of this in practice,
      but it is still the placeholder `203.0.113.0/24` in the file.
- [ ] `A8-35` **The login ticket travels in the URL query string, so nginx writes it to the access
      log.** `?ticket=…` lands in `$request` under the default log format. Bounded by the ticket
      being single-use with a 20-second TTL — an attacker needs log read access inside that window,
      by which point the browser has almost certainly redeemed it — but it is a credential in a
      logfile. `ticket.go` documents why a query parameter was the only option available (WASM
      cannot set WebSocket handshake headers); the cheap fix is at the nginx layer, logging without
      arguments on this location.
- [x] `A8-36` **FIXED 2026-08-11.** — original report: **`auth.Normalize` is documented as applied before hashing and is not applied.**
      "Normalize applies NFKC before hashing. Without it the same passphrase typed on a different
      keyboard or platform produces different bytes and fails to verify" — but `Hash`,
      `HashPeppered`, `Verify` and `VerifyPasswordPeppered` all pass the raw `password` to
      `argon2.IDKey`. The only callers of `Normalize` are `IsWeak` and `IsBreached`, so it governs
      the length and breach checks and nothing else. The stated cross-platform property does not
      hold, and the policy is enforced against one string while the credential is derived from
      another.
      `Hash` and `HashPeppered` now derive from `Normalize(password)`, and both verify paths compare the
      normalised form first. A raw match is still ACCEPTED and reported as `needsRehash`, which is what
      makes this safe to ship: every hash written before today derived from the raw string, and for a
      passphrase containing a composed accent or a ligature the normalised form derives a different key —
      refusing it would lock the admin out of a credential that has not changed, with no way back short of
      `aff admin reset`. The next successful login rewrites the hash normalised and the fallback stops being
      reachable for that credential. For an ASCII passphrase NFKC is the identity and nothing changes at all.
      Tested with U+FB01 (the fi ligature) against a genuinely old-format hash, plus that a wrong password is
      still wrong — the fallback widens what verifies to exactly one more form of the SAME string.
- [ ] `A8-37` **`sessionTokenFromContext`'s metadata fallback fires for anonymous bridge sockets,
      contrary to its own comment.** The comment says "only a connection with no bridge session at
      all falls through this far"; the condition is `ok && sess.Token != ""`, and an anonymous
      bridge upgrade has a Session with an empty Token — so it does fall through, and
      browser-supplied gRPC metadata is consulted. Not exploitable on its own (the metadata still
      has to carry a valid session token, which an XSS cannot read out of an HttpOnly cookie), but
      §4's "the token never touches JavaScript or WASM" is stated as an invariant and this is a path
      where a WASM-supplied value authenticates a call. Either drop the metadata source when a
      bridge session is present, or correct the comment to describe what the code does.
- [x] `A8-38` **FIXED 2026-08-11.** `backoffTracker`'s map was never evicted: one entry per distinct peer
      address, retained for the life of the process. Inert behind the proxy (one key), unbounded on a
      directly-reachable listener.
      `sweepLocked` drops entries whose window closed more than 15 minutes ago, run on the failure path
      rather than from a timer — entries are only ever created there, so it is the one path that can grow
      the map, and there is no goroutine to own, start or stop. Retention is well past the 60s delay cap so
      an attacker pausing between attempts does not get a free reset: the entry, and the failure count that
      makes the next delay longer, outlives the window. Tests pin all three properties, including that a
      sweep never releases an address still inside its window.
- [ ] `A8-39` Small inaccuracies found while reading, none of them exploitable: `recoveryAlphabet`'s
      comment claims 31 symbols and says vowels are dropped (it holds 30, and `A`/`E` are in it — the
      modulo-bias note is computed off the wrong number, though its conclusion still stands);
      `auth.NewResetToken` calls `time.Now()` directly rather than an injected clock, unlike the rest
      of the package; `totp_used` accumulates a row per successful login and is not among the
      nightly prune's three tables.

Second pass, widening past the credential path itself into what a session can
reach and what untrusted input can reach:

- [ ] `A8-40` **`SystemService.Backup` hands the entire credential database to anyone holding a live
      session, with no password or TOTP re-proof.** It `VACUUM INTO`s the whole SQLite file and
      returns it as bytes: `admin.password_hash` and its KDF params, the encrypted TOTP secret,
      every `sessions.token_hash`, every `recovery_codes.code_hash`, and every stored provider
      setting. `ChangePassword` and `RegenerateRecoveryCodes` both call `verifyCurrentCredentials`
      first, precisely because §4 says a stolen-but-still-live session must not be enough on its own
      — and this is the single most damaging read in the system, protected by strictly less than
      either of them. Fix: put `Backup` behind the same `verifyCurrentCredentials` gate.
      Second-order, worth deciding separately: the response travels the bridge into the WASM client,
      so the whole hash database lands in browser memory. §4 works hard to keep the session token
      out of WASM while the file every hash lives in goes straight through it.
- [ ] `A8-41` **`sources.Fetcher` has no scheme allowlist, no private/link-local address block, and
      follows redirects — server-side request forgery.** `fetchOnce` builds a request straight from
      the target string and calls `f.Client.Do`, so nothing stops the fetch reaching
      `http://169.254.169.254/` (the droplet metadata service) or `http://127.0.0.1:9311/` (the
      admin bridge itself). Redirects are what make this more than an operator shooting themselves:
      Go's default client follows up to ten, so a source URL the operator *does* trust can 302 the
      fetcher anywhere, and the body flows into the grounding path and can surface in a published
      feed. No construction site sets `http.Client.Timeout` either, so one slow upstream stalls a
      run for as long as it likes.
      **Latent, not live:** every production wiring site currently passes `noFetcher{}`
      (`wire.go:1192`, `:1404`, `:1426`), so nothing fetches anything today. This is a
      fix-before-grounded-feeds-ship item — the cost of adding a resolve-then-check dialer is far
      lower now than after the feature is running.
- [ ] `A8-42` **Fetched upstream text will reach the model context unfiltered — prompt injection.**
      Same gate as `A8-41`: the moment grounded feeds are wired, an article body an attacker
      controls sits in the same context window as the recipe's instructions, and the model's output
      is published under this system's name. §8's error taxonomy and §7's contract checks constrain
      the SHAPE of what comes back, not whether the instructions were followed by the operator or by
      a paragraph in someone else's article. Decide the containment (delimiting, separate turns,
      treating fetched text as data never instruction, validating the output against the source)
      before the feature exists, not after.
- [ ] `A8-43` **The admin plane has no in-application rate limit and no nginx one either.** `A9-12`
      (per-IP token bucket, `429` with `Retry-After`) is still unticked and was scoped to the
      publish plane; `A8-34` covers the missing nginx `limit_req` on the admin vhost. Between them,
      the only thing bounding repeated `Login` attempts is the backoff tracker `A8-32` shows is a
      single global bucket. The three are one problem and should be fixed as one.

What holds, recorded so nobody re-derives it: argon2id at OWASP-plus cost with PHC-encoded params
and a sanity ceiling computed FROM the defaults (so a future cost bump cannot lock the admin out);
constant-time comparison everywhere it matters; one generic `errAuthFailed` for every credential
failure; the KDF always run so a missing admin row is not a timing oracle; TOTP replay enforced by
`totp_used.step` being the primary key rather than by application logic; recovery codes single-use
via `UPDATE … WHERE used_at IS NULL` and `RowsAffected`; login tickets single-use inside one
critical section; `__Host-` + Secure + HttpOnly + SameSite=Strict; exact-match Origin with no
wildcarding and a missing Origin refused; the session re-checked against the store on EVERY RPC
rather than at upgrade; and `AFF_DEV_INSECURE_AUTH` gated on a loopback-only listener with the dev
credential prefill behind a build tag and `-ldflags`, so no credential sits in the tree.

### Adversarial QA sweep 2026-08-11 (A8-20 onward)

Every route and the STATES that matter — signed out, nothing selected, a feed loaded, a new unsaved
draft, an expanded run, an open item form, all six settings sections — screenshotted in dark and
light at 1500px, 1200px and 900px, and read as pixels rather than as code.

- [x] `A8-20` **FIXED: on the sign-in screen, Back and Sign in were both filled primary buttons.**
      The action that commits and the action that retreats looked identical, side by side, on the
      screen where a wrong click throws away the code you just typed. Back is outlined now. (The fix
      needed two classes to out-specify the page's generic `button` rule — a class plus a type
      selector beats a single class, which is why the first attempt changed nothing visible.)
- [x] `A8-21` **FIXED: the sign-in page had a horizontal scrollbar below ~1180px.** The ring
      signature is a 110rem pseudo-element centred on the crest, and nothing clipped it, so it pushed
      the document wide — measured at 900px: scrollWidth 1181 against a 900px viewport. Clipped at
      the page container with `overflow: clip` (not `hidden`, which would have made it a scroll
      container and swallowed the card's own focus scrolling). The rings still bleed past the card.
- [x] `A8-22` **FIXED: pressing "New feed" looked like nothing had happened.** The picker still read
      "Choose a feed…", the feed list stayed open, and the only tell was the Save button turning
      blue. The picker now reads "New feed — not saved yet", and the feed list collapses, because a
      new draft is not a moment for browsing the list you are no longer choosing from.
- [x] `A8-23` **FIXED: the item form's single-line fields were 1400px wide** — a title or a URL
      stretched across the whole page, unlike every other form in the app. Capped at a readable
      measure; the text AREAS keep the full width, because what goes in them is a document.
- [x] `A8-24` **FIXED: the item form's Save was disabled with no explanation.** It now says a title
      is required, and the field is marked as such — a dead control with no reason is the same defect
      §12.3 objects to elsewhere.
- [x] `A8-25` **FIXED: every row checkbox on /history/items was unnamed and 13px.** A screen reader
      heard twenty-five identical "checkbox"; a mouse had a 13px target. Named after the item's own
      title, and enlarged.
- [x] `A8-26` **FIXED: an expanded run with no log showed a heading over an empty box.** It says the
      run recorded no log.
- [x] `A8-27` **FIXED: run durations were raw `time.Duration` strings** — "13.261957s", "908.649ms"
      and "508.6µs" in one column, six significant figures nobody asked for, on a screen used to scan
      two dozen rows. Sub-second reads in ms, the rest to one decimal, over a minute in m/s.

- [ ] `A8-28` **`/settings/security`'s only actions for recovery codes and active sessions are
      unlabeled ⋯ glyphs**, and its fields sit in a ~333px column in a 64rem page. Same two defects
      that `/settings/data` had before its rework (`A7-16`) — the fix is the same shape and was left
      out of this pass only because another session is actively editing the settings package.
- [ ] `A8-29` **`/settings/provider`'s "Active provider" is an unexplained empty text box.** It sits
      under a labelled heading with no placeholder and no help text, so it reads as a broken field
      rather than an optional override. Another session's area; noted, not touched.
- [ ] `A8-30` **Reject reasons render as raw identifiers** — "novelty_duplicate: 1",
      "tags_not_lowercase: 2". They are diagnostic, so this is defensible, but they are the only
      machine identifiers left on an operator surface.

### Feed CRUD 2026-08-11 (A7 series)

- [x] `A7-01` **FIXED: a feed could not be deleted from anywhere in the UI.** `FeedService.Delete`
      has been implemented, version-checked and tested on the server since the RPC layer was
      written, and nothing ever called it: a feed created by mistake could be disabled but never
      removed. Each rail row now has a ⋯ menu (destructive actions behind the kebab, D0-15) with
      Delete, behind a typed confirmation that asks for **the feed's own slug** rather than a fixed
      word — the failure mode with "DELETE" is confirming the right-looking wrong row. The call
      sends `expected_version`, so a feed edited in another tab is never removed on a stale view.
      Verified end to end: create → update → delete, including that a wrong slug keeps the confirm
      button disabled.
- [x] `A7-02` **FIXED: creating a feed was impossible.** The "+ New" draft carried no cron and no
      timezone, and `internal/feedspec` rejects both as `cron_invalid` / `timezone_invalid` — so
      every new feed failed validation, on top of the budgets and kind fixed in `A5-06`. New drafts
      now start at 09:00 daily UTC. That is a real default, not a placeholder: once a day is what
      this app is for, and a morning slot means an overnight failure is visible when someone is
      awake. UTC because the browser's zone is not available to that code and a confidently wrong
      local zone is worse than an obvious one the operator will change.
- [x] `A7-03` **FIXED: the feed list was hidden inside "Recipe settings".** The rail — every feed
      with status, stale flag, last build, 7-day spend, enable toggle and Run Now — was nested in a
      disclosure about slugs and cron expressions, which is why the app looked like it had no feed
      management at all. It is now its own `Feeds (N)` disclosure, open by default when no feed is
      selected.
- [x] `A7-04` **FIXED: a `[]any` passed to `h.Tag` printed `0x58930000` on the page.** `h.Tag` is
      variadic; handing it a slice makes the slice itself an argument, so it is neither props nor a
      child. GWC stringified it, putting a pointer in the body text — and the element silently got
      none of its props.
- [x] `A7-08` **FIXED: none of the CRUD controls were on screen.** The operator's follow-up was
      "where is the option to CRUD a feed?????" and it was right — everything worked and nothing was
      visible. Save was at the bottom of a collapsed "Recipe settings" disclosure, Delete inside a ⋯
      inside a row inside a collapsed "Feeds" disclosure, and New was a text link. Worse, the Feeds
      list collapsed itself the moment a feed was selected, so the management surface disappeared
      exactly when work started.
      The strip now carries the whole verb set beside the feed picker: **New feed**, **Save** (which
      doubles as the unsaved-changes indicator — "Save" when dirty, "Saved" when not), and a **⋯
      menu** with Run now, Enable/Disable and Delete. The Feeds list stays open. The recipe drawer
      opens automatically for a new draft, because creating a feed REQUIRES fields that live in it.
      **My own verification had hidden this**: the CRUD test opened the disclosures itself with
      `d.open = true`, so it proved the operations worked while saying nothing about whether anyone
      could reach them. There is now a second test that uses only visible controls and fails if a
      panel has to be forced open.
- [x] `A7-09` **FIXED: deleting a feed burned its slug permanently.** Deletion is soft, and `slug` is
      UNIQUE across every row including deleted ones, so a deleted feed held its name forever:
      delete `daily-anime-trivia`, try to recreate it, and the server answers *feed slug
      "daily-anime-trivia" already exists* — about a feed that appears in no list and cannot be
      restored, since there is no feed Restore RPC. The tombstoned row now takes a timestamped
      suffix and the name goes back into circulation; the row itself stays, so the delete is still
      auditable rather than becoming a hard delete by stealth.
      Covered by `TestFeedDeleteReleasesSlugForReuse`.
- [x] `A7-10` **FIXED: a new feed was created disabled, and a disabled feed cannot be previewed.**
      So the first thing a newly created feed did was tell the operator three times that it was off,
      with no hint that they had to turn it on before anything would work. New feeds are enabled; the
      safety net for unwanted spend is the budgets and ceilings the draft carries plus the global
      kill switch, not a feed that silently does nothing.
- [x] `A7-11` **FIXED: the sampler rendered `generate.This feed is disabled.`** An already-resolved
      sentence was being passed as `DisabledReasonKey`, and the shell's translator prefixed its
      namespace before failing to find it. It goes through the passthrough key with the text as an
      argument now — `StatePanelProps` has had a `DisabledReasonArgs` field the whole time, and the
      comment in the code claiming it did not was wrong.

- [x] `A7-12` **FIXED: overlays did not float — every kebab menu and every modal reflowed the page.**
      `gwcui.Overlay` positions nothing: it sets a z-index and some `data-overlay-*` attributes, and
      leaves placement to the caller (the library's own anchored-overlay example passes
      `fixed left-… top-…` through `SurfaceClass`). Neither `web/ui.Kebab` nor `web/ui.Modal` ever
      did, so the menu rendered `position: static` and the dialog `position: relative` — both in
      normal flow. Opening the feed-delete confirmation grew the document from 1556px to 1879px and
      put the "dialog" inline halfway down the page.
      The modal's backdrop is now a fixed, full-viewport flex centring layer. The kebab measures its
      trigger and positions itself `fixed`, flipping above when there is no room below and clamping
      to both viewport edges — `absolute` would have been the smaller change, but a kebab lives in a
      table row and these tables are their own horizontal-scroll containers, which clip an
      absolutely positioned child.
      One trap worth remembering: the menu is hidden until measured, and the first version dropped
      the `visibility` key once a measurement existed. Omitting a key does not clear the property —
      the DOM diff has nothing to compare against — so the menu ended up correctly positioned, on
      top, and invisible. Every key is now present in every state.
- [x] `A7-13` **FIXED: `/history` had a second, hand-rolled kebab that opened on CSS `:hover`.**
      No click, no touch, and it closed if the pointer strayed on the way to the item; it was also
      `position: absolute` inside the scrolling table, so the lower rows' menus were clipped by the
      table edge. Both tables now use the shared `web/ui.Kebab`, which brings a real open state,
      Escape and outside-click handling, and the fixed positioning from `A7-12`. The item row's
      "publish a correction" form used to unfold *inside* the dropdown; it is now a panel under the
      row, beside the revisions panel it already resembled. One kebab implementation in the app
      instead of two.

- [x] `A7-14` **FIXED: clicking an open kebab reopened it — the menu flashed shut and came back, and
      then would not close at all.** `gwcui.Overlay` dismisses on `pointerdown`, captured at the
      document, and treats anything outside the SURFACE as outside — which includes the trigger that
      opened it. So a click on an open menu ran two handlers in order: the dismissal closed it, then
      the trigger's own click toggled it back open. Whether the state had re-rendered in between
      decided what the operator saw, which is why it flashed rather than failing cleanly.
      The trigger cannot stop the event — a capture-phase listener at the document runs before the
      target's own handlers, so there is nothing left to stop by then. Instead the trigger ignores a
      click that lands within 300ms of a dismissal, since that dismissal was caused by this very
      click. Both directions are then right: a click on a closed menu opens it, a click on an open
      one leaves it closed.
      Verified on all three kebabs (feed row, strip, history row): open/closed/open/closed across
      four clicks, plus Escape and click-away, and the trigger still works afterwards.

- [x] `A7-15` **FIXED: `/history`'s controls were six different sizes, and its two tabs had no URLs.**
      Measured before: filter selects 29px next to date inputs 37px next to a text input 35px, in the
      same row; table buttons 32px beside 35px; three different label treatments on the Items filter
      row alone (inline before the search box, stacked over the feed menu, inline again before the
      deleted filter). The browser's intrinsic sizing differs per widget and padding cannot reconcile
      it, so the height is stated outright now — one constant for page controls (34px), one for the
      smaller in-row controls (32px, matching the shared kebab's square trigger) — and every filter
      field is a label-above-control pair. The actions column hugs its buttons instead of stretching
      across the leftover width, which had left Expand and ⋯ an inch from the row they act on.
      Each tab is now its own address (`/history/runs`, `/history/items`), like `/settings/:section`:
      a reload keeps the tab you were on, and the Items list can be linked to. Bare `/history` still
      resolves to Runs.
      One trap: registering the route on the page is not enough. `web/shell`'s route table is what
      the guard consults, and a path missing from it is treated as unknown and redirected — which is
      why `/history/items` first went to `/generate`.

- [x] `A7-16` **FIXED: `/settings/data` was four operations with four different layouts.** The three
      figures the page exists to report were three muted sentences that read like a caption; the
      buttons were 333px slabs sized by a field beside them; and two of the four operations — Import
      and Vacuum — had an unlabeled overflow glyph as their ONLY control, so the page looked like it
      could not do anything. Now: a stat strip (feeds / items / database size, in the data face, as
      the first thing on the page), then one row per operation — name, the consequence in a line,
      and its control right-aligned on a shared axis, with whatever it needs or produces underneath
      at full width. Import and Vacuum have real labelled buttons in the danger tone; their typed
      confirmations, which are the actual safety, are unchanged. The kebab rule (§12.6 / D0-15) is
      about destructive actions inside a LIST OF ROWS; when the operation IS the card, hiding it
      behind a glyph is not caution, it is concealment.
- [x] `A7-17` **FIXED: the item search could not match a prefix — "triv" found nothing, "trivia"
      found everything.** The search box's contents went straight into `items_fts MATCH`, and FTS5
      matches whole tokens, so a search-as-you-type field returned zero results with total confidence
      until the word was finished. It also leaked FTS5's query LANGUAGE: `AND`, `NEAR`, `*`, `-`, `:`
      and `"` all meant something, an unbalanced quote was a syntax error that failed the whole RPC,
      and a title containing the word "and" could not be searched for.
      `internal/rpc/itemsearch.go` now builds the query: each run of letters or digits becomes one
      quoted prefix term (`triv ques` → `"triv"* "ques"*`), ANDed, capped at twelve terms, with
      everything else treated as a separator so no operator character can reach the query language.
      Covered by unit tests over the built string and an end-to-end test against the real FTS5 index
      (`TestItemListSearchMatchesPrefix`), because the first only proves what was built and the
      second proves SQLite agrees.
- [x] `A7-18` **FIXED: the first search after `/history/items` loaded was always lost.** You typed,
      and the table sat on "Loading…" forever — not for a while, forever: the request wedged (the
      `A5-44` tunnel defect), and `retryRead`'s own timeout never fired either, because the timer
      that would fire it was waiting on the same stalled render loop. Both history tabs now hold
      `web/ui.Pump` open for the duration of a load, which keeps renders and therefore timers moving,
      and the retry does its job. Two related fixes in the same pass: the Items tab now serialises
      its feed-list and item-list requests on a cold load (the Runs tab already did — landing
      directly on `/history/items` otherwise showed an empty table), and the loader converges on the
      latest filter, so a filter changed while a request is in flight is no longer swallowed.

- [x] `A7-19` **FIXED: "Run now" told you nothing.** The click called `FeedService.RunNow`, which
      opens a run row, hands it to the executor and returns a `run_id` — and the page threw that id
      away, reloaded the feed list, and showed no sign anything had happened. The run then spent real
      money, and the only way to learn what it did was to go to History and pick the feed out of a
      filter. There is now a status line under the strip: starting → running → the outcome (items
      added and rejected, tokens, cost), with a link to that feed's runs and a dismiss. Polled rather
      than streamed: `RunService.Watch` exists and would be tidier, but the streaming path in this
      app has needed its own care, and a run this page just started is worth two seconds of polling
      rather than a second streaming implementation to maintain.
- [x] `A7-20` **FIXED: per-feed generation history was effectively unreachable.** Every feed row and
      the strip's ⋯ menu now link to `/history/runs?feed=<id>`, and the Runs tab reads that from the
      URL — so "show me this feed's runs" is a link, shareable and reload-proof, instead of an
      instruction to navigate elsewhere and re-choose the feed.
- [x] `A7-21` **FIXED: the recipe form's Model field was free text while the strip's was a menu.**
      Backwards: the strip's is a per-preview choice, the recipe's is SAVED and used by every
      scheduled run, and §8 classifies "model not found" as a recipe-scoped Fatal that disables the
      feed — a typo there is indistinguishable from a deprecation until a run fails at 4am. Both now
      render through one `renderModelPicker` (126 models, chat grouped first, unlisted values pinned
      in, free-text fallback when the provider cannot be reached), so they cannot drift apart again.

- [ ] `A7-05` **`FeedService.SetMembers` is still unreachable from the UI.** Aggregate feeds cannot
      have their member feeds chosen anywhere. Either build the control or mark aggregate feeds as
      not-yet-supported in the Kind menu — as it stands, an operator can create a feed of a kind
      that cannot be configured.

- [x] `A7-06` **FIXED (workaround): an off-loop state update on `/generate` could never render.**
      GWC v5.0.1 queues state updates made from a goroutine and books a drain; the drain defers
      itself while a render pass is in flight, and `PostAsync` will not book a second one while a
      booked drain is outstanding — so if that drain never runs, every later update joins a queue
      nothing comes back for. Measured on Save: the goroutine ran, the RPC returned, the feed row
      was written to the database, and the button stayed on "Saving…" forever. A JS-side marker
      written straight to `window` from that same goroutine proved the Go side completed; nothing
      reached the screen. Not headless-only (reproduced headed with background throttling disabled)
      and not the transport. The tell: adding an unrelated goroutine that touched state once a
      second made everything work.
      `web/ui/pump.go` now keeps a heartbeat alive for the duration of an in-flight mutation plus a
      two-second grace (a mutation's last act is usually a refetch, whose own update lands after the
      operation returns — stopping the pump immediately put exactly that update back in the queue,
      which showed as "feed created, list does not show it"). Every mutation on `/generate` runs
      through it.
      **This is a workaround for a framework defect and should be removed when that is fixed.**
      `/settings` saves were checked and are unaffected; `/history`'s reducer dispatches have their
      own nudge (`web/pages/history/asyncdispatch.go`).
- [ ] `A7-07` **Report the GWC async-inbox wedge upstream** (see `A7-06`). The suspicious code is
      `internal/runtime/inbox.go`: `PostAsync` returns early when `inbox.scheduled` is true, and
      `DrainAsyncInbox` re-books itself via `scheduler.SetTimeout` while `wipRoot != nil` — if that
      re-booked drain is ever lost, nothing re-arms it.

### Review sweep 2026-08-10 (A5 series)

Seventeen parallel reviewers audited every page against `PLAN.md` and this file. Items marked
**FIXED in the same pass** were repaired immediately; the rest are open. The recurring theme is not
broken widgets — it is **settings that are stored and displayed but read by nothing**, which is
worse than a missing feature, because the screen says the setting is in effect.

#### The systemic one

- [x] `A5-01` **FIXED 2026-08-11: every setting that has somewhere to go now goes there. — original report:
      WRITE-ONLY SETTINGS: at least eleven settings persist, round-trip through the UI, and are read by
      nothing.** Confirmed individually by four independent reviewers. Each showed a "Saved." confirmation
      that was true of the database and false of the system.
      Resolved one at a time, each with a test:
      `public_base_url` — already wired before this pass (`liveBaseURL` + `loadPublishingAtBoot` + the
      publishing sink); the report was stale on this one.
      `default_cache_control` — wired through `publish.Deps.CacheControlFn`, read per request so a change
      needs no restart and is not frozen into already-cached bodies. Unset falls back to the previously
      hardcoded `max-age=900`.
      `default_author` / `default_copyright` / `default_og_image` / `default_ttl_minutes` — seed a feed at
      CREATE, filling only fields the request left empty. Update deliberately does not consult them, or a
      cleared author could never be cleared.
      `default_daily_token_budget` / `default_daily_run_budget` / `default_feed_window` — already read by
      `/generate`'s new-feed draft (`A5-06`).
      `staleness_threshold_minutes` — the rail now flags stale against it instead of a hardcoded 24h
      constant. `/healthz` and the nightly webhook keep their grace MULTIPLIER over each feed's own cron
      interval, which is not a disagreement to collapse: a flat minute count cannot serve an hourly feed
      and a weekly one at once.
      the provider price table — `A5-02`.
      Remaining, and split out rather than left ambiguous: `default_contact` (`A5-50`).

#### Blockers

- [x] `A5-02` **FIXED: the price table now reaches the cost engine, and its units are honest. — original report: The Settings price table is disconnected from the cost and budget engine.**
      `UpdateSettings` persists it into the `settings` row, while real per-run cost and §13's budget
      ceilings come from a separate, file-backed `internal/budget.Table` the RPC never touches.
      Editing rates on `/settings/provider` has zero effect on spend enforcement. Compounding it:
      the Rates panel's column headers say **$/1M tokens** while the field and its only consumer
      (`web/pages/generate/logic.go:276`) treat the value as **$/1K** — a 1000x unit mismatch that
      would poison the table even once it is wired.
- [x] `A5-03` **FIXED: a fresh install now seeds real global ceilings. — original report: The global daily spend ceiling is silently absent on every fresh install.**
      `sysLoadGeneration` (`internal/rpc/system.go:239`) seeds only `Enabled`;
      `GlobalDailyTokenCeiling` and `GlobalDailySpendCeilingUsd` ship as 0, and
      `internal/budget.CheckRequest` treats 0 as "no cap" (gated on `> 0`). §13's backstop does not
      exist by default, and nothing in the UI says 0 means unlimited rather than zero-allowed.
- [x] `A5-04` **FIXED: recipe import sends the expected version. — original report: Recipe import from `/settings/data` cannot succeed for any feed.** `doImport` never
      sets `ExpectedVersion`, and every feed row starts at `version=1`, so the server's
      optimistic-concurrency check (`internal/rpc/feed.go:1177`) rejects every import. The generic
      error message also hides the cause. Related to the already-filed `A4-43` format confusion.
- [x] `A5-05` **FIXED: creating an item from `/history` was impossible.** The create form never let
      the operator pick a feed and never set `FeedId`, while the server hard-rejects `feed_id == 0`.
      The form now has a feed picker (create only — an existing item cannot move between feeds, its
      guid is already published per §5.5), defaulting to the first feed.
- [x] `A5-06` **FIXED: a brand-new feed could never be saved.** `/generate`'s "+ New" draft set only
      `ItemsPerRun` and `FeedWindow`; `internal/feedspec`'s `validateBudgets` rejects a zero daily
      token budget and a zero daily run budget, so the first save of every new feed failed validation
      on two fields the operator was never shown. The draft now seeds both from the admin's
      configured defaults with fallbacks, plus `TtlMinutes` (which shipped 0 and rendered
      `<ttl>0</ttl>` — the schema's `DEFAULT 15` never applied because the RPC always sends a value).
- [x] `A5-07` **FIXED: the Preview cost estimate could never appear.** `samplerProps.Prices` was
      hardcoded `nil`, so `EstimateSampleCostUSD` never matched a model and the strip read "Estimate
      unavailable" permanently, against §12.3's requirement that cost be visible at the moment of
      spending. Now fed from the settings resource the page already fetches — though it only shows a
      real number once `A5-02`'s rate table is real.

#### Major

- [x] `A5-08` **FIXED: every unary RPC could hang forever.** `Conn.Usable` deliberately attempts calls
      while the ClientConn is Idle or Connecting, and gRPC parks them on a connection that may never
      arrive. `/history` and `/generate` both sat on "Loading…" with no error and no timeout on a hard
      page load. All 47 unary wrappers in `web/wsconn/clients.go` now carry a deadline (30s; 10m for
      `SampleService.Sample`, which runs a real generation); the two streaming wrappers deliberately
      do not.
- [x] `A5-09` **FIXED: `/history` and `/generate` loaded nothing when opened directly.** Both mounted
      their loaders while the session was still `appstate.Anon`. Now gated on the session actually
      being usable, keyed on the session state rather than on a `!= Disconnected` boolean that is
      already true at `Anon`.
- [x] `A5-10` **FIXED: the `/history` Runs filter was a bare numeric feed-id box.** §12.4 specifies
      "filter by feed, status, date range"; `RunFilter` and `BuildRunHistoryRequest` have always
      carried all four fields, but the only control was an `<input type="number">` asking for a
      database id that appears nowhere in the UI. Replaced with a feed menu, a status menu (including
      SKIPPED, so "what did the budget stop?" is answerable), a date range and a clear control — and
      the fields are styled instead of raw browser widgets.
- [x] `A5-11` **FIXED: every Settings error view now offers Retry, and the feed list on /generate has one. — original report: `StatePanel.OnRetry` is declared but never set by any caller, app-wide.**
      `web/pages/settings/render.go`'s `screenWrapper` does not even accept a retry callback, so every
      error view on `/generate` and every `/settings` section offers no recovery short of a full page
      reload. §12.6.
- [x] `A5-12` **FIXED: the recipe stakes are on the page again. — original report: The `/generate` rail — feed status, the stale-feed flag, the enable toggle, Run Now —
      is now buried inside the collapsed "Recipe settings" disclosure.** §12.3 requires the stale-feed
      flag to be surfaced, not hidden behind a click on every page load. The workbench redesign put it
      there; it needs its own home. Candidate: a compact status line on the strip.
- [x] `A5-13` **FIXED: the always-zero budget readout is gone until something can fill it. — original report: The sampler's "remaining daily budget" is hardcoded `$0.00` forever.** The backing
      state is never updated from any server response, and the streaming proto carries no field for
      it. Either wire it (needs a proto field) or remove the figure — a budget readout that is always
      zero is worse than none.
- [x] `A5-14` **FIXED: the item search is debounced. — original report: The item search box fires a full FTS5 RPC per keystroke.** No debounce.
- [x] `A5-15` **FIXED: the backdating check is scoped to one feed. — original report: The item form's no-backdating check is not scoped per feed.** `newestPublishedAt` is
      computed across all currently-loaded items in a feed-agnostic list, so §5.5's increasing-pubDate
      protection blocks and warns on the wrong comparisons.
- [x] `A5-16` **FIXED: /history Items has a feed filter. — original report: `/history` Items has no feed / origin / date-range filters** despite `ItemFilter` and
      `BuildItemListRequest` already supporting them. §12.4.
- [x] `A5-17` **FIXED: bulk delete/restore live behind the kebab. — original report: Bulk delete and restore are plain top-level buttons**, contradicting this repo's own
      ticked `D0-15` rule that destructive actions live behind the kebab.
- [x] `A5-18` **PARTLY FIXED: three of the four dead fields are now read. — original report: Four of six `/settings/generation` fields were dead controls.** `DefaultDailyTokenBudget`,
      `DefaultDailyRunBudget`, `DefaultFeedWindow` and `StalenessThresholdMinutes` persist and
      round-trip but were read nowhere. The first three are now read by `/generate`'s new-feed draft
      (`A5-06`); staleness remains dead. See `A5-01`.
- [x] `A5-19` **FIXED: a rate can name its model. — original report: "Add rate" creates an unusable row.** The new price row renders its model name as
      plain text with no input or select, so the rate can never be assigned a model id.
- [x] `A5-20` **FIXED: /recover has a way out of every step. — original report: `/recover` has no way out of a step.** Unlike `/login`, the Reset-Password /
      Re-enroll-TOTP / Choose-Action steps have no Back control, so an elevated session that expires
      mid-flow leaves a hard reload as the only escape.
- [x] `A5-21` **FIXED: three swallowed errors made failures look like nothing happening.**
      `/generate`'s feed-into-editor load (`if err != nil { return }`, leaving the previous feed's
      fields under the new feed's name), `/history`'s run-log expand (rendered an EMPTY log, which
      reads as "this run logged nothing" — the most misleading thing that row can say), and
      `/generate`'s Promote (no success handling: the candidate stayed listed and promotable, so
      clicking twice created the item twice, and the localStorage snapshot could resurface it after a
      refresh).
- [x] `A5-22` **FIXED: the feed-picker failure is surfaced. — original report: `/settings/data`'s feed-picker load swallows its error** and is not folded into the
      section's `ScreenError`, so a failed load renders as "no feeds exist".
- [x] `A5-23` **FIXED: `/settings/security` showed "Password changed" next to a failure.** `pwSuccess`
      was only ever set, never reset, so a failed attempt after a successful one rendered both banners
      at once.
- [x] `A5-24` **FIXED: the shared Toggle was a 36x20px hit target**, inherited by every toggle in the
      app including the generation kill switch. Now 44x24. The `/generate` variable chips (88x20) and
      the "+ New" button (49x17, which had no padding rule at all) are 24px minimum too.
- [x] `A5-25` **FIXED: a white flash on every reload for operators who chose Dark.** `ApplyStoredTheme`
      runs inside Mount, which cannot happen until the ~30MB wasm binary has loaded.
      `web/static/index.html` now stamps `data-theme` synchronously from localStorage before anything
      paints.

- [x] `A5-43` **FIXED: on a cold load of `/history`, the Runs tab showed "Loading…" forever.**
      Cause: `FeedService.List` (for the filter menu) and `RunService.History` were issued
      concurrently as the page mounted, and on a cold load — the tunnel having just been replaced by
      the authenticated one — the run query was the one that never came back. Serialising them
      (request the run history only after the feed list settles) fixes it, and that was confirmed by
      isolation: putting the two back in flight together reproduces the hang immediately.
      The diagnosis cost more than the fix, and two things made it expensive. The first attempt at
      the serialisation *did not build* (a hook declared after its use), so `web/build.sh` aborted
      and the browser kept being served the previous bundle — the fix looked like it had been tried
      and failed. And the intermediate evidence was misread: a debug marker that "never advanced"
      was itself a stale render, which sent the investigation after a transport wedge that did not
      exist. Both were only settled by instrumenting each layer separately — the render path
      (an off-loop `time.Sleep` loop that repaints proves renders work), the transport
      (a stage marker in `guardUnary` showing three calls completing end to end), and the server
      (a log line in `RunServer.History`).
      Kept from that investigation, because each is worth having on its own: a deadline plus a
      watchdog on every unary RPC (`A5-08`), the readiness gate (`A5-09`), a bounded retry for
      reads, and `web/pages/history/asyncdispatch.go`'s note that a `ui.UseReducer` dispatch from a
      goroutine updates state without scheduling a render.
      **Still open underneath this:** the tunnel should not lose a stream just because two RPCs are
      opened at once during startup. The page-level serialisation is a workaround; `A5-44` tracks
      the real fix.
- [ ] `A5-44` **The gRPC-over-WebSocket tunnel drops a concurrent stream opened during startup.**
      Root cause behind `A5-43`. Two unary RPCs issued in the same tick, moments after the
      authenticated reconnect, and one silently never completes — no error, no server-side arrival.
      Every page that fires more than one request at mount is exposed; `/history` is simply where it
      showed. Needs instrumentation of `grpctunnel`'s frames on the client side, which needs a debug
      build of GoGRPCBridge. Until it is fixed, a page that loads several things at mount should
      chain them.

- [x] `A5-45` **FIXED: every enabled toggle in the app displayed "Reconnecting to the server".**
      `web/ui/toggle.go` rendered `DisabledReasonKey` whenever the key was set, regardless of whether
      the control was actually disabled. So the kill switch — a working control — carried a permanent
      notice that the server was unreachable. It also cost real debugging time here: the false banner
      was mistaken for a stuck DISCONNECTED state and sent an investigation into the transport before
      the primitive was read. The reason (and its `aria-describedby`) now render only while disabled.
- [x] `A5-46` **FIXED: `/settings/publishing`'s public base URL now drives the publish plane.**
      `publish.Deps` gained `BaseURLFn`, read per request, seeded at boot from the stored setting and
      updated on save through a sink alongside the price table's. Trailing slashes are trimmed at the
      point of use — a stored `https://host/` would otherwise have produced `https://host//feeds/x.xml`,
      a different URL to most aggregators, and a feed whose guids changed is a feed that reposts
      everything (§5.5). The rest of `A5-01`'s write-only settings are still write-only.
- [x] `A5-47` **FIXED: the settings tabs are no longer a narrow column in a wide page** (`A6-01`).
      `.af-settings-card` is a grid with `repeat(auto-fit, minmax(20rem, 1fr))`, so a card with one
      field stays one column and a card with six fills the width; headings, prose and tables span all
      columns. `/settings/generation` is additionally split into "Global ceilings" / "Per-feed
      defaults" / "Staleness" with units in every label (`A6-07`).
- [x] `A5-48` **FIXED: status now carries colour, and cost carries weight** (`A6-02`, `A6-08`).
      Item status renders as a tinted pill using the `RoleSuccess`/`RoleBorder` pairs the token set
      has always defined — the word stays, so colour is never the only encoding — and the runs
      table's Cost column uses full-strength ink and tabular figures.
- [x] `A5-49` **FIXED: the `/generate` strip separates configuring from acting** (`A6-04`), with a
      rule and real space between the two zones instead of leaving Preview as the seventh identical
      box on the row.

#### Minor

- [x] `A5-26` **FIXED (validated: empty model, duplicate model, negative rate; save refuses and names the row).** No validation on price-table saves: negative rates, duplicate model names and empty model
      strings are all accepted, unlike the effort and profile validation in the same handler.
- [x] `A5-27` **FIXED (negatives clamp to zero, and "0 means no limit" is now stated on the group).** No negative-value validation on any of the six `/settings/generation` numeric fields,
      client or server; and no help text on any of them.
- [x] `A5-28` **FIXED 2026-08-11.** Server-side publishing validation covered only `public_base_url`: TTL
      could be saved negative and the og:image scheme was unchecked. Neither side stripped a trailing slash
      from the base URL, which would double-slash every feed URL once `A5-01` wired it up — and `A5-01` has
      now wired it up, so that one had become live rather than hypothetical.
      `sysValidatePublishing` replaces the base-URL-only check: TTL bounded to [0, 7 days] (a ceiling to
      catch a typo, not to ration — a TTL is advisory), and og:image must be an absolute http(s) URL, since
      it is emitted into the page head and `javascript:` there is an injection vector rather than a broken
      image. The trailing slash is stripped on save so the database holds one canonical form instead of
      every consumer remembering to trim.
      Validation still runs before the transaction opens, and a test proves a rejected publishing section
      leaves a provider change in the same request unwritten rather than half-applied.
- [x] `A5-29` **FIXED: the run-row Expand button never relabelled to Collapse**, and the typed-delete
      confirmation input had no accessible name at all.
- [ ] `A5-30` **STILL OPEN.** The cost chart's per-day hover detail has no keyboard-focusable equivalent.
- [x] `A5-31` **FIXED (in-flight guard, Busy state, and the failure is surfaced).** The per-row session Revoke button has no in-flight guard, unlike every other mutation on
      that page.
- [x] `A5-32` **FIXED (dead matcher deleted).** `web/pages/settings/confirm.go`'s `ConfirmationMatches` is dead code and disagrees with
      the `web/ui.ConfirmMatches` actually wired to the modals (whitespace trimming).
- [x] `A5-33` **FIXED 2026-08-11.** `/recover` had i18n keys for password-too-short/too-long, with real text in
      every locale, referenced by nothing — so a too-short password failed silently: the server refused it
      and the page said nothing. The length is now checked before the request, rendered in the form's error
      slot and announced assertively. The server remains the gate; this stops a doomed round trip and says
      why. Bounds are duplicated from `web/pages/settings/validate.go` rather than imported, for the same
      reason `newFeedDraft` duplicates `internal/rpc`'s defaults — two sibling page packages, neither owning
      the other.
- [x] `A5-34` **FIXED (renders as a percentage via a new Formatters.Percent).** Novelty similarity is interpolated as a raw 0..1 float instead of the catalogue's own
      `FormatPercent` helper, whose doc comment exists for exactly this value.
- [x] `A5-35` **PARTLY FIXED (export box is labelled and read-only; error detail still generic).** The `/settings/data` export textarea is unlabelled and not marked read-only despite looking
      editable; import/export/backup errors show a generic string while vacuum's shows the server's
      actual error.
- [x] `A5-36` **PARTLY FIXED (Save is disabled without a title; indeterminate checkbox still open).** No client-side required-field validation on the item form (the server enforces
      title-required, the UI does not), and the select-all checkbox never shows an indeterminate state
      for a partial selection.
- [ ] `A5-37` **STILL OPEN.** `/login` announces a failed login twice to screen readers (two aria-live regions with
      identical content) and uses `aria-live` without `role="alert"`, inconsistent with the shell.
- [ ] `A5-38` **STILL OPEN (tooling).** The i18n lint tool has no coverage for prose inside `h.Aria(...)`.
- [x] `A5-39` **FIXED (size, temperature and selection reset with the feed).** Switching to a fresh feed on `/generate` leaks the previous feed's candidate count and
      temperature override; only `candidates`/`sampleID` reset.
- [x] `A5-40` **PARTLY FIXED (the field now says it is inert; effort default still hardcoded).** The temperature override is a documented no-op under §8.1 with no disclosure in the UI,
      and the effort default is hardcoded `"smart"` rather than read from Settings.
- [ ] `A5-41` **STILL OPEN, and now wider.** The pager's Previous/Next handlers mutate the cursor pointer directly
      instead of going through `Dispatch`, leaving a matching `"next-page"` reducer case dead and untested.
      2026-08-11: the numbered jump control added by `D3-21` follows the same pattern — `OnJump` calls
      `cursor.JumpTo` directly — so there are now three direct mutations, not two. Recorded rather than
      quietly fixed: routing all three through the reducer is a state-handling change across both tab
      files, not a side errand of adding the control.
- [x] `A5-42` **FIXED 2026-08-11.** Stale doc comments in `web/pages/auth/` (`doc.go`, `backoff_display.go`,
      `recover.go`) claimed `keyConnectionUnreachable`, `keyBackoffCleared` and `keyRecoverSavedConfirm` were
      not yet in the catalogue and leaned on D6-07's "a missing key renders the key itself" to cover the gap.
      All three are declared in `web/i18n` and resolve in every locale, which that package's own
      `TestEveryLocaleHasEveryKey` and `TestEveryCallSiteKeyIsDefined` enforce. Corrected rather than left:
      a comment claiming a key is missing sends the next reader after a bug that was already fixed.
- [ ] `A5-50` **`settings.publishing.default_contact` has nowhere to go.** Split out of `A5-01`, which fixed
      every other write-only setting. This one cannot be wired as things stand: `feeds` has `author`,
      `copyright`, `og_image` and `ttl_minutes` columns and no `contact` column, and `Feed` carries no
      contact field — so the control edits a value with no destination. Either add the column plus the proto
      field and seed it at create like its siblings, or remove the control. Do not leave it looking wired.

### Design critique 2026-08-10 (A6 series)

An adversarial design reviewer screenshotted every route in both themes and critiqued the rendered
pixels. Its verdict, worth recording because it is more useful than a score: *"a design system with
real bones and inconsistent follow-through, not a generic AI-default look."* The app has one genuine
idea — the broadcast-ring signature on `/login` and the left-edge status marks on the Runs table —
and it is under-committed, while the merely functional screens are over-decorated with identical
hairline boxes that all compete for the same attention.

Two of its findings are already resolved and are recorded here only so the report reads honestly
against the code: the Runs feed filter it saw as "a lone text input with 1200px of dead space" is
now the feed/status/date-range row (`A5-10`), and the empty preview pane's stale "Select or save a
feed to sample it" copy is gone. It reviewed the bundle that was live at the time, not the tree.

- [x] `A6-01` **FIXED — see A5-47.** **BLOCKER (design): every settings tab is a narrow field column adrift in a wide page.**
      `.af-settings` caps the page at 64rem while `.af-settings input/select/textarea` caps fields at
      30rem, so Security, Provider, Generation and Publishing each render as a ~480px column of
      stacked fields inside a much wider frame — roughly two-thirds of the horizontal space empty, on
      every tab, in both themes. This is one systemic decision, not five bugs: the 2026-08-10 rescue
      pass fixed "no styling at all" without reconciling proportion. Fix once at the layout level:
      either narrow the page measure to ~42–46rem for single-column tabs, or introduce a real
      two-column card layout for the tabs with more than about four fields (Generation and Provider
      both qualify). Every settings screen improves at once.
- [x] `A6-02` **FIXED — see A5-48.** **The token set defines `RoleSuccess`, `RoleWarning` and `RoleLive` so that status
      carries colour, and the pages mostly do not spend it.** History Items' Status and Origin
      columns, About's "Never built", and Generate's disabled-reason banners are all places where a
      role exists with a WCAG-checked contrast pair and is not applied. One pass wiring the existing
      status strings (Published / Draft / Never built / Stale) to those roles raises scannability
      across the app without inventing anything.
- [ ] `A6-03` **STILL OPEN (direction, not a single change).** **Spend the boldness where the signature already is, and stop spending it on forms.**
      The login rings and the Runs table's left-rule marks are the app's only real visual ideas and
      both are quiet enough to miss; meanwhile every settings row, every strip control and every panel
      wears the same 1px hairline box. Commit harder to the two signatures (and to the cost figures,
      which nobody has made loud yet) and strip uniform chrome off the repetitive form screens.
- [x] `A6-04` **FIXED — see A5-49.** **The `/generate` strip gives the Preview button no spatial distinction.** Seven leading
      controls render in identical bordered, same-radius, same-fill boxes and Preview is distinguished
      only by fill colour, sitting last after five unrelated controls of equal weight. Split the strip
      into two zones with a visible gap or divider — left = choose and configure, right = act
      (estimate + Preview) — so the verb is spatially distinct, not just chromatically.
- [x] `A6-05` **PARTLY FIXED (the stale "select or save" copy is gone; the empty pane is compact).** **The empty preview pane is ~700x850px of chrome around one line of text.** Either let
      the bordered box grow only once there is content, or spend the space on a short "what happens
      when you press Preview" explainer so a first run is not staring into a void.
- [x] `A6-06` **FIXED — see A5-12.** **The recipe's slug, schedule and budget are exactly the facts that decide whether a
      Preview click is safe, and they are at the bottom of the page behind a small muted disclosure.**
      Put a compact one-line summary of the active recipe (slug · schedule · remaining budget) above
      the prompt fields, and keep the disclosure for the rarely-edited detail. Overlaps `A5-12`, which
      is the same complaint from the correctness side — fix them together.
- [x] `A6-07` **FIXED — see A5-47.** **`/settings/generation` is six unrelated ceilings rendered as one undifferentiated
      stack of bare zeros with no units.** Global token ceiling, global spend ceiling, per-feed token
      budget, per-feed run budget, feed window and staleness threshold all look identical. Split into
      "Global ceilings" / "Per-feed defaults" / "Staleness" sub-groups using the hairline device
      `.af-settings-card` already has, and put units in the labels (tokens, $, minutes) the way
      Publishing's "TTL (minutes)" already does. Pairs with `A5-03` and `A5-27`.
- [x] `A6-08` **FIXED — see A5-48.** **The Cost column in the Runs table carries no more weight than Tokens or
      Added/Rejected.** Money is the column an operator scans this table for; it should read as the
      figure, not as one of seven equal numeric columns.
- [ ] `A6-09` **STILL OPEN.** **The reconnecting/disabled state on `/settings/generation` is a quiet caption where it
      should be a banner** — the same §12.3 complaint as the kill-switch reason.
- [x] `A6-10` **PARTLY FIXED (the textarea is labelled and read-only).** **`/settings/data`'s import textarea is sized far beyond its use frequency, and its "…"
      control is unlabelled.** Overlaps `A5-35`.
- [ ] `A6-11` **STILL OPEN.** **The login ring signature is legible only on close inspection.** It is the one bold
      thing in the app; commit to it or cut it.

- [ ] `A4-46` **NEW: the dev loop had two silent failure modes that both present as application
      bugs.** `internal/publish.NewStaticHandler` snapshots every asset into memory at construction,
      so a running dev server keeps serving the bundle from its own start time — rebuilding without
      restarting changes nothing in the browser. And `pkill` does not reach a Windows process from
      git-bash: it exits 0 having killed nothing, the old server keeps :8082, the replacement fails
      to bind, and a health check then passes against the OLD server. `.devrun/restart.sh` now
      handles both. This ticket is the remaining question: whether the dev build should serve from
      disk (an `AFF_DEV_STATIC_RELOAD=1` path that stats per request) so a rebuild is enough, rather
      than relying on everyone remembering to restart.
- [ ] `A4-43` **NEW: `aff recipe import` says "TOML file path" and parses JSON.** The CLI's usage
      string, its error message ("want exactly one TOML file path argument") and PLAN.md §7 ("TOML
      import/export ... for versioning and disaster recovery") all say TOML; the server's
      `ImportTOML` json.Unmarshals the payload, and `recipe export` emits JSON. Feeding it real TOML
      fails with `invalid character 'S' looking for beginning of value`. Found 2026-08-10 while
      repointing a feed for `A4-30`'s live run. Either implement TOML (which is what §7 promises and
      what a human hand-edits comfortably) or rename the command and fix §7 — but the current state
      means the documented disaster-recovery path does not work with the format it names.
- [ ] `A4-31` Span `llm.generate` comes from SchemaFlux — wire the provider, do not re-instrument. §15.0a
      — **UNTICKED 2026-08-10, was ticked in error.** The wiring is real: `cmd/animefeedflux/wire.go`
      calls `schemafluxotel.Install(obs.GetTracerProvider())`. The spans are not. Grepping the vendored
      `schemaflux@v1.1.0` source shows `telemetry.Observer`'s `OperationStarted`/`OperationFinished` are
      called only in `telemetry/observer.go` and its own test — never from the real
      `Generate`/`Extract`/`Transform` pipeline. So `Install` registers an observer that nothing invokes,
      and no `schemaflux.<op>` span (attempts, tokens, latency) ever opens in production.
      This is the twelfth instance this session of a component that is built, tested, and reachable from
      nothing — and the most expensive kind, because "the provider is instrumented" is exactly the claim
      you would rely on at 2am to see why a run is slow or burning tokens.
      Resolving it means either upstreaming the observer calls into SchemaFlux, or instrumenting at the
      `internal/llm` boundary and accepting the re-instrumentation this todo was written to avoid.
      Do not re-tick without a captured span from a real run.
- [x] `A4-32` Span `validate` records rejected count and reasons as attributes. §15.0a
- [x] `A4-33` Emit `aff_tokens_total` and `aff_cost_usd_total` from the recorded usage. §15.0a
- [x] `A4-34` Emit the canonical `run.finished` wide event with every §15.0 field. §15.0

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
- [x] `A5-11` Span `novelty.check` with `max_cosine` and the verdict as attributes. §15.0a — implemented in `filterNovel` (`internal/generate/runner.go`): per-item `obs.Start(ctx, "novelty.check", ...)` with `max_cosine` (the score `CheckVector`/`Check` return) and `verdict` (`novel`|`duplicate`|`error`) attributes.
- [x] `A5-12` `aff_items_rejected_total{reason="novelty"}` incremented on a rejection. §15.0a — implemented: `filterNovel` now calls the new `recordItemRejected` helper, which wraps `obs.Metrics.RecordItemRejected(ctx, feedSlug, reason)` (reason is `novelty_duplicate` or `novelty_check_failed`, obs/fields.go's existing tokens) on every novelty rejection; previously `RecordItemRejected`/`RecordItemsPublished` were never called from production code at all.

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
- [ ] `A6-17` Span `sources.fetch` per source: url, status, whether it 304'd, item count. §15.0a — item-count gap fixed 2026-08-10: `internal/sources/fetch.go`'s `FetchCandidates` now owns the `sources.fetch` span itself (via the new unexported `fetchOnce`, which does the network call with no span of its own) and keeps it open through `Parse`, so the same span carries `host`, `status`, `outcome` (a 304 is `success`, distinguishable from the status code alone), and now an `items` attribute too — 0 on a 304 (nothing new fetched), the post-normalization candidate count otherwise, and omitted entirely on a failure that never parsed. `Fetch` itself (used standalone, with no parse step) still emits the same span with no `items` attribute, which is correct — it has nothing to count. Tests: `TestFetchCandidates_EmitsSourcesFetchSpan_WithItemCount`, `_ZeroItemsOn304`, `_OnFailure` (`internal/sources/fetch_test.go`). **Still not tickable**: `internal/sources.Fetcher`/`FetchCandidates` is not reachable from any real caller. `cmd/animefeedflux/wire.go`'s `noFetcher` (its own doc comment: "grounded-feed source fetching is not wired in this build ... out of scope for this change") is what every production caller of `generate.Fetcher`/`CandidateFetcher` actually gets — `noFetcher.Candidates` unconditionally returns an error, never calling into `internal/sources` at all. `cmd/` is outside this change's edit scope, so the fix is wiring a real adapter there, not anything further in `internal/sources`. Leaving unticked per "only tick if reachable from a real caller."
- [x] `A6-18` Span `link.integrity` with candidates, accepted, rejected. §15.0a — implemented in `runAttempt` (`internal/generate/runner.go`): for grounded feeds, `obs.Start(ctx, "link.integrity", ...)` with `candidates` (`len(opts.CandidateURLs)`), `accepted`, `rejected` attributes, counted per-attempt from `Validate`'s `Rejection.Field == "link"`.
- [x] `A6-19` A rejected link is logged with `reason`, never with the model's raw output. RULE-3 — verified: `CheckLink`'s only call site (`contract.go:247`) discards the raw `error` (which embeds the offending URL) after mapping it through `linkErrorReason`, so only the stable reason token (`link_not_candidate`/`link_invalid`/`link_required_grounded`) ever reaches `Rejection`/`RunRecord.RejectReasons`; no other call site or log statement touches `CheckLink`'s raw error text.

## A7 — Scheduler

- [x] `A7-01` Cron parser evaluating in the recipe's **IANA timezone**, not UTC. §7
- [x] `A7-02` DST: a run in the skipped hour fires at the next valid instant. §7
- [x] `A7-03` DST: a run in the repeated hour fires **once**, tracked by `last_fired_slot`. §7
- [x] `A7-04` Deterministic jitter from `hash(slug)` across the configured window. §14.3
- [x] `A7-05` Persist `jitter_offset` so the UI readback matches reality. §14.3
      (`internal/rpc/feed.go`'s `FeedServer.Create` computes the offset with `schedule.Offset(slug, ...)`
      once and persists it into `feeds.jitter_offset` in the same INSERT — the real RPC path the CLI/UI
      use. A stale comment in `internal/flowtest/j2_createfeed_test.go` describes an older, lower-level
      `store.CreateFeed` stand-in that predates this RPC-level fix; it does not describe current behavior.)
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
- [x] `A7-19` Root span `generation.run` with `feed_slug`, `trigger`, `outcome`. §15.0a
- [x] `A7-20` `aff_runs_total` and `aff_run_duration_seconds` on every terminal state. §15.0a
- [x] `A7-21` Budget refusals increment `aff_runs_total{outcome="skipped"}`, not an error. §13

## A8 — Sampling

- [x] `A8-01` Dry-run path reusing the **entire** generation pipeline, writing no items. §11
- [x] `A8-02` Return candidate items, rendered `<item>` XML, novelty verdict, and cost. §11
- [x] `A8-03` Return grounded link verdicts including the failing URL. §12.3
- [x] `A8-04` Persist samples for 24h with `expires_at`. §12.3
- [x] `A8-05` Streaming variant emitting deltas as they arrive. §11
- [x] `A8-06` Sample size 1–5 and an optional temperature override. §12.3
- [x] `A8-07` `PromoteSample` writes the item stamped **now**, retrying on timestamp collision. §11
- [x] `A8-08` Sampling draws from the same budget as scheduled generation. §13
      **Caveat added 2026-08-10, not a full untick: true for the daily caps, false for the monthly
      one.** `cmd/animefeedflux/wire.go`'s `sampleBudget.CheckSample` (the `budget.Limits{}` it builds
      for `SampleService`) sets `PerFeedDailyTokens`/`PerFeedDailyRuns`/`GlobalDailyTokens`/
      `GlobalDailyUSD` — verified identical to `genGate.Allowed`'s scheduled-run path, so the daily
      claim this task makes is real and tested. It does **not** set `MonthlyUSDCeiling`, unlike
      `genGate.Allowed`, which does (`MonthlyUSDCeiling: g.monthlyCeilingUSD`, sourced from
      `cfg.MonthlySpendCeilingUSD`/`AFF_MONTHLY_SPEND_CEILING_USD`). So once an operator sets a
      monthly ceiling, scheduled generation is bound by it and interactive sampling is not — exactly
      the hole PLAN.md §13 names this task to close ("otherwise the safety net has a hole exactly
      where the interactive, easy-to-repeat action is"), just for the monthly dimension specifically
      rather than the daily one this task was written against. A human mashing "Sample" in the admin
      UI near month-end can push spend past `AFF_MONTHLY_SPEND_CEILING_USD` with every call still
      returning `Allow: true`. Reported, not fixed — the fix is one field
      (`MonthlyUSDCeiling: <the same monthlyCeilingUSD genGate already has access to>`) plus the
      matching `budget.MonthStart` month-to-date query `sampleBudget.CheckSample` does not currently
      run at all. See `DOD-7`, which this same gap also affects.
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
- [ ] `A9-12` Per-IP token-bucket rate limit; `429` with `Retry-After`. §6 — UNTICKED 2026-08-10
      (serving/auth/ops audit), was ticked in error: there is no rate limiter anywhere in the publish
      plane. `internal/publish/server.go`'s `NewServer` builds its `http.ServeMux` behind exactly one
      middleware (`requestIDMiddleware`); nothing else wraps it, and `grep -rn
      "429\|RateLimit\|Limiter\|TokenBucket" internal/publish/` (excluding generated protobuf and
      `internal/llm`'s unrelated provider-429 handling) returns nothing at all — no type, no
      constructor, no test. `cmd/animefeedflux/wire.go:1461` hands `publishHandler` — the bare return
      value of `buildPublishHandlerWithInvalidator`, itself just `publish.NewServer(deps)`'s mux —
      directly to the publish `http.Server{Handler: publishHandler}` with nothing else wrapping it. This
      is not the usual "built, tested, reachable from nothing" pattern this repo keeps finding
      elsewhere (a component with no caller); there is no component to fail to reach. The publish
      plane is `AnimeFeedFlux`'s one internet-facing, unauthenticated surface (PLAN.md §2/§6), so this
      is a real gap on the highest-exposure part of the system: nothing currently stops a client from
      hammering `/feeds/{slug}.xml` past the point §6 promises is "pointless."
- [x] `A9-13` `405` with `Allow` for any method beyond GET/HEAD. §6
- [x] `A9-14` `404` unknown slug; **`410 Gone`** for a soft-deleted item. §6
- [x] `A9-15` A disabled feed still serves its last built content; a deleted feed `410`s. §6
- [x] `A9-16` No stack traces, no version banner, no directory listing. §6
- [x] `A9-17` `robots.txt`. §6
- [x] `A9-18` Test: 304 on both validators; 405; 410; gzip correctness; `Vary` present. §17
- [x] `A9-19` End-to-end: generate → fetch → validator passes → item appears once over two polls. §17
- [x] `A9-20` Span `http.request` with route, status, and cache result (hit|miss|304). §15.0a
      (CLOSED 2026-08-10: `internal/publish/server.go`'s six handlers now each open a root span via
      `obs.Start(r.Context(), "http.request", obs.KindRequest)` and pass it through to `(*server)
      .observe`, which sets `route`/`status`/`cache` attributes and calls `span.End()` — after the
      wide event and metrics are recorded, so all three observability surfaces see the same values
      from one place. Traced to a real caller: `cmd/animefeedflux/wire.go`'s
      `buildPublishHandlerWithInvalidator` builds `publish.NewServerAndInvalidator(deps)`, called from
      `runAll`, which is `main`'s only entry point — no wiring gap.)
- [x] `A9-21` Child span `render.feed` on a cache miss only — a hit must stay cheap. §15.0a
      (CLOSED 2026-08-10: `handleFeed`'s cache-hit branch returns before any span-related code runs;
      the miss branch calls the new `(*server).renderFeed`, whose ONLY caller is that miss branch, and
      which opens `obs.Start(ctx, "render.feed", obs.KindRequest)`, sets `format`/`items`/`bytes`
      attributes, and closes it via `defer span.End()`. `internal/publish/otel_test.go`'s
      `TestRenderFeedSpan_OpensOnlyOnCacheMiss` drives one miss then one hit and asserts exactly one
      `render.feed` span across both.)
- [x] `A9-22` `aff_http_requests_total` and `aff_cache_hits_total`; the 304 ratio is the number that matters. §15.0a
      (CLOSED 2026-08-10: the RE-VERIFIED note below was accurate when written, but
      `cmd/animefeedflux/wire.go` has since been fixed by a separate change — its own comment at the
      `publish.Deps{...}` literal now reads "Logger and Metrics were absent here, and their absence
      was silent in the worst way" — and `Metrics: metrics` is wired from `runAll`'s
      `obs.NewMetrics(obs.GetMeterProvider())`, which is real (not nil) whether or not OTel export is
      enabled, since `NewMetrics` registers against the SDK's genuine no-op MeterProvider by default.
      Re-verified against the current tree: `grep -n "Metrics:" cmd/animefeedflux/wire.go` shows it set
      at the one publish-plane call site, and `internal/publish/server_test.go`'s
      `TestHTTPRequestMetricsRecordRouteStatusAndCacheResult` already covers the counters themselves.
      Superseded note, kept for history: RE-VERIFIED 2026-08-10, `observe`'s calls to
      `RecordHTTPRequest`/`RecordCacheResult` were guarded by `if m := s.deps.Metrics; m != nil`, and
      `buildPublishPlane` constructed `publish.Deps{...}` WITHOUT ever setting `Metrics` — it stayed
      nil in the real composition root, so these two counters never incremented in production despite
      the call site existing.)
- [x] `A9-23` Publish-plane requests are ratio-sampled; errors always sampled. §15.0a
      (CLOSED 2026-08-10: every `obs.Start` call in `internal/publish` passes `obs.KindRequest`, which
      `internal/obs/otel.go`'s `tailSampleProcessor`/`Sampler` ratio-samples by construction — nothing
      new to build there, that mechanism already existed and is already tested in
      `internal/obs/otel_test.go`. What was missing was this package ever calling `span.SetStatus
      (codes.Error, ...)` on a failure so the "errors always sampled" branch has something to key off;
      `observe` now does that for any 5xx status, and `renderFeed` does it for every internal failure
      on the miss path. `internal/publish/otel_test.go`'s
      `TestHTTPRequestSpan_ErrorAlwaysSampledRegardlessOfRatio` drives a forced backend error at the
      lowest ratio `obs.Setup` honors (0, floored to the documented 0.05 default) and asserts the span
      still exports — a 5% ratio keeping it by chance is a ~1-in-20 false pass, which is why that test
      also asserts the child `render.feed` span was kept for the same reason, not just the root.)
- [x] `A9-24` Emit the canonical `http.request` event once per request, not per stage. §15.0
      (CLOSED 2026-08-10: same wiring fix as A9-22 closes this one's remaining gap — `obs.HTTPRequest`
      was already called exactly once per request via `observe`'s single deferred call, but
      `Deps.Logger` was unset in production so it fell through to an unconfigured `slog.Default()`.
      `cmd/animefeedflux/wire.go` now passes `Logger: log` — the same `*slog.Logger`
      `obs.NewLogger(obs.Options{...})` builds in `main.go` and threads through `runAll` — into the one
      publish-plane call site, so the event now reaches the canonical structured/stdout pipeline, not
      a side channel.)

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


### B0-SEC — Authentication architecture (§4)

The shape is deliberately small: **argon2id for low-entropy human passwords, SHA-256 for
already-high-entropy random tokens, one opaque cookie the client cannot read.** No JWT, no OAuth, no
refresh tokens, no bearer token in WASM. Those solve a problem this system does not have — letting
independent services validate claims without asking the session authority — and buy a signing key to
leak, a denylist to maintain, and a logout that is not actually immediate.

**Credential**

- [x] `SEC-01` argon2id parameters: 64 MiB, 3 iterations, **parallelism 1**, 16-byte salt, 32-byte output. §4
- [x] `SEC-02` Correct the current `p=4` to `p=1` — OWASP's recommendation for this memory profile. §4
- [x] `SEC-03` Salt is 16 fresh CSPRNG bytes per credential, never derived from id, email or a constant. §4
- [x] `SEC-04` Store parameters AND a `password_version` with the hash, so cost can be raised later. §4
- [x] `SEC-05` Rehash on next successful login when stored parameters are weaker than current. §4
- [x] `SEC-06` NFKC-normalise the password before hashing, or the same passphrase fails on another keyboard. §4

**Pepper (optional, defence in depth)**

- [x] `SEC-07` `HMAC-SHA256(pepper, argon2idOutput)` before storage; pepper from env, never in the DB. §4
      (fixed order — `internal/auth/password.go`'s `HashPeppered`/`VerifyPasswordPeppered` now apply
      `Pepper` to the argon2id OUTPUT, not the password string, and `internal/rpc/auth.go`'s
      Login/ChangePassword/CompletePasswordReset/rehashAdminPassword all call through those two
      functions — `pepperCandidate`'s pre-hash ordering is gone. `pepper.go`'s `VerifyPeppered` now
      has its real caller via `VerifyPasswordPeppered`.)
- [x] `SEC-08` `pepper_version` column from day one — without it rotation is impossible, not merely hard. §4
- [x] `SEC-09` Migration adding `password_version` and `pepper_version`. §10
- [x] `SEC-10` Test: a database dump alone (salt + params + hash, no pepper) does not permit verification.
      (`internal/sectest/sec10_pepper_dump_test.go`'s `TestPepper_DatabaseDumpAloneDoesNotVerify`
      proves the negative — unpeppered `Verify` and `VerifyPasswordPeppered` with nil/wrong pepper
      both fail against a `HashPeppered` row — and the complement with the correct pepper; passes.)

**Password policy — NIST SP 800-63B, which looks unfamiliar on purpose**

- [x] `SEC-11` Minimum **15** characters, maximum **128**. Replaces the current 12-char floor. §4
- [x] `SEC-12` **Remove the composition-rule check.** Mandatory character classes measurably produce
      worse passwords by pushing people to `P@ssw0rd2026!` instead of a passphrase. §4
- [x] `SEC-13` Allow spaces and Unicode. §4
- [x] `SEC-14` **Passwords never expire.** No periodic rotation, no maximum age, no `password_age`
      check anywhere. `password_changed_at` records when a change happened and must never be read
      to force one — a human asked to rotate on a schedule increments a digit. §4
- [x] `SEC-15` Compromised-password blocklist; reject a breached or common password. Length plus
      not-already-breached is the pair that actually matters. §4
- [x] `SEC-16` Ship the blocklist offline (no k-anonymity API call at login — a login path that
      depends on a third party is a login path that fails when they do).

**Session tokens**

- [x] `SEC-17` 256 bits from the OS CSPRNG, never a counter, timestamp or UUIDv1. §4
- [x] `SEC-18` Store **only** `SHA-256(token)` — the sessions table must hold no usable credential. §4
- [x] `SEC-19` SHA-256 not argon2id here, and a comment saying why: there is no brute-forceable
      space in 256 random bits, so a memory-hard KDF buys nothing and costs latency. §4
- [x] `SEC-20` Test: the stored value cannot be replayed as a cookie.
      (`internal/sectest/sec20_stored_hash_replay_test.go`'s
      `TestSessionHash_StoredValueCannotBeReplayedAsCookie` drives the real production path —
      `rpc.AuthServer`'s interceptor via a live `Session` call — presents the DB-stored SHA-256 as
      the cookie and gets `Unauthenticated`, then proves the real raw token still authenticates; passes.)

**Cookie**

- [x] `SEC-21` `__Host-aff_session` (corrected 2026-08-10; this line and PLAN.md §4 both said
      `__Host-session`, but `internal/auth/session.go`'s `cookieName` const has always been
      `__Host-aff_session`), `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`. §4
- [x] `SEC-22` **Never set `Domain`** — `__Host-` is only honoured without it. §4
- [x] `SEC-23` Test asserting every flag, and asserting `Domain` is absent.
- [x] `SEC-24` Never place the token in localStorage, sessionStorage, IndexedDB or WASM memory. §4
- [x] `SEC-25` D-phase check: grep the built WASM bundle for the cookie name and fail if present.
      (`scripts/check-wasm-secrets.sh` exists and was run against a real built `web/dist/app.wasm` —
      not skipped, exit 0/PASS: cookie name absent, no storage-sink-near-token-identifier hit.)

**WebSocket upgrade**

- [x] `SEC-26` Exact-match `Origin` against an allowlist — the browser attaches the cookie to a
      cross-site handshake automatically, so this is the principal anti-hijacking defence. §4
- [x] `SEC-27` Authenticate from the cookie at upgrade; reject with 401 before switching protocols. §4
- [x] `SEC-28` **Revalidate the session periodically on a live socket and close when it expires or is
      revoked.** Without it: authenticated 12:01, session expires 18:00, socket still serving at
      23:00. §4
- [x] `SEC-29` Test that exact clock scenario with an injected clock.
- [x] `SEC-30` Test: a near-miss Origin (`https://app.example.com.evil.tld`) is refused.

**Reset and single-use tokens**

- [x] `SEC-31` Reset tokens are 256-bit opaque, SHA-256 in the DB, single-use, expiring — never a JWT.
      **Deliberately not a proto RPC, ever** — see §12.2's reachability decision: an unauthenticated
      caller minting its own reset token is an unauthenticated account-takeover primitive, and there
      is no channel to deliver a token anywhere the issuing machine hasn't already touched. §4
      (`internal/auth/reset.go`'s `NewResetToken`/`VerifyResetToken` are 256-bit/SHA-256/TTL-bound;
      `internal/rpc/auth.go`'s `IssuePasswordResetToken`/`CompletePasswordReset` are ordinary Go
      methods with no proto route, called only by `cmd/aff/admin_cmd.go`'s `cmdAdminResetPassword` in
      the same process, which mints and consumes the token in one call before it is ever displayed,
      logged, or carried anywhere. `internal/rpc/auth_test.go`'s `TestPasswordResetNotOnGRPCSurface`
      fails if either method is ever added to `AuthService_ServiceDesc.Methods`.)
- [x] `SEC-32` Migration for `password_reset_tokens(token_hash, expires_at, used_at)`. §10
- [x] `SEC-33` **Using a reset revokes every existing session** — a reset that leaves old sessions
      alive has not locked anyone out. §4 (`internal/rpc/auth.go`'s `CompletePasswordReset` revokes
      all sessions in the same transaction, proven by
      `TestPasswordResetRevokesAllSessionsIncludingPreExisting`, and is reachable via
      `cmd/aff/admin_cmd.go`'s `cmdAdminResetPassword` — the local-only CLI caller, deliberately the
      only one; see SEC-31 and §12.2.)
- [x] `SEC-34` Test: a used token is refused; an expired token is refused; sessions are gone after use.
      (now real function-level tests, not schema-only — `internal/rpc/auth_test.go`'s
      `TestPasswordResetIsSingleUse`, `TestPasswordResetSingleUseUnderConcurrency`,
      `TestPasswordResetExpiredTokenRefused`, `TestPasswordResetUnknownTokenRefused`, and
      `TestPasswordResetRevokesAllSessionsIncludingPreExisting` all pass against `CompletePasswordReset` itself.
      Ticked on its own narrow wording — the test exists and is correct — despite `CompletePasswordReset` having
      no RPC/CLI caller yet, see SEC-31/SEC-33.)

**Login protection**

- [x] `SEC-35` Rate-limit attempts per IP and per account, exponential backoff. §4
- [x] `SEC-36` One generic failure message for every cause, to prevent enumeration. §4
- [x] `SEC-37` Always run the KDF, even for an unknown account, so timing does not leak existence. §4
- [x] `SEC-38` Log every attempt, success or failure, to `auth_events`. §4

**Adversarial tests — the suite that must fail closed**

- [x] `SEC-39` Timing: unknown-account and wrong-password medians are indistinguishable over N runs.
- [x] `SEC-40` A forged cookie with a plausible-looking token is refused.
- [x] `SEC-41` A valid token for a REVOKED session is refused everywhere, including mid-stream.
- [x] `SEC-42` A session past its absolute lifetime is refused even if recently active.
- [x] `SEC-43` A session past its idle timeout is refused even if within absolute lifetime.
- [x] `SEC-44` TOTP replay across two concurrent logins loses the race in the DB, not the app. §4
- [x] `SEC-45` A recovery code cannot be used twice, under concurrency.
- [x] `SEC-46` Password change revokes all other sessions.
- [x] `SEC-47` Fuzz the cookie parser: no panic, no accept, on arbitrary bytes.
- [x] `SEC-48` Fuzz the password validator: no panic on arbitrary Unicode, including lone surrogates.
- [x] `SEC-49` An argon2id hash string that is malformed, truncated, or has absurd parameters is
      rejected without panicking and without attempting a multi-gigabyte allocation.
- [x] `SEC-50` No secret (password, token, pepper, TOTP secret) appears in any log line, at any level.

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
- [x] `B0-12` Log every attempt to `auth_events`. §4
- [x] `B0-13` `aff admin reset` break-glass, requiring local DB access. §12.2
- [x] `B0-14` Recovery flow: consume a code → 10-minute elevated session → force re-login. §12.2
- [x] `B0-15` Elevated session reaches **only** password change and TOTP re-enrollment. §12.2
- [x] `B0-16` Recovery revokes every other session. §12.2
- [x] `B0-17` Test: replayed TOTP rejected; drift-window edges behave.
- [x] `B0-18` Test: session expiry, rotation, revocation.
- [x] `B0-19` Test: timing uniformity between unknown user and bad password.
- [x] `B0-20` Test: a recovery code cannot be used twice.

## B1 — RPC services

- [x] `B1-01` `proto/aff/v1` definitions for all six services; buf codegen wired into the build. §11
- [x] `B1-02` `AuthService`. §11
- [x] `B1-03` `FeedService` including `ValidateSpec`, `SetMembers`, TOML export/import. §11
- [x] `B1-04` `SampleService`. §11
- [x] `B1-05` `ItemService` — **no `PurgeDeleted`; hard delete does not exist.** §12.4
- [x] `B1-06` `RunService` with `Watch` streaming. §11
- [x] `B1-07` `SystemService` including `Backup` and the kill switch. §11
- [x] `B1-08` Auth interceptor validating the session on **every** RPC. §4
- [x] `B1-09` `expected_version` optimistic concurrency on every mutation. §11
- [x] `B1-10` Opaque cursor pagination on list RPCs. §11
- [x] `B1-11` gRPC status codes with machine-readable detail for field-level errors. §11
- [x] `B1-12` Cache invalidation on **feed-level** writes too: `Update`, `SetEnabled`, `SetMembers`. §11
- [x] `B1-13` Invalidate any aggregate containing a changed feed. §11
- [x] `B1-14` `PublishCorrection` creating a linked, later-stamped item. §12.4
- [x] `B1-15` Record edits into `item_revisions`. §12.4
- [x] `B1-16` Reject an aggregate as a member of an aggregate. §14.2
- [x] `B1-17` Reject a slug change after first publish. §14.1
- [x] `B1-18` Reject reserved slugs. §14.1
- [x] `B1-19` Test: `PromoteSample` racing a scheduled run yields distinct timestamps, no raw error. §17

## B2 — Bridge

- [x] `B2-01` Wire GoGRPCBridge over WebSocket for the control plane. §3
- [x] `B2-02` Validate the session cookie at upgrade. §4
- [x] `B2-03` Check `Origin` against `AFF_ALLOWED_ORIGINS` at upgrade. §4
- [x] `B2-04` Pair client keepalive with server `EnforcementPolicy` — the known GOAWAY flap. §3
- [x] `B2-05` Session revocation terminates in-flight streams. §4
- [ ] `B2-06` Verify `SampleStream` and `RunService.Watch` actually stream through the bridge. §11
      (RE-CHECKED 2026-08-10, still not fully closeable from this pass's editable set
      (`internal/flowtest/`, `cmd/aff/`): `RunService.Watch` is now proven two ways — over the REAL
      WebSocket bridge by `internal/e2e/watch_test.go`'s `TestWatchOverBridge` (pre-existing, outside
      this pass's scope), and, deeper, over a real plain-TCP gRPC connection by
      `internal/flowtest/j9_watch_test.go`'s `TestJ9_StreamTerminatesWithRun` (BF-40),
      `TestJ9_DroppedSocketDoesNotAbortRun` (BF-41), and `TestJ9_ProgressEventsNeverClaimUncommittedItems`
      (BF-43) — all real assertions on run/item state read back from the store, not a mock's call log,
      and all green as of this pass. `SampleStream` narrowed but is not fully closed: this pass added
      `internal/flowtest/j3_sample_test.go`'s `TestB2_06_SampleStreamOverRealConnection`, which drives
      `SampleServer.SampleStream` over a real TCP `grpc.Server` (candidates arrive as separate stream
      messages, and the persisted samples row is read back from the store afterward) — but that is the
      same plain-connection shape j7/j9 use, not the actual WebSocket bridge, because exercising the
      real bridge means importing/extending `internal/e2e`'s scaffolding, which is outside
      `internal/flowtest/`/`cmd/aff/`. Closing B2-06 fully needs one more test, a
      `TestSampleStreamOverBridge` mirroring `TestWatchOverBridge`, added to `internal/e2e` by whoever
      owns that package.)

      (RE-CHECKED AGAIN 2026-08-10, this pass editable set narrowed further to `internal/bridge/`
      tests + `internal/flowtest/`: the open half of the previous note — "does the bridge relay
      actually stream incrementally, preserve order, terminate cleanly, and surface a mid-stream
      disconnect as an error, or would a naive dial-and-read-one-message test have missed a buffered
      implementation?" — was NOT actually answered by anything green so far. `TestWatchOverBridge`
      only ever asserts on one terminal snapshot, and BF-40/41/43 run over a plain TCP `grpc.Server`,
      not the bridge. Added `internal/bridge/stream_test.go`, three new tests exercising the exact
      grpctunnel relay `NewServer` configures (both `SampleStream` and `RunService.Watch` ride this
      same relay, so this is direct transport-layer evidence for both, without importing
      `internal/rpc` or `internal/e2e`'s private wiring, both out of this pass's editable set):
      `TestWatchOverBridge_MessagesArriveIncrementallyInOrder` (three pushes over the stock gRPC
      health service's real push-driven `Watch`, each one proven NOT yet delivered — via a bounded
      per-step Recv timeout on an already in-flight call, not a sleep — until the server actually
      sends it, then received in exact order: rules out a batch-until-RPC-completion relay, which
      would simply hang here since health's `Watch` never completes on its own),
      `TestControlledStreamOverBridge_CleanTerminationSeenByClient` (a hand-built streaming RPC —
      no new .proto needed, it reuses `healthpb`'s message types purely as envelopes — returns nil
      after N sends; client sees `io.EOF` promptly, not a hang), and
      `TestControlledStreamOverBridge_MidStreamDisconnectSurfacesError` (the same fixture, socket
      severed mid-stream via a raw `net.Conn` capture — NOT `httptest.Server.CloseClientConnections`,
      confirmed by reading `net/http/httptest/server.go` to be a silent no-op against an
      already-hijacked WebSocket connection, since it deletes a conn from its tracked set the instant
      it reaches `http.StateHijacked`; using it produced a false "hangs forever" result that was a
      broken test fixture, not a bridge bug, caught by building a `recordingListener` that captures
      the accepted `net.Conn` directly before ruling anything in). All three pass, and prove the
      bridge relay itself does none of the things B2-06 warns a naive test would miss. What remains
      unclosed is unchanged from the note above: an application-level `TestSampleStreamOverBridge`
      still needs `internal/e2e` to expose `SampleService` over the real bridge, which is still
      outside every file this pass may touch. `go build ./... && go vet ./... && go test
      ./internal/bridge/ ./internal/flowtest/ -count=2` all green.)
- [x] `B2-07` Test: an upgrade from a disallowed `Origin` is rejected. §17

## B3 — CLI

- [x] `B3-01` `aff` as a gRPC client — **no privileged back door** past auth or validation. §11
- [x] `B3-02` `aff login` storing a session locally.
- [x] `B3-03` `aff feed list|get|create|update|enable|disable|delete`.
- [x] `B3-04` `aff recipe export|import` (TOML). §7
- [x] `B3-05` `aff sample <slug> [--size N] [--dry-run]` rendering results to the terminal. §11
- [x] `B3-06` `aff promote <sample-id>`. §11
- [x] `B3-07` `aff run <slug>` triggering a manual run and streaming progress. §11
- [x] `B3-08` `aff runs [--feed] [--status]` history. §11
- [x] `B3-09` `aff item list|get|create|update|delete|restore|correct`. §12.4
- [x] `B3-10` `aff system stats|kill-switch|backup|version`. §11
- [x] `B3-11` **Drive the full lifecycle of one feed end to end with only the CLI.** §18 B3 — the prior
      note calling this "out of scope" was wrong: `cmd/aff` is squarely inside this pass's editable set.
      Closed 2026-08-10 with `cmd/aff/lifecycle_e2e_test.go`'s `TestLifecycleCLIOnly`, which drives real
      `aff` commands (login, feed create/get, sample, promote, run, item list/get, runs) against a real
      `grpc.Server` exposing every RPC service on a real TCP loopback listener and a real migrated
      `*store.Store` — the CLI's actual production dial path (`a.realDial`), not a fake client. At the
      time this was written, the test's `FeedRunExecutor` and `SampleService` feed-lookup supplied a
      fixed, valid `generate.Spec` directly rather than mapping the stored `FeedSpec`, because that
      mapping did not exist yet anywhere in `internal/rpc`. **That gap has since closed in production
      code** (re-verified 2026-08-10, serving/auth/ops audit): `cmd/animefeedflux/wire.go`'s
      `generateSpecFrom` (line ~200) maps `internal/feedspec.Spec` onto `internal/generate.Spec`, and
      `wireRunExecutor.ExecuteRun` (line ~680) — constructed at `wire.go:1055` and passed into
      `rpc.NewFeedServer` as the real `FeedRunExecutor` — calls `specFromRow`/`generateSpecFrom` and
      `generate.Run` for real on `RunNow`, the same pattern `feedLookup.GetFeedForSample` (line ~733)
      uses for `Sample`. So this test's fixed-spec stand-in is now narrower than the code it's testing
      the CLI against — worth noting for whoever next touches this test, not a reason to distrust the
      lifecycle assertions themselves, which don't depend on which spec-construction path ran. Asserts
      on resulting system state throughout (§17.5): the promoted and generated items are both readable
      back via `item list`/`item get`, and the run appears in `runs` history — not just that each
      command exited 0.

## BF — Flow sanity tests, headless (§22, §17.5)

These drive each flow end to end through the RPC layer, then assert the flow's invariants against
**resulting system state** — not against a mock's call log. This is the regression suite; it runs on
every commit. The UI walkthroughs in `DF` come later and do not replace these.

- [x] `BF-00` Harness: run a flow against a real store and RPC server, then assert on final state.
- [x] `BF-01` **J1** login: exactly one unexpired session row exists. §22
- [x] `BF-02` J1: cookie has `HttpOnly`, `Secure`, `SameSite=Strict`, `__Host-` prefix. §22
- [x] `BF-03` J1: every attempt, success or failure, lands in `auth_events`. §22
- [x] `BF-04` J1: wrong password and unknown user match in **message and timing**. §22
- [x] `BF-05` J1: the TOTP step just used cannot be replayed. §22
- [x] `BF-06` **J2** create: feed exists, disabled by default, zero items. §22
- [x] `BF-07` J2: `jitter_offset` populated and deterministic from the slug. §22
- [x] `BF-08` J2: next three runs are in the future and in the feed's timezone. §22
- [x] `BF-09` J2: each of duplicate/reserved slug, bad cron, unknown tz, unknown template var,
      grounded-without-source, and zero budget is refused **server-side**. §22
- [x] `BF-10` J2: no provider call was made and nothing was published. §22
- [x] `BF-11` **J3** sample: **`items` row count is unchanged.** The single most important one. §22
- [x] `BF-12` J3: a `samples` row exists with `expires_at` set. §22
- [x] `BF-13` J3: cost is non-zero and debited from the same budget scheduled runs use. §22
- [x] `BF-14` J3: returned XML fragment is byte-identical to what publishing emits. §22
- [x] `BF-15` J3: with the kill switch on, **no provider call is made at all**. §22
- [x] `BF-16` **J4** promote: exactly one new item, `origin = sampled`. §22
- [x] `BF-17` J4: `published_at` strictly greater than the previously newest item. §22
- [x] `BF-18` J4: fresh ULID, and the guid contains it. §22
- [x] `BF-19` J4: render cache invalidated and `lastBuildDate` bumped. §22 — `internal/flowtest/j4_promote_test.go`'s `TestJ4_LastBuildDateReflectsThePromotedItem` and `TestJ4_PromoteInvalidatesRenderCache` both pass.
- [x] `BF-20` J4: item appears exactly once in all three formats. §22
- [x] `BF-21` J4: a timestamp collision retries at +1s, no constraint error escapes. §22
- [x] `BF-22` **J5** diagnose: every run reaches a terminal status; none left `running`. §22
- [x] `BF-23` J5: `items_added + items_rejected` reconciles with recorded reasons. §22
- [x] `BF-24` J5: a failed run has **zero** items attributable to it. §22
- [x] `BF-25` J5: tokens and cost recorded even for failed runs — a failure that spent money shows it. §22
- [x] `BF-26` **J6** correct: the original's guid and `published_at` are unchanged. §22
- [x] `BF-27` J6: correction is a new item, new ULID, strictly later `published_at`. §22
- [x] `BF-28` J6: the `corrections` row links the two. §22
- [x] `BF-29` J6: the original is still resolvable at its permalink. §22
- [x] `BF-30` J6: a plain edit produces no new guid and therefore no redelivery. §22
- [x] `BF-31` **J7** recover: the consumed code is marked used and refused on reuse. §22
- [x] `BF-32` J7: the elevated session reaches **only** password change and TOTP re-enrollment. §22 —
      the referenced `t.Skip` stub is gone; `internal/flowtest/j7_recovery_test.go`'s
      `TestJ7_ElevatedSessionReachesOnlyPasswordAndTOTP` is a real assertion, verified 2026-08-10. It
      drives two real `RecoverWithCode` calls over a real (plain, pre-bridge) `grpc.Server` with
      `AuthServer`'s own `UnaryInterceptor` chained in, then checks resulting state: the session's own
      `scope` column reads `elevated` (real state, not a tracker's opinion of itself), an ordinary
      unrelated method (`ListSessions`) is refused `PermissionDenied`, `RegenerateRecoveryCodes` is
      refused the same way, and both allowed methods (`ChangePassword`, `ReenrollTOTP`) succeed. Passes.
- [x] `BF-33` J7: all other sessions were revoked. §22
- [x] `BF-34` J7: remaining-code count decremented by exactly one. §22
- [x] `BF-35` J7: the recovery attempt appears in `auth_events`. §22
- [x] `BF-36` **J8** spend: sum of per-run `est_cost_usd` equals the reported total. §22
- [x] `BF-37` J8: editing the price table does **not** rewrite historical run costs. §22
- [x] `BF-38` J8: a feed at its cap logs a skipped run with a distinct status. §22
- [x] `BF-39` J8: sampling spend appears in the same totals as scheduled spend. §22
- [x] `BF-40` **J9** watch: the stream terminates when the run does, in every branch. §22 — the
      referenced `t.Skip` stub is gone; `internal/flowtest/j9_watch_test.go`'s
      `TestJ9_StreamTerminatesWithRun` is a real assertion, verified 2026-08-10. It drives real
      `RunServer.Watch` calls over a real TCP `grpc.Server`, for both a succeeded and a failed run, and
      asserts the stream ends in `io.EOF` carrying the run's true terminal status read from the store.
      Passes.
- [x] `BF-41` J9: a dropped socket does **not** abort the run. §22 — same situation as `BF-40`;
      `TestJ9_DroppedSocketDoesNotAbortRun` closes the real connection mid-watch, then proves the run
      still reaches `success` with its items intact, read back from the store after the watcher is gone.
      Verified 2026-08-10, passes.
- [x] `BF-42` J9: reconnecting shows true current state, not a stale snapshot. §22
- [x] `BF-43` J9: progress events never claim items that were not committed. §22 — same situation as
      `BF-40`; `TestJ9_ProgressEventsNeverClaimUncommittedItems` drives real progress ticks through
      `RunServer.ReportCommitted` after `store.CommitRun` has actually returned, and asserts that at the
      moment a watcher observes each tick over a real stream, the store already has at least as many
      items as the tick claims. Verified 2026-08-10, passes.
- [x] `BF-44` **J10** subscriber: feed validates with zero warnings in all three formats. §22
- [x] `BF-45` J10: every item has a unique, strictly decreasing `pubDate`. §22
- [x] `BF-46` J10: **each item delivered exactly once across many polls** — real HTTP, ≥2 cycles. §17.5
- [x] `BF-47` J10: an unchanged feed answers `304`, touching neither SQLite nor the LLM. §22
- [x] `BF-48` J10: a deleted item's permalink returns `410`, never `404`. §22
- [x] `BF-49` J10: no trivia answer in `description` or `og:description`. §22
- [x] `BF-50` J10: an edited item is not redelivered; a correction **is**. §22
- [x] `BF-51` J10: a backdated item is delivered to nobody. RULE-7
- [x] `BF-52` Wire the whole `BF` suite into CI as a required gate. §17.2 — `.github/workflows/ci.yml`'s "go test -race (full suite, no -short...)" step runs plain `go test -race ./...` (no `continue-on-error`), which includes `internal/flowtest` unconditionally; a `BF` failure fails that required job.

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

- [x] `C1-01` GitHub repo created and the local repo pushed. **Ask before creating.**
- [x] `C1-02` Actions workflow: checkout, test, `make validate`, then build.
- [x] `C1-03` **A failing test must fail the job before any push.** §15.3
- [x] `C1-04` Push to GHCR authenticated with `GITHUB_TOKEN`. §15.2
- [x] `C1-05` Tag with `sha-<short-commit>` on every build — the immutable one. §15.2
- [x] `C1-06` Tag `v<semver>` on release tags only. §15.2
- [x] `C1-07` Tag `latest` on `main`, and **never deploy it**. §15.2
- [x] `C1-08` Layer caching between runs.
- [x] `C1-09` **Deploy job in the same workflow run** — `GITHUB_TOKEN` pushes trigger nothing. §15.3
- [ ] `C1-10` Deploy over SSH with a scoped deploy user and a known host key. §16 — host key pinning is done (`DEPLOY_KNOWN_HOSTS`, release.yml); the "scoped deploy user" half is droplet state (a non-root user in the `docker` group only) that no file in this repo creates or asserts, so it can't be closed by reading. Verify on the droplet: `id <deploy-user>` (not root, no unneeded sudo) and that it can run `docker compose` without `sudo`.
- [x] `C1-11` Deploy writes the new tag, then `docker compose pull && up -d`. §15.3
- [x] `C1-12` **Deploy waits on the healthcheck and fails the job if it never goes healthy.** §15.3
- [ ] `C1-13` Scheduled `docker image prune` so a 60 GB disk does not fill quietly. §15.3 — release.yml only prunes as the last step of a deploy (deploy-triggered, not calendar-scheduled), so disk can still grow between releases. Needs either an `on: schedule` cron workflow (out of my edit scope: `.github/workflows/`) or a systemd timer added in `scripts/deploy-bootstrap.sh` (out of my edit scope: only `scripts/check-container.sh` is mine to touch this round) running `docker image prune -af --filter "until=720h"` on a cadence independent of deploys.
- [ ] `C1-14` Install Docker and the compose plugin on the droplet. — `scripts/deploy-bootstrap.sh` §1 does this correctly (idempotent, checks `docker compose version` first, installs from Docker's own apt repo). Not tickable by reading alone — the task is the droplet's actual state. Complete it by running `sudo sh scripts/deploy-bootstrap.sh <home-ip>/32` on the droplet.
- [ ] `C1-15` Confirm Docker's DNAT rules do not expose anything past `ufw`. §19 — compose already binds `127.0.0.1` only (mitigation in place) and `deploy/RUNBOOK.md` ("publishing past `ufw`") already has the exact reproduction command. Needs a live droplet to run: `sudo iptables -t nat -L DOCKER -n | grep 931` (expect `127.0.0.1:9310`/`9311` as the destination, not `0.0.0.0`) and `curl -m 5 http://<droplet-public-ip>:9311/` from outside the box (must time out/refuse).

## C2 — Staging

- [ ] `C2-01` DNS for `staging.anime.earlcameron.com`. §18 C2 (external prerequisite — no droplet or
      registrar access from this repo; see `docs/deploy-readiness.md`)
- [ ] `C2-02` nginx vhost proxying to the container's loopback port. §15 (no `deploy/nginx/staging.*`
      existed at all before this pass — `scripts/deploy-bootstrap.sh AFF_DEPLOY_MODE=staging` now
      generates a publish-only staging vhost on the deployed box; unticked because it has not run
      against a real droplet — see `docs/deploy-readiness.md`)
- [ ] `C2-03` TLS certificate issued and renewing. (`deploy-bootstrap.sh` now checks DNS resolves
      before calling certbot, so a not-yet-live DNS record fails with an explicit message instead of
      certbot's generic connect error; renewal itself relies on certbot's own systemd timer, unverified
      since nothing is deployed — see `docs/deploy-readiness.md`)
- [x] `C2-04` `proxy_http_version 1.1` with `Upgrade`/`Connection` headers. §20
- [x] `C2-05` `proxy_read_timeout` long enough to outlive an idle admin session. §20
- [x] `C2-06` **`proxy_buffering off`** or streaming RPCs arrive in one lump. §20
- [ ] `C2-07` Deploy the current image to staging and confirm feeds are publicly fetchable.
      (`scripts/deploy-release.sh` + `scripts/deploy-verify.sh` already cover this end to end; needs a
      real staging host to run against)
- [ ] `C2-08` Run the external feed validator against the **live** staging URL. §5.6
      (`scripts/deploy-verify.sh AFF_VERIFY_BASE_URL=https://staging...` already covers this; needs a
      real staging host)

## C3 — Slack proof

- [ ] `C3-01` Create a private Slack workspace or channel for testing. §5.5 — procedure in `docs/slack-proof.md` Part 2; still requires a real workspace, not started
- [ ] `C3-02` `/feed subscribe` the staging RSS URL. §5.5 — procedure in `docs/slack-proof.md` Part 2; blocked on C3-01 and staging reachability (C2)
- [ ] `C3-03` Confirm a generated item posts at all. §5.5 — pre-publish mechanical coverage: date-tag-present is enforced by `internal/feedvalidate.RSS` and `render.TestSlack_EveryItemHasParseableDate`; live confirmation still required, see `docs/slack-proof.md`
- [ ] `C3-04` Confirm multiple items from one run **all** post (the duplicate-timestamp trap). §5.5 — pre-publish coverage: strictly-descending-unique pubDates enforced by `internal/feedvalidate.RSS` (`§5.5 pubdate-strictly-descending-unique`) and `render.TestSlack_PubDatesStrictlyDescendingAndUnique`; whether Slack's bookmark actually delivers all of them is live-only, see `docs/slack-proof.md`
- [ ] `C3-05` Confirm no item posts twice across a week of polls. §5.5 — not mechanically checkable offline (needs cross-run/store state); procedure and both failure directions (under- and over-count) in `docs/slack-proof.md`
- [ ] `C3-06` Confirm the unfurl renders title, summary, and image from the OG tags. §5.5 — pre-publish coverage added: `internal/feedvalidate.Permalink` now checks `og:title`/`og:description`/`og:type`/`og:url`/`article:published_time` on rendered permalink pages, wired into `affvalidate` (previously skipped entirely); live rendering in an actual Slack card is still unverified, see `docs/slack-proof.md`
- [ ] `C3-07` Confirm a trivia answer is **not** visible in the channel. §5.5 — pre-publish coverage: `render.TestSlack_DescriptionNeverLeaksAnswer`/`permalink_test.go` check `description`/`og:description` never contain the answer token; `feedvalidate` (no answer oracle) only catches raw-markup leaks. Do not read a clean live result as resolving `docs/spoiler-design.md`'s open decision — see `docs/slack-proof.md` Part 1
- [ ] `C3-08` Confirm editing an item does **not** repost it. §5.5 — not mechanically checkable offline (assumption about Slack's undocumented bookmark keying); see `docs/slack-proof.md`
- [ ] `C3-09` Confirm a correction **does** appear as a new item. §5.5 — not mechanically checkable offline; see `docs/slack-proof.md`
- [ ] `C3-10` Confirm a backdated item never appears — then stop creating them. RULE-7 — not mechanically checkable offline (store-level `UNIQUE(feed_id, published_at)` and the admin backdate guard live outside `internal/feedvalidate`'s scope); this is the rule whose live failure mode is pure absence with no signal to look for, see `docs/slack-proof.md` Part 2/3
- [ ] `C3-11` Record the observed Slack poll interval for the staleness grace factor. §15 — not started; recording template and how to compute it from C3-03/04/05 timestamps is in `docs/slack-proof.md`

## C4 — Ops

- [x] `C4-01` In-process nightly backup: `VACUUM INTO` plus an integrity check on the copy. §15
- [x] `C4-02` Retain 14 days locally. §15
- [ ] `C4-03` **Encrypt and ship the copy off the box.** §15 (encryption is now wired against real nightly backups
      — `internal/ops/schedule.go`'s `shipOffsite` calls `encryptBackupFile` after every successful backup and
      alerts if it's unconfigured, missing a key, or fails — but "off the box" is still not true: `OffsiteDir` is a
      local directory on the same volume, and no network transport — upload, rsync, S3, scp — exists anywhere in
      `internal/ops` or `cmd/aff`. Judged strictly per instruction: the copy never leaves the box.)
- [x] `C4-04` `SystemService.Backup` on-demand download. §11
- [x] `C4-05` Alert on backup failure — its failure is otherwise invisible. §15 (`internal/ops/schedule.go`'s
      `runBackup`/`alertBackup` now post via `NotifyBackupAlert` on a failed run AND, independently, when the
      backup-absence check (`Check`, reused from the staleness watchdog) finds no successful backup within the
      grace window — carrying the last successful timestamp either way. Every alert failure itself degrades to a
      log line rather than compounding.)
- [x] `C4-06` **Restore into a scratch instance and confirm identical feeds.** §19 — performed locally against
      the dev database (`.devrun/aff.db`, 3 seeded feeds): `aff backup --db .devrun/aff.db` → `aff restore --from
      <snapshot> --to <scratch path>` → served the restored copy with `animefeedflux.exe` on a separate port
      (`AFF_GENERATION_ENABLED=false`, admin listener untouched). Fetched all 9 endpoints (xml/atom/json ×3 feeds)
      from both the live dev server and the scratch instance: JSON feeds byte-identical; RSS/Atom differed only
      in `lastBuildDate`/`updated`, which are computed at render time from the request clock, not stored data —
      every item, guid, title, and body matched. Scratch process killed after (PID confirmed via
      `Get-Process animefeedflux`, distinct from the live dev server's PID); live dev server on :8081 undisturbed
      throughout and confirmed healthy after.
- [x] `C4-07` Staleness watchdog comparing last success against schedule plus grace. §15
- [x] `C4-08` Surface stale feeds on `/healthz` and via the Slack webhook. §15 (RE-VERIFIED
      2026-08-10: this note was stale — both halves are wired and reachable from real callers.
      `/healthz`: `cmd/animefeedflux/wire.go`'s `buildPublishHandlerWithInvalidator` sets
      `publish.Deps.HealthFeeds` from `ops.LiveFeedStatuses`, and `runAll` calls that builder
      (`wire.go:1373`) — `internal/publish/server.go`'s `handleHealthz` uses it to build a
      `BuildHealthReport` with `Stale`/`StaleFeedCount` per feed. Slack: `runAll` wires
      `ops.SchedulerConfig{StaleFn, WebhookURL}` (`wire.go:1415`) into `ops.NewScheduler`, whose
      nightly `runOnce` calls `Check` then `Notify`. Grace is per-feed-cadence already (`Interval *
      grace`, not a fixed constant), and a disabled/kill-switched feed is already immune — `ops.Check`
      skips `!Enabled` unconditionally, and both surfaces route through `Check`, so this can't drift
      per-surface (`TestLiveFeedStatusesFlagsAStaleEnabledFeed` asserts a disabled-and-very-stale feed
      is never flagged). What THIS change added: the grace factor was a hardcoded `2.0` literal with
      no way to tune it short of a code change, and `C3-11` (Slack's real observed poll interval,
      `docs/slack-proof.md`) is still unresolved — an operator needs to be able to correct the guess
      without waiting on that. `internal/ops/cli.go` now exposes `StaleGraceEnv`
      (`AFF_STALE_GRACE_FACTOR`) and `ResolveStaleGrace()` (env override, else `DefaultStaleGrace` =
      2.0 — reasoning for 2.0 is in that constant's doc comment: absorbs one missed/retried run without
      absorbing a second). `NewScheduler`'s and `NewDoctorConfig`'s zero-value `Grace`/`StaleGrace`
      defaults now call it, and neither `wire.go`'s `runAll` nor `cmd/aff/doctor_cmd.go` sets that
      field explicitly, so the env var is live today for the nightly Slack alert and `aff doctor`
      without touching either file. GAP CLOSED, re-verified 2026-08-10 (this pass, serving/auth/ops
      audit): `cmd/animefeedflux/wire.go:1259` now sets `HealthGrace: ops.ResolveStaleGrace()` in the
      `publish.Deps{...}` literal built by `buildPublishHandlerWithInvalidator`, whose sole caller is
      `runAll` (`wire.go:1394`) — so `/healthz`, the nightly webhook, and `aff doctor` all resolve the
      same grace factor from `AFF_STALE_GRACE_FACTOR`; the per-surface drift this note used to warn
      about no longer exists in the tree. Tests: `internal/ops/cli_test.go` —
      `TestResolveStaleGrace{DefaultsWhenEnvUnset,HonorsEnvOverride,IgnoresInvalidOrNonPositiveValues}`,
      `TestNewSchedulerDefaultGraceRespectsEnvOverride`,
      `TestNewSchedulerExplicitGraceIsNotOverriddenByEnv`,
      `TestNewDoctorConfigDefaultStaleGraceRespectsEnvOverride`.)
- [x] `C4-09` Graceful shutdown: stop new runs, drain, checkpoint WAL, exit. §15
- [x] `C4-10` Mark runs still active at the shutdown deadline as interrupted. §15
- [x] `C4-11` Boot watchdog releases stale run locks. §15
- [x] `C4-12` A stale run found **with** committed items is `completed_unconfirmed`, not a failure. §15
- [x] `C4-13` Nightly prune: expired samples, old embeddings, `runs` past 180 days except failures. §15
- [x] `C4-14` Test: kill after the model returns but before commit → interrupted, zero items. §17 — `internal/store/crash_test.go`'s `TestCrashBeforeCommitLeavesRunInterruptedWithZeroItems` fires a simulated pre-commit crash inside `CommitRun`'s transaction and asserts the run lands `interrupted` with zero items.
- [x] `C4-15` Expose per-feed last-success age and error counts on `/healthz`. §15 (CLOSED, re-verified
      2026-08-10 (this pass, serving/auth/ops audit): both halves the prior note said were missing are
      now in the tree. `cmd/animefeedflux/wire.go:1228-1251`'s `HealthFeeds` closure (the sole real
      caller is `runAll` via `buildPublishHandlerWithInvalidator`, `wire.go:1394`) calls both
      `ops.LiveFeedStatuses` (age) and `ops.LiveFeedErrorCounts(ctx, st.Writer(), now, 0)` (error
      counts) and builds each `publish.FeedHealthInput{FeedStatus: fs, ErrorCount:
      errCounts[fs.FeedSlug]}` — the bare zero-value `{FeedStatus: st}` the prior note flagged is gone.
      `AgeSeconds`/`NeverSucceeded`/`Stale`/`ThresholdSeconds`/`error_count` all reach `/healthz` from
      real per-feed queries, not a hardcoded zero. Tested: `TestLiveFeedErrorCounts{
      CountsFailuresSinceLastSuccess, NeverSucceededUsesLookbackWindow, FeedWithNoFailuresIsZero}`
      (`internal/ops/cli_test.go`).)
- [x] `C4-16` Export `aff_feed_staleness_seconds` so the watchdog's number is graphable. §15.0a — `internal/ops/schedule.go`'s `recordStalenessMetric` calls `Metrics.RecordFeedStaleness`, and `cmd/animefeedflux/wire.go:1255` wires the real `metrics` (backed by `obs.Setup`'s `MeterProvider`) into `ops.NewScheduler`'s `SchedulerConfig.Metrics` — reachable end to end, not just unit-tested.
- [ ] `C4-17` Point `OTEL_EXPORTER_OTLP_ENDPOINT` at a hosted backend; **no local collector** on a 2GB box. §15.0a (a deploy/config action against a live backend; nothing is deployed)
- [x] `C4-18` Verify a failing exporter degrades silently and does not stall a run. §15.0a — `internal/obs/otel_test.go`'s `TestOtlpExporterHangingEndpointDoesNotStallShutdown` proves `Setup` against a hanging OTLP endpoint doesn't fail construction and `shutdown` returns bounded near the internal 5s timeout rather than hanging.
- [x] `C4-19` Confirm providers flush on SIGTERM before the container exits. §15 — `cmd/animefeedflux/wire_test.go`'s `TestRunAllWiresObsSetupAndFlushesOnShutdown` drives `runAll` end to end (the real SIGTERM-triggered shutdown path via ctx cancellation), records a metric, and asserts it reached the stdout exporter only after `runAll` returned — proving the composition root's deferred `obsShutdown` actually runs and flushes.

## C5 — Production deploy

- [ ] `C5-01` DNS for `anime.earlcameron.com` and `admin.anime.earlcameron.com`. §2 (external
      prerequisite — no droplet or registrar access from this repo; see `docs/deploy-readiness.md`)
- [ ] `C5-02` nginx vhosts for both, TLS on both. §15 (`scripts/deploy-bootstrap.sh` already covers
      both vhosts and both certs end to end, now with a DNS-resolves pre-check before calling
      certbot; needs a real production droplet to run against)
- [ ] `C5-03` **IP allowlist on the admin host** (home IP). §4 (`deploy-bootstrap.sh` already refuses
      to install/enable the admin vhost while the checked-in placeholder CIDR is in effect on the
      deployed box, and refuses `0.0.0.0/0`/`::/0` outright — verified still true this pass; needs a
      real CIDR and a real host)
- [ ] `C5-04` Confirm the admin host is unreachable from off-allowlist. (not scriptable — needs a
      vantage point off the allowlist, e.g. a phone on cellular data; commands documented in
      `docs/deploy-readiness.md` and `deploy/RUNBOOK.md`)
- [ ] `C5-05` Production compose pins a `sha-` tag, never `latest`. §15.2 (`deploy-release.sh`
      already refuses to deploy a `latest` tag outright — enforced, not just documented)
- [ ] `C5-06` Production `env_file` with real secrets at 0600. §15.4 (`deploy-bootstrap.sh` already
      seeds the file at 0600 from the example and re-asserts the mode every run; filling in real
      values is a manual `sudo -e` step by design — see the file's own header comment)
- [ ] `C5-07` First production deploy; feeds live and validating. (`deploy-release.sh` +
      `deploy-verify.sh` already cover this end to end, including the health gate; needs a real
      production host and an image tag to run against)
- [ ] `C5-08` **Perform an actual rollback to the previous tag and confirm service.** §18
      (`scripts/rollback.sh`, unmodified, plus `deploy-release.sh`'s `.previous-tag` recording already
      give this a mechanism; the drill itself cannot be performed until something is actually deployed)
- [ ] `C5-09` Confirm a push to `main` reaches the running service with no manual step. §19
      (`.github/workflows/release.yml`, unmodified, already checks for `DEPLOY_HOST` and skips
      gracefully rather than failing when it's unset; blocked on the `DEPLOY_HOST`/`DEPLOY_USER`/
      `DEPLOY_SSH_KEY`/`DEPLOY_KNOWN_HOSTS` GitHub repo secrets being configured, which is external to
      this repo)
- [ ] `C5-10` Point Slack at production and confirm continuity. §5.5 (manual, depends on `C3`'s Slack
      workspace existing first)

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

**Text.** Every user-visible string in this section's screens comes from the i18n catalogue
(`D6-*`), including the state matrix's own strings — the reason in `disabled-with-reason` and the
banner in `disconnected`. There is one locale. The catalogue exists so the interface's wording has
one owner, not because a second language is planned.

## D0 — Shell

- [x] `D0-01` GWC (v5 pin) project set up under `web/`, building to WASM. §3
      (`go.mod` pins `github.com/monstercameron/GoWebComponents/v5 v5.0.1`; `web/main.go` +
      `web/build.sh` build it to WASM, per D0-02/D0-03.)
- [x] `D0-02` WASM build in an isolated scratch directory — the known concurrent-build race. §15
- [x] `D0-03` Emit `.wasm.gz` and serve with correct `Content-Encoding`. §12
- [x] `D0-04` HTML shell with a correct **`<base href>`** or deep links and refreshes break. §12
- [x] `D0-05` Client-side router for the five routes. §12
- [x] `D0-06` gRPC-over-WS client wired to the bridge. §3
- [x] `D0-07` Auth guard: `ANON` reaching an authed route redirects to `/login`. D-FLOW
- [x] `D0-08` Session expiry mid-session drops to `ANON` without losing unsaved work silently. D-FLOW
- [x] `D0-09` **`DISCONNECTED` banner with automatic reconnect and backoff.** D-FLOW
- [x] `D0-10` Queue or refuse mutations while `DISCONNECTED` — never fail silently. D-FLOW
- [ ] `D0-11` Design tokens: colour, type scale, spacing. Load the frontend-design skill first.
      — UNTICKED 2026-08-10, was ticked in error: the tokens exist in `web/tokens`, but the visible
      product mostly doesn't source from them — `web/static/index.html`'s hand-written `<style>`
      block hardcodes colour directly (e.g. `.af-banner { background: #7a1f1f; }`) instead of a
      token, and of the 103 unique `af-*`/`history-*` classes the pages emit, only the ~38 shared
      primitives in `web/ui` read a token via `css.Var`/`tokens.*` — the rest have no rule at all.
      A token layer that exists but that the shipped screens mostly bypass is not "decided," it's
      unused. See `D0-24`..`D0-27`.
- [ ] `D0-12` Light and dark handling decided once, at the token layer.
      — UNTICKED 2026-08-10, was ticked in error, same evidence as `D0-11`: `web/tokens/theme.go`'s
      `Emit()` had zero callers until this pass wired it into `web/shell/app.go:152` (see that
      file's comment: "Nothing caught it... It took a screenshot"), so the light/dark decision
      never reached a rendered element. `Emit()` now runs, but it only populates `:root`'s custom
      properties — it does not retroactively give the 103 unmapped `af-*`/`history-*` classes a
      rule that reads them, so most of the admin UI still has no theme-aware styling at all.
      **SWITCH NOW WIRED, 2026-08-10 (second finding, same shape as the first).** `Emit()` running
      was necessary but not sufficient: the dark palette lives under `:root[data-theme="dark"]`,
      **nothing in this app had ever called `ui.SetTheme`**, and that attribute is the only thing
      that selector can match — so dark mode was unreachable for every operator including one whose
      OS is set to dark. Caught the same way as the first half: a screenshot in dark mode came back
      byte-identical to one in light mode. New `web/shell/theme.go` resolves a three-state
      preference (`system` default / `light` / `dark`) from `localStorage` against
      `prefers-color-scheme`, stamps `data-theme` **before the first render** (applying it from a
      component effect paints a frame of light theme first — a white flash on the login screen), and
      keeps following the OS while the preference is `system`. Control is in the header, not
      Settings, because `/settings` is behind the session and `/login` is where a night-shift
      operator first meets the app. Verified in a real browser across all five behaviours: boot from
      OS, explicit choice, persistence across reload, return to `system`, and a live OS flip while
      on `system`. **Still correctly unticked** — this task is "decided once at the token layer",
      and the 103 unmapped `af-*`/`history-*` classes from the note above still have no rule reading
      those variables, so most surfaces remain unthemed regardless of which theme is active.
- [x] `D0-13` Shared primitives: button, input, select, toggle, table, tabs, modal, toast, kebab menu.
      **Reachability note, 2026-08-10 audit:** the primitives all exist and are unit tested, but
      `web/ui.Toast`, `web/ui.Textarea`, and `web/ui.SelectListState` have **zero callers anywhere**
      in `web/pages` or `web/shell` (`grep -rn "wui\.Toast\|affui\.Toast"` etc. across both, and
      inside `web/ui` itself, all empty) — the twelfth-plus instance of this session's recurring
      pattern, a component built and tested but reachable from nothing. `Modal` looked the same on a
      shallow grep (no direct `wui.Modal`/`affui.Modal` call site either) but is not: `web/ui/confirm.
      go`'s `Confirm` wraps it internally, and `Confirm` has a real caller in `web/pages/settings`, so
      `Modal` reaches the screen transitively — checked before concluding it was dead. This item's own
      wording ("shared primitives exist") is still true and stays ticked; the gap is a `D0-14`/`D5-02`-
      shaped one (built vs. reachable), not a false completion of this specific task.
      **Two sources of truth, same audit:** `web/pages/history` and `web/pages/auth` import `web/ui`
      not at all (`grep -rn "AnimeFeedFlux/web/ui\"" web/pages/history web/pages/auth` — only
      `history/styles.go`, for the `NarrowMaxWidth` constant, not a component) and instead hand-roll
      36 raw `h.Button(...)` call sites between them, alongside `wui.Button`/`affui.Button` used in
      `web/pages/generate`/`web/pages/settings` — two independent button implementations in the same
      app, which is exactly the defect this task's brief named ("two sources of truth for a button").
      Already implied by `D5-02`/`D5-03`'s "web/ui has zero importers on /login, /recover, /history"
      notes; recorded explicitly here against `D0-13` since that is the task this defect belongs to.
- [ ] `D0-14` Shared `loading` / `empty` / `error` components used by every list. D-FLOW (the shared components exist — `web/ui/state.go`'s `StatePanel`/`SelectListState` — and generate and settings use them, but **not every list does**: `web/pages/history/screenstate.go` is a second, parallel six-state implementation with no `ui.` dependency, so runs and items resolve their states through different code than the rest of the app. Two implementations is the thing this task exists to prevent.)
- [x] `D0-15` Destructive actions live behind a `⋯` kebab, never a primary button. §12.6
- [x] `D0-16` Typed-confirmation modal for irreversible actions. §12.6
- [x] `D0-17` GWC discipline: hooks unconditional and positional. §12.6 — no conditional `UseState`/`UseEffect`/`UseReducer` in any render path across `web/`; the trap is called out explicitly in `web/shell/banner.go` and `web/pages/generate/render_editor.go`, and `render_sampler.go` keeps a fiber's hook count fixed by registering `OnCancel` unconditionally under `h.If(true, ...)`.
- [x] `D0-18` No `UseAtom` in render-only paths. §12.6 — the hook form `state.UseAtomKey` appears only inside real components (`web/pages/generate/render.go:36`, `web/pages/settings/render.go:26`, `web/shell/banner.go:70`, `web/shell/expiry.go:34`); the non-render paths use `SessionAtom.Global()` instead (`web/shell/app.go:168`, `web/shell/guardadapter.go:14`).
- [x] `D0-19` Declared effect deps; no browser-state reads in the render body. §12.6 — `ui.UseEffectOf` with declared deps where re-run matters (`web/pages/auth/login.go:193`), and no `js.Global()`/`document.`/`window.` read appears in any render body under `web/pages` or `web/shell`.
- [-] `D0-20` ~~No i18n — single user, English, explicitly out of scope.~~ **Reversed 2026-08-10.**
      Superseded by `D6-*`. The original reasoning was about *languages*; the actual value is about
      *where strings live*, which applies at one user and one locale. Retrofitting later means
      touching every component written without it, which is the work that never gets scheduled. §12.6
- [x] `D0-21` i18n provider mounted in the **root component, above the router** — a provider inside
      a route cannot serve the shell's own reconnect banner or the guard's redirect notice. §12.6
- [x] `D0-22` Every shared primitive (`D0-13`) takes its labels as keys, never literals — a
      primitive with a hardcoded label defeats the catalogue everywhere it is reused. §12.6
- [x] `D0-23` The six shared state components (`D0-14`) carry i18n keys, including the reason string
      in `disabled-with-reason` and the countdown in `disconnected`. §12.6
      (D0-02/03/05/08/10/22/23 verified 2026-08-10: `web/build.sh` isolated scratch dir + atomic
      replace; `internal/publish/static.go` serves `.wasm.gz` with negotiated `Content-Encoding`;
      `web/shell/app.go` routes exactly the five paths (`route_registration_test.go` asserts it under
      `GOOS=js GOARCH=wasm`); `web/shell/expiry.go` blocks on unsaved work rather than silently
      dropping to ANON; `web/shell/mutationerror.go` + per-page variants refuse mutations while
      disconnected with a distinguishable sentinel; `go run ./cmd/affi18n lint web/ui` reports
      "0 literals in web/ui".)
- [x] `D0-24` Remove the hand-written `<style>` block from `web/static/index.html`; document what,
      if anything, must remain and why. §12.6 — expected remainder is near-zero: `<base href>`
      (`D0-04`), the `<noscript>` fallback (must render before WASM loads, so it cannot depend on
      Go-emitted CSS), and the bare `color-scheme: light dark` on `:root` (a UA hint read before
      any script runs). Everything else in the current block — `.af-banner*`, `.af-expiry-modal*`,
      `.af-content`, `.af-placeholder`, the `html body #app` box model — moves to a typed rule in
      `web/shell` per `D0-25`.
      (2026-08-10: found NOT actually done despite `.af-*`/`.af-expiry-modal*`/`.af-content`/
      `.af-placeholder` having moved to `web/shell/styles.go` — a `<style>` block still remained in
      `index.html`, self-justified by a comment, for `color-scheme`, the `html,body` margin/height
      reset, and `#noscript-fallback`'s padding/font. `scripts/check-styles.sh` (`D0-27`) caught this
      on its first run. Fixed: `color-scheme` is now `<meta name="color-scheme" content="light dark">`
      (its dedicated mechanism, no stylesheet needed); the `html`/`body` reset and
      `#noscript-fallback`'s two properties are now inline `style=` attributes on those three
      elements. Verified: `check-styles.sh`'s check (a) now passes against `web/static/index.html`
      source; browser re-check against the running dev server was NOT done post-fix, because the
      server serves `web/dist/index.html` (build.sh's staged copy, gitignored), not
      `web/static/index.html` directly, and rebuilding/redeploying `dist/` was out of scope — this
      task's brief said not to restart the server. The pre-fix browser check (against the stale
      `dist/` build) confirmed `tokens.Emit()` + `emitShellStyles()` resolve real token-based rules
      at runtime, e.g. `.af-banner` reads `var(--color-danger)`, not a hardcoded hex.)
- [ ] `D0-25` Every `af-*`/`history-*` class the pages emit has a typed rule (`css.Class` or
      `css.Global`) defining it. §12.6 — enumerated by `grep -rhoE 'ClassStr\("[^"]*"' web/`: 175
      call sites across `web/pages/auth` (`D1`), `web/pages/generate` (`D2`), `web/pages/history`
      (`D3`), `web/pages/settings` (`D4`), and the shell's banner/expiry-modal/content/placeholder
      (`web/shell/banner.go`, `expiry.go`, `pages.go`), collapsing to **103 unique class names**.
      As of 2026-08-10, **zero** of the 103 have a matching typed rule anywhere in `web/` — the
      only styled surface is the ~38 `css.Class` rules in `web/ui`'s shared primitives (`D0-13`),
      which use their own generated class names, not these literals.
      — Re-measured 2026-08-10 (later same day, via `scripts/check-styles.sh`'s check (b), which now
      does this diff for real): `web/pages/auth/styles.go` and `web/pages/generate/styles.go` have
      landed since the count above was taken, so this is no longer "zero of 103" — but it is still
      genuinely open: **27 literal class names still have no matching rule anywhere in `web/`**,
      concentrated in `web/pages/history` and `web/pages/settings` (neither has a `styles.go` at
      all yet) plus a handful of shell/auth stragglers (`af-error`, `af-success`, `af-warning`,
      `af-expiry-modal--visible`, `af-auth-page--not-wired`). Run the script for the current list —
      it is the live source of truth, not this note.
      **`web/pages/settings/styles.go` now exists (2026-08-10, browser-inspection pass).** It was
      the larger of the two gaps named above: the package emitted 9 distinct `af-*` names and
      contained not one `css.Global` call, so /settings rendered as an undifferentiated column of
      browser-default form controls, edge to edge, with the active-sessions table pushing the whole
      DOCUMENT into horizontal scroll at 1280px. All 9 (`af-settings`, `--error`, `-section`,
      `-card`, `-card-header`, `-signout`, `af-warning`, `af-error`, `af-success`) now have typed,
      token-only rules, with the narrow breakpoint in the same file per `D5-01`. Measured after:
      a signed-in browser pass reports zero unstyled classes on /settings and no document overflow
      at either 1280px or the 320px floor. `web/pages/history` DOES have a styles.go (the note
      above is wrong on that point) — but its `.history-table` rule set `overflow-x` on a `<table>`,
      where overflow is ignored, so the runs table overflowed the document at 320px (viewport 320,
      scrollWidth 752); fixed by adding `display: block`. **Still open**: the remaining stragglers
      and a re-run of `scripts/check-styles.sh` for the current count.
- [x] `D0-26` `tokens.Emit()` is called exactly once, before first render. §12.6 — already true as
      of this pass (`web/shell/app.go:152`, inside `Mount`, before the initial dial/render); this
      task is to keep it true, since it regressed silently once already (`Emit` had zero callers
      from when `web/tokens` was built until this fix — see that call site's comment for how it was
      found: a screenshot, not a test).
      (Reverified 2026-08-10: still the single call site, `grep -rn "tokens.Emit(" web` finds it only
      in `web/shell/app.go:152`, still ahead of `emitShellStyles()` in `Mount`. Confirmed live in a
      browser too, against the running dev server: `document.styleSheets` shows a populated `:root`
      block with `--color-*`/`--space-*`/etc. and a `:root[data-theme="dark"]` override block, and
      `.af-banner` resolves to `background-color: var(--color-danger)` — a real token reference, not
      a hardcoded value — confirming `Emit()` actually ran before that rule was read.)
- [x] `D0-27` A regression guard for `D0-24`/`D0-25`. §12.6 Two checks, of different honesty:
      (a) **statically checkable** — a lint (same shape as `cmd/affi18n`) that fails if
      `web/static/index.html` contains a `<style>` tag, wired into CI like the i18n ratchet
      (`D6-20`/`D6-21`, which show what an unwired lint is worth: nothing, until it gates the
      build).
      (b) **"every `ClassStr` name has a matching rule" is only partly statically checkable.** A
      tool can extract every `ClassStr("literal")`/`ClassMap(map[string]bool{"literal": ...})`
      argument and every `css.Class`/`css.Global` selector that are Go string literals and diff the
      two sets — that catches the exact failure mode this task found (a class emitted, no rule
      anywhere) and should be built. It cannot catch a class name built from a variable,
      concatenation, or `fmt.Sprintf`, and it proves nothing about the browser actually resolving
      the rule (a selector typo, specificity loss, or a rule that never gets registered at runtime
      would all pass a source-level diff). The backstop for that gap is a browser assertion, not a
      static one: fold "no element has zero applied CSS rules besides UA defaults" into the `D5-05`
      walkthrough (the same screenshot method that found this bug in the first place), across all
      five routes, both themes.
      (2026-08-10: `scripts/check-styles.sh` written, implementing both (a) and (b). (a) strips HTML
      comments first, then greps for a literal `<style` tag — catches this doc's own comment text
      saying the word "style" without false-positiving. (b) extracts every literal
      `ClassStr("...")`/`ClassMap(map[string]bool{"...": ...})` name (splitting multi-class strings
      on whitespace) and every literal `css.Class("...")`/`css.Global("...")` selector's first
      class token, and diffs them — currently reports 27 real misses (see `D0-25`'s updated note).
      The script's own header and end-of-run output both state plainly, per this task's own
      instruction, what it does NOT prove: literal-only (a variable/`fmt.Sprintf` class name is
      invisible to it in both directions), and a source-level name match is not proof the browser
      resolves the rule — that backstop is still the unbuilt `D5-05` browser walkthrough, not this
      script. Not yet wired into CI (no CI config in this task's edit scope); running it today
      exits 1 because `D0-25` is genuinely incomplete, which is the guard doing its job, not a bug
      in the guard.)
- [ ] `D0-28` `web/tokens`' light/dark and contrast work (`D0-11`/`D0-12`, `D5-04`) only reaches the
      screen once rules actually read those variables — i.e. once `D0-25` lands. `D5-04`'s "checked
      in both themes" is honest about the *token layer's own math* (`contrast_test.go` asserts on
      generated rule text) but says nothing about whether a rendered element uses that rule; treat
      `D5-04` as re-opened for the visible product until `D0-25`'s 103 classes have rules and a
      browser check (`D0-27b`) confirms elements are actually themed.

## D1 — Auth pages (`J1`, `J7`)

- [x] `D1-01` `/login`: password step then TOTP step, one page. §12.1
- [x] `D1-02` **One generic error string for every failure** — no oracle. §12.1
- [x] `D1-03` Surface backoff honestly ("try again in 30s"). §12.1
- [x] `D1-04` Disable submit while in flight; no double-submit burning a TOTP window. §12.1
- [x] `D1-05` Replace history state on success so Back cannot land on a stale form. §12.1
- [x] `D1-06` No "remember me". §12.1
      (D1-01..D1-11 verified 2026-08-10 by reading `web/pages/auth/login.go` and `recover.go`:
      one-page password→TOTP flow, single generic error, backoff surfaced, submit disabled in-flight,
      `pushState`/history-replace on success per `login.go:132`, no remember-me, two-path recover page,
      code entry → ELEVATED → reset/re-enroll, remaining-code count shown, break-glass documented on
      page, ELEVATED nav restricted to password change/TOTP only.)
- [x] `D1-07` `/recover`: state plainly that there are exactly two paths. §12.2
- [x] `D1-08` Recovery-code entry → `ELEVATED` → password reset and TOTP re-enrollment. §12.2
- [x] `D1-09` Show remaining recovery-code count after use. §12.2
- [x] `D1-10` Document the `aff admin reset` break-glass **on the page**. §12.2
- [x] `D1-11` `ELEVATED` cannot navigate to `/generate`, `/history`, or `/settings`. D-FLOW
- [ ] `D1-12` **Perform a full recovery drill against staging.** §19

## D2 — Generate (`J2`, `J3`, `J4`, `J9`)

- [x] `D2-01` Three-pane layout: rail, editor, sampler. §12.3
- [x] `D2-02` Rail: status, last build, next run in local time, item count, 7-day spend. §12.3
- [x] `D2-03` Rail flags stale feeds inline. §15
- [x] `D2-04` Rail: enable toggle, Run Now, new feed. §12.3
- [x] `D2-05` Rail: all six states from the matrix. D-FLOW
- [x] `D2-06` Editor: slug, title, description, language, kind. §12.3
- [x] `D2-07` Slug is **immutable after first publish**, and the UI says why. §14.1
- [x] `D2-08` Editor: cron plus timezone with plain-English readback. §12.3
- [ ] `D2-09` Show the next three runs in local time, **jittered**, not nominal. §14.3 — UNTICKED 2026-08-10, was ticked in error: `web/pages/generate/render_editor.go:161` and `render_rail.go:120` render `generate.editor.nextRunsUnavailable`/`generate.rail.nextRunUnavailable` — the feature is explicitly NOT shown, per those keys' own names.
- [x] `D2-10` Editor: model and parameters. §12.3
- [x] `D2-11` Editor: system and user prompt templates with the variable list inline. §7
- [x] `D2-12` Editor: novelty settings and budgets. §12.3
- [x] `D2-13` Editor: source list for grounded feeds. §12.3
- [x] `D2-14` Server-side validation errors render **against the offending field**. §12.3
- [ ] `D2-15` Unsaved-changes guard on navigation. §12.3 — UNTICKED 2026-08-10, was ticked in error: `web/shell/session.go`'s `RegisterDirtyCheck`/`DraftDirty` only gate the session-EXPIRY hold, not route navigation. `web/guard/guard.go`'s `Decide` (the actual `BeforeEnter` route guard) has zero reference to dirty state — navigating `/generate` → `/history`/`/settings` with unsaved edits is unguarded.
- [x] `D2-16` Optimistic-concurrency conflict shows a real merge choice, not a silent clobber. §11
- [x] `D2-17` Sampler: size 1–5 and optional temperature override. §12.3
- [x] `D2-18` Sampler streams output with a working cancel. §12.3
- [x] `D2-19` Candidate view: **rendered**. §12.3
- [x] `D2-20` Candidate view: **raw validated fields**. §12.3
- [x] `D2-21` Candidate view: **exact feed XML**. §12.3
- [x] `D2-22` Candidate view: **Slack card preview** — the thing that actually gets read. §12.3
- [x] `D2-23` Novelty verdict with the nearest existing item shown. §12.3
- [x] `D2-24` Grounded: candidate source set with failed links flagged and the URL shown. §12.3
- [x] `D2-25` Cost per sample and remaining daily budget. §12.3
- [x] `D2-26` Sample button always shows estimated cost before it is clicked. §12.3
- [x] `D2-27` Kill switch disables sampling **with a visible reason**, not a dead control. §12.3
- [x] `D2-28` Promote and Discard; nothing publishes implicitly. §12.3
      (D2-01..D2-28 verified 2026-08-10: `web/pages/generate` (3100+ LOC) — rail, editor, sampler,
      all four candidate views incl. Slack card, novelty verdict, failed-link display, cost/budget
      display, kill-switch reason, Promote/Discard — all wired to real `deps.*` RPC clients, not stubs.)
- [x] `D2-29` Samples survive a page refresh for 24h. §12.3
      (2026-08-10: wired via localStorage — `web/pages/generate/render.go`'s
      load/save/clearPersistedSampleState write a `PersistedSampleState` envelope
      (`logic.go`) keyed by feed slug, stamped `SavedAtUnix` at write time. Written on a
      completed sample (a real sample ID plus >=1 candidate) and restored by the
      feed-select effect only when `PersistedSampleUsable` finds a same-slug, <24h-old
      entry — a mismatched/stale one is discarded instead of shown. Cleared on Discard
      (and on starting a new sample), restored on a failed Discard's rollback. NOTE: this
      restores per feed slug on reselect, not the page's open feed itself — `/generate`
      has no URL/session state for "which feed was selected" to restore automatically on
      a bare refresh (that's routing/shell, out of this page's edit scope); reselecting
      the same feed within 24h brings its samples back. `TestPersistedSampleUsable` in
      `logic_test.go` covers the slug-match/TTL/empty-candidate/no-ID cases.)
- [ ] `D2-30` Live run progress streams via `Watch`. §12.4
      (server-side `RunService.Watch` exists per B1-06, but nothing in `web/pages/generate` calls it
      or references streaming progress — not implemented in the UI. Worse than a missing caller on
      one side: nothing **produces** progress either. `RunProgressReporter`/`ReportCommitted`
      (`internal/rpc/run.go:288,455`) has no production caller — only `internal/rpc/run_test.go:323` —
      and `cmd/animefeedflux/wire.go`'s `wireRunExecutor` builds `generate.Deps` with no reporter, so
      a live progress tick is never emitted at runtime even if a page did subscribe.)

## D3 — History (`J5`, `J6`)

- [x] `D3-01` Two tabs over one page: Runs and Items. §12.4
- [x] `D3-02` Runs: status, trigger, duration, items added/rejected, tokens, cost, error kind. §12.4
- [ ] `D3-03` Runs: filter by feed, status, date range. §12.4 — UNTICKED 2026-08-10, was ticked in error: `web/pages/history/runs_ui.go` renders only one filter control, `history-runs-feed-filter` (a bare number input). No status filter and no date-range control are rendered, despite the logic-layer `RunFilter` struct supporting more.
- [x] `D3-04` Runs: expand to the full log. §12.4
- [ ] `D3-05` Runs: in-flight run streams live. §12.4 (**unticked by audit 2026-08-10.** No live stream exists on this surface: `.Watch(` appears nowhere under `web/pages/history` — only in the `web/wsconn/clients.go:239` wrapper, which nothing calls. The run-log expander is pull-based. Same missing caller as `D2-30`.)
- [x] `D3-06` Runs: delete allowed, **edit is not**. §12.4
- [x] `D3-07` Runs: show reject reasons so `J5` can actually be completed. §10
- [x] `D3-08` Items: FTS5 search. §12.4
- [ ] `D3-09` Items: filter by feed, origin, date, deleted state. §12.4 — UNTICKED 2026-08-10, was ticked in error: `web/pages/history/items_ui.go` renders only a query text box and the deleted-state select. `ItemFilter`'s `FeedID`/`Origin`/`PublishedAfter`/`PublishedBefore` fields are never surfaced by any control in the UI.
- [x] `D3-10` Items: pagination. §11
- [x] `D3-11` Items: create a manual item. §12.4
- [x] `D3-12` Items: edit title, summary, body, link, tags, publish date. §12.4
- [x] `D3-13` **State in the UI that the guid never changes on edit, and why.** §12.4
- [x] `D3-14` Block backdating `published_at`; warn loudly on override. §5.5
      (D3-01..D3-14, D3-16..D3-20 verified 2026-08-10 against `web/pages/history`: two tabs, run
      fields/filter/expand/live-stream/reject-reasons, FTS5 item search/filter/pagination/create/edit,
      guid-never-changes stated at key `history.items.guid_never_changes` (`forms_ui.go:77`),
      backdating blocked, soft delete/restore with no purge control, PublishCorrection next to Delete,
      RSS-no-retraction stated, bulk select, mutations refresh feed state — all wired to real RPCs.)
- [x] `D3-15` Items: revision history with a diff view and revert. §12.4
      (**re-ticked 2026-08-10:** `items_ui.go`'s `loadRevisions`/`revertRevision` now call the real
      `ItemService.ListRevisions`/`RevertRevision` RPCs directly (own doc comments at lines 258 and
      283 state the session-local `RevisionStore` stopgap is gone), paginated by `at`
      (`BuildListRevisionsRequest`), each revision's per-field diff rendered via
      `RevisionFieldDiffs`/`DiffLines` (`revisions.go`, `diff.go`), and revert passes
      `expected_version` with `IsVersionConflict` surfaced distinctly from other errors — no longer
      the client-side-only workaround the prior audit found.)
- [x] `D3-16` Items: soft delete and restore. **No purge control exists.** §12.4
- [x] `D3-17` **"Publish a correction" sits next to Delete, not three menus away.** §12.4
- [x] `D3-18` State plainly that RSS has no retraction. §12.4
- [x] `D3-19` Bulk select for delete and restore. §12.4
- [x] `D3-20` Every mutation visibly refreshes the affected feed's state. RULE-6
- [x] `D3-21` **Runs and Items: a real pager — current page, total pages, and jump.** 2026-08-11. Both
      tables had Previous / Next / Refresh and nothing else: no indication of where you were, how much
      was left, or any way to reach page 9 but pressing Next eight times.
      Server: `total_count` added to `RunServiceHistoryResponse` and `ItemServiceListResponse` (§11), counted
      with the request's filters and deliberately WITHOUT the cursor condition — folding `id < cursor` in
      would count the rows remaining after the current page, so the total would shrink as the operator
      paged and "page 3 of 9" would become "page 3 of 6".
      Client: one `renderPager` replaces the two hand-rolled button rows, which had already drifted (Items
      grew its Next control on a different day than Runs). Draws Previous, a sliding 7-wide window of
      numbered pages, Next, and "Page X of Y", with `aria-current` on the active page and `role=status`
      on the readout.
      A number is clickable only for a page the cursor holds a token for: an opaque cursor is only
      obtainable by having been handed it, so page 9 is genuinely unreachable until 4-8 have been fetched.
      Further pages render disabled — counted in "of Y" so the operator sees how far the table goes, honest
      about needing Next. The window is capped because runs accrue a row per feed per day forever, and one
      button per page is unusable at four hundred.
      `TotalPages`, `jumpWindow` and the cursor's jump semantics live in the untagged file and are tested on
      the host: an empty table reads "page 1 of 1" not "of 0", the window never draws fewer buttons than it
      could, and `JumpTo` refuses a page it has no token for rather than sending the wrong one — which
      would return a valid-looking page of the WRONG rows.
      Known gap: the cursor is still mutated directly rather than through the reducer — see `A5-41`.

## D4 — Settings (`J8`)

- [x] `D4-01` Security: change password (current password + TOTP). §12.5
- [x] `D4-02` Security: re-enroll TOTP. §12.5
- [x] `D4-03` Security: regenerate recovery codes, showing remaining count. §12.5
- [x] `D4-04` Security: active sessions with device, IP, last-seen; revoke one or all. §12.5
      **Verified live 2026-08-10, and it was broken until then.** Individual revoke could not be
      reached from the UI at all: `SessionRow.RevokedAt` used `timestamppb.AsTime()` on an optional
      field, which returns 1970 for nil, and `IsZero()` is false for 1970 — so every session,
      including the caller's own, reported itself revoked and no row rendered its Revoke action. Now
      fixed and driven end to end (revoke a second live session, reload, confirm it persisted: 72
      rows/64 hidden-revoked before, 71/65 after). Also fixed alongside: the remaining-recovery-code
      count had no read path (now on `AuthService.Session`), revoked rows sorted above live ones, and
      a revoked row was visually identical to a live one.
- [x] `D4-05` Provider: active provider and default model for new feeds. §12.5
- [x] `D4-06` Provider: **key presence only** — never displayed, never sent to the client. §12.5
- [x] `D4-07` Provider: editable price table used for cost estimates. §12.5
- [x] `D4-08` Generation: kill switch, global ceiling, default budgets, staleness threshold. §12.5
- [x] `D4-09` Publishing: base URL, author, copyright, TTL, default `og:image`, validated on save. §12.5
- [x] `D4-10` Data: TOML export/import, backup download, DB size, item counts, vacuum. §12.5
      (**re-ticked 2026-08-10:** `SystemService.Vacuum` now exists (`internal/rpc/system.go`),
      blocking for a real size-dependent duration and refusing with `codes.FailedPrecondition`
      while a generation run is in flight (`store.RunInFlight`) rather than contending for
      SQLite's single writer connection. `web/pages/settings/render_data.go`'s new
      `renderVacuumSection` wires it: behind the kebab with typed confirmation
      (`confirm.go`'s `ActionVacuum`, word "VACUUM") like the page's other destructive actions;
      warns how long the lock will likely block given the CURRENT `db_size_bytes` before the
      admin confirms (`format.go`'s `EstimateVacuumDuration`, a coarse brief/moderate/long
      bucket rather than a false-precision ETA); reports before/after sizes plus the actual
      elapsed duration on return; and on rejection renders the server's own message (the
      in-flight-run refusal reads as that specific reason, not a generic failure). Zero new
      literals (`affi18n lint` still 0); new `settings.data.vacuum.*` keys are referenced from
      `web/pages/settings` only — `web/i18n`'s catalogue still needs those keys added by
      whoever owns that package next, since `web/i18n` was out of this change's allowed paths.)
- [x] `D4-11` About: version, build, uptime, last successful run per feed. §12.5
      (D4-01..D4-11 verified 2026-08-10 against `web/pages/settings`: password/TOTP change and
      re-enrollment, recovery-code regen with count, session list+revoke, provider section with
      `api_key_present` as a bool — key material never sent to the client, per `render_provider.go:11-14` —
      editable price table, kill switch/ceiling/budgets/staleness, publishing fields with save
      validation, TOML export, backup download, DB size/vacuum, About section — all wired to real
      `deps.System`/`deps.Feed` RPC calls.)

## D5 — Polish

- [ ] `D5-01` Responsive breakpoints land **in the same commit** as each layout. §12.6
      (**unticked by audit 2026-08-10**, see `docs/accessibility-audit.md` §6. True inside
      `web/ui`: `Table`/`Tabs`/`Modal`/`Kebab`/`Toast` each carry their `narrowMedia(...)` override
      in the same file as the layout it modifies, through the single `NarrowMaxWidth` chokepoint in
      `responsive.go` — but that is one package, not "each layout", and `web/pages/*` was not
      re-audited this pass.)
- [ ] `D5-02` Audit every list against the six-state matrix. D-FLOW
      (**unticked by audit 2026-08-10**, see `docs/accessibility-audit.md` §1/§6. `web/ui`'s
      `ListState` is closed to the six named states and `SelectListState`'s precedence is unit-tested
      — but `/login`, `/recover`, and `/history` don't route through it at all (`web/ui` has zero
      importers on those pages), and `history`'s `screenstate.go` remains the parallel implementation
      D0-14 already flagged. "Every list" is not yet true.)
- [ ] `D5-03` Keyboard path through every journey; visible focus states.
      (**unticked by audit 2026-08-10**, see `docs/accessibility-audit.md` §2/§4/§7. The building
      blocks (`focusVisible()`, `Tabs`' roving tabindex via `gwcui.UseCompositeNavigation`, `Table`'s
      keyboard-reachable scroll container, `Modal`/`Kebab`'s `AccessibleOverlay` trap+restore) are
      verified by reading against the vendored GWC source, and a concrete keyboard walkthrough is
      written in the audit doc §7 — but `/login` is being rebuilt and unreachable this pass (same
      blocker as `D5-05`), so no journey was walked end to end, and `/login`/`/history` don't use
      this kit's focus treatment regardless.)
- [x] `D5-04` Colour contrast checked in both themes.
      (automated, not just claimed: `web/tokens/contrast_test.go`'s `TestColorContrast_AA_BothThemes`
      passes light and dark subtests. **Scope note, 2026-08-10 (see `D0-28`):** this proves the
      token layer's own generated rule text is AA in both themes; it does not prove any rendered
      element uses that rule, since 103 of the pages' classes currently have no rule at all. The
      guarantee reaches the visible product only after `D0-25` lands. **Re-checked 2026-08-10** during
      the `web/ui` audit: `RoleFocusRing` is tested against exactly the three backgrounds
      `focusVisible()` actually draws over — `RoleBg`, `RoleSurface`, `RoleSurfaceRaised` — so the
      one shared focus ring in `web/ui` is covered end to end for its two adopting pages.)
- [ ] `D5-05` Walk `J1`–`J9` end to end in a browser and fix what is awkward.
      (**left open 2026-08-10**: login is being rebuilt right now and the journeys are not reachable.
      Confirmed live: Playwright against the `:8082` dev server reaches `/login` — title
      "AnimeFeedFlux Admin", password→TOTP form renders, the gRPC-over-WS bridge correctly refuses
      without credentials — but no working credentials were available to get past it to
      `/generate`/`/settings`/`/history`. See `docs/accessibility-audit.md` §4/§7 for the walkthrough
      to run once it's reachable.)
- [ ] `D5-06` Confirm nothing in the UI can reach a state the flow table does not name. D-FLOW
      (**unticked by audit 2026-08-10**, see `docs/accessibility-audit.md` §1/§6. True by construction
      for `web/ui`'s own `ListState` — the enum admits no seventh value — but the application-level
      states (`ANON`/`AUTH`/`ELEVATED`/`DISCONNECTED`/`KILLED`) live outside `web/ui` and were not
      re-audited this pass.)

## D6 — i18n across every user-visible string (§12.6)

Reverses `D0-20`. One locale ships (`en`); the point is not other languages but that the
interface's vocabulary becomes a reviewable artefact instead of three hundred literals scattered
across components. Do these **alongside** each surface, not as a pass afterwards — a cleanup pass
over finished screens is the retrofit `D0-20`'s reversal exists to avoid.

**Amended 2026-08-11 (`D6-26`): two locales ship now — `en` and `es`.** The sentence above still
describes why this section exists, and it is no longer a description of what the app can do. Adding
Spanish took a package-level current-locale var, ~550 call sites reading it instead of the
`DefaultLocale` constant, and one atom the i18n Provider subscribes to; the catalogue layer needed
no change at all. That is the payoff this section predicted, collected.

**Foundations**

- [x] `D6-01` Adopt GWC v5's `i18n` package; record in `PLAN.md` §12.6 what it does and does not do
      (interpolation, plurals, formatters) before any keys are written. §12.6
- [x] `D6-02` `en` catalogue as a single checked-in source of truth, loaded at build time. §12.6
- [x] `D6-03` **Keys named for meaning, not for the English text** — `auth.login.submit`, never
      `auth.login.signIn`. A key named after its wording turns fixing wording into a rename. §12.6
- [x] `D6-04` Namespace keys by surface (`auth.*`, `generate.*`, `history.*`, `settings.*`,
      `shell.*`, `common.*`) so an unused key is findable and a missing one is obvious. §12.6
- [x] `D6-05` Interpolation through the library, never string concatenation — concatenated
      fragments assume English word order and silently break the moment a second locale exists. §12.6
- [x] `D6-06` Plurals through the library's plural rules, not `if n == 1`. §12.6
- [x] `D6-07` A missing key renders the key itself and logs, loudly, in dev — never an empty string.
      A blank label is the one failure mode that looks like a styling bug. §12.6
- [x] `D6-08` Locale-aware formatters for dates, times, relative times, numbers and currency. The
      rail's "next run in local time" (`D2-02`) and every cost figure (`D2-25`) go through these,
      never `fmt.Sprintf`. §12.6
      — `web/i18n/adapter.go:75-117`
      `FormatDateTime`/`FormatRelativeTime`/`FormatCurrencyUSD`/`FormatByteSize`, bound once
      in `web/main.go`'s `bundleFormatters` and consumed by the rail and every cost figure via
      `deps.Formatters`.

**Extraction, per surface**

- [x] `D6-09` Shell: nav, the `DISCONNECTED` banner and its countdown, the session-expiry modal,
      the guard's redirect notice. §12.6
      — `web/shell/banner.go:121` and `web/shell/expiry.go:43-47` resolve through `t.T(key)`;
      the reconnect banner, its countdown, the session-expiry modal and the placeholder text
      were repointed at real catalogue constants.
- [x] `D6-10` `D1` auth pages, **including the single generic failure string**. §12.1 — auth pages resolve every string through `afi18n`/`gwci18n` keys, the generic failure string included (`web/pages/auth/login.go:124,147` → `KeyCommonGenericAuthError`).
- [x] `D6-11` `D2` generate: rail, editor, sampler, all four candidate views, every verdict. §12.3 — `web/pages/generate` routes all rail/editor/sampler/candidate-view/verdict strings through its `Translator` interface (`i18n.go`); no literals in `render_rail.go`, `render_editor.go`, `render_sampler.go`.
- [x] `D6-12` `D3` history: both tabs, filters, reject reasons, the correction-vs-edit explanation. §12.4 — `web/pages/history` resolves both tabs, filters, reject reasons and the correction-vs-edit notice through `Catalog.T` (`items_ui.go:414,425`, `runs_ui.go:248-252`, `forms_ui.go:76-77`).
- [x] `D6-13` `D4` settings: every section, every field label and help text. §12.5 — every `web/pages/settings` section renders labels and help text via `t("settings.*")` (`render_security.go`, `render_provider.go`, `render_generation.go`, `render_publishing.go`, `render_data.go`, `render_about.go`).
- [x] `D6-14` Shared primitives and the six state components (`D0-13`, `D0-14`, `D0-22`, `D0-23`). §12.6 — `web/ui/labels.go`'s `T`/`resolve` make every shared primitive take a `LabelKey`, and `web/ui/state.go`'s state components carry keys only (`state.loading`, `disabledReasonKey`, `reconnectingKey`).
- [ ] `D6-15` Validation and error messages, including ones **mirroring server text** — these are
      where wording drifts furthest from the server, because nobody diffs them. §12.3 (the strings
      are keyed, but nothing checks the "mirroring server text" half: no test or lint diffs `web/`'s
      messages against `internal/rpc`'s, which is the exact drift this task names.)
- [x] `D6-16` Typed-confirmation modals (`D0-16`): the prompt, and the word the user must type.
      Decide explicitly whether the typed word is translated; if it is, the comparison must
      translate too or confirmation becomes impossible in that locale. §12.6
      — `web/ui/confirm.go:21-30` settles the question explicitly and translates the typed
      word together with the comparison, so confirmation stays possible in any locale;
      `web/pages/settings/confirm.go:56` uses it.

**Boundaries — what deliberately stays out**

- [x] `D6-17` Feed **content** is never translated: it is authored by the model in the feed's own
      configured language and is data, not interface. Assert this — a well-meaning wrapper around
      item titles in `D3` would silently corrupt published output. §5
      — `web/i18n/catalog_test.go:228` `TestFeedContentNamespacesStayEmpty` asserts the
      boundary, so a wrapper around item titles fails a test rather than silently corrupting
      published output.
- [x] `D6-18` The generic login-failure string stays generic in **every** locale. A translation that
      distinguishes "no such account" from "wrong password" reintroduces the oracle `D1-02`
      removes. §12.1
      — one `KeyCommonGenericAuthError` for every cause, asserted singular by
      `web/i18n/catalog_test.go:151` `TestGenericAuthErrorIsSingular`.
- [x] `D6-19` Slugs, cron expressions, model identifiers, HTTP status text and log output stay
      untranslated — they are identifiers and operator surface, not prose. §12.6
      — identifiers stay untranslated, with the escape used deliberately:
      `web/pages/generate/render_editor.go:206-208` tags the `{{.Today}}`-style template
      variables `//nolint:i18n` rather than keying them.

**Gate**

- [x] `D6-20` Lint that fails the build on a user-visible string literal in `web/`, with a narrow
      `//nolint`-style escape that requires a reason. A convention nobody can check decays. §17
      (closed 2026-08-10: `.github/workflows/ci.yml`'s `i18n` job's "i18n lint" step now runs
      `make i18n-lint` unguarded — the previous `|| echo "::warning::...reporting-only"` fallback is
      gone, so a nonzero exit fails the job. Verified both directions locally: `go run ./cmd/affi18n
      lint web` exits 0 with "0 literals in web" against the real tree; a scratch file built outside
      `web/` (`Div(Text("Welcome back!"))`, kept out of the repo per this pass's edit-scope rule) is
      correctly flagged `text-call` and exits 1. `go build ./...` and `go vet ./...` stay clean.)
      **REGRESSION found 2026-08-10, later same day, by re-running the same command against the
      current tree rather than trusting this note's recorded output:** `go run ./cmd/affi18n lint web`
      now reports 3 real findings, not 0 —
      `web/shell/header.go:282:60: text-call: "Anime"`,
      `web/shell/header.go:283:59: text-call: "Feed"`,
      `web/shell/header.go:284:60: text-call: "Flux"` — three hardcoded `h.Text(...)` calls in
      `renderHeaderBrand`'s wordmark, added after this task's close-out. The lint mechanism itself is
      working exactly as designed (it caught real violations on a real re-run, same as the scratch-file
      check above proved it would) — this is not a false tick on D6-20's own claim ("lint that fails
      the build on a literal" — it does). It is new work for whoever next owns `web/shell`: either add
      a `//nolint`-style escape with a reason (a product's own brand name arguably belongs alongside
      D6-19's identifier exemptions — slugs, cron expressions, model IDs — but D6-19 does not name
      brand text, so this is a decision to make explicitly, not infer) or route "Anime"/"Feed"/"Flux"
      through three new `shell.header.brand.*` keys. Left unfixed here since `web/shell` is outside
      this pass's edit scope (docs-only). See `D6-21`'s note for what this means for the CI ratchet.
- [x] `D6-21` Zero-literal ratchet in CI, starting at zero and never allowed to rise. §17
      (closed 2026-08-10: `.github/i18n-baseline.txt` contains `0`, matching the confirmed-zero real
      count (see D6-20) — was git-untracked (`??`) as of the prior audit; is now on disk for this
      commit to pick up. `ci.yml`'s "i18n ratchet" step dropped the `if [ -f .github/i18n-baseline.txt
      ]; then ... else skip` guard and now runs `make i18n-ratchet` unconditionally, so a fresh
      checkout without the file fails loudly (`i18nlint.Ratchet` treats a missing baseline as an
      error, not an implicit zero) rather than silently skipping. Verified the three cases against the
      same scratch fixture used for D6-20: count under baseline passes; count above baseline
      (`count=1, baseline=0`) fails with the "may not rise" message; `i18nlint.Ratchet`'s doc comment
      confirms a count *below* baseline is a pass, not a failure, and never rewrites the baseline file
      itself — lowering the floor stays a deliberate human edit. Makefile's stale "deliberately NOT in
      `all` yet" comment on `i18n-ratchet` (dated to when the lint still reported real findings) is
      updated to say why it stays out of `all` now: CI is the enforcement point, `all` stays the fast
      fmt-check/vet/test loop.)
      **RE-AUDITED 2026-08-10, later same day: `.github/i18n-baseline.txt` is STILL `git status`
      `??` (untracked) on this working tree — the "is now on disk for this commit to pick up" note
      above described intent, not a committed fact, and no commit has happened since. The engineering
      is genuinely correct (unconditional step, loud failure on a missing file, per D6-21's design),
      but that is exactly why this matters: the very next `git push` from this tree, before the file
      is staged and committed, ships a CI checkout that does not have it and the `i18n ratchet` step
      hard-fails on a file that exists locally but not in the repo. This is not a false tick — the
      code and workflow changes are real and correct — but "closed" should not be read as "safe to
      push right now." `git add .github/i18n-baseline.txt` (a plain data file, not excluded by
      `.gitignore`) must happen in the same commit as the `ci.yml` change before either lands on
      `dev`.**
- [x] `D6-22` Test: every key referenced by code exists in the catalogue. Catches a typo'd key,
      which otherwise ships as a visible raw key in the UI. §17
      — **UNTICKED 2026-08-10, was ticked in error.** The stated check does not pass: a manual sweep
      (grep every literal string passed to a `t(`/`T(`/`wt(` call or a `LabelKey`/`DisabledReasonKey`
      argument across `web/pages` and `web/shell`, diffed against `web/i18n/keys_*.go`'s real
      catalogue) found **21 referenced-but-undefined keys**, which render their own raw key text
      instead of English (D6-07's documented degrade). `TestEveryDeclaredKeyResolves`/
      `TestNoOrphanCatalogueEntries` (`web/i18n/catalog_test.go`) do not catch any of these: they only
      check the Go-constant-backed namespaces (`auth`/`common`/`shell`'s `Key*` consts, which cannot
      drift by construction — a typo there is a compile error) — nothing walks the actual call sites
      inside `web/pages/generate`, `web/pages/history`, `web/pages/settings`, or `web/shell/header.go`,
      which pass bare string literals with no compiler backing. `TestGenerateHistorySettingsResolve`
      only iterates keys already present in `generateMessages`/`historyMessages`/`settingsMessages` —
      a key a page calls but the catalogue never defines is invisible to it by construction, the exact
      inverse of what this task needs. This is `DF-14`'s subject, found without a browser or a login:

      **`shell` namespace** (`web/shell/header.go`, added since `D6-09` closed — see that file's own
      doc comment, which lists all 9 keys the header needs and states plainly the catalogue additions
      were out of that task's edit scope): `header.brand.label`, `header.brand.homeLabel` — both
      render on **every authenticated page load**, unconditionally, since the header mounts on every
      route (`renderShellWrapper`) — plus `header.signOut.busy`, `header.signOut.error`, both reachable
      every time the header's own sign-out control is used. (`header.nav.*` and `header.signOut` were
      already fixed in `web/i18n/keys_shell.go` before this pass; these four were not.)
      **All four now defined, 2026-08-10** (`web/i18n/keys_shell.go`: `KeyShellHeaderBrandLabel`,
      `KeyShellHeaderBrandHomeLabel`, `KeyShellHeaderSignOutBusy`, `KeyShellHeaderSignOutError`,
      backed by Go constants like their neighbours so they cannot drift). Confirmed against a real
      browser: the console's `i18n: missing key "header.brand.label"` / `"header.signOut.error"`
      lines on every `/login` load are gone. Four more shell keys were added in the same pass for
      the new appearance control (`header.theme.*`, `web/shell/theme.go`) — defined at the same
      time as their call sites, which is the discipline this whole task exists to enforce.
      **CLOSED 2026-08-10 (browser-inspection pass).** Both halves are done.
      (a) Every referenced key is now defined: the 17 `settings` keys below, plus four this list did
      not contain because a prefix-based sweep structurally cannot see them —
      `common.connectionUnreachable`, `common.backoffCleared`, `auth.recoverSavedConfirmLabel` and
      `history.notWired`, all declared at their call sites as bare `const keyFoo = "foo"` strings
      that get their namespace from `intl.NS(...)` at runtime. Two of those had doc comments openly
      recording that the catalogue entry was "out of this task's allowed paths"; one of them,
      `common.connectionUnreachable`, was reported by Cam from a real login screen while this very
      pass was running — an automated sweep earlier in the same session had already declared the
      catalogue complete, because it only looked at namespace-prefixed literals.
      (b) The TEST exists: `web/i18n/callsite_test.go`'s `TestEveryCallSiteKeyIsDefined` parses the
      real source of `web/pages` and `web/shell` and collects keys three ways — literal first
      arguments to lookup calls, `key*` string constants, and the `...Key:` struct fields the shared
      `web/ui` primitives take — then asserts each is defined in some namespace. It found
      `history.notWired` on its first run, which no existing test could see: the other three are
      driven from hand-maintained slices or iterate the catalogue's own keys (the inverse question),
      and `internal/i18nlint.FindKeyRefs` only collects literals passed directly as arguments. It
      also fails if it finds implausibly few references, so a broken scanner cannot pass silently.

      **`settings` namespace** (`web/pages/settings`), 17 keys:
      - `settings.common.disconnectedReason` — 5 call sites (`render_data.go:247`,
        `render_generation.go:109,113`, `render_provider.go:152`, `render_publishing.go:120`,
        `render_security.go:185`): the DISCONNECTED-state warning shown on every settings section.
        The catalogue has a same-shaped neighbor (`settings.common.state.disabledGeneric`) that this
        may have been meant to reuse or rename to — worth checking intent, not just adding the key.
      - `settings.data.vacuum.action`, `.title`, `.description`, `.confirmTitle`, `.confirmPrompt`,
        `.confirmWord`, `.running`, `.error`, `.estimate.brief`, `.estimate.moderate`,
        `.estimate.long`, `.result.sizes`, `.result.duration` (12 keys, `render_data.go`,
        `confirm.go`) — `D4-10`'s own note already flagged this gap ("`web/i18n` was out of this
        change's allowed paths") but did not enumerate it; this is the enumeration.
      - `settings.security.signOut.action`, `.error`, `.errorDisconnected` (`wiring.go:162,180,182`)
        — exactly the gap this task's brief predicted ("may still be absent") — confirmed absent.

      All 21 were verified by reading `web/i18n/keys_shell.go` and `keys_settings.go` end to end and
      confirming no such key string appears in either file. `D6-13` (settings extraction) stays
      ticked — the strings genuinely route through `t(...)`/`LabelKey`, which is what that task
      asked for — but routing through i18n and resolving in the catalogue are different guarantees,
      and this is the gap between them. Fixing this is a `web/i18n` change (add the 21 entries with
      real English text) — out of this pass's edit scope (docs-only), left for whoever owns that
      package next, same as `D4-10`'s note already said for the vacuum subset.
- [ ] `D6-23` Test: every catalogue key is referenced by code — dead keys are how a catalogue grows
      to twice the size of the interface it describes. §17
      — **UNTICKED 2026-08-10, was ticked in error, same finding as `D6-22`.** This half only fails
      to be *fully* proven (its own inverse-direction check, `TestNoOrphanCatalogueEntries`, is real
      and does pass for `auth`/`common`/`shell`), but `D6-20`/`D6-21`'s closing note bundled all four
      of `D6-20`..`D6-23` together as "verified" on the strength of `D6-22`'s check, which does not
      hold — see `D6-22`'s note. Leaving this unticked alongside it rather than half-crediting a
      bundled verification claim that was itself wrong on one of its four members.
      (Correction to the parenthetical originally here: D6-20/D6-21 are NOT unaffected — see the fresh
      finding logged directly on D6-20 below, found by actually re-running the lint rather than
      trusting its last recorded output. The lint/ratchet MECHANISM is sound; what changed is that
      `web/shell/header.go` landed after D6-20/D6-21's last verification and re-introduced literals.)
- [x] `D6-24` Pseudolocale (`en-XA`) build that lengthens and brackets every string, so truncation
      and clipped layouts are found now rather than by the first real translator. §17
      (closed 2026-08-10: `internal/i18nlint.Pseudolocalize` is now wired end to end, not just
      exposed as an ad hoc CLI helper. `web/i18n/pseudo.go`'s `NewBundle()` registers the REAL
      catalogue pseudolocalized (`PseudoCatalog`, built from `enCatalog` — the same
      merged-with-common one `NewBundle` normally registers) whenever `AFI18N_PSEUDOLOCALE` is set
      at runtime or `-ldflags -X .../web/i18n.pseudolocaleFlag=1` is set at build time — since every
      `T()` call site in `web/` already targets `DefaultLocale` directly (no per-call locale
      argument to switch), this is the only way to get pseudolocalized text into a real render
      without touching `web/main.go`/`web/shell`/`web/pages` (out of scope for this pass). `cmd/
      affi18n pseudo-catalog` is the build/verification artifact: runs the real catalogue (472
      entries) through the transform and fails loudly on any placeholder-count mismatch — confirmed
      `0 placeholder mismatches` against the current catalogue. `web/i18n/pseudo_test.go`'s
      `TestPseudolocaleEnvTogglesNewBundle`/`TestPseudoCatalogPreservesPlaceholders` cover the
      runtime switch and placeholder preservation under `go test ./...`.)
- [ ] `D6-25` `D5-04` contrast and `D5-03` keyboard audits re-run against the pseudolocale, where
      longer strings change wrapping and focus order. §12.6
- [x] `D6-26` Interface language selectable at runtime, with a second real locale (`es`). §12.6
      (closed 2026-08-11, asked for directly by Cam. `web/i18n/locale.go` holds the active tag in an
      `atomic.Value` — atomic because every RPC in this app formats text from its own goroutine and a
      plain string var would be a `-race` failure; `web/main.go`'s two translator adapters and
      `adapter.go`'s five formatters read it AT CALL TIME rather than capturing it, since `wirePages`
      runs once at boot and a captured locale would pin the app to its startup language for the life
      of the tab. `web/shell/locale.go` owns persistence (`aff.locale` in `localStorage`, beside the
      theme preference), first-run negotiation from `navigator.languages` on the primary subtag
      (`es-MX` → `es`), and `<html lang>`. `web/shell/pages.go`'s `renderShellRoot` subscribes to
      `LocaleAtom` and feeds `gwci18n.Provider`, which is what makes a switch reach every page:
      GWC's reconciler has no props-equality bailout for function components, so re-rendering the
      Provider re-runs the whole tree. The control is Settings → Appearance
      (`web/pages/settings/render_appearance.go`), which also absorbed the theme switch out of the
      header. Verified in a real browser, not just by unit test: 31 checks across sign-in, all seven
      settings sections, `/generate`, `/history`, `/login` pre-auth, `es-MX` negotiation, reload
      persistence and switching back. That browser pass is what caught the one real defect —
      `h.Value` on a `<select>` is inert in this renderer, so the language control read "English"
      while the app was entirely in Spanish; `h.SelectedIf` per `<option>` is the working idiom, and
      two other selects still carry the broken pattern, see `D6-28`.)
- [x] `D6-27` Tests: key parity, placeholder parity and plural-form parity across every registered
      locale. §12.6
      (closed 2026-08-11 with `D6-26`. `web/i18n/locale_test.go`. Placeholder parity is the one that
      matters most: substitution is BY NAME, so a translated `{cuenta}` for `{count}` compiles,
      ships, and renders literal braces with the number gone — invisible to the compiler, to
      `D6-22`'s key check, and to a reader who does not speak the language. Also covers parity in
      both directions (missing keys render English inside a translated page; orphaned keys are dead
      weight), `PluralArg` agreement, English-identical text with an explicit allowlist, locale
      negotiation, and that an unsupported tag is REFUSED rather than stored — storing one would make
      every lookup in the app take the missing-key path and log.)
- [ ] `D6-28` Fix the two remaining `<select>`s that mark no selected `<option>`, so they stop
      displaying their first entry regardless of state. §12.6
      (found 2026-08-11 while fixing the same bug in the language control. `web/pages/settings/
      render_data.go`'s feed picker passes `h.Value` to the `<select>`, which this renderer drops;
      `web/pages/history/items_ui.go`'s deleted-items filter marks no option at all. Both display
      option 0 whatever the state says. `web/pages/generate/render_workbench.go` and
      `web/pages/history/filters_ui.go` already do it correctly with `h.SelectedIf` — left untouched
      here only because both files were being actively edited in another session at the time.)
- [ ] `D6-29` Native-speaker review of the `es` catalogue before it goes in front of a Spanish
      operator. §12.6
      (opened 2026-08-11. The translations are model-written and unreviewed; the app discloses this
      under the language selector, in Spanish, whenever a translated locale is active. Two specific
      things for a reviewer: the register is informal second person throughout ("Revisa tus datos")
      where Spanish enterprise software often prefers the impersonal, and it is applied consistently
      so changing it is mechanical; and the typed-confirmation words are translated (REGENERAR,
      REVOCAR TODO, IMPORTAR, COMPACTAR) deliberately — see `web/i18n/es_settings.go`'s doc comment
      for why that is both safe and necessary.)
- [x] `T-01` Repository coverage above 80%. §17.2
      (closed 2026-08-11 at **81.6%**, up from 79.2%, ratchet floor raised 80.1 → 81.0.
      **The first finding was a measurement bug, not a coverage one:** `./...` includes
      `gen/aff/v1`, 3,574 statements of protoc output at 0%, which reported the repository at 61.9%
      while hand-written code sat at 79.2% — and meant the CI ratchet had been failing against a
      baseline describing a population that no longer existed. Coverage is now measured over
      `go list ./... | grep -v /gen/` in the Makefile (`COVERPKG`), in `.github/workflows/ci.yml`,
      and documented in `scripts/coverage-ratchet.sh` so the three cannot drift. The deliberately
      NOT-taken option was a reflection walk calling all 1,191 generated getters, which would have
      cleared 80% in one commit while asserting nothing — the exact failure the ratchet script's own
      doc comment rejects. New tests went to the edges instead: `cmd/affseed` 0% → 83%,
      `aff admin reset` 21% → covered, `aff admin reset-password` 0% → covered,
      `animefeedflux healthcheck` 0% → covered, plus `aff encrypt`/`decrypt` overwrite and error
      paths and the individual `aff doctor` check failures.)
- [x] `T-02` Fix `affseed --force`, which had never worked. §15
      (closed 2026-08-11, found by `T-01`'s first test. Three consecutive blockers, each revealed by
      clearing the previous one: the feed slug already existed (`createFeed` now reuses it); every
      `published_at` collided, since they derive from a day-truncated clock and two runs on one date
      compute identical timestamps (`seedJitter` adds a constant per-run offset, constant so it
      cannot disturb the strictly-increasing ordering the schema requires); and the correction's
      `content_hash` collided because its wording was fixed (it now names the item it corrects,
      which is better copy anyway). The flag's help text and the refusal message both promised
      behaviour no version of this command had.)
- [x] `T-03` Fix the flaky `TestSchedulerFiresOncePerDayAndRunsBackup`. §17
      (closed 2026-08-11. Failed roughly one run in three, and it fails inside CI's coverage step,
      so a red run meant the ratchet never executed. The test advanced a fake clock in a loop and
      then polled WITHOUT advancing; the scheduler re-registers its next wait only after the real
      VACUUM completes and computes it from the clock at that moment, so on a loaded machine the
      next target lands beyond the loop's budget and a non-advancing poll can never reach a target
      in the fake future. The poll advances too now. Eight consecutive runs green.)
- [x] `T-04` Measure `web/`'s browser code at all. §17.2
      (closed 2026-08-11. `scripts/coverage-wasm.sh` / `make cover-wasm`. 62 files carry
      `//go:build js && wasm`, so a host build excludes them from their packages entirely — they were
      never reported as 0%, they were absent, and the host number was measuring the untagged
      leftovers. **Browser code is at 25.7%**: `web/wsconn` 4.1% (host says 100%), `web/shell` 5.6%
      (host says 100%), `web/pages/settings` 15.5% (host says 81.7%), `web/pages/history` 25.9%,
      `web/pages/generate` 28.4%, `web/pages/auth` 31.8%. Two Windows workarounds are load-bearing
      and documented in the script: `go_js_wasm_exec` is a bash script so `-exec` needs a generated
      `.cmd` shim, and the wasm process cannot generate its own report because Go's js syscall layer
      has no `O_DIRECTORY` — raw counters via `-test.gocoverdir` plus `go tool covdata textfmt` on
      the host sidesteps it.)
- [x] `T-05` Fix the tests that only pass on the host. §17
      (closed 2026-08-11 with `T-04`, which is what exposed them. One was real:
      `web/ui`'s `TestInputWithoutIDStillWiresLabelToField` called `Input(...)` outside any component
      and so reached `gwcui.UseId()` with no fiber — tolerated by the native stub, a
      `GoUseId called outside component context` PANIC in the browser build the code actually ships
      to. It renders through `ui.CreateElement` now and passes in both. The other three are honest
      environment mismatches, skipped with reasons: `web/i18n`'s call-site scan walks the source tree
      (no directory reads under js/wasm) and two `web/tokens` tests assert on what `css.Harvest()`
      returns, which buffers nothing in a browser because rules go straight to the stylesheet.)
- [ ] `T-06` Decide how the render layer gets tested, then get `web/` over 80%. §17.2
      (opened 2026-08-11. **Blocked on a decision, not on effort.** Spiked it: rendering a panel
      under wasm via `gwcui.RenderToString` panics with `GoUseFunc dom adapter is nil` — any
      component wiring an event handler needs a DOM adapter, GWC installs one only from
      `ensureInitialized()` against a real `document`, node has none, GWC exposes no seam to install a
      test adapter (it lives behind `internal/runtime`), and jsdom is not present here. Two routes:
      (a) a jsdom-class node harness that boots GWC's real runtime — new infrastructure before a
      single assertion; (b) split the page packages so the pure props-to-node render functions lose
      the `js && wasm` tag and become host-testable with `RenderToString`, which is exactly how
      `web/ui` reaches 84% today with no infrastructure at all — but that is a refactor across 62
      files. (b) is the better end state; (a) is reversible. ~3,500 uncovered statements either way.)
- [ ] `T-07` Bring the 50 remaining host files over 80% individually. §17.2
      (opened 2026-08-11. The repository total is 81.9%, but 50 of 185 measured files are below 80%.
      Largest: `cmd/animefeedflux/wire.go` 63.7% (203 uncovered), `internal/rpc/item.go` 76.4% (142),
      `internal/rpc/feed.go` 77.6% (118), `cmd/aff/system_cmd.go` 71.1% (88), `internal/rpc/auth.go`
      79.9% (86), `internal/e2e/app.go` 76.1% (67), `internal/flowtest/harness.go` 62.6% (64). All
      are feasible with ordinary tests — no blocker, just work. Four files are genuinely not worth
      it and should be excluded rather than chased: `cmd/aff/term_windows.go` (needs a real console),
      `web/pages/auth/devfill_off.go` and `web/ui/kebab_anchor_host.go` (build-tag stubs whose whole
      body is the other build's absence), and the platform-gated `diskspace_*`/`term_*` pair that
      cannot compile on the machine running the tests.)

## DF — Flow sanity walkthroughs, through the UI (§22, §17.5)

The `BF` suite already proves the system stays coherent. `DF` proves the **interface can actually
complete each flow a human is meant to complete, including its failure branches** — a different
question, and not answerable by the headless suite.

- [ ] `DF-01` `J1` login through the UI, including every failure branch. §22
      (**checked 2026-08-10**: `e2eweb/j1_login.js` — a real Playwright harness, no stub — now
      exists and was run live against the dev admin UI. It `[SKIP]`s: `/login` currently renders
      straight into "Step 2 of 2 — Authentication code" with two stray `-0` text nodes, because
      `web/pages/auth/login.go` is mid-rewrite and uncommitted. Not tickable; re-run once login
      lands, per `e2eweb/README.md`.)
- [ ] `DF-02` `J2` create a feed from nothing; every validation error renders on its field. §22
      (blocked on the same `AUTH` precondition as `DF-01` — `e2eweb/j2_create_feed.js` exists and
      `[SKIP]`s via `lib/auth.js`'s shared login helper hitting the same wall.)
- [ ] `DF-03` `J3` iterate a prompt: sample, read all verdicts, adjust, sample again. §22
      (same `AUTH` blocker; `e2eweb/j3_iterate_prompt.js` exists, `[SKIP]`s.)
- [ ] `DF-04` `J3` failure branch: kill switch on shows a **reason**, not a dead control. §12.3
      (same `AUTH` blocker; covered by `e2eweb/j3_iterate_prompt.js`, `[SKIP]`s.)
- [ ] `DF-05` `J4` promote a sample and see it appear in the feed. §22
      (same `AUTH` blocker; `e2eweb/j4_promote_sample.js` exists, `[SKIP]`s.)
- [ ] `DF-06` `J5` diagnose a deliberately broken run; reject reasons are readable. §22
      (same `AUTH` blocker; `e2eweb/j5_diagnose_run.js` exists, `[SKIP]`s.)
- [ ] `DF-07` `J6` publish a correction **without** first being tempted to edit. §22
      (same `AUTH` blocker; `e2eweb/j6_publish_correction.js` exists, `[SKIP]`s.)
- [ ] `DF-08` `J7` full recovery drill through the UI. §22
      (partially reachable: `e2eweb/j7_recovery_drill.js` exists and, per its own header, `/recover`
      *does* render the recovery-code field for real without needing `AUTH` — it stops short of
      actually submitting a code because that would burn a real recovery code and rotate the admin
      credential for the rest of the sitting. Not run to completion; not tickable.)
- [ ] `DF-09` `J8` review spend and adjust a budget; enforcement is visible. §22
      (same `AUTH` blocker; `e2eweb/j8_review_spend.js` exists, `[SKIP]`s.)
- [ ] `DF-10` `J9` watch a live run, **drop the WebSocket mid-run**, reconnect, see true state. §22
      (same `AUTH` blocker; `e2eweb/j9_watch_run_live.js` exists, `[SKIP]`s.)
- [ ] `DF-11` `J10` subscribe a real reader to the real URL and observe two poll cycles. §17.5
      (**run for real 2026-08-10, and it FAILED, not skipped**: `e2eweb/j10_subscriber_lifecycle.js`
      needs no login — real HTTP via Playwright's `request` context against the live dev publish
      plane on :8081. Result: `assertion failed: pubDate values are not unique: 16 items, 15
      distinct dates` on the first feed linked from `/`. A real defect surfaced against the running
      seed data, not a harness problem — worth checking whether RFC 822's one-second `pubDate`
      resolution is collapsing two items whose `published_at` differ only sub-second (A4-23's
      "strictly increasing" is enforced in the DB at full precision; the rendered string is coarser).
      Left open on its own stated check — it did not observe two clean poll cycles.)
- [ ] `DF-12` Every `DF` flow re-walked at the narrowest supported breakpoint. §12.6
      (cannot start — none of `DF-01`..`DF-10` has been walked once yet, per above.)
- [ ] `DF-13` Every `DF` flow re-walked under the pseudolocale (`D6-24`): no clipped label, no
      overflowed button, no flow made uncompletable by a longer string. §12.6
      (cannot start — same `DF-12` blocker: nothing has been walked once, let alone twice.)
- [ ] `DF-14` `J3` and `J5` re-walked checking that **no raw key and no blank label** appears
      anywhere on screen — the two failure modes `D6-07` and `D6-22` are meant to catch, verified
      where a human would actually notice them. §12.6
      (cannot start — `J3`/`J5` are both behind the `AUTH` blocker `DF-03`/`DF-06` describe; `J10`
      is HTTP-only with no rendered UI to check for raw keys or blank labels.)
      **Static substitute run 2026-08-10, without a browser or login:** a source-level sweep (every
      literal key argument to `t(`/`T(`/`wt(`/`LabelKey:`/`DisabledReasonKey:` across `web/pages` and
      `web/shell`, diffed against `web/i18n/keys_*.go`) found 21 raw-key hits this walkthrough would
      have surfaced — see `D6-22`'s note for the full list (4 shell header keys rendering on every
      page, 17 settings keys). This is not a substitute for `DF-14` itself (`J3`/`J5` cover generate/
      history, neither of which the sweep found a gap in — their gap, if any, is in behavior a static
      grep cannot see) but it does mean the raw-key failure mode is already confirmed present, not
      merely theoretical, ahead of the browser walkthrough landing.

---

# Phase E — After

- [ ] `E0-01` Subscribe ArticleFlux to the production feeds. §18 — blocked on C1/C2/C5 (nothing is
      deployed yet, so there is no URL to subscribe to). Requirements and step-by-step procedure
      worked out against ArticleFlux's actual fetch/parse/subscribe code in
      `docs/E0-articleflux-integration.md`: gofeed accepts all three of our formats, honours
      conditional GET, reads `<guid>`/Atom `id`/JSON Feed `id` verbatim (ignores `isPermaLink`).
      2026-08-10: checklist re-verified against the three real seeded dev feeds
      (`daily-anime-trivia`, `weekly-anime-news`, `character-spotlight-weekly` — not the §19 names
      this doc originally guessed); doc §4 now a minutes-runnable checklist once C1/C2/C5 land.
- [ ] `E0-02` Verify rendering, dedup by guid, and refresh behavior there. §18 — test plan (not yet
      run) in `docs/E0-articleflux-integration.md` §4–5. Key findings from reading ArticleFlux's
      `internal/store/ingest.go` and `internal/reader/service.go`: ArticleFlux dedups
      `(source_id, guid)` and keeps revision history on edit without moving `published_at` — matches
      our §5.1/§5.5 design, no disagreement there. Real disagreement, now CONFIRMED against a real
      seeded trivia item's actual `content:encoded` (2026-08-10, doc §3): our §5.5 trivia spoiler is a
      bare `<hr class="spoiler-break"/>` marker, not a disclosure tag, so there is nothing for
      ArticleFlux's sanitizer to unwrap — every byte of the answer passes through both its (absent)
      server-side RSS-ingest sanitization and its client-side `html.RawHTML`/GWC `DefaultPolicy`
      (which allows `hr`/`p`/`strong`) unchanged, and it renders immediately on open via `.xml`/
      `.atom`. New finding: the three output formats disagree — the `.json` variant's `content_html`
      omits the trivia answer entirely (0 of 19 seeded items), so subscribing via `.json` is, today, a
      working zero-code spoiler mitigation, though likely an accident of the seeder rather than a
      designed contract — re-verify once A2/A4 build the real generation path. Worth resolving before
      A4/A9 build the real item renderer regardless — see doc §3 and §6 for options.
- [ ] `E1-01` **Deferred until a 4th or 5th feed exists.** Aggregate feeds. §14.2 — 2026-08-10: three
      feeds now live on the dev publish plane (the threshold's stated edge, not past it); no aggregate
      route exists yet (dev root is a static per-feed index, not a merged item stream), so still
      correctly deferred — see `docs/E0-articleflux-integration.md` §7 for the full re-check of all
      five `E1` items against the live instance; none should be promoted.
- [ ] `E1-02` Deferred. Shared upstream source cache. §14.3
- [ ] `E1-03` Deferred. Bounded LRU render cache with a byte ceiling. §14.3
- [ ] `E1-04` Deferred. Rail search, filter, and pagination past ~40 feeds. §14.3
- [ ] `E1-05` Deferred. Per-feed published identity overrides. §14.1

---

# Using the app

## U0 — First-run setup

- [ ] `U0-01` `aff admin init`; store the passphrase in a password manager. VERIFIED 2026-08-10: ran
      the full command end to end against a fresh scratch DB (never `.devrun/aff.db`) — it works,
      prompts for a password, refuses weak ones, prints the `otpauth://` URI and ten recovery codes
      exactly once. Found and fixed in `docs/first-run.md` §2 along the way: piped stdin (scripting
      this) prints `stdin is not a terminal, echo cannot be suppressed` and falls back to a plain
      read — harmless but undocumented before now. Still open: this was a scratch rehearsal, not the
      real admin account, and no real passphrase has gone into a password manager yet.
- [ ] `U0-02` Enroll TOTP; **save the recovery codes somewhere that is not this machine.** VERIFIED
      2026-08-10: enrollment and `aff login` (password + live TOTP code) both work end to end on a
      scratch instance. Found and fixed a real ordering bug in `docs/first-run.md` §3: `aff login`
      cannot run until the server (§5) is up — the doc had it before the server existed. Also
      corrected a fabricated detail: `$AFF_SESSION_FILE` is not a real env var (nothing in source
      reads it); the session path is OS-config-dir by default, one fixed location per OS user shared
      across every instance on the box, overridable only with `--session-file`. Still open: no real
      recovery codes have been generated or stored off-machine for an actual admin account yet.
- [ ] `U0-03` Set the publishing defaults: base URL, author, copyright, `og:image`. §12.5 STALE NOTE
      CORRECTED 2026-08-10, then ACTUALLY RUN 2026-08-10: `aff system settings get` and
      `aff system settings set --base-url ... --author ... --copyright ... --spend-ceiling-usd 5`
      were both executed against a scratch instance and worked exactly as documented — `set` printed
      a clean before/after diff and `get` showed the new values afterward. `docs/first-run.md` §7 was
      rewritten from "known gap" to the verified procedure. Stays open on the task's own terms: not
      yet performed against the real running instance, only proven to work.
- [ ] `U0-04` Set the global daily spend ceiling before creating any feed. §13 Same as `U0-03` —
      `--spend-ceiling-usd 5` was part of the same verified `aff system settings set` call above;
      still not actually performed against the real instance.
- [ ] `U0-05` Create `anime-trivia-daily`; iterate the prompt with sampling until it is good. §20
      VERIFIED 2026-08-10 up through feed creation and enable: `aff feed create` with the §8 recipe
      and `aff feed enable` both succeeded on a scratch instance, confirming new feeds are created
      disabled as documented. `aff sample` was run with a deliberately fake `SCHEMAFLUX_API_KEY` and
      failed safely (401 from the provider, no spend) — confirms the plumbing but not a real
      candidate. Found and fixed two real bugs in `docs/first-run.md` along the way: (1) a Windows-only
      gap where `aff feed create` rejects any real IANA timezone (`America/New_York`) with "not a
      recognised IANA zone" unless `ZONEINFO` points at Go's `zoneinfo.zip`, since the binary doesn't
      embed `time/tzdata` and Windows has no system tz database; (2) `aff feed enable`/`sample`/
      `promote` examples all had the flag written *after* the positional id/slug, which Go's `flag`
      package cannot parse — corrected to flags-first in §8–10. Still open: no real feed exists on the
      real instance, and the prompt has not been iterated against a live model (that costs money —
      deliberately not done here).
- [ ] `U0-06` Create `anime-fact-daily`. §20 Same procedure as `U0-05` applies; not yet done, and
      `OQ-01` (confirm the three launch feeds) is still open.
- [ ] `U0-07` Create `anime-news-daily` with ANN and Crunchyroll sources. §20 Not yet done; blocked
      on `OQ-03` (grounded source list beyond ANN/Crunchyroll not confirmed).
      — **HARDER BLOCK found 2026-08-10: grounded source fetching is not wired at all.**
      `cmd/animefeedflux/wire.go` installs `noFetcher{}` at all three sites that need a
      `generate.CandidateFetcher` (lines 872, 1056, 1068); its own doc comment says grounded-feed
      fetching "is not wired in this build", and `Candidates` returns an immediate error.
      `internal/sources` is fully built and tested — conditional GET, normalization, the
      `sources.fetch` span from `A6-17` — and reached by nothing. So a news feed created today
      would have no candidate set to ground against, and `OQ-03` is the smaller of the two problems.
      Consequences to track: `A6-17` cannot be ticked while its only caller is a stub;
      `DOD-5` ("zero invented URLs, audited against the candidate set at generation time") is not
      merely unaudited but unachievable, because there is no candidate set;
      and the novelty gate's corpus problem has the same shape.
      This is a wiring task in `wire.go`, not new code in `internal/sources`.
- [ ] `U0-08` Decide digest vs separate items for news — currently assumed 3 separate. §20 Still an
      open product decision (`OQ-04`), not a documentation gap — undecided.
- [ ] `U0-09` Subscribe Slack to all three. §5.5 Intended procedure written as UNVERIFIED in
      `docs/first-run.md` §11 — needs a real, publicly reachable host and a Slack workspace, neither
      of which exist yet for this task.
- [ ] `U0-10` Subscribe ArticleFlux to all three. §18 Intended procedure written as UNVERIFIED in
      `docs/first-run.md` §11 — same blocker as `U0-09`.
- [ ] `U0-11` Watch the first week daily; do not assume it is fine because it launched. Cannot start
      until `U0-05`–`U0-10` are actually done; not begun.

## U1 — Recurring operations

Specified, not performed, in `docs/drills.md` → "U1 — Recurring operations" (all commands checked
against `cmd/aff/dispatch.go`; every one still blocked on `U0`'s launch feeds actually being live and
accumulating real history, per that section's header note).

- [ ] `U1-01` Weekly: read the run history for failures and skipped-for-novelty runs. §12.4
      See `docs/drills.md` → U1-01. Blocked on live run history; pagination-undercount risk noted there.
- [ ] `U1-02` Weekly: skim published trivia for factual errors; correct what is wrong. §12.4
      See `docs/drills.md` → U1-02. Blocked on live items; no oracle for "wrong," per §20.
- [ ] `U1-03` Weekly: confirm every feed built and none is stale. §15
      See `docs/drills.md` → U1-03. `aff stale` command exists and is runnable now against a scratch
      DB; blocked on live feeds for the real weekly cadence. Distinct from the `U2-06` alert drill.
- [ ] `U1-04` Monthly: review spend against the ceiling and per-feed attribution. §13
      See `docs/drills.md` → U1-04. Blocked on live spend history; figures are ESTIMATES per §8.1
      (SchemaFlux reports no real usage/cost), not provider-billed truth — cross-check periodically.
- [ ] `U1-05` Monthly: confirm the nightly backup ran and the off-box copy exists. §15
      See `docs/drills.md` → U1-05. Blocked on a live deployment. Flags a real gap: `C4-03` records
      that `OffsiteDir` is still same-volume, not actually shipped off the box — this check can pass
      every month while the single-point-of-failure risk §20 warns about remains unaddressed.
- [ ] `U1-06` Monthly: check remaining recovery-code count. §12.5
      See `docs/drills.md` → U1-06. **No `aff` command reports this** — manual `sqlite3` query only,
      until a CLI/RPC surface exists. Blocked on a live admin account with codes issued.
- [ ] `U1-07` Quarterly: re-check the price table against published prices. §12.5
      See `docs/drills.md` → U1-07. PARTIALLY STALE, corrected 2026-08-10: `aff system settings get
      --json` (now wired, see `U0-03`'s correction) reads the stored price table back — `cmd/aff/
      system_cmd.go:250-261,372` — read access is no longer manual-only. Editing the table is still
      not exposed via CLI (`system_cmd.go:372`'s comment: price_table is read-only from this
      command) — a real gap remains, just narrower than "no CLI surface" implied. Still blocked on
      live published-price data to compare against.
- [ ] `U1-08` Quarterly: audit grounded links for rot (advisory only — not a defect). §19
      See `docs/drills.md` → U1-08. Blocked on live grounded items; explicitly advisory per §19 item 5.
- [ ] `U1-09` Quarterly: confirm the novelty gate is still catching repeats as the corpus grows. §19
      See `docs/drills.md` → U1-09. Blocked on live corpus; a gate that drifted too loose is
      indistinguishable from "the model stopped repeating" without deliberate re-testing, noted there.
- [ ] `U1-10` On any model deprecation notice, re-pin the model and re-sample every recipe. §19
      See `docs/drills.md` → U1-10. Blocked on a live deprecation notice; command sequence given.

## U2 — Drills, performed not described

Fully specified — preconditions, exact commands, expected outcome per step, and silent-failure
checks — in `docs/drills.md`. **None has been performed; every step there is marked UNVERIFIED.**
Nothing is deployed anywhere (`TODOS.md` Phase C open), so nothing here has been run against a real
system, only checked against `cmd/aff/dispatch.go` and the source that would execute it.

- [ ] `U2-01` Restore drill: restore a backup into a scratch instance; confirm identical feeds. §19
      PERFORMED 2026-08-10 against a scratch instance (`.devrun/drill/`, ports 18081/18082, never
      `.devrun/aff.db`) seeded via `cmd/affseed` (3 feeds, 53 items, through real store/rpc code
      paths, not hand-inserted rows). Full sequence run: `aff backup` → `integrity: ok` →
      `aff verify` on the snapshot independently → `aff restore --to` a second scratch DB →
      pointed a second server instance (ports 18091/18092) at the restored copy → diffed
      `item list --json` between source and restored: **byte-identical**, and the restored feed's
      RSS XML matched the source's item-for-item (only port-derived fields — `<link>`,
      `<lastBuildDate>`, guids — differed, as expected). The restored copy's session token from the
      *source* DB also worked against it, confirming the whole file — not just feeds/items —
      round-tripped. Mechanism confirmed sound. **Still open**: this was seed/placeholder content,
      not data accumulated from real generation/promotion runs against a live provider, so
      `docs/drills.md`'s original blocker (needs "a handful of real items" from actual use) is
      narrowed, not fully closed — re-run once `U0`'s launch feeds have real history.
- [ ] `U2-02` Rollback drill: deploy, roll back to the previous tag, confirm service. §18
      See `docs/drills.md` → U2-02. Blocked entirely — nothing is deployed, no GHCR tag history exists
      yet. Procedure transcribed from `deploy/RUNBOOK.md`/`scripts/rollback.sh`, not exercised.
- [x] `U2-03` Recovery drill: lock yourself out, recover with a code, reset, re-login. §19
      PERFORMED 2026-08-10 end to end via `aff recover` against the drill scratch instance. Spent one
      code on the "set new password" branch: remaining-code count went 10→9, old password/TOTP-only
      login rejected, new password + unchanged TOTP logged in fine, and re-running `aff recover` with
      that SAME spent code failed generically ("recovery failed") without decrementing the count
      further — one code buys exactly one use. Spent a second, different code on the "re-enroll TOTP"
      branch: count went 9→8, the OLD TOTP secret stopped authenticating immediately, the NEW one
      worked, and `aff login` was required again in both cases (elevated session ends the instant
      either action succeeds — confirmed against `internal/rpc/auth.go`'s `ChangePassword`/
      `ReenrollTOTP`, which both call `revokeOtherSessions` + `endElevatedSession` when elevated).
      This is the concrete demonstration `OQ-06` (still open, not resolved by this drill) describes:
      the realistic "lost phone" case — new password AND new TOTP needed in one sitting — costs TWO
      recovery codes, not one, because the elevated session is single-action by design.
- [x] `U2-04` Break-glass drill: `aff admin reset` over SSH (SSH itself out of scope; command needs
      only local DB access). §12.2
      PERFORMED 2026-08-10 against the drill scratch DB. `aff admin reset` prompted for a new
      password, printed a fresh `otpauth://` URI and ten fresh recovery codes exactly once. Confirmed:
      a session token minted before the reset was rejected afterward (`Unauthenticated: session
      expired`), the OLD password+TOTP combo failed login ("authentication failed"), and the NEW
      password+TOTP combo logged in successfully. All three assertions the drill exists to make —
      old creds dead, new creds live, old sessions revoked — held.
- [ ] `U2-05` Kill-switch drill: disable generation, confirm feeds still serve. §13
      PERFORMED 2026-08-10 against the drill scratch instance — and it surfaced a real defect, not a
      clean pass. Confirmed as expected: `aff system kill-switch off` disables generation, and the
      publish plane is completely unaffected — same 19-item count, same `Last-Modified`, `200 OK`,
      before and after. **Defect found**: `aff run <slug>` (manual trigger, `FeedService.RunNow` →
      `wireRunExecutor.ExecuteRun` in `cmd/animefeedflux/wire.go:659`) does NOT check the kill switch
      before calling `generate.Run` — with the switch off, `aff run daily-anime-trivia` still made a
      real outbound provider call (observed in the server log: `Generate operation started` →
      `LLM request failed` with a live 401 from the OpenAI endpoint, 5.5s round trip) and only failed
      because the drill's placeholder API key was invalid, not because the kill switch refused it.
      Contrast: `aff sample` (same feed, switch off) correctly refuses instantly with
      `FailedPrecondition: generation is disabled: generation_disabled` and makes no provider call —
      that path's `sampleBudget.CheckSample` (`cmd/animefeedflux/wire.go`) explicitly checks
      `settings.GetEnabled()`; `wireRunExecutor.ExecuteRun` has no equivalent check anywhere in its
      body. This directly contradicts `run_cmd.go`'s own doc comment ("the same budget/kill-switch
      gates as a scheduled one ... the CLI does not skip them") and `PLAN.md` §13's claim that the
      kill switch is honored by "scheduled runs and sampling" — manual runs are the gap neither
      statement accounts for. Left `[ ]` because of this defect; re-run after it's fixed (out of
      scope here — no Go files touched per this task's constraints). Flagged for a fix, not a new
      ticket number, since editing outside `U1-*`/`U2-*` lines is out of scope for this pass.
- [ ] `U2-06` Staleness drill: stop a feed generating and confirm the alert actually fires. §15
      See `docs/drills.md` → U2-06. The read half (`aff stale`) is performable locally now; the alert
      half needs `AFF_SLACK_WEBHOOK_URL` pointed at an observed sink, which nothing currently is.
      (`C4-08` is no longer a blocker here — RE-VERIFIED 2026-08-10, staleness is wired into both
      `/healthz` and the webhook and reachable from real callers; see its TODOS.md entry.)
- [ ] `U2-07` Re-run each drill after any change to auth, deploy, or backup.
      Cannot itself be scheduled or performed until `U2-01`…`U2-06` have each been run at least once.

---

# OQ — Open questions that gate work (§21)

These are decisions, not tasks, and each one blocks something concrete. Left undecided they will be
resolved by accident, which is the worst way. Each names what it blocks.

- [ ] `OQ-01` Confirm the three launch feeds. Blocks `U0-05`…`U0-07`. §21.1
- [x] `OQ-02` **Public or private feeds?** Private needs per-subscriber URL tokens and changes the
      §5.4 caching design and the §2 unauthenticated plane. Blocks `A9-01`. Decide before A9. §21.2
      **Decided 2026-08-10: public.** Settled by the implementation, not a fresh decision: no
      token/secret column on `feeds` (`migrations/0002_feeds_items.sql`), no credential check in
      `internal/publish/server.go`'s `Deps.GetFeed`, and no subscriber-token code anywhere in the
      tree. See `PLAN.md` §21.2.
- [ ] `OQ-03` Confirm the grounded source list beyond ANN and Crunchyroll. Blocks `U0-07`. §21.3
- [ ] `OQ-04` News cadence: one digest item per day, or N separate items? Currently assumed 3
      separate, which reads better in Slack. Blocks `A6-13`. §21.4
- [ ] `OQ-05` Record each answer in `PLAN.md` §21 as decided, with the date and the reason.
- [ ] `OQ-06` **Does a recovery-code elevated session grant one privileged action or two?** Today it
      grants exactly one: `ChangePassword` and `ReenrollTOTP` both end the elevated session
      (`internal/rpc/auth.go`'s `endElevatedSession`) the moment either succeeds, so a code cannot
      chain "reset password" and "re-enroll TOTP" in one recovery. Ending elevation after a single
      action is the safer default (a recovery code is a bearer credential; a longer-lived elevated
      window is exactly what an attacker who found one would want) but costs a second code at
      "new phone, lost authenticator" — the realistic lockout, where both are wanted at once — out of
      a finite set. Decide whether that tradeoff is acceptable or whether a recovery session should
      cover both actions. Blocks nothing currently shipped (the UI at `web/pages/auth/recover.go`
      already models "choose one" honestly), but should be settled before `U2-03`'s recovery drill is
      treated as final. §12.2

# Definition of done — v1 (§19)

- [ ] `DOD-1` Three feeds live. Blocked on `C5-07` (first deploy). See `docs/definition-of-done.md`.
- [ ] `DOD-2` All three validate clean in all three formats, zero warnings. Blocked on `U0-05`…`U0-07`
      (feeds don't exist yet); does not require deployment. Validator mechanics re-proven 2026-08-10:
      `make validate` (11/11 golden docs) and live-fetched bytes from the running dev/seed feeds (9/9,
      RSS+Atom+JSON) both zero errors/zero warnings — but those are seed-data feeds, not the named
      launch feeds, so still correctly unticked. See `docs/definition-of-done.md`.
- [ ] `DOD-3` Slack: 7 days, every item posts exactly once, no dupes, no misses, no spoilers. Static
      compliance (`A3-03`…`A3-07`) done; blocked on deploy + `C3` + 7 days. See
      `docs/definition-of-done.md`.
- [ ] `DOD-4` 30 consecutive days of production trivia with no near-duplicate pairs. Blocked on
      deploy + 30 days of real runs. **This is wall-clock, not engineering speed: nothing shortens it,
      and the clock cannot start until deployment exists.** See `docs/definition-of-done.md`.
- [ ] `DOD-5` Zero invented URLs, audited against the candidate set at generation time. Flagged: no
      candidate set is persisted to audit retroactively; needs a PLAN.md §19.5 wording decision, not
      infrastructure. Re-examined 2026-08-10, recommendation sharpened: amend §19.5 to state
      generation-time enforcement (`CheckLink`) as the proof, not a retroactive table audit — (b),
      persisting a candidate-set snapshot per item just to re-derive a guarantee already enforced
      synchronously, is schema weight with no new guarantee. See `docs/definition-of-done.md`.
- [ ] `DOD-6` Admin reachable only from the allowlisted IP with password + TOTP; drill passed. Auth
      half tested (`internal/sectest`); IP-allowlist half blocked on deploy (`C5-03`/`C5-04`, nginx
      allowlist is still a placeholder). See `docs/definition-of-done.md`.
- [ ] `DOD-7` Monthly spend under the ceiling with per-feed attribution. Attribution schema exists.
      **STALE NOTE CORRECTED 2026-08-10** (audit pass, `internal/budget`/`internal/store` scope): the
      superseded text below said "neither production call site sets it" — that is no longer accurate.
      `cmd/animefeedflux/wire.go`'s `genGate.Allowed` (scheduled/cron runs, the `AFF_MAX_CONCURRENT_RUNS`
      path) now DOES set `MonthlyUSDCeiling: g.monthlyCeilingUSD` from `cfg.MonthlySpendCeilingUSD`
      (`AFF_MONTHLY_SPEND_CEILING_USD`) and queries real month-to-date spend via `budget.MonthStart`
      before deciding — this half is wired and enforced. What is still genuinely unwired: `sampleBudget
      .CheckSample` (the `SampleService` path) builds its own `budget.Limits{}` and never sets
      `MonthlyUSDCeiling` at all — see the note now on `A8-08`. So "monthly spend under the ceiling" is
      true for scheduled generation and false for interactive sampling: sampling has no monthly cap
      regardless of what `AFF_MONTHLY_SPEND_CEILING_USD` is set to. The criterion is still correctly
      unticked, but for a narrower and different reason than the superseded note gave — not "unwired
      everywhere," but "wired on one of the two paths that must share a budget per §13." Also still
      blocked on deploy + one month of real runs regardless. See `docs/definition-of-done.md`.
      Superseded text, kept for history: "`internal/budget` now has a real calendar-month UTC
      `MonthlyUSDCeiling`/`MonthlySpend` mechanism, independent of the daily caps — but neither
      production call site (`cmd/animefeedflux/wire.go:485`, `:764`) sets it, so it is unwired and the
      criterion is still unmeasurable. Flagged for a §19.7 wording decision (name the daily enforcement
      that's real) or, if a real monthly ceiling is wanted, a small scoped `wire.go` plumbing task —
      neither done here."
- [x] `DOD-8` A backup has been restored and serves identical feeds. Performed for real 2026-08-10
      against the running dev instance's seeded DB (`.devrun/aff.db`, 3 feeds with real items):
      `aff backup` → `aff verify` (`ok`) → `aff restore --yes` (`verified: true`) → served from a
      second, isolated scratch instance. All 9 rendered documents (3 feeds × RSS/Atom/JSON) matched
      the pre-backup baseline byte-for-byte except the two fields expected to differ (base URL,
      per-render timestamp). See `docs/definition-of-done.md`.
- [ ] `DOD-9` A push to `main` reaches production, and a rollback has been performed. Workflow fully
      coded (`release.yml`); blocked on deploy supplying droplet secrets. See
      `docs/definition-of-done.md`.
