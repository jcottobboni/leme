// Shared helpers of the M7 GitOps and network specs (gateway-api, flux), against the isolated kind
// cluster ONLY. The CRDs come from scripts/gitops-crds.sh (SHA-256 pinned, created only if absent
// and never deleted: the cluster is shared); the fixtures live in their own m7-* namespaces and are
// left in place, restored to the fixture state, after each run.
import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import type { Page } from '@playwright/test'

export const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
export const KIND_CONTEXT = 'kind-leme-dev'
export const FIXTURES = path.join(__dirname, 'fixtures')

export function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
}

/** Installs the pinned Gateway API and Flux CRDs when one of them is missing. */
export function ensureGitopsCRDs() {
  try {
    kubectl('get', 'crd', 'tcproutes.gateway.networking.k8s.io', 'gitrepositories.source.toolkit.fluxcd.io', 'kustomizations.kustomize.toolkit.fluxcd.io', 'helmreleases.helm.toolkit.fluxcd.io')
  } catch {
    execFileSync('bash', [path.join(__dirname, '..', '..', 'scripts', 'gitops-crds.sh')], {
      stdio: 'inherit', env: { ...process.env, DEV_KUBECONFIG: KIND_KUBECONFIG },
    })
  }
}

/** Writes the status of each object of a fixture's status.json (there is no controller). */
export function patchStatus(file: string) {
  const all = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<string, unknown>
  for (const [key, patch] of Object.entries(all)) {
    if (key.startsWith('_')) continue
    const [resource, ns, name] = key.split('/')
    kubectl(...(ns ? ['-n', ns] : []), 'patch', resource, name, '--subresource=status', '--type=merge', '-p', JSON.stringify(patch))
  }
}

/** Recreates every object of a fixture file (except namespaces) and writes its status. */
export function recreate(dir: string, kinds: string[], ns: string) {
  kubectl('apply', '-f', path.join(dir, 'objects.yaml'))
  // Back to the fixture state: drop what a previous test wrote (annotations, suspend) by recreating.
  kubectl('-n', ns, 'delete', kinds.join(','), '--all', '--ignore-not-found', '--wait=true')
  kubectl('apply', '-f', path.join(dir, 'objects.yaml'))
  patchStatus(path.join(dir, 'status.json'))
}

export interface Engine { proc: ChildProcess; base: string; token: string }

/** Starts the engine on a random port with the kind kubeconfig and a throwaway config dir. */
export async function startEngine(tag: string): Promise<Engine> {
  const token = `e2e-${tag}-` + Math.random().toString(36).slice(2)
  const config = fs.mkdtempSync(path.join(os.tmpdir(), `leme-e2e-${tag}-`))
  const proc = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: token, LEME_CONFIG_DIR: config },
  })
  const base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: proc.stdout! }).on('line', (l) => {
      try {
        const m = JSON.parse(l)
        if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
      } catch {
        /* not a JSON line */
      }
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  return { proc, base, token }
}

export async function openApp(page: Page, e: Engine) {
  await page.goto(`${e.base}/#token=${e.token}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

export async function engineCall(page: Page, e: Engine, method: string, url: string, body?: unknown) {
  return page.evaluate(async ({ base, token, method, url, body }) => {
    const r = await fetch(base + url, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
    return { status: r.status, body: await r.json().catch(() => null) }
  }, { base: e.base, token: e.token, method, url, body })
}

export async function clusterId(page: Page, e: Engine) {
  const r = await engineCall(page, e, 'GET', '/api/clusters')
  return (r.body.clusters as { id: string; name: string }[]).find((c) => c.name === KIND_CONTEXT)!.id
}
