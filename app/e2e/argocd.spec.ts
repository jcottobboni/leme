// M5 Argo CD end-to-end, against the isolated kind cluster ONLY. There is no Argo CD controller:
// minimal CRDs (created only if absent, never deleted) and a hand-written Application status
// (app/e2e/fixtures/argocd). Every object of this spec is named m5-argocd-*.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-argo-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-argo-'))
const FIX = path.join(__dirname, 'fixtures', 'argocd')
const APP_NS = 'm5-argocd-system'
const APP = 'm5-argocd-guestbook'
const DEST_NS = 'm5-argocd-guestbook'
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

/** Creates the CRDs only when absent; never deletes or replaces them (the cluster is shared). */
function ensureCRDs() {
  try {
    kubectl('create', '-f', path.join(FIX, 'crds.yaml'))
  } catch (e) {
    const msg = String((e as { stderr?: string }).stderr ?? e)
    if (!/AlreadyExists|already exists/.test(msg)) throw e
  }
  kubectl('wait', '--for=condition=Established', '--timeout=60s', 'crd/applications.argoproj.io', 'crd/appprojects.argoproj.io')
}

/** Recreates only the fixture Application (status included: the CRD has no status subresource). */
function recreateApp() {
  kubectl('-n', APP_NS, 'delete', 'application.argoproj.io', APP, '--ignore-not-found', '--wait=true')
  kubectl('create', '-f', path.join(FIX, 'application.yaml'))
}

test.beforeAll(async () => {
  ensureCRDs()
  kubectl('apply', '-f', path.join(FIX, 'objects.yaml'))
  recreateApp()
  // The engine discovers the CRDs when the UI connects, after the fixture exists.
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: CONFIG },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      try {
        const m = JSON.parse(l)
        if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
      } catch {
        /* not a JSON line */
      }
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.beforeEach(() => recreateApp())

test.afterAll(() => {
  engine?.kill('SIGTERM')
  // Leave the Application as the fixture defines it (no pending operation) for the next run.
  try {
    recreateApp()
  } catch {
    /* best effort */
  }
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

async function openArgo(page: Page) {
  await page.getByTestId('tool-argocd').click()
  const panel = page.getByTestId('argocd-panel').filter({ visible: true })
  await expect(panel).toBeVisible()
  return panel
}

async function engineCall(page: Page, method: string, url: string, body?: unknown) {
  return page.evaluate(async ({ base, token, method, url, body }) => {
    const r = await fetch(base + url, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
    return { status: r.status, body: await r.json().catch(() => null) }
  }, { base, token: TOKEN, method, url, body })
}

async function clusterId(page: Page) {
  const r = await engineCall(page, 'GET', '/api/clusters')
  return (r.body.clusters as { id: string; name: string }[]).find((c) => c.name === KIND_CONTEXT)!.id
}

test('CA03: Application do fixture aparece com sync/health, origem, destino, recursos, condições e histórico', async ({ page }) => {
  await openApp(page)
  const panel = await openArgo(page)
  const row = panel.getByTestId(`argo-app-${APP}`)
  await expect(row.getByTestId('argo-sync')).toHaveText('Fora de sincronia')
  await expect(row.getByTestId('argo-health')).toHaveText('Saudável')
  await expect(row).toContainText('m5-argocd-default')
  await expect(row).toContainText('guestbook')
  // Search and filters.
  await panel.getByTestId('argo-search').fill('nao-existe')
  await expect(panel).toContainText('Nenhuma Application com estes filtros')
  await panel.getByTestId('argo-search').fill('guestbook')
  await panel.getByTestId('argo-filter-sync').selectOption('Synced')
  await expect(row).toHaveCount(0)
  await panel.getByTestId('argo-filter-sync').selectOption('OutOfSync')
  await expect(row).toBeVisible()

  await row.click()
  const detail = panel.getByTestId('argo-detail')
  await expect(detail).toContainText('argocd-example-apps.git')
  await expect(detail).toContainText(`${DEST_NS} em este cluster`)
  await expect(detail.getByTestId('argo-operation')).toContainText('Succeeded')
  // Resource tree grouped by kind; the ConfigMap is not in the cluster.
  await expect(detail.getByTestId('argo-kind-Deployment')).toContainText('m5-argocd-guestbook-ui')
  await expect(detail.getByTestId('argo-kind-Service')).toContainText('m5-argocd-guestbook-ui')
  await expect(detail.getByTestId('argo-res-ConfigMap-m5-argocd-guestbook-config')).toContainText('ausente no cluster')
  await expect(detail.getByTestId('argo-conditions')).toContainText('SyncError')
  await expect(detail.getByTestId('argo-history').locator('tbody tr')).toHaveCount(2)
  // A managed object opens in the object panel.
  await detail.getByTestId('argo-res-Deployment-m5-argocd-guestbook-ui').getByRole('button', { name: 'm5-argocd-guestbook-ui' }).click()
  await expect(page.locator('.object-panel').filter({ visible: true })).toContainText('m5-argocd-guestbook-ui')
  // "Ver na Topologia" opens the destination namespace.
  await page.getByTestId('tool-argocd').click()
  await panel.getByTestId('argo-topology').click()
  await expect(page.getByTestId(`topo-Deployment/${DEST_NS}/m5-argocd-guestbook-ui`).first()).toBeVisible()
})

test('CA03: Sync gera proposta com diff do operation e aplicar grava o pedido', async ({ page }) => {
  await openApp(page)
  const panel = await openArgo(page)
  await panel.getByTestId(`argo-app-${APP}`).click()
  const detail = panel.getByTestId('argo-detail')
  await detail.getByTestId('argo-sync-btn').click()
  const review = page.getByTestId('argo-proposal')
  await expect(review).toContainText('dry-run ok')
  await expect(review.getByTestId('argo-notes')).toContainText('controlador do Argo CD')
  const diff = review.getByTestId('yaml-diff')
  await expect(diff).toContainText('operation')
  await expect(diff).toContainText('leme')
  await page.getByTestId('argo-apply').click()
  await page.getByRole('dialog', { name: 'Confirmar pedido ao Argo CD' }).getByRole('button', { name: 'Aplicar' }).click()
  await expect(review).toHaveCount(0)
  await expect.poll(() => kubectl('-n', APP_NS, 'get', 'application.argoproj.io', APP, '-o', 'jsonpath={.operation.initiatedBy.username}')).toBe('leme')
  expect(kubectl('-n', APP_NS, 'get', 'application.argoproj.io', APP, '-o', 'jsonpath={.operation.sync.revision}')).toBe('53e28ff20cc530b9ada2173fbbd64d48338583ba')
  // The request is pending (no controller here): a second Sync is blocked.
  await expect(detail.getByTestId('argo-sync-btn')).toBeDisabled()
  // Audited with source argocd.
  const audit = await engineCall(page, 'GET', '/api/audit')
  const mine = (audit.body.entries as { source: string; action: string; result: string }[]).filter((e) => e.source === 'argocd' && e.result === 'ok').map((e) => e.action)
  expect(mine).toEqual(expect.arrayContaining(['propose', 'apply']))
})

test('Refresh gera proposta com a anotação e aplicar grava', async ({ page }) => {
  await openApp(page)
  const panel = await openArgo(page)
  await panel.getByTestId(`argo-app-${APP}`).click()
  const detail = panel.getByTestId('argo-detail')
  await detail.getByTestId('argo-hard').check()
  await detail.getByTestId('argo-refresh-btn').click()
  const review = page.getByTestId('argo-proposal')
  await expect(review.getByTestId('yaml-diff')).toContainText('argocd.argoproj.io/refresh')
  await page.getByTestId('argo-apply').click()
  await page.getByRole('dialog', { name: 'Confirmar pedido ao Argo CD' }).getByRole('button', { name: 'Aplicar' }).click()
  await expect.poll(() => kubectl('-n', APP_NS, 'get', 'application.argoproj.io', APP, '-o', 'jsonpath={.metadata.annotations.argocd\\.argoproj\\.io/refresh}')).toBe('hard')
})

test('CA03: somente leitura desabilita os botões e o engine recusa aplicar (403)', async ({ page }) => {
  await openApp(page)
  const id = await clusterId(page)
  // A proposal made while writable, applied after locking: refused by the engine.
  const p = await engineCall(page, 'POST', `/api/clusters/${id}/argocd/apps/${APP_NS}/${APP}/propose`, { action: 'sync' })
  expect(p.status).toBe(200)
  await page.getByTestId('rw-toggle').click() // kind opens writable; lock it
  try {
    const panel = await openArgo(page)
    await panel.getByTestId(`argo-app-${APP}`).click()
    const detail = panel.getByTestId('argo-detail')
    await expect(detail.getByTestId('argo-sync-btn')).toBeDisabled()
    await expect(detail.getByTestId('argo-refresh-btn')).toBeDisabled()
    await expect(detail.getByTestId('argo-readonly')).toContainText('somente leitura')
    const r = await engineCall(page, 'POST', `/api/clusters/${id}/argocd/apply/${p.body.token}`)
    expect(r.status).toBe(403)
    expect(kubectl('-n', APP_NS, 'get', 'application.argoproj.io', APP, '-o', 'jsonpath={.operation}')).toBe('')
    // Read-only proposals never run a server-side dry-run.
    const ro = await engineCall(page, 'POST', `/api/clusters/${id}/argocd/apps/${APP_NS}/${APP}/propose`, { action: 'sync' })
    expect(ro.status).toBe(200)
    expect(ro.body.dryRun).toBe(false)
  } finally {
    await page.getByTestId('rw-toggle').click()
  }
})

test('CA06: sem os CRDs do Argo CD o painel explica o que falta', async ({ page }) => {
  // The shared cluster keeps the CRDs (never deleted), so the engine answer without them is
  // replayed from the Go test contract; the engine side is covered by internal/api tests.
  await page.route(/\/api\/clusters\/[^/]+\/argocd(\?|$)/, (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ installed: false, apps: [], projects: [], hint: 'Os CRDs do Argo CD (applications.argoproj.io e appprojects.argoproj.io) não existem neste cluster.' }),
  }))
  await openApp(page)
  const panel = await openArgo(page)
  await expect(panel.getByTestId('argo-not-installed')).toContainText('Argo CD não encontrado')
  await expect(panel.getByTestId('argo-not-installed')).toContainText('applications.argoproj.io')
})
