// Observed-traffic overlay of the topology (M5, feature traffic). Edges come from the shared contract
// (features/map-webgl/trafficEdges.ts); endpoints missing from the current graph become "externo" nodes.
import { memo, useMemo } from 'react'
import {
  BaseEdge, EdgeLabelRenderer, Handle, MarkerType, Position, getBezierPath,
  type Edge, type EdgeProps, type Node, type NodeProps,
} from '@xyflow/react'
import { Globe } from 'lucide-react'
import type { GraphNode } from '../../api'
import { endpointKey, topologyId, useTrafficEdges, type TrafficEdge, type TrafficEndpoint, type TrafficSnapshot } from '../map-webgl/trafficEdges'
import { endpointLabel, fmtRate, sourceLabel } from '../traffic/api'
import { kindIcon } from '../shell/nav'
import type { Dir } from './layout'
import '../traffic/traffic.css'

/** Event that turns the overlay on in an already open topology (detail: clusterId). */
export const TRAFFIC_EVENT = 'leme:topology-traffic'

const WORKLOAD_KINDS = new Set(['Deployment', 'StatefulSet', 'DaemonSet', 'DeploymentConfig', 'ReplicaSet', 'Job', 'CronJob', 'Pod'])
const EXT_W = 200
const EXT_H = 52
const GAP = 140

type ExtData = { e: TrafficEndpoint; dir: Dir }
type TrfData = { label: string; testid: string; title: string }

export const TrafficExtNode = memo(function TrafficExtNode({ data }: NodeProps<Node<ExtData>>) {
  const { e, dir } = data
  const Icon = e.kind === 'External' ? Globe : kindIcon(e.kind)
  return (
    <div className="trf-ext" title={`${e.kind} ${endpointLabel(e)}\nFora do grafo atual`} data-testid={`traffic-ext-${e.name}`}>
      <Handle type="target" position={dir === 'RIGHT' ? Position.Left : Position.Top} />
      <span className="tc-ico"><Icon size={15} /></span>
      <span className="tc-txt">
        <span className="tc-name">{e.name}</span>
        <span className="tc-sub">externo · {e.kind === 'External' ? 'fora do cluster' : `${e.kind}${e.namespace ? ' · ' + e.namespace : ''}`}</span>
      </span>
      <Handle type="source" position={dir === 'RIGHT' ? Position.Right : Position.Bottom} />
    </div>
  )
})

export const TrafficEdgeView = memo(function TrafficEdgeView(p: EdgeProps<Edge<TrfData>>) {
  const [path, lx, ly] = getBezierPath({
    sourceX: p.sourceX, sourceY: p.sourceY, sourcePosition: p.sourcePosition, targetX: p.targetX, targetY: p.targetY, targetPosition: p.targetPosition,
  })
  return (
    <>
      <BaseEdge id={p.id} path={path} markerEnd={p.markerEnd} interactionWidth={14} />
      {p.data && (
        <EdgeLabelRenderer>
          <div className="trf-label nodrag nopan" style={{ transform: `translate(-50%, -50%) translate(${lx}px, ${ly}px)` }} data-testid={p.data.testid} title={p.data.title}>
            {p.data.label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
})

/** Absolute bounding box of the laid-out cards (children of regions are relative to their region). */
function bounds(nodes: Node[]) {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const n of nodes) {
    if (n.type !== 'kind') continue
    const parent = n.parentId ? byId.get(n.parentId) : undefined
    const x = n.position.x + (parent?.position.x ?? 0)
    const y = n.position.y + (parent?.position.y ?? 0)
    x0 = Math.min(x0, x); y0 = Math.min(y0, y)
    x1 = Math.max(x1, x + (n.width ?? 0)); y1 = Math.max(y1, y + (n.height ?? 0))
  }
  return Number.isFinite(x0) ? { x0, y0, x1, y1 } : { x0: 0, y0: 0, x1: 0, y1: 0 }
}

export interface TopologyTraffic {
  snapshot: TrafficSnapshot
  error?: string
  loading: boolean
  nodes: Node[]
  edges: Edge[]
  /** Edges drawn (after merging ports of the same pair). */
  shown: number
}

/**
 * Turns traffic edges into React Flow elements over the current graph. `cards` are the laid-out graph
 * nodes (cards and regions) so external endpoints can be placed beside the layout.
 */
export function useTopologyTraffic(clusterId: string, namespace: string, enabled: boolean, graphNodes: GraphNode[] | undefined, cards: Node[], dir: Dir, selected?: string): TopologyTraffic {
  const { data, error, loading } = useTrafficEdges(clusterId, namespace, enabled)
  const box = useMemo(() => bounds(cards), [cards])
  const built = useMemo(() => {
    const nodes: Node[] = []
    const edges: Edge[] = []
    if (!enabled || !graphNodes || !data.available) return { nodes, edges }
    const ids = new Set(graphNodes.map((n) => n.id))
    // Fallback match by namespace/name across workload kinds (Hubble may not report the kind).
    const byName = new Map<string, string | null>()
    for (const n of graphNodes) {
      if (!WORKLOAD_KINDS.has(n.kind) || n.count) continue
      const k = `${n.namespace ?? ''}/${n.name}`
      byName.set(k, byName.has(k) ? null : n.id)
    }
    const ext = new Map<string, TrafficEndpoint>()
    const resolve = (e: TrafficEndpoint) => {
      const id = topologyId(e)
      if (ids.has(id)) return id
      if (WORKLOAD_KINDS.has(e.kind)) {
        const alt = byName.get(`${e.namespace ?? ''}/${e.name}`)
        if (alt) return alt
      }
      const key = 'trf-ext:' + endpointKey(e)
      if (!ext.has(key)) ext.set(key, e)
      return key
    }
    // One drawn edge per pair; ports of the same pair add up.
    const pairs = new Map<string, { from: string; to: string; fe: TrafficEndpoint; te: TrafficEndpoint; list: TrafficEdge[] }>()
    for (const e of data.edges) {
      const from = resolve(e.from)
      const to = resolve(e.to)
      if (from === to) continue
      const k = `${from}>${to}`
      const p = pairs.get(k) ?? pairs.set(k, { from, to, fe: e.from, te: e.to, list: [] }).get(k)!
      p.list.push(e)
    }
    for (const [k, p] of pairs) {
      const total = p.list.reduce((s, e) => s + e.rate, 0)
      const unit = p.list[0].unit
      const ports = p.list.map((e) => (e.port ? `${e.port}${e.protocol ? '/' + e.protocol : ''}` : e.protocol || '')).filter(Boolean)
      const hot = !!selected && (p.from === selected || p.to === selected)
      edges.push({
        id: 'trf:' + k, source: p.from, target: p.to, type: 'trf', zIndex: 5,
        className: `trf-edge ${selected && !hot ? 'trf-dim' : ''}`,
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: 'var(--trf)' },
        data: {
          label: fmtRate(total, unit),
          testid: `traffic-edge-${p.fe.name}-${p.te.name}`,
          title: `${endpointLabel(p.fe)} → ${endpointLabel(p.te)}${ports.length ? `\nporta ${ports.join(', ')}` : ''}\n${fmtRate(total, unit)} (${sourceLabel(p.list[0].source)})`,
        } satisfies TrfData,
      })
    }
    // External endpoints: one column (or row) beside the layout.
    let i = 0
    for (const [id, e] of ext) {
      const position = dir === 'RIGHT'
        ? { x: box.x1 + GAP, y: box.y0 + i * (EXT_H + 24) }
        : { x: box.x0 + i * (EXT_W + 24), y: box.y1 + GAP }
      nodes.push({ id, type: 'trfExt', position, width: EXT_W, height: EXT_H, data: { e, dir } satisfies ExtData, zIndex: 4 })
      i++
    }
    return { nodes, edges }
  }, [enabled, graphNodes, data, box, dir, selected])
  return { snapshot: data, error, loading, nodes: built.nodes, edges: built.edges, shown: built.edges.length }
}
