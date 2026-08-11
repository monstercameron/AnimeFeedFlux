# Session findings — 2026-08-10

What one day of ~40 subagents found, written for whoever decides where to spend the next day. Not a
celebration: several of these are still open, several "fixes" are unverified in a browser, and one
finding (the tick audit) says the project's own bookkeeping cannot be trusted at face value.

Sources: `git log --oneline -15`, `DEVLOG.md`, `docs/unreached-components.md`,
`docs/definition-of-done.md`, `docs/browser-verification-plan.md`, `docs/spoiler-design.md`,
`TODOS.md` (1356 lines, 693 task lines, 535 `[x]` / 157 `[ ]` as of this writing), and `PLAN.md`
(1987 lines, headings §1–§22).

---

## 1. The defect taxonomy: "built, tested, reachable from nothing"

The task brief's working list named twelve items. Checked against the tree, two of the twelve are
the *same finding* counted from before and after its fix (reset tokens / recovery CLI path — see
below), so the real count of distinct instances is **eleven**, and of those eleven, **seven are now
fixed** in this session's later commits, **three are partially fixed**, and **one is still fully
unreached**. The corrected table:

| # | Finding | Status as of this write-up |
|---|---|---|
| 1 | Pepper (`internal/auth/pepper.go`) | **Fixed** — `acffbc7`. `HashPeppered`/`VerifyPasswordPeppered` now apply the pepper to the argon2id *output* as `pepper.go` always documented, and `internal/rpc/auth.go`'s Login/ChangePassword/reset paths call through them (`SEC-07`, `TODOS.md:404-409`). |
| 2 | Reset tokens / recovery CLI path | **Fixed, and these are one finding, not two.** `internal/rpc/auth.go`'s `IssuePasswordResetToken`/`CompletePasswordReset` are deliberately *not* a proto RPC (`SEC-31`, §12.2's reachability decision) and are now called from `cmd/aff/admin_cmd.go`'s `cmdAdminResetPassword` — the "recovery CLI path" is exactly the caller that closed the "reset tokens have no caller" gap. `internal/rpc/auth_test.go`'s `TestPasswordResetNotOnGRPCSurface` enforces the RPC stays absent on purpose. |
| 3 | Off-box backup encryptor | **Partially fixed.** `internal/ops/schedule.go`'s `shipOffsite` now calls `encryptBackupFile` after every nightly backup and alerts on failure/misconfiguration (`C4-05`) — the encryptor has a real caller now. What is still true from the original finding: `OffsiteDir` is a same-volume local directory; no network transport (upload/rsync/S3/scp) exists anywhere in `internal/ops` or `cmd/aff` (`C4-03`, `TODOS.md:722-726`). Judged strictly, the copy still never leaves the box. |
| 4 | `item_revisions` reads | **Partially fixed.** `ItemService.ListRevisions`/`RevertRevision` now exist server-side (`internal/rpc/item.go:1236,1377`) and are wired into `web/wsconn/clients.go`'s guarded client — but `web/pages/history` never calls them (`D3-15`, `TODOS.md:998-1003`). The revision diff/revert UI that exists uses a session-local client-side snapshot workaround instead, so revert only survives the current browser tab, not a page reload. |
| 5 | Novelty gate's corpus (`item_embeddings`) | **Fixed** — closed in `0788372` ("Connect the engine: the pipeline had parts that reached nothing"). `A5-11`/`A5-12` (span + rejection metric) are both wired into `filterNovel` now; nothing in `TODOS.md`'s A5 block is still open on this point. |
| 6 | i18n `Provider` | **Fixed.** `D0-21`: mounted in the root component, above the router. |
| 7 | Admin UI's page registration | **Fixed.** All four page packages self-register via `init() { shell.RegisterPage(...) }`; `web/shell/route_registration_test.go` blank-imports all four to assert it (`docs/unreached-components.md` §C). |
| 8 | `ops.Scheduler` | **Fixed.** Wired in `cmd/animefeedflux/wire.go`'s `runAll` (`ops.NewScheduler` + `.Run(runCtx)` at lines 1241-1305 per `docs/unreached-components.md`). |
| 9 | `obs.Setup` | **Fixed.** Called at `wire.go:1104`. |
| 10 | `web/ui`'s entire component library | **Still fully unreached.** `docs/unreached-components.md` finding A1, high confidence: 1,724 non-test lines, zero production importers (`grep -rl "AnimeFeedFlux/web/ui\""` across every page, including test files, returns nothing). Every page instead imports `GoWebComponents/v5/ui` directly under the same local alias (`ui`), which is what made this easy to miss by name. The shared `focusVisible()` a11y treatment and the one reviewed destructive-confirmation implementation never reach a real page. |
| 11 | `tokens.Emit()` | **Fixed as literally scoped, not as a product outcome.** Now called once, in `web/shell/app.go:152`, before first render (`D0-26`). But `D0-24`/`D0-25` remain open: of **103 unique `af-*`/`history-*` classes** the pages emit, only `web/ui`'s ~38 shared-primitive rules and a further ~42 rules now in `web/shell/styles.go` + `web/pages/auth/styles.go` have a matching typed rule — the majority of the admin UI's classes still have no CSS rule reading a token at all, so `Emit()` running doesn't mean the product looks token-driven. |

**Two items dropped from the original twelve as the same finding counted twice:** #2 above. **Net
corrected count of genuinely distinct "reachable from nothing" instances found this session: 11**,
not 12; **of those, 1 remains fully unreached today** (`web/ui`), **3 are partially fixed** (off-box
transport, item-revisions UI wiring, token-driven styling coverage), and **7 are fully closed.**
`docs/unreached-components.md`'s own tally (a systematic, not ad hoc, sweep) adds one more still-open
instance on top of the above list — **A1 above is that same finding**, restated here in the
brief's original vocabulary rather than as a new twelfth item.

### Why unit tests never caught any of them

Every one of the eleven passed its own tests. A unit test calls the function directly — it cannot see
an absent *caller*, because the caller is not part of what the test exercises. `DEVLOG.md`'s framing
(2026-08-10, "Seven things...") states the mechanism plainly: "tested" was read as "working" seven
times running, and each time the gap was invisible until something existed that would actually have
had to call the code — a composition root, an adversarial suite, or someone reading `TODOS.md`
against the tree instead of against the last time it was ticked.

The check that would have caught these is not a better unit test — it is a **reachability sweep from
a real entry point**: `docs/unreached-components.md`'s method (enumerate every exported identifier,
search for a call site outside its own file, read the ~5% with zero matches by hand against `PLAN.md`)
is exactly that, and it is what found the twelfth (`web/ui`, A1) after the first seven were found one
at a time by tracing individual suspicions. The cheaper, permanent version of the same check: make the
composition root the only place a new package is allowed to matter — "wire the call unconditionally,
not as an opt-in step a future composition root can forget," which is literally the shape `DEVLOG.md`
records the fixes taking (`ServeHTTP` sets `Session.Token` unconditionally; `web/main.go`'s `Mount`
takes a wire callback invoked before route registration, not after).

---

## 2. What only running it found

Two classes of bug in this repository are structurally invisible to `go test ./...`: things that
depend on a real composition root (§1 above), and things that depend on a real browser or a real
network socket, which `go test` never opens. Four concrete instances, all found and fixed the same
day the binary was run for the first time (`DEVLOG.md`, "2026-08-10 — The server ran for the first
time, and nothing could log in"):

- **Nothing could log in, by either interface, for two unrelated reasons that produced the same
  symptom.** The CLI (`cmd/aff`) dialled `AFF_ADMIN_ADDR` with a real `grpc.NewClient`, but the admin
  listener served only the bridge's `http.Server` — no `*grpc.Server` was bound anywhere in the tree.
  Every CLI command died at the transport, before any RPC dispatched, and `aff login` printed `aff
  login: authentication failed` — byte-identical to what a genuinely wrong password produces, because
  `client.go`'s `isUnreachable` deliberately treats a non-status transport error as a credential
  rejection (PLAN.md §12.1 forbids distinguishing failure causes in the generic case, on purpose, as
  the safe default against an account-existence oracle). The one thing that would have proven the RPC
  never arrived — `auth_events` staying empty through every attempt — took reading the database
  directly to notice; the CLI's own output gave no hint.
- **The browser could not upgrade the WebSocket without a cookie it could only obtain through that
  socket.** `bridge.NewServer`'s `ServeHTTP` validates the session cookie *before* the WebSocket
  upgrade, with no exemption for `AuthService.Login` — confirmed against
  `internal/bridge/server_test.go`'s own `TestUpgradeWithoutCookie_Unauthorized`, which proves the
  behavior and does not special-case Login either. §4 forbids the session token ever reaching
  JS/WASM-readable memory, which is why Login was designed to answer via a header a real `*grpc.Server`
  attaches with `grpc.SetHeader`, not a message field a WebSocket frame could carry — but the plain
  HTTP login endpoint that header design requires (`§4`: `__Host-` cookie, `HttpOnly`, `Secure`,
  `SameSite=Strict`) had never been implemented anywhere. `internal/e2e`'s own test harness
  (`internal/e2e/app.go`) had already documented the exact gap by routing around it — opening a
  *second*, non-bridge `*grpc.Server` just to call Login, with a comment naming it outright as "a real
  production gap and not a shortcut this suite invented" — and that comment sat there, correct and
  ignored, while the suite stayed green, because a workaround inside a test proves the workaround
  works, not that the gap it routes around is closed.
- **A modal rendered with `hidden` plus a competing `display` swallowed every click on the page.**
  `web/shell/expiry.go`'s session-expiry modal originally used `h.Show`, which hides via the `hidden`
  *attribute* — that only works while nothing overrides it with a CSS `display` of its own, and
  `af-expiry-modal--visible` set one. The result: a full-screen fixed overlay, invisible, still laid
  out, that intercepted every pointer event — the login form could not be clicked at all, on a fresh
  load, for a visitor who had never had a session. `expiry.go:40-57`'s own comment records how it was
  found: "No unit test could see it — the node was present and correct in the tree, and only a real
  browser resolves `hidden` against a competing `display`. A headless Playwright click found it in one
  run." The fix is now in place: a closed modal renders nothing at all (`h.Fragment()`) rather than a
  hidden node.
- **The UI had a large gap between classes emitted and rules defined.** `D0-25`'s count: **103 unique
  `af-*`/`history-*` class names** emitted across the pages and shell; as of the sweep, **zero** had a
  matching typed CSS rule anywhere outside `web/ui`'s ~38 shared-primitive rules. Since then, ~42 rules
  have landed in `web/shell/styles.go` and `web/pages/auth/styles.go` (this session, alongside the
  hidden/display fix above) — narrowing but not closing the gap; `web/pages/generate`,
  `web/pages/history`, and `web/pages/settings` still have no `styles.go` of their own as of this
  write-up (`D0-24`/`D0-25` remain open). Each of these was invisible to a green test suite because
  `go test ./...` never runs anything under the `js && wasm` build tag — `docs/browser-verification-plan.md`
  counts **61 unverified behaviours** across 44 such files, of which only these two (the modal, and
  the general styling gap) have actually been exercised against a real browser session so far. The
  rest of that document's Part 2 ranking — no live region on the DISCONNECTED banner, and unverified
  controlled-input clobber risk on the auth forms — are read findings, not yet run findings; they are
  flagged, not fixed.

---

## 3. The over-marking finding

`TODOS.md` states its own stated failure mode as "silent under-marking." This session found the
opposite is real, and it happened on **six items across two audits**, all re-opened 2026-08-10 with
the finding recorded inline rather than the checkbox simply flipped:

- `A9-20`, `A9-21`, `A9-22`, `A9-23`, `A9-24` — publish-plane tracing spans/metrics (§15.0a). Re-audit
  found `internal/publish` has no `obs.Start` call anywhere (`grep -n "obs.Start" internal/publish/*.go`
  is empty) despite `A9-20`/`A9-21`/`A9-23` claiming a span exists; `A9-22`/`A9-24` had a *caller* for
  the metrics/log helpers but the composition root (`buildPublishPlane` in `wire.go`) never sets
  `Deps.Metrics` or `Deps.Logger`, so the calls exist and run against nil-guards or `slog.Default()`
  and never reach the app's real metrics registry or structured logger.
- `B2-06` — verifying `SampleStream`/`RunService.Watch` stream through the real bridge (§11). Re-audit
  found `SampleStream`'s only test uses a `fakeStream` explicitly documented as "without a network
  connection," and `RunService.Watch` has no bridge test at all (`internal/flowtest/j9_watch_test.go`
  skips both BF-40/BF-41 for exactly this reason).
- `D0-11`, `D0-12` — design tokens and light/dark handling (§12.6). Re-audit found the tokens exist in
  `web/tokens` but the shipped product mostly doesn't source from them (the 103-classes gap in §2
  above), and `tokens.Emit()` itself had zero callers until this session wired it — so "decided" was
  never actually true; it was built and never connected to anything a browser would render.

**Why an over-marked task is worse than an unmarked one.** An unticked box still reads as unfinished
— it gets looked at again the next time someone scans the list, because its own appearance invites
scrutiny. A ticked box against work that was never actually done reads as *settled*: nobody re-examines
a checked box on the strength of the tick alone, and `TODOS.md`'s own rule ("Do not mark a task done
on the strength of code existing. Done means its stated check passes.") only helps if someone actually
re-runs the check — which is exactly what this pass did and prior passes had not. The unmarked failure
mode delays discovery; the over-marked one *hides* it, actively, behind a signal that specifically
means "you don't need to look here." That is a strictly worse failure because the cost of catching it
is higher (it takes an audit that starts from reading the code, not from trusting the last tick) and
the cost of missing it compounds silently — every task planned on top of a false "done" (e.g., anyone
who read `A9-20..24` and assumed publish-plane traces existed, and built an incident runbook around
them) inherits the error without any signal that they should check.

---

## 4. What this implies for the remaining work

`TODOS.md` currently: **535 of 693** task lines ticked `[x]`, **157 open**. That count moved from
"397 -> 417 of 688" (recorded at the `a531dd7` tick-audit commit, mid-session) to its current state
across three further commits (`0788372` engine wiring, `acffbc7` pepper, `088206c`/`3787c5f` admin UI
build-and-serve) — the open count is not just shrinking, its composition is shifting: what's left is
increasingly *not* code.

**`docs/definition-of-done.md`'s own tally is the sharpest instrument for this.** Of the nine
top-level definition-of-done checks (§19):

- **Satisfied: 0.**
- **Satisfiable now, purely locally, no deployment needed: 2** — `DOD-8` (restore drill — the
  mechanics are unit-tested; the actual drill of standing up a scratch instance and diffing output has
  never been run) and `DOD-2` (create the three named feeds locally and run `make validate` against
  real rendered output — does not require the feeds to be publicly served).
- **Blocked on the single production deployment (`C5-01`…`C5-09`, all unticked) plus, for two of them,
  elapsed time afterward: 7** — `DOD-1` (feeds live), `DOD-6` (IP-allowlist half only — the auth half
  is fully drilled at the unit level), `DOD-9` (rollback performed), `DOD-3` (7 consecutive days of
  Slack delivery once deployed), `DOD-4` (30 consecutive days of production trivia with no
  near-duplicate pairs — explicitly not provable by the existing canned-corpus novelty harness,
  because that proves the mechanism works, not that a live model run daily for a month won't repeat
  itself), plus `DOD-5` and `DOD-7`, which additionally need a **wording decision** independent of
  infrastructure (§19.5's literal "audit over the full item table" is unachievable under the current
  schema — the system enforces link integrity synchronously at generation time and never persisted a
  retrospective candidate-set snapshot to audit later; §19.7 names "the configured ceiling" — **update,
  later 2026-08-10: a real monthly ceiling now exists and is wired.** `AFF_MONTHLY_SPEND_CEILING_USD`
  (`internal/config.Config.MonthlySpendCeilingUSD`) feeds `cmd/animefeedflux/wire.go`'s `genGate`,
  which every scheduled run passes through, and sets `budget.Limits.MonthlyUSDCeiling` for real —
  see `docs/definition-of-done.md`'s DOD-7 section and `TODOS.md` DOD-7 for the corrected read. The
  gap narrowed to: `sampleBudget.CheckSample` (interactive sampling) still builds its own
  `budget.Limits{}` without a monthly ceiling, and the production env file has no example of the new
  variable to set — not a "no monthly concept exists" problem anymore, a "one of two budget call
  sites, and one deploy-config line" problem).

**The honest fraction:** of the seven blocked items, six share one root cause — nothing has ever been
deployed (`CHANGELOG.md`: "none of it is running anywhere... no staging host, no production deploy";
`TODOS.md`'s `C5-*` block is entirely unticked, including the still-placeholder nginx admin allowlist
at `deploy/nginx/admin.anime.earlcameron.com.conf:33`, `allow 203.0.113.0/24;`). Two of those six
additionally require elapsed time measured in days-to-weeks *after* the deploy, which no amount of
engineering speed shortens. That means **most of what stands between this repository and "done" is not
code left to write** — it is one infrastructure event, two wording decisions Cam needs to make (§19.5,
§19.7), and calendar time.

**The cheapest next action, concretely, in order:**

1. `DOD-8`'s restore drill and `DOD-2`'s three-feed creation + `make validate` — same-machine, no
   deployment, no decision needed, and unblock the two DOD items that don't need anything else.
2. Decide §19.5 and §19.7's wording (recommend amending both to state the check method the system
   actually implements — generation-time enforcement for §19.5, daily-ceiling-plus-monthly-review for
   §19.7 — rather than building new schema/limits to match language that was never load-bearing).
3. The production deployment (`C5-01`…`C5-07`) — the single event that starts the clock on `DOD-1`,
   `DOD-3`, `DOD-4`, `DOD-6`, `DOD-9` simultaneously.

**What is still genuinely open code work, not deployment-or-time-gated**, and should not be mistaken
for either of the above: closing `web/ui`'s zero-importer gap (§1, #10 — either wire real pages onto
it or delete it, since an unreached accessible-primitive layer currently provides zero of the
guarantees it exists for); finishing `D0-24`/`D0-25` (the remaining ~60 of 103 classes with no CSS
rule, across generate/history/settings, which have no `styles.go` yet); wiring `item_revisions`'s
existing RPC into the history UI (`D3-15`); and working through `docs/browser-verification-plan.md`'s
remaining 59 of 61 unverified behaviours — most consequentially the DISCONNECTED banner's missing
live region and the unverified controlled-input-clobber risk on the login form, since login is the one
page explicitly named "must never break." None of that needs a droplet or a calendar; all of it needs
someone at a real browser, which is the resource this session spent for the first time and should keep
spending before assuming any of Part 1's 61-item inventory is fine because it compiles.
