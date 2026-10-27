import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import {
  Background, Handle, MarkerType, MiniMap, Panel, Position, ReactFlow, ReactFlowProvider, useReactFlow, useStore,
  type Edge, type Node, type NodeChange, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { ArrowDown, ArrowLeftRight, ArrowRight, Focus, Group, Maximize, Minus, Plus, Settings2, Tag, Workflow, X } from 'lucide-react'
import { api, type Graph, type GraphNode } from '../../api'
import { findByKind, openPanel, useApp, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { ClusterGate, ErrorBanner, Partial } from '../common'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { behaviorKind } from '../shell/nav'
import { useWorkNamespace } from '../shell/useWorkNamespace'
import { kindIcon } from '../shell/nav'
import { useTheme } from '../shell/theme'
import { layoutGraph, type Dir, type LayoutResult } from './layout'
import {
  CARD_W, EDGE_COLORS, HEALTH_COLORS, OTHER, distinctApps, hueOf, nodeSize, podStats, relClass, squares, total, type Health, type PodStats,
} from './model'
import { TRAFFIC_EVENT, TrafficEdgeView, TrafficExtNode, useTopologyTraffic } from './traffic'
import { sourceLabel } from '../traffic/api'
import './topology.css'

const DEPENDENCY_KINDS = ['ConfigMap', 'Secret', 'ServiceAccount', 'RoleBinding', 'Role', 'ClusterRole', 'Node', 'PersistentVolume', 'StorageClass', 'GatewayClass']
const OWNER_GROUPS: Record<string, string> = { ReplicaSet: 'apps', StatefulSet: 'apps', DaemonSet: 'apps', Job: 'batch', DeploymentConfig: 'apps.openshift.io', ReplicationController: '' }

// The summary sits above the canvas (not over it), so the fit only keeps room for the zoom controls.
const FIT_PADDING = { top: '24px', right: '24px', bottom: '56px', left: '24px' } as const

type CardData = { n: GraphNode; root: boolean; stats?: PodStats; dir: Dir }
type RegionData = { app: string; count: number; w: number; h: number }

const statusText = (n: GraphNode, stats?: PodStats) => {
  if (n.missing) return 'Ausente'
  if (stats) {
    const t = total(stats)
    return `${t} pod${t === 1 ? '' : 's'}${stats.error ? ` · ${stats.error} com erro` : stats.warn ? ` · ${stats.warn} em alerta` : ''}`
  }
  return n.status || ''
}

const KindNode = memo(function KindNode({ data }: NodeProps<Node<CardData>>) {
  const { n, root, stats, dir } = data
  const Icon = kindIcon(behaviorKind(n.group ?? '', n.kind))
  const sq = stats ? squares(stats) : undefined
  const t = stats ? total(stats) : 0
  return (
    <div className={`tc h-${n.health} ${root ? 'root' : ''} ${n.missing ? 'missing' : ''} ${n.ref ? 'ref' : ''}`} title={`${n.kind} ${n.namespace ? n.namespace + '/' : ''}${n.name}${n.status ? '\n' + n.status : ''}`} data-testid={`topo-${n.id}`}>
      <Handle type="target" position={dir === 'RIGHT' ? Position.Left : Position.Top} />
      <div className="tc-head">
        <span className="tc-ico"><Icon size={16} /></span>
        <span className="tc-txt">
          <span className="tc-name">{n.name}</span>
          <span className="tc-sub">{n.kind}{n.count ? ` ×${n.count}` : ''}{n.namespace ? ` · ${n.namespace}` : ''}</span>
        </span>
      </div>
      {statusText(n, stats) && <div className="tc-status"><i className={`dot h-${n.health}`} />{statusText(n, stats)}</div>}
      {sq && (
        <div className="tc-pods" data-testid="pod-squares">
          {sq.items.map((h, i) => <i key={i} className={`sq h-${h}`} />)}
          {sq.more > 0 && <span className="tc-more">+{sq.more}</span>}
        </div>
      )}
      <div className="tc-bar">
        {stats && t > 0 ? (['error', 'warn', 'ok', 'neutral'] as Health[]).map((h) => stats[h] ? <i key={h} className={`h-${h}`} style={{ flex: stats[h] }} /> : null) : <i className={`h-${n.health}`} style={{ flex: 1 }} />}
      </div>
      <Handle type="source" position={dir === 'RIGHT' ? Position.Right : Position.Bottom} />
    </div>
  )
})

const RegionNode = memo(function RegionNode({ data }: NodeProps<Node<RegionData>>) {
  const other = data.app === OTHER
  return (
    <div className={`topo-region ${other ? 'other' : ''}`} style={{ ['--h' as string]: other ? 215 : hueOf(data.app), width: data.w, height: data.h }} data-testid="topo-region">
      <span className="topo-region-label">{other ? 'outros' : data.app}<b>{data.count}</b></span>
    </div>
  )
})

const nodeTypes = { kind: KindNode, region: RegionNode, trfExt: TrafficExtNode }
const edgeTypes = { trf: TrafficEdgeView }

type Focus = { group?: string; kind: string; name: string; ns: string }
type Lay = { g: Graph; res: LayoutResult; stats: Map<string, PodStats>; dir: Dir }

export function TopologyView(props: { clusterId: string; namespace: string; group?: string; kind?: string; name?: string; leading?: ReactNode; traffic?: boolean }) {
  return (
    <ReactFlowProvider>
      <TopologyInner {...props} />
    </ReactFlowProvider>
  )
}

function TopologyInner({ clusterId, namespace, group: kindGroup, kind, name, leading, traffic: trafficInit }: { clusterId: string; namespace: string; group?: string; kind?: string; name?: string; leading?: ReactNode; traffic?: boolean }) {
  const theme = useTheme((s) => s.theme)
  const { fitView } = useReactFlow()
  const root = useRef<HTMLDivElement>(null)
  const [raw, setRaw] = useState<Graph>()
  const [lay, setLay] = useState<Lay>()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [rev, setRev] = useState(0)
  const [showDeps, setShowDeps] = useState(true)
  const [labels, setLabels] = useState(false)
  const [q, setQ] = useState('')
  const [dir, setDir] = useState<Dir>('RIGHT')
  const [groupPref, setGroupPref] = useState<boolean>()
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState<string>()
  const [focus, setFocus] = useState<Focus>()
  const [moved, setMoved] = useState<Map<string, { x: number; y: number }>>(new Map())
  const [traffic, setTraffic] = useState(!!trafficInit)

  useEffect(() => {
    const on = (e: Event) => (e as CustomEvent<string>).detail === clusterId && setTraffic(true)
    window.addEventListener(TRAFFIC_EVENT, on)
    return () => window.removeEventListener(TRAFFIC_EVENT, on)
  }, [clusterId])

  const effKind = focus?.kind ?? kind
  const effName = focus?.name ?? name
  const effNs = focus?.ns ?? namespace
  const effGroup = focus ? focus.group : kindGroup

  useEffect(() => setFocus(undefined), [clusterId, namespace, kind, name, kindGroup])

  useEffect(() => {
    if (!effNs) return
    let stop = false
    setBusy(true)
    api
      .graph(clusterId, effNs, effKind, effName, effGroup)
      .then((g) => {
        if (stop) return
        setRaw({ ...g, nodes: g.nodes ?? [], edges: g.edges ?? [] })
        setErr(undefined)
      })
      .catch((e) => !stop && setErr(e.message))
      .finally(() => !stop && setBusy(false))
    return () => {
      stop = true
    }
  }, [clusterId, effNs, effKind, effName, effGroup, rev])

  const base = useMemo(() => (raw ? (showDeps ? raw : filterDeps(raw, raw.root)) : undefined), [raw, showDeps])
  const group = groupPref ?? (raw ? distinctApps(raw) >= 2 : false)
  const visible = useMemo(() => {
    if (!base) return undefined
    if (!hidden.size) return base
    const keep = new Set(base.nodes.filter((n) => n.id === base.root || !hidden.has(n.kind)).map((n) => n.id))
    return { ...base, nodes: base.nodes.filter((n) => keep.has(n.id)), edges: base.edges.filter((e) => keep.has(e.from) && keep.has(e.to)) }
  }, [base, hidden])

  useEffect(() => {
    if (!visible) return
    let stop = false
    const stats = podStats(visible)
    const sizes = new Map(visible.nodes.map((n) => [n.id, nodeSize(n, stats.get(n.id))]))
    layoutGraph(visible, { dir, group, sizes })
      .then((res) => {
        if (stop) return
        setLay({ g: visible, res, stats, dir })
        setMoved(new Map())
        setErr(undefined)
      })
      .catch((e) => !stop && setErr(String(e?.message ?? e)))
    return () => {
      stop = true
    }
  }, [visible, dir, group])

  useEffect(() => {
    if (!lay) return
    const t = setTimeout(() => fitView({ padding: FIT_PADDING, maxZoom: 1, duration: 250 }), 60)
    return () => clearTimeout(t)
  }, [lay, fitView])

  useEffect(() => {
    if (selected && lay && !lay.g.nodes.some((n) => n.id === selected)) setSelected(undefined)
  }, [lay, selected])

  const kinds = useMemo(() => {
    const m = new Map<string, number>()
    for (const n of base?.nodes ?? []) m.set(n.kind, (m.get(n.kind) ?? 0) + (n.count ?? 1))
    return [...m].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
  }, [base])

  const adj = useMemo(() => {
    const out = new Map<string, Set<string>>()
    const inn = new Map<string, Set<string>>()
    for (const e of lay?.g.edges ?? []) {
      ;(out.get(e.from) ?? out.set(e.from, new Set()).get(e.from)!).add(e.to)
      ;(inn.get(e.to) ?? inn.set(e.to, new Set()).get(e.to)!).add(e.from)
    }
    return { out, inn }
  }, [lay])

  const baseNodes = useMemo(() => {
    if (!lay) return [] as Node[]
    const regions: Node[] = lay.res.regions.map((r) => ({
      id: r.id, type: 'region', position: { x: r.x, y: r.y }, width: r.w, height: r.h, data: { app: r.app, count: r.count, w: r.w, h: r.h },
      selectable: false, draggable: false, focusable: false, zIndex: 0,
    }))
    const cards: Node[] = lay.g.nodes.map((n) => {
      const p = lay.res.pos.get(n.id) ?? { x: 0, y: 0 }
      const s = lay.stats.get(n.id)
      return {
        id: n.id, type: 'kind', position: { x: p.x, y: p.y }, parentId: p.parent, width: CARD_W, height: nodeSize(n, s).h,
        data: { n, root: n.id === lay.g.root, stats: s, dir: lay.dir } satisfies CardData,
      }
    })
    return [...regions, ...cards]
  }, [lay])

  const nodes = useMemo(() => {
    const f = q.trim().toLowerCase()
    const nb = selected ? new Set([selected, ...(adj.out.get(selected) ?? []), ...(adj.inn.get(selected) ?? [])]) : undefined
    return baseNodes.map((b) => {
      let cls = ''
      if (b.type === 'kind') {
        const n = (b.data as CardData).n
        if (b.id === selected) cls = 'topo-sel'
        else if (nb && !nb.has(b.id)) cls = 'topo-dim'
        else if (nb) cls = 'topo-nb'
        if (f && !`${n.kind} ${n.name}`.toLowerCase().includes(f) && b.id !== selected) cls = 'topo-dim'
      }
      const mv = moved.get(b.id)
      return cls === (b.className ?? '') && !mv ? b : { ...b, className: cls, position: mv ?? b.position }
    })
  }, [baseNodes, selected, adj, q, moved])

  const edges = useMemo(() => {
    const colors = EDGE_COLORS[theme]
    return (lay?.g.edges ?? []).map((e, i): Edge => {
      const c = relClass(e.rel)
      const hot = !!selected && (e.from === selected || e.to === selected)
      return {
        id: `e${i}`, source: e.from, target: e.to, type: 'default', animated: hot,
        label: labels || hot ? e.rel : undefined,
        className: selected && !hot ? 'topo-edge-dim' : hot ? 'topo-edge-hot' : undefined,
        style: { stroke: colors[c], strokeWidth: hot ? 2.2 : 1.4, strokeDasharray: c === 'sched' ? '3 5' : undefined },
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: colors[c] },
        labelBgStyle: { fill: 'var(--panel)' }, labelStyle: { fill: 'var(--muted)', fontSize: 10 }, labelBgPadding: [4, 2], labelBgBorderRadius: 4,
      }
    })
  }, [lay, selected, labels, theme])

  const trf = useTopologyTraffic(clusterId, effNs, traffic, lay?.g.nodes, baseNodes, lay?.dir ?? dir, selected)
  const allNodes = useMemo(() => (trf.nodes.length ? [...nodes, ...trf.nodes.map((n) => (moved.has(n.id) ? { ...n, position: moved.get(n.id)! } : n))] : nodes), [nodes, trf.nodes, moved])
  const allEdges = useMemo(() => (trf.edges.length ? [...edges, ...trf.edges] : edges), [edges, trf.edges])
  // External endpoints sit beside the layout: refit when that set changes so they are on screen.
  const extIds = trf.nodes.map((n) => n.id).join('|')
  useEffect(() => {
    if (!extIds) return
    const t = setTimeout(() => fitView({ padding: FIT_PADDING, maxZoom: 1, duration: 250 }), 60)
    return () => clearTimeout(t)
  }, [extIds, fitView])

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    const pos = changes.filter((c) => c.type === 'position' && c.position)
    if (!pos.length) return
    setMoved((m) => {
      const next = new Map(m)
      for (const c of pos) if (c.type === 'position' && c.position) next.set(c.id, c.position)
      return next
    })
  }, [])

  /** Group of the controller that owns an aggregate: the owner card in the graph, the built-in map, or the only kind with that name. */
  const ownerOf = (n: GraphNode) => {
    // The engine names the owner of an aggregate (group included); older engines only had the id.
    if (n.owner) return { kind: n.owner.kind, ns: n.namespace, name: n.owner.name, group: n.owner.group }
    const [, k, ns, owner] = n.id.split('/')
    const card = raw?.nodes.find((x) => !x.count && x.kind === k && x.name === owner && (x.namespace ?? '') === (ns ?? ''))
    const group = card ? card.group : k in OWNER_GROUPS ? OWNER_GROUPS[k] : findByKind(clusterId, k)?.group
    return { kind: k, ns, name: owner, group }
  }
  const open = (n: GraphNode) => {
    if (n.count) {
      // Aggregate: open the controller that owns these pods (it may be a CRD: Rollout, CloneSet…).
      const o = ownerOf(n)
      if (o.group === undefined || !findByKind(clusterId, o.kind, o.group)) {
        useApp.getState().toast('error', `Não foi possível abrir ${o.kind}/${o.name}: o tipo não está disponível neste cluster.`)
        return
      }
      openObject(clusterId, o.group, o.kind, o.name, o.ns)
    } else if (!n.missing) openObject(clusterId, n.group, n.kind, n.name, n.namespace)
  }
  const focusOn = (n: GraphNode) => {
    if (n.missing) return
    const o = n.count ? ownerOf(n) : undefined
    const f: Focus = o ? { group: o.group, kind: o.kind, name: o.name, ns: n.namespace || effNs } : { group: n.group, kind: n.kind, name: n.name, ns: n.namespace || effNs }
    setSelected(undefined)
    setFocus(f)
  }

  const sel = selected ? lay?.g.nodes.find((n) => n.id === selected) : undefined
  const toggleKind = (k: string) => setHidden((h) => {
    const next = new Set(h)
    if (!next.delete(k)) next.add(k)
    return next
  })
  const hc = HEALTH_COLORS[theme]
  const apps = raw ? distinctApps(raw) : 0

  return (
    <div className="topology" ref={root} tabIndex={-1} onKeyDown={(e) => e.key === 'Escape' && selected && (e.stopPropagation(), setSelected(undefined))}>
      <PanelHeader icon={Workflow} title="Topologia" scope={leading} className="tp-bar" actions={[
        { key: 'traffic', label: 'Tráfego', icon: ArrowLeftRight, pressed: traffic, testid: 'topology-traffic', title: 'Conexões observadas (Caretta ou Hubble pelo Prometheus)', onClick: () => setTraffic((v) => !v) },
        { key: 'group', label: 'Agrupar por app', icon: Group, pressed: group, testid: 'group-toggle', title: apps ? `${apps} apps identificadas pelos rótulos` : 'Nenhuma app identificada', onClick: () => setGroupPref(!group) },
        { key: 'deps', label: 'Config, RBAC e nodes', icon: Settings2, pressed: showDeps, title: 'Mostrar ConfigMaps, Secrets, RBAC e nodes ligados aos workloads', onClick: () => setShowDeps((v) => !v) },
        { key: 'labels', label: 'Rótulos nas setas', icon: Tag, pressed: labels, title: 'Mostrar o tipo de cada relação nas setas', onClick: () => setLabels((v) => !v) },
        { key: 'dir', label: 'Direção', value: dir, onSelect: (v) => setDir(v as Dir), options: [
          { value: 'RIGHT', label: 'Horizontal', icon: ArrowRight, title: 'Fluxo da esquerda para a direita' },
          { value: 'DOWN', label: 'Vertical', icon: ArrowDown, title: 'Fluxo de cima para baixo' },
        ] },
        refreshAction(() => setRev((r) => r + 1), busy),
      ]}>
        <input className="search" placeholder="Destacar…" aria-label="Destacar objetos pelo nome" value={q} onChange={(e) => setQ(e.target.value)} />
      </PanelHeader>
      <div className="tp-strip" data-testid="topo-head">
        <span className="tp-title"><span>{lay ? `${lay.g.nodes.length} objeto${lay.g.nodes.length === 1 ? '' : 's'}, ${lay.g.edges.length} relaç${lay.g.edges.length === 1 ? 'ão' : 'ões'}` : '…'}</span></span>
        <div className="tp-chips">
          {kinds.map(([k, c]) => {
            const Icon = kindIcon(k)
            return (
              <button key={k} className={`tp-chip ${hidden.has(k) ? 'off' : ''}`} aria-pressed={!hidden.has(k)} data-testid={`kind-chip-${k}`} title={hidden.has(k) ? `Mostrar ${k}` : `Ocultar ${k}`} onClick={() => toggleKind(k)}>
                <Icon size={12} />{k}<b>{c}</b>
              </button>
            )
          })}
          {focus && <button className="tp-chip focus" onClick={() => setFocus(undefined)} title="Voltar ao grafo completo"><Focus size={12} />{focus.kind}/{focus.name}<X size={12} /></button>}
        </div>
      </div>
      <ErrorBanner error={err} />
      <Partial errors={raw?.errors} />
      {raw?.truncated && <div className="banner warn">Grafo truncado em 2.000 nós. Abra a topologia de um objeto para ver um recorte.</div>}
      <div className="flow" data-testid="topology">
        {raw && (
          <ReactFlow
            nodes={allNodes} edges={allEdges} nodeTypes={nodeTypes} edgeTypes={edgeTypes} onNodesChange={onNodesChange}
            onNodeClick={(_, node) => {
              root.current?.focus()
              setSelected(node.type === 'kind' ? node.id : undefined)
            }}
            onNodeDoubleClick={(_, node) => node.type === 'kind' && open((node.data as CardData).n)}
            onPaneClick={() => setSelected(undefined)}
            fitView minZoom={0.05} maxZoom={2} nodesConnectable={false} proOptions={{ hideAttribution: true }} colorMode={theme}
            onlyRenderVisibleElements={nodes.length > 300} zoomOnDoubleClick={false}
          >
            <Background gap={24} size={1} />
            {sel && (
              <Panel position="top-right" className="tp-info" data-testid="topo-info">
                <div className="tp-info-head">
                  <span className="tc-ico">{(() => { const I = kindIcon(behaviorKind(sel.group ?? '', sel.kind)); return <I size={16} /> })()}</span>
                  <span className="tc-txt"><span className="tc-name">{sel.name}</span><span className="tc-sub">{sel.kind}{sel.namespace ? ` · ${sel.namespace}` : ''}</span></span>
                  <button className="tp-x" aria-label="Fechar" onClick={() => setSelected(undefined)}><X size={14} /></button>
                </div>
                {statusText(sel, lay?.stats.get(sel.id)) && <div className="tc-status"><i className={`dot h-${sel.health}`} />{statusText(sel, lay?.stats.get(sel.id))}</div>}
                <div className="tp-rel">
                  <span><b>{adj.out.get(sel.id)?.size ?? 0}</b> usa</span>
                  <span><b>{adj.inn.get(sel.id)?.size ?? 0}</b> depende dele</span>
                </div>
                <div className="tp-actions">
                  <button className="primary" disabled={sel.missing} onClick={() => open(sel)}>Abrir detalhes</button>
                  <button disabled={sel.missing} onClick={() => focusOn(sel)}><Focus size={13} /> Focar</button>
                </div>
              </Panel>
            )}
            {traffic && <TrafficStatus t={trf} ns={effNs} />}
            <ZoomControls />
            {nodes.length > 12 && (
              <MiniMap pannable zoomable className="tp-mini" nodeStrokeWidth={0} nodeColor={(n) => (n.type === 'kind' ? hc[(n.data as CardData).n.health as Health] ?? hc.neutral : 'transparent')} maskColor="color-mix(in srgb, var(--bg) 62%, transparent)" />
            )}
          </ReactFlow>
        )}
        {raw && !raw.nodes.length && <div className="empty"><Workflow size={24} /><span>Nada para mostrar em <b>{effNs}</b>.</span><span className="muted small">Escolha outro namespace no seletor acima.</span></div>}
      </div>
    </div>
  )
}

function TrafficStatus({ t, ns }: { t: ReturnType<typeof useTopologyTraffic>; ns: string }) {
  const s = t.snapshot
  let body: ReactNode
  if (t.error) body = <><b>Tráfego indisponível.</b> {t.error}</>
  else if (!s.available && t.loading && !s.hint) body = <>Lendo o tráfego observado…</>
  else if (!s.available) body = <><b>Sem dados de tráfego.</b> {s.hint || 'A fonte de tráfego não respondeu.'}</>
  else if (!s.edges.length) body = <>Nenhuma conexão observada em <b>{ns}</b> nos últimos {s.window ?? '5m'} ({sourceLabel(s.source)}).</>
  else body = <><b>{t.shown}</b> conex{t.shown === 1 ? 'ão' : 'ões'} observada{t.shown === 1 ? '' : 's'} · {sourceLabel(s.source)} · últimos {s.window ?? '5m'}</>
  return <Panel position="bottom-center" className="trf-banner" data-testid="topology-traffic-status">{body}</Panel>
}

function ZoomControls() {
  const { zoomIn, zoomOut, fitView } = useReactFlow()
  const zoom = useStore((s) => s.transform[2])
  return (
    <Panel position="bottom-left" className="tp-zoom" data-testid="topo-zoom">
      <button aria-label="Diminuir zoom" onClick={() => zoomOut({ duration: 150 })}><Minus size={14} /></button>
      <span data-testid="topo-zoom-pct">{Math.round(zoom * 100)}%</span>
      <button aria-label="Aumentar zoom" onClick={() => zoomIn({ duration: 150 })}><Plus size={14} /></button>
      <button aria-label="Ajustar à tela" title="Ajustar à tela" onClick={() => fitView({ padding: FIT_PADDING, maxZoom: 1, duration: 250 })}><Maximize size={14} /></button>
    </Panel>
  )
}

function filterDeps(g: Graph, root?: string): Graph {
  const hide = new Set(g.nodes.filter((n) => DEPENDENCY_KINDS.includes(n.kind) && n.id !== root && !n.missing).map((n) => n.id))
  return { ...g, nodes: g.nodes.filter((n) => !hide.has(n.id)), edges: g.edges.filter((e) => !hide.has(e.from) && !hide.has(e.to)) }
}

export function openTopology(clusterId: string, namespace?: string, opts?: OpenOptions, extra?: { traffic?: boolean }) {
  openPanel(`topology:${clusterId}`, 'topology', 'Topologia', { clusterId, namespace, traffic: extra?.traffic }, opts)
  // An already open topology keeps its params: ask it to turn the overlay on.
  if (extra?.traffic) window.dispatchEvent(new CustomEvent(TRAFFIC_EVENT, { detail: clusterId }))
}

export function TopologyPanel({ params }: IDockviewPanelProps<{ clusterId: string; namespace?: string; traffic?: boolean }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <NamespaceTopology clusterId={params.clusterId} initial={params.namespace} traffic={params.traffic} />
    </ClusterGate>
  )
}

function NamespaceTopology({ clusterId, initial, traffic }: { clusterId: string; initial?: string; traffic?: boolean }) {
  const ns = useWorkNamespace(clusterId, initial)
  if (!ns) return <div className="pad"><span className="spinner" /> Escolhendo o namespace com mais workloads…</div>
  return (
    <div className="object-panel">
      <TopologyView clusterId={clusterId} namespace={ns} traffic={traffic} leading={<NamespaceSelect clusterId={clusterId} effective={ns} />} />
    </div>
  )
}
