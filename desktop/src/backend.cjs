const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const path = require('node:path');
const { randomUUID } = require('node:crypto');
const { setTimeout: delay } = require('node:timers/promises');

function serverURL(value, allowHTTP = false) {
  const url = new URL(value);
  const loopback = ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname);
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('服务器地址只能包含协议、主机和端口，不能包含密码、路径或查询参数。');
  }
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && (loopback || allowHTTP))) {
    throw new Error('请使用 HTTPS；内网 HTTP 需在设置中明确启用。');
  }
  return url.origin;
}

class Backend {
  constructor({ executable, resources, data, timeout = 120000, shutdownTimeout = 10000, onExit = () => {} }) {
    Object.assign(this, { executable, resources, data, timeout, shutdownTimeout, onExit });
    this.child = null;
    this.stopping = false;
  }

  async start() {
    await fs.mkdir(this.data, { recursive: true, mode: 0o700 });
    this.readyFile = path.join(this.data, `.desktop-ready-${randomUUID()}.json`);
    const logPath = path.join(this.data, 'desktop-backend.log');
    const existing = await fs.stat(logPath).catch(() => null);
    if (existing?.size > 5 * 1024 * 1024) await fs.rename(logPath, `${logPath}.previous`);
    const log = await fs.open(logPath, 'a', 0o600);
    const portFile = path.join(this.data, 'desktop-port.json');
    let port = 0;
    try {
      port = JSON.parse(await fs.readFile(portFile, 'utf8')).port;
      if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('本地端口配置无效。');
    } catch (error) { if (error.code !== 'ENOENT') { await log.close(); throw new Error(`本地端口配置损坏，请退出后检查或移走 ${portFile}：${error.message}`); } }
    const args = ['--portable', '--data-dir', this.data, '--resources-dir', this.resources,
      '--host', '127.0.0.1', '--port', String(port), '--ready-file', this.readyFile, '--exit-on-stdin-close'];
    this.child = spawn(this.executable, args, {
      cwd: this.resources, windowsHide: true, stdio: ['pipe', log.fd, log.fd],
      env: { ...process.env, GIN_MODE: 'release' },
    });
    let startupError;
    this.child.on('error', error => { startupError = error; });
    this.child.once('exit', (code, signal) => {
      startupError = new Error(`后端已退出 (${code ?? signal})，日志：${logPath}`);
      if (!this.stopping && this.url) this.onExit(startupError);
    });
    await log.close();
    const deadline = Date.now() + this.timeout;
    try {
      while (Date.now() < deadline) {
        if (startupError) throw startupError;
        let ready;
        try { ready = JSON.parse(await fs.readFile(this.readyFile, 'utf8')); }
        catch (error) { if (error.code !== 'ENOENT' && !(error instanceof SyntaxError)) throw error; }
        if (ready) {
          const url = serverURL(ready.url);
          if (ready.pid !== this.child.pid || new URL(url).hostname !== '127.0.0.1') {
            throw new Error('后端启动信息不匹配。');
          }
          const response = await fetch(`${url}/health`, { signal: AbortSignal.timeout(2000) }).catch(() => null);
          if (response?.ok) {
            if (!port) {
              const temporary = `${portFile}.${randomUUID()}.tmp`;
              try {
                await fs.writeFile(temporary, JSON.stringify({ port: Number(new URL(url).port) }), { mode: 0o600 });
                await fs.rename(temporary, portFile);
              } finally { await fs.rm(temporary, { force: true }); }
            }
            if (startupError) throw startupError;
            if (this.stopping) throw new Error('启动已取消。');
            this.url = url; return url;
          }
        }
        await delay(150);
      }
      throw new Error(`后端启动超时，日志：${logPath}`);
    } catch (error) {
      await this.stop();
      throw error;
    }
  }

  stop() {
    if (!this.stopPromise) this.stopPromise = this.stopOnce();
    return this.stopPromise;
  }

  async stopOnce() {
    this.stopping = true;
    const child = this.child;
    if (child?.pid && child.exitCode === null && child.signalCode === null) {
      const exited = new Promise(resolve => child.once('exit', resolve));
      child.stdin.on('error', () => {});
      child.stdin.end();
      let gracefulTimer, forceTimer;
      try {
        const graceful = await Promise.race([exited.then(() => true), new Promise(resolve => {
          gracefulTimer = setTimeout(() => resolve(false), this.shutdownTimeout);
        })]);
        if (!graceful) {
          child.kill('SIGKILL');
          await Promise.race([exited, new Promise((_, reject) => {
            forceTimer = setTimeout(() => reject(new Error('后端未能退出，请检查进程后重新启动。')), 5000);
          })]);
        }
      } finally { clearTimeout(gracefulTimer); clearTimeout(forceTimer); }
    }
    if (this.readyFile) await fs.rm(this.readyFile, { force: true });
  }
}

module.exports = { Backend, serverURL };
