// Exercise real HTML form navigation, including browser-generated Origin headers.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';

const require = createRequire(new URL('../../.cache/browser-tests/package.json', import.meta.url));
const { chromium } = require('playwright-core');
assert(process.env.GATEWAY_TEST_CHROMIUM, 'Set GATEWAY_TEST_CHROMIUM to a Chromium executable');
const credentials = JSON.parse(await readFile(new URL('../../dist/virtualbox/credentials.json', import.meta.url)));
const browser = await chromium.launch({ executablePath: process.env.GATEWAY_TEST_CHROMIUM, headless: true });
try {
  // The isolated lab uses a self-signed certificate; no browser trust store is changed.
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  const base = 'https://127.0.0.1:8443';
  await page.goto(base + '/login');
  await page.locator('input[name=password]').fill(credentials.gateway_admin_password);
  const loginResponse = page.waitForResponse(r => r.url() === base + '/login' && r.request().method() === 'POST');
  await page.locator('button[type=submit]').click();
  const response = await loginResponse;
  const origin = (await response.request().allHeaders()).origin;
  console.log(JSON.stringify({ action: 'login', origin, status: response.status() }));
  assert.equal(response.status(), 303, 'Browser login must succeed');
  assert.equal(origin, base, 'The browser must retain the same origin on form submission');
  await page.waitForURL(base + '/');
  assert.match(await page.locator('body').innerText(), /DHCP rezervace/);
  const logoutResponse = page.waitForResponse(r => r.url() === base + '/logout' && r.request().method() === 'POST');
  await page.locator('form[action="/logout"] button').click();
  assert.equal((await logoutResponse).status(), 303, 'Browser logout must succeed');
  await page.waitForURL(base + '/login');
  await page.goto(base + '/');
  assert.equal(page.url(), base + '/login', 'Logout must invalidate the session');
  console.log('Browser login, dashboard and logout passed.');
} finally {
  await browser.close();
}
