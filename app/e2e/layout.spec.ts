// Layout (M3.5): hotbar, navigator, preview tabs, tab menu, shortcuts and bottom dock. Kind cluster ONLY.
import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

test.beforeAll(async () => {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => engine?.kill('SIGTERM'))

async function openConnected(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.getByTestId('nav-kind-Pod')).toBeVisible()
}

const tabs = (page: Page) => page.locator('.dv-tab .ctab')

test('CA-L1: conectar pelo catálogo põe o cluster na hotbar e abre o navigator', async ({ page }) => {
  await page.goto(`${base}/#token=${TOKEN}`)
  await expect(page.getByTestId(`hotbar-${KIND_CONTEXT}`)).toHaveCount(0)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.getByTestId(`hotbar-${KIND_CONTEXT}`)).toBeVisible()
  await expect(page.getByTestId(`hotbar-${KIND_CONTEXT}`).locator('.st-connected')).toBeVisible()
  for (const s of ['Cluster', 'Workloads', 'Config', 'Network', 'Storage', 'Access', 'Security', 'Helm']) {
    await expect(page.locator(`.nav-section[data-section="${s}"]`)).toBeVisible()
  }
  // "Outros recursos" starts collapsed.
  await expect(page.locator('.nav-section[data-section="Other"] .nav-item')).toHaveCount(0)
  // The catalog button brings back the list of contexts.
  await page.getByTestId('hotbar-catalog').click()
  await expect(page.locator('.navigator .cluster-row').first()).toBeVisible()
  await page.getByTestId(`hotbar-${KIND_CONTEXT}`).click()
  await expect(page.getByTestId('nav-kind-Pod')).toBeVisible()
})

test('CA-L2: aba temporária é reaproveitada; duplo clique mantém', async ({ page }) => {
  await openConnected(page)
  await page.getByTestId('nav-kind-Pod').click()
  await expect(tabs(page).filter({ hasText: 'Pods' })).toHaveClass(/preview/)
  await page.getByTestId('nav-kind-Deployment').click()
  await expect(tabs(page).filter({ hasText: 'Deployments' })).toBeVisible()
  await expect(tabs(page).filter({ hasText: 'Pods' })).toHaveCount(0)
  await expect(page.getByTestId('nav-kind-Deployment')).toHaveClass(/active/)
  // Double click pins.
  await page.getByTestId('nav-kind-Pod').dblclick()
  await expect(tabs(page).filter({ hasText: 'Pods' })).not.toHaveClass(/preview/)
  await page.getByTestId('nav-kind-Service').click()
  await expect(tabs(page).filter({ hasText: 'Pods' })).toBeVisible()
  await expect(tabs(page).filter({ hasText: 'Services' })).toBeVisible()
  // A row opens the details drawer next to the grid; the grid tab stays.
  await page.getByTestId('nav-kind-ConfigMap').click()
  await page.locator('.ns-select:visible').first().selectOption('demo')
  await page.getByTestId('row-app-config').click()
  await expect(page.getByTestId('drawer').locator('.obj-name', { hasText: 'app-config' })).toBeVisible()
  await expect(tabs(page).filter({ hasText: 'ConfigMaps' })).toBeVisible()
})

test('CA-L3: fechar outras, Ctrl+W e Ctrl+Shift+T', async ({ page }) => {
  await openConnected(page)
  for (const k of ['Pod', 'Deployment', 'Service']) await page.getByTestId(`nav-kind-${k}`).dblclick()
  await expect(tabs(page)).toHaveCount(4) // Início + 3
  await tabs(page).filter({ hasText: 'Deployments' }).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Fechar outras' }).click()
  await expect(tabs(page)).toHaveCount(1)
  await expect(tabs(page).first()).toContainText('Deployments')
  // Ctrl+W closes the active tab; the main area falls back to Início.
  await page.locator('.grid-title').click()
  await page.keyboard.press('Control+W')
  await expect(tabs(page).filter({ hasText: 'Deployments' })).toHaveCount(0)
  await expect(tabs(page).filter({ hasText: 'Início' })).toBeVisible()
  await page.keyboard.press('Control+Shift+T')
  await expect(tabs(page).filter({ hasText: 'Deployments' })).toBeVisible()
  // Close button and middle click.
  await tabs(page).filter({ hasText: 'Deployments' }).hover()
  await tabs(page).filter({ hasText: 'Deployments' }).locator('.ctab-close').click()
  await expect(tabs(page).filter({ hasText: 'Deployments' })).toHaveCount(0)
  await page.getByTestId('nav-kind-Pod').dblclick()
  await tabs(page).filter({ hasText: 'Pods' }).click({ button: 'middle' })
  await expect(tabs(page).filter({ hasText: 'Pods' })).toHaveCount(0)
})

test('CA-L4: logs abrem no dock inferior', async ({ page }) => {
  await openConnected(page)
  await page.getByTestId('nav-kind-Pod').click()
  await page.locator('.ns-select:visible').first().selectOption('demo')
  await page.locator('[data-testid^="row-web-"]').first().click()
  await page.getByRole('button', { name: 'Logs' }).click()
  const logsTab = tabs(page).filter({ hasText: /^logs / })
  await expect(logsTab).toBeVisible()
  const [a, b] = [await page.getByTestId('drawer').boundingBox(), await logsTab.boundingBox()]
  expect(b!.y).toBeGreaterThan(a!.y + 100)
  // Status bar shows the active cluster and the namespace.
  await expect(page.locator('.statusbar')).toContainText(KIND_CONTEXT)
  await expect(page.locator('.statusbar')).toContainText('ns: demo')
})
