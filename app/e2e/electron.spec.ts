// Desktop smoke test: launches the real Electron app pinned to the isolated kind kubeconfig.
import { test, expect, _electron as electron } from '@playwright/test'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const shots = (name: string) => path.join(__dirname, 'screenshots', name)

test('desktop: abre, conecta no kind, telas do M2 e janela destacada', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-electron-'))
  const app = await electron.launch({
    args: ['--no-sandbox', path.join(__dirname, '..')],
    env: {
      ...process.env,
      KUBECONFIG: path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig'),
      LEME_EXTRA_KUBECONFIG: path.join(__dirname, 'fake-prod.kubeconfig'),
      LEME_CONFIG_DIR: path.join(tmp, 'config'),
      // Own user-data dir: the single-instance lock must not collide with an installed Leme that is open.
      XDG_CONFIG_HOME: path.join(tmp, 'xdg'),
      HELM_CONFIG_HOME: path.join(tmp, 'helm'),
      HELM_CACHE_HOME: path.join(tmp, 'helm-cache'),
      HELM_DATA_HOME: path.join(tmp, 'helm-data'),
    },
  })
  try {
    const win = await app.firstWindow()
    await expect(win.locator('.cluster-name', { hasText: 'kind-leme-dev' }).first()).toBeVisible({ timeout: 30_000 })
    await win.screenshot({ path: shots('01-inicio.png') })
    await win.getByTestId('cluster-row-kind-leme-dev').first().click()
    await win.getByTestId('nav-kind-Pod').click()
    await win.locator('.ns-select:visible').first().selectOption('demo')
    await expect(win.getByTestId('row-logger')).toBeVisible()
    await win.waitForTimeout(1500)
    await win.screenshot({ path: shots('02-pods.png') })
    await win.locator('[data-testid^="row-broken-"]').first().click()
    await expect(win.locator('.container-card')).toBeVisible()
    await win.screenshot({ path: shots('03-detalhe-pod.png') })
    await win.getByTestId('nav-kind-Deployment').click()
    await win.getByTestId('row-web').click()
    await win.waitForTimeout(800)
    await win.screenshot({ path: shots('04-deployment.png') })

    // M2 screens.
    await win.getByTestId('tab-topology').click()
    await expect(win.getByTestId('topo-Deployment/demo/web')).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(800)
    await win.screenshot({ path: shots('05-topologia-deployment.png') })
    await win.getByTestId('tab-metrics').click()
    await expect(win.getByTestId('chart-cpu').locator('canvas').first()).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(800)
    await win.screenshot({ path: shots('06-metricas.png') })
    await win.getByTestId('tool-topology').click()
    await expect(win.getByTestId('topology').filter({ visible: true }).getByTestId('topo-Deployment/demo/broken')).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(800)
    await win.screenshot({ path: shots('07-topologia-namespace.png') })
    await win.getByTestId('tool-timeline').click()
    await expect(win.getByTestId('timeline')).toBeVisible()
    await win.waitForTimeout(2000)
    await win.screenshot({ path: shots('08-timeline.png') })
    await win.getByTestId('tool-rbac').click()
    await expect(win.getByTestId('rbac-full').or(win.getByTestId('rbac-matrix'))).toBeVisible()
    await win.screenshot({ path: shots('09-rbac.png') })
    await win.getByTestId('tool-security').click()
    await expect(win.getByTestId('score').first()).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(800)
    await win.screenshot({ path: shots('12-seguranca.png') })
    await win.getByTestId('tool-netpol').click()
    await expect(win.getByTestId('netpol-graph')).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(1500)
    await win.screenshot({ path: shots('13-networkpolicy.png') })
    await win.getByTestId('tool-overview').click()
    await expect(win.getByTestId('ov-nodes')).toBeVisible({ timeout: 30_000 })
    await win.waitForTimeout(1500)
    await win.screenshot({ path: shots('10-overview.png') })

    // Pop the overview group out into its own window.
    const [popout] = await Promise.all([app.waitForEvent('window'), win.getByTestId('popout').last().click()])
    await expect(popout.locator('.overview-panel')).toBeVisible({ timeout: 15_000 })
    await popout.waitForTimeout(800)
    await popout.screenshot({ path: shots('11-janela-destacada.png') })
    const prefs = await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map((w) => w.webContents.getLastWebPreferences?.() ?? {}))
    for (const p of prefs) {
      expect(p.sandbox).toBe(true)
      expect(p.contextIsolation).toBe(true)
      expect(p.nodeIntegration).toBe(false)
    }
  } finally {
    await app.close()
    fs.rmSync(tmp, { recursive: true, force: true })
  }
})

test('desktop: clusters fixados, perfil, tema e abas sobrevivem ao reiniciar o app', async () => {
  // The engine port (and so the page origin) changes on every launch; state must still persist.
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-electron-persist-'))
  const launch = () =>
    electron.launch({
      args: ['--no-sandbox', path.join(__dirname, '..')],
      env: {
        ...process.env,
        KUBECONFIG: path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig'),
        LEME_EXTRA_KUBECONFIG: path.join(__dirname, 'fake-prod.kubeconfig'),
        LEME_CONFIG_DIR: path.join(tmp, 'config'),
        XDG_CONFIG_HOME: path.join(tmp, 'xdg'),
      },
    })
  const FAKE = 'fake-app-prd/api-ocp-prd-example:6443/someone'
  let app = await launch()
  try {
    const win = await app.firstWindow()
    await win.getByTestId(`cluster-card-${FAKE}`).click({ button: 'right' })
    await win.getByRole('menuitem', { name: 'Fixar na hotbar' }).click()
    await win.getByTestId(`hotbar-${FAKE}`).click({ button: 'right' })
    await win.getByRole('menuitem', { name: /Personalizar/ }).click()
    await win.getByTestId('cp-alias').fill('Produção A')
    await win.getByTestId('cp-short').fill('PA')
    await win.getByTestId('cp-save').click()
    await win.getByTestId('theme-toggle').click()
    await win.getByTestId('cluster-row-kind-leme-dev').first().click()
    await win.getByTestId('nav-kind-Deployment').dblclick()
    await expect(win.locator('.dv-tab .ctab', { hasText: 'Deployments' })).toBeVisible()
    await win.waitForTimeout(800) // the shell writes the state 300 ms after the last change
  } finally {
    await app.close()
  }
  expect(fs.statSync(path.join(tmp, 'xdg', 'leme', 'ui-state.json')).mode & 0o777).toBe(0o600)

  app = await launch()
  try {
    const win = await app.firstWindow()
    const hot = win.getByTestId(`hotbar-${FAKE}`)
    await expect(hot).toBeVisible({ timeout: 30_000 })
    await expect(hot.locator('.hot-label')).toHaveText('Produção A')
    await expect(hot.locator('.avatar-text')).toHaveText('PA')
    await expect(win.locator('html')).not.toHaveAttribute('data-theme', 'dark')
    // Tabs come back, but nothing reconnects on its own.
    await expect(win.locator('.dv-tab .ctab', { hasText: 'Deployments' })).toBeVisible()
    await expect(win.getByTestId('cluster-row-kind-leme-dev').first()).not.toHaveClass(/is-connected/)
  } finally {
    await app.close()
  }
})
