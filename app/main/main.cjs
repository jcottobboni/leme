// Electron shell: starts the Go engine on loopback and opens the UI it serves.
const { app, BrowserWindow, shell, Menu, dialog, ipcMain, safeStorage } = require('electron')
const { spawn } = require('node:child_process')
const crypto = require('node:crypto')
const path = require('node:path')
const fs = require('node:fs')
const readline = require('node:readline')

const isPackaged = app.isPackaged
const root = path.join(__dirname, '..')

// Same lowercase directory as the engine's settings (~/.config/leme), whatever the display name.
if (!app.commandLine.hasSwitch('user-data-dir')) app.setPath('userData', path.join(app.getPath('appData'), 'leme'))

// UI state that must survive restarts. The page origin changes on every launch (random engine
// port), so the renderer's localStorage starts empty each time: the shell keeps a copy on disk.
const UI_KEYS = new Set(['leme.ui.v1', 'leme.clusterProfiles.v1', 'leme.layout.v1'])
const UI_MAX = 2_000_000
let uiState
let uiTimer
let appOrigin
const uiStateFile = () => path.join(app.getPath('userData'), 'ui-state.json')

function loadUiState() {
  if (uiState) return uiState
  uiState = {}
  try {
    const saved = JSON.parse(fs.readFileSync(uiStateFile(), 'utf8'))
    for (const [k, v] of Object.entries(saved)) if (UI_KEYS.has(k) && typeof v === 'string' && v.length <= UI_MAX) uiState[k] = v
  } catch {
    /* missing or unreadable: start empty */
  }
  return uiState
}

function flushUiState() {
  clearTimeout(uiTimer)
  if (!uiState) return
  try {
    fs.mkdirSync(path.dirname(uiStateFile()), { recursive: true, mode: 0o700 })
    const tmp = uiStateFile() + '.tmp'
    fs.writeFileSync(tmp, JSON.stringify(uiState), { mode: 0o600 })
    fs.renameSync(tmp, uiStateFile())
  } catch (err) {
    process.stderr.write(`[shell] cannot save UI state: ${err}\n`)
  }
}

// AI provider API keys. Encrypted with the OS keychain (safeStorage) in userData/ai-keys.json;
// without a keychain they live only for the session and are never written in clear text. The
// renderer can set a key but never read one back; the engine receives keys over its local API.
const AI_KEYS_FILE = () => path.join(app.getPath('userData'), 'ai-keys.json')
const aiKeys = new Map() // provider|baseURL -> { provider, baseURL, key }

function keychainAvailable() {
  if (!safeStorage.isEncryptionAvailable()) return false
  return process.platform !== 'linux' || safeStorage.getSelectedStorageBackend() !== 'basic_text'
}

function loadAiKeys() {
  // Only touch the keychain when there is something to decrypt: querying it can block on an
  // unlock prompt, and most users never configure AI.
  if (!fs.existsSync(AI_KEYS_FILE()) || !keychainAvailable()) return
  try {
    for (const e of JSON.parse(fs.readFileSync(AI_KEYS_FILE(), 'utf8'))) {
      aiKeys.set(`${e.provider}|${e.baseURL}`, { provider: e.provider, baseURL: e.baseURL, key: safeStorage.decryptString(Buffer.from(e.enc, 'base64')) })
    }
  } catch {
    /* unreadable: no keys */
  }
}

function saveAiKeys() {
  if (!keychainAvailable()) return
  const out = [...aiKeys.values()].map((e) => ({ provider: e.provider, baseURL: e.baseURL, enc: safeStorage.encryptString(e.key).toString('base64') }))
  fs.mkdirSync(path.dirname(AI_KEYS_FILE()), { recursive: true, mode: 0o700 })
  fs.writeFileSync(AI_KEYS_FILE(), JSON.stringify(out), { mode: 0o600 })
}

async function pushAiKey(e) {
  if (!engineURL) return
  await fetch(`${engineURL}/api/ai/keys`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${engineToken}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider: e.provider, baseURL: e.baseURL, key: e.key }),
  }).catch((err) => process.stderr.write(`[shell] cannot hand the AI key to the engine: ${err}\n`))
}

// Only the app's own page may read or write it.
const fromApp = (e) => !!appOrigin && !!e.senderFrame && e.senderFrame.url.startsWith(appOrigin + '/')
ipcMain.on('ui-state:load', (e) => {
  e.returnValue = fromApp(e) ? loadUiState() : {}
})
ipcMain.handle('ai-key:set', async (e, provider, baseURL, key) => {
  if (!fromApp(e) || !['anthropic', 'openai', 'ollama'].includes(provider) || typeof key !== 'string' || key.length > 4096) return { ok: false }
  const base = String(baseURL || '').replace(/\/+$/, '')
  const entry = { provider, baseURL: base, key: key.trim() }
  if (entry.key) aiKeys.set(`${provider}|${base}`, entry)
  else aiKeys.delete(`${provider}|${base}`)
  saveAiKeys()
  await pushAiKey(entry)
  return { ok: true, stored: keychainAvailable() ? 'keychain' : 'session' }
})
ipcMain.handle('ai-key:status', (e) => (fromApp(e) ? { keychain: keychainAvailable() } : {}))

ipcMain.on('ui-state:set', (e, key, value) => {
  if (!fromApp(e) || !UI_KEYS.has(key)) return
  const state = loadUiState()
  if (value === null) delete state[key]
  else if (typeof value === 'string' && value.length <= UI_MAX) state[key] = value
  else return
  clearTimeout(uiTimer)
  uiTimer = setTimeout(flushUiState, 300)
})

function enginePath() {
  const bin = process.platform === 'win32' ? 'leme.exe' : 'leme'
  return isPackaged ? path.join(process.resourcesPath, 'engine', bin) : path.join(root, '..', 'engine', 'bin', bin)
}

function staticDir() {
  return isPackaged ? path.join(process.resourcesPath, 'renderer-dist') : path.join(root, 'renderer-dist')
}

let engine
let engineURL
let engineToken
let mainWindow

function startEngine() {
  return new Promise((resolve, reject) => {
    const token = crypto.randomBytes(32).toString('hex')
    const args = ['serve', '--addr', '127.0.0.1:0', '--static', staticDir()]
    for (const k of (process.env.LEME_EXTRA_KUBECONFIG || '').split(path.delimiter).filter(Boolean)) args.push('--kubeconfig', k)
    if (!isPackaged && process.env.LEME_DEV_ORIGIN) args.push('--dev-origin', process.env.LEME_DEV_ORIGIN)
    engine = spawn(enginePath(), args, {
      env: { ...process.env, LEME_TOKEN: token, LEME_EXIT_ON_STDIN_EOF: '1' },
      stdio: ['pipe', 'pipe', 'pipe'],
    })
    engine.on('error', reject)
    engine.stderr.on('data', (d) => process.stderr.write(`[engine] ${d}`))
    const rl = readline.createInterface({ input: engine.stdout })
    const timer = setTimeout(() => reject(new Error('engine did not start in 15s')), 15000)
    rl.on('line', (line) => {
      try {
        const msg = JSON.parse(line)
        if (msg.event === 'ready') {
          clearTimeout(timer)
          engineURL = `http://127.0.0.1:${msg.port}`
          engineToken = token
          resolve({ port: msg.port, token })
        }
      } catch {
        process.stdout.write(`[engine] ${line}\n`)
      }
    })
    engine.on('exit', (code) => {
      clearTimeout(timer)
      if (!app.isQuitting) {
        dialog.showErrorBox('Leme', `O engine encerrou inesperadamente (código ${code}).`)
        app.quit()
      }
    })
  })
}

async function createWindow() {
  const { port, token } = await startEngine()
  loadAiKeys()
  for (const e of aiKeys.values()) await pushAiKey(e)
  const win = new BrowserWindow({
    width: 1500,
    height: 920,
    backgroundColor: '#0e1013',
    title: 'Leme',
    icon: path.join(__dirname, 'icon.png'),
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      spellcheck: false,
    },
  })
  const origin = `http://127.0.0.1:${port}`
  appOrigin = origin
  // Keep the window on our origin; open everything else (e.g. port-forward URLs) in the system browser.
  const openOutside = (url) => {
    try {
      const u = new URL(url)
      if (u.protocol === 'http:' || u.protocol === 'https:') shell.openExternal(u.toString())
    } catch {
      /* ignore malformed URLs */
    }
  }
  const guard = (e, url) => {
    if (!url.startsWith(origin + '/')) {
      e.preventDefault()
      openOutside(url)
    }
  }
  const secure = { contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true, spellcheck: false }
  const harden = (wc) => {
    wc.on('will-navigate', guard)
    wc.on('will-redirect', guard)
    wc.on('will-frame-navigate', (e) => {
      if (!e.url.startsWith(origin + '/')) e.preventDefault()
    })
    wc.setWindowOpenHandler(({ url }) => {
      // Only the dock's own blank popout page may open as an app window (panels rendered by the main window).
      if (wc === win.webContents && url === `${origin}/popout.html`) {
        return { action: 'allow', overrideBrowserWindowOptions: { backgroundColor: '#0e1013', autoHideMenuBar: true, title: 'Leme', webPreferences: secure } }
      }
      openOutside(url)
      return { action: 'deny' }
    })
  }
  harden(win.webContents)
  win.webContents.on('did-create-window', (child) => harden(child.webContents))
  const allowed = new Set(['clipboard-sanitized-write'])
  win.webContents.session.setPermissionRequestHandler((_wc, permission, cb) => cb(allowed.has(permission)))
  win.webContents.session.setPermissionCheckHandler((_wc, permission) => allowed.has(permission))
  mainWindow = win
  await win.loadURL(`${origin}/#token=${token}`)
}

Menu.setApplicationMenu(
  Menu.buildFromTemplate([
    { label: 'Arquivo', submenu: [{ role: 'reload', label: 'Recarregar' }, ...(isPackaged ? [] : [{ role: 'toggleDevTools', label: 'DevTools' }]), { type: 'separator' }, { role: 'quit', label: 'Sair' }] },
    { label: 'Editar', submenu: [{ role: 'undo' }, { role: 'redo' }, { type: 'separator' }, { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
    { label: 'Exibir', submenu: [{ role: 'zoomIn' }, { role: 'zoomOut' }, { role: 'resetZoom' }, { role: 'togglefullscreen' }] },
  ]),
)

app.on('web-contents-created', (_e, contents) => {
  contents.on('will-attach-webview', (e) => e.preventDefault())
})

if (!app.requestSingleInstanceLock()) {
  app.quit()
  process.exit(0)
}

app.on('second-instance', () => {
  if (!mainWindow) return
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.focus()
})

app.whenReady().then(() => {
  if (!fs.existsSync(enginePath())) {
    dialog.showErrorBox('Leme', `Engine não encontrado em ${enginePath()}. Rode "make engine".`)
    app.quit()
    return
  }
  createWindow().catch((err) => {
    dialog.showErrorBox('Leme', String(err))
    app.quit()
  })
})

app.on('before-quit', () => {
  app.isQuitting = true
  flushUiState()
  if (engine && !engine.killed) {
    engine.stdin.end()
    engine.kill('SIGTERM')
  }
})

app.on('window-all-closed', () => app.quit())

module.exports = { get engineURL() { return engineURL } }
