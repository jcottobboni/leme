import { podTotal, type PodPhases, type TopoNode } from './api'

export const NODE_W = 110
export const NODE_H = 120
const GAP = 70
const PAD = 26
const LABEL_SPACE = 44 // group name pill below each box
const BLOCK_GAP = 66

/** A box on the canvas: an application group, a Helm release or a workload on its own. */
export interface Block {
  key: string
  kind: 'app' | 'release' | 'solo'
  name: string
  nodes: TopoNode[]
}

export interface Placed {
  blocks: { block: Block; x: number; y: number; w: number; h: number; collapsed: boolean }[]
  nodes: { node: TopoNode; parent: string; x: number; y: number }[] // x/y relative to the parent box
}

/** Groups workloads as the console does: part-of first, then Helm release, then alone. */
export function blocksOf(nodes: TopoNode[]): Block[] {
  const apps = new Map<string, TopoNode[]>()
  const releases = new Map<string, TopoNode[]>()
  const solo: Block[] = []
  for (const n of nodes) {
    if (n.app) apps.set(n.app, [...(apps.get(n.app) ?? []), n])
    else if (n.release) releases.set(n.release, [...(releases.get(n.release) ?? []), n])
    else solo.push({ key: `solo:${n.id}`, kind: 'solo', name: n.name, nodes: [n] })
  }
  const named = (m: Map<string, TopoNode[]>, kind: 'app' | 'release') =>
    [...m.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, ns]) => ({ key: `${kind}:${name}`, kind, name, nodes: ns }))
  return [...named(apps, 'app'), ...named(releases, 'release'), ...solo]
}

function boxSize(b: Block, collapsed: boolean) {
  if (b.kind === 'solo') return { w: NODE_W, h: NODE_H, cols: 1 }
  if (collapsed) return { w: NODE_W + PAD * 2, h: NODE_H + PAD * 2, cols: 1 }
  const cols = Math.ceil(Math.sqrt(b.nodes.length))
  const rows = Math.ceil(b.nodes.length / cols)
  return { w: cols * NODE_W + (cols - 1) * GAP + PAD * 2, h: rows * NODE_H + (rows - 1) * GAP + PAD * 2, cols }
}

/** Shelf packing of the boxes, rows about as wide as the content is tall. */
export function place(blocks: Block[], collapsed: (b: Block) => boolean): Placed {
  const sized = blocks.map((b) => ({ b, c: collapsed(b), ...boxSize(b, collapsed(b)) }))
  const area = sized.reduce((a, s) => a + (s.w + BLOCK_GAP) * (s.h + LABEL_SPACE), 0)
  const rowW = Math.max(700, Math.sqrt(area) * 1.5)
  const out: Placed = { blocks: [], nodes: [] }
  let x = 0, y = 0, rowH = 0
  for (const s of sized) {
    if (x > 0 && x + s.w > rowW) {
      x = 0
      y += rowH + BLOCK_GAP
      rowH = 0
    }
    out.blocks.push({ block: s.b, x, y, w: s.w, h: s.h, collapsed: s.c })
    if (!s.c) {
      s.b.nodes.forEach((n, i) => {
        const pad = s.b.kind === 'solo' ? 0 : PAD
        out.nodes.push({ node: n, parent: s.b.key, x: pad + (i % s.cols) * (NODE_W + GAP), y: pad + Math.floor(i / s.cols) * (NODE_H + GAP) })
      })
    }
    x += s.w + BLOCK_GAP
    rowH = Math.max(rowH, s.h + (s.b.kind === 'solo' ? 0 : LABEL_SPACE))
  }
  return out
}

/** Pod phases of a collapsed group: the sum of its workloads. */
export function sumPhases(nodes: TopoNode[]): PodPhases {
  const out = { running: 0, notReady: 0, pending: 0, failed: 0, succeeded: 0, terminating: 0, unknown: 0 }
  for (const n of nodes) for (const k of Object.keys(out) as (keyof PodPhases)[]) out[k] += n.phases[k] ?? 0
  return out
}

export const hasPods = (n: TopoNode) => podTotal(n.phases) > 0

// Name tokens that mark a helper of an application rather than the application itself
// (tcmgo-web-almoxarifado-redis-hmg serves tcmgo-web-almoxarifado-hmg).
const ROLE_TOKENS = new Set([
  'redis', 'sidekiq', 'worker', 'workers', 'migration', 'migrations', 'migrate', 'cron', 'cronjob', 'job', 'jobs', 'scheduler',
  'celery', 'beat', 'queue', 'consumer', 'cache', 'memcached', 'db', 'database', 'postgres', 'postgresql', 'mysql', 'mariadb',
  'mongo', 'mongodb', 'rabbitmq', 'kafka', 'elasticsearch', 'solr', 'clock', 'nginx', 'proxy',
])
const MAIN_KINDS = ['Deployment', 'DeploymentConfig', 'StatefulSet', 'DaemonSet']

/** An application as the user thinks of it: the main deployment plus the workloads that serve it. */
export interface Family {
  key: string
  main: TopoNode
  members: TopoNode[]
}

/**
 * Groups workloads into applications. app.kubernetes.io/part-of wins (as in the OpenShift console);
 * otherwise the name without role tokens is the key, so -redis, -sidekiq and -migration companions
 * join their main deployment. The main is the member whose name has no role token (a Deployment first).
 */
export function familiesOf(nodes: TopoNode[]): Family[] {
  const by = new Map<string, TopoNode[]>()
  for (const n of nodes) {
    const key = n.app ? `app:${n.app}` : n.name.split('-').filter((t) => !ROLE_TOKENS.has(t.toLowerCase())).join('-') || n.name
    by.set(key, [...(by.get(key) ?? []), n])
  }
  const rank = (n: TopoNode) => {
    const kind = MAIN_KINDS.indexOf(n.kind)
    const roles = n.name.split('-').some((t) => ROLE_TOKENS.has(t.toLowerCase()))
    return (kind < 0 ? 10 : kind) + (roles ? 100 : 0)
  }
  const out: Family[] = []
  for (const [key, members] of by) {
    const sorted = [...members].sort((a, b) => rank(a) - rank(b) || a.name.length - b.name.length || a.name.localeCompare(b.name))
    if (!MAIN_KINDS.includes(sorted[0].kind)) continue // only Jobs, CronJobs or pods: no application to pick
    out.push({ key, main: sorted[0], members: [...members].sort((a, b) => (a === sorted[0] ? -1 : b === sorted[0] ? 1 : a.name.localeCompare(b.name))) })
  }
  return out.sort((a, b) => a.main.name.localeCompare(b.main.name))
}
