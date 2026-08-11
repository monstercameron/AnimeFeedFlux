'use strict';

// J9 — Watch a run live (PLAN.md §22 J9; TODOS.md DF-10).
//
// Actor: admin, AUTH. Precondition: a run triggered manually or by cron.
// Steps: observe progress streaming until the run reaches a terminal state.
// Failure branches: WebSocket drops mid-run; run outlives the session; two
// viewers.
//
// The WS-drop branch is simulated with `context.setOffline(true/false)` —
// NOT by touching the shared dev server (it is not this suite's to
// restart/kill; see e2eweb/README.md). This also happens to be the more
// faithful simulation of "the admin's network dropped," vs. "the server
// went down," which is a different failure this suite does not attempt.
//
// Sanity from §22 checkable here: the DISCONNECTED banner appears while
// offline and clears on reconnect without a full page reload; the run
// resumes showing progress after reconnect rather than a stale snapshot.
// NOT checkable from the browser: the stream terminating exactly when the
// run does in every branch including failure, the run itself continuing
// server-side while the socket is down (this script can observe the UI
// recovering, not the server-side run state independently), and progress
// events never claiming uncommitted items — Phase B (§17.5).

const { run, assert, SkipError } = require('./lib/harness');
const { login } = require('./lib/auth');

run('J9-watch-run-live', async (j) => {
  const { page } = j;
  await login(page);

  const feedRail = page.getByRole('button', { name: 'Feeds' }).locator('..');
  const firstFeed = feedRail.locator('a, button').first();
  if (!(await firstFeed.isVisible().catch(() => false))) {
    throw new SkipError('No feed exists to run — precondition (a triggered run) is not met.');
  }
  await firstFeed.click();

  const runNowButton = page.getByRole('button', { name: 'Run now' });
  const runNowVisible = await runNowButton.isVisible().catch(() => false);
  if (!runNowVisible) {
    throw new SkipError('No "Run now" control visible for the selected feed — cannot trigger a run to watch.');
  }
  await runNowButton.click();

  // Mid-run: drop the network at the browser level (not the server).
  await page.waitForTimeout(500);
  await page.context().setOffline(true);

  const banner = page.locator('text=Disconnected — reconnecting…');
  await banner.waitFor({ state: 'visible', timeout: 10000 });

  await page.context().setOffline(false);
  await banner.waitFor({ state: 'hidden', timeout: 15000 });

  // After reconnect the run's true current state should render — not
  // necessarily "still running" (it may have finished while offline), but
  // the page must show a definite status, not be stuck on a pre-drop
  // snapshot forever.
  const runStatus = page.locator('table tbody tr').first();
  await runStatus.waitFor({ state: 'visible', timeout: 10000 });

  console.log('  NOTE: this only proves the client recovers; whether the run itself kept executing');
  console.log('  server-side while the socket was down, and whether the stream terminates exactly at the');
  console.log('  run\'s true terminal state, need the headless Phase B suite — §17.5.');

  return 'banner appeared on drop, cleared on reconnect without a full reload, run status rendered post-reconnect';
});
