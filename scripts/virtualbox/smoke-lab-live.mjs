// Uses the opt-in Go fixture, never the configured VM or cloud credentials.
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import net from 'node:net';
import path from 'node:path';

const require = createRequire(new URL('../../.cache/browser-tests/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const dir = process.env.GATEWAY_LAB_BROWSER_DIR;
assert(dir && process.env.GATEWAY_TEST_CHROMIUM, 'Set the isolated fixture directory and Chromium path');
const fixture = JSON.parse(await readFile(path.join(dir, 'fixture.json')));
const document = await readFile(path.join(dir, 'document.pdf'));
const browser = await chromium.launch({ executablePath: process.env.GATEWAY_TEST_CHROMIUM, headless: true });
try {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, httpCredentials: { username: 'technik', password: fixture.password } });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(fixture.url, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => document.querySelector('#live-status').dataset.state === 'live');
  await page.evaluate(() => { window.smokeMarker = 'unchanged-document'; });
  assert(await page.locator('button[value=label]').isDisabled());
  assert.match(await page.locator('#local-jobs').innerText(), /historical-local-job/);
  assert.match(await page.locator('#local-jobs').innerText(), /Čas nebyl zaznamenán/);
  const send = () => new Promise((resolve, reject) => {
    const [host, port] = fixture.printer.split(':');
    const socket = net.createConnection({ host, port: Number(port) }, () => socket.end(document));
    socket.on('error', reject);
    socket.on('close', hadError => { if (!hadError) resolve(); });
  });
  await send();
  await page.waitForFunction(() => document.querySelectorAll('#captures a[href^="/captures/"]').length === 1);
  assert.equal(await page.locator('#local-jobs tbody tr').count(), 1);
  assert.match(await page.locator('#captures').innerText(), /Zachyceno v simulátoru/);
  assert.notEqual(await page.locator('#last-checked').innerText(), 'čeká na spojení');
  const firstChecked = await page.locator('#last-checked').innerText();
  // Cross the production server's default write timeout; heartbeats must keep SSE alive.
  await page.waitForTimeout(17000);
  assert.equal(await page.locator('#live-status').getAttribute('data-state'), 'live');
  assert.notEqual(await page.locator('#last-checked').innerText(), firstChecked);
  await context.setOffline(true);
  await page.waitForFunction(() => document.querySelector('#live-status').dataset.state === 'stale', null, { timeout: 30000 });
  await send();
  await context.setOffline(false);
  await page.waitForFunction(() => document.querySelector('#live-status').dataset.state === 'live' && document.querySelectorAll('#captures a[href^="/captures/"]').length === 2);
  assert.equal(await page.evaluate(() => window.smokeMarker), 'unchanged-document', 'The browser should never reload the page');
  assert.deepEqual(errors, []);
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.screenshot({ path: path.join(dir, 'lab-live.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Mobile page overflows');
  await page.screenshot({ path: path.join(dir, 'lab-live-mobile.png'), fullPage: true });
  console.log('Real TCP capture, authenticated SSE, heartbeats, stale status, reconnect and mobile layout passed without touching the VM.');
} finally {
  await browser.close();
  await writeFile(path.join(dir, 'done'), 'done');
}
