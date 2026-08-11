'use strict';

// J6 — Correct a wrong item (PLAN.md §22 J6; TODOS.md DF-07).
//
// Actor: admin, AUTH. Precondition: a published item that is factually
// wrong. Steps: find it -> publish a correction -> verify the correction
// reaches subscribers. Failure branch: admin edits the item instead and
// expects redelivery. DF-07's specific ask: publish a correction **without
// first being tempted to edit** — i.e. the UI must make Publish correction
// the obviously-correct action, not something reached by fighting an Edit
// affordance that looks like it does the same thing.
//
// Sanity from §22 checkable here: the "no retraction" notice
// (history.items.no_retraction_notice) is shown before/alongside the
// correction form, so the admin cannot miss it; the correction form is a
// distinct action (Publish correction), not a relabeled Edit. NOT
// checkable from the browser: original guid/published_at unchanged, the
// correction gets a new ULID and strictly later published_at, the
// `corrections` row link, and the original still resolving at its
// permalink — Phase B (§17.5). "Reaches subscribers" for real is J10's job.

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J6-publish-correction', async (j) => {
  const { page } = j;
  await login(page);

  await page.getByRole('link', { name: 'History' }).or(page.getByRole('button', { name: 'History' })).click();
  await page.getByRole('tab', { name: 'Items' }).click();

  const rows = page.locator('table tbody tr');
  const count = await rows.count();
  if (count === 0) {
    throw new SkipError('No published items exist — precondition (a published item to correct) is not met.');
  }
  const firstRow = rows.first();
  await firstRow.getByRole('button', { name: 'Actions' }).click();

  const publishCorrection = page.getByRole('menuitem', { name: 'Publish correction' }).or(
    page.getByRole('button', { name: 'Publish correction' })
  );
  const editItem = page.getByRole('menuitem', { name: 'Edit' }).or(page.getByRole('button', { name: 'Edit' }));

  const [correctionVisible, editVisible] = await Promise.all([
    publishCorrection.isVisible().catch(() => false),
    editItem.isVisible().catch(() => false),
  ]);
  assert(correctionVisible, 'expected a distinct "Publish correction" action in the row menu');

  await publishCorrection.click();
  const noRetractionNotice = page.locator('text=There is no retraction');
  await noRetractionNotice.waitFor({ state: 'visible', timeout: 5000 });

  await page.getByLabel('Correction title', { exact: true }).fill('e2e correction title');
  await page.getByLabel('Correction summary', { exact: true }).fill('e2e correction summary');
  await page.getByLabel('Correction body', { exact: true }).fill('e2e correction body text.');
  await page.getByRole('button', { name: 'Publish correction' }).last().click();

  const correctionRow = page.locator('table tbody tr', { hasText: 'Correction' });
  await correctionRow.first().waitFor({ state: 'visible', timeout: 10000 });

  console.log('  NOTE: original guid/published_at unchanged, correction gets a fresh ULID + strictly');
  console.log('  later published_at, corrections-table link, and original still resolving at its');
  console.log('  permalink are DB/render checks — Phase B (§17.5). "Reaches subscribers" is J10.');

  return `distinct Publish-correction action used (Edit also present: ${editVisible}); correction row appeared`;
});
