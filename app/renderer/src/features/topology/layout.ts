import ELK from 'elkjs/lib/elk-api.js'
import type { ElkNode } from 'elkjs/lib/elk-api.js'
import elkWorkerUrl from 'elkjs/lib/elk-worker.min.js?url'
import type { Graph } from '../../api'
import { CARD_H, CARD_W, OTHER, regionId } from './model'

// Legacy compact size, still used by the network-policy graph.
export const NODE_W = 210
export const NODE_H = 52

// One worker for the whole app: layout never blocks the UI thread.
let elk: InstanceType<typeof ELK> | undefined
function engine() {
  elk ??= new ELK({ workerFactory: () => new Worker(elkWorkerUrl) })
  return elk
}

export type Dir = 'RIGHT' | 'DOWN'
export interface Region { id: string; app: string; x: number; y: number; w: number; h: number; count: number }
export interface Placed { x: number; y: number; parent?: string }
export interface LayoutResult { pos: Map<string, Placed>; regions: Region[] }

/** ELK layered layout. With `group`, apps become regions (hierarchical nodes laid out together with their edges). */
export async function layoutGraph(g: Graph, opts: { dir: Dir; group: boolean; sizes: Map<string, { w: number; h: number }> }): Promise<LayoutResult> {
  const size = (id: string) => opts.sizes.get(id) ?? { w: CARD_W, h: CARD_H }
  const leaf = (id: string): ElkNode => ({ id, width: size(id).w, height: size(id).h })
  const apps = new Map<string, string[]>()
  if (opts.group) for (const n of g.nodes) (apps.get(n.app || OTHER) ?? apps.set(n.app || OTHER, []).get(n.app || OTHER)!).push(n.id)
  const grouped = opts.group && apps.size > 0 && !(apps.size === 1 && apps.has(OTHER))
  const base: Record<string, string> = {
    'elk.algorithm': 'layered',
    'elk.direction': opts.dir,
    'elk.layered.spacing.nodeNodeBetweenLayers': opts.dir === 'RIGHT' ? '70' : '46',
    'elk.spacing.nodeNode': opts.dir === 'RIGHT' ? '22' : '28',
    'elk.layered.nodePlacement.strategy': 'BRANDES_KOEPF',
    'elk.layered.crossingMinimization.strategy': 'LAYER_SWEEP',
  }
  const graph: ElkNode = {
    id: 'root',
    layoutOptions: grouped
      ? { ...base, 'elk.hierarchyHandling': 'INCLUDE_CHILDREN', 'elk.spacing.nodeNode': '40', 'elk.padding': '[top=20,left=20,bottom=20,right=20]' }
      : { ...base, 'elk.separateConnectedComponents': 'true', 'elk.spacing.componentComponent': '40' },
    children: grouped
      ? [...apps].sort(([a], [b]) => (a === OTHER ? 1 : b === OTHER ? -1 : a.localeCompare(b))).map(([app, ids]) => ({
          id: regionId(app),
          layoutOptions: { 'elk.padding': '[top=44,left=22,bottom=22,right=22]', 'elk.algorithm': 'layered', 'elk.direction': opts.dir },
          children: ids.map(leaf),
        }))
      : g.nodes.map((n) => leaf(n.id)),
    edges: g.edges.map((e, i) => ({ id: `e${i}`, sources: [e.from], targets: [e.to] })),
  }
  const res = await engine().layout(graph)
  const pos = new Map<string, Placed>()
  const regions: Region[] = []
  for (const c of res.children ?? []) {
    if (!grouped) {
      pos.set(c.id, { x: c.x ?? 0, y: c.y ?? 0 })
      continue
    }
    regions.push({ id: c.id, app: c.id.slice('region:'.length), x: c.x ?? 0, y: c.y ?? 0, w: c.width ?? 0, h: c.height ?? 0, count: c.children?.length ?? 0 })
    for (const k of c.children ?? []) pos.set(k.id, { x: k.x ?? 0, y: k.y ?? 0, parent: c.id })
  }
  return { pos, regions }
}

/** Plain left-to-right layout (compact nodes) for simple graphs. */
export async function layout(g: Graph): Promise<Map<string, { x: number; y: number }>> {
  const sizes = new Map(g.nodes.map((n) => [n.id, { w: NODE_W, h: NODE_H }]))
  return (await layoutGraph(g, { dir: 'RIGHT', group: false, sizes })).pos
}
