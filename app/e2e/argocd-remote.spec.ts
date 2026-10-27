// M5 Argo CD, remote destination: a sync that the Argo CD controller would run on another cluster
// is not covered by the connected cluster's read-only lock or production confirmation, so the UI
// asks for the Application name. Isolated kind cluster ONLY; every object is named m5-argo-review-*
// and only those are deleted. The Argo CD CRDs are created only when absent and never deleted.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-argo-remote-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-argo-remote-'))
const NS = 'm5-argo-review'
const APP = 'm5-argo-review-remote'
let engine: ChildProcess
let base = ''

function kubectl(input: string | undefined, ...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', input, stdio: ['pipe', 'pipe', 'pipe'] })
}

const objects = `
apiVersion: v1
kind: Namespace
metadata: {name: ${NS}, labels: {leme.dev/fixture: m5-argo-review}}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: ${APP}, namespace: ${NS}}
spec:
  project: default
  source: {repoURL: 'https://github.com/argoproj/argocd-example-apps.git', path: guestbook, targetRevision: HEAD}
  destination: {server: 'https://m5-argo-review-remote.example.invalid:6443', namespace: guestbook}
status:
  sync: {status: OutOfSync, revision: 53e28ff20cc530b9ada2173fbbd64d48338583ba}
  health: {status: Healthy}
`

test.beforeAll(async () => {
  try {
    kubectl(undefined, 'create', '-f', path.join(__dirname, 'fixtures', 'argocd', 'crds.yaml'))
  } catch (e) {
    if (!/AlreadyExists|already exists/.test(String((e as { stderr?: string }).stderr ?? e))) throw e
  }
  kubectl(undefined, 'wait', '--for=condition=Established', '--timeout=60s', 'crd/applications.argoproj.io', 'crd/appprojects.argoproj.io')
  kubectl(undefined, '-n', NS, 'delete', 'application.argoproj.io', APP, '--ignore-not-found', '--wait=true')
  kubectl(objects, 'apply', '-f', '-')
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

test.afterAll(() => {
  engine?.kill('SIGTERM')
  try {
    kubectl(undefined, 'delete', 'namespace', NS, '--ignore-not-found', '--wait=false')
  } catch {
    /* best effort */
  }
})

async function openArgo(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
  await page.getByTestId('tool-argocd').click()
  const panel = page.getByTestId('argocd-panel').filter({ visible: true })
  await expect(panel).toBeVisible()
  return panel
}

test('Sync com destino remoto avisa e exige digitar o nome da Application', async ({ page }) => {
  const panel = await openArgo(page)
  await panel.getByTestId(`argo-app-${APP}`).click()
  const detail = panel.getByTestId('argo-detail')
  await expect(detail).toContainText('m5-argo-review-remote.example.invalid')
  await detail.getByTestId('argo-sync-btn').click()
  const review = page.getByTestId('argo-proposal')
  await expect(review.getByTestId('argo-notes')).toContainText('Destino remoto')
  await page.getByTestId('argo-apply').click()
  const confirm = page.getByRole('dialog', { name: 'Confirmar pedido ao Argo CD' })
  await expect(confirm.getByTestId('argo-confirm-remote')).toContainText('não é o cluster conectado')
  const apply = confirm.getByRole('button', { name: 'Aplicar' })
  await expect(apply).toBeDisabled()
  await confirm.getByTestId('argo-confirm-name').fill('outro-nome')
  await expect(apply).toBeDisabled()
  await confirm.getByTestId('argo-confirm-name').fill(APP)
  await apply.click()
  await expect(review).toHaveCount(0)
  await expect.poll(() => kubectl(undefined, '-n', NS, 'get', 'application.argoproj.io', APP, '-o', 'jsonpath={.operation.initiatedBy.username}')).toBe('leme')
})
