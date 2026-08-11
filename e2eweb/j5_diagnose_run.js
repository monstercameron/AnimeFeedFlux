'use strict';

// J5 — Diagnose a bad run (PLAN.md §22 J5; TODOS.md DF-06).
//
// Actor: admin, AUTH. Precondition: a run that failed, was skipped, or
// added fewer items than asked. Steps: open history -> find the run -> read
// status, error kind, and reject reasons. Failure branches: run still in
// flight; run interrupted by a crash.
//
// Sanity from §22 checkable here: a failed/skipped run's status and reject
// reasons render as actual readable text, not a raw error code or a blank
// panel. NOT checkable from the browser: every run reaching a terminal
// status server-side, items_added+items_rejected reconciling with recorded
// reasons, a failed run having zero attributable items, and tokens/cost
// being recorded even for failed runs — all DB invariants, Phase B (§17.5).

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J5-diagnose-run', async (j) => {
  const { page } = j;
  await login(page);

  await page.getByRole('link', { name: 'History' }).or(page.getByRole('button', { name: 'History' })).click();
  await page.getByRole('tab', { name: 'Runs' }).click();

  const rows = page.locator('table tbody tr');
  const count = await rows.count();
  if (count === 0) {
    throw new SkipError('No runs exist in history — precondition (a failed/skipped/short run) is not met.');
  }

  const failedRow = page.locator('table tbody tr', { hasText: 'Failed' }).or(
    page.locator('table tbody tr', { hasText: 'Skipped' })
  );
  const hasFailure = await failedRow.first().isVisible().catch(() => false);
  if (!hasFailure) {
    throw new SkipError('No Failed/Skipped run visible in history — precondition not met (seed a deliberately broken run).');
  }

  await failedRow.first().getByRole('button', { name: 'Expand' }).click();
  const reasonsPanel = page.locator('text=Reject reasons').locator('..');
  await reasonsPanel.waitFor({ state: 'visible', timeout: 5000 });
  const reasonsText = await reasonsPanel.innerText();
  assert(
    reasonsText.trim().length > 'Reject reasons'.length,
    `expected readable reject reasons, panel only had: ${JSON.stringify(reasonsText)}`
  );
  assert(!/^[0-9a-f-]{8,}$/i.test(reasonsText.trim()), 'reject reasons must not render as a raw error code/UUID');

  const errorCell = await failedRow.first().locator('td').last().innerText().catch(() => '');
  console.log(`  observed error cell text: ${JSON.stringify(errorCell)}`);

  console.log('  NOTE: terminal-status guarantee, added+rejected reconciliation, zero-items-on-failure,');
  console.log('  and cost-recorded-even-on-failure are DB invariants — see Phase B suite (§17.5).');

  return 'found a failed/skipped run and confirmed reject reasons render as readable text';
});
