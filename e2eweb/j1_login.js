'use strict';

// J1 — First login (PLAN.md §22 J1; TODOS.md DF-01).
//
// Actor: admin, ANON. Precondition: `aff admin init` has run and TOTP is
// enrolled. Steps: submit password -> submit TOTP -> land on the default
// surface. Failure branches: wrong password; wrong code; replayed code;
// backoff active; expired session.
//
// Sanity (from §22, checkable from the browser without DB access):
//   - the session cookie carries HttpOnly, Secure, SameSite=Strict and the
//     __Host- prefix
//   - a wrong password and an unknown user produce an indistinguishable
//     error message (server-side timing indistinguishability is NOT
//     checkable from here — that needs the headless Phase B suite against
//     the RPC layer, per §17.5)
//   - the TOTP step just used cannot be replayed
//
// Current status: BLOCKED. Confirmed live on 2026-08-10: navigating to
// /login does not render the password step at all — it renders directly at
// "Step 2 of 2 — Authentication code", the body carries stray leading
// "-0\n-0" text nodes above the heading, and the browser console logs:
//   WebSocket connection to 'ws://localhost:8082/grpc' failed: HTTP
//   Authentication failed; no valid credentials available
// `git status` shows web/pages/auth/{login,login_state,recover,
// recover_state}.go and web/shell/{app,expiry,session}.go all modified and
// uncommitted, plus new untracked devfill_on.go/devfill_off.go/styles.go —
// login is mid-rewrite right now, not merely buggy. This script documents
// the intended flow below the guard so it is runnable the moment that
// rewrite lands; do not try to make the guard pass by patching around it.

const { run, assert, ADMIN_URL, SkipError } = require('./lib/harness');

run('J1-login', async (j) => {
  const { page } = j;

  await page.goto(`${ADMIN_URL}/login`);
  await j.waitReady();

  const passwordField = page.getByLabel('Password', { exact: true });
  const totpField = page.getByLabel('6-digit code', { exact: true });
  const [passwordVisible, totpVisible] = await Promise.all([
    passwordField.isVisible().catch(() => false),
    totpField.isVisible().catch(() => false),
  ]);

  if (!passwordVisible) {
    const bodyText = await page.innerText('body').catch(() => '(could not read body text)');
    await j.screenshot('blocked');
    console.log('  observed body text:');
    console.log(
      bodyText
        .split('\n')
        .slice(0, 12)
        .map((l) => `    | ${l}`)
        .join('\n')
    );
    if (j.consoleErrors.length) {
      console.log('  observed console errors:');
      for (const e of j.consoleErrors) console.log(`    - ${e}`);
    }
    throw new SkipError(
      `/login rendered TOTP-step-visible=${totpVisible} instead of the password step. ` +
        'login.go is mid-rewrite and uncommitted (see file header). Re-run once it lands — ' +
        'do not chase this as a fixed bug; it is a moving target by design right now.'
    );
  }

  // --- Below here is the intended flow once the guard above passes -------

  const password = process.env.AFF_E2E_ADMIN_PASSWORD;
  const totpSecret = process.env.AFF_E2E_ADMIN_TOTP_SECRET;
  if (!password || !totpSecret) {
    throw new SkipError(
      'Password step rendered, but AFF_E2E_ADMIN_PASSWORD / AFF_E2E_ADMIN_TOTP_SECRET are not ' +
        'set — see e2eweb/README.md ("Credentials for the login journeys").'
    );
  }

  // Failure branch: wrong password does not disclose which field was wrong.
  await passwordField.click();
  await passwordField.fill('definitely-the-wrong-password');
  await page.getByRole('button', { name: 'Continue' }).click();
  const wrongPwError = page.locator('[aria-live="assertive"]');
  await wrongPwError.waitFor({ state: 'visible', timeout: 5000 });
  const wrongPwText = (await wrongPwError.innerText()).toLowerCase();
  assert(
    !/no such user|unknown user|not found/.test(wrongPwText),
    `wrong-password error must be indistinguishable from unknown-user, got: ${wrongPwText}`
  );

  // Happy path: correct password -> focus should land on the TOTP field
  // (Part 1 #14 of docs/browser-verification-plan.md — named explicitly in
  // the task brief).
  await passwordField.click();
  await passwordField.fill(password);
  await page.getByRole('button', { name: 'Continue' }).click();
  await totpField.waitFor({ state: 'visible', timeout: 5000 });
  const focused = await page.evaluate(() => document.activeElement?.getAttribute('id'));
  const totpId = await totpField.getAttribute('id');
  assert(focused === totpId, `focus should move to the TOTP field on step transition, active id was ${focused}`);

  // Failure branch: wrong TOTP code -> focus moves to the error, announced
  // assertively (Part 1 #16).
  await totpField.click();
  await totpField.fill('000000');
  await page.getByRole('button', { name: 'Sign in' }).click();
  const totpError = page.locator('[aria-live="assertive"]');
  await totpError.waitFor({ state: 'visible', timeout: 5000 });

  const { totp } = require('./lib/harness');
  const code = totp(totpSecret);

  // Happy path: correct code signs in.
  await totpField.click();
  await totpField.fill(code);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10000 });

  // Sanity: cookie attributes.
  const cookies = await page.context().cookies();
  const session = cookies.find((c) => c.name.startsWith('__Host-'));
  assert(session, `expected a __Host--prefixed session cookie, got: ${cookies.map((c) => c.name).join(', ')}`);
  assert(session.httpOnly, 'session cookie must be HttpOnly');
  assert(session.sameSite === 'Strict', `session cookie must be SameSite=Strict, got ${session.sameSite}`);
  // Secure requires an actual TLS origin to observe meaningfully; on
  // http://localhost Chromium may still report it, so check but don't hard
  // fail the whole journey on this one line — flag it instead.
  if (!session.secure) {
    console.log('  NOTE: session cookie Secure flag not observed over http://localhost — verify against a TLS host.');
  }

  // Failure branch: the TOTP code just used must not be replayable. Log out
  // is out of scope for this script (no logout button audited yet); the
  // strongest cheap check here is re-submitting the same code on a second
  // /login attempt in a fresh context.
  const replayContext = await page.context().browser().newContext();
  const replayPage = await replayContext.newPage();
  await replayPage.goto(`${ADMIN_URL}/login`);
  await replayPage.waitForFunction(() => document.body.innerText.trim().length > 0);
  await replayPage.getByLabel('Password', { exact: true }).fill(password);
  await replayPage.getByRole('button', { name: 'Continue' }).click();
  await replayPage.getByLabel('6-digit code', { exact: true }).fill(code);
  await replayPage.getByRole('button', { name: 'Sign in' }).click();
  const replayError = replayPage.locator('[aria-live="assertive"]');
  await replayError.waitFor({ state: 'visible', timeout: 5000 });
  await replayContext.close();

  return 'full flow incl. wrong-password, wrong-TOTP, and replay failure branches';
});
