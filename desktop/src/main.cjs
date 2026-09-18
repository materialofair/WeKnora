const { app, BrowserWindow, Menu, dialog, ipcMain, shell } = require('electron');
const fs = require('node:fs/promises');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { Backend, serverURL } = require('./backend.cjs');

if (process.env.WEKNORA_DESKTOP_DATA_DIR) app.setPath('userData', path.resolve(process.env.WEKNORA_DESKTOP_DATA_DIR));

let backend, mainWindow, settingsWindow, quitting = false, stopping = false;
const settingsURL = pathToFileURL(path.join(__dirname, 'settings.html')).href;
const configPath = () => path.join(app.getPath('userData'), 'connection.json');

async function readConfig() {
  try { return JSON.parse(await fs.readFile(configPath(), 'utf8')); }
  catch (error) { if (error.code === 'ENOENT') return {}; throw error; }
}

function secureWindow(options = {}) {
  const win = new BrowserWindow({ width: 1280, height: 840, minWidth: 800, minHeight: 600,
    title: 'Knowledge Assistant', ...options,
    webPreferences: { contextIsolation: true, sandbox: true, nodeIntegration: false,
      webSecurity: true, ...options.webPreferences },
  });
  win.webContents.session.setPermissionRequestHandler((_wc, _permission, callback) => callback(false));
  win.webContents.session.setPermissionCheckHandler(() => false);
  win.webContents.on('will-attach-webview', event => event.preventDefault());
  return win;
}

async function showSettings() {
  if (settingsWindow) { settingsWindow.focus(); return; }
  settingsWindow = secureWindow({ width: 650, height: 620, minWidth: 600, minHeight: 560,
    webPreferences: { preload: path.join(__dirname, 'settings-preload.cjs') } });
  settingsWindow.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  settingsWindow.webContents.on('will-navigate', event => event.preventDefault());
  settingsWindow.on('closed', () => { settingsWindow = null; });
  await settingsWindow.loadURL(settingsURL);
}

function verifySettings(event) {
  if (!settingsWindow || event.sender !== settingsWindow.webContents ||
    event.senderFrame !== settingsWindow.webContents.mainFrame || event.senderFrame.url !== settingsURL) {
    throw new Error('Invalid settings sender');
  }
}

ipcMain.handle('connection:read', async event => { verifySettings(event); return readConfig(); });
ipcMain.handle('connection:save', async (event, input) => {
  verifySettings(event);
  const config = input.mode === 'remote'
    ? { mode: 'remote', url: serverURL(input.url, input.allowHTTP === true), allowHTTP: input.allowHTTP === true }
    : { mode: 'local' };
  await fs.mkdir(app.getPath('userData'), { recursive: true, mode: 0o700 });
  await fs.writeFile(`${configPath()}.tmp`, JSON.stringify(config), { mode: 0o600 });
  await fs.rename(`${configPath()}.tmp`, configPath());
  app.relaunch();
  setImmediate(() => app.quit());
});

async function openAssistant() {
  const config = await readConfig();
  let url;
  if (config.mode === 'remote') {
    url = serverURL(config.url, config.allowHTTP === true);
  } else {
    const resources = app.isPackaged ? path.join(process.resourcesPath, 'backend')
      : (process.env.WEKNORA_BACKEND_RESOURCES || path.resolve(__dirname, '../../dist/portable', `${process.platform}-${process.arch}`));
    backend = new Backend({ resources, data: path.join(app.getPath('userData'), 'data'),
      executable: path.join(resources, process.platform === 'win32' ? 'server.exe' : 'server'),
      onExit: error => dialog.showErrorBox('后端已停止', error.message),
    });
    url = await backend.start();
  }
  mainWindow = secureWindow();
  // No preload or IPC bridge is installed on the application window,
  // including in remote mode. Authentication uses normal web origin scoping.
  const guardNavigation = (event, target) => {
    try { if (new URL(target).origin === new URL(url).origin) return; } catch {}
    event.preventDefault();
  };
  mainWindow.webContents.on('will-navigate', guardNavigation);
  mainWindow.webContents.on('will-redirect', guardNavigation);
  mainWindow.webContents.setWindowOpenHandler(({ url: target }) => {
    try {
      const parsed = new URL(target);
      if (['https:', 'http:'].includes(parsed.protocol) && !parsed.username && !parsed.password) {
        dialog.showMessageBox(mainWindow, { type: 'question', message: '在浏览器中打开链接？',
          detail: parsed.href, buttons: ['取消', '打开'], defaultId: 0, cancelId: 0,
        }).then(({ response }) => { if (response === 1) return shell.openExternal(parsed.href); }).catch(() => {});
      }
    } catch {}
    return { action: 'deny' };
  });
  mainWindow.on('closed', () => { mainWindow = null; });
  await mainWindow.loadURL(url);
}

if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.enableSandbox();
  app.on('second-instance', () => { const win = mainWindow || settingsWindow; win?.restore(); win?.focus(); });
  app.on('window-all-closed', () => app.quit());
  app.on('before-quit', event => {
    if (quitting) return;
    event.preventDefault();
    if (stopping) return;
    stopping = true;
    Promise.resolve(backend?.stop()).then(() => { quitting = true; app.quit(); }).catch(error => {
      stopping = false; dialog.showErrorBox('无法安全退出', error.message);
    });
  });
  app.whenReady().then(async () => {
    Menu.setApplicationMenu(Menu.buildFromTemplate([
      { label: 'Knowledge Assistant', submenu: [
        { label: '本地 / 公司服务器设置', click: showSettings },
        { label: '打开数据目录', click: () => shell.openPath(app.getPath('userData')) },
        { type: 'separator' }, { role: 'quit' },
      ] },
      { role: 'editMenu' }, { role: 'viewMenu' },
    ]));
    try { await openAssistant(); }
    catch (error) { dialog.showErrorBox('无法启动知识库助手', error.message); await showSettings(); }
  });
}
