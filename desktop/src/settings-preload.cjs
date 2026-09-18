const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('connection', {
  read: () => ipcRenderer.invoke('connection:read'),
  save: value => ipcRenderer.invoke('connection:save', value),
});
