import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { applyNodeChanges, Handle, MarkerType, Position, ReactFlow, ReactFlowProvider, useReactFlow, type Edge, type Node, type NodeChange, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  Check, ChevronDown, CircleCheck, CircleX, ExternalLink, Filter, GitBranch, Info, LayoutGrid, LoaderCircle, Lock, LockOpen, Maximize, Minus, MoreVertical, Plus, RefreshCw,
  Search, SlidersHorizontal, Star, X,
} from 'lucide-react'
import type { Row } from '../../api'
import { findKind, findResource, openPanel, useApp, useUi, type OpenOptions, type TopoPos } from '../../store'
import { useWatch } from '../../useWatch'
import { openObject } from '../../components/ObjectPanel'
import { openMenu, type MenuItem } from '../shell/menu'
import { useRowActions } from '../grid/actions'
import { kindIcon } from '../shell/nav'
import { useTheme } from '../shell/theme'
import { ClusterGate, ErrorBanner, Partial } from '../common'
import { badgeOf, getProjectTopology, isDefaultProject, PHASES, podTotal, type PodPhases, type ProjectTopology, type TopoNode } from './api'
import { blocksOf, familiesOf, NODE_W, place, sumPhases, type Block, type Family } from './layout'
import './projtopo.css'

export function openProjectTopology(clusterId: string, opts?: OpenOptions) {
  openPanel(`projtopo:${clusterId}`, 'projtopo', 'Topologia do projeto', { clusterId }, opts)
}

const REFRESH_MS = 15_000
const MAX_LAYOUTS = 40
const NO_APP = '\u0000none'

interface Display { podCount: boolean; labels: boolean; apps: boolean; releases: boolean }

// --- Nodes --------------------------------------------------------------------------------------

/** Pod ring as in the console: one arc per phase, an empty grey ring when scaled to zero. */
function PodRing({ phases, size }: { phases: PodPhases; size: number }) {
  const r = size / 2 - 5, c = size / 2
  const total = podTotal(phases)
  if (!total) return <circle className="pt-ring-empty" cx={c} cy={c} r={r} />
  const arcs: ReactNode[] = []
  let a = -Math.PI / 2
  const gap = total > 1 ? 0.05 : 0
  for (const [k] of PHASES) {
    const n = phases[k] ?? 0
    if (!n) continue
    const span = (n / total) * Math.PI * 2
    if (span >= Math.PI * 2 - 1e-6) {
      arcs.push(<circle key={k} className={`pt-ring ${k}`} cx={c} cy={c} r={r} />)
    } else {
      const a0 = a + gap / 2, a1 = a + span - gap / 2
      const p = (t: number) => `${(c + r * Math.cos(t)).toFixed(2)} ${(c + r * Math.sin(t)).toFixed(2)}`
      arcs.push(<path key={k} className={`pt-ring ${k}`} d={`M${p(a0)}A${r} ${r} 0 ${a1 - a0 > Math.PI ? 1 : 0} 1 ${p(a1)}`} />)
    }
    a += span
  }
  return <>{arcs}</>
}

type WorkloadData = {
  n: TopoNode
  display: Display
  hit?: boolean
  dim?: boolean
  selected?: boolean
  onSelect: (n: TopoNode) => void
  onKebab: (n: TopoNode, e: React.MouseEvent) => void
  onBuild: (n: TopoNode) => void
  onOpen: (n: TopoNode) => void
}

const RING = 84

// Long names keep both ends, as the console does (el-sid…sitory): they usually differ at the end.
const midTrunc = (s: string, n = 16) => (s.length <= n ? s : `${s.slice(0, Math.ceil((n - 1) / 2))}…${s.slice(-Math.floor((n - 1) / 2))}`)

const BuildIcon = ({ phase }: { phase?: string }) =>
  phase === 'Complete' ? <CircleCheck size={13} /> : phase === 'Failed' || phase === 'Error' || phase === 'Cancelled' ? <CircleX size={13} /> : <LoaderCircle size={13} />

const WorkloadNode = memo(function WorkloadNode({ data }: NodeProps<Node<WorkloadData>>) {
  const { n, display, hit, dim, selected } = data
  const Icon = kindIcon(n.kind)
  const [badge, cls] = badgeOf(n.kind)
  const total = podTotal(n.phases)
  const buildCls = n.build?.phase === 'Complete' ? 'ok' : n.build?.phase === 'Failed' || n.build?.phase === 'Error' ? 'fail' : 'run'
  return (
    <div className={`pt-node${selected ? ' selected' : ''}${hit ? ' hit' : ''}${dim ? ' dim' : ''}`} data-testid={`pt-node-${n.name}`}
      onClick={(e) => (e.stopPropagation(), data.onSelect(n))} onDoubleClick={(e) => (e.stopPropagation(), data.onOpen(n))}>
      <Handle type="target" position={Position.Left} className="pt-handle" />
      <Handle type="source" position={Position.Right} className="pt-handle" />
      <div className="pt-circle">
        <svg width={RING} height={RING}>
          <PodRing phases={n.phases} size={RING} />
          <circle className="pt-core" cx={RING / 2} cy={RING / 2} r={RING / 2 - 11} />
        </svg>
        <div className="pt-center">
          {display.podCount ? (
            <span className="pt-count"><b>{total}</b><small>{total === 1 ? 'pod' : 'pods'}</small></span>
          ) : (
            <Icon size={26} strokeWidth={1.5} />
          )}
        </div>
        {n.url && (
          <button className="pt-deco tr nodrag" title={`Abrir ${n.url}`} onClick={(e) => (e.stopPropagation(), window.open(n.url, '_blank', 'noopener'))}>
            <ExternalLink size={11} />
          </button>
        )}
        {n.vcs && (
          <button className="pt-deco tl nodrag" title={`Código-fonte: ${n.vcs}`} onClick={(e) => (e.stopPropagation(), window.open(n.vcs, '_blank', 'noopener'))}>
            <GitBranch size={11} />
          </button>
        )}
        {n.build && (
          <button className={`pt-deco bl build ${buildCls} nodrag`} title={`Build ${n.build.name ?? n.build.config}: ${n.build.phase ?? 'sem builds'}`} onClick={(e) => (e.stopPropagation(), data.onBuild(n))}>
            <BuildIcon phase={n.build.phase} />
          </button>
        )}
        {(n.health === 'error' || n.phases.failed > 0) && <span className="pt-deco br alert" title={n.status || 'Com problemas'}>!</span>}
      </div>
      <div className="pt-label">
        <span className={`pt-badge ${cls}`}>{badge}</span>
        <span className="pt-name" title={n.name}>{midTrunc(n.name)}</span>
        <button className="pt-kebab nodrag" title="Ações" onClick={(e) => (e.stopPropagation(), data.onKebab(n, e))}><MoreVertical size={12} /></button>
      </div>
      {display.labels && n.labels && Object.keys(n.labels).length > 0 && (
        <div className="pt-labels">{Object.entries(n.labels).slice(0, 3).map(([k, v]) => <span key={k} title={`${k}=${v}`}>{k.split('/').pop()}={v}</span>)}</div>
      )}
    </div>
  )
})

type GroupData = { block: Block; collapsed: boolean; dim?: boolean; hit?: boolean; onToggle: (b: Block) => void }

const GroupNode = memo(function GroupNode({ data }: NodeProps<Node<GroupData>>) {
  const { block, collapsed, dim, hit } = data
  const ph = collapsed ? sumPhases(block.nodes) : undefined
  return (
    <div className={`pt-group ${block.kind}${dim ? ' dim' : ''}${hit ? ' hit' : ''}`} data-testid={`pt-group-${block.name}`}>
      <Handle type="target" position={Position.Left} className="pt-handle" />
      <Handle type="source" position={Position.Right} className="pt-handle" />
      {collapsed && ph && (
        <div className="pt-collapsed">
          <svg width={RING} height={RING}>
            <PodRing phases={ph} size={RING} />
            <circle className="pt-core" cx={RING / 2} cy={RING / 2} r={RING / 2 - 11} />
          </svg>
          <span className="pt-center"><span className="pt-count"><b>{block.nodes.length}</b><small>{block.nodes.length === 1 ? 'recurso' : 'recursos'}</small></span></span>
        </div>
      )}
      <div className="pt-group-label">
        <span className={`pt-badge ${block.kind === 'app' ? 'appgrp' : 'helmgrp'}`}>{block.kind === 'app' ? 'A' : 'HR'}</span>
        <span className="pt-name" title={block.name}>{midTrunc(block.name, 34)}</span>
        <button className="pt-kebab nodrag" title={collapsed ? 'Expandir' : 'Recolher'} onClick={(e) => (e.stopPropagation(), data.onToggle(block))}>
          {collapsed ? <Plus size={12} /> : <Minus size={12} />}
        </button>
      </div>
    </div>
  )
})

const nodeTypes = { workload: WorkloadNode, tgroup: GroupNode }

// --- Toolbar pickers -----------------------------------------------------------------------------

function Popover({ label, testid, children, wide }: { label: ReactNode; testid: string; children: (close: () => void) => ReactNode; wide?: boolean }) {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])
  return (
    <div className="pt-pop-wrap">
      <button className={`pt-select${open ? ' open' : ''}`} data-testid={testid} onClick={() => setOpen((o) => !o)}>
        {label} <ChevronDown size={14} />
      </button>
      {open && (
        <>
          <div className="pt-away" onClick={() => setOpen(false)} />
          <div className={`pt-pop${wide ? ' wide' : ''}`}>{children(() => setOpen(false))}</div>
        </>
      )}
    </div>
  )
}

function useFavorites(key: 'topoFavorites' | 'topoAppFavorites', prefix: string) {
  const all = useUi((s) => s[key])
  const has = (v: string) => all.includes(`${prefix}|${v}`)
  const toggle = (v: string) => {
    const k = `${prefix}|${v}`
    useUi.getState().update({ [key]: all.includes(k) ? all.filter((x) => x !== k) : [...all, k] })
  }
  return { has, toggle }
}

function ProjectPicker({ clusterId, value, onChange }: { clusterId: string; value: string; onChange: (ns: string) => void }) {
  const { rows, error } = useWatch(clusterId, '', 'namespaces', '')
  const hasProjects = !!findResource(clusterId, 'project.openshift.io', 'projects')
  const projects = useWatch(clusterId, 'project.openshift.io', 'projects', '', !!error && hasProjects)
  const names = useMemo(() => (error && hasProjects ? projects.rows : rows).map((r) => r.name).sort(), [rows, projects.rows, error, hasProjects])
  const [q, setQ] = useState('')
  const [showDefault, setShowDefault] = useState(false)
  const fav = useFavorites('topoFavorites', clusterId)
  const list = names.filter((n) => (showDefault || !isDefaultProject(n)) && n.includes(q.trim().toLowerCase()))
  const favs = list.filter((n) => fav.has(n))
  const item = (n: string, close: () => void) => (
    <div key={n} className={`pt-item${n === value ? ' on' : ''}`} onClick={() => (onChange(n), close())}>
      <span className="ellipsis">{n}</span>
      {n === value && <Check size={14} className="pt-check" />}
      <button className={`pt-star${fav.has(n) ? ' on' : ''}`} title="Favorito" onClick={(e) => (e.stopPropagation(), fav.toggle(n))}><Star size={14} /></button>
    </div>
  )
  return (
    <Popover testid="pt-project" wide label={<><span className="muted">Namespace:</span> <b>{value || '—'}</b></>}>
      {(close) => (
        <>
          <label className="pt-pop-search"><Search size={14} /><input autoFocus placeholder="Selecionar namespace…" value={q} onChange={(e) => setQ(e.target.value)} /></label>
          <label className="pt-switch"><input type="checkbox" checked={showDefault} onChange={(e) => setShowDefault(e.target.checked)} /> Mostrar namespaces do sistema</label>
          <div className="pt-pop-list">
            {favs.length > 0 && <div className="pt-pop-head">Favoritos</div>}
            {favs.map((n) => item(n, close))}
            <div className="pt-pop-head">Namespaces</div>
            {list.map((n) => item(n, close))}
            {!list.length && <div className="pt-pop-empty muted">{error && !hasProjects ? `Sem permissão para listar namespaces` : 'Nenhum namespace'}</div>}
          </div>
        </>
      )}
    </Popover>
  )
}

/** Picks one application: its main deployment with the redis, sidekiq, migration… that serve it. */
function DeploymentPicker({ clusterId, ns, families, value, onChange }: { clusterId: string; ns: string; families: Family[]; value: string; onChange: (key: string) => void }) {
  const [q, setQ] = useState('')
  const fav = useFavorites('topoFavorites', `${clusterId}|${ns}`)
  const t = q.trim().toLowerCase()
  const shown = families.filter((f) => !t || f.members.some((n) => n.name.toLowerCase().includes(t)))
  const favs = shown.filter((f) => fav.has(f.key))
  const current = families.find((f) => f.key === value)
  const item = (f: Family, close: () => void) => {
    const [badge, cls] = badgeOf(f.main.kind)
    const extra = f.members.length - 1
    return (
      <div key={f.key} className={`pt-item${f.key === value ? ' on' : ''}`} onClick={() => (onChange(f.key), close())}
        title={f.members.map((n) => `${n.kind} ${n.name}`).join('\n')}>
        <span className={`pt-badge ${cls}`}>{badge}</span>
        <span className="ellipsis">{f.main.name}</span>
        {extra > 0 && <span className="pt-more">+{extra}</span>}
        {f.key === value && <Check size={14} className="pt-check" />}
        <button className={`pt-star${fav.has(f.key) ? ' on' : ''}`} title="Favorito" onClick={(e) => (e.stopPropagation(), fav.toggle(f.key))}><Star size={14} /></button>
      </div>
    )
  }
  return (
    <Popover testid="pt-deployment" wide label={<><span className="muted">Deployment:</span> <b className="pt-sel-name">{current ? current.main.name : 'Todos os deployments'}</b>{current && current.members.length > 1 && <span className="pt-more">+{current.members.length - 1}</span>}</>}>
      {(close) => (
        <>
          <label className="pt-pop-search"><Search size={14} /><input autoFocus placeholder="Selecionar deployment…" value={q} onChange={(e) => setQ(e.target.value)} /></label>
          <div className="pt-pop-list">
            <div className={`pt-item${value === '' ? ' on' : ''}`} onClick={() => (onChange(''), close())}>
              <span className="ellipsis">Todos os deployments</span>
              {value === '' && <Check size={14} className="pt-check" />}
            </div>
            {favs.length > 0 && <div className="pt-pop-head">Favoritos</div>}
            {favs.map((f) => item(f, close))}
            <div className="pt-pop-head">Deployments por aplicação</div>
            {shown.map((f) => item(f, close))}
            {!shown.length && <div className="pt-pop-empty muted">Nenhum deployment neste namespace</div>}
          </div>
        </>
      )}
    </Popover>
  )
}

function AppPicker({ clusterId, apps, value, onChange }: { clusterId: string; apps: string[]; value: string; onChange: (v: string) => void }) {
  const [q, setQ] = useState('')
  const fav = useFavorites('topoAppFavorites', clusterId)
  const label = value === '' ? 'Todas as aplicações' : value === NO_APP ? 'Sem grupo de aplicação' : value
  const opts: [string, string][] = [['', 'Todas as aplicações'], [NO_APP, 'Sem grupo de aplicação'], ...apps.map((a) => [a, a] as [string, string])]
  const shown = opts.filter(([, l]) => l.toLowerCase().includes(q.trim().toLowerCase()))
  const sorted = [...shown.filter(([v]) => fav.has(v)), ...shown.filter(([v]) => !fav.has(v))]
  return (
    <Popover testid="pt-app" label={<><span className="muted">Aplicação:</span> <b>{label}</b></>}>
      {(close) => (
        <>
          <label className="pt-pop-search"><Search size={14} /><input autoFocus placeholder="Selecionar aplicação" value={q} onChange={(e) => setQ(e.target.value)} /></label>
          <div className="pt-pop-list">
            {sorted.map(([v, l]) => (
              <div key={v || 'all'} className={`pt-item${v === value ? ' on' : ''}`} onClick={() => (onChange(v), close())}>
                <span className="ellipsis">{l}</span>
                {v === value && <Check size={14} className="pt-check" />}
                <button className={`pt-star${fav.has(v) ? ' on' : ''}`} title="Favorito" onClick={(e) => (e.stopPropagation(), fav.toggle(v))}><Star size={14} /></button>
              </div>
            ))}
          </div>
        </>
      )}
    </Popover>
  )
}

// --- Side panel and actions ----------------------------------------------------------------------

const rowOf = (n: TopoNode, ns: string): Row => ({ uid: n.id, name: n.name, namespace: ns, health: n.health, labels: n.labels, cells: { ready: n.ready, desired: n.desired } })

/** Kebab menu of one node: grid row actions (scale, restart, delete honor read-only) plus topology shortcuts. */
function KebabHost({ clusterId, ns, target, onSelect }: { clusterId: string; ns: string; target: { n: TopoNode; x: number; y: number }; onSelect: (n: TopoNode) => void }) {
  const res = findKind(clusterId, target.n.group, target.n.kind)
  const tab = (r: Row) => openObject(clusterId, target.n.group, target.n.kind, r.name, ns)
  const actions = useRowActions(clusterId, res, { drawer: () => onSelect(target.n), tab })
  useEffect(() => {
    const n = target.n
    const extra: MenuItem[] = []
    if (n.url) extra.push({ label: 'Abrir URL', icon: ExternalLink, onClick: () => window.open(n.url, '_blank', 'noopener') })
    if (n.pods[0]) extra.push({ label: `Logs (${n.pods[0].name})`, icon: Info, onClick: () => openPanel(`logs:${clusterId}:${ns}/${n.pods[0].name}`, 'logs', `logs ${n.pods[0].name}`, { clusterId, namespace: ns, pod: n.pods[0].name }) })
    const items = res ? actions.menuFor(rowOf(n, ns)) : [{ label: 'Ver detalhes', onClick: () => onSelect(n) }]
    openMenu({ clientX: target.x, clientY: target.y, preventDefault() {}, stopPropagation() {} }, extra.length ? [...items, { separator: true }, ...extra] : items)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target])
  return <>{actions.dialogs}</>
}

function SidePanel({ clusterId, ns, n, onClose, onKebab }: { clusterId: string; ns: string; n: TopoNode; onClose: () => void; onKebab: (n: TopoNode, e: React.MouseEvent) => void }) {
  const [tab, setTab] = useState<'details' | 'resources'>('resources')
  const [badge, cls] = badgeOf(n.kind)
  const total = podTotal(n.phases)
  return (
    <aside className="pt-side" data-testid="pt-side">
      <header>
        <span className={`pt-badge ${cls}`}>{badge}</span>
        <b className="ellipsis" title={n.name}>{n.name}</b>
        <button className="icon-btn" title="Ações" onClick={(e) => onKebab(n, e)}><MoreVertical size={15} /></button>
        <button className="icon-btn" title="Fechar" onClick={onClose}><X size={15} /></button>
      </header>
      <nav className="pt-tabs">
        <button className={tab === 'details' ? 'on' : ''} onClick={() => setTab('details')}>Detalhes</button>
        <button className={tab === 'resources' ? 'on' : ''} onClick={() => setTab('resources')}>Recursos</button>
      </nav>
      <div className="pt-side-body">
        {tab === 'details' ? (
          <>
            <div className="pt-side-ring">
              <svg width={120} height={120}><PodRing phases={n.phases} size={120} /><circle className="pt-core" cx={60} cy={60} r={44} /></svg>
              <span className="pt-center"><span className="pt-count big"><b>{total}</b><small>{total === 1 ? 'pod' : 'pods'}</small></span></span>
            </div>
            <dl className="pt-dl">
              <dt>Tipo</dt><dd>{n.kind}</dd>
              <dt>Namespace</dt><dd>{ns}</dd>
              {n.status && (<><dt>Status</dt><dd>{n.status}</dd></>)}
              {n.ready && (<><dt>Prontos</dt><dd>{n.ready}</dd></>)}
              {n.desired !== undefined && (<><dt>Réplicas desejadas</dt><dd>{n.desired}</dd></>)}
              {n.app && (<><dt>Aplicação</dt><dd>{n.app}</dd></>)}
              {n.release && (<><dt>Helm release</dt><dd>{n.release}</dd></>)}
              {n.runtime && (<><dt>Runtime</dt><dd>{n.runtime}</dd></>)}
            </dl>
            {n.labels && Object.keys(n.labels).length > 0 && (
              <div className="pt-chips">{Object.entries(n.labels).map(([k, v]) => <span key={k}>{k}={v}</span>)}</div>
            )}
            <button onClick={() => openObject(clusterId, n.group, n.kind, n.name, ns)}>Abrir objeto completo</button>
          </>
        ) : (
          <>
            <h5>Pods</h5>
            {!n.pods.length && <div className="muted small">Nenhum pod (escalado para zero ou aguardando).</div>}
            {n.pods.map((p) => (
              <div key={p.name} className="pt-res">
                <i className={`pt-dot ${p.phase}`} />
                <a className="ellipsis" onClick={() => openObject(clusterId, '', 'Pod', p.name, ns)}>{p.name}</a>
                <span className="muted small">{PHASES.find(([k]) => k === p.phase)?.[1]}{p.restarts ? ` · ${p.restarts} reinícios` : ''}</span>
                <button className="link" onClick={() => openPanel(`logs:${clusterId}:${ns}/${p.name}`, 'logs', `logs ${p.name}`, { clusterId, namespace: ns, pod: p.name })}>Logs</button>
              </div>
            ))}
            {n.build && (
              <>
                <h5>Builds</h5>
                <div className="pt-res">
                  <BuildIcon phase={n.build.phase} />
                  <a className="ellipsis" onClick={() => openObject(clusterId, 'build.openshift.io', 'BuildConfig', n.build!.config, ns)}>{n.build.config}</a>
                  <span className="muted small">{n.build.name ? `${n.build.name}: ${n.build.phase}` : 'sem builds'}</span>
                </div>
              </>
            )}
            <h5>Services</h5>
            {!n.services.length && <div className="muted small">Nenhum service seleciona este workload.</div>}
            {n.services.map((s) => (
              <div key={s} className="pt-res"><span className="pt-badge other">S</span><a className="ellipsis" onClick={() => openObject(clusterId, '', 'Service', s, ns)}>{s}</a></div>
            ))}
            {n.url && (
              <>
                <h5>Rotas</h5>
                <div className="pt-res"><ExternalLink size={13} /><a className="ellipsis" onClick={() => window.open(n.url, '_blank', 'noopener')}>{n.url}</a></div>
              </>
            )}
          </>
        )}
      </div>
    </aside>
  )
}

// --- Panel --------------------------------------------------------------------------------------

function Controls() {
  const { zoomIn, zoomOut, fitView } = useReactFlow()
  return (
    <div className="pt-zoom">
      <button title="Aumentar" onClick={() => zoomIn({ duration: 150 })}><Plus size={14} /></button>
      <button title="Diminuir" onClick={() => zoomOut({ duration: 150 })}><Minus size={14} /></button>
      <button title="Ajustar à tela" data-testid="pt-fit" onClick={() => fitView({ padding: 0.15, maxZoom: 1.2, duration: 250 })}><Maximize size={14} /></button>
    </div>
  )
}

function matches(n: TopoNode, mode: 'name' | 'label', q: string) {
  const t = q.trim().toLowerCase()
  if (!t) return false
  if (mode === 'name') return n.name.toLowerCase().includes(t)
  const [k, v] = t.split('=')
  return Object.entries(n.labels ?? {}).some(([lk, lv]) => lk.toLowerCase().includes(k) && (v === undefined || lv.toLowerCase().includes(v)))
}

function ProjectTopologyView({ clusterId }: { clusterId: string }) {
  const theme = useTheme((s) => s.theme)
  const stored = useApp((s) => s.namespace[clusterId] ?? '')
  const fallback = useApp((s) => s.connected[clusterId]?.defaultNamespace ?? '')
  const ns = stored || fallback
  const [data, setData] = useState<ProjectTopology>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [app, setApp] = useState('')
  const [dep, setDep] = useState('') // one deployment and what links to it; '' = whole namespace
  const [hiddenKinds, setHiddenKinds] = useState<Set<string>>(new Set())
  const [find, setFind] = useState<{ mode: 'name' | 'label'; q: string }>({ mode: 'name', q: '' })
  const [display, setDisplay] = useState<Display>({ podCount: false, labels: false, apps: true, releases: true })
  const [toggled, setToggled] = useState<Set<string>>(new Set()) // groups collapsed/expanded against the display default
  const [selected, setSelected] = useState<string>()
  const [kebab, setKebab] = useState<{ n: TopoNode; x: number; y: number }>()
  const findRef = useRef<HTMLInputElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const { fitView } = useReactFlow()
  const layoutKey = `${clusterId}|${ns}`
  const saved = useUi((s) => s.topoLayouts[layoutKey])
  const [locked, setLocked] = useState(false)

  const last = useRef('')
  const load = useCallback(async () => {
    if (!ns) return
    setLoading(true)
    try {
      const next = await getProjectTopology(clusterId, ns)
      // The periodic refresh usually returns the same thing: keep the old object so nothing re-renders.
      const sig = JSON.stringify(next)
      if (sig !== last.current) {
        last.current = sig
        setData(next)
      }
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [clusterId, ns])

  useEffect(() => {
    setData(undefined)
    last.current = ''
    setSelected(undefined)
    setApp('')
    setDep('')
    load()
    const t = setInterval(() => document.visibilityState === 'visible' && rootRef.current?.offsetParent && load(), REFRESH_MS)
    return () => clearInterval(t)
  }, [load])

  // "/" focuses the search, as in the console.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      if (e.key !== '/' || !rootRef.current?.offsetParent || (el && ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return
      e.preventDefault()
      findRef.current?.focus()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const kinds = useMemo(() => [...new Set((data?.nodes ?? []).map((n) => n.kind))].sort(), [data])
  const families = useMemo(() => familiesOf(data?.nodes ?? []), [data])
  // Workloads without part-of get their family's main name as application, so the canvas groups
  // tcmgo-web-x-hmg with its -redis/-sidekiq/-migration companions like an OpenShift application.
  const appOf = useMemo(() => {
    const m = new Map<string, string>()
    for (const f of families) if (f.members.length > 1) for (const n of f.members) m.set(n.id, n.app || f.main.name)
    return m
  }, [families])
  const apps = useMemo(() => [...new Set((data?.nodes ?? []).map((n) => n.app || appOf.get(n.id)).filter(Boolean) as string[])].sort(), [data, appOf])
  const visible = useMemo(() => {
    let keep: Set<string> | undefined
    if (dep) {
      const fam = families.find((f) => f.key === dep)
      keep = new Set(fam ? fam.members.map((n) => n.id) : [])
      const ids = new Set(keep)
      for (const e of data?.edges ?? []) {
        if (ids.has(e.source)) keep.add(e.target)
        if (ids.has(e.target)) keep.add(e.source)
      }
    }
    return (data?.nodes ?? []).filter((n) => {
      if (keep) return keep.has(n.id)
      const a = n.app || appOf.get(n.id)
      return !hiddenKinds.has(n.kind) && (app === '' || (app === NO_APP ? !a : a === app))
    })
  }, [data, hiddenKinds, app, dep, families, appOf])
  // Picking an application opens the side panel of its main deployment right away.
  useEffect(() => setSelected(families.find((f) => f.key === dep)?.main.id), [dep]) // eslint-disable-line react-hooks/exhaustive-deps
  const searching = find.q.trim() !== ''
  const placed = useMemo(() => {
    // Display options turn the grouping itself off; the +/- on a group collapses that one group.
    const prepared = visible.map((n) => ({ ...n, app: display.apps ? n.app || appOf.get(n.id) : undefined, release: display.releases ? n.release : undefined }))
    return place(blocksOf(prepared), (b) => b.kind !== 'solo' && toggled.has(b.key))
  }, [visible, display.apps, display.releases, toggled, appOf])

  const selectNode = useCallback((n: TopoNode) => setSelected(n.id), [])
  const onKebab = useCallback((n: TopoNode, e: React.MouseEvent) => setKebab({ n, x: e.clientX, y: e.clientY }), [])
  const onBuild = useCallback((n: TopoNode) => n.build && openObject(clusterId, 'build.openshift.io', n.build.name ? 'Build' : 'BuildConfig', n.build.name ?? n.build.config, ns), [clusterId, ns])
  const onOpen = useCallback((n: TopoNode) => openObject(clusterId, n.group, n.kind, n.name, ns), [clusterId, ns])
  const onToggle = useCallback((b: Block) => setToggled((s) => { const x = new Set(s); x.has(b.key) ? x.delete(b.key) : x.add(b.key); return x }), [])

  const { nodes, edges } = useMemo(() => {
    const nodes: Node[] = []
    const where = new Map<string, string>() // workload id -> node id on the canvas (itself or its collapsed group)
    for (const p of placed.blocks) {
      const hit = searching && p.block.nodes.some((n) => matches(n, find.mode, find.q))
      if (p.block.kind === 'solo') continue
      nodes.push({ id: p.block.key, type: 'tgroup', position: { x: p.x, y: p.y }, style: { width: p.w, height: p.h }, zIndex: 0,
        data: { block: p.block, collapsed: p.collapsed, dim: searching && !hit, hit: p.collapsed && hit, onToggle } satisfies GroupData })
      if (p.collapsed) for (const n of p.block.nodes) where.set(n.id, p.block.key)
    }
    const solo = new Map(placed.blocks.filter((p) => p.block.kind === 'solo').map((p) => [p.block.key, p]))
    for (const p of placed.nodes) {
      const s = solo.get(p.parent)
      const hit = searching && matches(p.node, find.mode, find.q)
      nodes.push({
        id: p.node.id, type: 'workload', zIndex: 1, width: NODE_W,
        ...(s ? { position: { x: s.x, y: s.y } } : { position: { x: p.x, y: p.y }, parentId: p.parent, expandParent: true }),
        data: { n: p.node, display, hit, dim: searching && !hit, selected: selected === p.node.id, onSelect: selectNode, onKebab, onBuild, onOpen } satisfies WorkloadData,
      })
      where.set(p.node.id, p.node.id)
    }
    const edges: Edge[] = []
    for (const e of data?.edges ?? []) {
      const s = where.get(e.source), t = where.get(e.target)
      if (s && t && s !== t) edges.push({ id: `${s}->${t}`, source: s, target: t, className: 'pt-edge', markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 } })
    }
    // Positions and box sizes the user dragged win over the automatic layout.
    if (saved) {
      for (const n of nodes) {
        const p = saved[n.id]
        if (!p) continue
        n.position = { x: p.x, y: p.y }
        if (n.type === 'tgroup' && p.w && p.h) n.style = { ...n.style, width: p.w, height: p.h }
      }
    }
    return { nodes, edges }
  }, [saved, placed, data, display, searching, find, selected, selectNode, onKebab, onBuild, onOpen, onToggle])

  // React Flow owns the nodes while they are dragged; a refresh or filter change rebuilds them.
  const [rfNodes, setRfNodes] = useState<Node[]>([])
  const rfRef = useRef<Node[]>([])
  const dragging = useRef(false)
  useEffect(() => {
    // A refresh during a drag waits: saving the layout at drag end rebuilds the nodes anyway.
    if (!dragging.current) setRfNodes(nodes)
  }, [nodes])
  useEffect(() => { rfRef.current = rfNodes }, [rfNodes])
  const onNodesChange = useCallback((changes: NodeChange[]) => setRfNodes((nds) => applyNodeChanges(changes, nds)), [])
  const saveLayout = useCallback(() => {
    const out: Record<string, TopoPos> = {}
    for (const n of rfRef.current) {
      const w = n.type === 'tgroup' ? Number(n.width ?? n.measured?.width ?? n.style?.width) : undefined
      const h = n.type === 'tgroup' ? Number(n.height ?? n.measured?.height ?? n.style?.height) : undefined
      out[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y), ...(w && h ? { w: Math.round(w), h: Math.round(h) } : {}) }
    }
    // Most recent layouts last; only the last MAX_LAYOUTS survive, so the UI state (capped by the
    // shell at 2 MB, beyond which nothing saves) cannot grow without bound.
    const { [layoutKey]: prev, ...rest } = useUi.getState().topoLayouts
    const keep = Object.entries(rest).slice(-(MAX_LAYOUTS - 1))
    useUi.getState().update({ topoLayouts: { ...Object.fromEntries(keep), [layoutKey]: { ...prev, ...out } } })
  }, [layoutKey])
  const resetLayout = () => {
    const { [layoutKey]: _drop, ...rest } = useUi.getState().topoLayouts
    useUi.getState().update({ topoLayouts: rest })
    requestAnimationFrame(() => fitView({ padding: 0.15, maxZoom: 1.2, duration: 250 }))
  }

  // Fit once per project (and when the filters change the shape), not on every refresh.
  const shape = `${ns}|${dep}|${app}|${[...hiddenKinds].join()}|${display.apps}|${display.releases}|${[...toggled].join()}|${!!data}`
  useEffect(() => {
    if (data) requestAnimationFrame(() => fitView({ padding: 0.15, maxZoom: 1.2 }))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shape])

  const sel = data?.nodes.find((n) => n.id === selected)
  const setNs = (v: string) => useApp.getState().setNamespace(clusterId, v)
  const check = (on: boolean, label: string, onChange: () => void) => (
    <label className="pt-check-row"><input type="checkbox" checked={on} onChange={onChange} /> {label}</label>
  )

  return (
    <div className="pt-panel" ref={rootRef} data-testid="projtopo-panel">
      <div className="pt-bar">
        <DeploymentPicker clusterId={clusterId} ns={ns} families={families} value={dep} onChange={setDep} />
        {!dep && <AppPicker clusterId={clusterId} apps={apps} value={app} onChange={setApp} />}
      </div>
      <div className="pt-bar sub">
        <ProjectPicker clusterId={clusterId} value={ns} onChange={setNs} />
        <Popover testid="pt-display" label={<><SlidersHorizontal size={14} /> Opções de exibição</>}>
          {() => (
            <div className="pt-pop-body">
              <div className="pt-pop-head">Nó</div>
              {check(display.podCount, 'Contagem de pods', () => setDisplay((d) => ({ ...d, podCount: !d.podCount })))}
              {check(display.labels, 'Labels', () => setDisplay((d) => ({ ...d, labels: !d.labels })))}
              <div className="pt-pop-head">Agrupar</div>
              {check(display.apps, 'Grupos de aplicação', () => setDisplay((d) => ({ ...d, apps: !d.apps })))}
              {check(display.releases, 'Helm releases', () => setDisplay((d) => ({ ...d, releases: !d.releases })))}
            </div>
          )}
        </Popover>
        <Popover testid="pt-kinds" label={<><Filter size={14} /> Filtrar por recurso{hiddenKinds.size ? ` (${kinds.length - hiddenKinds.size})` : ''}</>}>
          {() => (
            <div className="pt-pop-body">
              {kinds.map((k) => check(!hiddenKinds.has(k), k, () => setHiddenKinds((s) => { const x = new Set(s); x.has(k) ? x.delete(k) : x.add(k); return x })))}
              {!kinds.length && <div className="muted small">Nada neste projeto.</div>}
            </div>
          )}
        </Popover>
        <div className="pt-find">
          <select value={find.mode} onChange={(e) => setFind((f) => ({ ...f, mode: e.target.value as 'name' | 'label' }))} title="Buscar por">
            <option value="name">Nome</option>
            <option value="label">Label</option>
          </select>
          <input ref={findRef} data-testid="pt-find" placeholder={find.mode === 'name' ? 'Buscar por nome…' : 'Buscar por label (chave=valor)…'} value={find.q}
            onChange={(e) => setFind((f) => ({ ...f, q: e.target.value }))} onKeyDown={(e) => e.key === 'Escape' && setFind((f) => ({ ...f, q: '' }))} />
          <kbd>/</kbd>
        </div>
        <span className="spacer" />
        {loading && <span className="spinner" />}
        <button className={locked ? 'pt-on' : ''} data-testid="pt-lock" onClick={() => setLocked((v) => !v)} title={locked ? 'Layout travado: clique para mover caixas e nós' : 'Arraste caixas e nós livremente; clique para travar'}>
          {locked ? <Lock size={14} /> : <LockOpen size={14} />} {locked ? 'Layout travado' : 'Travar layout'}
        </button>
        <button data-testid="pt-reset" disabled={!saved} onClick={resetLayout} title="Descarta as posições movidas e volta ao layout automático"><LayoutGrid size={14} /> Reorganizar</button>
        <button onClick={load} title="Recarregar (atualiza sozinho a cada 15 s)"><RefreshCw size={14} /> Recarregar</button>
      </div>
      <Partial errors={data?.errors} />
      {error && <ErrorBanner error={error} />}
      <div className="pt-body">
        <div className="pt-canvas">
          {!ns ? (
            <div className="pt-empty">Escolha um projeto.</div>
          ) : !data ? (
            <div className="pt-empty"><span className="spinner" /></div>
          ) : !nodes.length ? (
            <div className="pt-empty">Nenhum workload {visible.length !== data.nodes.length ? 'com estes filtros' : 'neste projeto'}.</div>
          ) : null}
          <ReactFlow nodes={rfNodes} edges={edges} nodeTypes={nodeTypes} colorMode={theme} minZoom={0.1} maxZoom={2.5} nodesConnectable={false}
            nodesDraggable={!locked} onNodesChange={onNodesChange} onNodeDragStart={() => { dragging.current = true }}
            onNodeDragStop={() => {
              dragging.current = false
              requestAnimationFrame(saveLayout)
            }}
            proOptions={{ hideAttribution: true }} onPaneClick={() => setSelected(undefined)}>
            <Controls />
          </ReactFlow>
        </div>
        {sel && <SidePanel clusterId={clusterId} ns={ns} n={sel} onClose={() => setSelected(undefined)} onKebab={onKebab} />}
      </div>
      {kebab && <KebabHost key={`${kebab.n.id}:${kebab.x}:${kebab.y}`} clusterId={clusterId} ns={ns} target={kebab} onSelect={selectNode} />}
    </div>
  )
}

export function ProjectTopologyPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <ReactFlowProvider>
        <ProjectTopologyView clusterId={params.clusterId} />
      </ReactFlowProvider>
    </ClusterGate>
  )
}
