// M5 feature grafana (browser mode, `leme serve`): detection of Grafana/Prometheus, port-forward opened
// on demand and the UI in a new tab, without credentials. Kind cluster ONLY (kind-leme-dev); the
// fixture lives in namespace m5-grafana; monitoring/prometheus-server is only read.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const FIXTURE = path.join(__dirname, 'fixtures', 'grafana', 'm5-grafana.yaml')
const MARKER = 'm5-grafana-marker-Qx7Zk2pW'
const MARKER_B64 = Buffer.from(MARKER).toString('base64')
const TOKEN = 'e2e-gfn-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', timeout: 240_000 })
}

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  const text = await r.text()
  return { status: r.status, text, json: text ? JSON.parse(text) : undefined }
}

async function kindId() {
  const { json } = await apiCall('GET', '/api/clusters')
  return json.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id as string
}

test.beforeAll(async () => {
  kubectl('apply', '-f', FIXTURE)
  kubectl('-n', 'm5-grafana', 'rollout', 'status', 'deploy/m5-grafana', '--timeout=180s')
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN },
  })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
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

async function openPanel(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-grafana').click()
  await expect(page.getByTestId('grafana-panel')).toBeVisible()
}

function expectNoMarker(text: string) {
  expect(text).not.toContain(MARKER)
  expect(text).not.toContain(MARKER_B64)
}

test('CA04 (navegador): detecta Grafana e Prometheus sem valores de Secret', async () => {
  const id = await kindId()
  expect((await apiCall('POST', `/api/clusters/${id}/connect`)).status).toBe(200)
  const r = await apiCall('GET', `/api/clusters/${id}/grafana/targets`)
  expect(r.status).toBe(200)
  expectNoMarker(r.text)
  const g = r.json.targets.find((t: { service: string }) => t.service === 'm5-grafana')
  expect(g).toMatchObject({ kind: 'grafana', namespace: 'm5-grafana', port: 3000, scheme: 'http', adminSecret: { namespace: 'm5-grafana', name: 'm5-grafana-admin' } })
  expect(Object.keys(g.adminSecret).sort()).toEqual(['name', 'namespace'])
  const p = r.json.targets.find((t: { service: string }) => t.service === 'prometheus-server')
  expect(p).toMatchObject({ kind: 'prometheus', namespace: 'monitoring', port: 80 })
  // Only detected targets can be forwarded: an arbitrary service (or port) is refused.
  const before = (await apiCall('GET', '/api/portforwards')).json.length
  expect((await apiCall('POST', `/api/clusters/${id}/grafana/open`, { namespace: 'demo', service: 'web', port: 80 })).status).toBe(404)
  expect((await apiCall('POST', `/api/clusters/${id}/grafana/open`, { namespace: 'm5-grafana', service: 'm5-grafana', port: 22 })).status).toBe(404)
  expect((await apiCall('GET', '/api/portforwards')).json.length).toBe(before)
})

test('CA04 (navegador): abre o port-forward, a interface em nova aba, reaproveita e para', async ({ page, context }) => {
  await openPanel(page)
  const card = page.getByTestId('gfn-target-m5-grafana-m5-grafana')
  await expect(card).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('gfn-target-monitoring-prometheus-server')).toBeVisible()
  await expect(card.getByTestId('gfn-open-m5-grafana-m5-grafana')).toHaveText(/Abrir em nova aba/)

  const [tab] = await Promise.all([context.waitForEvent('page'), page.getByTestId('gfn-open-m5-grafana-m5-grafana').click()])
  await tab.waitForLoadState()
  expect(tab.url()).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/$/)
  await expect(tab.locator('#m5-title')).toHaveText('m5 grafana')
  // noopener: the tab cannot reach back into the app.
  expect(await tab.evaluate(() => window.opener)).toBeNull()
  const port = new URL(tab.url()).port

  await expect(page.getByTestId('gfn-forward-m5-grafana-m5-grafana')).toContainText(`127.0.0.1:${port}`)
  const forwards = (await apiCall('GET', '/api/portforwards')).json as { id: string; localPort: number; namespace: string; name: string }[]
  const fw = forwards.find((f) => String(f.localPort) === port)
  expect(fw).toMatchObject({ namespace: 'm5-grafana', name: 'm5-grafana' })

  // Opening again reuses the active forward.
  const id = await kindId()
  const again = await apiCall('POST', `/api/clusters/${id}/grafana/open`, { namespace: 'm5-grafana', service: 'm5-grafana', port: 3000 })
  expect(again.json).toMatchObject({ forwardId: fw!.id, reused: true, url: `http://127.0.0.1:${port}/` })

  expectNoMarker(await page.content())
  expectNoMarker(await tab.content())
  await tab.close()

  await page.getByTestId('gfn-stop-m5-grafana-m5-grafana').click()
  await expect(page.getByTestId('gfn-forward-m5-grafana-m5-grafana')).toHaveCount(0)
  expect(((await apiCall('GET', '/api/portforwards')).json as { id: string }[]).some((f) => f.id === fw!.id)).toBe(false)
})

test('CA04 (navegador): "Credencial de admin" abre o Secret mascarado', async ({ page }) => {
  await openPanel(page)
  await page.getByTestId('gfn-secret-m5-grafana-m5-grafana').click()
  await expect(page.locator('.dv-tab .ctab', { hasText: 'm5-grafana-admin' }).first()).toBeVisible()
  // The object is loaded (its keys are listed) but the values stay masked until an explicit "Revelar".
  await page.getByTestId('tab-yaml').first().click()
  await expect(page.locator('.monaco-editor').filter({ visible: true }).first()).toContainText('admin-password', { timeout: 30_000 })
  expectNoMarker(await page.content())
})

test('CA06 (navegador): sem serviços detectados, o painel explica como instalar/expor', async ({ page }) => {
  await page.route('**/grafana/targets', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '{"targets":[]}' }))
  await openPanel(page)
  const empty = page.getByTestId('gfn-empty')
  await expect(empty).toBeVisible()
  await expect(empty).toContainText('app.kubernetes.io/name=grafana')
  await expect(empty).toContainText('kube-prometheus-stack')
})
