// M5 observed traffic (RF02) against the isolated kind cluster ONLY. Fixtures live in m5-traffic and
// demo/m5-traffic-backend; the engine reads a private Prometheus saved in a temp LEME_CONFIG_DIR, so the
// shared monitoring/prometheus-server is never touched.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const ENGINE = path.join(__dirname, '..', '..', 'engine', 'bin', 'leme')
const STATIC = path.join(__dirname, '..', 'renderer-dist')
const FIXTURE = path.join(__dirname, 'fixtures', 'traffic', 'traffic.yaml')
const TOKEN = 'e2e-trf-' + Math.random().toString(36).slice(2)
const TSDB = { mode: 'service', namespace: 'm5-traffic', service: 'm5-traffic-tsdb', port: '9090' }
const EMPTY = { mode: 'service', namespace: 'm5-traffic', service: 'm5-traffic-empty-tsdb', port: '9090' }

let engine: ChildProcess
let base = ''
let tmp = ''
let id = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: r.status, json: await r.json().catch(() => undefined) }
}

test.beforeAll(async () => {
  test.setTimeout(240_000)
  kubectl('apply', '-f', FIXTURE)
  for (const [ns, d] of [['demo', 'm5-traffic-backend'], ['m5-traffic', 'm5-traffic-exporter'], ['m5-traffic', 'm5-traffic-tsdb'], ['m5-traffic', 'm5-traffic-empty-tsdb']]) {
    kubectl('-n', ns, 'rollout', 'status', `deploy/${d}`, '--timeout=180s')
  }
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-trf-'))
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: path.join(tmp, 'config') },
  })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  const { json } = await apiCall('GET', '/api/clusters')
  id = json.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  expect((await apiCall('POST', `/api/clusters/${id}/connect`)).status).toBe(200)
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true })
  // The fixture stays for re-runs; remove it with: kubectl delete -f e2e/fixtures/traffic/traffic.yaml
})

async function useProm(cfg: object) {
  const r = await apiCall('PUT', `/api/clusters/${id}/prometheus`, cfg)
  expect(r.status).toBe(200)
}

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId('hotbar-catalog').click()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.getByTestId('tool-traffic')).toBeVisible()
}

async function openDemoTopology(page: Page) {
  await page.getByTestId('tool-topology').click()
  const ns = page.locator('.ns-select:visible').first()
  await expect(ns.locator('option[value="demo"]')).toHaveCount(1)
  await ns.selectOption('demo')
  const topo = page.getByTestId('topology').filter({ visible: true })
  await expect(topo.getByTestId('topo-Deployment/demo/web')).toBeVisible({ timeout: 30_000 })
  return topo
}

test('CA06: sem métricas de tráfego, painel e toggle explicam e nada quebra', async ({ page }) => {
  await useProm(EMPTY)
  const snap = await apiCall('GET', `/api/clusters/${id}/traffic?namespace=demo&window=5m`)
  expect(snap.status).toBe(200)
  expect(snap.json.available).toBe(false)
  expect(snap.json.edges).toEqual([])
  expect(snap.json.hint).toContain('Caretta')
  expect((await apiCall('GET', `/api/clusters/${id}/traffic?window=2d`)).status).toBe(400)
  expect((await apiCall('GET', `/api/clusters/${id}/traffic?namespace=${encodeURIComponent('x"} or vector(1)')}`)).status).toBe(400)

  await openApp(page)
  await page.getByTestId('tool-traffic').click()
  const panel = page.getByTestId('traffic-panel')
  await expect(panel.getByTestId('traffic-empty')).toBeVisible()
  await expect(panel.getByTestId('traffic-hint')).toContainText('Caretta')
  await expect(panel.getByTestId('traffic-hint')).toContainText('labelsContext')
  await expect(panel.getByTestId('traffic-source')).toContainText('indisponível')

  const topo = await openDemoTopology(page)
  await topo.page().getByTestId('topology-traffic').filter({ visible: true }).click()
  await expect(page.getByTestId('topology-traffic-status').filter({ visible: true })).toContainText('Sem dados de tráfego')
  await expect(page.locator('[data-testid^="traffic-edge-"]')).toHaveCount(0)
  // The graph is intact.
  await expect(topo.getByTestId('topo-Deployment/demo/broken')).toBeVisible()
  await expect(topo.getByTestId('topo-Deployment/demo/m5-traffic-backend')).toBeVisible()
})

test('CA02: Topologia do demo mostra a aresta web → m5-traffic-backend com volume', async ({ page }) => {
  test.setTimeout(240_000)
  await useProm(TSDB)
  // The private Prometheus needs a couple of scrapes before increase() has two samples.
  await expect.poll(async () => {
    const { json } = await apiCall('GET', `/api/clusters/${id}/traffic?namespace=demo&window=5m`)
    const names = (json?.edges ?? []).map((e: { from: { name: string }; to: { name: string } }) => `${e.from.name}>${e.to.name}`)
    return json?.available && names.includes('web>m5-traffic-backend') && names.includes('web>api.example.net')
  }, { timeout: 150_000, intervals: [3000] }).toBe(true)
  const { json } = await apiCall('GET', `/api/clusters/${id}/traffic?namespace=demo`)
  expect(json.source).toBe('caretta')
  const e = json.edges.find((x: { to: { name: string } }) => x.to.name === 'm5-traffic-backend')
  expect(e).toMatchObject({ from: { kind: 'Deployment', group: 'apps', namespace: 'demo', name: 'web' }, to: { kind: 'Deployment', namespace: 'demo' }, port: 80, unit: 'bytes/s', source: 'caretta' })
  expect(e.rate).toBeGreaterThan(0)
  // Namespace filter keeps edges with either end in the namespace.
  const other = await apiCall('GET', `/api/clusters/${id}/traffic?namespace=kube-system`)
  expect(other.json.edges).toEqual([])

  await openApp(page)
  // Panel: detected source, table row, link to the object.
  await page.getByTestId('tool-traffic').click()
  const panel = page.getByTestId('traffic-panel')
  await expect(panel.getByTestId('traffic-source')).toContainText('Caretta')
  const row = panel.getByTestId('traffic-row-web-m5-traffic-backend')
  await expect(row).toBeVisible()
  await expect(row).toContainText('80/TCP')
  await expect(row).toContainText(/B\/s/)
  await expect(panel.getByTestId('traffic-row-web-api.example.net')).toContainText('externo')
  await panel.getByTestId('traffic-sort-to').click()
  await expect(row).toBeVisible()

  // Topology overlay.
  const topo = await openDemoTopology(page)
  await page.getByTestId('topology-traffic').filter({ visible: true }).click()
  const edge = page.getByTestId('traffic-edge-web-m5-traffic-backend')
  await expect(edge).toBeVisible({ timeout: 30_000 })
  await expect(edge).toHaveText(/\d+(\.\d)? (B|KiB|MiB)\/s/)
  await expect(page.locator('.react-flow__edge.trf-edge')).toHaveCount(2)
  // An endpoint outside the graph becomes an "externo" node.
  await expect(page.getByTestId('traffic-ext-api.example.net')).toContainText('externo')
  await expect(page.getByTestId('traffic-edge-web-api.example.net')).toBeVisible()
  await expect(page.getByTestId('topology-traffic-status').filter({ visible: true })).toContainText('Caretta')
  await expect(topo.getByTestId('topo-Deployment/demo/web')).toBeVisible()
  // Toggling off removes the overlay.
  await page.getByTestId('topology-traffic').filter({ visible: true }).click()
  await expect(edge).toHaveCount(0)

  // "Ver na Topologia" from the panel turns the overlay on in the open topology.
  await page.getByTestId('tool-traffic').click()
  await row.getByRole('button', { name: 'Topologia', exact: true }).click()
  await expect(page.getByTestId('traffic-edge-web-m5-traffic-backend')).toBeVisible({ timeout: 30_000 })

  // Clicking an endpoint opens the object.
  await page.getByTestId('tool-traffic').click()
  await row.getByRole('button', { name: /m5-traffic-backend/ }).click()
  await expect(page.locator('.dv-tab .ctab', { hasText: 'm5-traffic-backend' }).first()).toBeVisible()
})
