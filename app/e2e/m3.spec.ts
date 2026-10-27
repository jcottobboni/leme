// M3 end-to-end tests against the isolated kind cluster ONLY: security posture, RBAC risks, external
// scanners (Polaris, Kubescape, Trivy on local files/images), NetworkPolicy and OpenShift look-alikes.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const ROOT = path.join(__dirname, '..', '..')
const ENGINE = path.join(ROOT, 'engine', 'bin', 'leme')
const STATIC = path.join(__dirname, '..', 'renderer-dist')
const TOKEN = 'e2e-m3-' + Math.random().toString(36).slice(2)

// The external scanners are optional: their tests run only where they are installed (same lookup as the engine).
function hasTool(name: string) {
  const dirs = [...(process.env.PATH ?? '').split(path.delimiter), path.join(os.homedir(), '.local', 'bin'), '/usr/local/bin']
  return dirs.some((d) => d && fs.existsSync(path.join(d, name)))
}
// Trivy's vulnerability DB is large: keep it between runs (outside the repo, never in the user's config).
const TRIVY_DIR = path.join(os.homedir(), '.cache', 'leme-e2e')

let engine: ChildProcess
let base = ''
let tmp = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: r.status, json: await r.json().catch(() => undefined) }
}

async function kindId() {
  const { json } = await apiCall('GET', '/api/clusters')
  return json.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id as string
}

test.beforeAll(async () => {
  execFileSync('bash', [path.join(ROOT, 'scripts', 'm3-fixtures.sh'), 'apply'], { stdio: 'inherit', timeout: 300_000 })
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-m3-'))
  fs.mkdirSync(TRIVY_DIR, { recursive: true })
  // The engine keeps Trivy's cache under <config>/trivy: point it at the persistent e2e cache.
  fs.mkdirSync(path.join(tmp, 'config'), { recursive: true })
  fs.symlinkSync(TRIVY_DIR, path.join(tmp, 'config', 'trivy'))
  const env = { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: path.join(tmp, 'config'), HELM_CONFIG_HOME: path.join(tmp, 'helm') }
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC], { env })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
  base = await new Promise<string>((resolve, reject) => {
    const rl = createInterface({ input: engine.stdout! })
    rl.on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  try {
    execFileSync('bash', [path.join(ROOT, 'scripts', 'm3-fixtures.sh'), 'delete'], { timeout: 120_000 })
  } catch {
    /* best effort */
  }
  fs.rmSync(tmp, { recursive: true, force: true })
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await expect(page.getByTestId('tool-security')).toBeVisible()
}

async function selectDemo(page: Page) {
  const ns = page.locator('.ns-select:visible').first()
  await expect(ns.locator('option[value="demo"]')).toHaveCount(1)
  await ns.selectOption('demo')
}

async function openObjectFromGrid(page: Page, kind: string, row: string) {
  await page.getByTestId(`nav-kind-${kind}`).click()
  await selectDemo(page)
  await page.getByTestId(`row-${row}`).click()
}

test('CA01/CA02: painel de segurança e achados do workload inseguro', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('tool-security').click()
  await selectDemo(page)
  await expect(page.getByTestId('score')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId('risky-workloads').locator('tbody tr').first()).toContainText('insecure')
  await expect(page.locator('.card', { hasText: 'Namespaces sem NetworkPolicy' })).toContainText('demo')
  await openObjectFromGrid(page, 'Deployment', 'insecure')
  await page.getByTestId('tab-security').click()
  const f = page.getByTestId('findings').filter({ visible: true })
  await expect(f).toContainText('Container privilegiado')
  await expect(f).toContainText('Monta caminho do host (/var/lib/kubelet)')
  await expect(f.locator('.sev-critical').first()).toBeVisible()
})

test('CA03: riscos de RBAC listam o cluster-admin de uma ServiceAccount', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('tool-security').click()
  await page.locator('.tab', { hasText: 'Riscos de RBAC' }).click()
  const t = page.getByTestId('rbac-risks')
  await expect(t).toContainText('e2e-ci-cluster-admin')
  await expect(t.locator('tr', { hasText: 'e2e-ci-cluster-admin' }).locator('.sev-critical')).toBeVisible()
})

test('CA04: Polaris e Kubescape rodam sobre o YAML exportado', async ({ page }) => {
  test.skip(!hasTool('polaris') || !hasTool('kubescape'), 'polaris/kubescape not installed')
  test.setTimeout(240_000)
  await openApp(page)
  await page.getByTestId('tool-security').click()
  await selectDemo(page)
  await page.locator('.tab', { hasText: 'Scanners externos' }).click()
  await page.getByTestId('run-polaris').click()
  await expect(page.locator('h4', { hasText: /^Polaris: \d+ achados/ })).toBeVisible({ timeout: 120_000 })
  await expect(page.getByTestId('findings').filter({ visible: true })).toContainText('polaris')
  await page.getByTestId('run-kubescape').click()
  await expect(page.locator('h4', { hasText: /^Kubescape NSA: \d+ achados/ })).toBeVisible({ timeout: 180_000 })
  await expect(page.getByTestId('findings').filter({ visible: true })).toContainText('kubescape')
})

test('CA05: Trivy lista CVEs da imagem do web', async ({ page }) => {
  test.skip(!hasTool('trivy'), 'trivy not installed')
  test.setTimeout(600_000)
  await openApp(page)
  await openObjectFromGrid(page, 'Deployment', 'web')
  await page.getByTestId('tab-security').click()
  await page.getByTestId('trivy-scan').click()
  const rep = page.getByTestId('image-report')
  await expect(rep).toContainText('nginx:1.27-alpine', { timeout: 540_000 })
  await expect(rep).toContainText('CVE-')
})

test('CA06: simulador de NetworkPolicy', async ({ page }) => {
  const pol = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: e2e-deny-all, namespace: demo}
spec: {podSelector: {}, policyTypes: [Ingress]}
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: e2e-allow-client-web, namespace: demo}
spec:
  podSelector: {matchLabels: {app: web}}
  ingress: [{from: [{podSelector: {matchLabels: {app: client}}}], ports: [{port: 80}]}]
`
  execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, 'apply', '-f', '-'], { input: pol })
  try {
    await openApp(page)
    await page.getByTestId('tool-netpol').click()
    await selectDemo(page)
    await expect(page.getByTestId('netpol-graph')).toBeVisible()
    await page.locator('.tab', { hasText: 'Workloads' }).click()
    await expect(page.getByTestId('netpol-groups').locator('tr', { hasText: 'web' })).toContainText('isolada')
    await page.locator('.tab', { hasText: 'Simulador' }).click()
    const web = kubectl('-n', 'demo', 'get', 'po', '-l', 'app=web', '-o', 'jsonpath={.items[0].metadata.name}')
    const broken = kubectl('-n', 'demo', 'get', 'po', '-l', 'app=broken', '-o', 'jsonpath={.items[0].metadata.name}')
    await page.getByTestId('src-pod').selectOption('client')
    await page.getByTestId('dst-pod').selectOption(web)
    await page.getByTestId('sim-port').fill('80')
    await page.getByTestId('sim-run').click()
    await expect(page.getByTestId('sim-answer')).toContainText('Permitido')
    await page.getByTestId('src-pod').selectOption(broken)
    await page.getByTestId('sim-run').click()
    await expect(page.getByTestId('sim-answer')).toContainText('Bloqueado')
  } finally {
    kubectl('-n', 'demo', 'delete', 'networkpolicy', 'e2e-deny-all', 'e2e-allow-client-web', '--ignore-not-found')
  }
})

test('CA07: grids do OpenShift (Route, DeploymentConfig, Build, ImageStream)', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('nav-kind-Route').click()
  await selectDemo(page)
  await expect(page.getByTestId('row-web').locator('a')).toHaveAttribute('href', 'https://web-demo.apps.kind.local')
  await expect(page.getByTestId('row-web')).toContainText('Admitted')
  await page.getByTestId('nav-kind-DeploymentConfig').click()
  await expect(page.getByTestId('row-frontend')).toContainText('1/1')
  await page.getByTestId('nav-kind-Build').click()
  await expect(page.getByTestId('row-frontend-1')).toContainText('Complete')
  await expect(page.getByTestId('row-frontend-1')).toContainText('2m30s')
  await page.getByTestId('nav-kind-ImageStream').click()
  await expect(page.getByTestId('row-frontend')).toContainText('latest')
})

test('CA08: topologia do DeploymentConfig', async ({ page }) => {
  await openApp(page)
  await openObjectFromGrid(page, 'DeploymentConfig', 'frontend')
  await page.getByTestId('tab-topology').click()
  const topo = page.getByTestId('topology').filter({ visible: true })
  await expect(topo.getByTestId('topo-ReplicationController/demo/frontend-1')).toBeVisible({ timeout: 30_000 })
  await expect(topo.locator('[data-testid^="topo-Pod/demo/frontend-1-"]').first()).toBeVisible()
  await expect(topo.getByTestId('topo-ImageStream/demo/frontend')).toBeVisible()
  await expect(topo.getByTestId('topo-BuildConfig/demo/frontend')).toBeVisible()
})

test('CA09: ações OpenShift bloqueadas em somente leitura; análises continuam', async () => {
  const id = await kindId()
  await apiCall('POST', `/api/clusters/${id}/connect`)
  await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: true })
  try {
    expect((await apiCall('POST', `/api/clusters/${id}/openshift/rollout`, { namespace: 'demo', name: 'frontend' })).status).toBe(403)
    expect((await apiCall('POST', `/api/clusters/${id}/openshift/build`, { namespace: 'demo', name: 'frontend' })).status).toBe(403)
    expect((await apiCall('GET', `/api/clusters/${id}/security/report?namespace=demo`)).status).toBe(200)
    expect((await apiCall('GET', `/api/clusters/${id}/netpol?namespace=demo`)).status).toBe(200)
  } finally {
    await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: false })
  }
  // Login OpenShift is only offered when the cluster exposes OAuth (kind does not).
  const oauth = await apiCall('GET', `/api/clusters/${id}/oauth`)
  expect(oauth.json.available).toBe(false)
  // Scanner inputs are validated.
  expect((await apiCall('POST', `/api/clusters/${id}/security/scan`, { tool: 'rm', namespace: 'demo' })).status).toBe(400)
  expect((await apiCall('POST', `/api/clusters/${id}/security/images/scan`, { images: ['--help'] })).json[0].error).toBeTruthy()
})
