// M7 front O: aggregated logs (CA01), diff between namespaces (CA03) and the persistent change history (CA06).
// Kind cluster ONLY. Creates and deletes its own namespaces (m7-logs, m7-diff, m7-history); demo is only read.
import { test, expect, type Page } from '@playwright/test'
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
const NAMESPACES = ['m7-logs', 'm7-diff', 'm7-history']
const CONFIG_DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-m7-'))
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

function apply(yaml: string) {
  execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, 'apply', '-f', '-'], { input: yaml, encoding: 'utf8' })
}

async function startEngine() {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: CONFIG_DIR },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
}

async function stopEngine() {
  if (!engine || engine.exitCode !== null) return
  const done = new Promise((r) => engine.once('exit', r))
  engine.kill('SIGTERM')
  await done
}

async function api(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return r.json()
}

test.beforeAll(async () => {
  for (const ns of NAMESPACES) apply(`apiVersion: v1\nkind: Namespace\nmetadata: {name: ${ns}}\n`)
  for (const ns of NAMESPACES) {
    for (let i = 0; i < 60 && !kubectl('-n', ns, 'get', 'serviceaccounts', '-o', 'name').includes('default'); i++) await new Promise((r) => setTimeout(r, 500))
  }
  apply(`apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: m7-logs, labels: {app: web}}
spec:
  replicas: 3
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      terminationGracePeriodSeconds: 1
      containers:
      - name: app
        image: busybox:1.36
        imagePullPolicy: IfNotPresent
        args: [sh, -c, 'i=0; while true; do echo "tick $i from $HOSTNAME"; i=$((i+1)); sleep 1; done']
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: app, namespace: m7-history, labels: {app: hist}}
spec:
  replicas: 0
  selector: {matchLabels: {app: hist}}
  template:
    metadata: {labels: {app: hist}}
    spec:
      containers: [{name: app, image: "busybox:1.36", args: [sleep, "3600"]}]
`)
  // CA03: a copy of demo/web with two real changes (replicas and image). Everything else comes from the live object.
  const web = JSON.parse(kubectl('-n', 'demo', 'get', 'deployment', 'web', '-o', 'json'))
  const ann = { ...(web.metadata.annotations ?? {}) }
  delete ann['kubectl.kubernetes.io/last-applied-configuration']
  delete ann['deployment.kubernetes.io/revision']
  const copy = {
    apiVersion: 'apps/v1', kind: 'Deployment',
    metadata: { name: 'web', namespace: 'm7-diff', labels: web.metadata.labels, annotations: ann },
    spec: { ...web.spec, replicas: 0 },
  }
  copy.spec.template.spec.containers[0].image = 'nginx:1.28-alpine'
  execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, 'create', '-f', '-'], { input: JSON.stringify(copy), encoding: 'utf8' })
  kubectl('-n', 'm7-logs', 'rollout', 'status', 'deployment/web', '--timeout=120s')
  await startEngine()
})

test.afterAll(async () => {
  await stopEngine()
  kubectl('delete', 'namespace', ...NAMESPACES, '--wait=false', '--ignore-not-found')
  fs.rmSync(CONFIG_DIR, { recursive: true, force: true })
})

async function connect(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

async function openKind(page: Page, kind: string, ns: string) {
  await page.getByTestId(`nav-kind-${kind}`).click()
  const sel = page.locator('.ns-select:visible').first()
  await expect(sel.locator(`option[value="${ns}"]`)).toHaveCount(1)
  await sel.selectOption(ns)
}

test('CA01: logs de todos os pods juntos, com cores, e um pod novo entra sem reabrir', async ({ page }) => {
  await connect(page)
  await openKind(page, 'Deployment', 'm7-logs')
  await page.getByTestId('row-web').click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Logs (todos os pods)' }).click()
  const panel = page.getByTestId('multilogs')
  await expect(panel).toBeVisible()
  const chips = panel.locator('[data-testid^="ml-pod-web-"]')
  await expect(chips).toHaveCount(3, { timeout: 30_000 })
  const names = await chips.evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')!.slice('ml-pod-'.length)))
  // Lines of the three pods arrive in the same terminal, each prefixed with its pod.
  const rows = panel.locator('.xterm-rows')
  for (const n of names) await expect.poll(async () => (await rows.textContent()) ?? '', { timeout: 20_000 }).toContain(n)
  await expect(rows).toContainText('tick')
  // One color per pod.
  const colors = await chips.locator('.ml-dot').evaluateAll((els) => els.map((e) => getComputedStyle(e).backgroundColor))
  expect(new Set(colors).size).toBe(3)
  await expect(panel.getByTestId('ml-status')).toContainText('3 pods')

  // Scale up: the new pod joins the open panel.
  kubectl('-n', 'm7-logs', 'scale', 'deployment/web', '--replicas=4')
  await expect(chips).toHaveCount(4, { timeout: 60_000 })
  const added = (await chips.evaluateAll((els) => els.map((e) => e.getAttribute('data-testid')!.slice('ml-pod-'.length)))).find((n) => !names.includes(n))!
  await expect.poll(async () => (await rows.textContent()) ?? '', { timeout: 30_000 }).toContain(`pod ${added} entrou`)
  await expect.poll(async () => (await rows.textContent()) ?? '', { timeout: 30_000 }).toContain(`tick 0 from ${added}`)

  // Filter: only lines that match stay on screen.
  await panel.getByTestId('ml-filter').fill(`from ${added}`)
  await expect.poll(async () => (await rows.textContent()) ?? '').not.toContain(`from ${names[0]}`)
  await panel.getByTestId('ml-filter').fill('')
})

test('CA03: comparar demo/web com uma cópia alterada noutro namespace mostra só a diferença real', async ({ page }) => {
  await connect(page)
  await openKind(page, 'Deployment', 'demo')
  await page.getByTestId('row-web').click()
  await page.getByTestId('drawer').getByRole('button', { name: 'Comparar com…' }).click()
  const dialog = page.getByTestId('compare-dialog')
  await dialog.getByTestId('compare-namespace').fill('m7-diff')
  await dialog.getByTestId('compare-go').click()
  const diff = page.getByTestId('diff-panel')
  await expect(diff).toBeVisible()
  await expect(diff.getByTestId('diff-changes')).toBeVisible({ timeout: 20_000 })
  const paths = await diff.getByTestId('diff-change').evaluateAll((els) => els.map((e) => e.getAttribute('data-path')).sort())
  expect(paths).toEqual(['/spec/replicas', '/spec/template/spec/containers/0/image'])
  await expect(diff.getByTestId('diff-right')).toContainText('m7-diff/web')
  await expect(diff.getByTestId('yaml-diff')).toBeVisible()
})

test('CA06: alterar a imagem gera um registro com diff que continua lá depois de reiniciar o engine', async ({ page }) => {
  await connect(page)
  // The Deployments grid of the namespace keeps the informer that observes the change.
  await openKind(page, 'Deployment', 'm7-history')
  await expect(page.getByTestId('row-app')).toBeVisible()
  kubectl('-n', 'm7-history', 'set', 'image', 'deployment/app', 'app=busybox:latest')
  await page.getByTestId('row-app').click()
  await page.getByTestId('drawer').getByTestId('tab-history').click()
  const entry = page.getByTestId('history-entry').first()
  await expect(entry).toBeVisible({ timeout: 20_000 })
  await expect(entry).toHaveAttribute('data-type', 'updated')
  await entry.locator('.hist-head').click()
  await expect(entry).toContainText('/spec/template/spec/containers/0/image')
  await expect(entry).toContainText('busybox:1.36')
  await expect(entry).toContainText('busybox:latest')

  // Restart the engine with the same config directory: the record is still there.
  await stopEngine()
  await startEngine()
  await connect(page)
  const { clusters } = await api('GET', '/api/clusters')
  const id = clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  const hist = await api('GET', `/api/clusters/${id}/history?namespace=m7-history&group=apps&resource=deployments&name=app`)
  expect(hist.entries.length).toBeGreaterThanOrEqual(1)
  expect(JSON.stringify(hist.entries[0].ops)).toContain('busybox:latest')
  // And in the Timeline's "Mudanças" view.
  await openKind(page, 'Deployment', 'm7-history')
  await page.getByTestId('tool-timeline').click()
  await page.getByTestId('timeline-mode').getByRole('button', { name: 'Mudanças' }).click()
  const view = page.getByTestId('changes-view')
  await expect(view).toBeVisible()
  await expect(view.getByTestId('history-entry').first()).toContainText('app', { timeout: 20_000 })
  await expect(view.getByTestId('history-entry').first()).toContainText('containers[0].image')
})
