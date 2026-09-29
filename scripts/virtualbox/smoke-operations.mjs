// Disposable local fixture only; no VM or cloud credentials are read.
import assert from 'node:assert/strict';
import { readFile, writeFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../../.cache/browser-tests/package.json', import.meta.url));
const { chromium } = require('playwright-core');
const root = process.env.GATEWAY_ADMIN_BROWSER_FIXTURE;
const { url } = JSON.parse(await readFile(`${root}/ready.json`, 'utf8'));
assert(/^https:\/\/127\.0\.0\.1:\d+$/.test(url));
const browser = await chromium.launch({ executablePath: process.env.GATEWAY_TEST_CHROMIUM, headless: true });
try {
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 } });
  await page.goto(`${url}/login`);
  await page.locator('input[name=password]').fill('browser-fixture-password');
  await page.locator('button[type=submit]').click();
  await page.waitForURL(`${url}/`);
  await page.goto(`${url}/jobs`);
  const form = page.locator('form[action="/jobs/resolve"]');
  await form.locator('input[type=checkbox]').check();
  await form.locator('button').click();
  await page.getByText('Rozhodnutí je uložené, čeká na potvrzení serveru.').waitFor();
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: `${root}/jobs-mobile.png`, fullPage: true });
  await page.goto(`${url}/`);
  const password = page.locator('form[action="/access/password"]');
  await password.locator('[name=current_password]').fill('browser-fixture-password');
  await password.locator('[name=new_password]').fill('browser-replacement-password');
  await password.locator('[name=confirm_password]').fill('browser-replacement-password');
  // Wait for the shared anti-bruteforce window without altering production limits.
  await page.waitForTimeout(2100);
  await password.locator('button').click();
  await page.waitForURL(`${url}/login`);
  await page.waitForTimeout(2100);
  await page.locator('input[name=password]').fill('browser-replacement-password');
  await page.locator('button[type=submit]').click();
  await page.waitForURL(`${url}/`);
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await page.screenshot({ path: `${root}/dashboard-mobile.png`, fullPage: true });
  console.log('Browser forms, pending resolution, password rotation, login and mobile layout passed.');
} finally {
  await browser.close();
  await writeFile(`${root}/stop`, 'done');
}
