#!/usr/bin/env node
// Build on each target OS; end users only run the generated executables.
import { spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = join(root, 'dist', 'portable', `${process.platform}-${process.arch}`);
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
mkdirSync(output, { recursive: true });
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
run(npm, ['run', 'build'], { cwd: join(root, 'frontend'), shell: process.platform === 'win32' });
run('bash', ['scripts/build-anydoc-lib.sh']);
const env = { ...process.env, CGO_ENABLED: '1' };
run('go', ['build', '-tags', 'sqlite_fts5,anydoc', '-ldflags', '-s -w', '-o', join(output, `server${exe}`), './cmd/server'], { env });
run('go', ['build', '-ldflags', '-s -w', '-o', join(output, `assistant-backup${exe}`), './cmd/assistant-backup'], { env });
for (const [source, destination] of [['config', 'config'], ['migrations', 'migrations'], ['frontend/dist', 'web']]) {
  cpSync(join(root, source), join(output, destination), { recursive: true });
}
const jieba = capture('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/yanyiwu/gojieba']);
mkdirSync(join(output, 'jieba'), { recursive: true });
for (const name of ['jieba.dict.utf8', 'hmm_model.utf8', 'user.dict.utf8', 'idf.utf8', 'stop_words.utf8']) {
  cpSync(join(jieba, 'deps', 'cppjieba', 'dict', name), join(output, 'jieba', name));
}
cpSync(join(root, 'LICENSE'), join(output, 'LICENSE'));
cpSync(join(root, 'docs', 'portable.md'), join(output, 'README.md'));
writeFileSync(join(output, 'start.sh'), '#!/bin/sh\nset -eu\nbase=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)\nexec "$base/server" --portable --resources-dir "$base" --data-dir "${WEKNORA_DATA_DIR:-$base/data}" --host "${WEKNORA_HOST:-127.0.0.1}" --port "${WEKNORA_PORT:-8080}"\n', { mode: 0o755 });
writeFileSync(join(output, 'start.cmd'), '@echo off\r\nif not defined WEKNORA_DATA_DIR set "WEKNORA_DATA_DIR=%~dp0data"\r\nif not defined WEKNORA_HOST set "WEKNORA_HOST=127.0.0.1"\r\nif not defined WEKNORA_PORT set "WEKNORA_PORT=8080"\r\n"%~dp0server.exe" --portable --resources-dir "%~dp0." --data-dir "%WEKNORA_DATA_DIR%" --host "%WEKNORA_HOST%" --port "%WEKNORA_PORT%"\r\n');
for (const required of [`server${exe}`, `assistant-backup${exe}`, 'config/config.yaml', 'web/index.html', 'migrations/sqlite', 'jieba/jieba.dict.utf8']) {
  if (!existsSync(join(output, required))) throw new Error(`Missing artifact: ${required}`);
}
console.log(`Portable bundle ready: ${output}`);
