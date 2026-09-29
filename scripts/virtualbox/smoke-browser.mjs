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
  if (process.argv.includes('--configure-lab')) {
    // Never replace a real server's settings as a side effect of this lab test.
    assert.equal(await page.locator('#server_url').inputValue(), 'https://127.0.0.1:9443');
    const save = async token => {
      await page.locator('#token').fill(token);
      const response = page.waitForResponse(r => r.url() === base + '/cloud' && r.request().method() === 'POST');
      await page.locator('form[action="/cloud"] button').click();
      assert.equal((await response).status(), 303);
      await page.waitForURL(base + '/?cloud=saved#cloud');
      assert.equal(await page.locator('#token').inputValue(), '');
      assert(!(await page.content()).includes(credentials.gateway_lab_token), 'Stored token leaked into HTML');
    };
    const waitState = async expected => {
      for (let attempt = 0; attempt < 60; attempt++) {
        await page.reload();
        if ((await page.locator('#cloud-state').innerText()).includes(expected)) return;
        await page.waitForTimeout(250);
      }
      assert.fail('Connection state did not reach ' + expected);
    };
    try {
      await save('invalid-lab-token-'.repeat(4));
      await waitState('Server odmítl přístup');
    } finally {
      await save(credentials.gateway_lab_token);
    }
    await waitState('Připojeno k serveru');
    await save('');
    await waitState('Připojeno k serveru');
    await page.screenshot({ path: new URL('../../dist/virtualbox/cloud-settings.png', import.meta.url).pathname, fullPage: true });
    console.log('Credential rejection, replacement, reconnect and blank-token retention passed.');
  }
  if (process.argv.includes('--diagnostics')) {
    const originalServer = await page.locator('#server_url').inputValue();
    const printer = page.locator('form[action="/printers/check"]').filter({
      has: page.locator('input[name="mac"][value="02:77:00:00:00:01"]'),
    });
    assert.equal(await printer.count(), 1, 'Expected the isolated lab printer');
    const checked = page.waitForResponse(r => r.url() === base + '/printers/check' && r.request().method() === 'POST');
    await printer.locator('button').click();
    assert.equal((await checked).status(), 303);
    await page.waitForURL(base + '/#printers');
    assert.match(await page.locator('.probe-result').innerText(), /TCP port 9100 je dostupný/);
    assert.equal(await page.locator('#server_url').inputValue(), originalServer);
    await page.screenshot({ path: new URL('../../dist/virtualbox/printer-diagnostics.png', import.meta.url).pathname, fullPage: true });
    await page.goto(base + '/jobs');
    assert.equal(await page.locator('h1').innerText(), 'Tiskové úlohy');
    assert((await page.locator('table.history tbody tr').count()) <= 50);
    await page.locator('#state').selectOption('UNKNOWN');
    await page.locator('.history-filter button').click();
    await page.waitForURL(base + '/jobs?state=UNKNOWN');
    assert.equal(await page.locator('#state').inputValue(), 'UNKNOWN');
    await page.goto(base + '/jobs');
    await page.screenshot({ path: new URL('../../dist/virtualbox/print-history.png', import.meta.url).pathname, fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'History overflows mobile viewport');
    const resultCell = await page.locator('table.history tbody tr:first-child td:last-child').boundingBox();
    assert(resultCell && resultCell.x + resultCell.width <= 390, 'Print result is offscreen on mobile');
    await page.screenshot({ path: new URL('../../dist/virtualbox/print-history-mobile.png', import.meta.url).pathname, fullPage: true });
    await page.goto(base + '/');
    assert.equal(await page.locator('#server_url').inputValue(), originalServer);
    console.log('Printer probe, history, state filter and mobile layout passed without changing cloud settings.');
  }
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
