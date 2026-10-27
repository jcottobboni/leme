// M7 RF02 / CA02: Gateway API as a first-class kind, against the isolated kind cluster ONLY.
// CRDs from scripts/gitops-crds.sh (SHA-256 pinned, never deleted); fixtures in the m7-gateway and
// m7-gateway-backend namespaces (app/e2e/fixtures/gateway), left in place; no Gateway controller,
// the status is written with kubectl patch --subresource=status.
import { test, expect } from '@playwright/test'
import path from 'node:path'
import { FIXTURES, clusterId, engineCall, ensureGitopsCRDs, kubectl, openApp, patchStatus, startEngine, type Engine } from './gitops'

const FIX = path.join(FIXTURES, 'gateway')
const NS = 'm7-gateway'
let engine: Engine

test.beforeAll(async () => {
  ensureGitopsCRDs()
  kubectl('apply', '-f', path.join(FIX, 'objects.yaml'))
  patchStatus(path.join(FIX, 'status.json'))
  kubectl('-n', NS, 'rollout', 'status', 'deploy/m7-web', '--timeout=120s')
  engine = await startEngine('gateway')
})

test.afterAll(() => {
  engine?.proc.kill('SIGTERM')
})

test('CA02: Gateway API na navegação, com colunas e estado vindos das condições', async ({ page }) => {
  await openApp(page, engine)
  // Network section: the Gateway API kinds are there; Istio's Gateway stays with the custom resources.
  await page.getByTestId('nav-kind-HTTPRoute').click()
  const grid = page.locator('.grid-panel').filter({ visible: true })
  await grid.getByTestId('row-m7-web').waitFor()
  await expect(grid.getByTestId('row-m7-web')).toContainText('web.m7.example')
  await expect(grid.getByTestId('row-m7-web')).toContainText('m7-gw:http')
  await expect(grid.getByTestId('row-m7-web')).toContainText('m7-web:80')
  await expect(grid.getByTestId('row-m7-web')).toContainText('Aceita')
  await expect(grid.getByTestId('row-m7-broken')).toContainText('Refs não resolvidas')

  await page.getByTestId('nav-kind-Gateway').click()
  const gw = page.locator('.grid-panel').filter({ visible: true }).getByTestId('row-m7-gw')
  await expect(gw).toContainText('m7-gateway-class')
  await expect(gw).toContainText('http:80/HTTP')
  await expect(gw).toContainText('172.18.0.50')
  await expect(gw).toContainText('Programado')
})

test('CA02: Topologia mostra Gateway → HTTPRoute → Service → pods', async ({ page }) => {
  await openApp(page, engine)
  const id = await clusterId(page, engine)
  const g = await engineCall(page, engine, 'GET', `/api/clusters/${id}/graph?namespace=${NS}`)
  expect(g.status).toBe(200)
  const edges = (g.body.edges as { from: string; to: string; rel: string }[]).map((e) => `${e.from} -${e.rel}-> ${e.to}`)
  expect(edges).toEqual(expect.arrayContaining([
    `Gateway/${NS}/m7-gw -attaches-> HTTPRoute/${NS}/m7-web`,
    `HTTPRoute/${NS}/m7-web -routes-> Service/${NS}/m7-web`,
    `Gateway/${NS}/m7-gw -attaches-> TCPRoute/${NS}/m7-db`,
    `TCPRoute/${NS}/m7-db -routes-> Service/m7-gateway-backend/m7-db`,
    `Gateway/${NS}/m7-gw -class-> GatewayClass//m7-gateway-class`,
  ]))
  expect(edges.some((e) => e.startsWith(`Service/${NS}/m7-web -selects-> Pod/${NS}/m7-web-`))).toBe(true)
  const missing = (g.body.nodes as { id: string; missing?: boolean }[]).find((n) => n.id === `Service/${NS}/m7-missing`)
  expect(missing?.missing).toBe(true)

  // The same chain on the canvas.
  await page.getByTestId('tool-topology').click()
  await page.locator('.ns-select:visible').first().selectOption(NS)
  await expect(page.getByTestId(`topo-Gateway/${NS}/m7-gw`).first()).toBeVisible()
  await expect(page.getByTestId(`topo-HTTPRoute/${NS}/m7-web`).first()).toBeVisible()
  await expect(page.getByTestId(`topo-Service/${NS}/m7-web`).first()).toBeVisible()
  await expect(page.getByTestId(`topo-Service/${NS}/m7-missing`).first()).toContainText('Ausente')
})

test('CA02: rota com backend inexistente e backend entre namespaces sem ReferenceGrant geram diagnóstico', async ({ page }) => {
  await openApp(page, engine)
  const id = await clusterId(page, engine)
  const d = await engineCall(page, engine, 'GET', `/api/clusters/${id}/diagnose?namespace=${NS}`)
  expect(d.status).toBe(200)
  const found = (d.body.findings as { id: string; object: { kind: string; name: string } }[]).map((f) => `${f.id} ${f.object.kind}/${f.object.name}`)
  expect(found).toEqual(expect.arrayContaining([
    'gwroute-missing-backend HTTPRoute/m7-broken',
    'gwroute-refs-unresolved HTTPRoute/m7-broken',
    'gwroute-no-referencegrant TCPRoute/m7-db',
  ]))
  expect(found.filter((f) => f.includes('/m7-web') && f.includes('Route'))).toEqual([])

  // In the object's Diagnóstico tab.
  await page.getByTestId('nav-kind-HTTPRoute').click()
  await page.locator('.grid-panel').filter({ visible: true }).getByTestId('row-m7-broken').click()
  await page.getByTestId('tab-diagnosis').click()
  const diag = page.getByTestId('diagnosis')
  await expect(diag.getByTestId('finding-gwroute-missing-backend').first()).toContainText('Service inexistente')
  await expect(diag.getByTestId('finding-gwroute-missing-backend').first()).toContainText('m7-missing')
})
