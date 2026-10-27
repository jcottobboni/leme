// M8 plugins: Leme and k9s plugin files, trust, the read-only proxy, CRD views and CEL rules.
// Kind cluster ONLY; the plugin and k9s folders are temporary, never the user's.
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
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-m8-config-'))
const K9S = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-m8-k9s-'))
const DATA = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-m8-data-'))
const PLUGINS = path.join(CONFIG, 'plugins')

let engine: ChildProcess
let base = ''
let clusterId = ''

const kubectl = (args: string[], input?: string) =>
  execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', input })

async function apiCall(method: string, p: string, body?: unknown) {
  const r = await fetch(base + p, { method, headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined })
  return { status: r.status, json: await r.json().catch(() => undefined) }
}

const CRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.e2e.leme.dev}
spec:
  group: e2e.leme.dev
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
  - name: v1
    served: true
    storage: true
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
`

const LEME_PLUGIN = (extra = '') => `apiVersion: leme.dev/v1
name: e2e
actions:
- id: whoami
  title: Ver pod (kubectl)
  scopes: [pods]
  command: kubectl
  args: [get, pod, $NAME, -n, $NAMESPACE, -o, "jsonpath={.metadata.name}@{.metadata.namespace}@$CONTEXT${extra}"]
  mode: output
- id: try-delete
  title: Tentar apagar ConfigMap
  scopes: [configmaps]
  command: kubectl
  args: [delete, configmap, $NAME, -n, $NAMESPACE, --dry-run=server]
  mode: output
- id: scale-zero
  title: Escalar para zero
  scopes: [deployments]
  command: kubectl
  args: [scale, deploy, $NAME, -n, $NAMESPACE, --replicas=0, --dry-run=server]
  mutates: true
links:
- id: docs
  title: Documentação do app
  scopes: [deployments]
  url: https://example.com/apps/$NAMESPACE/$NAME
views:
- group: e2e.leme.dev
  kind: Widget
  columns:
  - {name: Cor, jsonPath: .spec.color}
  fields:
  - {name: Tamanho em dobro, expr: 'object.spec.size * 2'}
  health:
  - {when: 'object.spec.color == "red"', status: error}
  - {when: 'true', status: ok}
rules:
- id: red-widget
  title: Widget vermelho
  severity: high
  kinds: [e2e.leme.dev/Widget]
  match: 'object.spec.color == "red"'
  explanation: Widgets vermelhos não passam na revisão.
  evidence: '"cor: " + object.spec.color'
`

const K9S_PLUGINS = `plugins:
  echo-pod:
    shortCut: Shift-E
    description: Eco do k9s
    scopes: [po]
    command: sh
    args: [-c, "echo k9s:$NAME:$NAMESPACE; kubectl get pod $NAME -n $NAMESPACE -o name"]
    background: false
  piped:
    scopes: [pods]
    command: kubectl
    pipes: [less]
`

test.beforeAll(async () => {
  kubectl(['apply', '-f', '-'], CRD)
  kubectl(['wait', '--for=condition=Established', 'crd/widgets.e2e.leme.dev', '--timeout=60s'])
  kubectl(['apply', '-f', '-'], `apiVersion: e2e.leme.dev/v1
kind: Widget
metadata: {name: azul, namespace: demo}
spec: {color: blue, size: 3}
---
apiVersion: e2e.leme.dev/v1
kind: Widget
metadata: {name: vermelho, namespace: demo}
spec: {color: red, size: 5}
`)
  fs.mkdirSync(PLUGINS, { recursive: true })
  fs.writeFileSync(path.join(PLUGINS, 'e2e.yaml'), LEME_PLUGIN())
  fs.writeFileSync(path.join(PLUGINS, 'bad.yaml'), "apiVersion: leme.dev/v1\nname: bad\nlinks:\n- {id: x, title: X, scopes: [pods], url: 'javascript:alert(1)'}\n")
  fs.writeFileSync(path.join(K9S, 'plugins.yaml'), K9S_PLUGINS)
  const env = { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: CONFIG, K9S_CONFIG_DIR: K9S, XDG_DATA_HOME: DATA }
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC], { env })
  engine.stderr?.on('data', (d) => process.env.E2E_DEBUG && process.stderr.write(d))
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  const { json } = await apiCall('GET', '/api/clusters')
  clusterId = json.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  try {
    kubectl(['delete', 'crd', 'widgets.e2e.leme.dev', '--wait=false'])
  } catch {
    /* best effort */
  }
  for (const d of [CONFIG, K9S, DATA]) fs.rmSync(d, { recursive: true, force: true })
})

async function connect(page: Page, readOnly: boolean) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
  await apiCall('POST', `/api/clusters/${clusterId}/readonly`, readOnly ? { readOnly: true } : { readOnly: false, confirm: KIND_CONTEXT })
  // The store picks the lock state on reconnect/refresh; reload to see it in the UI.
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

async function openKind(page: Page, testid: string) {
  await page.getByTestId(testid).click()
  const ns = page.locator('.ns-select:visible').first()
  if (await ns.count()) {
    await expect(ns.locator('option[value="demo"]')).toHaveCount(1)
    await ns.selectOption('demo')
  }
}

async function rowMenu(page: Page, rowTestId: string) {
  const row = page.getByTestId(rowTestId).first()
  await row.waitFor()
  await row.click({ button: 'right' })
  return page.locator('.ctx-menu, [role="menu"]').last()
}

const webPod = () => kubectl(['-n', 'demo', 'get', 'pods', '-l', 'app=web', '-o', 'jsonpath={.items[0].metadata.name}']).trim()

test('CA01/CA04: ação Leme pede confiança, roda pelo proxy no contexto certo e volta a pedir quando muda', async ({ page }) => {
  await connect(page, true)
  await openKind(page, 'nav-kind-Pod')
  const pod = webPod()
  const menu = await rowMenu(page, `row-${pod}`)
  await menu.getByText('Ver pod (kubectl) (revisar)').click()
  const trust = page.getByTestId('plugin-trust')
  await expect(trust.getByTestId('plugin-cmd')).toContainText('kubectl get pod $NAME -n $NAMESPACE')
  await trust.getByTestId('plugin-trust-ok').click()
  const out = page.getByTestId('plugin-output')
  // The plugin's kubectl reached the kind cluster through the read-only proxy, under the real context name.
  await expect(out.getByTestId('plugin-output-text')).toHaveText(`${pod}@demo@${KIND_CONTEXT}`)
  await expect(out.getByTestId('plugin-exit')).toContainText('saída 0')
  await out.getByRole('button', { name: 'Fechar' }).click()

  // Trusted: the second run goes straight to the output.
  await (await rowMenu(page, `row-${pod}`)).getByText('Ver pod (kubectl)', { exact: true }).click()
  await expect(page.getByTestId('plugin-output-text')).toHaveText(`${pod}@demo@${KIND_CONTEXT}`)
  await page.getByTestId('plugin-output').getByRole('button', { name: 'Fechar' }).click()

  // Changing the command in the file asks for trust again.
  fs.writeFileSync(path.join(PLUGINS, 'e2e.yaml'), LEME_PLUGIN('@mudou'))
  await expect.poll(async () => (await apiCall('POST', '/api/plugins/reload')).json.actions.find((a: { id: string }) => a.id === 'e2e/whoami').trusted).toBe(false)
  await page.evaluate(() => window.dispatchEvent(new Event('focus')))
  await expect.poll(async () => {
    const m = await rowMenu(page, `row-${pod}`)
    const n = await m.getByText('Ver pod (kubectl) (revisar)').count()
    await page.keyboard.press('Escape')
    return n
  }).toBe(1)
  // Audit: trust and runs are recorded with source "plugin".
  const audit = await apiCall('GET', '/api/audit?limit=50')
  const entries = (audit.json?.entries ?? audit.json ?? []) as { source: string; action: string }[]
  expect(entries.some((e) => e.source === 'plugin' && e.action === 'run')).toBe(true)
  expect(entries.some((e) => e.source === 'plugin' && e.action === 'trust')).toBe(true)
})

test('CA03: cluster travado — ação de alteração desabilitada e o proxy recusa escrita', async ({ page }) => {
  await connect(page, true)
  await openKind(page, 'nav-kind-Deployment')
  const menu = await rowMenu(page, 'row-web')
  await expect(menu.getByRole('menuitem', { name: /Escalar para zero/ })).toBeDisabled()
  await page.keyboard.press('Escape')
  // API too: a mutating action never starts on a locked cluster.
  const trustAll = (await apiCall('GET', '/api/plugins')).json.actions.find((a: { id: string }) => a.id === 'e2e/scale-zero')
  await apiCall('POST', '/api/plugins/trust', { id: trustAll.id, hash: trustAll.hash })
  const run = await apiCall('POST', `/api/clusters/${clusterId}/plugins/run`, { action: 'e2e/scale-zero', input: { group: 'apps', version: 'v1', resource: 'deployments', namespace: 'demo', name: 'web' } })
  expect(run.status).toBe(403)

  // A "read" plugin that tries to delete gets a 403 from the proxy (server dry-run would otherwise pass).
  await openKind(page, 'nav-kind-ConfigMap')
  await (await rowMenu(page, 'row-app-config')).getByText('Tentar apagar ConfigMap (revisar)').click()
  await page.getByTestId('plugin-trust-ok').click()
  const out = page.getByTestId('plugin-output')
  await expect(out.getByTestId('plugin-output-text')).toContainText('cluster travado')
  await expect(out.getByTestId('plugin-exit')).not.toContainText('saída 0')
  await out.getByRole('button', { name: 'Fechar' }).click()
  expect(kubectl(['-n', 'demo', 'get', 'cm', 'app-config', '-o', 'name']).trim()).toBe('configmap/app-config')

  // Unlocked, the same command goes through (dry-run: nothing is deleted).
  await apiCall('POST', `/api/clusters/${clusterId}/readonly`, { readOnly: false, confirm: KIND_CONTEXT })
  const ok = await apiCall('POST', `/api/clusters/${clusterId}/plugins/run`, { action: 'e2e/try-delete', input: { group: '', version: 'v1', resource: 'configmaps', namespace: 'demo', name: 'app-config' } })
  expect(ok.json.exitCode).toBe(0)
  expect(ok.json.output).toContain('server dry run')
  await apiCall('POST', `/api/clusters/${clusterId}/readonly`, { readOnly: true })
})

test('CA02: plugin do k9s aparece no escopo certo e roda no terminal', async ({ page }) => {
  await connect(page, true)
  await openKind(page, 'nav-kind-Pod')
  const pod = webPod()
  const menu = await rowMenu(page, `row-${pod}`)
  await menu.getByText('Eco do k9s (revisar)').click()
  await expect(page.getByTestId('plugin-trust')).toContainText('Plugin do k9s')
  await page.getByTestId('plugin-trust-ok').click()
  const term = page.getByTestId('plugin-term')
  await expect(term.locator('.xterm-rows')).toContainText(`k9s:${pod}:demo`, { timeout: 20_000 })
  await expect(term.locator('.xterm-rows')).toContainText(`pod/${pod}`)
  await expect(term.getByTestId('plugin-term-status')).toHaveText('concluído')

  // The Plugins panel lists both files, the unsupported k9s entry and the refused javascript: link.
  await page.getByTestId('tool-plugins').click()
  const panel = page.getByTestId('plugins-panel')
  await expect(panel).toContainText('pipes não é suportado')
  await expect(panel.getByTestId('plugin-file-error')).toContainText('http(s)')
  // Turning k9s off removes its actions.
  // The checkbox follows the engine's answer, so click and wait for the new state.
  await panel.getByTestId('plugins-k9s').click()
  await expect(panel.getByTestId('plugins-k9s')).not.toBeChecked()
  await expect(panel.getByTestId('plugin-action-k9s/echo-pod')).toHaveCount(0)
  await panel.getByTestId('plugins-k9s').click()
  await expect(panel.getByTestId('plugins-k9s')).toBeChecked()
  await expect(panel.getByTestId('plugin-action-k9s/echo-pod')).toBeVisible()
})

test('CA05/CA06: visão de CRD (coluna, saúde, campo) e regra CEL no Problemas', async ({ page }) => {
  await connect(page, true)
  await page.locator('.nav-crd-head', { hasText: 'e2e.leme.dev' }).click()
  await openKind(page, 'nav-kind-Widget.e2e.leme.dev')
  await expect(page.locator('.grid-head, thead').first()).toContainText('Cor')
  const red = page.getByTestId('row-vermelho')
  await expect(red).toContainText('red')
  await expect(red).toHaveClass(/h-error/)
  await expect(page.getByTestId('row-azul')).toHaveClass(/h-ok/)
  await page.getByTestId('row-vermelho').dblclick()
  await expect(page.getByTestId('plugin-fields')).toContainText('Tamanho em dobro')
  await expect(page.getByTestId('plugin-fields')).toContainText('10')

  await page.getByTestId('tool-problems').click()
  const panel = page.getByTestId('problems')
  await panel.locator('.ns-select').selectOption('demo')
  const f = panel.getByTestId('finding-plugin:e2e/red-widget')
  await expect(f).toBeVisible({ timeout: 20000 })
  await expect(f).toContainText('vermelho')
  await expect(panel.getByTestId('finding-plugin:e2e/red-widget')).toHaveCount(1)
})

test('CA07: link de plugin abre a URL montada (só http/https)', async ({ page, context }) => {
  await connect(page, true)
  await openKind(page, 'nav-kind-Deployment')
  const menu = await rowMenu(page, 'row-web')
  const popup = context.waitForEvent('page')
  await menu.getByText('Documentação do app').click()
  const p = await popup
  expect(p.url()).toBe('https://example.com/apps/demo/web')
  await p.close()
})
