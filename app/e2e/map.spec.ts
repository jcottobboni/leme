// Mapa do cluster: islands, tile click, search, level control. Kind cluster ONLY.
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

async function openMap(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-map').click()
  await expect(page.getByTestId('map-stage')).toBeVisible()
  await expect(page.getByTestId('map-island-demo')).toBeVisible()
}

test('CA-MAP1: ilhas por namespace, resumo e clique no workload abre os detalhes', async ({ page }) => {
  await openMap(page)
  await expect(page.getByTestId('map-island-monitoring')).toBeVisible()
  // System namespaces are hidden by default when there are user namespaces.
  await expect(page.getByTestId('map-island-kube-system')).toHaveCount(0)
  await expect(page.getByTestId('map-summary')).toContainText('namespaces')
  await expect(page.getByTestId('map-summary')).toContainText('workloads')
  await page.getByTestId('map-hide-system').click()
  await expect(page.getByTestId('map-island-kube-system')).toBeVisible()
  await page.getByTestId('map-hide-system').click()
  await expect(page.getByTestId('map-island-kube-system')).toHaveCount(0)

  const tile = page.getByTestId('map-tile-web')
  await expect(tile).toBeVisible()
  await tile.click()
  await expect(page.locator('.dv-tab .ctab', { hasText: 'web' }).first()).toBeVisible()
})

test('CA-MAP2: busca destaca resultados e filtro de problemas', async ({ page }) => {
  await openMap(page)
  await page.getByTestId('map-search').fill('broken')
  await expect(page.getByTestId('map-matches')).toContainText('1 resultado')
  await expect(page.getByTestId('map-tile-broken')).toHaveClass(/match/)
  await expect(page.getByTestId('map-tile-web')).toHaveClass(/map-dim/)
  await page.getByTestId('map-search').fill('')
  await page.getByTestId('map-problems').click()
  await expect(page.getByTestId('map-tile-broken')).toBeVisible()
  await expect(page.getByTestId('map-tile-web')).toHaveCount(0)
  await page.getByTestId('map-problems').click()
  await expect(page.getByTestId('map-tile-web')).toBeVisible()
})

test('CA-MAP3: níveis de zoom e ajustar ao conteúdo', async ({ page }) => {
  await openMap(page)
  const zoom = async () => parseInt((await page.getByTestId('map-zoom').innerText()).replace('%', ''), 10)
  await page.getByTestId('map-level-pods').click()
  await expect(page.getByTestId('map-level-pods')).toHaveClass(/active/)
  await expect.poll(zoom).toBeGreaterThan(200)
  await page.getByTestId('map-level-workloads').click()
  await expect(page.getByTestId('map-level-workloads')).toHaveClass(/active/)
  await expect.poll(zoom).toBeLessThan(150)
  await page.getByTestId('map-fit').click()
  await expect(page.getByTestId('map-island-demo')).toBeVisible()
})
