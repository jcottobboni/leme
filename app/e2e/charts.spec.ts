// Charts v2: overview namespaces/headroom and metric cards. Kind cluster ONLY.
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

async function openOverview(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-overview').click()
}

test('CH-1: tabela de namespaces com barra empilhada por fase e filtro ao clicar', async ({ page }) => {
  await openOverview(page)
  const table = page.getByTestId('ov-namespaces')
  await expect(table).toBeVisible({ timeout: 20_000 })
  const demo = page.getByTestId('ns-row-demo')
  await expect(demo).toBeVisible()
  // The stacked bar has one segment per phase present; demo runs web pods, so at least one.
  expect(await demo.locator('.ns-bar i').count()).toBeGreaterThanOrEqual(1)
  await expect(page.locator('.ns-legend')).toContainText('Running')
  await expect(page.locator('.ns-legend')).toContainText('Pending')
  // Sorting by name flips the order of the first column.
  await table.getByRole('columnheader', { name: 'Namespace' }).click()
  const names = await table.locator('tbody tr td:first-child').allInnerTexts()
  expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b)))
  await demo.click()
  await expect(demo).toHaveClass(/ns-active/)
  await expect(page.getByText('ns: demo')).toBeVisible()
  await demo.click()
  await expect(demo).not.toHaveClass(/ns-active/)
  // Top consumers toggles between CPU and memory.
  await expect(page.getByTestId('ov-top')).toBeVisible()
  await page.getByTestId('ov-top').getByRole('button', { name: 'Memória' }).click()
  await expect(page.getByTestId('ov-top').getByRole('button', { name: 'Memória' })).toHaveClass(/on/)
})

test('CH-2: barras de headroom e compromisso do alocável', async ({ page }) => {
  await openOverview(page)
  const cpu = page.getByTestId('hb-cpu')
  await expect(cpu).toContainText('Uso de CPU', { timeout: 20_000 })
  await expect(cpu.locator('.hb-pct')).toContainText('%')
  await expect(cpu).toContainText('Reservado')
  await expect(cpu).toContainText('Limit')
  const mem = page.getByTestId('hb-mem')
  await expect(mem).toContainText('Uso de memória')
  await expect(page.getByTestId('ov-headroom')).toContainText('Compromisso do alocável')
  for (const t of ['Pods', 'CPU', 'Memória']) await expect(page.getByTestId(`commit-${t}`)).toContainText('solicitados de')
})

test('CH-3: cartão de métrica com chips de legenda, valores e expandir', async ({ page }) => {
  await openOverview(page)
  const card = page.getByTestId('chart-cpu')
  await expect(card.locator('canvas').first()).toBeVisible({ timeout: 30_000 })
  const chips = card.locator('.mc-chip')
  expect(await chips.count()).toBeGreaterThanOrEqual(1)
  // Every chip carries a name and a formatted value (never the empty placeholder for the usage series).
  const usage = chips.first()
  await expect(usage.locator('b')).not.toHaveText('—')
  await expect(usage.locator('b')).toHaveText(/\d/)
  await expect(page.getByTestId('chart-network').locator('.mc-chip')).toHaveCount(2)
  await expect(page.getByTestId('chart-network')).toContainText('Recebido (RX)')
  // Toggling a chip dims it.
  await usage.click()
  await expect(usage).toHaveClass(/off/)
  await usage.click()
  await expect(usage).not.toHaveClass(/off/)
  // Expand takes the whole row.
  const before = (await card.boundingBox())!.width
  await page.getByTestId('chart-expand-cpu').click()
  await expect(card).toHaveClass(/expanded/)
  await expect.poll(async () => (await card.boundingBox())!.width).toBeGreaterThan(before * 1.5)
  await page.getByTestId('chart-expand-cpu').click()
  await expect(card).not.toHaveClass(/expanded/)
  // Crosshair tooltip with the values at the cursor.
  const over = card.locator('.mc-plot .u-over')
  const box = (await over.boundingBox())!
  await page.mouse.move(box.x + box.width * 0.5, box.y + box.height * 0.5)
  await expect(card.locator('.mc-tip')).toBeVisible()
})

test('CH-4: tema claro recria os gráficos', async ({ page }) => {
  await openOverview(page)
  const card = page.getByTestId('chart-memory')
  await expect(card.locator('canvas').first()).toBeVisible({ timeout: 30_000 })
  await page.getByTestId('theme-toggle').click()
  await expect(card.locator('canvas').first()).toBeVisible()
  await expect(card.locator('.mc-chip').first()).toBeVisible()
})
