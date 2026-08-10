# Unreached components — a systematic sweep

Method: enumerated every exported identifier in `internal/`, `web/`, and `gen/aff/v1` (2,233
declarations, deduped to 1,717 unique `(package, name)` pairs), then searched the whole non-test
corpus for real call sites (`Name(` / `.Name(`, not just textual mentions) outside each symbol's
own file. That produced 1,360 func/method candidates in the packages this sweep covers; 88 had at
most one match — the definition line itself, i.e. zero real callers anywhere in production code.
Each of those 88 was read by hand, in context, against `PLAN.md` and the surrounding package, to
separate a real gap from a false positive (interface satisfaction, function-passed-by-reference,
functional options with sane defaults, test-only helpers). The composition roots
(`cmd/animefeedflux/wire.go`'s `runAll`, `cmd/aff/dispatch.go`, `web/main.go`) were read in full to
confirm what they actually wire, not just what they import.

**Counts:** 8 dead-on-arrival, 6 speculative, and roughly a dozen categories of confirmed
false-positive noise (documented below so the next sweep doesn't re-flag them). The most
consequential single finding is **#A1 — the entire `web/ui` component library is unreached**: 1,724
lines of accessible, tested UI primitives that zero pages import, because every page hand-rolls its
own markup instead.

---

## A. Dead on arrival — needs a caller wired

### A1. The entire `web/ui` component library — zero production importers

**What it is:** `web/ui/` (`base.go`, `button.go`, `confirm.go`, `input.go`, `kebab.go`,
`labels.go`, `modal.go`, `responsive.go`, `select.go`, `state.go`, `table.go`, `toast.go`,
`toggle.go` — 1,724 non-test lines, 162 lines of tests) is a full accessible component kit: `Button`,
`Modal`, `Confirm`, `Toast`, `Tabs`, `StatePanel`, `Kebab`, `Input`, `Select`, `Table`, `Toggle`,
`SelectBreakpoint`. `web/ui/base.go`'s own doc comment: *"Node is this package's export of the GWC
render-tree node type, so callers composing these primitives do not also need to import
`GoWebComponents/v5/ui` directly."*

**What it was built for:** a shared, accessible primitive layer under all four pages
(`PLAN.md §12.6` "Conventions"). `web/i18n/adapter.go`'s `NewLabelResolver` exists *specifically* to
feed these primitives' `T` label-resolver signature ("web/ui's primitives ... only ever render
common.* keys") and is itself never called in production for the same reason (only referenced from
comments/doc examples — `grep -rn "NewLabelResolver" --include=*.go .` outside `_test.go` and
comments returns nothing).

**Who should call it:** every page under `web/pages/{auth,generate,history,settings}` — none of
them import `github.com/monstercameron/AnimeFeedFlux/web/ui` at all
(`grep -rl "AnimeFeedFlux/web/ui\"" --include=*.go .` returns zero hits, including test files). Each
page instead imports `GoWebComponents/v5/ui` directly (aliased `ui` — same identifier, different
package, which is what makes this easy to miss by name alone) and hand-rolls markup with
`h.Button`, `h.Input`, `CreateElement`, `UseState`, etc.

**What silently goes wrong in production:** nothing is *broken* — the pages work, because they
reimplement equivalent markup by hand. What's lost is everything the kit was built to guarantee
once, centrally: `web/ui/base.go`'s shared `focusVisible()` treatment ("A11y is not polish here:
every primitive gets this, not just the ones someone remembered to test with a keyboard"), and a
single reviewed implementation of destructive-action confirmation, kebab menus, toasts, and
responsive breakpoints. Any accessibility or interaction bug fixed in `web/ui` never reaches a real
page, because no real page runs that code. This is the largest single instance of "built, tested,
reachable from nothing" in the repo — an entire abstraction layer, not one function.

**Confidence:** high. Verified by import-path grep across the whole repo (not just call-site
matching), including test files.

---

### A2. `llm.Error.Scope` (`ScopeAccount` / `ScopeRecipe`) — never read

**What it is:** `internal/llm/errors.go`'s `Error.Scope` field, set to `ScopeAccount` (bad/exhausted
credential — shared by every feed) or `ScopeRecipe` (this feed's own config is broken) at every
`Fatal`-kind classification site (`errors.go:188,194,214,220,224`; `llm.go:116,129,204`).

**What it was built for:** `PLAN.md §8`, verbatim: *"the error taxonomy below survives only because
**scope** — account-wide versus recipe-scoped — is our business rule about the kill switch, not the
library's concern."* i.e., a `ScopeAccount` failure is supposed to trip the global generation kill
switch (`SystemService.SetGenerationEnabled` / the `settings.generation` row `genGate.Allowed`
checks); a `ScopeRecipe` failure should disable only that one feed.

**Who should call it:** `internal/schedule/runner.go`'s `recordOutcome` (the only place that
currently reads a run's failure and decides what to disable — `internal/schedule/runner.go:474-508`)
or `cmd/animefeedflux/wire.go`'s `genExecutor.Execute`/`wireRunExecutor.ExecuteRun`, which are the
only two call sites that see a `generate.Run` error with an `internal/llm.Error` underneath it.

**What silently goes wrong in production:** `grep -rn "\.Scope\b|ScopeAccount|ScopeRecipe" internal
cmd | grep -v _test.go` shows `.Scope` set in exactly one package (`internal/llm`) and read nowhere
else. The *only* auto-disable mechanism that exists is `schedule/runner.go`'s per-feed consecutive-
failure counter (`WithMaxConsecutiveFailures`, default 5), which is blind to scope. A revoked or
rate-limited API key — the textbook `ScopeAccount` case — does not trip the global kill switch at
all today: instead, every enabled feed independently burns through its own 5 consecutive failures
(each one a real, doomed LLM call against the same broken credential) before disabling itself, one
feed at a time, over however many cron cycles that takes. The design in `PLAN.md §8` — one bad key,
one immediate global stop — does not happen; the system instead keeps spending budget hitting a
credential everyone already knows is dead.

**Confidence:** high on "never read" (confirmed by full-repo grep); high on the intended behavior
(quoted directly from `PLAN.md §8`); the specific fix (where exactly to call `SetGenerationEnabled`
equivalent) is a design decision, not verified against a written spec beyond the quoted sentence.

---

### A3. `obs.WithRunID` / `obs.WithRequestID` — never called; every log line is missing the identifier that makes it traceable

**What it is:** `internal/obs/obs.go:28,33` — context-enrichment functions meant to attach a
generation run ID or HTTP request ID to `context.Context`, which `contextHandler.Handle`
(`obs.go`, same file) then copies onto every `slog` record automatically.

**What it was built for:** `PLAN.md §15.0a`: *"Logs join traces. `trace_id` and `span_id` go onto
every log record from the active span... This is the one integration worth the effort; traces and
logs that cannot be correlated are two tools and half the value."* `run_id`/`request_id` are the
non-OTel half of that same correlation contract (`obs.RunID(ctx)` / `obs.RequestID(ctx)` are the
readers, both only ever invoked internally by `contextHandler.Handle` reading a context nothing
populated).

**Who should call it:** `internal/generate/runner.go` (wrap `ctx` with `obs.WithRunID` once a run
row exists — right where `recordRunMetrics` already fires) and `internal/publish/server.go` /
`internal/rpc/interceptor.go` (wrap the inbound request context with `obs.WithRequestID`).

**What silently goes wrong in production:** `grep -rn "WithRunID|WithRequestID|obs\.RunID|obs\.RequestID" --include=*.go . | grep -v _test.go`
shows the functions are only ever *defined*, never invoked outside `internal/obs` itself. Every log
line this system ever emits is missing `run_id` and `request_id` — an operator debugging a slow or
failed run from the logs (the exact incident `§15.0a` describes: *"a slow trace leads directly to
the lines it produced"*) has no way to filter logs down to the one run in question. The logging
pipeline was built and works; the one value that would make it useful during an incident is never
attached.

**Confidence:** high.

---

### A4. `obs.Metrics.RecordItemsPublished` — `aff_items_published_total` never recorded

**What it is:** `internal/obs/metrics.go:353`. Increments `aff_items_published_total{feed_slug}`.
Has its own passing tests (`metrics_test.go:85`).

**What it was built for:** `PLAN.md §15.0a`'s metric list names it explicitly:
`aff_items_published_total{feed_slug}` / `aff_items_rejected_total{feed_slug,reason}`.

**Who should call it:** `internal/generate/runner.go`'s `recordRunMetrics` (`runner.go:924-947`),
right beside the `RecordRun`/`RecordTokens`/`RecordCost` calls it already makes from the exact same
`RunRecord` — the item count is available at that call site (`ItemsAdded: len(items)`,
`runner.go:293`) but never passed through. The sibling function `recordItemRejected`
(`runner.go:958`) *is* wired via `RecordItemRejected` — this is the missing half of the same pair.

**What silently goes wrong in production:** the dashboard's "is generation working, and how much is
it producing" metric — the companion to `aff_items_rejected_total`, which does work — reads zero
forever. An operator watching `aff_items_published_total` for a stalled-but-not-crashing feed (one
that runs "successfully" but keeps rejecting every candidate) sees a flat line and has no metric-only
way to tell "publishing zero items" from "not running at all."

**Confidence:** high (confirmed against every other `Record*` method — 7 of 9 are wired from
production code; this one and A5 are the exceptions).

---

### A5. `obs.Metrics.RecordProviderError` — `aff_provider_errors_total{kind}` never recorded

**What it is:** `internal/obs/metrics.go:456`. Increments `aff_provider_errors_total{kind}`.

**What it was built for:** `PLAN.md §15.0a`: `aff_provider_errors_total{kind} — the §8 taxonomy,
counted`. The "kind" is exactly `internal/llm.Error.Kind` (`Transient`/`Invalid`/`Fatal`), which
`internal/generate/runner.go:862-869`'s `errorKindFromProvider` already extracts as a string for
`RunRecord.ErrorKind` — the value is computed at the right place, just never forwarded to the
metric.

**Who should call it:** the same call site as A2/A4 — wherever `generate.Run`'s provider-call
failure is classified (`errorKindFromProvider`), a `RecordProviderError(ctx, kind)` call is missing.

**What silently goes wrong in production:** the one metric `§15.0a` calls out as specifically
answering "is the LLM provider itself healthy" never increments. Provider outages, rate-limiting,
and account problems (A2) are invisible on this metric even though every one of them is already
classified in memory at the moment it happens.

**Confidence:** high.

---

### A6. `SampleServer.CommitRunCalls()` — a poison-pill safety canary that nothing reads

**What it is:** `internal/rpc/sample.go:176`. Its own doc comment: *"reports how many times the
sample pipeline's poison-pill CommitRun fired... Exported (rather than a test-only accessor) so an
operator wiring metrics can alert on a nonzero value in production too — it should never happen, and
if it ever does, that is the worst bug this design can produce."*

**What it was built for:** `Sample`/`SampleStream` are supposed to be read-only/ephemeral — never
committing a row to the `runs` table the way a real generation does. This counter is the intended
detector for "that invariant broke": if a sample ever accidentally persists like a real run, budget
and cost tracking silently double-count, and this method exists precisely so that can be alerted on.

**Who should call it:** `cmd/animefeedflux/wire.go`'s `buildControlPlane`, once per admin-listener
tick or via a metric gauge fed by `obs.Metrics` (whatever mechanism the rest of `§15.0a`'s metrics
use) — currently nothing calls it at all, in or out of tests-with-assertions
(`grep -rn "CommitRunCalls" --include=*.go .` finds only its own definition).

**What silently goes wrong in production:** by design, this should never fire — but if the one bug
this whole method exists to catch ever happens, there is currently zero alerting path. The canary is
built and armed and connected to nothing.

**Confidence:** high (confirmed zero references anywhere, including tests).

---

### A7. `store.PurgeExpiredSessions` — the sessions table has no purge path at all

**What it is:** `internal/store/auth.go:664`. Its own doc comment: *"an unbounded sessions table is
still a table that should not grow forever on a single-admin system that logs in daily."*

**What it was built for:** housekeeping for the `sessions` table, parallel to the nightly retention
job (`internal/ops/prune.go`) that already prunes samples, embeddings, and old runs.

**Who should call it:** `internal/ops/prune.go`'s `Prune` — but note `prune.go`'s own header is
explicit that *only* three things are pruned there ("Three things are pruned and nothing else...")
and sessions is not one of them, so this needs either a fourth stage added to `Prune`/`PruneOptions`,
or a separate call from `ops.Scheduler`'s nightly job in `cmd/animefeedflux/wire.go`.

**What silently goes wrong in production:** the table grows without bound for the life of the
deployment. On a single-admin system with one login a day this is a slow leak, not an emergency —
but on the 2 GB droplet `PLAN.md §15` describes, "slow and silent" is exactly the failure mode the
plan is written to avoid elsewhere (staleness watchdog, backup alerting). Nothing will ever notice
this one growing until someone happens to look at the file size.

**Confidence:** high.

---

### A8. `store.ListAuthEvents` / `store.RecentFailures` — a write-only audit trail

**What it is:** `internal/store/auth.go:708` (`ListAuthEvents`, doc comment: *"the audit trail
newest-first, for the security pane / login-hardening review"*) and `auth.go:747`
(`RecentFailures`, doc comment: *"the query the per-IP exponential backoff (§4) reads on every login
attempt"*).

**The write side is fully wired and running:** `internal/rpc/auth.go` calls
`store.RecordAuthEvent(...)` at ~19 call sites — every login attempt, TOTP check, recovery, password
change, and re-enrollment, success or failure, is durably recorded. This is exactly the
`item_embeddings` shape the task brief calls out: data faithfully written forever.

**The read side has zero callers anywhere:**
- No RPC method exposes it — `proto/aff/v1/auth.proto`'s 10 `rpc`s (`Login`, `RecoverWithCode`,
  `Logout`, `Session`, `ChangePassword`, `ListSessions`, `RevokeSession`, `RevokeAllSessions`,
  `ReenrollTOTP`, `RegenerateRecoveryCodes`) have no audit/history method.
- No UI reads it — `web/pages/settings/render_security.go` shows active sessions via
  `AuthService.ListSessions` and lets you revoke them, but has no login-history/audit view.
- Production auth actually rate-limits via `internal/rpc/auth.go`'s in-memory `backoffTracker`
  (`auth.go:1027-1075`), not via `RecentFailures` reading `auth_events` from the database — so the
  persistent, restart-surviving version of per-IP backoff the doc comment describes was built but
  never wired; what runs instead resets on every process restart or deploy.

**What silently goes wrong in production:** the "security pane / login-hardening review" the doc
comment names never shipped — the audit table accumulates every credential attempt forever with no
way to view it short of opening the SQLite file directly, and the operator-facing promise ("you can
review login history") is unmet. Separately, a deploy or crash-restart during a brute-force attempt
resets the in-memory backoff counter to zero, silently undoing whatever throttling had accumulated —
a `RecentFailures`-backed check would have survived that restart.

**Confidence:** high on "never read" (full-repo grep, both symbols, zero non-test/non-doc-comment
hits); high on the write side being live (19 call sites read directly).

---

## B. Speculative — recommend deletion

### B1. `store.PruneExpiredSamples` — superseded duplicate

`internal/store/samples.go:164`. The nightly prune job that actually runs
(`internal/ops/prune.go:pruneExpiredSamples`, lowercase, package-private) reimplements the identical
`DELETE FROM samples WHERE expires_at ...` query independently, with a subtly different boundary
(`<=` in the store method vs `<` in the ops one) — two copies of the same SQL that can silently
disagree at the boundary instant. The store method is otherwise unused. Recommend deleting the store
method (or, if kept, making `ops.Prune` call it instead of its private duplicate, closing the
boundary-drift risk).

### B2. `store.ListRuns` — superseded duplicate

`internal/store/runs.go:512`, doc comment: *"the same ordering the history page ... wants."* But
`RunService.History` (`internal/rpc/run.go:532`) needs filtering by feed/status/date-range and
cursor pagination that `ListRuns` doesn't provide, so it was reimplemented directly against `runs`
with its own query rather than calling `ListRuns`. `ListRuns` is now a simpler, unused, dead
alternative. Recommend deletion unless a caller that genuinely wants the unfiltered form appears.

### B3. `store.Heartbeat` — built for a periodic-liveness need the design doesn't have

`internal/store/runs.go:201`. Meant to periodically renew `heartbeat_at` on a long-running run so a
stale-run watchdog can tell "still working" from "crashed." But `ReclaimStaleRuns`
(`runs.go:435`) is only ever invoked once, at process boot (`cmd/animefeedflux/server.go:51`), never
on a running interval — so there is no live watchdog for `Heartbeat` to report to, and the design as
built doesn't need it. Legitimately speculative: keep only if a periodic (not boot-only) stale-run
check is added later.

### B4/B5. `web/pages/generate.JitteredRuns` / `FormatNextRuns` — self-documented, already-known gap

`web/pages/generate/logic.go:226,237`. Both `render_editor.go:158` and `render_rail.go:115` already
carry the comment explaining why: *"why this pane cannot compute them today (no nominal
next-fire-time is available from any RPC in proto/aff/v1)."* The UI shows a static
`generate.editor.nextRunsUnavailable` message instead. Not a new discovery — this repo already knows
about it — but flagged here so it's on record as the concrete unreachable code, not just a comment:
either delete `JitteredRuns`/`FormatNextRuns` or add the missing proto field
(`FeedServiceGetResponse`'s next-fire-time) and wire them in.

### B6. `web/pages/generate.SumCandidateCostUSD` — built, never displayed

`web/pages/generate/logic.go:283`, doc comment: *"for 'cost per sample' (PLAN.md §12.3) once a
sample has actually run."* `SampleCandidate.GetEstimatedCostUsd()` is populated server-side and the
summing helper exists, but `render_sampler.go` never calls it — the post-sample "what did that
actually cost" total that §12.3 asks for is never shown; only the pre-sample estimate
(`EstimateSampleCostUSD`) renders. Low-effort fix (call the existing function in the render path) —
listed here as dead-on-arrival-adjacent rather than under A because it's UI-completeness, not a
silent operational gap.

### B7. Grounded-feed vertical — accepted, self-documented out-of-scope

`internal/generate/grounded.go`'s `BuildCandidateBlock`, `RankingSystemPrompt`,
`DegradeOnSourceFailure`, and `internal/sources.Fetcher.FetchCandidates` (the real fetcher
implementation) are all unreached. `cmd/animefeedflux/wire.go`'s `noFetcher` stub says so directly:
*"Wiring the real internal/sources pipeline ... is a substantial feature in its own right and out of
scope for this change; a grounded feed configured in this build fails loudly and legibly at
generation time instead."* Not a new finding — recorded here only so a future sweep recognizes this
cluster as already-known and doesn't re-report it as newly discovered.

---

## C. Legitimately unreferenced — do not re-flag

- **`gen/aff/v1` protobuf getters** (118 zero/near-zero matches in the raw sweep, e.g.
  `Item.GetDeletedAt`, `Run.GetHeartbeatAt`, `AuthServiceLoginRequest.GetPassword`). Spot-checked
  several: production code in this repo overwhelmingly reads fields directly (`resp.Field`) rather
  than through `Get*()` accessors, which is why the accessors show no call sites. This is normal for
  generated protobuf code — the getters exist for nil-safety and interface completeness, not because
  every one is expected to be called by name. Confidence: medium (spot-checked, not exhaustively
  verified field-by-field).
- **`internal/testutil`, `internal/flowtest`, `internal/llm.Fake`** — test-support packages.
  Correctly invoked only from `_test.go` files; that is their entire purpose.
- **`internal/config.Secret.GoString` / `.LogValue`** — implicit interface satisfaction
  (`fmt.GoStringer`, `slog.LogValuer`). Invoked via reflection by `fmt`/`slog` when a `Secret` value
  is formatted or logged directly, which grep cannot see. Confidence: medium-low — did not trace an
  actual `%#v` or `slog.Any("secret", cfg.SecretKey)` call site, so this is a plausible false
  positive rather than a confirmed one; flagging so it isn't mistaken for a real gap.
- **Functional options with sane defaults** — `schedule.WithRunTimeout`,
  `WithMaxConsecutiveFailures`, `WithShutdownTimeout`; `rpc.WithGetenv`, `rpc.WithPasswordPepper`.
  All have working defaults set in their constructors (`schedule.New`, `rpc.NewAuthServer`,
  `rpc.NewSystemServer`) and are exercised only by tests that want a non-default value. `auth.go`'s
  own doc comment on `WithPasswordPepper` confirms production instead reads
  `AFF_PASSWORD_PEPPER`/`AFF_PASSWORD_PEPPER_VERSION` from the environment directly inside
  `NewAuthServer` — this is the "pepper" item from the original ten, and it is already resolved, not
  a new gap.
- **`web/pages/history.RunsTab` / `ItemsTab` / `ItemForm` / `TypedConfirm`** — false positives from
  the mechanical `Name(`/`.Name(` sweep. These are GWC components invoked by function reference —
  `ui.CreateElement(RunsTab, RunsTabProps{...})` (`web/pages/history/root.go:60`) — which the
  call-site regex doesn't match. Confirmed reachable; not a finding.
- **`ops.Scheduler`, `obs.Setup`** (two of the original ten) — both are fully wired in
  `cmd/animefeedflux/wire.go`'s `runAll` today (`obs.Setup` at line 1104, `ops.NewScheduler` +
  `opsScheduler.Run(runCtx)` at lines 1241-1305). Already fixed; not re-flagged.
- **`item_revisions`, the i18n Provider, the admin UI's page registration** (three more of the
  original ten) — verified fixed: all four page packages (`auth`, `generate`, `history`, `settings`)
  self-register via `init() { shell.RegisterPage(...) }`, and `web/shell/route_registration_test.go`
  blank-imports all four to assert exactly this.

---

## Notes on method and confidence

- The mechanical sweep (1,717 symbols, cross-package call-site search) has two known blind spots,
  both handled by manual review rather than trusted raw: (1) function-passed-by-reference (`Foo,`
  not `Foo(`) — caught the `RunsTab`-style false positives above; (2) implicit interface
  satisfaction / reflection dispatch (`GoStringer`, `slog.LogValuer`, and any `affv1.*ServiceServer`
  interface satisfaction) — flagged as lower-confidence rather than asserted.
- Every finding in section A and B was verified by reading the actual defining file and its doc
  comment, and by grepping the *whole* repository (not just the sweep's package list) for the
  symbol name, to rule out a caller living outside `internal/`/`web/`/`gen/` (e.g. `cmd/`, `scripts/`).
- Every `PLAN.md §N.M` citation above was checked against a real heading in the 1,974-line
  `PLAN.md` at the time of this sweep (§8, §12.3, §12.6, §15.0a).
