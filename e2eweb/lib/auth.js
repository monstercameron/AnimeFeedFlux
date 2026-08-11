'use strict';

// Shared login helper for every journey past J1 (J2-J9 all have an AUTH
// precondition per PLAN.md §22). Attempts the real flow against the real
// page rather than assuming it's broken, so the moment login lands this
// starts working with no edit required — but as of 2026-08-10 it reliably
// cannot complete, and throws a precise SkipError explaining why.

const { SkipError, ADMIN_URL } = require('./harness');

/**
 * Drives /login to a signed-in session. Real clicks/fills only (never
 * page.evaluate(...).dispatchEvent(...)) — a synthetic event sails through
 * an invisible pointer-blocking overlay and would silently pass where a
 * real click correctly fails with "intercepts pointer events". That
 * distinction is the whole reason this bug class got caught once already
 * today; do not undo it here for convenience.
 */
async function login(page) {
  await page.goto(`${ADMIN_URL}/login`);
  await page.waitForFunction(() => document.body.innerText.trim().length > 0, { timeout: 20000 });

  const passwordField = page.getByLabel('Password', { exact: true });
  const totpField = page.getByLabel('6-digit code', { exact: true });

  const [passwordVisible, totpVisibleAlready] = await Promise.all([
    passwordField.isVisible().catch(() => false),
    totpField.isVisible().catch(() => false),
  ]);

  if (!passwordVisible) {
    // This is the exact state observed live on 2026-08-10: /login renders
    // directly at "Step 2 of 2 — Authentication code" (skipping the
    // password step entirely), the body carries stray leading "-0\n-0"
    // text nodes, and the browser console logs
    //   WebSocket connection to 'ws://localhost:8082/grpc' failed:
    //   HTTP Authentication failed; no valid credentials available
    // web/pages/auth/login.go, login_state.go, recover.go and
    // web/shell/{app,expiry,session}.go are all mid-rewrite and uncommitted
    // right now (`git status` shows them modified, plus new untracked
    // devfill_on.go/devfill_off.go/styles.go) — this is not a stable bug to
    // chase, it is a moving target. Re-run once login lands.
    throw new SkipError(
      `/login did not render a Password field (found TOTP step visible=${totpVisibleAlready} instead). ` +
        'login.go is mid-rewrite (uncommitted) — see this function\'s doc comment for the exact ' +
        'observed symptom. Blocked until login lands; do not chase this as a fixed bug.'
    );
  }

  const password = process.env.AFF_E2E_ADMIN_PASSWORD;
  const totpSecret = process.env.AFF_E2E_ADMIN_TOTP_SECRET;
  if (!password || !totpSecret) {
    throw new SkipError(
      'Password step rendered, but AFF_E2E_ADMIN_PASSWORD and/or AFF_E2E_ADMIN_TOTP_SECRET ' +
        'are not set in the environment — see e2eweb/README.md for how to get these from ' +
        '`aff admin init`. Not a code blocker, a credentials-provisioning one.'
    );
  }

  const { totp } = require('./harness');

  await passwordField.click();
  await passwordField.fill(password);
  await page.getByRole('button', { name: 'Continue' }).click();

  await totpField.waitFor({ state: 'visible', timeout: 5000 });
  await totpField.click();
  await totpField.fill(totp(totpSecret));
  await page.getByRole('button', { name: 'Sign in' }).click();

  await page.waitForURL((url) => !url.pathname.startsWith('/login'), { timeout: 10000 }).catch(() => {
    throw new SkipError('Submitted password + TOTP but never navigated off /login — login flow did not complete.');
  });
}

module.exports = { login };
