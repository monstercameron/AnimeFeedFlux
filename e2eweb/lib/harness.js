'use strict';

// Shared harness for the e2eweb/ journey scripts. No npm dependency of its
// own beyond Playwright, which is borrowed at run time via NODE_PATH — see
// e2eweb/README.md. Every journey script requires this module with a
// relative path (`require('./lib/harness')`), never a package name, so
// nothing here implies a package.json / node_modules of its own.

const { chromium } = require('playwright');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');

const ADMIN_URL = (process.env.AFF_ADMIN_URL || 'http://localhost:8082').replace(/\/$/, '');
const PUBLISH_URL = (process.env.AFF_PUBLISH_URL || 'http://127.0.0.1:8081').replace(/\/$/, '');
const ARTIFACTS_DIR = path.join(__dirname, '..', 'artifacts');
const HEADLESS = process.env.AFF_E2E_HEADED !== '1';

function ensureArtifactsDir() {
  fs.mkdirSync(ARTIFACTS_DIR, { recursive: true });
}

/**
 * Thrown by a journey to mean "this cannot run yet, and that is expected" —
 * as opposed to an uncaught error, which means "this ran and broke."
 * `run()` reports these as SKIP, not FAIL, and does not set a failing exit
 * code, so a suite full of known blockers still reports clean.
 */
class SkipError extends Error {}

// --- Minimal RFC 6238 TOTP (HMAC-SHA1, 30s step, 6 digits) ---------------
// No otplib/speakeasy dependency is available (or wanted — see README), so
// this is the ~20 lines TOTP needs. Only used if AFF_E2E_ADMIN_TOTP_SECRET
// is set; journeys that need a code call `totp(secret)` themselves.

function base32Decode(input) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  const clean = input.toUpperCase().replace(/=+$/, '').replace(/\s+/g, '');
  let bits = '';
  for (const c of clean) {
    const idx = alphabet.indexOf(c);
    if (idx === -1) throw new Error(`invalid base32 character in TOTP secret: ${JSON.stringify(c)}`);
    bits += idx.toString(2).padStart(5, '0');
  }
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(bytes);
}

function totp(secretBase32, { step = 30, digits = 6, at = Date.now() } = {}) {
  const key = base32Decode(secretBase32);
  const counter = Math.floor(at / 1000 / step);
  const counterBuf = Buffer.alloc(8);
  counterBuf.writeBigUInt64BE(BigInt(counter));
  const hmac = crypto.createHmac('sha1', key).update(counterBuf).digest();
  const offset = hmac[hmac.length - 1] & 0x0f;
  const code =
    (((hmac[offset] & 0x7f) << 24) |
      ((hmac[offset + 1] & 0xff) << 16) |
      ((hmac[offset + 2] & 0xff) << 8) |
      (hmac[offset + 3] & 0xff)) %
    10 ** digits;
  return String(code).padStart(digits, '0');
}

/**
 * Per-journey state: the page, and everything captured passively so a
 * failure report is self-contained (screenshot, console errors, failed/4xx+
 * network requests) without re-running anything.
 */
class Journey {
  constructor(name) {
    this.name = name;
    this.consoleErrors = [];
    this.failedRequests = [];
    this.browser = null;
    this.context = null;
    this.page = null;
  }

  async start() {
    ensureArtifactsDir();
    this.browser = await chromium.launch({ headless: HEADLESS });
    this.context = await this.browser.newContext();
    this.page = await this.context.newPage();

    this.page.on('console', (msg) => {
      if (msg.type() === 'error') this.consoleErrors.push(msg.text());
    });
    this.page.on('pageerror', (err) => {
      this.consoleErrors.push(`pageerror: ${err.message}`);
    });
    this.page.on('requestfailed', (req) => {
      this.failedRequests.push(`${req.method()} ${req.url()} — ${req.failure()?.errorText || 'failed'}`);
    });
    this.page.on('response', (res) => {
      if (res.status() >= 400) {
        this.failedRequests.push(`${res.request().method()} ${res.url()} — HTTP ${res.status()}`);
      }
    });

    return this.page;
  }

  /**
   * The one readiness signal that's actually correct for a 31 MB WASM
   * bundle: wait for the mounted app to have rendered *something*, not a
   * fixed sleep (which either flakes on a slow machine or wastes time on a
   * fast one).
   */
  async waitReady(timeout = 20000) {
    await this.page.waitForFunction(() => document.body.innerText.trim().length > 0, { timeout });
  }

  async screenshot(tag) {
    ensureArtifactsDir();
    const file = path.join(ARTIFACTS_DIR, `${this.name}-${tag}.png`);
    try {
      await this.page.screenshot({ path: file, fullPage: true });
    } catch {
      return null;
    }
    return file;
  }

  async close() {
    if (this.browser) await this.browser.close().catch(() => {});
  }

  async reportFailure(err) {
    console.error(`\n[FAIL] ${this.name}: ${err.message}`);
    if (this.page && !this.page.isClosed()) {
      const shot = await this.screenshot('failure');
      if (shot) console.error(`  screenshot: ${shot}`);
    }
    if (this.consoleErrors.length) {
      console.error(`  console errors (${this.consoleErrors.length}):`);
      for (const e of this.consoleErrors) console.error(`    - ${e}`);
    }
    if (this.failedRequests.length) {
      console.error(`  failed/4xx+ network requests (${this.failedRequests.length}):`);
      for (const r of this.failedRequests) console.error(`    - ${r}`);
    }
    if (err.stack) console.error(`  ${err.stack.split('\n').slice(1).join('\n')}`);
  }

  reportSkip(reason) {
    console.log(`[SKIP] ${this.name}: ${reason}`);
  }

  reportPass(note) {
    console.log(`[PASS] ${this.name}${note ? ' — ' + note : ''}`);
  }
}

function assert(cond, msg) {
  if (!cond) throw new Error(`assertion failed: ${msg}`);
}

/**
 * Runs one journey. Exit code stays 0 for PASS and SKIP alike — a SKIP is
 * an accurate report of a known, named blocker, not a test failure. Only a
 * FAIL (an unexpected break) sets a non-zero exit code, so this composes
 * correctly with `run-all.js` and with CI treating this suite as advisory.
 */
async function run(name, fn) {
  const j = new Journey(name);
  await j.start();
  try {
    const note = await fn(j);
    j.reportPass(typeof note === 'string' ? note : undefined);
  } catch (err) {
    if (err instanceof SkipError) {
      j.reportSkip(err.message);
    } else {
      await j.reportFailure(err);
      process.exitCode = 1;
    }
  } finally {
    await j.close();
  }
}

/**
 * Runs a journey that talks to the app over plain HTTP and never needs a
 * browser (J10 — the subscriber is a feed reader, not a person clicking).
 * Same PASS/SKIP/FAIL reporting contract as `run()`, without paying for a
 * Chromium launch it has no use for.
 */
async function runHttp(name, fn) {
  console.log(`[RUN ] ${name}`);
  try {
    const note = await fn();
    console.log(`[PASS] ${name}${note ? ' — ' + note : ''}`);
  } catch (err) {
    if (err instanceof SkipError) {
      console.log(`[SKIP] ${name}: ${err.message}`);
    } else {
      console.error(`\n[FAIL] ${name}: ${err.message}`);
      if (err.stack) console.error(`  ${err.stack.split('\n').slice(1).join('\n')}`);
      process.exitCode = 1;
    }
  }
}

module.exports = {
  ADMIN_URL,
  PUBLISH_URL,
  ARTIFACTS_DIR,
  Journey,
  SkipError,
  assert,
  totp,
  run,
  runHttp,
};
