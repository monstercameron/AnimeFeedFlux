# Browser verification plan — admin UI

No browser has ever loaded `web/`. `go test ./web/...` (and `./...` generally)
only compiles and runs the packages/functions with no `//go:build js && wasm`
tag, or the pure-logic siblings deliberately split out from the DOM-touching
code (`countdown.go` vs `banner.go`, `guard.go` vs `guardadapter.go`,
`expirydecision.go` vs `expiry.go`, and so on). Everything under the js/wasm
tag — rendering, focus, ARIA, keyboard traversal, real RPC round trips, the
actual bundle load — has executed zero times outside a compiler.

This document inventories what that gap actually contains (Part 1), ranks
what in it is most likely to be broken based on reading the source against
three known trap classes in this GWC v5 pin (Part 2), and gives an ordered,
one-sitting walkthrough to start closing it (Part 3).

## How this was produced

Static reading only: `grep`/`Read` across `web/`, cross-checked against the
pinned `github.com/monstercameron/GoWebComponents/v5 v5.0.1` module source
(resolved via `go env GOMODCACHE`) for the semantics of `css.Class`,
`UseAtomKey`, and controlled-input value handling. No build was run, no
browser was opened. Every behaviour below cites the file it lives in; every
setup command below was confirmed to exist in `cmd/aff/*.go` or `Makefile`,
not assumed.

---

## Part 1 — inventory of what only executes under `js && wasm`

44 files across `web/` carry the `//go:build js && wasm` tag (verified:
`grep -rl "go:build js" web`). Below is the behaviour inventory grouped by
package, specific enough to test, not just "rendering." Counting one row per
named behaviour, this list is **61 unverified behaviours**.

### `web/shell` — routing, session, banner, expiry (`app.go`, `banner.go`,
`countdown.go`, `expiry.go`, `guardadapter.go`, `pages.go`, `session.go`)

1. `Mount` (`app.go:136`) blocks up to `initialSessionTimeout` = 5s
   (`app.go:90`) on a boot-time `Session` RPC before the router renders
   anything. Never observed: does the page actually sit inert for up to 5s
   on a slow/unreachable control plane, or does something render first?
2. `resolveInitialSession` (`app.go:170`) fail-safe: a boot RPC error or
   timeout sets `SessionAtom` to `Anon`, not `Disconnected` — an operator
   with a genuinely broken connection at boot lands on `/login`, which then
   itself cannot succeed. Never watched happen.
3. `routeGuard`/`catchAllGuard` (`guardadapter.go`) actually redirecting in
   a real `router.HistoryRouter` — `guard.Decide`/`guard.DecideUnknown` are
   host-tested as pure functions (`web/guard/guard_test.go`), but the
   `router.GuardFunc` adapter that calls them from `BeforeEnter` has never
   fired against a real navigation.
4. `renderBanner` (`banner.go:69`): visibility toggling from `SessionAtom`
   transitioning to/from `Disconnected` — never watched render.
5. `renderBanner`'s local countdown ticking down once a second via
   `ui.UseInterval` (`banner.go:97`) — never watched actually count.
6. `renderBanner`'s `ui.UseEffect` restart-at-0 on every fresh disconnect
   (`banner.go:82`) — never watched reset on a second drop after a
   reconnect.
7. **The banner text node carries no `aria-live`/`role` at all**
   (`banner.go:115-122` — plain `h.Div`, no `Aria`/`Role` call, and no
   wrapping live region anywhere in `pages.go`). Whether a screen reader
   ever hears "reconnecting" is unverified and, by inspection, currently
   would not fire (see Part 2, finding #1).
8. `renderExpiryModal` (`expiry.go:33`) show/hide via `h.Show(pending.Get(), ...)`
   — never watched appear.
9. `renderExpiryModal`'s "Log in" button (`expiry.go:44`) calling
   `AcknowledgeExpiry()` (`session.go`) and releasing the held ANON
   transition — never exercised end-to-end.
10. The expiry modal has **no `role="dialog"`/`aria-modal`, no focus move
    into it, and no focus trap** (`expiry.go:40-50` — a bare `h.Div`, not
    `web/ui.Modal`). Whether Tab can escape it into the page behind is
    unverified (see Part 2, finding #2).
11. `RegisterDirtyCheck` (`session.go`) actually holding a real navigation
    away during a live session expiry while a form is dirty — the dirty
    predicate itself is per-page (see history/generate below); the
    hold-and-show-modal mechanism has never fired for real.
12. `watchConnectivity` (`web/wsconn/conn.go:154`) translating real
    grpc-go `connectivity.State` transitions into `EvWSDropped`/
    `EvWSReconnected` against an actual dropped WebSocket/gRPC-Web
    connection — this is the one thing the whole banner/backoff/modal
    chain depends on and it has never seen a real disconnect.
13. `Conn()` (`app.go:113`) returning a live `*wsconn.Conn` to `wirePages`
    only when the initial dial succeeds — the `conn == nil` branch
    (`main.go:74`, "leaves every page in its own honestly-labeled
    not-wired state") has never been observed on screen.

### `web/pages/auth` — login, recovery (`login.go`, `recover.go`)

14. `LoginPage`: focus moves from the password step to the TOTP step on
    `handleContinue` (`login.go:91`, `focus.FocusSelector("#"+totpID)`) —
    the exact behaviour named in the task brief; never watched.
15. `LoginPage`: `handleBack` (`login.go:100`) returns focus to the
    password field.
16. `LoginPage`: on submit failure, focus moves to `#errorID`
    (`login.go:125`) and `announcer.Assertive` fires (`login.go:124`) —
    never watched/heard.
17. `LoginPage`: on TOTP-step entry, `announcer.Polite` fires
    (`login.go:90`) — never heard.
18. `LoginPage`'s once-a-second re-render (`login.go:54-71`, a
    `time.NewTicker` inside `ui.UseMount`) actually causing the backoff
    countdown text to visibly count down — never watched.
19. `LoginPage`'s password step `Input` has no `Aria("invalid", ...)` on a
    rejected attempt (`login.go:171-178`) — only the separate error `<p>`
    changes; whether that reads correctly to a screen reader as "this
    field is invalid" (vs. just "a paragraph elsewhere changed") is
    unverified.
20. `RecoverPage` (`recover.go`): four separate `go func` RPC calls
    (lines 55, 95, 144, 180) and three `announcer.Assertive`/`.Polite`
    calls (lines 103, 114, 158, 187) driving a multi-step recovery flow
    (choose action → verify → new password/TOTP) — none executed against
    a live server.
21. `RecoverPage`'s `provisioningURI` state (`recover.go:44`) presumably
    feeding a QR code or displayable URI after re-enrollment — never
    rendered.

### `web/pages/generate` (`render.go`, `render_editor.go`,
`render_rail.go`, `render_sampler.go`)

22. `render.go:36`'s `state.UseAtomKey(shell.SessionAtom)` read inside the
    page's actual render call, and the `DraftDirty`/dirty-check
    registration it feeds — never exercised with a live session-expiry
    event while mid-edit.
23. Nine separate `go func` RPC call sites (`render.go` lines 105, 163,
    179, 216, 238, 327, 380, 407) covering feed load/save/sample/apply —
    none executed against a live server; every success/error branch's DOM
    effect is unverified.
24. `render_editor.go:356`'s `h.MapKeyedComponent(rows, ...)` for the
    dynamic source-list editor — per-row component identity across
    add/remove/reorder (does row 3's local state follow row 3's data or
    stay pinned to position 3?) never watched.
25. `render_editor.go:446`'s `MapKeyedComponent` over `FieldDiff` rows for
    the conflict-resolution panel (`renderConflictFieldRow`, line 459) —
    never rendered; this is the merge-conflict UI, one of the more
    complex interactions in the app.
26. `render_rail.go:61`'s `MapKeyedComponent` over the feed list rail —
    selection state surviving a feed being added/removed elsewhere never
    watched.
27. `render_sampler.go:176`'s `MapKeyedComponent` over sample-candidate
    tabs (`renderCandidateTab`) plus per-tab hook isolation
    (`render_sampler.go:186` comment: "hook-free, aside from the isolated
    MapKeyedComponent...") — tab switching/keyboard behaviour never
    watched.
28. Whatever keyboard pattern the sampler's tab strip implements (arrow-key
    tab traversal is the WAI-ARIA expectation for a tablist) — unverified
    either way; the source doesn't show it being built explicitly, so this
    is a candidate gap, not just an untested feature.

### `web/pages/history` (`items_ui.go`, `runs_ui.go`, `confirm_ui.go`,
`forms_ui.go`, `wiring.go`)

29. `wiring.go:94`'s `UseAtomKey(shell.SessionAtom)` read + `disabledReason`
    derivation for the KILLED state, and the page-granularity dirty-check
    registered unconditionally every render (`wiring.go:90-92`) — the
    comment admits this trades precision for safety ("an occasional
    unnecessary prompt... in exchange for never silently discarding an
    open edit"); whether that tradeoff is even survivable UX (a prompt on
    every navigation away from a merely-open, unedited correction panel)
    has never been seen.
30. `items_ui.go:368`'s `h.MapKeyedComponent(s.items, ...)` per-row items
    table, each row apparently owning its own correction-panel open/closed
    state (`items_ui.go:415-418`: `open`, `title`, `summary`, `body` —
    comment at 361-363 explicitly calls out this must be
    `MapKeyedComponent` not `MapKeyed` for exactly this reason) — never
    watched open two rows' panels independently, or watched one row's
    panel survive/not-survive a filter change that reorders the list.
31. `items_ui.go`: five `go func` RPC sites (lines 108, 150, 180, 194, 209)
    for filtering/paging/correcting/deleting items — none executed live.
32. `runs_ui.go`: three `go func` sites (92, 119, 135) for run list load
    and delete-confirm — none executed live; `pendingDelete` state
    (`runs_ui.go:82`) driving `confirm_ui.go`'s confirmation UI never
    watched.
33. `web/ui.Table`'s horizontally-scrolling container
    (`web/ui/table.go:96-100`) being reachable and operable via keyboard —
    `TabIndex(0)` plus a focus ring is present in source; whether Tab
    actually lands on it, and whether arrow/PageDown scroll it once
    focused, is unverified (browsers vary in whether a focusable
    `overflow:auto` div responds to arrow keys without extra JS).
34. `Kebab`'s (`web/ui/kebab.go`) per-row menu on an items/runs table row:
    trigger → open → `AccessibleOverlay` trap-focus → Escape closes and
    **restores focus to the trigger** (`RestoreFocus: true`,
    `kebab.go:184`) — never watched; this is the exact "kebab returns
    focus to its trigger on Escape" behaviour named in the task brief, and
    it depends entirely on the pinned GWC library's `AccessibleOverlay`
    doing what its option name says, which this codebase cannot verify by
    reading its own source.
35. `Kebab`'s menu clamped to `calc(100vw - 32px)` under `narrowMedia`
    (`kebab.go:172-175`) at phone width, and whether `AccessibleOverlay`'s
    anchored positioning actually flips to stay on-screen near a viewport
    edge — flagged in-source as unverifiable without a browser
    (`kebab.go:153-161`).

### `web/pages/settings` (`render_about.go`, `render_data.go`,
`render_generation.go`, `render_provider.go`, `render_publishing.go`,
`render_security.go`, `wiring.go`)

36. Six sub-pages, each with its own `ui.UseEffect`-triggered `go func`
    load-on-mount (About: line 30; Data: 44; Generation: 33; Provider: 32;
    Publishing: 36; Security: 63) — 12+ RPC round trips across the
    Settings tab strip, none executed live.
37. `render_data.go`: TOML export (line 74), TOML import (90), and backup
    download (110) — three more `go func` RPC sites; the import flow in
    particular (paste TOML → validate → apply) has multiple client-visible
    states (`importVisible`, `importErr`, `importOK`) never watched
    transition.
38. `render_security.go`: change-password (line 91), TOTP re-enrollment
    (110, showing a fresh `provisioningURI`/QR at line 43), recovery-code
    regeneration (129, `render_security.go:302`'s standalone `loadSessions`
    call), and session list load (63) — the most security-sensitive
    surface in the app, entirely unexercised. Re-enrollment in particular
    is a "scan a new QR with your authenticator app" flow that cannot be
    partially faked; it needs a real authenticator.
39. `render_security.go`'s session table (`sessions` state, line 23) —
    "IsCurrent" row styling/labeling and revoke-session actions never
    watched, including whether revoking *this* session correctly forces
    the D0-08 expiry-modal/logout path rather than silently 401ing the
    next RPC.
40. `settings/wiring.go:118`'s sign-out flow (`phase`/`signOutErr` state,
    `go func` at line 137) — never watched, including what the sign-out
    button does while the RPC is in flight (loading state, double-click
    protection).
41. Settings tabs (`web/ui/tabs.go`) — keyboard behaviour (arrow-key
    switching, the WAI-ARIA tablist pattern) unverified the same way as
    generate's sampler tabs (#28).

### `web/ui` primitives, used by every page above

42. `web/ui.Modal` (`modal.go`) — the *accessible* modal primitive that
    `renderExpiryModal` (finding #10) conspicuously does **not** use.
    Whatever focus-trap/restore-focus/Escape behaviour it does implement
    is itself unverified in a real browser.
43. `web/ui.Toast` (`toast.go`) — Danger/Warning toasts announced
    assertively, others politely (`toast.go:54-56`), auto-registering an
    `announcer.Region()` (line 62) — never seen or heard.
44. `web/ui.Confirm` (`confirm.go`) — the shared destructive-action
    confirmation dialog (feed delete, item delete, run delete all route
    through this) — never opened.
45. `web/ui.Toggle`, `web/ui.Select`, `web/ui.Input` — controlled-value
    round-tripping on real DOM elements (typing in a text input while a
    re-render happens mid-keystroke — the "controlled input clobbered on
    re-render" trap class) is unverified for every text field in the app;
    `login.go`'s password/TOTP inputs (`h.Value(f.Password)`,
    `h.Value(f.TOTPCode)`) are the highest-value ones to check first since
    a clobbered login field would be a hard blocker (see Part 2, #3).
46. `web/ui/responsive.go`'s `narrowMedia` breakpoint and every
    `css.Media`-gated rule across `web/ui` and `web/tokens` — real
    viewport-width behaviour (not just that the Go values compile) is
    unverified.
47. `focusVisible()` (`web/ui/base.go:20`) — the focus-ring CSS applied
    across buttons/inputs/kebab items/table scroll containers — whether
    it actually shows on `:focus-visible` (keyboard) and not on mouse
    click is unverified.

### `web/i18n`, `web/tokens` mounting

48. `gwci18n.Provider` actually mounting the one real `Bundle` and making
    `gwci18n.UseI18n()` resolve real translated strings in a live tree
    (rather than the `fallbackTranslator`/`fallbackCatalog` every page
    package falls back to before `Init` runs) — never watched; a mistake
    here would silently degrade every string on every page to its raw key
    (D6-07 behaviour, by design, but never confirmed it's not what ships).
49. `web/tokens/theme.go`'s CSS custom properties (`extraTokens`,
    `theme.go:283`) actually landing in the page's stylesheet via GWC's
    `css.New`/registry and being visible as real computed styles — the
    token *values* are host-tested (`tokens/contrast_test.go`,
    `motion_test.go`), but "the DOM actually looks like the tokens say"
    is not.
50. `color-scheme: light dark` in `index.html:31` plus whatever dark-mode
    styling `web/tokens` derives from it — never viewed in either OS theme.

### Bundle load itself

51. Actual wall-clock time from navigation to first paint of *anything*
    meaningful, on whatever connection the operator has — unmeasured (see
    Part 2, #4, for the concrete numbers).
52. `WebAssembly.instantiateStreaming` failure path (`index.html:111-115`)
    — the plain-text fallback message — never triggered/seen.
53. Whether the admin host's deployed reverse proxy actually serves
    `app.wasm` with `Content-Encoding: gzip` against `app.wasm.gz` as
    `index.html`'s comment says is "a server-side decision, out of scope
    for this static shell" (`index.html:98-106`) — `StaticHandler` itself
    does the negotiation correctly by inspection
    (`internal/publish/static.go:188-193`), but nothing has confirmed the
    admin listener in front of it doesn't strip or duplicate that header.

### Everything else that only compiles under the tag

54–61. `web/pages/history/mutationerror_js.go`, `web/shell/mutationerror_js.go`
(mutation-error → toast/banner mapping in a live tree), `web/wsconn/clients.go`
(typed RPC client construction against a real bridge connection),
`web/pages/auth/wiring.go`/`web/pages/generate/deps.go`/
`web/pages/settings/deps.go` (each page's real `Init` being called with
real deps rather than its `fallbackTranslator`), and `web/shell/pages.go`'s
`pageComponent` dispatch table actually rendering the right page body per
route — each is exercised only by host tests against fakes, never against a
mounted, routed, RPC-connected instance.

---

## Part 2 — most likely to be broken, ranked

### 1. The DISCONNECTED banner has no live region at all (High confidence — read directly)

`web/shell/banner.go:115-122` returns:

```go
return h.Div(
    h.ClassStr(h.ClassMap(map[string]bool{...})),
    h.Text(t.T(keyBannerReconnecting, gwci18n.Arguments{"seconds": secondsLeft})),
)
```

No `html.Aria("live", ...)`, no `html.Role("status"/"alert")`. `pages.go:106`
mounts it as a bare `ui.CreateElement(renderBanner, nil)` with no wrapping
live region either. The task brief's own framing — "announces at start and
completion but not every tick" — describes what D0-09/§12.6 *ask for*; the
code as written announces **never**. Contrast this with `web/ui/toast.go`
and `web/pages/auth/login.go`, both of which correctly wire
`gwcui.UseAnnouncer()`/`ui.UseAnnouncer()`. The banner is the one
disconnect-notice surface in the app and it is silent to a screen reader —
this is a straightforward, cheap-to-verify, cheap-to-fix miss, not a subtle
runtime trap. Verify by killing the control plane mid-session with a screen
reader running (or a browser's accessibility tree inspector) and confirming
nothing is announced.

### 2. The session-expiry modal is not a real modal (High confidence — read directly)

`web/shell/expiry.go:40-50` builds the blocking "your session expired,
unsaved work held" overlay as a bare `h.Div` with CSS classes
(`af-expiry-modal af-expiry-modal--visible`), not `web/ui.Modal`
(`web/ui/modal.go`) and not `gwcui.AccessibleOverlay` (the primitive
`web/ui/kebab.go` and `web/ui/confirm.go` both use for exactly this kind of
overlay). No `role="dialog"`, no `aria-modal`, no focus move into it on
open, no focus trap, no Escape handling. This is the one surface D0-08
explicitly built to stop silent data loss, and by inspection a keyboard
user can Tab straight through it into the (stale) page behind — potentially
letting them keep "editing" a form whose session is already gone. Verify by
triggering a session expiry while a form is dirty (kill the session server-
side or wait out the TTL) and Tab-cycling through the page.

### 3. Controlled-input clobber on the auth forms (Medium confidence — pattern match against a known GWC trap)

`web/pages/auth/login.go` and `recover.go` hold form state in `ui.UseState`
and feed it back as `h.Value(f.Password)`/`h.Value(f.TOTPCode)` every
render, with a 1-second ticker (`login.go:54-71`) forcing a re-render every
second purely to advance the backoff clock. This is precisely the shape
that trips GWC's controlled-input-clobber trap (a value-controlled input
whose owning component re-renders for an unrelated reason can have the DOM
value reset out from under an in-progress keystroke) — and login is the one
page D1-01 calls "must never break." It has never been typed into against a
live re-render tick. Verify by typing slowly into the password field and
watching for a dropped/reset character right as the countdown tick lands.

### 4. The 30 MB / 6.2 MB gzip bundle has zero loading feedback (High confidence — read directly, measured)

`web/dist/app.wasm` is 31,212,253 bytes (30 MB) uncompressed,
`app.wasm.gz` is 6,410,166 bytes (6.2 MB) as staged by `scripts/build-web.sh`
→ `web/build.sh` right now. `index.html:89-116` renders an empty
`<div id="app">` and does nothing else — no spinner, no progress bar, no
text — until `WebAssembly.instantiateStreaming(...).then(go.run)` resolves.
On the IP-allowlisted admin host (not a CDN) this is a multi-second blank
page even on a good connection, and on a bad one is indistinguishable from
a hung tab. This is a cheap, high-value first fix and the first thing any
walkthrough will hit (see J1 below).

### `UseAtomKey` panic risk — checked, currently low (worth stating, not burying)

All four non-test call sites (`generate/render.go:36`, `history/wiring.go:94`,
`settings/render.go:26`, `shell/banner.go:70`, `shell/expiry.go:34`) are
directly inside a package's `Render`/component function, called
synchronously on every render — not from a goroutine, timer, or event
handler closure. Every `go func` RPC call site found across `web/`
(len(`grep -c "go func"`) ≈ 45) calls only `.Set()` on a `ui.State`/atom
handle already captured by closure from the render — never a hook
constructor (`ui.UseState`, `state.UseAtomKey`, `ui.UseEffect`) from inside
the goroutine. By static reading, this specific trap is not currently
tripped anywhere in `web/`. It stays worth a first-session smoke check
(J4/J6 below) because a single new call site added without noticing the
rule reintroduces it invisibly, and because `Set()`-from-goroutine scheduling
a re-render correctly, the first time it is exercised against a real event
loop, is itself unverified (Part 1, #23 and friends).

### `css.Rule`-as-child and `Value("")`-omitted traps — not found in this codebase

Grepped every `css.Class`/`css.New` call site in `web/ui`, `web/tokens`,
and every page package: all pass `[]css.Rule{...}` or `...css.Rule` into
`css.Class`/`css.New`, never a bare `css.Rule` as a JSX-like child argument
to an element constructor. Grepped every `html.Value(...)` call site
(`web/ui/input.go:78`, `web/ui/select.go:82`): both always pass a live
string variable, never a hardcoded `""` meant to force-clear a field.
Neither trap is currently present by inspection — flagging them in the
walkthrough (J5) anyway, since a regression here is invisible to any host
test and the two known offending files (`login.go`'s two inputs) are cheap
to re-check.

### Per-row hooks in a bare `Map` loop — not found

Every dynamic list in `web/` (source rows, conflict-diff rows, feed rail,
sampler tabs, items table) uses `h.MapKeyedComponent`, never a bare `.Map`
with hooks called inside the per-item closure. `web/pages/generate/nodeutil.go`
and multiple render files carry explicit comments enforcing this
(`render_editor.go:29,342`, `items_ui.go:361-363`). This is the one trap
class the codebase was clearly built defensively against; still worth one
visual check (J7) that per-row local state (e.g. an open correction panel)
actually follows the *row's data identity*, not its screen position, when
the underlying list is filtered/reordered — `MapKeyedComponent` being used
correctly doesn't by itself prove the key function (`it.Id`) is stable
across every mutation path.

**Top three, in order: (1) banner has no live region — cheapest to both
verify and fix; (2) expiry modal is not a real modal — highest consequence
if wrong, since it exists specifically to prevent silent data loss and
currently by inspection does not trap focus; (3) auth-form controlled-input
clobber risk — highest consequence page ("must never break") combined with
the specific re-render-on-a-timer shape known to trigger this trap
elsewhere in the GWC family.**

---

## Part 3 — the walkthrough script

### Setup (verified against the tree, not assumed)

1. **Build the bundle.** `make web` (`Makefile`'s `web` target) runs
   `sh scripts/build-web.sh`, which execs `web/build.sh`. That script
   compiles `GOOS=js GOARCH=wasm go build ./web` into an isolated scratch
   dir, gzips it, copies `wasm_exec.js` from `$(go env GOROOT)`, and
   atomically stages `app.wasm`, `app.wasm.gz`, `wasm_exec.js`, and
   `index.html` into `web/dist/` (`SERVE_DIR` overrides). Confirmed
   present and current: `web/dist/` already has all four files
   (last built today, 31,212,253-byte `app.wasm`). Note from the
   Makefile's own comment: `web` is "not wired into `all`/CI yet" — it
   must be run explicitly.
2. **Static handler must be mounted on the admin plane.** Confirmed in
   `cmd/animefeedflux/wire.go`: `newAdminMux` (around line 1032) calls
   `publish.NewStaticHandler(cfg.AdminStaticDir)` and wires the result into
   `adminMux` alongside the GoGRPCBridge handler; the admin listener binds
   `cfg.AdminAddr`, a separate `*http.Server` from the public
   `publishSrv` (`wire.go:1091-1093`, "Two listeners, never one mux").
   `cfg.AdminStaticDir` defaults to `web/dist` (`internal/config/config.go:147`,
   `DefaultAdminStaticDir`), overridable via `AFF_ADMIN_STATIC_DIR`. If
   `NewStaticHandler` fails (directory missing), the admin listener logs a
   warning and serves the control-plane API with **no UI at all**
   (`wire.go:1032-1034`) rather than failing startup — check the server log
   for that line before assuming step 1 worked.
3. **`aff admin init` must have been run** against the same DB the server
   uses. Confirmed in `cmd/aff/admin_cmd.go`: `cmdAdminInit` requires
   `--db`/`AFF_DB_PATH` (local filesystem access to the SQLite file) and
   `AFF_SECRET_KEY` in the environment; it refuses to run if an admin
   already exists (`store.ErrAdminExists`). Run it once, capture the
   printed TOTP provisioning URI and recovery codes — **shown once** — and
   enroll them in a real authenticator app now, before starting the
   browser session, since login (J2) and TOTP re-enrollment (J9) both need
   a working authenticator.
4. **A feed must exist.** Confirmed in `cmd/aff/feed_cmd.go`:
   `aff feed create` (`cmdFeedCreate`, dispatched from `cmdFeed`'s `"create"`
   case). Create at least one feed before J3–J5 so `/generate` and
   `/history` have something to load instead of an empty-state screen —
   an empty list and a broken list render identically to a quick glance,
   so this step matters for the walkthrough to mean anything.
5. **Start the server** (`make build && ./bin/animefeedflux`, or however
   it's normally run) with `AFF_ADMIN_ADDR`, `AFF_ADMIN_STATIC_DIR` (if not
   default), `AFF_SECRET_KEY`, and the admin IP allowlist all pointing at
   the machine the browser will run on.

### The walkthrough (ordered cheapest/most-blocking failure first)

**J1 — does the bundle load at all.**
Navigate to the admin host. Watch: does `#app` stay visibly blank for
several seconds (expected, per Part 2 #4 — there is currently no loading
indicator) and then something appears? Open DevTools Network tab: confirm
`app.wasm` request completes with `Content-Encoding: gzip` and roughly
6.2 MB transferred, not 30 MB (this depends on the reverse proxy honoring
what `StaticHandler` already negotiates — Part 1 #53). **Wrong** =
console error from `WebAssembly.instantiateStreaming`, the
`index.html:113` fallback text ("Failed to load the admin app..."), a
perpetually blank page past ~15s, or a 30 MB transfer. If this fails,
nothing past this point can be tested.

**J2 — does the control-plane connection come up.**
Open DevTools Network/WS tab: confirm a WebSocket (GoGRPCBridge) connects
to the admin listener's `/ws`-or-similar path. Watch for the boot-time
`Session` RPC (`app.go:170`) resolving within the 5s `initialSessionTimeout`
— **wrong** = the app sits inert past 5s with no banner, or the banner
shows `Disconnected` immediately despite a healthy server. If the socket
never connects, every page below renders its `renderNotWired`/fallback
state and nothing else is testable except the shell chrome itself.

**J3 — login, step 1 → step 2 (Part 1 #14, #17; Part 2 #3).**
At `/login`, type the admin password slowly, pausing for at least one full
second mid-word (the countdown ticker forces a re-render every second —
Part 2 #3). **Wrong** = a character drops or the field visibly resets.
Submit. **Wrong** = anything other than focus landing on the TOTP field and
(if a screen reader/accessibility inspector is running) a polite
announcement of the TOTP step label.

**J4 — login, step 2 → session (Part 1 #16, #18).**
Enter a wrong TOTP code once: confirm focus moves to the error message and
an assertive announcement fires (Part 1 #16); confirm the backoff notice,
if any, visibly counts down rather than staying frozen (Part 1 #18). Then
enter the correct code: confirm navigation to `/generate` and that Back
does not return to a stale login form (D1-05).

**J5 — one full round trip on `/generate` (Part 1 #22-23, #26; Part 2's
"not found" traps).**
Select the feed created in setup step 4. Confirm it loads (the source-list
editor, `render_editor.go`'s `MapKeyedComponent` rows). Edit one field,
save, confirm the save error/success path renders correctly and the field
is not silently reverted. While here: View Source / DevTools Elements —
confirm no literal `css.Rule`-shaped text appears anywhere in the rendered
DOM (Part 2's css.Rule-as-child check), and that no input you didn't touch
shows a value stuck from a previous state (Value("") check).

**J6 — DISCONNECTED banner and reconnect (Part 1 #4-7, #12; Part 2 #1).**
Kill the server process (or block the admin port). Confirm the banner
appears, the countdown visibly ticks, and — with an accessibility inspector
open — confirm whether anything is announced at all (expected, per Part 2
#1: currently nothing). Restart the server; confirm the banner disappears
and the app resumes without a full page reload.

**J7 — history table, kebab, and per-row state (Part 1 #30, #33-34; Part
2's per-row-identity check).**
On `/history`'s items tab, open one row's correction panel (kebab menu →
action, or whatever the row-level entry point is). Confirm: the table's
scroll container is Tab-reachable and scrolls with arrow keys once
focused (Part 1 #33); opening the kebab traps Tab inside the menu and
Escape returns focus to the trigger button, not somewhere else (Part 1
#34); applying a filter that reorders/removes rows doesn't leave a
different row's panel open than the one you opened (per-row identity
check).

**J8 — settings surfaces and destructive actions (Part 1 #36-40; #44).**
Walk each Settings sub-tab once, confirming its load-on-mount state
resolves to real data, not a stuck spinner. Trigger one destructive action
behind a kebab (per CashFlux-style convention, if mirrored here) and
confirm `web/ui.Confirm` actually opens and blocks the action until
confirmed.

**J9 — session expiry while dirty, and TOTP re-enrollment (Part 1 #10-11,
#38; Part 2 #2).**
Start editing a field on `/generate` (making it dirty), then force a
session expiry (revoke the session server-side, or wait out the TTL if
short enough). Confirm the expiry modal appears and — the key check —
Tab-cycle through the page while it's open: **wrong** = focus reaches
anything behind the modal (expected to fail, per Part 2 #2, since it's not
built on the accessible-overlay primitive). Then go to Settings → Security
and re-enroll TOTP for real: scan the new QR with the same authenticator
used in setup, confirm the new code is accepted and the old one is not.

### What this session can and cannot establish

**Can:** whether the bundle loads and connects at all; whether the specific
named behaviours above render, focus, and announce correctly *once*, in
one browser, on one machine, against one feed and one operator account;
whether the three ranked Part 2 risks are real or were misread from static
analysis.

**Cannot:** `DOD-4`'s 30-day stability requirement — that needs the app
actually running unattended against real traffic and real reconnects over
weeks, which no single sitting can compress. Cannot substitute for a real
Slack workspace — nothing in this admin UI walkthrough touches Slack
delivery at all; that is a separate, already-noted constraint
(`slack-rss-app-constraints`) on the publish side, orthogonal to this
admin-UI gap. Cannot establish cross-browser behaviour (this script is
silent on which browser/OS to use — run it in whatever the actual
operator will use, since `AccessibleOverlay`/focus-trap/announcer behaviour
is exactly the kind of thing that varies by browser). Cannot establish
long-session memory behaviour (a WASM heap growing over a multi-hour
session) — one sitting is too short to notice that even if it's present.
