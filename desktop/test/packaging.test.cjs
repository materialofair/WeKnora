const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const { spawnSync } = require('node:child_process');

test('packaging refuses an existing runtime data directory without altering it', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-package-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const scripts = path.join(root, 'scripts');
  const data = path.join(root, 'dist/portable', `${process.platform}-${process.arch}`, 'data');
  await fs.mkdir(scripts, { recursive: true });
  await fs.mkdir(data, { recursive: true });
  await fs.writeFile(path.join(data, 'original.txt'), 'preserve this user data');
  await fs.copyFile(path.resolve(__dirname, '../../scripts/package-portable.mjs'), path.join(scripts, 'package-portable.mjs'));
  const result = spawnSync(process.execPath, [path.join(scripts, 'package-portable.mjs')], { encoding: 'utf8' });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Existing runtime data/);
  assert.equal(await fs.readFile(path.join(data, 'original.txt'), 'utf8'), 'preserve this user data');
});
