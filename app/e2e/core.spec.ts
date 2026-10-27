// End-to-end tests against the isolated kind cluster ONLY.
// KUBECONFIG is forced to ~/.kube/leme-dev/kubeconfig; the user's default kubeconfig is never read.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const ENGINE = path.join(__dirname, '..', '..', 'engine', 'bin', 'leme')
const STATIC = path.join(__dirname, '..', 'renderer-dist')
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)

let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  // Always pin both kubeconfig and context so nothing else can be targeted.
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: r.status, json: await r.json().catch(() => undefined) }
}

test.beforeAll(async () => {
  const env = { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN }
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC, '--kubeconfig', path.join(__dirname, 'fake-prod.kubeconfig')], { env })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
  base = await new Promise<string>((resolve, reject) => {
    const rl = createInterface({ input: engine.stdout! })
    rl.on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  kubectl('-n', 'demo', 'scale', 'deploy/web', '--replicas=3')
  kubectl('-n', 'demo', 'delete', 'pod', 'e2e-probe', '--ignore-not-found', '--wait=true')
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  try {
    kubectl('-n', 'demo', 'scale', 'deploy/web', '--replicas=3')
    kubectl('-n', 'demo', 'delete', 'pod', 'e2e-probe', '--ignore-not-found', '--wait=false')
  } catch {
    /* best effort */
  }
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await expect(page.locator('.cluster-name', { hasText: KIND_CONTEXT }).first()).toBeVisible()
}

async function connectKind(page: Page) {
  await page.getByTestId('hotbar-catalog').click()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.locator('.cat-title', { hasText: 'Workloads' })).toBeVisible()
}

async function openKind(page: Page, kind: string) {
  await page.getByTestId(`nav-kind-${kind}`).click()
  // The scale lab (kwok) may add thousands of pods elsewhere; tests work in the demo namespace like a user would.
  const ns = page.locator('.ns-select:visible').first()
  if (await ns.count()) {
    await expect(ns.locator('option[value="demo"]')).toHaveCount(1)
    await ns.selectOption('demo')
  }
}

test('CA01/CA02: lista contextos sem conectar e marca produção como somente leitura', async ({ page }) => {
  await openApp(page)
  const prod = page.locator('.cluster-row', { hasText: 'fake-app-prd' }).first()
  await expect(prod.locator('.badge-prod')).toBeVisible()
  const { json } = await apiCall('GET', '/api/clusters')
  expect(json.clusters.every((c: { connected: boolean }) => !c.connected)).toBe(true)
  const fake = json.clusters.find((c: { name: string }) => c.name.startsWith('fake-app-prd'))
  expect(fake.prod).toBe(true)
  expect(fake.readOnly).toBe(true)
})

test('segurança: API exige token e rejeita origem estranha', async () => {
  expect((await fetch(base + '/api/clusters')).status).toBe(401)
  expect((await fetch(base + '/api/clusters', { headers: { Authorization: `Bearer ${TOKEN}`, Origin: 'http://evil.example' } })).status).toBe(403)
})

test('CA03: grid de pods atualiza ao vivo', async ({ page }) => {
  await openApp(page)
  await connectKind(page)
  await openKind(page, 'Pod')
  await expect(page.getByTestId('row-logger')).toBeVisible()
  kubectl('-n', 'demo', 'run', 'e2e-probe', '--image=busybox:1.36', '--restart=Never', '--', 'sleep', '600')
  await expect(page.getByTestId('row-e2e-probe')).toBeVisible()
  kubectl('-n', 'demo', 'delete', 'pod', 'e2e-probe', '--wait=false', '--grace-period=0', '--force')
  await expect(page.getByTestId('row-e2e-probe')).toHaveCount(0, { timeout: 30_000 })
})

test('CA04: editar ConfigMap com diff, dry-run e apply', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base })
  await openApp(page)
  await connectKind(page)
  await openKind(page, 'ConfigMap')
  await page.getByTestId('row-app-config').click()
  await page.locator('.tab', { hasText: 'YAML' }).click()
  await page.getByTestId('drawer').getByRole('button', { name: 'Editar YAML', exact: true }).click()
  const current = JSON.parse(kubectl('-n', 'demo', 'get', 'cm', 'app-config', '-o', 'json'))
  const stamp = `e2e-${Date.now()}`
  const yaml = [
    'apiVersion: v1',
    'kind: ConfigMap',
    'metadata:',
    '  name: app-config',
    '  namespace: demo',
    `  resourceVersion: "${current.metadata.resourceVersion}"`,
    'data:',
    '  LOG_LEVEL: debug',
    `  E2E_STAMP: ${stamp}`,
    '',
  ].join('\n')
  await page.getByTestId('yaml-editor').locator('.view-lines').click()
  await page.keyboard.press('Control+A')
  await page.keyboard.press('Delete')
  await page.evaluate((y) => navigator.clipboard.writeText(y), yaml)
  await page.keyboard.press('Control+V')
  await page.getByRole('button', { name: /Revisar altera/ }).click()
  await expect(page.getByTestId('yaml-diff').or(page.locator('.toast-error'))).toBeVisible()
  await expect(page.locator('.toast-error')).toHaveCount(0)
  await expect(page.getByTestId('yaml-diff')).toBeVisible()
  await page.getByRole('button', { name: 'Aplicar no cluster' }).click()
  await page.locator('.modal').getByRole('button', { name: 'Aplicar' }).click()
  await expect(page.locator('.toast-ok', { hasText: 'atualizado' })).toBeVisible()
  expect(kubectl('-n', 'demo', 'get', 'cm', 'app-config', '-o', 'jsonpath={.data.E2E_STAMP}')).toBe(stamp)
})

test('CA05: logs em streaming', async ({ page }) => {
  await openApp(page)
  await connectKind(page)
  await openKind(page, 'Pod')
  await page.getByTestId('row-logger').click()
  await page.getByRole('button', { name: 'Logs', exact: true }).click()
  await expect(page.locator('.xterm-rows').last()).toContainText('line', { timeout: 20_000 })
})

test('CA06: terminal exec executa ls /', async ({ page }) => {
  await openApp(page)
  await connectKind(page)
  await openKind(page, 'Pod')
  await page.locator('[data-testid^="row-web-"]').first().click()
  await page.getByRole('button', { name: 'Shell' }).click()
  await expect(page.locator('.term-panel .muted', { hasText: 'conectado' })).toBeVisible()
  await expect(page.locator('.xterm-rows').last()).toContainText('#')
  await page.locator('.term-panel .xterm-screen').last().click()
  await page.keyboard.type('ls /')
  await page.keyboard.press('Enter')
  await expect(page.locator('.xterm-rows').last()).toContainText('etc', { timeout: 15_000 })
})

test('CA07: port-forward para svc/web responde 200', async () => {
  const { json: clusters } = await apiCall('GET', '/api/clusters')
  const id = clusters.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  await apiCall('POST', `/api/clusters/${id}/connect`)
  const { status, json } = await apiCall('POST', `/api/clusters/${id}/portforwards`, { namespace: 'demo', kind: 'service', name: 'web', remotePort: 80, localPort: 0 })
  expect(status).toBe(200)
  const r = await fetch(json.url)
  expect(r.status).toBe(200)
  await apiCall('DELETE', `/api/portforwards/${json.id}`)
})

test('CA08: scale de deploy/web reflete no grid', async ({ page }) => {
  await openApp(page)
  await connectKind(page)
  await openKind(page, 'Deployment')
  await page.getByTestId('row-web').click()
  await page.getByRole('button', { name: 'Escalar' }).click()
  const input = page.locator('.modal input')
  await input.fill('4')
  await page.locator('.modal').getByRole('button', { name: 'Aplicar' }).click()
  await expect(page.locator('.toast-ok', { hasText: 'escalado para 4' })).toBeVisible()
  await page.locator('.dv-tab', { hasText: 'Deployments' }).click()
  await expect(page.getByTestId('row-web')).toContainText('4/4', { timeout: 60_000 })
})

test('guarda: modo somente leitura bloqueia escrita na UI e na API', async ({ page }) => {
  await openApp(page)
  await connectKind(page)
  await page.getByTestId('rw-toggle').click()
  await expect(page.getByTestId('rw-toggle').locator('.badge-ro')).toBeVisible()
  await expect(page.getByTestId('sb-rw')).toContainText('somente leitura')
  const { json: clusters } = await apiCall('GET', '/api/clusters')
  const id = clusters.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  const del = await apiCall('POST', `/api/clusters/${id}/delete`, { group: '', version: 'v1', resource: 'configmaps', namespace: 'demo', name: 'app-config' })
  expect(del.status).toBe(403)
  const pf = await apiCall('POST', `/api/clusters/${id}/portforwards`, { namespace: 'demo', kind: 'service', name: 'web', remotePort: 80, localPort: 0 })
  expect(pf.status).toBe(403)
  await openKind(page, 'ConfigMap')
  await page.getByTestId('row-app-config').click()
  await expect(page.getByRole('button', { name: 'Excluir' })).toBeDisabled()
  await page.getByTestId('rw-toggle').click()
  await expect(page.getByTestId('rw-toggle').locator('.badge-ro')).toHaveCount(0)
  await expect(page.getByTestId('sb-rw')).toContainText('leitura e escrita')
  expect(kubectl('-n', 'demo', 'get', 'cm', 'app-config', '-o', 'name').trim()).toBe('configmap/app-config')
})

test('apply exige resourceVersion e rejeita múltiplos documentos', async () => {
  const { json: clusters } = await apiCall('GET', '/api/clusters')
  const id = clusters.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  await apiCall('POST', `/api/clusters/${id}/connect`)
  const blind = await apiCall('POST', `/api/clusters/${id}/apply`, { yaml: 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: app-config, namespace: demo}\ndata: {X: "1"}\n', dryRun: true })
  expect(blind.status).toBe(409)
  const multi = await apiCall('POST', `/api/clusters/${id}/apply`, { yaml: 'apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: demo}\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: b, namespace: demo}\n', dryRun: true })
  expect(multi.status).toBe(400)
})

test('travar o cluster encerra port-forwards ativos', async () => {
  const { json: clusters } = await apiCall('GET', '/api/clusters')
  const id = clusters.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  await apiCall('POST', `/api/clusters/${id}/connect`)
  const pf = await apiCall('POST', `/api/clusters/${id}/portforwards`, { namespace: 'demo', kind: 'service', name: 'web', remotePort: 80, localPort: 0 })
  expect(pf.status).toBe(200)
  await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: true })
  await expect.poll(async () => (await apiCall('GET', '/api/portforwards')).json.length).toBe(0)
  await expect.poll(() => fetch(pf.json.url).then(() => 'open', () => 'closed')).toBe('closed')
  await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: false })
})
