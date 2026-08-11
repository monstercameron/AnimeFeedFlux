# Devlog

A running narrative of how this project was built and why it looks the way it does.

**This is not the changelog.** `CHANGELOG.md` records *what changed* per version, for someone
deciding whether to upgrade. This file records *what was learned*, including the wrong turns — the
decisions that were made and then reversed, the research that overturned an assumption, and the
things that were nearly shipped wrong. Reversals are the point: a plan that only records its final
state loses the reasoning that makes it defensible six months later, and the same bad idea gets
proposed again by someone who cannot tell it was already considered.

Newest entries at the top.

---

## 2026-08-10 — Two hours of "broken UI" that was neither broken nor UI

The `/generate` rebuild was working. The browser said otherwise, and it took an embarrassingly long
time to stop believing the browser.

**Trap one: the dev server serves a snapshot, not a directory.** `internal/publish.NewStaticHandler`
reads every asset into memory at construction. That is a deliberate, good decision for a production
server — no per-request stat, no partially-written `.wasm` served mid-build — and it means a running
dev server keeps serving the bundle as it was *at its own start time*. Rebuilding changed the file
on disk and nothing in the browser. Several rounds of "the fix did not take" were the fix never
being loaded.

**Trap two: `pkill` does not kill Windows processes from git-bash.** It exits 0 having killed
nothing. So the restart script's kill was a no-op, the old server kept port 8082, the new server
failed to bind — and the script's own health check then passed, against the old server. A restart
that reports success while changing nothing is worse than one that fails loudly.

Both are now handled by `.devrun/restart.sh`, which rebuilds, kills through PowerShell, waits for
the process to actually be gone, and starts the replacement. Its comment explains why each step is
there, because both traps are invisible and both present as application bugs.

**What the detour did surface, on the way, was a real bug of exactly the shape being chased.** Every
`fetch.UseResource` loader on `/generate` ran once at mount, and on a hard load the mount happens
while the session state is still `appstate.Anon` — the WebSocket has not finished its handshake, so
the calls fail against a socket that cannot carry them, and nothing re-runs them. The page had been
loading no feeds, no settings and no model list whenever it was opened directly rather than
navigated to. The first attempt to fix it keyed a re-fetch on `connected := state != Disconnected`,
which is a bug in the same family: `Anon` is the zero value, so that boolean is *already true*
before there is any session and therefore never changes when one appears. The effect is now keyed on
the session state itself.

Worth noting what caught the smallest defect in the batch: a missing i18n key for the temperature
field's placeholder was found by `web/i18n/callsite_test.go` — the test written a few sessions ago
after shipping a raw `common.connectionUnreachable` key to the screen. It has now paid for itself
twice.

## 2026-08-10 — New brand, and the second half of a bug we thought we'd fixed

### The dark theme was never on

Earlier today's entry records finding that `tokens.Emit()` had zero callers, so every
`var(--color-…)` resolved to nothing and the app rendered as browser defaults. That was fixed, and
it was only half the bug. The dark palette lives under `:root[data-theme="dark"]`, and **nothing in
this application had ever called `ui.SetTheme`** — the attribute that selector needs was never set
on any element, ever. So `DarkTheme()`, its WCAG-corrected swatches and its own passing unit tests
were unreachable for every operator, including one whose OS is set to dark.

It was caught the same way as the first half, which is the part worth remembering: a screenshot in
dark mode came back **byte-identical** to one in light mode. Neither half of this bug is visible
from the Go side — both are "the code is correct and reaches nobody" — and in both cases the tests
passed throughout, because they assert on the rules the token layer *generates*, which is true
whether or not anything selects them. Two instances in one day of the same failure shape (built,
tested, unreachable) is a pattern, not a coincidence; it is the same one `D0-13`'s note records for
`web/ui.Toast` and friends.

The fix (`web/shell/theme.go`) is three-state — `Match system` (default), `Light`, `Dark` — rather
than a two-state toggle. A toggle has to guess an initial value, gets it wrong for half of all
users, and silently stops tracking the OS for all of them; "system" is the only state that can
express *I have not chosen*. It applies before the first render rather than from a component
effect, because a component effect paints one frame of light theme first, which on a login screen
in a dark room is a white flash.

The control went in the header rather than in Settings, and the argument that settled it is
structural, not aesthetic: `/settings` is behind the session, and `/login` is where an operator
working at night meets this application first. A theme control you cannot reach until after you
have signed in arrives one screen too late.

### The mark could not be a vector, so the wordmark could not be an image

The new brand is a kitsune crest — a fox over three broadcast arcs, in a hex shield. Cam supplied
it as three 1536×1024 renders on a flat grey card: no alpha, no vector.

Keying the grey out took three attempts, and the failures are the useful part. A single border-median
grey is not good enough — the backdrop carries a vignette that darkens it by ~0.05 toward the edges,
which is larger than any sane luminance floor, so a constant reference keys the entire frame as
subject. A trimmed per-channel degree-4 polynomial fit over the achromatic pixels solves that.

The glow was the real problem. It is wide, soft, and only half-hued at its edges, so keying it
yields a grey smudge if you keep the neutral part and a blue one if you unpremultiply anyway —
both of which look like a dirty rectangle on any surface that is not the source's own grey card.
**The glow is not extracted.** It is a `drop-shadow` reading the accent token instead, which is
strictly better: it follows the theme rather than being one fixed colour and radius everywhere.

The lockup could not be salvaged at all, and that turned out to be a design decision rather than a
setback. Its wordmark is dark navy sitting *inside* a bright blue bloom — loosen the key and the
mark ships inside a cloud twice its size; tighten it and the text dissolves before the cloud does.
And it is dark navy, so it would be invisible on the dark theme even if it had extracted perfectly.
So the wordmark stays HTML text beside the crest: crisp at any size, and it follows `color` into
dark mode. `web/shell/header.go` was already doing this for an unrelated reason (a GWC v5.0.1
reconciler gap around SVG `<text>`), which is a nice accident.

### Two smaller things the screenshots turned up

`/login` was rendering the brand lockup twice — once in the header, once in the auth card, 200px
apart — despite `header.go`'s own doc comment stating that ANON shows "the mark alone". The comment
described intent nobody had implemented.

And `h.Show(false, node)` does not remove a node. It clones it with `hidden` set and relies on the
UA sheet's `[hidden] { display: none }` — a single type-less selector that **any** class rule
setting `display` outranks. `.af-header__rule` is exactly that shape. This is a whole class of
silent bug (the hidden thing is visible) waiting on every future `css.Display.*` rule in the app,
closed with one global `!important`.

---

## 2026-08-10 — Documentation-backfill pass over serving/auth/transport/ops: PLAN.md was two designs behind on login

Three findings from a doc-vs-code audit scoped to `internal/publish`, `internal/auth`,
`internal/bridge`, `internal/rpc`, `internal/ops`, `internal/obs`, `internal/config`,
`cmd/animefeedflux`, `cmd/aff`. No Go was touched.

**PLAN.md's §2.1 sequence diagram and §4 described a login flow the code no longer implements.**
The diagram had `AuthService.Login`'s RPC response itself carrying the `__Host-` session cookie back
over the already-open anonymous socket. The real, deliberately-built mechanism (`internal/bridge/
ticket.go`, `web/wsconn/ticket.go`) is materially different: `Login` returns a single-use, 20-second
login ticket in a gRPC response *header* (never the raw session token — landing that in WASM memory
is exactly what §4 forbids), the client stashes it in `sessionStorage` and forces a full page
reload, and the cookie is set only on the 101 Switching Protocols response of the *reconnect* that
presents the ticket — spliced into the raw upgrade bytes via a hijacked `net.Conn`, because
`Set-Cookie` cannot go through `http.ResponseWriter` once `gorilla/websocket` has hijacked the
connection. §4 had no §4a describing any of this at all. Added one, backfilling the mechanism, the
verified dead end that ruled out a simpler design (Chromium does not apply `Set-Cookie` from a
WebSocket upgrade response to its cookie jar — confirmed against both the live server and an
isolated Node WS server), and the residual cost the code comments already admit: a plain page
refresh after login has no cookie and no ticket to present, so it silently drops to anonymous —
"one transport, no HTTP side door" and "durable session across a plain refresh" are in tension here,
not resolved.

**Two TODOS.md notes describing production gaps had already been closed by other work and nobody
re-ticked them.** `C4-15` (per-feed error counts on `/healthz`) was still `[ ]` with a note saying
the wiring "requires editing `cmd/animefeedflux/wire.go` (owned by another agent, not edited here)"
— but that wiring is in the tree: `wire.go`'s `HealthFeeds` closure already calls
`ops.LiveFeedErrorCounts` and populates `FeedHealthInput.ErrorCount` for real, reachable from
`runAll`. Ticked, with the evidence. `B3-11`'s note said "PLAN.md A7's scheduler (mapping a stored
`FeedSpec` into a live `generate.Spec`) does not exist yet" — also stale: `wire.go`'s
`generateSpecFrom` + `wireRunExecutor.ExecuteRun` do exactly that mapping for `RunNow`, wired into
`rpc.NewFeedServer` at `wire.go:1055`. The task was already correctly ticked; only the explanatory
note was wrong, so it was corrected in place rather than left implying the CLI e2e test's
fixed-spec stand-in is covering for a real gap that no longer exists.

**One undocumented magic number recorded:** `AFF_PROVIDER_MAX_INFLIGHT` defaults to 4 against a
3-run worker pool (`AFF_MAX_CONCURRENT_RUNS`), and the code carried no comment saying why they
differ. Reasoning backfilled into PLAN.md §14.3: the same semaphore also gates interactive
`Sample`/`SampleStream` calls, so sizing it to exactly the scheduled-run cap would make every sample
block behind three in-flight scheduled runs.

**Security properties named in the task brief were checked against code, not assumed:** the pepper
comes from `AFF_PASSWORD_PEPPER` via `internal/config`, never the DB (`Pepper()`'s zero-pepper no-op
is the fallback for "not configured", not a footgun); salt is 16
fresh CSPRNG bytes per credential (`password.go`'s `rand.Read`), never derived from id/email/a
constant; no code path reads `password_changed_at` to force a rotation — confirmed by its absence,
not by a comment saying so; the `__Host-session` cookie construction (`internal/auth/session.go`'s
`NewSessionCookie`/`ExpiredSessionCookie`) never sets `Domain`. All hold as designed; recorded with
their evidence rather than left as "everyone believes it, nobody checked."

## 2026-08-10 — The `/healthz` gap this file already flagged got closed the same day

The entry directly below records the deliberate decision to leave `internal/publish`'s
`DefaultHealthGrace` unwired to `AFF_STALE_GRACE_FACTOR`, because that package and `wire.go` were
"owned by another agent's in-flight change" at the time. That constraint lifted later the same day:
`cmd/animefeedflux/wire.go:1259` now sets `HealthGrace: ops.ResolveStaleGrace()` in the
`publish.Deps{...}` literal `buildPublishHandlerWithInvalidator` builds, so `/healthz`, the nightly
Slack webhook, and `aff doctor` all resolve the same grace factor from the same env var. The
divergence risk the entry below warns about — an operator staring at a "healthy" `/healthz` while a
stale-feed alert has already fired on a different threshold — no longer exists in the tree.
`TODOS.md` C4-08 carries the current "GAP CLOSED" note; `CHANGELOG.md`'s entry was corrected in
place rather than left asserting the old gap. This entry exists so the reasoning below (why the gap
was left open on purpose, not by oversight) isn't misread as still-current without a pointer forward.

## 2026-08-10 — Making the staleness grace factor configurable without letting `/healthz` and the Slack webhook disagree

`TODOS.md` C4-08/C4-15 asked for the staleness grace factor (how many multiples of a feed's own
cadence it may go quiet before being called stale) to be configurable rather than a hardcoded `2.0`,
since `C3-11` (the real observed Slack poll interval) is still unresolved and `2.0` is a documented
guess. `internal/publish/health.go`'s own doc comment already states the constraint this has to
respect: "the webhook and `/healthz` can never disagree about what 'stale' means" — both currently
default to the identical `2.0` (`ops`'s `SchedulerConfig`/`DoctorConfig` default and `publish`'s
`DefaultHealthGrace`), and that parity is load-bearing, not incidental (`health_test.go` asserts the
two constants match).

The nearly-shipped-wrong version of this change made `ops.NewScheduler`'s zero-value `Grace` default
read `AFF_STALE_GRACE_FACTOR` **without** doing anything about `/healthz`'s hardcoded default. That
looks like a clean, self-contained improvement in isolation — it is genuinely reachable, tested, and
live (neither `wire.go`'s `runAll` nor `cmd/aff/doctor_cmd.go` sets `Grace`/`StaleGrace` explicitly,
so the env var takes effect today for the nightly webhook and `aff doctor`) — but it silently breaks
the exact invariant `health.go`'s comment calls out: set the env var in production and the Slack
alert's threshold moves while `/healthz`'s stays at `2.0`, so an operator staring at a "healthy"
`/healthz` page could be one wire.go deploy behind an alert that already fired, or vice versa. That
is a worse failure than either surface being wrong on its own, because it looks internally consistent
from either surface alone and only shows up as a cross-surface contradiction nobody is checking for.

Landed instead: `internal/ops/cli.go` exposes `StaleGraceEnv`/`ResolveStaleGrace()` as the one place
that knows the env var name and the parse/validate/fallback logic, used by both `NewScheduler` and
`NewDoctorConfig`'s defaults (so the webhook and `aff doctor` are configurable and reachable today),
but `internal/publish`'s `DefaultHealthGrace` was deliberately left untouched — that package is owned
by another agent's in-flight change per this task's constraints, and `cmd/animefeedflux/wire.go`
(the only place that could pass the same resolved value into `Deps.HealthGrace`) is off-limits here
too. The gap is documented loudly in both `TODOS.md` (C4-08's entry) and a `CHANGELOG.md` note rather
than closed by quietly accepting the divergence risk: setting `AFF_STALE_GRACE_FACTOR` today is only
"fully" safe once `wire.go` adds one line (`HealthGrace: ops.ResolveStaleGrace()`) — until then, an
operator who sets it should know `/healthz` hasn't caught up.

## 2026-08-10 — The server ran for the first time, and nothing could log in

First boot of `cmd/animefeedflux`, and both ways into the admin plane were dead — the CLI at the
transport layer, the browser at the protocol layer. Two unrelated bugs that happened to produce the
same symptom.

**The CLI.** `cmd/aff` dials `AFF_ADMIN_ADDR` with a real `grpc.NewClient` (`cmd/aff/client.go`'s
`realDial`), but the admin listener (`cmd/animefeedflux/wire.go`) served only `bridge.NewServer`'s
`http.Server` — the WebSocket tunnel and, once built, the static UI. No `*grpc.Server` was bound to
that address anywhere in the tree. Every CLI command therefore died at the transport, before any RPC
was dispatched, and `aff login` printed `aff login: authentication failed` — byte-identical to
`internal/rpc/auth.go`'s `errAuthFailed` ("authentication failed", `codes.Unauthenticated`), which is
what a genuinely wrong password produces. `client.go`'s `isUnreachable` exists to tell these apart
(`codes.Unavailable`/`codes.DeadlineExceeded` from a dead port versus everything else), but its own
doc comment records the deliberate reason it can't always: PLAN.md §12.1 forbids distinguishing
failure causes in the generic case, so a non-status transport error is *treated* as a credential
rejection rather than reported as unreachable, on purpose, as the safe default. A dead port and a
wrong password produced the same output for the same reason the account-existence oracle rule exists
— and the one thing that would have proven the RPC never arrived, `auth_events` staying empty through
every attempt, took reading the database directly to notice, because the CLI's own output gave no
hint.

**The browser.** No CLI bug at all — a protocol-level chicken-and-egg. `bridge.NewServer`'s
`ServeHTTP` (internal/bridge/server.go) validates the session cookie *before* the WebSocket upgrade,
with no exemption for `AuthService.Login`: no cookie, no upgrade, a bare 401 — confirmed against
`internal/bridge/server_test.go`'s own `TestUpgradeWithoutCookie_Unauthorized`, which proves the exact
behavior and does not special-case Login either. A session is required to open the socket, and the
socket is the only place `AuthService.Login` — the one thing that can mint a session — lived. §4
forbids the token ever reaching JS/WASM-readable memory (no JWT in a response body, no token field a
WASM caller could read), which is exactly why Login answers with a header a real `*grpc.Server`
attaches via `grpc.SetHeader`, not a message field a WebSocket frame could carry cleanly. The missing
piece was a plain HTTP login endpoint outside the bridge entirely — designed in §4 (`__Host-` cookie,
`HttpOnly`, `Secure`, `SameSite=Strict`, no `Domain`) and never implemented anywhere. `internal/e2e`'s
own suite had already documented this exact gap rather than discovering it fresh: `internal/e2e/app.go`
opens a second, plain (non-bridge) `*grpc.Server` just to call Login, with a comment naming it
outright — *"a real production gap and not a shortcut this suite invented"* — and that comment sat
there, correct and ignored, while the suite stayed green the entire time, because a workaround inside
a test proves the workaround works, not that the gap it routes around has been closed.

**The fix, in one shape twice.** `*grpc.Server` implements `http.Handler` (grpc-go's own doc comment
on `Server.ServeHTTP`), so the admin listener now wraps its handler in `h2c.NewHandler` (cleartext
HTTP/2, which `grpc.NewClient`'s insecure transport speaks with prior knowledge) and a new
`adminRouter` in front of it: an HTTP/2 request whose `Content-Type` begins `application/grpc` goes to
a real `*grpc.Server` registered with the same six services the bridge uses; everything else falls
through unchanged. One listener, one port, §2's two-listener boundary untouched — `cmd/aff` gets a
real transport without a second admin port to secure. For the browser, `internal/bridge/httpauth.go`
adds `POST /auth/login` and `POST /auth/logout` as ordinary HTTP handlers, routed ahead of both the bridge
and the SPA static fallback in the new `adminMux`, backed by the *same* `AuthServer` every gRPC path
uses (via `bridgeAuthenticator`, which replays the call through `AuthServer.UnaryInterceptor()` with a
fabricated transport stream so `grpc.SetHeader`'s token capture still works outside a real RPC
dispatch) — never a second, independently-constructed auth path that could drift from the first.

**Two routing bugs surfaced in the same pass, same root cause (no admin router existed before this).**
Before `adminMux`, the admin listener's handler *was* the bridge with nothing in front of it, so there
was no place for the static UI to be mounted at all — the SPA's own `<base href="/">` had nowhere to
resolve, because nothing served `/` as anything but a WebSocket-upgrade attempt. And there was no
SPA-route fallback: `adminMux.ServeHTTP` now serves the shell for any path `m.static.Has` doesn't
recognize as a real asset, so a deep link or a refresh renders the client router's own not-found
instead of a bare 404 with no way back in.

The reusable shape, same as the "seven things" below but one layer further out: none of this is
visible to `go test ./...`, because the CLI's tests supply fakes for the network and the e2e suite's
workaround *is* the missing piece, faithfully exercised. All four gaps — the dead transport, the
upgrade-before-Login deadlock, the routing collision on `/`, and the missing SPA fallback — appeared
within ten minutes of running the actual binary for the first time. A green test suite proved every
component in isolation; running the thing was what found the seams between them.

## 2026-08-10 — A systematic sweep for "built, tested, reachable from nothing" found more of them

The login gap above and the "seven things" below were each found one at a time, by tracing a
specific suspicion back to a composition root. `docs/unreached-components.md` is what happens when
that search is made systematic instead: every exported identifier in `internal/`, `web/`, and
`gen/aff/v1` (2,233 declarations, 1,717 unique after dedup) checked for a real call site outside its
own file, narrowed to 88 zero-caller candidates, each read by hand against `PLAN.md` to separate a
real gap from a false positive (interface satisfaction, function-passed-by-reference, functional
options with sane defaults).

The count of things independently found "built, tested, and wired to nothing" was already close to
ten before this sweep — the seven below plus `ops.Scheduler` and `obs.Setup` (both since fixed,
`docs/unreached-components.md` notes) — and the sweep's own write-up refers to that running tally as
"the original ten" while cataloguing what it added on top: 8 dead-on-arrival findings and 6
recommended-for-deletion speculative ones in section A/B of the doc.

The single largest is **the entire `web/ui` component library** — `Button`, `Modal`, `Confirm`,
`Toast`, `Tabs`, `StatePanel`, `Kebab`, `Input`, `Select`, `Table`, `Toggle`: 1,724 non-test lines,
162 lines of its own tests, and zero production importers. `grep -rl "AnimeFeedFlux/web/ui\""` across
every page under `web/pages/{auth,generate,history,settings}`, including test files, returns nothing —
every page instead imports `GoWebComponents/v5/ui` directly under the same local alias (`ui`), which
is what made the gap easy to miss by name alone, and hand-rolls markup instead. Nothing is visibly
broken — the pages work by reimplementing equivalent markup — but every centralized guarantee the kit
exists to provide once (the shared `focusVisible()` a11y treatment, one reviewed implementation of
destructive-action confirmation) never reaches a real page, because no real page runs that code.

Other confirmed findings worth naming: `llm.Error.Scope` (`ScopeAccount`/`ScopeRecipe`) is set at
every fatal-classification site and read nowhere, so the one-bad-key-trips-the-global-kill-switch
design in §8 does not happen — each feed instead burns its own five-failure budget against the same
dead credential before disabling itself; `obs.WithRunID`/`obs.WithRequestID` are never called, so
every log line is missing the identifier that would let an operator filter logs down to one run
during an incident, despite the log/trace correlation contract in §15.0a existing and working
otherwise; and `store.ListAuthEvents`/`RecentFailures` — the read side of the audit trail
`internal/rpc/auth.go` writes at ~19 call sites — had no RPC and no UI reaching it at all (closed in
this same session by `SystemService.ListAuditEvents`, see `CHANGELOG.md`).

## 2026-08-10 — A tick audit un-ticked tasks for the first time, not just added them

Every previous audit pass in this project's history has *added* unticked tasks it found missing.
This is the first time one went the other way: re-checking `A9-20`..`A9-24` (publish-plane tracing
spans and metrics, §15.0a) and `B2-06` (verifying `SampleStream`/`RunService.Watch` actually stream
through the bridge, §11) against the tree found no supporting code at all — `internal/publish` has no
spans, and the streaming verification A9-checks depend on had never been written — despite all six
being marked `[x]`. They are `[ ]` again now, with the re-audit's finding recorded inline rather than
just the checkbox flipped.

Worth writing down on its own, separate from whatever caused the individual mismarks: an over-marked
task is worse than an unmarked one. An unticked box still gets looked at eventually, because it reads
as unfinished. A ticked box against work that was never done reads as settled, and nobody re-examines
a checked box — it takes an audit that starts from *reading the code* rather than *trusting the last
tick* to ever catch it. `TODOS.md`'s stated failure mode is "silent under-marking"; this session found
the opposite failure is real too, and arguably more expensive, because it hides rather than merely
delays.

## 2026-08-10 — The trivia spoiler does not survive a real reader; Slack only ever hid the bug, not the answer

PLAN.md §5.5 specified a trivia item's answer goes into `content:encoded` "behind a spoiler break,"
on the assumption that some `<details>`/`<summary>`-shaped disclosure widget would keep it hidden
until a reader chose to reveal it. That assumption was never checked against an actual sanitizing
reader until this session (`docs/spoiler-design.md`). It does not hold: `GoWebComponents/v5/sanitize`'s
`DefaultPolicy` — the sanitizer ArticleFlux's reading pane runs `content:encoded` through before
rendering it — does not allow `details` or `summary`, and disallowed tags are not dropped, they are
*unwrapped*: sanitized children survive, the tag around them does not. `<details><summary>Reveal
</summary>THE ANSWER</details>` comes out the other side as plain `THE ANSWER` text, inline, with no
toggle and no way to re-hide it.

The reason this shipped unnoticed: Slack was the only consumer this project ever actually tested
against (§5.5's own "practical check"), and Slack never renders `content:encoded` at all — it only
ever shows `description`, which was already question-only. The one channel the design was verified
against structurally cannot exhibit the bug, because the spoiler mechanism was never on the path
Slack reads. The failure only shows up in a full-content reader, which is ArticleFlux — the second
consumer PLAN.md §1 names by name — and nothing in this repository had ever emitted or sanitized a
`<details>` tag to find out. `docs/spoiler-design.md` records four options (whitespace/scroll
distance, a separate later item linked to the question, permalink-only answer, or accepting the
answer is visible and designing around that) with their real cost against all three consumer shapes;
none is adopted, and PLAN.md §5.5 now points at the document instead of restating the original
assumption as settled.

## 2026-08-10 — `DOD-4` puts a hard floor under how soon this project can be called done

Auditing `PLAN.md §19` against the tree (`docs/definition-of-done.md`) found the definition of done
is not nine independent checks — seven of the nine are gated on one thing that has never happened
(a production deployment), and two of those seven additionally require elapsed time *after* that
deployment: `DOD-3` needs 7 consecutive days of Slack delivery, and `DOD-4` needs **30 consecutive
days of production trivia generation with no near-duplicate pairs** — explicitly not provable by the
existing canned-corpus novelty harness, because that only proves the dedup mechanism works, not that
a live model run daily for a month won't repeat itself. The practical consequence is worth stating
plainly rather than leaving implicit: whatever day the first production deploy happens, `DOD-4` cannot
be satisfied before a month after it, no matter how fast everything else moves. It is a clock that has
not started, not a task that can be finished by working faster.

## 2026-08-10 — Seven things built, tested, and wired to nothing

The single most reusable lesson of this project so far, because it recurred
seven separate times across a single day of work rather than once:

- The **pepper** (`internal/auth/pepper.go`) — implemented, unit-tested, zero
  production callers.
- **Reset tokens** — single-use logic implemented and tested against the
  store; no proto RPC and no CLI command ever issued or consumed one.
- The **off-box backup encryptor** — chunked AES-256-GCM, tested against
  round-trip fixtures; `OffsiteDir` pointed at a local directory, so nothing
  it produced ever left the box.
- `item_revisions` — every `Update` wrote a row; nothing could read one back.
  `ListRevisions` and `RevertRevision` did not exist.
- The **novelty gate** — read `item_embeddings` for its dedup check; nothing
  had ever written a row to that table, so every candidate was compared
  against an empty corpus and always passed.
- The **i18n `Provider`** — `UseI18n()` calls were already written on the
  auth pages; nothing mounted the provider that fills it, so every call read
  an empty bundle and silently rendered its fallback default.
- The **entire admin UI** — four page packages, unit-tested, and
  `web/main.go` never called `shell.RegisterPage` for any of them. Every
  route rendered the shell's placeholder regardless of how complete the page
  behind it was — about eighty `TODOS.md` tasks blocked on wiring, not work.

Every one of these passed its own unit tests. That is not a coincidence or a
run of bad luck — a unit test cannot see an absent caller, by construction.
It exercises the function directly, which is exactly the thing a missing
composition root does not affect. "Tested" was read as "working" seven
times, and each time the gap was invisible until something existed that
would actually have had to call the code: `cmd/animefeedflux`'s composition
root, the adversarial `sectest` suite, `web/main.go`'s composition root, or
just someone reading `TODOS.md` against the tree instead of against the
last time it was ticked.

The placeholder screens made the UI case worse than a compile error would
have: a route that renders *something* looks like a legitimate, if unfinished,
page. A test asserting "the router has five routes" would have passed the
entire time it was broken. The fix in every case was the same shape —
find the one place nothing calls the tested thing, and make that call
unconditional rather than an opt-in step a future composition root can
forget. `ServeHTTP` now sets `Session.Token` from the cookie it already
validated with no wiring step to skip; `web/main.go`'s `Mount` takes a wire
callback invoked before route registration, not after.

The general rule this earns: a component is not "done" because its tests
pass. It is done when something that runs in production actually reaches
it, and that has to be checked by tracing the call graph outward from a
real entry point, not by trusting the component's own green tests.

## 2026-08-10 — `GoWebComponents@latest` is an abandoned v1

The admin UI dependency was first added as `GoWebComponents@latest`. That
resolves to an abandoned v1 proof-of-concept whose reconciler package is
unexported — nothing outside the module can mount a tree with it. A whole
shell got built on hand-rolled `syscall/js` before this was noticed and the
real library was found at a different module path entirely:
`GoWebComponents/v5` at `v5.0.1`, with the router, state, ui, css, a11y and
i18n packages the plan actually needed. `@latest` picking the wrong major
version silently, with no error to catch it, is the trap worth remembering —
checking the module path against the source, not the tag, is now the first
step before building on any dependency here.

A smaller version of the same trap: the docs describe a `Class(...)` helper
that does not exist in v5.0.1, only `ClassStr`. Three separate agents hit
that independently before anyone read the source instead of the manual.

## 2026-08-10 — A selector that can never match, caught only by rendering it

`css.DataTheme` composed with `Global(":root")` emitted
`[data-theme="dark"] :root` — a selector asking `:root` to be its own
descendant, which no document can ever satisfy. Dark mode would have shipped
looking correct in every code review and failed silently in every browser.
It was caught only because a test ran a real `css.Harvest()` over the
composed rule and read the emitted selector, rather than asserting that the
two pieces were present. Composing two style helpers that are each correct
in isolation is not evidence the composition is; the only check that works
is rendering the actual output.

## 2026-08-10 — The pepper was applied to the wrong thing, and said so

`pepper.go` documented `HMAC-SHA256(pepper, argon2idOutput)` — the pepper
applied *after* the memory-hard hash. The code that got wired up applied it
to the *password string*, *before* hashing instead. The agent that wired it
recorded the deviation honestly in a comment, and the constraint behind it
was real: `auth.Hash`/`Verify` only accept and return a PHC string, with no
exported access to the raw argon2id output, and `internal/auth` was
off-limits to that agent.

Neither order is cryptographically weaker — feeding the pepper through a
memory-hard KDF either way is fine. The actual defect was narrower and more
dangerous: the codebase held two mechanisms, `VerifyPeppered` implementing
the documented order with zero callers, and the wired one implementing a
different order the docs did not describe. The next person to read
`pepper.go` would believe the output was peppered and reason from something
untrue. A documented deviation that never gets reconciled decays into a lie
the moment someone reads the doc instead of the diff.

Fixed by implementing the documented order rather than editing the document
down to match the code — `HashPeppered`/`VerifyPasswordPeppered` now derive
the argon2id output exactly as `Hash`/`Verify` do, then apply
`Pepper`/`VerifyPeppered` to that. `Hash` and `Verify` themselves are
byte-for-byte untouched, so the no-pepper deployment path (the common case)
is unchanged by construction, not by testing.

## 2026-08-10 — Reverting `go.mod` is exactly as destructive as editing it

Three build breakages this session, previously assumed to be `go mod tidy`
running mid-build. The real cause was different and more instructive: agents
following "do not touch `go.mod`" ran `git checkout -- go.mod go.sum` to
undo what looked like their own accidental edit, and in doing so reverted
dependencies a *sibling* agent had legitimately added in a different task.
Each revert was locally correct — the file agents saw did look wrong to
them, in isolation — and globally destructive, because the build graph is
shared across everyone working at once. That is the same failure shape as
the earlier `go mod tidy` incident, just from the opposite direction: adding
and reverting are both writes, and both are prohibited for the same reason.

The rule is now explicit rather than implied: do not edit `go.mod`/`go.sum`
**and** do not revert them either — leave them alone entirely and report
what looks wrong. A locally correct action is not safe by default when the
thing it acts on is shared.

Worth keeping for the opposite reason too: one agent refused to act on an
unverifiable coordinator claim about a dependency, checked it against the
actual module cache itself, and solved the problem with what was already
present rather than trusting the claim. That scepticism is the behaviour
the corrected rule is trying to produce generally.

## 2026-08-10 — i18n was ruled out for the wrong reason

`D0-20` said: no i18n, single user, English, out of scope. Cam reversed it. The
reversal is worth recording because the original reasoning was not wrong so much
as answering a different question.

"Do we need other languages?" — no, and still no. But that is not what i18n buys
at one user and one locale. What it buys is that strings have an owner. Without a
catalogue the same label gets written three slightly different ways on three
screens, an error message drifts from the server text it is supposed to mirror,
and there is no way to see what the interface actually says without reading every
component. With one, the interface's vocabulary is an artefact that can be
reviewed and diffed.

The timing argument is the stronger one. Retrofitting i18n means touching every
component that was written without it — a large, low-status, all-at-once job that
never gets scheduled. Phase D has barely started, so the cost right now is close
to zero and falls with every screen not yet written. That is why `D6-*` says to
extract per surface *alongside* each screen rather than as a pass afterwards: a
cleanup pass over finished screens is exactly the retrofit being avoided.

Two boundaries fell out of writing it down, and neither was obvious beforehand:

- **Feed content must never go through the catalogue.** It is authored by the
  model in the feed's own configured language and is data, not interface. A
  well-meaning wrapper around item titles in the history screen would silently
  corrupt published output — and it would look like tidiness.
- **The generic login-failure string stays generic in every locale.** §12.1
  removes the account-existence oracle by using one message for every failure. A
  translator handed those keys in isolation would naturally make "no such
  account" and "wrong password" distinct, because that reads better, and would
  reintroduce the oracle without ever touching the auth code. The constraint has
  to live in the catalogue, not only in the login page.

The gate is a zero-literal ratchet, for the usual reason: a convention nobody can
check decays, and this one decays invisibly — a hardcoded string looks exactly
like a translated one until someone greps.

## 2026-08-10 — The password rule that was choosing the weaker password

Cam supplied a fully-specified authentication architecture (NIST SP 800-63B +
OWASP). Most of it matched what was already built. Three parts did not, and one
of those was a genuine defect rather than a preference.

### The composition rule was inverted

`IsWeak` required a password to mix letters with digits or symbols. Applied to
two real candidates:

    "correct battery dinosaur tennis"   -> REJECTED (no digit, no symbol)
    "P@ssw0rd2026!"                     -> ACCEPTED

The rule was actively selecting the weaker password. Not failing to catch the
weak one — *preferring* it. That is precisely the finding behind NIST dropping
composition requirements, and seeing it happen in our own code was more
persuasive than the citation.

It is replaced by length (15–128) plus a compromised-password blocklist, and
there is now a test whose stated purpose is to stop anyone reinstating the old
rule, with the two candidates above as its fixtures.

Related: passwords never expire. Forced rotation fails the same way — a human
asked to change a passphrase on a schedule increments a digit.

### Parallelism 4 was not "more secure"

Argon2id `p=4` splits the same memory budget across four lanes. That is easier
for an attacker with GPUs to parallelise than for us on one droplet core. OWASP
says 1 for this memory profile. Corrected.

### The blocklist is deliberately offline

The obvious implementation is a k-anonymity range query against Have I Been
Pwned. Rejected: it puts a third party on the login path, so their outage
becomes an outage of the only way into this system, and it leaks a hash prefix
of the admin's password on every enrolment. A local list can do neither.

Repetitive and sequential strings are blocked too. That is not the composition
rule returning by the back door, and the distinction is worth keeping straight:
a composition rule dictates what a password must *contain*; this rejects strings
with almost no entropy whatever they contain. NIST lists them as blocklist
material explicitly.

### One place the supplied design was not adopted

Session lifetimes stayed at 12h absolute / 60m idle rather than the suggested
7d/24h. Those figures are right for a consumer PWA; this is a single-admin
console that can rewrite every published feed, so re-authenticating twice a day
is cheap. Recorded in §4 as policy rather than cryptography, so it can be
relaxed without anyone wondering whether something structural depends on it.

### What was already right

Opaque 256-bit session tokens stored only as SHA-256, `__Host-` cookie with no
`Domain`, `Origin` exact-match at the WebSocket upgrade, and no JWT anywhere.
The one genuinely missing mechanism was **periodic session revalidation on a
live socket** — without it, authenticate at 12:01, session expires at 18:00, and
the socket is still serving RPCs at 23:00 because nothing re-checked.

---

## 2026-08-10 — Phase A built in parallel waves; what six-agent fan-out actually costs

Five waves of six Sonnet subagents took the repository from a specification to
most of Phase A: store, three renderers, publish plane, sanitizer, sources,
scheduler, generation pipeline, novelty, budget, auth, OTel. Roughly 15k lines
with tests, all gates green.

The interesting part is not the throughput. It is what went wrong.

### Isolation needs more than "own your files"

I told each agent to touch only its own files. That is necessary and it is not
sufficient, three times over:

- **The package namespace is shared.** Three agents independently wrote a test
  helper called `testChannel` in package `render` and collided. They noticed and
  renamed, but only because the build broke loudly.
- **The build graph is shared.** I ran `go mod tidy` while agents were writing,
  and it stripped `oklog/ulid` because nothing imported it *at that instant*,
  breaking an agent mid-task. Later, an agent following "do not modify go.mod"
  faithfully restored go.mod and deleted go.sum — a locally correct action that
  was globally wrong, because a sibling had legitimately added a dependency.
- **A spec can collide with itself.** I asked one agent for a function called
  `Validate` in two files of the same package. That one was mine.

The rule that actually works: the coordinator owns `go.mod` exclusively, adds
every dependency *before* dispatch, and agents are told never to delete a file
they did not create.

### Cheap fuzzing found more than careful review did

Three fuzz targets found real bugs that reading would not have:

- `urlnorm` idempotence: multi-slash paths needed the trailing-slash strip run to
  a fixed point, and a host that was only a port normalised to a host-less string
  that then failed on a second pass. §9.6's byte-equality check is only sound if
  normalization is stable, so both would have silently rejected *good* links.
- `sanitize` idempotence: attribute entity round-tripping turned `&amp;` into
  `&amp;amp;` on a second pass.

Both classes are invisible to review because each pass looks correct in
isolation.

### An over-strict test is a bug in the test

The sanitizer fuzz initially asserted that "javascript:" never appears anywhere
in the output, and failed on the plain text input "JAVASCRiPt:". Making the
sanitizer satisfy it would have corrupted legitimate prose — an article about XSS
must survive with its text intact. The property was rewritten structurally: every
surviving tag is in the allowlist, the only attribute is href on `a`, every href
scheme is http or https. Same protection, no false positives.

### Reading the dependency beat trusting the plan

§8 assumed SchemaFlux would report token usage and expose embeddings. Reading the
v1.1.0 surface found it does neither, and that a per-call `Client` does not
isolate state the way the plan claimed — `client.Context(ctx)` is required.
Recorded in §8.1 rather than discovered at A5. Cost is now labelled an estimate,
and the novelty gate calls go-openai directly as a documented exception.

### Ticking is part of the work, and I got it wrong

A batch of 37 completed tasks silently stayed unticked because I chained the tick
script behind `&&` after `staticcheck`, which failed on an unused constant and
short-circuited it. Cam caught it. `AGENTS.md` now says: run the verification,
read it, then tick — never chain the two.

---

## 2026-08-09 — Specification built, reviewed three times, and tagged `v0.0.1-dev`

Eleven commits, one day, no code. The repository went from empty to a specification, a build order,
and full scaffolding. What follows is the arc, not a commit list — the log itself is in git.

### The plan came out of research, not memory

The first draft (`d9fb7c5`) was written from what I already knew about RSS. That was wrong, and the
second pass fixed it by actually reading the RSS 2.0 specification, the RSS Advisory Board's Best
Practices Profile, RFC 4287, and JSON Feed 1.1. Three decisions changed as a direct result:

- **`guid`'s `isPermaLink` defaults to `true`.** A silent default. Left implicit, every guid would
  have claimed to be a permalink.
- **RSS uses RFC 822 dates; Atom uses RFC 3339.** Two formatters, and crossing them is a whole class
  of bug. The plan now forbids it by rule and asserts it in a test.
- **The Best Practices Profile prefers hexadecimal character references** over named entities, and
  notes RSS has **no base-URL mechanism** — so relative URLs are unusable and every href must be
  absolutized before storage.

The lesson worth keeping: for anything with a written specification, read it. The cost was two
WebFetch calls; the saving was three bugs that would each have surfaced only in someone else's
reader, weeks later.

### Slack turned out to be stricter than the spec, and quietly

Researching Slack's RSS app was the highest-value hour of the day. Its documented behaviour imposes
four requirements beyond valid RSS: a date tag on every item, items in sequence, **no duplicate
timestamps**, and a feed that passes the W3C validator.

The duplicate-timestamp rule was a live bug in the plan. A grounded news run publishing three items
in one pass would naturally have stamped them identically, and Slack would have kept one and dropped
two — with **no error anywhere**. It does not fail loudly; it just stops posting. That single fact
reshaped the design:

- distinct, strictly-increasing timestamps enforced by `UNIQUE(feed_id, published_at)` — a
  constraint, not a convention;
- a no-backdating rule, because Slack advances a bookmark past the newest item it has seen and a
  backdated item is therefore invisible forever;
- **corrections instead of edits**, because an edit does not change the guid or date and so is never
  re-delivered;
- plain-text `description` with the HTML moved to `content:encoded`, since Slack renders a snippet
  and mangles rich markup;
- OpenGraph tags on permalinks so the unfurl is not a bare URL;
- trivia answers kept out of `description` and `og:description`, or every question is spoiled in the
  channel preview.

A consumer whose failure mode is silence deserves its own test suite. It got one, plus a milestone
(C3) that sits deliberately *before* production deploy.

### Three adversarial review rounds, converging

Ran the plan past an adversarial reviewer three times: **16 findings, then 7, then 3.** Clear
convergence, and the third round confirmed the earlier fixes held. Most of it was real; the two
findings I rejected are noted below.

**Round 1 — the structural one.** The `guid` was derived from `sha256(slug | title | date)` while
the plan simultaneously promised it never changes on edit. True only by convention: any later code
path that re-derived it — a renderer refactor, a repair script — would mint a new guid after a title
edit and resurface the item as a duplicate in *every* subscriber's inbox. Items now carry an opaque
ULID, so the property is true by construction, and idempotency moved to a separate `content_hash`.
Separating identity from deduplication was the right factoring and I had them conflated.

Round 1 also caught that I had multi-feed scaling work gating backups and deploy, which contradicts
my own §14.4 claim that 1–10 feeds need none of it. Deferred to E1.

**Round 2 — the one I would have shipped.** The grounded link-integrity check compared
*asymmetrically normalized* URLs: candidates kept their `utm_*` and `fbclid` parameters while the
output path stripped them. A model faithfully echoing a real article URL would fail byte-equality
and be silently dropped. It fails safe — no hallucination gets through — but it would have starved
the news feed while appearing to work, and the symptom would have looked like the model
misbehaving. Normalization now happens once, at fetch, and both sides use the same function. There
is a test for exactly this.

Round 2 also found that a crash between "items committed" and "run closed" would leave live items
beside a run the watchdog marks `interrupted` — history lying about what happened, which is the
exact failure the plan cites when it forbids editing runs. Items and their run row now commit in one
transaction.

**Round 3 — deleting something.** A `PurgeDeleted` RPC I had specified contradicted three of the
plan's own promises: only runs and embeddings are ever pruned, the guid is never freed, and the
permalink 410s forever. Purging leaves nothing to 410 on. Cut it outright rather than reconciling
it — no definition-of-done item needed it.

**What I rejected:** a suggestion to treat cron jitter as premature scaling work (it is nearly free
and retrofitting it changes scheduler semantics), and a framing quibble about `SameSite=Strict`
versus `Origin` checking that I resolved by rewording rather than redesigning.

### Docker: rejected on evidence, then adopted anyway

Asked whether to deploy with Docker, I inspected the droplet instead of guessing — and the plan was
wrong about the platform. `Earl-Cameron-dot-com` runs **nginx, not Caddy** (the plan said Caddy in
six places), with three Go services and seven sibling timer units under systemd, no Docker
installed, 2 GB RAM, 4 GB swap already configured, Go 1.26.5 on-box.

I recommended against Docker: a second deployment model, a second log destination, and 100–200 MB of
2 GB for a daemon, against a static binary that systemd already sandboxes comparably.

Cam overrode it for the learning value of a real container pipeline. That is a legitimate reason my
analysis did not weigh, so §15 was rewritten for Docker **and records the trade explicitly** rather
than quietly flipping. Three things came out of doing it properly:

- **The build machine is ARM64 Windows; the droplet is amd64.** A local build produces an image that
  builds and pushes fine and then dies at `docker run` with an exec-format error. Building in CI
  removes the trap rather than requiring discipline — and keeps the WASM link, the memory spike, off
  a 2 GB box.
- **Named volumes inherit ownership from the image path; bind mounts do not.** Distroless runs
  non-root, so a root-owned data directory yields a volume SQLite cannot write.
- **Docker writes DNAT rules ahead of the host firewall chain**, so publishing to `0.0.0.0` exposes
  a port *past* `ufw`. Here that would put the admin plane on the internet.

Also inherited a hard-won lesson from `articleflux.service`: `StartLimitIntervalSec` and
`StartLimitBurst` are `[Unit]` keys, not `[Service]` — misplaced, systemd ignores them silently and
the rate limit does not exist. And from a real incident on that box: fourteen verified backups, the
source database, and the decryption key all lived on one volume, so the single event they insured
against took all three. Backups here go off-box, encrypted.

### SchemaFlux, and the line it does not cross

Adopted `github.com/monstercameron/schemaflux` as the LLM layer, which deletes real scope: schema
plumbing, parsing, retries, cost accounting. Reading its README rather than assuming gave three
facts worth having — only OpenAI is live-verified among its seven providers, process-wide state
means we must build an explicit `Client` per call since model varies per recipe, and its cassettes
may replace the planned fake provider.

The important line, written into §8: **typed is not valid.** SchemaFlux guarantees the *shape* of a
value. A struct containing a hallucinated URL is perfectly typed and completely wrong. Every
business rule stays ours.

Also rejected its `Deduplicate` for the novelty gate — it asks the model about pairs, so O(n²)
*model calls* against a 500-item window, versus one embedding and a dot product.

### Core engine first, UI last

Restructured milestones into phases A–E on Cam's direction. The argument that convinced me while
writing it: every RPC the UI calls gets exercised by the CLI first, so the UI is built once against
settled semantics instead of co-evolving with a changing API. And the product is delivering feeds to
Slack long before it has a front end — a UI built earlier would be polishing an admin surface for a
system not yet producing anything worth administering.

### The reference audit

Auditing every `§` cross-reference mechanically found four defects that reading would have missed:
`TODOS.md` cited `§9.1`–`§9.6` when §9 was an unnumbered list (the *second* time a dangling §9.x
reference appeared — now fixed at the source by making the eight generation steps citable anchors);
the load-bearing nginx directives were filed under Risks instead of deployment config; §21's open
questions had no tasks at all and would have been resolved by accident; and `D-FLOW` duplicated the
journey list it should have referenced.

**Ten user flows** (`J1`–`J10`) were promoted from a sketch into canonical §22 definitions with
sanity assertions — deliberately *system-state* invariants rather than unit assertions, because that
is the level this design's real failures live at. Each is automated twice: headless at Phase B as
the regression suite, and as a UI walkthrough at Phase D. The single most important is `BF-11`:
sampling leaves the item count unchanged. A sampler that publishes would look like a feature
working.

### Where it stands

452 → ~520 atomic tasks across five phases, every one citing a plan section. Zero lines of Go.
Tagged `v0.0.1-dev`, which versions the specification and sorts below any future `0.0.1`.

**Open before Phase A can finish:** `OQ-02`, public versus private feeds. Private needs
per-subscriber URL tokens and changes the caching design, so it cannot be decided late.

**Next:** `A0-01`.
