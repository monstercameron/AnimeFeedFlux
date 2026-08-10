// Package auth implements AnimeFeedFlux's two unauthenticated admin pages,
// /login and /recover (PLAN.md §12.1/§12.2, TODOS.md D1-01..12, D6-10).
//
// # Layout
//
// Pure, host-testable logic (no browser, no network) lives in files
// without a build tag:
//
//   - login_state.go / recover_state.go — the two pages' form state
//     machines: which step is showing, what is editable, when a submit
//     is allowed, what a failure does to the client-side backoff
//     estimate. Modeled after web/appstate's Transition-with-ok-bool
//     style so the whole app's pure state machines read the same way.
//   - backoff_display.go — the client-side "try again in Ns" estimate
//     (D1-03) and why it can only ever be an estimate.
//   - client.go — the minimal request/response interfaces login.go and
//     recover.go depend on, satisfied structurally by
//     affv1.AuthServiceClient (gen/aff/v1, no build tag itself) without
//     this package importing web/wsconn (js-only, owned by the shell
//     wave) or hard-coding a concrete transport.
//
// Browser-rendering GWC components live behind `//go:build js && wasm` in
// login.go and recover.go. They hold no logic beyond wiring: translating
// DOM events into the pure state machine's transition functions, calling
// the injected client, and rendering the result.
//
// # Assumptions surfaced by reading the current server implementation
//
// These are documented here, not silently designed around, per this
// wave's brief ("report any §12.1/§12.2 assumption that did not hold"):
//
//  1. Login is ONE RPC, not two. gen/aff/v1's AuthService has a single
//     Login(password, totp_code) — there is no separate "check password,
//     get a challenge" call. PLAN.md §12.1's "password step then TOTP
//     step, one page" is therefore implemented as purely client-side
//     sequencing: the password is held in LoginForm across the step
//     change and only sent, together with the TOTP code, when the
//     second step submits. This is actually the SAFER reading of "no
//     oracle" (D1-02): since no RPC round-trip happens after the
//     password step, there is no server response at that point that
//     could leak anything about the password's correctness.
//
//  2. The server discloses NO backoff signal, ever. Read directly in
//     internal/rpc/auth.go: `s.backoff.blocked(ip, now)` returns the
//     exact same errAuthFailed as a wrong password, a wrong TOTP code, a
//     replayed TOTP step, or a missing admin row — confirmed by that
//     file's own TestLoginGenericFailureAcrossCauses, which asserts all
//     five causes are indistinguishable. So D1-03's "surface backoff
//     honestly" cannot mean "display the server's cooldown" — there
//     isn't one to display, by design, because surfacing it would itself
//     be exactly the kind of oracle D1-02 forbids (an attacker could
//     otherwise use backoff timing to confirm accumulating failures are
//     being counted, or not). backoff_display.go instead mirrors the
//     server's own backoffDelay(failures) formula (grace 2, doubling,
//     60s cap — internal/rpc/auth.go) against a LOCAL, per-form failure
//     count, and is explicit in its own doc comment that this is an
//     estimate the client computes, not a value the server ever sends.
//     It will drift from the server's real state under multi-tab use or
//     `aff` CLI attempts against the same account (the server tracks
//     backoff per-IP across every entry point, this package's estimate
//     only sees this one form's failures) — an acceptable inaccuracy
//     given the alternative is either no backoff UI at all or an actual
//     oracle.
//
//  3. An ELEVATED recovery session can complete ChangePassword OR
//     ReenrollTOTP, but not both, despite PLAN.md §12.2 reading as if it
//     grants both actions ("opens a ... elevated session that can only
//     reach 'set new password' and 're-enroll TOTP'"). Read directly in
//     internal/rpc/auth.go: both ChangePassword and ReenrollTOTP call
//     s.endElevatedSession(ctx, cs) on their `cs.Elevated` branch —
//     whichever runs FIRST ends the elevated session immediately, so a
//     second privileged call in the same session would already be
//     unauthenticated. recover.go reflects this honestly: after a
//     recovery code is accepted, the page offers "set a new password" OR
//     "re-enroll TOTP" as a single mutually-exclusive choice (see
//     recover_state.go's RecoverStepChooseAction), with page copy
//     (i18n key auth.recover.chooseActionIntro) stating that doing both
//     requires a second code, or break-glass access, which the plan text
//     also documents as doing both at once by design
//     (cmd/aff/admin_cmd.go's cmdAdminReset comment: "a partial reset
//     would leave the operator still locked out").
package auth
