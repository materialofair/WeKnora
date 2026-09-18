// Uses a real bundled backend and temporary storage; does not contact a model.
const { Backend } = require('../desktop/src/backend.cjs');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { spawnSync } = require('node:child_process');
(async () => {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-smoke-'));
  const resources = path.resolve(process.argv[2] || `dist/portable/${process.platform}-${process.arch}`);
  const suffix = process.platform === 'win32' ? '.exe' : '';
  const make = data => new Backend({ executable: path.join(resources, `server${suffix}`), resources, data });
  let backend = make(path.join(temp, 'data'));
  const credentials = { email: 'smoke@example.invalid', password: 'Smoke-test-48!Only', username: 'Smoke Test' };
  async function request(url, route, body) {
    const response = await fetch(url + route, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    assert.ok(response.ok, `${route}: HTTP ${response.status}: ${await response.clone().text()}`);
    return response.json();
  }
  function backup(action, data) {
    const r = spawnSync(path.join(resources, `assistant-backup${suffix}`), ['--data-dir', data, '--archive', path.join(temp, 'backup.tar.gz'), action], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
  }
  try {
    const url = await backend.start();
    assert.equal((await fetch(url)).status, 200);
    await request(url, '/api/v1/auth/register', credentials);
    const login = await request(url, '/api/v1/auth/login', credentials);
    assert.equal(login.success, true);
    await backend.stop();
    backend = make(path.join(temp, 'data'));
    assert.equal(await backend.start(), url, 'desktop origin must survive restart');
    assert.equal((await request(url, '/api/v1/auth/login', credentials)).success, true);
    await backend.stop();
    backup('backup', path.join(temp, 'data'));
    backup('restore', path.join(temp, 'restored'));
    backend = make(path.join(temp, 'restored'));
    const restoredURL = await backend.start();
    assert.equal((await request(restoredURL, '/api/v1/auth/login', credentials)).success, true);
    console.log('PASS: packaged server, Web UI, registration/login, stable origin, restart persistence, backup/restore.');
  } catch (error) {
    console.error(error.message);
    console.error(`Diagnostics retained in ${temp}`);
    process.exitCode = 1;
  } finally {
    await backend.stop();
    if (!process.exitCode) await fs.rm(temp, { recursive: true, force: true });
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
