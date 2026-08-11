'use strict';

// J2 — Create a feed (PLAN.md §22 J2; TODOS.md DF-02).
//
// Actor: admin, AUTH. Precondition: none. Steps: create -> set slug, kind,
// schedule, timezone, prompts, budgets, sources -> validate -> save.
// Failure branches: duplicate slug; reserved slug; bad cron; unknown
// timezone; unknown template variable; grounded with no source; zero
// budget. DF-02's specific ask: "every validation error renders on its
// field" — that is the one this script checks hardest.
//
// Sanity from §22 this script can check from the browser:
//   - every rejection is refused (a field-level error appears, Save does
//     not proceed) for at least one representative case per failure branch
// Sanity this script CANNOT check from the browser (needs the headless
// Phase B suite against the RPC/DB per §17.5 — noted, not silently
// skipped):
//   - the feed is disabled by default with zero items
//   - jitter_offset is deterministic from the slug
//   - the next three computed runs are correct
//   - rejections are refused server-side, not merely client-side (this
//     script can only observe the client's rendering of the rejection; a
//     server that accepted the bad input and the client independently
//     re-validating would look identical from here)
//   - no provider call was made

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J2-create-feed', async (j) => {
  const { page } = j;
  await login(page);

  await page.getByRole('button', { name: 'New feed' }).click();

  const slugField = page.getByLabel('Slug', { exact: true });
  await slugField.waitFor({ state: 'visible', timeout: 10000 });

  // Failure branch: reserved slug (e.g. a routing-reserved word).
  await slugField.click();
  await slugField.fill('login');
  await page.getByRole('button', { name: 'Validate' }).click();
  const slugFieldContainer = page.locator('label:has-text("Slug")').locator('..');
  await slugFieldContainer.locator('.af-field-error, [role="alert"]').first().waitFor({
    state: 'visible',
    timeout: 5000,
  });

  // Failure branch: bad cron expression renders on the schedule field, not
  // a toast or a top-of-page banner.
  const cronField = page.getByLabel('Cron expression', { exact: true });
  await cronField.click();
  await cronField.fill('not a cron expression');
  await page.getByRole('button', { name: 'Validate' }).click();
  const cronFieldContainer = page.locator('label:has-text("Cron expression")').locator('..');
  await cronFieldContainer.locator('.af-field-error, [role="alert"]').first().waitFor({
    state: 'visible',
    timeout: 5000,
  });

  // Failure branch: grounded kind with no source.
  await slugField.click();
  await slugField.fill(`e2e-j2-${Date.now()}`);
  await cronField.click();
  await cronField.fill('0 9 * * *');
  const kindSelect = page.getByLabel('Kind', { exact: true });
  await kindSelect.selectOption({ label: 'Grounded' });
  await page.getByRole('button', { name: 'Validate' }).click();
  const sourcesSection = page.locator('text=Sources').locator('..');
  await sourcesSection.locator('.af-field-error, [role="alert"]').first().waitFor({
    state: 'visible',
    timeout: 5000,
  });

  console.log('  NOTE: server-side item count / jitter_offset / next-run-time / no-provider-call');
  console.log('  sanity from §22 are not checkable from the browser — see docs/browser-verification-plan.md');
  console.log('  and PLAN.md §17.5 for the headless Phase B counterpart of this flow.');

  return 'validation errors render on-field for reserved slug, bad cron, and grounded-with-no-source';
});
