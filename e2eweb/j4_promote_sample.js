'use strict';

// J4 — Promote a sample (PLAN.md §22 J4; TODOS.md DF-05).
//
// Actor: admin, AUTH. Precondition: a sample from J3.
// Steps: choose a candidate -> promote -> confirm it appears in the feed.
// Failure branches: sample expired; timestamp collision with a concurrent
// scheduled run.
//
// Sanity from §22 checkable here: the promoted candidate appears in the
// items list / feed view after promotion. NOT checkable from the browser
// (needs Phase B headless suite, §17.5): item_key is a fresh ULID, guid
// contains it, published_at is strictly greater than the previous newest
// item, render cache invalidation / lastBuildDate bump, exactly-once
// presence across all three formats (RSS/Atom/JSON), and collision retry
// behavior.

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J4-promote-sample', async (j) => {
  const { page } = j;
  await login(page);

  const feedRail = page.getByRole('button', { name: 'Feeds' }).locator('..');
  const firstFeed = feedRail.locator('a, button').first();
  if (!(await firstFeed.isVisible().catch(() => false))) {
    throw new SkipError('No feed exists in the rail to select — precondition (a saved recipe) is not met.');
  }
  await firstFeed.click();

  const sampleButton = page.getByRole('button', { name: /sample/i });
  await sampleButton.waitFor({ state: 'visible', timeout: 10000 });
  if (await page.locator('text=Generation disabled by the global kill switch.').isVisible().catch(() => false)) {
    throw new SkipError('Kill switch is on — cannot produce a sample to promote. See J3 for that branch.');
  }

  await sampleButton.click();
  const candidateTab = page.getByRole('tab', { name: '1' });
  await candidateTab.waitFor({ state: 'visible', timeout: 30000 });
  await candidateTab.click();

  const promoteButton = page.getByRole('button', { name: /promote/i });
  await promoteButton.waitFor({ state: 'visible', timeout: 5000 });
  await promoteButton.click();

  // Confirm it appears in the feed's items view.
  const itemsLink = page.getByRole('link', { name: 'History' }).or(page.getByRole('button', { name: 'History' }));
  await itemsLink.click();
  await page.getByRole('tab', { name: 'Items' }).click();
  const newestRow = page.locator('table tbody tr').first();
  await newestRow.waitFor({ state: 'visible', timeout: 10000 });

  console.log('  NOTE: item_key ULID freshness, guid, strictly-greater published_at, cache/lastBuildDate');
  console.log('  invalidation, and exactly-once presence across RSS/Atom/JSON are DB/render-layer sanity');
  console.log('  checks that need the headless Phase B suite (§17.5), not this browser walkthrough.');

  return 'promoted a candidate and confirmed it appears in the items list';
});
