// M5 feature grafana (desktop): "Abrir no app" opens the port-forwarded UI in an isolated Electron
// window (sandbox, no preload, in-memory session, navigation pinned to the forward's origin), and no
// credential reaches any window. Launches the real app pinned to the kind cluster ONLY (kind-leme-dev).
import { test, expect, _electron as electron, type ElectronApplication, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const FIXTURE = path.join(__dirname, 'fixtures', 'grafana', 'm5-grafana.yaml')
const MARKER = 'm5-grafana-marker-Qx7Zk2pW'
const MARKER_B64 = Buffer.from(MARKER).toString('base64')

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', timeout: 240_000 })
}

function expectNoMarker(text: string) {
  expect(text).not.toContain(MARKER)
  expect(text).not.toContain(MARKER_B64)
}

type Embed = { open: (url: string, title: string) => Promise<{ ok: boolean; error?: string }> }
const embedOpen = (win: Page, url: string) => win.evaluate((u) => (window as unknown as { leme: { embed: Embed } }).leme.embed.open(u, 'teste'), url)

test.beforeAll(() => {
  kubectl('apply', '-f', FIXTURE)
  kubectl('-n', 'm5-grafana', 'rollout', 'status', 'deploy/m5-grafana', '--timeout=180s')
})

test('CA04 (desktop): Grafana abre numa janela isolada do app via port-forward, sem credenciais', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-electron-gfn-'))
  let app: ElectronApplication | undefined
  try {
    app = await electron.launch({
      args: ['--no-sandbox', path.join(__dirname, '..')],
      env: {
        ...process.env,
        KUBECONFIG: KIND_KUBECONFIG,
        LEME_CONFIG_DIR: path.join(tmp, 'config'),
        XDG_CONFIG_HOME: path.join(tmp, 'xdg'),
        HELM_CONFIG_HOME: path.join(tmp, 'helm'),
      },
    })
    const win = await app.firstWindow()
    await win.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click({ timeout: 30_000 })
    const targetsResp = win.waitForResponse((r) => r.url().includes('/grafana/targets'))
    await win.getByTestId('tool-grafana').click()
    const body = await (await targetsResp).text()
    expectNoMarker(body)
    expect(JSON.parse(body).targets.some((t: { service: string; adminSecret?: unknown }) => t.service === 'm5-grafana' && t.adminSecret)).toBe(true)

    // embed:open validates in the main process: only 127.0.0.1 ports of active forwards.
    expect(await embedOpen(win, 'http://127.0.0.1:1/')).toMatchObject({ ok: false, error: 'no active port-forward on that port' })
    expect((await embedOpen(win, 'http://example.com:3000/')).ok).toBe(false)
    expect((await embedOpen(win, 'file:///etc/passwd')).ok).toBe(false)

    const open = win.getByTestId('gfn-open-m5-grafana-m5-grafana')
    await expect(open).toHaveText(/Abrir no app/, { timeout: 30_000 })
    const [child] = await Promise.all([app.waitForEvent('window'), open.click()])
    await child.waitForLoadState()
    await expect(child.locator('#m5-title')).toHaveText('m5 grafana')
    expect(child.url()).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/$/)
    const origin = new URL(child.url()).origin
    const port = new URL(child.url()).port

    // No bridge into the app, sandboxed renderer, in-memory partition.
    expect(await child.evaluate(() => typeof (window as unknown as { leme?: unknown }).leme)).toBe('undefined')
    expect(await child.evaluate(() => typeof (globalThis as unknown as { require?: unknown }).require)).toBe('undefined')
    const prefs = await app.evaluate(({ BrowserWindow, session }, u) => {
      const all = BrowserWindow.getAllWindows()
      const w = all.find((x) => x.webContents.getURL().startsWith(u))
      const p = (w?.webContents.getLastWebPreferences?.() ?? {}) as Record<string, unknown>
      const ses = w?.webContents.session
      return {
        sandbox: p.sandbox, contextIsolation: p.contextIsolation, nodeIntegration: p.nodeIntegration, preload: p.preload ?? null,
        persistent: ses?.isPersistent(), storage: ses?.getStoragePath() ?? null, title: w?.getTitle(),
        ownSession: !!ses && ses !== session.defaultSession && all.every((x) => x === w || x.webContents.session !== ses),
      }
    }, origin)
    expect(prefs).toMatchObject({ sandbox: true, contextIsolation: true, nodeIntegration: false, preload: null, persistent: false, storage: null, ownSession: true, title: 'Grafana — m5-grafana/m5-grafana' })

    // External navigation (link, location, popup) stays blocked: the window never leaves the forward's origin.
    // (clicked from the page: a prevented navigation would keep Playwright's click waiting for it)
    await child.evaluate(() => (document.getElementById('m5-external') as HTMLAnchorElement).click())
    await child.evaluate(() => {
      location.href = 'https://example.com/'
    })
    await child.waitForTimeout(1500)
    expect(new URL(child.url()).origin).toBe(origin)
    const before = app.windows().length
    expect(await child.evaluate(() => window.open('http://127.0.0.1:1/') === null)).toBe(true)
    await child.waitForTimeout(500)
    expect(app.windows().length).toBe(before)

    // The panel shows the active forward; no window shows the admin password.
    await expect(win.getByTestId(`gfn-forward-m5-grafana-m5-grafana`)).toContainText(`127.0.0.1:${port}`)
    await win.getByTestId('gfn-secret-m5-grafana-m5-grafana').click()
    await expect(win.locator('.dv-tab .ctab', { hasText: 'm5-grafana-admin' }).first()).toBeVisible()
    await win.waitForTimeout(1000)
    for (const w of app.windows()) expectNoMarker(await w.content())

    // After "Parar" the port no longer belongs to a forward: embed:open refuses it.
    await win.getByTestId('tool-grafana').click()
    await win.getByTestId('gfn-stop-m5-grafana-m5-grafana').click()
    await expect(win.getByTestId('gfn-forward-m5-grafana-m5-grafana')).toHaveCount(0)
    expect(await embedOpen(win, `http://127.0.0.1:${port}/`)).toMatchObject({ ok: false, error: 'no active port-forward on that port' })
  } finally {
    await app?.close()
    fs.rmSync(tmp, { recursive: true, force: true })
  }
})
