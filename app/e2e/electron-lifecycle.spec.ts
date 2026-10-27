// Desktop lifecycle: closing the main window quits the app even with a popout window open. Fake contexts only;
// nothing connects to a cluster.
import { test, expect, _electron as electron } from '@playwright/test'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const FAKES = path.join(__dirname, 'fixtures', 'sweep-fakes.kubeconfig')

test('desktop: fechar a janela principal encerra o app, mesmo com janela destacada aberta', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-lifecycle-'))
  const app = await electron.launch({
    args: [...(process.platform === 'linux' ? ['--no-sandbox'] : []), `--user-data-dir=${path.join(tmp, 'user-data')}`, path.join(__dirname, '..')],
    env: { ...process.env, KUBECONFIG: FAKES, LEME_CONFIG_DIR: path.join(tmp, 'config') },
  })
  try {
    const win = await app.firstWindow()
    await expect(win.locator('[data-testid^="cluster-card-"]').first()).toBeVisible({ timeout: 60_000 })
    const [popout] = await Promise.all([app.waitForEvent('window'), win.getByTestId('popout').last().click()])
    await popout.waitForLoadState()
    // A window that does not depend on the page, like a Grafana embed (opening a real one needs a port-forward).
    await app.evaluate(({ BrowserWindow }) => {
      new BrowserWindow({ show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } }).loadURL('about:blank')
    })
    expect(await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().length)).toBe(3)
    const exited = new Promise<void>((resolve) => app.process().once('exit', () => resolve()))
    // Close the main window the way the user does (window manager close), not app.quit().
    await app.evaluate(({ BrowserWindow }) => {
      const main = BrowserWindow.getAllWindows().sort((a, b) => a.id - b.id)[0]
      main?.close()
    })
    await Promise.race([exited, new Promise((_, rej) => setTimeout(() => rej(new Error('app still running 15 s after closing the main window')), 15_000))])
  } finally {
    await app.close().catch(() => {})
    fs.rmSync(tmp, { recursive: true, force: true })
  }
})
