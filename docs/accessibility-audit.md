# `web/ui` accessibility audit — 2026-08-10

Scope: `web/ui`'s component library, audited against the six-state matrix
(`TODOS.md` D-FLOW / D0-14 / D0-23) and the keyboard/contrast requirements in
D5. Everything below is scoped to the kit itself; per HARD RULES for this
pass, `web/pages/*`, `web/shell/`, `web/tokens/`, `web/i18n/`, `web/main.go`
and `internal/` were read for context but not edited.

## 0. The fact that governs everything else here

`web/ui` had **zero production importers** until today. Checked by grep:

```
web/pages/generate/*.go  → wui.Button, wui.Input, wui.Select, wui.StatePanel,
                            wui.Tabs, wui.Toggle, wui.T
web/pages/settings/*.go  → ui.Button, ui.Confirm, ui.Input, ui.Kebab,
                            ui.StatePanel, ui.Table, ui.Tabs, ui.Toggle
web/pages/auth/*.go      → no web/ui import
web/pages/history/*.go   → no web/ui import
```

So the accessibility work that lives *in this package* — focus trap/restore
via `AccessibleOverlay` (`modal.go`, `kebab.go`), roving-tabindex arrow-key
navigation on tabs (`tabs.go`, via `gwcui.UseCompositeNavigation`), the
keyboard-reachable table scroll container (`table.go`'s `tabindex="0"` div),
and `focusVisible()`'s contrast-tested focus ring — **only reaches the
screen on `/settings` and `/generate`.** `/login`, `/recover`, and
`/history` render their own controls and get none of it for free. Neither
adopting page uses `Modal` directly or `Toast` at all yet — `Modal`'s focus
trap is only reachable today through `Confirm`, which `settings` uses for
its destructive actions; `Toast` (referenced in its own doc comment as
showing "Sample promoted"/"Recipe saved") has no caller anywhere in the
tree.

`history`'s state handling is a second, parallel implementation
(`web/pages/history/screenstate.go`) with no `ui.` dependency, confirmed
already by the D0-14 note in `TODOS.md` — its six-state rendering is not
this package's `SelectListState`/`StatePanel`, so this audit's guarantees
about state precedence do not extend to it.

## 1. Six-state matrix — verified in `web/ui`

`state.go`'s `ListState` enum is closed to exactly the six states named by
D-FLOW (`loading`, `empty`, `populated`, `error`, `disabled-with-reason`,
`disconnected`) — there is no seventh value the type system allows.
`SelectListState` is a pure function (`ListStateInput → ListState`) with a
documented, tested precedence order (kill-switch reason > disconnected >
error > loading > empty > populated), and `state_test.go`'s
`TestSelectListState_Precedence` exercises that order directly — this is
verified by `go test`, not by reading. `StatePanel` renders exactly one view
per state and nothing falls through undefined.

What this does **not** prove: that every list on `/settings` and
`/generate` actually feeds `SelectListState` real connectivity/error/loading
signals rather than, say, always passing `Loading: false` — that's a
per-call-site wiring question in `web/pages/*`, out of this pass's edit
scope and not fully re-derivable by reading alone. `/login`, `/recover`, and
`/history` don't go through this matrix at all (§0).

## 2. Keyboard paths — verified by source reading, not by driving a browser

Verified by reading `web/ui` against the vendored
`github.com/monstercameron/GoWebComponents/v5@v5.0.1` source
(`internal/runtime/hooks.go`, `ui/ui.go`):

- **Every interactive primitive** (`Button`, `Input`, `Select`, `Toggle`,
  `Tabs`' tab buttons, `Table`'s scroll container, `Kebab`'s trigger and
  items, `Modal`'s close button, `Toast`'s dismiss button) composes
  `focusVisible()` — one shared `:focus-visible` outline rule, so there is
  one focus ring implementation to get right, not N.
- **`Tabs`** delegates arrow-key roving-tabindex navigation to
  `gwcui.UseCompositeNavigation`, wires `tabindex="0"` only onto the
  currently-active tab (`-1` on the rest) and forwards `onkeydown` to
  `nav.OnKeyDown`. Role/state wiring (`role="tab"`,
  `aria-selected`, `aria-controls`) is present and keyed off `p.ActiveID`.
- **`Table`**'s horizontal-scroll container carries `tabindex="0"` plus
  `focusVisible()` specifically so a keyboard user without a pointer can
  reach and scroll it — an `overflow:auto` box with no tabindex is
  otherwise mouse-only.
- **`Modal`/`Kebab`** build on `gwcui.AccessibleOverlay` with
  `TrapFocus: true`, `RestoreFocus: true`, `CloseOnEscape` (Modal only when
  not `Persistent`), rather than a hand-rolled fixed-position `div` — the
  trap/restore implementation is the library's, not re-derived here.
- **`Toast`** uses `gwcui.UseAnnouncer` to speak new toasts through a
  polite/assertive live-region pair, danger/warning toasts assertively.

What this does **not** and cannot prove without a browser: that
`AccessibleOverlay`'s trap actually holds under real Tab-key traversal in
Chromium, that `UseCompositeNavigation`'s arrow-key handling matches the
WAI-ARIA APG tabs pattern in practice, that focus visibly *lands* where
`RestoreFocus` claims it does after a modal closes, or that the announced
toast text is actually what a screen reader speaks. That requires driving
the app, and the app currently cannot be driven end to end (§4).

## 3. Contrast

`D5-04` is already ticked and automated: `web/tokens/contrast_test.go`'s
`TestColorContrast_AA_BothThemes` passes AA in both themes for the token
pairs it checks, including `RoleFocusRing` against `RoleBg`, `RoleSurface`,
and `RoleSurfaceRaised` (`contrast_test.go:76-78`) — exactly the three
backgrounds `focusVisible()` in `web/ui` is actually drawn over (page
background, card/panel surface, and modal/popover/menu surface
respectively). That closes the loop between the token layer's guarantee and
this package's one focus-ring implementation for the elements that use it.
Per `TODOS.md`'s own scope note on `D5-04` (see `D0-28`), this proves the
generated CSS rule text is AA-compliant; it does not by itself prove every
rendered element resolves to that rule outside `web/ui`'s two adopters.

## 4. What could not be verified in a browser this pass, and why

A dev server was confirmed live at `http://localhost:8082`; Playwright
reached it. `/` redirects to `/login`, which renders (title "AnimeFeedFlux
Admin", a two-step password→TOTP form, "Recover your account" link) and the
gRPC-over-WS bridge correctly refuses without credentials
(`WebSocket connection ... HTTP Authentication failed`). Per the brief,
login is being rebuilt right now and no working credentials were available
in this environment, so `/generate`, `/settings`, `/history`, and the
authenticated part of `/recover` are **not reachable this pass**. This
is the same blocker named for `D5-05`, and it also blocks any *browser*
verification of D5-01/02/03/06 beyond what's written above from source —
those items stay open for the same reason (§6).

## 5. Reported gaps, judged

**1. No `Textarea` primitive — REAL, fixed.**
Three page packages (`generate/render_editor.go`, `history/forms_ui.go` and
`items_ui.go`, `settings/render_data.go`) hand-roll a bare
`h.Textarea(...)` for every multi-line field (description, system/user
prompt templates, item summary/body, TOML import/export) with none of
`Input`'s label-for/help-or-error/`aria-describedby`/`aria-invalid`/focus-
ring wiring. That's a real, structural a11y gap, not a style preference —
every other field kind (`Input`, `Select`, `Toggle`) already gets this
treatment and `Textarea` was the one left out. Added `web/ui/textarea.go`:
`Textarea(TextareaProps)`, shaped to match `InputProps` (same
`LabelKey`/`HelpKey`/`ErrorKey`/`Mono` conventions, plus `Rows`), reusing
`input.go`'s `helpOrError`/`fieldLabelClass`/`fieldWrapClass`. It is **not**
wired into any page in this pass — that's a `web/pages/*` edit, outside
this ticket's scope — so it's exactly as unadopted right now as the rest of
`web/ui` was this morning (§0).

**2. `Button`/`Input`/`Select`/`Toggle` skip registering their `On*` hook
entirely when disabled — REAL, fixed.** Verified against the vendored GWC
source, not assumed: `html.OnClick`/`OnChange`/`OnInput` bottom out in
`ui.UseEvent` → `runtime.GoUseFunc`
(`v5@v5.0.1/internal/runtime/hooks.go:781`), which claims a **positional**
hook slot on the calling fiber (`parseHooks.index++`, `parseHooks.funcIndex++`)
and appends to a per-fiber slice keyed by that running index — the same
"hooks must be called unconditionally, in the same order, every render"
contract as `gwc-hooks-must-be-unconditional` describes for `UseState`/
`UseEffect`. Since `Button`/`Input`/`Select`/`Toggle` are plain Go functions
(not their own component boundary), their internal `html.On*` calls execute
against whatever fiber is currently rendering — typically the calling
page's. The old code (`if p.OnClick != nil && !isDisabled { … }`) skipped
that hook call on renders where `Disabled`/`Busy` was true and included it
on renders where it wasn't, for a control whose disabled-ness routinely
changes render-to-render (a save button during a save, a field mid-mutation
under a kill switch, a busy-until-confirmed action) — which shifts every
hook slot claimed *after* that call within the same render, misaligning
handler identity and any hooks other primitives register later in the same
fiber (e.g. `Input`/`Select`/`Toggle`'s own `gwcui.UseId()` fallback, or a
sibling `Button`'s own handler). Fixed in `button.go`, `input.go`,
`select.go`, `toggle.go`: the `html.On*` call is now made every render
whenever the prop callback is non-nil, with the `Disabled`/`Busy` guard
moved *inside* the callback instead of around the hook registration.
Functionally identical (the native `disabled` attribute already stops the
browser from ever dispatching the event), but no longer hook-order-unsafe.
Found and fixed the same bug in a fifth place while in the file:
**`kebab.go`'s per-item `OnSelect` handler** had the identical
`onSelect != nil && !it.Disabled` gate; same fix applied.

**3. `Tab` has no `LabelArgs`, so an interpolated label is inexpressible —
REAL, fixed.** Every other label-bearing prop in this package
(`ButtonProps.LabelArgs`, `KebabProps.LabelArgs`, `ToastItem.MessageArgs`,
`ConfirmProps.MessageArgs`) carries an `Args` companion; `Tab` was the one
struct that didn't, and `resolve(p.T, t.LabelKey)` was called with no way to
pass one — a tab label needing a count or name interpolated in (e.g. a
"Runs (12)" style label) had no path through this primitive. Added
`Tab.LabelArgs []any`, threaded through both call sites in `tabs.go`
(the `CompositeItem.Text` build and the tab button's visible label).

**4. `StatePanelProps.DisabledReasonKey` has no companion `Args` field —
REAL, fixed.** Same shape as #3: `disabledWithReasonView` called
`resolve(t, reasonKey)` with no args, while the doc comment on
`ListStateInput.DisabledReasonKey` explicitly describes the reason as
naming *why* something is disabled (a kill switch, a budget ceiling) —
exactly the kind of message that plausibly needs a figure or a time
interpolated in. Added `StatePanelProps.DisabledReasonArgs []any`, threaded
into `disabledWithReasonView`. (`Toggle.DisabledReasonKey` has the same gap
and was not in the reported list; left alone this pass to stay inside the
reported scope, but it's the same class of omission if it comes up again.)

## 6. `D5-*` disposition

- **`D5-01`** (responsive breakpoints land with each layout) — left open.
  Verified true *inside* `web/ui`: every primitive with narrow-width
  behavior (`Table`, `Tabs`, `Modal`, `Kebab`, `Toast`) carries its
  `narrowMedia(...)` override in the same file as the layout it modifies,
  gated through the single `NarrowMaxWidth`/`narrowMedia` chokepoint in
  `responsive.go` (unit-tested: `responsive_test.go`). Not verified: page-
  level layouts in `web/pages/*`, and no way to confirm the *behavior*
  (not just the CSS's existence) without a browser at real viewport widths.
- **`D5-02`** (audit every list against the six-state matrix) — left open.
  `web/ui`'s matrix is closed and tested (§1), but `/login`, `/recover`,
  and `/history` don't route through it at all (§0), and `history`'s
  `screenstate.go` is a documented parallel implementation. "Every list"
  is not yet true.
- **`D5-03`** (keyboard path through every journey, visible focus) — left
  open. The building blocks are in place and read correctly against the
  GWC source (§2), but "every journey" (`J1`-`J9`) cannot be walked without
  reachable `/login` (§4), and two of the nine journeys' surfaces
  (`/login`, `/history`) don't use this kit regardless.
- **`D5-04`** — already ticked; unchanged this pass (§3 double-checked it).
- **`D5-05`** (walk `J1`-`J9` in a browser) — **left open**, per instruction:
  login is being rebuilt and the journeys are not reachable (§4).
- **`D5-06`** (nothing reaches a state the flow table doesn't name) — left
  open. True by construction for `web/ui`'s own `ListState` (§1: the enum
  admits no seventh value), but the application-level states (`ANON` /
  `AUTH` / `ELEVATED` / `DISCONNECTED` / `KILLED`) live outside `web/ui`
  and were not re-audited this pass.

## 7. Keyboard walkthrough — to execute once `/login` is reachable

Concrete, so whoever picks this back up doesn't have to re-derive it. Run
with a mouse physically disconnected or simply never touched.

1. **Load `/login`.** Tab from the top of the page. Confirm focus lands on
   the password field first (not the browser chrome/skip link), each
   Tab moves to exactly one next control, and every stop shows the
   `focusVisible()` ring (`RoleFocusRing`, verified-AA per §3). Submit the
   password with Enter, not a click.
2. **TOTP step.** Confirm focus moves to the 6-digit code field
   automatically (or is reachable in one Tab), Enter submits, and a wrong
   code surfaces an error that is announced (not just colored) before
   focus is confirmed to still be on/near the code field, not lost to
   `<body>`.
3. **Land on `/generate` (J1).** Tab through the rail, editor fields, and
   sampler. For every `wui.Select`/`wui.Toggle` reached: Toggle should
   respond to Enter/Space (it's a real `<button role="switch">` per
   `toggle.go`, not a styled `<input>`), and confirm `aria-checked` flips
   in the accessibility tree (devtools, not just visually).
4. **`wui.Tabs` on `/generate` or `/settings`.** Focus the tablist, then
   use **ArrowRight/ArrowLeft** (not Tab) to move between tabs — Tab should
   move *out* of the tablist entirely after landing on the active tab
   (roving tabindex: only the active tab has `tabindex="0"`). Confirm
   wraparound at the ends if `Loop: true` is set at the call site.
5. **`ui.Table` on `/settings`.** Tab to the table's scroll container
   directly (it's `tabindex="0"`); confirm arrow keys or Home/End scroll it
   horizontally when the table is wider than its box, and that the
   container itself shows a focus ring (not just its contents).
6. **`ui.Kebab` on a `/settings` row.** Tab to the "⋯" trigger (confirm a
   screen reader/accessibility-tree inspection announces the *interpolated*
   `LabelArgs` name, e.g. "Actions for {feed}", not a bare "⋯"). Press
   Enter/Space to open — focus should move *into* the menu
   (`AccessibleOverlay`'s `TrapFocus`). Arrow through items, confirm
   destructive items are grouped last (`OrderKebabItems`) and visually
   distinct (danger color). Press Escape — focus should return to the
   trigger (`RestoreFocus: true`), not fall back to `<body>`.
7. **`ui.Confirm` from a destructive kebab item.** Confirm the modal traps
   Tab (cannot tab out to the page behind it), the confirm button stays
   disabled (and not just visually — actually unreachable/no-op) until the
   typed phrase exactly matches (`ConfirmMatches`), and Escape is *ignored*
   while `Busy` (`Persistent: p.Busy` in `confirm.go`) so an in-flight
   destructive call can't be dismissed out from under itself.
8. **DISCONNECTED (J9-adjacent).** With devtools open, throttle/kill the
   WebSocket while on `/generate` or `/history`-with-a-run-streaming.
   Confirm the disconnected view is reachable and announced
   (`disconnectedView`'s `role="status"`/`aria-live="polite"`), and that it
   does not silently swallow in-flight keyboard input.
9. **Reduced motion.** Enable `prefers-reduced-motion: reduce` at the OS
   level, reload, and confirm `spinner()` and `signalDot()` render as
   static (no `MotionOK`-gated keyframe animation) — this is a code-level
   guarantee (`css.Media(css.MotionOK, ...)`) worth a single visual spot
   check, not a keyboard step, but cheap to fold into the same pass.

## 8. Verification performed this pass

- `gofmt -l web/ui` — clean.
- `GOOS=js GOARCH=wasm go build ./web/...` — passes (the actual shipped
  target).
- `go build ./web/ui/...` and `go vet ./web/ui/...` — pass, native.
- `go test -count=1 ./web/ui/...` — pass (`TestConfirmMatches`,
  `TestOrderKebabItems`, `TestSelectBreakpoint`,
  `TestSelectListState_Precedence`, `TestListState_String`).
- Playwright against the live dev server at `:8082` — confirmed `/login`
  renders and the gRPC-over-WS bridge correctly refuses without
  credentials; confirmed `/generate`/`/settings`/`/history` are not
  reachable without them (§4).
