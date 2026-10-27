// M6 layout polish: PanelHeader overflow, grid with fixed columns, inherited namespace, favorite CRDs.
// Read-only: it only browses the kind cluster. Kind cluster ONLY.
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

test.afterAll(() => {
  engine?.kill('SIGTERM')
})

async function connect(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

test('M6-1: conectar abre a Visão geral', async ({ page }) => {
  await connect(page)
  await expect(page.locator('.dv-tab .ctab', { hasText: 'Visão geral' })).toBeVisible()
  await expect(page.locator('.overview-panel .grid-title')).toContainText('Visão geral')
})

test('M6-2: a 1280 px a grade mantém Nome e ações fixos ao rolar na horizontal', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await connect(page)
  await page.getByTestId('nav-kind-Pod').click()
  const scroller = page.locator('.grid-scroll').filter({ visible: true }).first()
  const row = page.locator('.tr[data-testid^="row-"]').first()
  await expect(row).toBeVisible()
  const name = row.locator('.td.name')
  const kebab = row.locator('.td-actions')
  const before = (await name.boundingBox())!
  const panel = (await scroller.boundingBox())!
  // Actions are on screen without scrolling.
  const act = (await kebab.boundingBox())!
  expect(act.x + act.width).toBeLessThanOrEqual(panel.x + panel.width + 1)
  await scroller.evaluate((el) => (el.scrollLeft = el.scrollWidth))
  await expect(scroller).toHaveClass(/sl/)
  const after = (await name.boundingBox())!
  expect(Math.abs(after.x - before.x)).toBeLessThan(2)
  // No header label is cut ("PRONT…"): every label fits its cell.
  const cut = await page.locator('.grid-head .th-label').filter({ visible: true }).evaluateAll((els) => els.filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent))
  expect(cut).toEqual([])
})

test('M6-3: Topologia herda o namespace global e, sem ele, abre no namespace com mais workloads', async ({ page }) => {
  await connect(page)
  await page.getByTestId('tool-topology').click()
  const ns = page.locator('.topology .ns-select').filter({ visible: true }).first()
  await expect(ns).toBeVisible({ timeout: 20_000 })
  await expect(ns).not.toHaveValue('default')
  await ns.selectOption('kube-system')
  await expect(page.locator('.statusbar')).toContainText('ns: kube-system')
  await page.getByTestId('tool-netpol').click()
  await expect(page.locator('.ns-select').filter({ visible: true }).first()).toHaveValue('kube-system')
})

test('M6-4: PanelHeader leva ações que não cabem para o menu ⋯ sem quebrar linha', async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 800 })
  await connect(page)
  await page.getByTestId('tool-topology').click()
  const header = page.locator('.topology .toolbar.ph').filter({ visible: true }).first()
  await expect(header).toBeVisible({ timeout: 20_000 })
  expect((await header.boundingBox())!.height).toBeLessThan(60)
  await header.getByTestId('ph-more').click()
  await expect(page.getByRole('menuitem', { name: /Direção: Vertical/ })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(header.getByRole('button', { name: 'Atualizar' })).toBeVisible()
})

test('M6-5: CRD favorito vai para o topo de Custom Resources e persiste', async ({ page }) => {
  await connect(page)
  // Expand every CRD group (a CRD in a built-in group, like the fixture in "apps", has no star).
  for (const head of await page.locator('.nav-crd-head').all()) {
    await head.scrollIntoViewIfNeeded()
    await head.click()
  }
  const star = page.locator('[data-testid^="nav-star-"]').filter({ visible: true }).first()
  const id = (await star.getAttribute('data-testid'))!.slice('nav-star-'.length)
  await star.click()
  await expect(page.getByTestId(`nav-fav-${id}`)).toBeVisible()
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.getByTestId(`nav-fav-${id}`)).toBeAttached()
})
