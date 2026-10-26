// The renderer talks to the engine over HTTP/WebSocket. The only bridge to the shell is the
// UI-state store (allowed keys only, enforced in the main process).
const { contextBridge, ipcRenderer } = require('electron')
contextBridge.exposeInMainWorld('leme', {
  platform: process.platform,
  uiState: {
    load: () => ipcRenderer.sendSync('ui-state:load'),
    set: (key, value) => ipcRenderer.send('ui-state:set', String(key), value == null ? null : String(value)),
  },
})
