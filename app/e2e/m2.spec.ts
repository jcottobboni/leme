// M2 end-to-end tests against the isolated kind cluster ONLY (Helm, RBAC, Prometheus, overview, topology,
// timeline, workspace, search and rollout). Helm and engine settings live in a temp dir, never in the user's.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import { createServer, type Server } from 'node:http'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const ALIAS_CONTEXT = 'kind-leme-dev-alias'
const ENGINE = path.join(__dirname, '..', '..', 'engine', 'bin', 'leme')
const STATIC = path.join(__dirname, '..', 'renderer-dist')
const CHART = path.join(__dirname, 'fixtures', 'charts', 'hello')
const TOKEN = 'e2e-m2-' + Math.random().toString(36).slice(2)
const RELEASE = 'e2e-hello'

let engine: ChildProcess
let base = ''
let tmp = ''
let repo: Server
let repoURL = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: r.status, json: await r.json().catch(() => undefined) }
}

async function kindId(name = KIND_CONTEXT) {
  const { json } = await apiCall('GET', '/api/clusters')
  return json.clusters.find((c: { name: string }) => c.name === name).id as string
}

test.beforeAll(async () => {
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-'))
  // Local chart repository: helm package + index, served on loopback.
  const repoDir = path.join(tmp, 'repo')
  fs.mkdirSync(repoDir)
  execFileSync('helm', ['package', CHART, '-d', repoDir], { env: { ...process.env, ...helmEnv() } })
  repo = createServer((req, res) => {
    const f = path.join(repoDir, path.basename(req.url ?? ''))
    if (!fs.existsSync(f)) return res.writeHead(404).end()
    res.writeHead(200, { 'Content-Type': f.endsWith('.yaml') ? 'text/yaml' : 'application/gzip' }).end(fs.readFileSync(f))
  })
  await new Promise<void>((r) => repo.listen(0, '127.0.0.1', r))
  repoURL = `http://127.0.0.1:${(repo.address() as { port: number }).port}`
  execFileSync('helm', ['repo', 'index', repoDir, '--url', repoURL], { env: { ...process.env, ...helmEnv() } })

  // Second context pointing at the same kind cluster, to test two clusters side by side.
  const kc = fs.readFileSync(KIND_KUBECONFIG, 'utf8')
  const alias = path.join(tmp, 'alias.kubeconfig')
  fs.writeFileSync(alias, kc.replace(/name: kind-leme-dev\n/g, `name: ${ALIAS_CONTEXT}\n`).replace(/(cluster|user|current-context): kind-leme-dev\n/g, `$1: ${ALIAS_CONTEXT}\n`), { mode: 0o600 })

  const env = { ...process.env, ...helmEnv(), KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: path.join(tmp, 'config') }
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC, '--kubeconfig', alias], { env })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
  base = await new Promise<string>((resolve, reject) => {
    const rl = createInterface({ input: engine.stdout! })
    rl.on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  cleanup()
  kubectl('-n', 'demo', 'create', 'serviceaccount', 'e2e-reader')
  kubectl('-n', 'demo', 'create', 'rolebinding', 'e2e-secret-reader', '--clusterrole=view', '--serviceaccount=demo:e2e-reader')
  kubectl('-n', 'demo', 'create', 'role', 'e2e-secrets', '--verb=get,list', '--resource=secrets')
  kubectl('-n', 'demo', 'create', 'rolebinding', 'e2e-secrets', '--role=e2e-secrets', '--serviceaccount=demo:e2e-reader')
})

function helmEnv() {
  const h = path.join(tmp, 'helm')
  return {
    HELM_CONFIG_HOME: path.join(h, 'config'),
    HELM_CACHE_HOME: path.join(h, 'cache'),
    HELM_DATA_HOME: path.join(h, 'data'),
    HELM_REPOSITORY_CONFIG: path.join(h, 'config', 'repositories.yaml'),
    HELM_REPOSITORY_CACHE: path.join(h, 'cache', 'repository'),
    HELM_REGISTRY_CONFIG: path.join(h, 'config', 'registry.json'),
  }
}

function cleanup() {
  for (const args of [
    ['-n', 'demo', 'delete', 'rolebinding', 'e2e-secret-reader', 'e2e-secrets', '--ignore-not-found'],
    ['-n', 'demo', 'delete', 'role', 'e2e-secrets', '--ignore-not-found'],
    ['-n', 'demo', 'delete', 'serviceaccount', 'e2e-reader', '--ignore-not-found'],
    ['-n', 'demo', 'delete', 'secret', '-l', `owner=helm,name=${RELEASE}`, '--ignore-not-found'],
    ['-n', 'demo', 'delete', 'deploy,svc,cm,secret', `${RELEASE}-hello`, '--ignore-not-found'],
  ]) {
    try {
      kubectl(...args)
    } catch {
      /* best effort */
    }
  }
}

test.afterAll(() => {
  engine?.kill('SIGTERM')
  repo?.close()
  cleanup()
  try {
    kubectl('-n', 'demo', 'set', 'image', 'deploy/web', 'nginx=nginx:1.27-alpine')
  } catch {
    /* best effort */
  }
  fs.rmSync(tmp, { recursive: true, force: true })
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await expect(page.locator('.cluster-name', { hasText: KIND_CONTEXT }).first()).toBeVisible()
}

async function connect(page: Page, name = KIND_CONTEXT) {
  await page.getByTestId('hotbar-catalog').click()
  await page.getByTestId(`cluster-row-${name}`).first().click()
  await expect(page.locator('.navigator .nav-cluster .cluster-name', { hasText: new RegExp(`^${name}$`) })).toBeVisible()
  await expect(page.getByTestId('tool-overview')).toBeVisible()
  return page
}

async function selectDemo(page: Page) {
  const ns = page.locator('.ns-select:visible').first()
  await expect(ns.locator('option[value="demo"]')).toHaveCount(1)
  await ns.selectOption('demo')
}

async function pasteInto(page: Page, editor: ReturnType<Page['getByTestId']>, text: string) {
  await editor.locator('.view-lines').click()
  await page.keyboard.press('Control+A')
  await page.keyboard.press('Delete')
  await page.evaluate((y) => navigator.clipboard.writeText(y), text)
  await page.keyboard.press('Control+V')
}

test('CA03/CA01/CA02: Helm — instalar do repositório, detalhe, upgrade, rollback e uninstall', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base })
  await openApp(page)
  const cl = await connect(page)
  await cl.getByTestId('tool-charts').click()
  // Add the local repository.
  await page.getByPlaceholder('nome').fill('e2e')
  await page.getByPlaceholder('https://charts.exemplo.com').fill(repoURL)
  await page.getByRole('button', { name: 'Adicionar' }).click()
  await expect(page.locator('.toast-ok', { hasText: 'e2e adicionado' })).toBeVisible()
  await page.getByTestId('chart-search').fill('hello')
  await page.getByTestId('chart-e2e/hello').click()
  await expect(page.locator('.readme')).toContainText('Fixture chart')
  await page.locator('.tab', { hasText: 'Instalar' }).click()
  await page.getByRole('textbox', { name: 'Namespace', exact: true }).fill('demo')
  await page.getByRole('textbox', { name: 'Nome do release', exact: true }).fill(RELEASE)
  await page.getByRole('button', { name: 'Revisar (dry-run)' }).click()
  await expect(page.getByTestId('yaml-diff')).toBeVisible()
  await page.getByTestId('review-confirm').click()
  await expect(page.locator('.toast-ok', { hasText: `${RELEASE} instalado` })).toBeVisible()

  // CA01: release detail.
  await expect(page.locator('.obj-title', { hasText: RELEASE })).toBeVisible()
  await expect(page.locator('.overview')).toContainText(`Deployment`)
  // Hidden until revealed: Secret values in the manifest and the notes.
  await page.locator('.tab', { hasText: 'Manifest' }).click()
  await expect(page.getByTestId('yaml-editor')).toContainText('<hidden>')
  await expect(page.getByTestId('yaml-editor')).not.toContainText('s3cr3t-value')
  await page.locator('.tab', { hasText: 'Resumo' }).click()
  await expect(page.getByTestId('helm-notes')).toHaveCount(0)
  await page.locator('.overview').getByRole('button', { name: 'Revelar' }).click()
  await expect(page.getByTestId('helm-notes')).toContainText('installed with 1 replica')
  await page.locator('.tab', { hasText: 'Manifest' }).click()
  await expect(page.getByTestId('yaml-editor')).toContainText('s3cr3t-value')
  await page.locator('.tab', { hasText: 'Histórico' }).click()
  await expect(page.getByTestId('revision-1')).toBeVisible()

  // CA02: upgrade with new values → revision 2.
  await page.locator('.tab', { hasText: 'Values' }).click()
  await page.getByRole('button', { name: 'Editar values' }).click()
  await pasteInto(page, page.getByTestId('yaml-editor'), 'replicaCount: 2\nmessage: upgraded by e2e\n')
  await page.getByRole('button', { name: 'Revisar upgrade (dry-run)' }).click()
  await expect(page.getByTestId('yaml-diff')).toBeVisible()
  await page.getByTestId('review-confirm').click()
  await expect(page.locator('.toast-ok', { hasText: 'revisão 2' })).toBeVisible()
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'deploy', `${RELEASE}-hello`, '-o', 'jsonpath={.spec.replicas}')).toBe('2')

  // Revert (helm rollback) to revision 1 → revision 3.
  await page.locator('.tab', { hasText: 'Histórico' }).click()
  await page.getByTestId('revision-1').getByRole('button', { name: 'Reverter' }).click()
  await page.getByTestId('review-confirm').click()
  await expect(page.locator('.toast-ok', { hasText: 'revisão 3' })).toBeVisible()
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'deploy', `${RELEASE}-hello`, '-o', 'jsonpath={.spec.replicas}')).toBe('1')

  // Releases list shows it; uninstall removes it.
  await cl.getByTestId('tool-helm').click()
  await selectDemo(page)
  await expect(page.getByTestId(`release-${RELEASE}`)).toContainText('deployed')
  await page.getByTestId(`release-${RELEASE}`).click()
  await page.getByRole('button', { name: 'Desinstalar' }).click()
  await page.locator('.modal').getByRole('button', { name: 'Desinstalar' }).click()
  await expect(page.locator('.toast-ok', { hasText: 'desinstalado' })).toBeVisible()
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'deploy', '-o', 'name')).not.toContain(`${RELEASE}-hello`)
})

test('CA04: Helm e rollback bloqueados em somente leitura (engine)', async () => {
  const id = await kindId()
  await apiCall('POST', `/api/clusters/${id}/connect`)
  await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: true })
  try {
    const install = await apiCall('POST', `/api/clusters/${id}/helm/install`, { namespace: 'demo', name: 'blocked', chart: CHART, dryRun: true })
    expect(install.status).toBe(403)
    const up = await apiCall('POST', `/api/clusters/${id}/helm/releases/demo/any/upgrade`, { replaceValues: false, dryRun: true })
    expect(up.status).toBe(403)
    const rb = await apiCall('POST', `/api/clusters/${id}/helm/releases/demo/any/rollback`, { revision: 1, dryRun: true })
    expect(rb.status).toBe(403)
    const un = await apiCall('POST', `/api/clusters/${id}/helm/releases/demo/any/uninstall`, { dryRun: true })
    expect(un.status).toBe(403)
    const undo = await apiCall('POST', `/api/clusters/${id}/rollout/undo`, { namespace: 'demo', name: 'web', revision: 1, dryRun: true })
    expect(undo.status).toBe(403)
  } finally {
    await apiCall('POST', `/api/clusters/${id}/readonly`, { readOnly: false })
  }
})

test('CA05/CA06: RBAC — matriz, posso? e quem pode', async ({ page }) => {
  await openApp(page)
  const cl = await connect(page)
  await cl.getByTestId('tool-rbac').click()
  await selectDemo(page)
  // The kind user is cluster-admin: the matrix starts collapsed under the "Acesso total" banner.
  await expect(page.getByTestId('rbac-full').or(page.getByTestId('rbac-matrix'))).toBeVisible()
  if (await page.getByTestId('rbac-full').isVisible()) await page.getByTestId('rbac-show-matrix').click()
  await expect(page.getByTestId('rbac-matrix')).toContainText('Pod')
  await page.locator('.tab', { hasText: 'Posso…?' }).click()
  await page.getByTestId('access-verb').selectOption('delete')
  await page.getByTestId('access-resource').selectOption('/pods')
  await page.getByRole('button', { name: 'Posso?' }).click()
  await expect(page.getByTestId('can-answer')).toContainText('Sim')
  await page.locator('.tab', { hasText: 'Quem pode…?' }).click()
  await page.getByTestId('access-verb').selectOption('get')
  await page.getByTestId('access-resource').selectOption('/secrets')
  await page.getByTestId('access-ns').fill('demo')
  await page.getByRole('button', { name: 'Quem pode?' }).click()
  await expect(page.getByTestId('whocan-table')).toContainText('demo/e2e-reader')
  await expect(page.getByTestId('whocan-table')).toContainText('e2e-secrets')
})

test('CA07: Prometheus detectado e gráfico de CPU do pod com pontos', async ({ page }) => {
  await openApp(page)
  await connect(page)
  await page.getByTestId('nav-kind-Pod').click()
  await selectDemo(page)
  await page.locator('[data-testid^="row-web-"]').first().click()
  await page.getByTestId('tab-metrics').click()
  const cpu = page.getByTestId('chart-cpu')
  await expect(cpu.locator('canvas').first()).toBeVisible({ timeout: 30_000 })
  await expect(cpu).not.toContainText('Sem dados')
  await expect(page.getByTestId('chart-memory')).toBeVisible()
  await expect(page.locator('.metrics .toolbar')).toContainText('monitoring/prometheus-server')
})

test('CA08: overview do cluster', async ({ page }) => {
  await openApp(page)
  const cl = await connect(page)
  await cl.getByTestId('tool-overview').click()
  await expect(page.getByTestId('ov-nodes')).toContainText('leme-dev-control-plane')
  await expect(page.getByTestId('ov-Deployment')).toBeVisible()
  await expect(page.getByTestId('gauge-CPU')).toContainText('Requests')
})

test('CA09/CA10: topologia do deployment e do namespace', async ({ page }) => {
  await openApp(page)
  const cl = await connect(page)
  await page.getByTestId('nav-kind-Deployment').click()
  await selectDemo(page)
  await page.getByTestId('row-web').click()
  await page.getByTestId('tab-topology').click()
  const topo = page.getByTestId('topology')
  await expect(topo.getByTestId('topo-Deployment/demo/web')).toBeVisible({ timeout: 30_000 })
  await expect(topo.locator('[data-testid^="topo-ReplicaSet/demo/web-"]').first()).toBeVisible()
  await expect(topo.locator('[data-testid^="topo-Pod/demo/web-"]').first()).toBeVisible()
  await expect(topo.getByTestId('topo-Service/demo/web')).toBeVisible()
  await expect(topo.getByTestId('topo-Node//leme-dev-control-plane')).toBeVisible()
  // Namespace topology shows every workload.
  await cl.getByTestId('tool-topology').click()
  await selectDemo(page)
  const ns = page.getByTestId('topology').filter({ visible: true })
  await expect(ns.getByTestId('topo-Deployment/demo/broken')).toBeVisible({ timeout: 30_000 })
  await expect(ns.getByTestId('topo-Deployment/demo/web')).toBeVisible()
})

test('CA11: timeline agrupa eventos do broken sob o Deployment', async ({ page }) => {
  await openApp(page)
  const cl = await connect(page)
  await cl.getByTestId('tool-timeline').click()
  await selectDemo(page)
  // The broken deployment keeps emitting BackOff events; make sure there is a fresh one.
  kubectl('-n', 'demo', 'delete', 'pod', '-l', 'app=broken', '--wait=false')
  await expect.poll(async () => page.getByTestId('timeline').getAttribute('data-rows'), { timeout: 60_000 }).toContain('demo|Deployment/broken')
  await expect(page.locator('.timeline-panel .toolbar')).toContainText('eventos retidos')
})

test('CA12: dois clusters lado a lado com cores distintas e layout restaurado', async ({ page }) => {
  await openApp(page)
  await connect(page, KIND_CONTEXT)
  await connect(page, ALIAS_CONTEXT)
  const ids = [await kindId(KIND_CONTEXT), await kindId(ALIAS_CONTEXT)]
  for (const name of [KIND_CONTEXT, ALIAS_CONTEXT]) {
    // The hotbar switches the navigator; double click keeps each overview in its own tab.
    await page.getByTestId(`hotbar-${name}`).click()
    await expect(page.locator('.navigator .nav-cluster .cluster-name', { hasText: new RegExp(`^${name}$`) })).toBeVisible()
    await page.getByTestId('tool-overview').dblclick()
  }
  const tabs = page.locator('.ctab[data-cluster]')
  await expect(tabs).toHaveCount(2)
  let colors = await tabs.evaluateAll((els) => els.map((e) => getComputedStyle(e).getPropertyValue('--cc').trim()))
  if (colors[0] === colors[1]) {
    // Same hash bucket: change one color like a user would.
    await page.getByTestId(`hotbar-${ALIAS_CONTEXT}`).click({ button: 'right' })
    await page.getByRole('menuitem', { name: /Personalizar/ }).click()
    await page.getByTestId('cluster-profile').locator('button.cp-swatch:not(.on)').first().click()
    await page.getByTestId('cp-save').click()
    colors = await tabs.evaluateAll((els) => els.map((e) => getComputedStyle(e).getPropertyValue('--cc').trim()))
  }
  expect(colors[0]).not.toBe(colors[1])
  expect(new Set(await tabs.evaluateAll((els) => els.map((e) => e.getAttribute('data-cluster'))))).toEqual(new Set(ids))
  // Layout survives a reload; panels wait for an explicit connect.
  await page.waitForTimeout(800)
  await page.reload()
  await expect(page.locator('.ctab[data-cluster]')).toHaveCount(2)
  await expect(page.getByRole('button', { name: 'Conectar' }).first()).toBeVisible()
})

test('CA13: Ctrl+K encontra web e abre o detalhe', async ({ page }) => {
  await openApp(page)
  await connect(page)
  await page.keyboard.press('Control+K')
  await page.getByTestId('palette-input').fill('web')
  const item = page.getByTestId('palette-item').filter({ hasText: 'Deployment · demo' }).first()
  await expect(item).toBeVisible({ timeout: 20_000 })
  await item.click()
  await expect(page.locator('.obj-title', { hasText: 'web' }).locator('.kind-chip', { hasText: 'Deployment' })).toBeVisible()
})

test('CA14: rollback de deploy/web restaura a imagem', async ({ page }) => {
  kubectl('-n', 'demo', 'set', 'image', 'deploy/web', 'nginx=busybox:1.36')
  kubectl('-n', 'demo', 'annotate', 'deploy/web', 'kubernetes.io/change-cause=e2e: imagem errada', '--overwrite')
  await openApp(page)
  await connect(page)
  await page.getByTestId('nav-kind-Deployment').click()
  await selectDemo(page)
  await page.getByTestId('row-web').click()
  await page.getByTestId('tab-revisions').click()
  const table = page.getByTestId('revisions')
  await expect(table).toContainText('busybox:1.36')
  const previous = table.locator('tr', { hasText: 'nginx:1.27-alpine' }).first()
  await previous.getByRole('button', { name: 'Reverter' }).click()
  await expect(page.getByTestId('yaml-diff')).toBeVisible()
  await page.getByTestId('review-confirm').click()
  await expect(page.locator('.toast-ok', { hasText: 'Reversão de web' })).toBeVisible()
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'deploy', 'web', '-o', 'jsonpath={.spec.template.spec.containers[0].image}')).toBe('nginx:1.27-alpine')
})

test('segurança M2: token do cluster, referências de chart e URLs com credenciais', async () => {
  const id = await kindId()
  await apiCall('POST', `/api/clusters/${id}/connect`)
  // The kubeconfig token never goes to a host outside the cluster's domain.
  const leak = await apiCall('PUT', `/api/clusters/${id}/prometheus`, { mode: 'url', url: 'https://evil.example.com', sendToken: true })
  expect(leak.status).toBe(400)
  const leakTest = await apiCall('POST', `/api/clusters/${id}/prometheus/test`, { mode: 'url', url: 'https://evil.example.com', sendToken: true })
  expect(leakTest.json.ok).toBe(false)
  // Service-mode path cannot escape the service proxy.
  const escape = await apiCall('PUT', `/api/clusters/${id}/prometheus`, { mode: 'service', namespace: 'monitoring', service: 'prometheus-server', port: '80', path: '/../../../api/v1/secrets' })
  expect(escape.status).toBe(400)
  // Chart "show" only takes repo/chart, oci:// or https:// references.
  for (const ref of ['/home', 'file:///etc/passwd', 'http://example.com/x.tgz']) {
    const r = await apiCall('GET', `/api/helm/chart?ref=${encodeURIComponent(ref)}`)
    expect(r.status, ref).toBe(502)
    expect(r.json.error, ref).toMatch(/chart must be|https|invalid URL/)
  }
  const creds = await apiCall('POST', '/api/helm/repos', { name: 'x', url: 'https://user:pass@charts.example.com' })
  expect(creds.status).toBe(400)
  // Too many metrics in one chart request.
  const many = await apiCall('POST', `/api/clusters/${id}/prometheus/chart`, { scope: { kind: 'cluster' }, metrics: Array(20).fill('cpu'), range: '1h' })
  expect(many.status).toBe(400)
})
