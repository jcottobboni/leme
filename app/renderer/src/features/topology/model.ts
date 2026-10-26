import type { Graph, GraphNode } from '../../api'

export const CARD_W = 236
export const CARD_H = 68
const SQ_COLS = 17
export const MAX_SQUARES = 51

export type Health = 'ok' | 'warn' | 'error' | 'neutral'
export type PodStats = Record<Health, number>

const WORKLOADS = new Set(['Deployment', 'StatefulSet', 'DaemonSet', 'DeploymentConfig', 'Job', 'CronJob', 'ReplicaSet', 'ReplicationController'])

/** Pods per health owned (transitively) by workloads and aggregates; nodes without pods are absent. */
export function podStats(g: Graph): Map<string, PodStats> {
  const owns = new Map<string, string[]>()
  const owned = new Set<string>()
  for (const e of g.edges) {
    if (e.rel !== 'owns') continue
    ;(owns.get(e.from) ?? owns.set(e.from, []).get(e.from)!).push(e.to)
    owned.add(e.to)
  }
  const byId = new Map(g.nodes.map((n) => [n.id, n]))
  const memo = new Map<string, PodStats>()
  const empty = (): PodStats => ({ ok: 0, warn: 0, error: 0, neutral: 0 })
  const walk = (id: string, seen: Set<string>): PodStats => {
    const hit = memo.get(id)
    if (hit) return hit
    const n = byId.get(id)
    const s = empty()
    if (!n || seen.has(id)) return s
    seen.add(id)
    if (n.kind === 'Pod') {
      if (n.count && n.pods) for (const h of Object.keys(n.pods)) s[(h in s ? h : 'neutral') as Health] += n.pods[h]
      else s[n.health as Health] += 1
    } else {
      for (const c of owns.get(id) ?? []) {
        const cs = walk(c, seen)
        for (const h of Object.keys(s) as Health[]) s[h] += cs[h]
      }
    }
    memo.set(id, s)
    return s
  }
  const out = new Map<string, PodStats>()
  for (const n of g.nodes) {
    if (n.count && n.pods) {
      out.set(n.id, walk(n.id, new Set()))
    } else if (WORKLOADS.has(n.kind) && !owned.has(n.id)) {
      const s = walk(n.id, new Set())
      if (s.ok + s.warn + s.error + s.neutral > 0) out.set(n.id, s)
    }
  }
  return out
}

export const total = (s: PodStats) => s.ok + s.warn + s.error + s.neutral

export function nodeSize(n: GraphNode, stats?: PodStats) {
  if (!stats) return { w: CARD_W, h: CARD_H }
  const rows = Math.ceil(Math.min(total(stats), MAX_SQUARES) / SQ_COLS)
  return { w: CARD_W, h: CARD_H + 8 + rows * 12 }
}

/** Squares to draw: worst first so problems stay visible when capped. */
export function squares(s: PodStats): { items: Health[]; more: number } {
  const all: Health[] = [...Array(s.error).fill('error'), ...Array(s.warn).fill('warn'), ...Array(s.ok).fill('ok'), ...Array(s.neutral).fill('neutral')]
  return { items: all.slice(0, MAX_SQUARES), more: Math.max(0, all.length - MAX_SQUARES) }
}

export type RelClass = 'route' | 'own' | 'cfg' | 'sched'

const REL_CLASS: Record<string, RelClass> = {
  routes: 'route', selects: 'route', tls: 'route', attaches: 'route',
  owns: 'own', scales: 'own', triggers: 'own', produces: 'own',
  mounts: 'cfg', env: 'cfg', pulls: 'cfg', uses: 'cfg', bound: 'cfg', class: 'cfg', grants: 'cfg',
  'runs on': 'sched',
}
export const relClass = (rel: string): RelClass => REL_CLASS[rel] ?? 'own'

// Concrete colors (markers cannot use CSS variables reliably); they mirror the design tokens.
export const EDGE_COLORS: Record<'dark' | 'light', Record<RelClass, string>> = {
  dark: { route: '#4aa3ff', own: '#6b7684', cfg: '#a78bfa', sched: '#6b7684' },
  light: { route: '#1f6feb', own: '#8a94a0', cfg: '#7c4dff', sched: '#8a94a0' },
}
export const HEALTH_COLORS: Record<'dark' | 'light', Record<Health, string>> = {
  dark: { ok: '#2fbf71', warn: '#e3b341', error: '#f0524f', neutral: '#6b7684' },
  light: { ok: '#1a9d55', warn: '#c48a00', error: '#d1302d', neutral: '#8a94a0' },
}

export const OTHER = '\u0000outros'
export const regionId = (app: string) => `region:${app}`

export function hueOf(app: string): number {
  let h = 0
  for (let i = 0; i < app.length; i++) h = (h * 31 + app.charCodeAt(i)) >>> 0
  return h % 360
}

export function distinctApps(g: Graph): number {
  return new Set(g.nodes.map((n) => n.app).filter(Boolean)).size
}
