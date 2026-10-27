// M7 RF04 / CA04: Flux CD panel and reviewed reconcile/suspend/resume, against the isolated kind
// cluster ONLY. CRDs from scripts/gitops-crds.sh (SHA-256 pinned, never deleted); fixtures in the
// m7-flux namespace (app/e2e/fixtures/flux), recreated before each test and left in the fixture
// state; no Flux controller, the status is written with kubectl patch --subresource=status.
import { test, expect, type Page } from '@playwright/test'
import path from 'node:path'
import { FIXTURES, clusterId, engineCall, ensureGitopsCRDs, kubectl, openApp, recreate, startEngine, type Engine } from './gitops'

const FIX = path.join(FIXTURES, 'flux')
const NS = 'm7-flux'
const KINDS = ['kustomizations.kustomize.toolkit.fluxcd.io', 'helmreleases.helm.toolkit.fluxcd.io', 'gitrepositories.source.toolkit.fluxcd.io',
  'helmrepositories.source.toolkit.fluxcd.io', 'ocirepositories.source.toolkit.fluxcd.io']
let engine: Engine

const reset = () => recreate(FIX, KINDS, NS)

test.beforeAll(async () => {
  ensureGitopsCRDs()
  reset()
  engine = await startEngine('flux')
})

test.beforeEach(() => reset())

test.afterAll(() => {
  engine?.proc.kill('SIGTERM')
  try {
    reset()
  } catch {
    /* best effort */
  }
})

async function openFlux(page: Page) {
  await page.getByTestId('tool-flux').click()
  const panel = page.getByTestId('flux-panel').filter({ visible: true })
  await expect(panel).toBeVisible()
  return panel
}

const ks = (name: string) => kubectl('-n', NS, 'get', 'kustomizations.kustomize.toolkit.fluxcd.io', name, '-o', 'json')

test('CA04: o painel lista Kustomizations, HelmReleases e fontes com estado, revisão e fonte', async ({ page }) => {
  await openApp(page, engine)
  const panel = await openFlux(page)
  const apps = panel.getByTestId('flux-row-Kustomization-m7-flux-apps')
  await expect(apps.getByTestId('flux-ready')).toHaveText('Pronto')
  await expect(apps).toContainText('main@sha1:4f1c2b9e7d0a')
  await expect(apps).toContainText('GitRepository m7-flux-repo')
  await expect(panel.getByTestId('flux-row-Kustomization-m7-flux-broken').getByTestId('flux-ready')).toHaveText('Falhou')
  // Filters.
  await panel.getByTestId('flux-filter-state').selectOption('Falhou')
  await expect(apps).toHaveCount(0)
  await panel.getByTestId('flux-filter-state').selectOption('')
  await panel.getByTestId('flux-search').fill('broken')
  await expect(apps).toHaveCount(0)
  await panel.getByTestId('flux-search').fill('')

  await panel.getByRole('button', { name: /^HelmReleases/ }).click()
  const hr = panel.getByTestId('flux-row-HelmRelease-m7-flux-podinfo')
  await expect(hr.getByTestId('flux-ready')).toHaveText('Suspenso')
  await expect(hr).toContainText('podinfo@6.9.2')
  await expect(hr).toContainText('HelmRepository m7-flux-charts')

  await panel.getByRole('button', { name: /^Fontes/ }).click()
  const git = panel.getByTestId('flux-row-GitRepository-m7-flux-repo')
  await expect(git.getByTestId('flux-ready')).toHaveText('Pronto')
  await expect(git).toContainText('main@sha1:4f1c2b9e7d0a')
  await expect(git).toContainText('https://example.com/m7/fleet.git')
  await expect(panel.getByTestId('flux-row-OCIRepository-m7-flux-oci').getByTestId('flux-ready')).toHaveText('Desconhecido')

  // Detail of the failed Kustomization: the controller's message and the conditions.
  await panel.getByRole('button', { name: /^Kustomizations/ }).click()
  await panel.getByTestId('flux-row-Kustomization-m7-flux-broken').click()
  const detail = panel.getByTestId('flux-detail')
  await expect(detail.getByTestId('flux-message')).toContainText('kustomization path not found')
  await expect(detail.getByTestId('flux-conditions')).toContainText('ArtifactFailed')
})

test('CA04: reconcile gera proposta revisada (diff, dry-run) e aplicar grava a anotação, auditado como flux', async ({ page }) => {
  await openApp(page, engine)
  const panel = await openFlux(page)
  await panel.getByTestId('flux-row-Kustomization-m7-flux-apps').click()
  await panel.getByTestId('flux-reconcile-btn').click()
  const review = page.getByTestId('flux-proposal')
  await expect(review).toContainText('dry-run ok')
  await expect(review).toContainText('permissão ok')
  await expect(review.getByTestId('flux-notes')).toContainText('reconcile.fluxcd.io/requestedAt')
  await expect(review.getByTestId('yaml-diff')).toContainText('reconcile.fluxcd.io/requestedAt')
  // Nothing is written before the confirmation.
  expect(JSON.parse(ks('m7-flux-apps')).metadata.annotations?.['reconcile.fluxcd.io/requestedAt']).toBeUndefined()
  await page.getByTestId('flux-apply').click()
  await page.getByRole('dialog', { name: 'Confirmar pedido ao Flux' }).getByRole('button', { name: 'Aplicar' }).click()
  await expect(review).toHaveCount(0)
  await expect.poll(() => JSON.parse(ks('m7-flux-apps')).metadata.annotations?.['reconcile.fluxcd.io/requestedAt'] ?? '').toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/)
  // No controller here: the request stays pending and the panel says so.
  await expect(panel.getByTestId('flux-pending')).toBeVisible()
  const audit = await engineCall(page, engine, 'GET', '/api/audit')
  const mine = (audit.body.entries as { source: string; action: string; result: string; object?: string }[])
    .filter((e) => e.source === 'flux' && e.result === 'ok' && e.object?.includes('m7-flux-apps')).map((e) => e.action)
  expect(mine).toEqual(expect.arrayContaining(['propose', 'apply']))
})

test('CA04: suspender e retomar passam pela mesma revisão', async ({ page }) => {
  await openApp(page, engine)
  const panel = await openFlux(page)
  await panel.getByTestId('flux-row-Kustomization-m7-flux-apps').click()
  await panel.getByTestId('flux-suspend-btn').click()
  await expect(page.getByTestId('flux-proposal').getByTestId('yaml-diff')).toContainText('suspend: true')
  await page.getByTestId('flux-apply').click()
  await page.getByRole('dialog', { name: 'Confirmar pedido ao Flux' }).getByRole('button', { name: 'Aplicar' }).click()
  await expect.poll(() => JSON.parse(ks('m7-flux-apps')).spec.suspend ?? false).toBe(true)
  await expect(panel.getByTestId('flux-row-Kustomization-m7-flux-apps').getByTestId('flux-ready')).toHaveText('Suspenso')
  // Suspended: reconcile is disabled, resume is offered.
  await expect(panel.getByTestId('flux-reconcile-btn')).toBeDisabled()
  await panel.getByTestId('flux-resume-btn').click()
  await page.getByTestId('flux-apply').click()
  await page.getByRole('dialog', { name: 'Confirmar pedido ao Flux' }).getByRole('button', { name: 'Aplicar' }).click()
  await expect.poll(() => JSON.parse(ks('m7-flux-apps')).spec.suspend ?? false).toBe(false)
  expect(JSON.parse(ks('m7-flux-apps')).metadata.annotations?.['reconcile.fluxcd.io/requestedAt']).toBeTruthy()
})

test('CA04: somente leitura desabilita as ações e o engine recusa aplicar (403)', async ({ page }) => {
  await openApp(page, engine)
  const id = await clusterId(page, engine)
  const p = await engineCall(page, engine, 'POST', `/api/clusters/${id}/flux/Kustomization/${NS}/m7-flux-apps/propose`, { action: 'reconcile' })
  expect(p.status).toBe(200)
  await page.getByTestId('rw-toggle').click() // kind opens writable; lock it
  try {
    const panel = await openFlux(page)
    await panel.getByTestId('flux-row-Kustomization-m7-flux-apps').click()
    await expect(panel.getByTestId('flux-reconcile-btn')).toBeDisabled()
    await expect(panel.getByTestId('flux-suspend-btn')).toBeDisabled()
    await expect(panel.getByTestId('flux-readonly')).toContainText('somente leitura')
    const r = await engineCall(page, engine, 'POST', `/api/clusters/${id}/flux/apply/${p.body.token}`)
    expect(r.status).toBe(403)
    expect(JSON.parse(ks('m7-flux-apps')).metadata.annotations?.['reconcile.fluxcd.io/requestedAt']).toBeUndefined()
    const ro = await engineCall(page, engine, 'POST', `/api/clusters/${id}/flux/Kustomization/${NS}/m7-flux-apps/propose`, { action: 'reconcile' })
    expect(ro.status).toBe(200)
    expect(ro.body.dryRun).toBe(false)
  } finally {
    await page.getByTestId('rw-toggle').click()
  }
})

test('CA04: sem os CRDs do Flux o painel ensina o que falta', async ({ page }) => {
  // The shared cluster keeps the CRDs (never deleted): the engine answer without them is replayed
  // from the Go test contract (internal/api flux tests cover the engine side).
  await page.route(/\/api\/clusters\/[^/]+\/flux(\?|$)/, (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ installed: false, groups: {}, kustomizations: [], helmReleases: [], sources: [], hint: 'Os CRDs do Flux (kustomize.toolkit.fluxcd.io, helm.toolkit.fluxcd.io e source.toolkit.fluxcd.io) não existem neste cluster.' }),
  }))
  await openApp(page, engine)
  const panel = await openFlux(page)
  const empty = panel.getByTestId('flux-not-installed')
  await expect(empty).toContainText('Flux não encontrado')
  await expect(empty).toContainText('kustomize.toolkit.fluxcd.io')
  await expect(empty).toContainText('flux install')
})
