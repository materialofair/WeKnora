const test = require('node:test');
const assert = require('node:assert/strict');
const { serverURL, Backend } = require('../src/backend.cjs');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');

test('server URLs require trusted schemes and no credentials or hidden targets', () => {
  assert.equal(serverURL('https://knowledge.internal:8443'), 'https://knowledge.internal:8443');
  assert.equal(serverURL('http://127.0.0.1:8888'), 'http://127.0.0.1:8888');
  assert.equal(serverURL('http://knowledge.internal', true), 'http://knowledge.internal');
  for (const value of ['file:///etc/passwd', 'javascript:alert(1)', 'https://user:secret@host',
    'https://host/path', 'https://host/?redirect=evil', 'http://knowledge.internal']) {
    assert.throws(() => serverURL(value));
  }
});

test('missing backend fails visibly and cleans its readiness file', async t => {
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-test-'));
  t.after(() => fs.rm(data, { recursive: true, force: true }));
  const backend = new Backend({ executable: path.join(data, 'absent'), resources: data, data, timeout: 200 });
  await assert.rejects(backend.start(), /ENOENT/);
  assert.equal((await fs.readdir(data)).some(name => name.startsWith('.desktop-ready')), false);
});

test('forced shutdown waits for exit, supports concurrent stop, and releases port', { skip: process.platform === 'win32' }, async t => {
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'assistant-lifecycle-'));
  t.after(() => fs.rm(data, { recursive: true, force: true }));
  const executable = path.join(data, 'fixture');
  await fs.writeFile(executable, `#!${process.execPath}\nconst http=require('node:http'),fs=require('node:fs');\nconst args=process.argv; const get=k=>args[args.indexOf(k)+1];\nconst server=http.createServer((q,r)=>r.end('ok'));\nserver.listen(Number(get('--port')),'127.0.0.1',()=>fs.writeFileSync(get('--ready-file'),JSON.stringify({pid:process.pid,url:'http://127.0.0.1:'+server.address().port})));\nprocess.stdin.resume(); // Intentionally ignores stdin EOF to exercise forced stop.\n`, { mode: 0o755 });
  const make = () => new Backend({ executable, resources: data, data, shutdownTimeout: 30 });
  let b = make();
  const url = await b.start();
  const firstStop = b.stop();
  assert.equal(b.stop(), firstStop);
  await firstStop;
  assert.ok(b.child.signalCode || b.child.exitCode !== null);
  b = make();
  try { assert.equal(await b.start(), url); }
  finally { await b.stop(); }
});
