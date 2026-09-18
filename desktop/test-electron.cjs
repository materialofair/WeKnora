const { _electron } = require('playwright-core');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
(async () => {
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-electron-'));
  let app;
  try {
    const packaged = process.argv[2];
    app = await _electron.launch({
      ...(packaged ? { executablePath: path.resolve(packaged), args: [] } : { args: [path.resolve(__dirname)] }),
      env: { ...process.env, WEKNORA_DESKTOP_DATA_DIR: data }, timeout: 120000,
    });
    const page = await app.firstWindow({ timeout: 120000 });
    await page.locator('input').first().waitFor({ timeout: 30000 });
    assert.equal(new URL(page.url()).hostname, '127.0.0.1');
    assert.equal(await page.evaluate(() => typeof window.require), 'undefined');
    assert.equal(await page.evaluate(() => typeof window.connection), 'undefined');
    await page.screenshot({ path: path.resolve(__dirname, '../dist/electron-login.png') });
    const port = JSON.parse(await fs.readFile(path.join(data, 'data', 'desktop-port.json'), 'utf8')).port;
    const next = app.waitForEvent('window');
    await app.evaluate(({ Menu }) => Menu.getApplicationMenu().items[0].submenu.items[0].click());
    const settings = await next;
    await settings.locator('input').first().waitFor();
    assert.equal(await settings.evaluate(() => typeof window.connection), 'object');
    await app.close(); app = null;
    // Stop acknowledgement must mean the listener is already closed.
    const health = await fetch(`http://127.0.0.1:${port}/health`).catch(() => null);
    assert.equal(health, null);
    console.log('PASS: real Electron login window, isolated renderer, settings window, graceful backend shutdown.');
  } finally {
    if (app) await app.close();
    await fs.rm(data, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
