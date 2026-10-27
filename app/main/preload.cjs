// The renderer talks to the engine over HTTP/WebSocket. The bridges to the shell are the UI-state
// store and the write-only AI key store (allowed keys and providers only, enforced in the main process).
const { contextBridge, ipcRenderer } = require('electron')
contextBridge.exposeInMainWorld('leme', {
  platform: process.platform,
  uiState: {
    load: () => ipcRenderer.sendSync('ui-state:load'),
    set: (key, value) => ipcRenderer.send('ui-state:set', String(key), value == null ? null : String(value)),
  },
  // Write-only: a key can be set or cleared, never read back.
  aiKeys: {
    set: (provider, baseURL, key) => ipcRenderer.invoke('ai-key:set', String(provider), String(baseURL || ''), String(key || '')),
    status: () => ipcRenderer.invoke('ai-key:status'),
  },
  // Native file/folder picker for kubeconfigs; returns the chosen paths only.
  kubeconfig: {
    pick: (kind) => ipcRenderer.invoke('kubeconfig:pick', kind === 'dir' ? 'dir' : 'file'),
  },
  // Opens a port-forwarded web UI in an isolated window (validated in the main process).
  embed: {
    open: (url, title) => ipcRenderer.invoke('embed:open', String(url), String(title || '')),
  },
})
