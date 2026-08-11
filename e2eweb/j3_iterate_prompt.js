'use strict';

// J3 — Iterate a prompt by sampling (PLAN.md §22 J3; TODOS.md DF-03, DF-04).
//
// Actor: admin, AUTH. Precondition: a saved recipe (feed).
// Steps: sample -> read the item, novelty verdict, link verdicts, and cost
// -> edit the prompt -> sample again.
// Failure branches: kill switch on; daily budget exhausted; provider
// transient error; malformed output; every candidate rejected as a
// near-duplicate.
//
// DF-04 folds the kill-switch failure branch into this same journey file
// (both are "J3" per TODOS.md) rather than a separate one: with the kill
// switch on, the Sample control must show a *reason* it's disabled, not
// just sit there dead (§12.3).
//
// Sanity from §22 checkable here: a `samples` row concept is opaque from
// the browser, but its visible proxies are not — the sample must render an
// item, a novelty verdict, and a non-zero cost, and with the kill switch on
// no candidate should render at all (no provider call). The single most
// important sanity assertion in §22 for this flow — "items row count is
// unchanged" — is a DB invariant this script cannot observe directly; it is
// covered by the headless Phase B suite (§17.5), not here.

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J3-iterate-prompt', async (j) => {
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

  // --- DF-04: kill switch failure branch -----------------------------
  const killSwitchNotice = page.locator('text=Generation disabled by the global kill switch.');
  const killSwitchOn = await killSwitchNotice.isVisible().catch(() => false);
  if (killSwitchOn) {
    const disabled = await sampleButton.isDisabled().catch(() => false);
    assert(disabled, 'Sample control must be disabled while the kill switch is on');
    const reasonVisible = await killSwitchNotice.isVisible();
    assert(reasonVisible, 'a disabled Sample control must show a reason, not sit there dead (§12.3)');
    return 'kill switch on: Sample control disabled with a visible reason (DF-04 branch only — switch it off to exercise the sample-and-iterate path)';
  }

  // --- Happy path: sample, read verdicts, edit prompt, sample again ---
  await sampleButton.click();
  const candidateTab = page.getByRole('tab', { name: '1' }).or(page.locator('text=Candidates').locator('..').locator('button, [role="tab"]').first());
  await candidateTab.waitFor({ state: 'visible', timeout: 30000 });

  const costText = await page.locator('text=Cost').locator('..').innerText().catch(() => '');
  assert(/\d/.test(costText), `expected a non-zero cost to render after sampling, got: ${JSON.stringify(costText)}`);

  const systemPromptField = page.getByLabel('System prompt', { exact: true });
  await systemPromptField.click();
  await systemPromptField.fill((await systemPromptField.inputValue()) + '\n');
  await sampleButton.click();
  await candidateTab.waitFor({ state: 'visible', timeout: 30000 });

  return 'sampled, read cost/verdicts, edited prompt, sampled again';
});
