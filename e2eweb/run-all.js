'use strict';

// Runs every journey script in order, in one process, and reports a final
// summary. Each journey already prints its own [PASS]/[SKIP]/[FAIL] line
// and (on FAIL) its own diagnostics, so this file's only job is sequencing
// and a roll-up — it deliberately does not re-implement any assertion
// logic. Exit code is non-zero iff any journey FAILed (a SKIP is not a
// failure — see lib/harness.js's `run()` doc comment).
//
// Usage: NODE_PATH=/c/Users/mreca/Desktop/CashFlux/node_modules node e2eweb/run-all.js

const path = require('path');
const { execFileSync } = require('child_process');

const journeys = [
  'j1_login.js',
  'j2_create_feed.js',
  'j3_iterate_prompt.js',
  'j4_promote_sample.js',
  'j5_diagnose_run.js',
  'j6_publish_correction.js',
  'j7_recovery_drill.js',
  'j8_review_spend.js',
  'j9_watch_run_live.js',
  'j10_subscriber_lifecycle.js',
];

let failures = 0;

for (const file of journeys) {
  const full = path.join(__dirname, file);
  console.log(`\n=== ${file} ===`);
  try {
    execFileSync(process.execPath, [full], { stdio: 'inherit', env: process.env });
  } catch (err) {
    // A non-zero exit here means the child process itself hit a FAIL (or
    // crashed outright) — either way, count it and keep going so one
    // broken journey doesn't hide the rest of the suite's results.
    failures += 1;
  }
}

console.log(`\n${journeys.length - failures}/${journeys.length} journeys ran without a FAIL (SKIPs count as clean).`);
if (failures > 0) process.exitCode = 1;
