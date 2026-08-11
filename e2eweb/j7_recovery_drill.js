'use strict';

// J7 — Recover from lockout (PLAN.md §22 J7; TODOS.md DF-08).
//
// Actor: admin, ANON, password lost. Precondition: recovery codes exist.
// Steps: /recover -> enter a code -> elevated session -> set new password
// -> re-enroll TOTP -> re-login. Failure branches: code already used; code
// invalid; elevated window expired; no codes left.
//
// This journey does NOT go through /login first (the actor is locked out),
// so it is not blocked by J1's login-rewrite guard directly — but
// `web/pages/auth/recover.go` and `recover_state.go` are themselves in the
// same uncommitted, mid-rewrite change as login.go (confirmed via
// `git status`), so it is checked for the same symptom independently below
// rather than assumed broken by association.
//
// Sanity from §22 checkable here: a used/invalid code is refused with a
// visible error; the elevated session's UI only offers password-change and
// TOTP-re-enrollment (not, say, the feed rail or settings). NOT checkable
// from the browser: the consumed code is marked used server-side, all other
// sessions were revoked, the remaining-code count decremented by exactly
// one, and the attempt landing in auth_events — Phase B (§17.5).

const { run, assert, SkipError, ADMIN_URL } = require('./lib/harness');

run('J7-recovery-drill', async (j) => {
  const { page } = j;
  await page.goto(`${ADMIN_URL}/recover`);
  await j.waitReady();

  const codeField = page.getByLabel('Recovery code', { exact: true });
  const codeVisible = await codeField.isVisible().catch(() => false);
  if (!codeVisible) {
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
    throw new SkipError(
      '/recover did not render a "Recovery code" field. recover.go/recover_state.go are part of ' +
        'the same uncommitted auth rewrite as login.go — see j1_login.js. Re-run once that lands.'
    );
  }

  const recoveryCode = process.env.AFF_E2E_RECOVERY_CODE;
  if (!recoveryCode) {
    throw new SkipError(
      'Recovery code field rendered, but AFF_E2E_RECOVERY_CODE is not set. Recovery codes are ' +
        'single-use (this journey burns one on every real run) — see e2eweb/README.md before wiring ' +
        'this up to CI or a shared credential.'
    );
  }

  // Failure branch: an invalid code is refused with a visible error.
  await codeField.click();
  await codeField.fill('0000-invalid-0000');
  await page.getByRole('button', { name: 'Continue' }).click();
  const invalidError = page.locator('[aria-live="assertive"], [role="alert"]');
  await invalidError.first().waitFor({ state: 'visible', timeout: 5000 });

  // Happy path: real code -> elevated session.
  await codeField.click();
  await codeField.fill(recoveryCode);
  await page.getByRole('button', { name: 'Continue' }).click();
  const chooseAction = page.locator('text=Choose one action');
  await chooseAction.waitFor({ state: 'visible', timeout: 10000 });

  // Sanity: elevated session offers only password reset / TOTP re-enroll,
  // not the rest of the app.
  const feedRailVisible = await page.getByRole('button', { name: 'New feed' }).isVisible().catch(() => false);
  assert(!feedRailVisible, 'elevated recovery session must not expose the feed rail / rest of the app');

  await page.getByRole('button', { name: /password reset/i }).click();
  const newPasswordField = page.getByLabel('New password', { exact: true });
  await newPasswordField.waitFor({ state: 'visible', timeout: 5000 });

  console.log('  NOTE: continuing to actually set a new password here would invalidate the current');
  console.log('  admin credentials for every other journey in this suite — stopping short of Submit.');
  console.log('  Full re-enroll + re-login is the last stretch of this drill; do it by hand once, once,');
  console.log('  to confirm it, rather than burning a recovery code and rotating the password on every run.');

  return 'invalid code refused, valid code reached the elevated session, elevated session scoped correctly';
});
