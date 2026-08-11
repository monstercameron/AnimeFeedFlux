'use strict';

// J8 — Review and control spend (PLAN.md §22 J8; TODOS.md DF-09).
//
// Actor: admin, AUTH. Precondition: several runs have executed.
// Steps: read per-feed and total spend -> adjust budgets or the price table
// -> confirm enforcement. Failure branches: price table stale; a feed
// silently at its cap.
//
// Sanity from §22 checkable here: run-level cost renders (History > Runs,
// Cost column) and per-feed budget fields (Settings > Generation, and the
// per-feed Daily token/run budget in the recipe editor) round-trip a saved
// value. NOT checkable from the browser: sum(per-run est_cost_usd) equals
// the reported total, cost computed at the price in force at run time
// (editing the price table must not rewrite history), a feed at its cap
// logging a distinct-status skipped run rather than failing silently, and
// sampling spend sharing the same totals as scheduled spend — all DB
// invariants, Phase B (§17.5). The "feed silently at its cap" failure
// branch specifically needs a feed actually driven to its cap first, which
// this script does not attempt to set up.

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J8-review-spend', async (j) => {
  const { page } = j;
  await login(page);

  await page.getByRole('link', { name: 'History' }).or(page.getByRole('button', { name: 'History' })).click();
  await page.getByRole('tab', { name: 'Runs' }).click();
  const costCells = page.locator('table tbody tr td', { hasText: /\$/ });
  const costCount = await costCells.count();
  if (costCount === 0) {
    throw new SkipError('No runs with a rendered cost — precondition (several executed runs) is not met.');
  }

  await page.getByRole('link', { name: 'Settings' }).or(page.getByRole('button', { name: 'Settings' })).click();
  await page.getByRole('tab', { name: 'Generation' }).click();
  const ceilingField = page.getByLabel('Global daily spend ceiling', { exact: true });
  await ceilingField.waitFor({ state: 'visible', timeout: 10000 });
  const before = await ceilingField.inputValue();
  const probe = String(Number(before || '0') + 1);
  await ceilingField.click();
  await ceilingField.fill(probe);
  const saveButton = page.getByRole('button', { name: 'Save' });
  await saveButton.click();
  await page.waitForTimeout(500);
  await page.reload();
  await j.waitReady();
  await page.getByRole('tab', { name: 'Generation' }).click();
  const after = await page.getByLabel('Global daily spend ceiling', { exact: true }).inputValue();
  assert(after === probe, `spend ceiling did not persist: saved ${probe}, read back ${after}`);

  // restore
  await page.getByLabel('Global daily spend ceiling', { exact: true }).fill(before);
  await page.getByRole('button', { name: 'Save' }).click();

  console.log('  NOTE: total-equals-sum-of-runs, price-in-force-at-run-time, and cap-produces-distinct-');
  console.log('  status-not-silent-failure are DB invariants — Phase B (§17.5). This script only proves');
  console.log('  spend is visible per-run and the ceiling round-trips through Save.');

  return `run costs visible (${costCount} cost cells), global spend ceiling round-tripped through Save`;
});
