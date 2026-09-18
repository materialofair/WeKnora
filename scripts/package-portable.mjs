#!/usr/bin/env node
// Build on each target OS; end users only run the generated executables.
import { spawn, spawnSync } from 'node:child_process';
import { createInterface } from 'node:readline';
import { cpSync, mkdirSync, writeFileSync, existsSync, mkdtempSync, renameSync } from 'node:fs';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const destination = join(root, 'dist', 'portable', `${process.platform}-${process.arch}`);
if (existsSync(join(destination, 'data'))) throw new Error(`Existing runtime data in ${destination}/data; move it to a separate data directory before packaging.`);
mkdirSync(join(root, 'dist'), { recursive: true });
const output = mkdtempSync(join(root, 'dist', `.portable-${process.platform}-${process.arch}-`));
const exe = process.platform === 'win32' ? '.exe' : '';
function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status})`);
}
function capture(command, args) {
  const r = spawnSync(command, args, { cwd: root, encoding: 'utf8' });
  if (r.status !== 0) throw new Error(r.stderr || String(r.error));
  return r.stdout.trim();
}
if (process.platform === 'win32') {
  // DuckDB's pinned static library predates GCC 16's Windows TLS ABI change.
  const gccVersion = capture(process.env.CC || 'gcc', ['-dumpfullversion']);
  if (Number.parseInt(gccVersion, 10) >= 16) {
    throw new Error(`DuckDB requires the GCC 15 UCRT64 toolchain; found GCC ${gccVersion}. See docs/portable.md.`);
  }
  if (process.env.MSYSTEM && process.env.MSYSTEM !== 'UCRT64') {
    throw new Error('Build from the MSYS2 UCRT64 shell; DuckDB does not support the MINGW64 CRT.');
  }
}
mkdirSync(output, { recursive: true });
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
run(npm, ['run', 'build'], { cwd: join(root, 'frontend'), shell: process.platform === 'win32',
  env: { ...process.env, NODE_OPTIONS: process.env.NODE_OPTIONS || '--max-old-space-size=4096' } });
run('bash', ['scripts/build-anydoc-lib.sh']);
const env = { ...process.env, CGO_ENABLED: '1' };
// A cloud desktop must not need MinGW DLLs installed alongside the app.
if (process.platform === 'win32') {
  env.CGO_LDFLAGS = `${env.CGO_LDFLAGS || ''} -static -static-libgcc -static-libstdc++`;
}
run('go', ['build', '-tags', 'sqlite_fts5,anydoc', '-ldflags', '-s -w', '-o', join(output, `server${exe}`), './cmd/server'], { env });
if (process.platform === 'win32') {
  // PE unwind tables can exceed spawnSync's buffer; inspect DLL lines as a stream.
  const externalRuntime = await new Promise((resolve, reject) => {
    const inspector = spawn('objdump', ['-p', join(output, `server${exe}`)], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] });
    const dependencies = new Set();
    let errors = '';
    createInterface({ input: inspector.stdout }).on('line', line => {
      if (/DLL Name:/i.test(line) && /lib(gcc|stdc|winpthread|c\+\+)/i.test(line)) dependencies.add(line.trim());
    });
    inspector.stderr.on('data', chunk => { errors = (errors + chunk.toString()).slice(-8192); });
    inspector.on('error', reject);
    inspector.on('close', code => code === 0 ? resolve([...dependencies]) : reject(new Error(`objdump failed (${code}): ${errors}`)));
  });
  if (externalRuntime.length) throw new Error(`Unbundled compiler runtime: ${externalRuntime.join(', ')}`);
}
run('go', ['build', '-ldflags', '-s -w', '-o', join(output, `assistant-backup${exe}`), './cmd/assistant-backup'], { env });
for (const [source, destination] of [['config', 'config'], ['migrations', 'migrations'], ['frontend/dist', 'web']]) {
  cpSync(join(root, source), join(output, destination), { recursive: true });
}
const jieba = capture('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/yanyiwu/gojieba']);
mkdirSync(join(output, 'jieba'), { recursive: true });
for (const name of ['jieba.dict.utf8', 'hmm_model.utf8', 'user.dict.utf8', 'idf.utf8', 'stop_words.utf8']) {
  cpSync(join(jieba, 'deps', 'cppjieba', 'dict', name), join(output, 'jieba', name));
}
run('bash', ['scripts/copy-licenses.sh', output]);
cpSync(join(root, 'docs', 'portable.md'), join(output, 'README.md'));
writeFileSync(join(output, 'start.sh'), '#!/bin/sh\nset -eu\nbase=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)\nexec "$base/server" --portable --resources-dir "$base" --data-dir "${WEKNORA_DATA_DIR:-$base/data}" --host "${WEKNORA_HOST:-127.0.0.1}" --port "${WEKNORA_PORT:-8080}"\n', { mode: 0o755 });
writeFileSync(join(output, 'start.cmd'), '@echo off\r\nif not defined WEKNORA_DATA_DIR set "WEKNORA_DATA_DIR=%~dp0data"\r\nif not defined WEKNORA_HOST set "WEKNORA_HOST=127.0.0.1"\r\nif not defined WEKNORA_PORT set "WEKNORA_PORT=8080"\r\n"%~dp0server.exe" --portable --resources-dir "%~dp0." --data-dir "%WEKNORA_DATA_DIR%" --host "%WEKNORA_HOST%" --port "%WEKNORA_PORT%"\r\n');
for (const required of [`server${exe}`, `assistant-backup${exe}`, 'config/config.yaml', 'web/index.html', 'migrations/sqlite', 'jieba/jieba.dict.utf8']) {
  if (!existsSync(join(output, required))) throw new Error(`Missing artifact: ${required}`);
}
// Build in a fresh staging directory: old runtime files must never enter a release.
mkdirSync(dirname(destination), { recursive: true });
if (existsSync(destination)) renameSync(destination, `${destination}.previous-${Date.now()}`);
renameSync(output, destination);
console.log(`Portable bundle ready: ${destination}`);
