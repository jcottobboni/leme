// A CRD reusing a built-in kind name (like Longhorn's nodes.longhorn.io, kind Node) must not show up as a
// second "Nodes" in the Cluster section nor get the built-in Node columns. Kind cluster ONLY.
import { test, expect } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

const kubectl = (args: string[], input?: string) =>
  execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', input })

const CRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: nodes.clash.leme.dev}
spec:
  group: clash.leme.dev
  scope: Namespaced
  names: {plural: nodes, singular: node, kind: Node, listKind: NodeList}
  versions:
  - name: v1
    served: true
    storage: true
    schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}
`

test.beforeAll(async () => {
  kubectl(['apply', '-f', '-'], CRD)
  kubectl(['wait', '--for=condition=Established', 'crd/nodes.clash.leme.dev', '--timeout=60s'])
  kubectl(['apply', '-f', '-'], 'apiVersion: clash.leme.dev/v1\nkind: Node\nmetadata: {name: only-one, namespace: demo}\n')
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN },
  })
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
  try {
    kubectl(['delete', 'crd', 'nodes.clash.leme.dev', '--wait=false'])
  } catch {
    /* best effort */
  }
})

test('CRD com kind Node fica em Custom Resources e os Nodes nativos continuam únicos', async ({ page }) => {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
  // Before the fix the CRD showed up as a second "Nodes" in the Cluster section.
  await expect(page.locator('.nav-crd-head', { hasText: 'clash.leme.dev' })).toHaveCount(1)
  await expect(page.getByTestId('nav-kind-Node')).toHaveCount(1)
  await page.locator('.nav-crd-head', { hasText: 'clash.leme.dev' }).click()
  // CRD items carry their group in the testid, so they never collide with the built-in one.
  await expect(page.locator('.nav-crd', { hasText: 'clash.leme.dev' }).getByTestId('nav-kind-Node.clash.leme.dev')).toHaveCount(1)
  await expect(page.getByTestId('nav-kind-Node')).toHaveCount(1)
  // The built-in Nodes grid keeps its columns and lists the real node.
  await page.getByTestId('nav-kind-Node').click()
  await expect(page.getByTestId('row-leme-dev-control-plane')).toBeVisible()
  await expect(page.locator('.grid-head, thead').first()).toContainText('Papéis')
})
