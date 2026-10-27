// Istio panel (M5, feature istio) against the isolated kind cluster ONLY. No Istio controller is
// installed: minimal CRDs (created only when absent, never deleted) plus m5-istio-* fixtures.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createHash } from 'node:crypto'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const ROOT = path.join(__dirname, '..', '..')
const ENGINE = path.join(ROOT, 'engine', 'bin', 'leme')
const STATIC = path.join(__dirname, '..', 'renderer-dist')
const FIXTURES = path.join(__dirname, 'fixtures', 'istio')
const TOKEN = 'e2e-istio-' + Math.random().toString(36).slice(2)
const NAMESPACES = ['m5-istio-strict', 'm5-istio-permissive']

// istioctl for the analyze test: the one on PATH, or a pinned official release verified by SHA-256
// and unpacked into a temp dir (never installed on the machine or the cluster).
// LEME_E2E_NO_DOWNLOAD=1 skips the download.
const ISTIOCTL_VERSION = '1.31.1'
const ISTIOCTL_SHA256: Record<string, string> = {
  'linux-amd64': 'a44563904f22f2a8bf6ac4fff1b0bad9f718587c2be6aa807b83fe9049910639',
  'linux-arm64': '9b27a99b2eaa41991ff11f0a5790c604144c3813d0ea21e5c5b84a0ede64b062',
  'osx-amd64': 'e3205a241150eab2058d72564b1fab70dac3f881edf172b8594158db61c5926a',
  'osx-arm64': '3f37edda91e51a5948d688681c9db64971ee3abb38cda65d797dec51f46fecd1',
}

let engine: ChildProcess
let base = ''
let tmp = ''
let istioctlDir = ''

function kubectl(args: string[], input?: string) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', input, timeout: 120_000 })
}

function onPath(name: string) {
  return (process.env.PATH ?? '').split(path.delimiter).some((d) => d && fs.existsSync(path.join(d, name)))
}

async function fetchIstioctl(): Promise<string> {
  if (onPath('istioctl')) return ''
  if (process.env.LEME_E2E_NO_DOWNLOAD) return ''
  const plat = `${process.platform === 'darwin' ? 'osx' : process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`
  const sum = ISTIOCTL_SHA256[plat]
  if (!sum) return ''
  const dir = path.join(os.tmpdir(), `leme-e2e-istioctl-${ISTIOCTL_VERSION}`)
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 })
  const tgz = path.join(dir, `istioctl-${ISTIOCTL_VERSION}-${plat}.tar.gz`)
  const verified = () => fs.existsSync(tgz) && createHash('sha256').update(fs.readFileSync(tgz)).digest('hex') === sum
  if (!verified()) {
    const res = await fetch(`https://github.com/istio/istio/releases/download/${ISTIOCTL_VERSION}/istioctl-${ISTIOCTL_VERSION}-${plat}.tar.gz`)
    if (!res.ok) return ''
    fs.writeFileSync(tgz, Buffer.from(await res.arrayBuffer()), { mode: 0o600 })
    if (!verified()) {
      fs.rmSync(tgz, { force: true })
      throw new Error('istioctl: SHA-256 não confere com o valor fixado')
    }
  }
  const bin = path.join(dir, 'bin')
  fs.rmSync(bin, { recursive: true, force: true })
  fs.mkdirSync(bin, { mode: 0o700 })
  execFileSync('tar', ['-xzf', tgz, '-C', bin, 'istioctl'])
  return bin
}

/** Creates each CRD only when absent; never updates or deletes an existing one. */
function ensureCRDs() {
  const docs = fs.readFileSync(path.join(FIXTURES, 'crds.yaml'), 'utf8').split(/^---$/m)
  const names: string[] = []
  for (const doc of docs) {
    const name = /^\s{2}name:\s*(\S+)/m.exec(doc)?.[1]
    if (!name) continue
    names.push(name)
    try {
      kubectl(['get', 'crd', name, '-o', 'name'])
    } catch {
      kubectl(['create', '-f', '-'], doc)
    }
  }
  kubectl(['wait', '--for', 'condition=Established', '--timeout=60s', ...names.map((n) => `crd/${n}`)])
}

test.beforeAll(async () => {
  test.setTimeout(240_000)
  ensureCRDs()
  // A previous run may still be terminating its namespaces.
  try {
    kubectl(['wait', '--for=delete', '--timeout=120s', ...NAMESPACES.map((n) => `namespace/${n}`)])
  } catch {
    /* not found: nothing to wait for */
  }
  // A fresh CRD can take a moment to be served even after Established.
  for (let i = 0; ; i++) {
    try {
      kubectl(['apply', '-f', path.join(FIXTURES, 'objects.yaml')])
      break
    } catch (e) {
      if (i > 10) throw e
      await new Promise((r) => setTimeout(r, 1000))
    }
  }
  try {
    istioctlDir = await fetchIstioctl()
  } catch (e) {
    console.warn(`istioctl indisponível: ${(e as Error).message}`)
  }
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-istio-'))
  const PATH = [istioctlDir, process.env.PATH].filter(Boolean).join(path.delimiter)
  const env = { ...process.env, PATH, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: path.join(tmp, 'config'), HELM_CONFIG_HOME: path.join(tmp, 'helm') }
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', STATIC], { env })
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
  // Only the m5-istio-* namespaces this spec created; the CRDs stay (other specs may use them).
  if (!process.env.LEME_E2E_KEEP) {
    try {
      kubectl(['delete', 'namespace', ...NAMESPACES, '--ignore-not-found', '--wait=false'])
    } catch {
      /* best effort */
    }
  }
  if (tmp) fs.rmSync(tmp, { recursive: true, force: true })
})

async function openIstioPanel(page: Page, namespace?: string) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-istio').click()
  const panel = page.getByTestId('istio-panel')
  await expect(panel).toBeVisible()
  if (namespace) await panel.locator('.ns-select').selectOption(namespace)
  return panel
}

test('CA05: mTLS por namespace e rotas 80/20 do VirtualService de fixture', async ({ page }) => {
  const panel = await openIstioPanel(page)
  const strict = panel.getByTestId('ist-ns-m5-istio-strict')
  await expect(strict).toContainText('STRICT')
  await expect(strict).toContainText('política do namespace')
  await expect(strict).toContainText('sidecar habilitado')
  await expect(strict).toContainText('m5-istio-strict/m5-istio-reviews')
  await expect(strict).toContainText('porta 9080: PERMISSIVE')
  const permissive = panel.getByTestId('ist-ns-m5-istio-permissive')
  await expect(permissive).toContainText('PERMISSIVE')
  await expect(permissive).toContainText('m5-istio-permissive/default')

  await panel.locator('.ns-select').selectOption('m5-istio-strict')
  await expect(panel.getByTestId('ist-ns-m5-istio-permissive')).toHaveCount(0)
  await panel.getByRole('button', { name: 'Rotas', exact: true }).click()
  const vs = panel.getByTestId('ist-vs-m5-istio-strict-m5-istio-reviews')
  await expect(vs).toBeVisible()
  await expect(vs).toContainText('uri prefix /reviews')
  await expect(vs.getByTestId('ist-dest-v1')).toContainText('80%')
  await expect(vs.getByTestId('ist-dest-v2')).toContainText('20%')
  const segs = vs.locator('.ist-bar .ist-seg')
  await expect(segs).toHaveCount(2)
  expect(await segs.evaluateAll((els) => els.map((e) => (e as HTMLElement).style.flexGrow))).toEqual(['80', '20'])
  const w = await segs.evaluateAll((els) => els.map((e) => e.getBoundingClientRect().width))
  expect(w[0] / (w[0] + w[1])).toBeGreaterThan(0.75)
  expect(w[0] / (w[0] + w[1])).toBeLessThan(0.85)

  await panel.getByRole('button', { name: 'DestinationRules', exact: true }).click()
  const dr = panel.getByTestId('ist-dr-m5-istio-strict-m5-istio-reviews')
  await expect(dr).toContainText('ISTIO_MUTUAL')
  await expect(dr).toContainText('version=v1')
  await expect(dr.getByRole('button', { name: 'm5-istio-reviews', exact: true })).toBeVisible()
  await panel.getByRole('button', { name: 'Gateways', exact: true }).click()
  await expect(panel.getByTestId('ist-gw-m5-istio-strict-m5-istio-gateway')).toContainText('HTTP :80')
  await expect(panel.getByTestId('ist-gw-m5-istio-strict-m5-istio-gateway')).toContainText('istio=ingressgateway')

  // The destination host links to its Service (opens a new tab, so it goes last).
  await panel.getByRole('button', { name: 'Rotas', exact: true }).click()
  await vs.getByTestId('ist-dest-v1').getByRole('button', { name: 'm5-istio-reviews' }).click()
  await expect(page.locator('.dv-tab', { hasText: 'm5-istio-reviews' }).first()).toBeVisible()
})

test('CA05 API: versões preferidas e destinos ligados ao Service', async () => {
  const clusters = await (await fetch(`${base}/api/clusters`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json()
  const id = clusters.clusters.find((c: { name: string }) => c.name === KIND_CONTEXT).id
  await fetch(`${base}/api/clusters/${id}/connect`, { method: 'POST', headers: { Authorization: `Bearer ${TOKEN}` } })
  const rep = await (await fetch(`${base}/api/clusters/${id}/istio?namespace=m5-istio-strict`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json()
  expect(rep.installed).toBe(true)
  expect(rep.kinds).toContain('security.istio.io/v1 PeerAuthentication')
  const vs = rep.virtualServices.find((v: { name: string }) => v.name === 'm5-istio-reviews')
  expect(vs.http[0].destinations.map((d: { subset: string; weight: number; service: { found: boolean } }) => [d.subset, d.weight, d.service.found])).toEqual([['v1', 80, true], ['v2', 20, true]])
  expect(rep.destinationRules[0].service).toEqual({ namespace: 'm5-istio-strict', name: 'm5-istio-reviews', found: true })
})

test('CA06: sem os CRDs do Istio o painel explica', async ({ page }) => {
  // The shared kind cluster keeps the CRDs once created, so the engine answer is simulated here; the
  // engine side (installed=false without the kinds) is covered by the Go tests.
  await page.route((u) => u.pathname.endsWith('/istio'), (route) =>
    route.fulfill({ json: { installed: false, kinds: [], rootNamespace: 'istio-system', namespaces: [], virtualServices: [], destinationRules: [], gateways: [] } }))
  const panel = await openIstioPanel(page)
  const box = panel.getByTestId('istio-not-installed')
  await expect(box).toBeVisible()
  await expect(box).toContainText('Istio não detectado')
  await expect(box).toContainText('networking.istio.io')
  await expect(panel.getByRole('button', { name: 'Rotas', exact: true })).toHaveCount(0)
})

test('Analyze sem istioctl mostra como instalar', async ({ page }) => {
  await page.route((u) => u.pathname === '/api/istio/tool', (route) => route.fulfill({ json: { installed: false, hint: 'Instale o istioctl' } }))
  const panel = await openIstioPanel(page)
  await panel.getByRole('button', { name: 'Analyze', exact: true }).click()
  await expect(panel.getByTestId('ist-install')).toContainText('istioctl não encontrado')
  await expect(panel.getByTestId('ist-install')).toContainText('~/.local/bin')
})

test('Analyze com istioctl (offline, isolado)', async ({ page }) => {
  test.skip(!istioctlDir && !onPath('istioctl'), 'istioctl indisponível')
  const panel = await openIstioPanel(page, 'm5-istio-strict')
  await panel.getByRole('button', { name: 'Analyze', exact: true }).click()
  await panel.getByTestId('ist-analyze-run').click()
  await expect(panel.getByTestId('ist-analyze-messages').or(panel.getByTestId('ist-analyze-empty'))).toBeVisible({ timeout: 120_000 })
  // The Gateway selects istio=ingressgateway pods that do not exist in the fixture.
  await expect(panel.getByTestId('ist-analyze-messages')).toContainText('IST')
  // Nothing of the export is left behind.
  expect(fs.readdirSync(os.tmpdir()).filter((d) => d.startsWith('leme-scan-istio-'))).toEqual([])
})
