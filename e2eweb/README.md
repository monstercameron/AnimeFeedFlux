# e2eweb — browser verification harness for J1–J10

One file per user flow from `PLAN.md` §22 (`TODOS.md` `DF-01`..`DF-11`), driving the real admin
UI at `http://localhost:8082` and the real publish plane at `http://127.0.0.1:8081`. Built to close
the gap `docs/browser-verification-plan.md` documents: 61 behaviours that only execute under
`js && wasm` and had never run in a browser.

## This is NOT a Node package

There is deliberately **no `package.json`** in this directory. Playwright is borrowed from
CashFlux's install, not vendored here:

```bash
NODE_PATH=/c/Users/mreca/Desktop/CashFlux/node_modules node e2eweb/j1_login.js   # one journey
NODE_PATH=/c/Users/mreca/Desktop/CashFlux/node_modules node e2eweb/run-all.js    # the whole suite
```

Do not add a `package.json` or run `npm install` in this directory — that would create a Node
dependency this repo doesn't otherwise have. If Playwright ever needs to become a real dependency
of this repo, that's a deliberate decision for someone to make outside this harness, not something
to slide in by adding a lockfile here.

## What each file is

| File | Journey | Status as of 2026-08-10 |
|---|---|---|
| `j1_login.js` | J1 — first login, incl. wrong-password/wrong-TOTP/replay branches | **SKIP** — see below |
| `j2_create_feed.js` | J2 — create a feed, every validation error on-field | SKIP (needs AUTH) |
| `j3_iterate_prompt.js` | J3 — sample/read verdicts/edit/sample again, + kill-switch branch (DF-04) | SKIP (needs AUTH) |
| `j4_promote_sample.js` | J4 — promote a sample into the feed | SKIP (needs AUTH) |
| `j5_diagnose_run.js` | J5 — diagnose a bad run, reject reasons readable | SKIP (needs AUTH) |
| `j6_publish_correction.js` | J6 — publish a correction, not an edit | SKIP (needs AUTH) |
| `j7_recovery_drill.js` | J7 — full recovery drill | SKIP — reaches the code-entry step for real, then needs a recovery code |
| `j8_review_spend.js` | J8 — review spend, adjust a budget | SKIP (needs AUTH) |
| `j9_watch_run_live.js` | J9 — watch a run live, drop the WS mid-run, reconnect | SKIP (needs AUTH) |
| `j10_subscriber_lifecycle.js` | J10 — subscriber lifecycle over real HTTP | **RUNS FOR REAL** — no login needed |
| `lib/harness.js` | Shared runner: browser/HTTP journey wrappers, PASS/SKIP/FAIL reporting, screenshot-on-failure, TOTP | — |
| `lib/auth.js` | Shared login helper J2–J9 all call first | — |
| `run-all.js` | Runs every journey in sequence, prints a roll-up | — |
| `artifacts/` | Screenshots written on failure/skip-with-evidence (git-ignore this if it accumulates) | — |

## Why nine of ten are blocked, and it's not a guess

Login is being rebuilt right now (`git status` shows `web/pages/auth/{login,login_state,recover,
recover_state}.go` and `web/shell/{app,expiry,session}.go` all modified and uncommitted, plus new
untracked `devfill_on.go`/`devfill_off.go`/`styles.go`). Confirmed live against the running dev
server on 2026-08-10: navigating to `/login` does not render the password step at all — it renders
directly at "Step 2 of 2 — Authentication code", the body carries two stray leading `-0` text nodes
above the heading, and the browser console logs:

```
WebSocket connection to 'ws://localhost:8082/grpc' failed: HTTP Authentication failed; no valid credentials available
```

`j1_login.js` detects exactly this (missing "Password" label) and reports it as a `[SKIP]`, not a
`[FAIL]` — it's a known, expected, moving-target block, not a regression to chase. It captures a
screenshot to `artifacts/J1-login-blocked.png` and the console errors either way.

J2 through J9 all have an `AUTH` precondition per §22, so `lib/auth.js`'s shared `login()` helper
hits the same wall and every one of them skips with a message pointing back at this. **The moment
login lands, re-run `run-all.js` — no script edits should be required** for the guard to pass; each
file's "below the guard" code is the real, intended flow using real i18n label text (`Password`,
`6-digit code`, `Continue`, `Sign in`, …), not a stub.

`j7_recovery_drill.js` is the interesting exception: `/recover` currently *does* render its
"Recovery code" field for real (confirmed live) — it just then needs `AFF_E2E_RECOVERY_CODE` to go
further, which is a credentials problem, not a code-blocker one. Don't conflate the two SKIP
reasons; they mean different things and the harness's messages say which is which.

## Credentials for the login journeys

Once login lands, set before running:

```bash
export AFF_E2E_ADMIN_PASSWORD='...'          # from `aff admin init`
export AFF_E2E_ADMIN_TOTP_SECRET='...'       # the base32 secret from the same `aff admin init` output
export AFF_E2E_RECOVERY_CODE='...'           # ONE unused recovery code — J7 burns it on every real run
```

`lib/harness.js` implements TOTP itself (RFC 6238, HMAC-SHA1, ~20 lines) rather than pulling in
`otplib`/`speakeasy` — see "no npm package" above.

`j7_recovery_drill.js` deliberately stops short of actually submitting a new password: doing so
would rotate the admin credential every run and break every other journey's login for the rest of
that sitting. Run the last stretch (set new password, re-enroll TOTP, re-login) by hand once to
confirm it, rather than scripting a self-inflicted lockout into the suite.

## Design choices this suite makes on purpose

- **Real clicks, never dispatched events.** A click blocked by an invisible overlay (`hidden` +
  competing `display`) is exactly the kind of bug Playwright's real `.click()` reports precisely
  ("intercepts pointer events") and a `page.evaluate(...).dispatchEvent(...)` would silently sail
  through, passing a test that should fail. Every journey here uses Playwright locator actions
  (`.click()`, `.fill()`), never a dispatched synthetic event.
- **Readiness = `document.body.innerText` non-empty, never a fixed sleep.** `Journey.waitReady()`
  in `lib/harness.js` is the one readiness primitive every journey uses after a navigation. A fixed
  sleep either flakes on a slow machine loading a 31 MB WASM bundle or wastes time on a fast one.
- **Assert on rendered structure, not just presence of text.** Where §22 gives a checkable UI
  signal (a field-level error element, a disabled control with a visible reason, a distinct row in
  a table), journeys assert on that, and every failure captures a full-page screenshot — "13 CSS
  rules and nobody noticed from the source" is the failure mode a screenshot catches that a text
  assertion does not.
- **Every failure is self-contained.** `Journey.reportFailure()` always dumps: a screenshot, every
  console error since the journey started, and every failed or 4xx+/5xx network request — so a FAIL
  never needs a re-run just to see what broke.
- **SKIP ≠ FAIL.** A SKIP means "this is a known, named blocker, not a surprise" and does not set a
  failing exit code. `run-all.js`'s roll-up only counts FAILs. This is why the suite can be run
  today, honestly, with 9 of 10 journeys blocked, and still report cleanly.
- **J10 never restarts or blocks the shared server.** J9's WebSocket-drop branch simulates the drop
  with Playwright's `context.setOffline()` at the browser level, specifically so this suite never
  needs to touch the dev server process someone else owns.

## What this harness found already, just by existing

Running `j10_subscriber_lifecycle.js` against the live seeded feed
(`character-spotlight-weekly.xml`) surfaces a real defect, not a script bug: **the two newest items
share an identical `pubDate`** (`Sun, 09 Aug 2026 10:28:00 +0000`, twice), which fails §22 J10's
"every item has a unique, strictly decreasing pubDate" sanity assertion. Confirmed independently
with a bare `curl` before trusting the script — see the file's own inline comment. This is exactly
the class of bug this harness exists to catch cheaply and repeatably; it has not been investigated
or fixed here (out of scope — see the hard rules this harness was built under), only reported.

## Running the suite

```bash
NODE_PATH=/c/Users/mreca/Desktop/CashFlux/node_modules node e2eweb/run-all.js
```

Reads `AFF_ADMIN_URL` (default `http://localhost:8082`) and `AFF_PUBLISH_URL` (default
`http://127.0.0.1:8081`) if you need to point it elsewhere. Set `AFF_E2E_HEADED=1` to watch it run
instead of headless. Do not point this at a server this session doesn't already own — it does not
start or stop the dev server itself, by design (the dev server here is Cam's, not this suite's).
