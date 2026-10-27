// M5 map-webgl (RF01): WebGL stage above the threshold, SVG below it and as fallback, traffic edges in both.
// The map payload is synthetic (page.route); the engine still runs against the kind cluster ONLY, because the
// object panel and the topology need a live session.
import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'
import { NEEDLE, OTHER, PEER, syntheticMap, syntheticTraffic } from './fixtures/map-webgl/synthetic'

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

async function serveMap(page: Page, n: number) {
  const { map, problems } = syntheticMap(n)
  const body = JSON.stringify(map)
  await page.route('**/api/clusters/*/map*', (r) => r.fulfill({ status: 200, contentType: 'application/json', body }))
  return problems
}

async function openMap(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-map').click()
  await expect(page.getByTestId('map-stage')).toBeVisible()
  await expect(page.getByTestId('map-renderer')).toHaveAttribute('data-ready', '1', { timeout: 30_000 })
}

const renderer = (page: Page) => page.getByTestId('map-renderer')
const zoomPct = async (page: Page) => parseInt((await page.getByTestId('map-zoom').innerText()).replace('%', ''), 10)

test('RF01: 20k workloads abrem em WebGL; busca, dica, clique abre o objeto e duplo clique a topologia', async ({ page }) => {
  await serveMap(page, 20_000)
  await openMap(page)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'webgl')
  await expect(renderer(page)).toHaveAttribute('data-workloads', '20000')
  await expect(page.getByTestId('map-gl').locator('canvas')).toBeVisible()
  await expect(page.getByTestId('map-summary')).toContainText('20000 workloads')
  await expect(page.getByTestId('map-renderer-label')).toHaveText('WebGL')
  await expect.poll(async () => Number(await renderer(page).getAttribute('data-fps'))).toBeGreaterThan(0)

  // Search: one result, the view zooms to it (150%) and centres it.
  await page.getByTestId('map-search').fill(NEEDLE.name)
  await expect(page.getByTestId('map-matches')).toContainText('1 resultado')
  await expect.poll(() => zoomPct(page)).toBe(150)
  const box = (await page.getByTestId('map-stage').boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await expect(page.getByTestId('map-tip')).toContainText(NEEDLE.name)
  await expect(page.getByTestId('map-tip')).toContainText(NEEDLE.kind)

  // Click: object preview; double click: topology of its namespace.
  await page.getByTestId('map-stage').click()
  await expect(page.locator('.dv-tab .ctab', { hasText: NEEDLE.name }).first()).toBeVisible()
  // Double click on an island (its header band, no tiles there) opens the topology of the namespace.
  await page.locator('.dv-tab', { hasText: 'Mapa do cluster' }).first().click()
  await page.getByTestId('map-search').fill(OTHER.ns)
  await expect(page.getByTestId('map-matches')).toContainText('1 resultado')
  await expect.poll(() => zoomPct(page)).not.toBe(150)
  let prev = -1
  await expect.poll(async () => { const z = await zoomPct(page); const same = z === prev; prev = z; return same }, { intervals: [300] }).toBe(true)
  const sb = (await page.getByTestId('map-stage').boundingBox())!
  const hx = sb.x + sb.width / 2, hy = sb.y + sb.height / 2 - 0.29 * Math.min(sb.width, sb.height)
  await page.mouse.move(hx, hy)
  await expect(page.getByTestId('map-tip')).toContainText(OTHER.ns)
  await page.mouse.dblclick(hx, hy)
  await expect(page.locator('.dv-tab .ctab', { hasText: 'Topologia' }).first()).toBeVisible()
})

test('RF01: "Só com problemas" e níveis de zoom no WebGL', async ({ page }) => {
  const problems = await serveMap(page, 20_000)
  await openMap(page)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'webgl')
  await page.getByTestId('map-problems').click()
  await expect(page.getByTestId('map-summary')).toContainText(`${problems} workloads`)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'webgl')
  await page.getByTestId('map-problems').click()
  await expect(page.getByTestId('map-summary')).toContainText('20000 workloads')
  await page.getByTestId('map-level-pods').click()
  await expect.poll(() => zoomPct(page)).toBeGreaterThan(200)
  await page.getByTestId('map-fit').click()
  await expect.poll(() => zoomPct(page)).toBeLessThan(50)
})

test('RF01: abaixo do limiar fica em SVG; o limiar nas configurações troca o renderizador', async ({ page }) => {
  await serveMap(page, 300)
  await openMap(page)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'svg')
  await expect(page.getByTestId('map-gl')).toHaveCount(0)
  await page.getByTestId('map-search').fill(NEEDLE.name)
  await expect(page.getByTestId(`map-tile-${NEEDLE.name}`)).toHaveClass(/match/)
  await page.getByTestId('map-search').fill('')

  await page.getByTestId('map-settings').click()
  const input = page.getByTestId('map-webgl-threshold')
  await expect(input).toHaveValue('1500')
  await input.fill('200')
  await input.press('Enter')
  await expect(renderer(page)).toHaveAttribute('data-kind', 'webgl')
  await expect(renderer(page)).toHaveAttribute('data-ready', '1')
  // Persisted in the UI prefs.
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('leme.ui.v1') ?? '{}').mapWebglThreshold)).toBe(200)
  await input.fill('1500')
  await input.press('Enter')
  await expect(renderer(page)).toHaveAttribute('data-kind', 'svg')
})

test('CA06: sem WebGL o mapa grande cai no SVG com aviso, e busca e clique continuam', async ({ page }) => {
  await page.addInitScript(() => {
    const orig = HTMLCanvasElement.prototype.getContext
    HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, id: string, ...rest: unknown[]) {
      if (id === 'webgl' || id === 'webgl2' || id === 'experimental-webgl') return null
      return (orig as (...a: unknown[]) => unknown).call(this, id, ...rest)
    } as typeof orig
  })
  await serveMap(page, 3000)
  await openMap(page)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'svg')
  await expect(page.getByTestId('map-webgl-fallback')).toContainText('WebGL indisponível')
  await page.getByTestId('map-search').fill(NEEDLE.name)
  await expect(page.getByTestId('map-matches')).toContainText('1 resultado')
  await expect(page.getByTestId(`map-tile-${NEEDLE.name}`)).toHaveClass(/match/)
  await expect.poll(() => zoomPct(page)).toBe(150)
  await page.getByTestId(`map-tile-${NEEDLE.name}`).click()
  await expect(page.locator('.dv-tab .ctab', { hasText: NEEDLE.name }).first()).toBeVisible()
})

test('RF01/RF02: toggle Tráfego desenha arestas nos dois renderizadores e explica quando não há fonte', async ({ page }) => {
  let snapshot: unknown = syntheticTraffic()
  await page.route('**/api/clusters/*/traffic*', (r) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(snapshot) }))
  await serveMap(page, 300)
  await openMap(page)
  await expect(renderer(page)).toHaveAttribute('data-kind', 'svg')
  await expect(renderer(page)).toHaveAttribute('data-edges', '0')
  await page.getByTestId('map-traffic').click()
  await expect(page.getByTestId('map-traffic')).toHaveClass(/on/)
  // 3 edges between tiles; the External one is counted as outside the map.
  await expect(renderer(page)).toHaveAttribute('data-edges', '3')
  await expect(page.getByTestId('map-edge')).toHaveCount(3)
  await expect(page.getByTestId('map-traffic-note')).toContainText('3 conexão(ões)')
  await expect(page.getByTestId('map-traffic-note')).toContainText('Caretta')
  await expect(page.getByTestId('map-traffic-note')).toContainText('1 fora do mapa')
  await expect(page.getByTestId('map-edge').first().locator('title')).toContainText('B/s')
  // Heavier edge is drawn thicker.
  const widths = await page.locator('[data-testid="map-edge"] .mgl-edge').evaluateAll((els) => els.map((e) => Number(e.getAttribute('stroke-width'))))
  expect(Math.max(...widths)).toBeGreaterThan(Math.min(...widths) * 2)
  await expect(page.getByTestId('map-edge').filter({ hasText: `${NEEDLE.name} → ${PEER.name}` })).toHaveCount(1)

  // Same edges in WebGL.
  await page.getByTestId('map-settings').click()
  await page.getByTestId('map-webgl-threshold').fill('100')
  await page.getByTestId('map-webgl-threshold').press('Enter')
  await page.keyboard.press('Escape')
  await expect(renderer(page)).toHaveAttribute('data-kind', 'webgl')
  await expect(renderer(page)).toHaveAttribute('data-edges', '3')

  // No source: the note explains, nothing breaks.
  snapshot = { available: false, edges: [], hint: 'Nenhuma métrica do Caretta ou do Hubble no Prometheus.' }
  await page.getByTestId('map-traffic').click()
  await page.getByTestId('map-traffic').click()
  await expect(page.getByTestId('map-traffic-note')).toContainText('indisponível')
  await expect(page.getByTestId('map-traffic-note')).toContainText('Nenhuma métrica')
  await expect(renderer(page)).toHaveAttribute('data-edges', '0')
})
