import { useEffect, useMemo, useRef, useState } from 'react'
import { ChevronDown, ChevronRight, EllipsisVertical, LayoutGrid, Lock, LockOpen, Pencil, Plug, Search, Tags, TriangleAlert } from 'lucide-react'
import type { ClusterInfo, Resource } from '../../api'
import { clusterName, contextParts, ENV_LABEL, hostTag, openPanel, useApp, useIdentity, useUi, type ClusterIdentity, type OpenOptions } from '../../store'
import { openTool } from '../workspace/tools'
import { buildSections, kindIcon, plural, type NavSection } from './nav'
import { clusterMenu, openLogin, openMenu, openProfile, unlockWrites } from './menu'

export function gridPanelId(clusterId: string, r: Pick<Resource, 'group' | 'resource'>) {
  return `grid:${clusterId}:${r.group}/${r.resource}`
}

export function openGrid(clusterId: string, r: Resource, opts?: OpenOptions) {
  openPanel(gridPanelId(clusterId, r), 'grid', plural(r.kind), { clusterId, group: r.group, resource: r.resource, kind: r.kind }, opts)
}

// Panel id of each navigator tool, used to highlight the item of the active tab.
const TOOL_PANEL: Record<string, (id: string) => string> = {
  overview: (id) => `overview:${id}`, topology: (id) => `topology:${id}`, map: (id) => `map:${id}`, timeline: (id) => `timeline:${id}`, rbac: (id) => `rbac:${id}`,
  security: (id) => `security:${id}`, netpol: (id) => `netpol:${id}`, helm: (id) => `helm-releases:${id}`, charts: () => 'helm-charts',
  problems: (id) => `problems:${id}`, copilot: (id) => `copilot:${id}`,
}

export function providerLabel(p: string) {
  return { eks: 'EKS', gke: 'GKE', aks: 'AKS', openshift: 'OCP', local: 'LOCAL' }[p] ?? 'K8S'
}

/** Avatar of a cluster identity: icon or short label on its color, with an environment strip. */
export function AvatarView({ id, size = 34, status }: { id: ClusterIdentity; size?: number; status?: string }) {
  const len = id.icon ? 1 : id.short.length
  const font = id.icon ? size * 0.5 : size * (len <= 2 ? 0.36 : len === 3 ? 0.3 : 0.25)
  return (
    <span className={`avatar env-${id.env}`} style={{ '--cc': id.color, width: size, height: size, fontSize: font } as React.CSSProperties} data-env={id.env}>
      <span className="avatar-text">{id.icon || id.short}</span>
      {id.env !== 'none' && size >= 26 && <span className="avatar-env">{ENV_LABEL[id.env]}</span>}
      {status && <span className={`avatar-status st-${status}`} />}
    </span>
  )
}

/** Square cluster avatar with the connection status dot. */
export function ClusterAvatar({ c, size = 34 }: { c: ClusterInfo; size?: number }) {
  const id = useIdentity(c)
  const connected = useApp((s) => !!s.connected[c.id])
  const connecting = useApp((s) => s.connecting[c.id])
  const failed = useApp((s) => !!s.connectError[c.id])
  const status = connecting ? 'connecting' : connected ? 'connected' : failed ? 'failed' : 'off'
  return <AvatarView id={id} size={size} status={status} />
}

/** Hover card of a hotbar item: full identity and state, so pinned clusters are told apart without clicking. */
function HotCard({ c, top, left }: { c: ClusterInfo; top: number; left: number }) {
  const id = useIdentity(c)
  const st = useApp((s) => s.connected[c.id])
  const err = useApp((s) => s.connectError[c.id])
  return (
    <div className="hot-card" style={{ top, left, '--cc': id.color } as React.CSSProperties} role="tooltip" data-testid="hot-card">
      <div className="hot-card-head">
        <ClusterAvatar c={c} size={36} />
        <div className="cl-main">
          <b className="ellipsis">{id.name}</b>
          <span className="muted small ellipsis">{c.name}</span>
        </div>
      </div>
      <div className="hot-card-rows">
        {id.host && <span><i>cluster</i>{id.host}</span>}
        <span><i>servidor</i>{c.server.replace(/^https?:\/\//, '')}</span>
        <span><i>tipo</i>{providerLabel(c.provider)}{id.env !== 'none' ? ` · ${ENV_LABEL[id.env]}` : ''}{c.prod ? ' · PRODUÇÃO' : ''}</span>
        <span><i>estado</i>{st ? `conectado · ${st.version} · ${st.readOnly ? 'somente leitura' : 'leitura e escrita'}` : err ? 'falhou ao conectar' : 'desconectado'}</span>
      </div>
      <div className="muted small">Clique: abrir · botão direito: ações · arraste para reordenar</div>
    </div>
  )
}

function HotItem({ c, active, labels, onHover, drag }: {
  c: ClusterInfo; active: boolean; labels: boolean; onHover: (c?: ClusterInfo, el?: HTMLElement) => void
  drag: { over?: string; start: (id: string) => void; enter: (id: string) => void; drop: (id: string) => void; end: () => void }
}) {
  const id = useIdentity(c)
  // Contexts with the same readable name on different clusters get the cluster tag in the label.
  const twin = useApp((s) => id.name === contextParts(c.name).base && !!id.host && s.clusters.some((o) => o.id !== c.id && contextParts(o.name).base === contextParts(c.name).base))
  const label = twin ? `${id.name} · ${hostTag(id.host)}` : id.name
  const select = () => {
    useApp.getState().setActiveCluster(c.id)
    if (useUi.getState().sidebarHidden) useUi.getState().update({ sidebarHidden: false })
  }
  return (
    <button
      className={`hot-item ${active ? 'active' : ''} ${drag.over === c.id ? 'drop-before' : ''}`}
      style={{ '--cc': id.color } as React.CSSProperties}
      aria-label={id.name}
      data-testid={`hotbar-${c.name}`}
      draggable
      onDragStart={(e) => {
        onHover()
        e.dataTransfer.effectAllowed = 'move'
        e.dataTransfer.setData('text/plain', c.id)
        drag.start(c.id)
      }}
      onDragOver={(e) => {
        e.preventDefault()
        drag.enter(c.id)
      }}
      onDrop={(e) => {
        e.preventDefault()
        drag.drop(c.id)
      }}
      onDragEnd={drag.end}
      onMouseEnter={(e) => onHover(c, e.currentTarget)}
      onMouseLeave={() => onHover()}
      onClick={select}
      onContextMenu={(e) => {
        onHover()
        openMenu(e, clusterMenu(c))
      }}
    >
      <ClusterAvatar c={c} size={labels ? 34 : 36} />
      {labels && <span className="hot-label">{label}</span>}
    </button>
  )
}

/** Hotbar: pinned and connected clusters (drag to reorder); the top button shows every context. */
export function Hotbar() {
  const clusters = useApp((s) => s.clusters)
  const connected = useApp((s) => s.connected)
  const connecting = useApp((s) => s.connecting)
  const active = useApp((s) => s.activeCluster)
  const pinned = useUi((s) => s.pinned)
  const labels = useUi((s) => s.hotbarLabels)
  const [hover, setHover] = useState<{ c: ClusterInfo; top: number; left: number }>()
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const [dragging, setDragging] = useState<string>()
  const [over, setOver] = useState<string>()
  const shown = useMemo(() => {
    const ids = [...pinned, ...clusters.filter((c) => connected[c.id] || connecting[c.id]).map((c) => c.id)]
    return [...new Set(ids)].map((id) => clusters.find((c) => c.id === id)).filter(Boolean) as ClusterInfo[]
  }, [clusters, connected, connecting, pinned])
  const onHover = (c?: ClusterInfo, el?: HTMLElement) => {
    clearTimeout(timer.current)
    if (!c || !el || dragging) return setHover(undefined)
    const r = el.getBoundingClientRect()
    timer.current = setTimeout(() => setHover({ c, top: Math.min(r.top, window.innerHeight - 190), left: r.right + 10 }), 300)
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  // Dropping puts the dragged cluster before the target and pins it, so the order sticks.
  const drag = {
    over,
    start: (id: string) => setDragging(id),
    enter: (id: string) => dragging && id !== dragging && setOver(id),
    end: () => (setDragging(undefined), setOver(undefined)),
    drop: (target: string) => {
      if (dragging && dragging !== target) {
        const order = shown.map((c) => c.id).filter((id) => id !== dragging)
        order.splice(order.indexOf(target), 0, dragging)
        const keep = new Set([...pinned, dragging])
        useUi.getState().update({ pinned: order.filter((id) => keep.has(id)) })
      }
      setDragging(undefined)
      setOver(undefined)
    },
  }
  return (
    <nav className={`hotbar ${labels ? 'with-labels' : ''}`} aria-label="Clusters">
      <button className={`hot-btn ${!active ? 'active' : ''}`} title="Todos os clusters" data-testid="hotbar-catalog" onClick={() => useApp.getState().setActiveCluster(undefined)}>
        <LayoutGrid size={18} />
      </button>
      <div className="hot-sep" />
      <div className="hot-list" onScroll={() => onHover()}>
        {shown.map((c) => <HotItem key={c.id} c={c} active={active === c.id} labels={labels} onHover={onHover} drag={drag} />)}
      </div>
      <div className="hot-foot">
        <button className={`hot-btn small ${labels ? 'on' : ''}`} title={labels ? 'Ocultar nomes na hotbar' : 'Mostrar nomes na hotbar'} data-testid="hotbar-labels"
          onClick={() => useUi.getState().update({ hotbarLabels: !labels })}>
          <Tags size={15} />
        </button>
      </div>
      {hover && <HotCard {...hover} />}
    </nav>
  )
}

/** Every kubeconfig context; one click connects and opens the cluster in the navigator. Nothing connects by itself. */
export function ClusterList({ compact }: { compact?: boolean }) {
  const clusters = useApp((s) => s.clusters)
  const connected = useApp((s) => s.connected)
  const pinned = useUi((s) => s.pinned)
  const [q, setQ] = useState('')
  const f = q.toLowerCase()
  const shown = clusters.filter((c) => !f || c.name.toLowerCase().includes(f) || clusterName(c.id).toLowerCase().includes(f) || c.server.toLowerCase().includes(f))
  const groups: [string, ClusterInfo[]][] = [
    ['Conectados', shown.filter((c) => connected[c.id])],
    ['Fixados', shown.filter((c) => !connected[c.id] && pinned.includes(c.id))],
    ['Contextos do kubeconfig', shown.filter((c) => !connected[c.id] && !pinned.includes(c.id))],
  ]
  return (
    <div className={`cluster-list ${compact ? 'compact' : ''}`}>
      <label className="search-box">
        <Search size={14} />
        <input placeholder={`Buscar em ${clusters.length} contextos…`} value={q} onChange={(e) => setQ(e.target.value)} />
      </label>
      {groups.filter(([, l]) => l.length).map(([title, list]) => (
        <div key={title} className="cl-group">
          <div className="cl-title">{title} <span className="muted">{list.length}</span></div>
          {list.map((c) => <ClusterRow key={c.id} c={c} />)}
        </div>
      ))}
      {!clusters.length && <div className="muted pad">Nenhum contexto no kubeconfig.</div>}
    </div>
  )
}

type CardFilter = 'all' | 'connected' | 'pinned' | 'prod'

/** Home catalog: one card per kubeconfig context. */
export function ClusterCards() {
  const clusters = useApp((s) => s.clusters)
  const connected = useApp((s) => s.connected)
  const pinned = useUi((s) => s.pinned)
  const [q, setQ] = useState('')
  const [f, setF] = useState<CardFilter>('all')
  const term = q.toLowerCase()
  const shown = clusters
    .filter((c) => !term || c.name.toLowerCase().includes(term) || clusterName(c.id).toLowerCase().includes(term) || c.server.toLowerCase().includes(term))
    .filter((c) => f === 'all' || (f === 'connected' ? !!connected[c.id] : f === 'pinned' ? pinned.includes(c.id) : c.prod))
    .sort((a, b) => Number(!!connected[b.id]) - Number(!!connected[a.id]) || Number(pinned.includes(b.id)) - Number(pinned.includes(a.id)) || a.name.localeCompare(b.name))
  const labels: [CardFilter, string][] = [['all', 'Todos'], ['connected', 'Conectados'], ['pinned', 'Fixados'], ['prod', 'Produção']]
  return (
    <>
      <div className="home-bar">
        <label className="search-box">
          <Search size={14} />
          <input placeholder={`Buscar em ${clusters.length} contextos…`} value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
        <div className="seg">
          {labels.map(([k, l]) => <button key={k} className={f === k ? 'on' : ''} onClick={() => setF(k)}>{l}</button>)}
        </div>
      </div>
      <div className="cluster-cards">
        {shown.map((c) => <ClusterCard key={c.id} c={c} />)}
        {!shown.length && <div className="empty">Nenhum contexto com esse filtro.</div>}
      </div>
    </>
  )
}

function ClusterCard({ c }: { c: ClusterInfo }) {
  const st = useApp((s) => s.connected[c.id])
  const connecting = useApp((s) => s.connecting[c.id])
  const err = useApp((s) => s.connectError[c.id])
  const id = useIdentity(c)
  const color = id.color
  const pinned = useUi((s) => s.pinned.includes(c.id))
  const open = async () => {
    const app = useApp.getState()
    if (st) return app.setActiveCluster(c.id)
    if (!connecting) await app.connect(c.id)
  }
  return (
    <div className="cluster-card" style={{ '--cc': color } as React.CSSProperties} onClick={open} onContextMenu={(e) => openMenu(e, clusterMenu(c))} data-testid={`cluster-card-${c.name}`} title={c.source}>
      <div className="cc-top">
        <ClusterAvatar c={c} size={38} />
        <div className="cl-main">
          <span className="cc-name" title={c.name}>{id.name}</span>
          <span className="cc-server">{id.host ? `${id.host} · ` : ''}{c.server.replace(/^https?:\/\//, '')}</span>
        </div>
        <button className="icon-btn" title="Personalizar" onClick={(e) => (e.stopPropagation(), openProfile(c))}><Pencil size={14} /></button>
        <button className="icon-btn" title="Ações" onClick={(e) => openMenu(e, clusterMenu(c))}><EllipsisVertical size={16} /></button>
      </div>
      <div className="cc-tags">
        <span className={`prov prov-${c.provider}`}>{providerLabel(c.provider)}</span>
        {c.prod && <span className="badge badge-prod" title="Servidor de produção: a escrita só é liberada digitando o nome do contexto">{id.env === 'prod' || id.env === 'none' ? 'PROD' : 'CLUSTER PROD'}</span>}
        {st && <span className="badge badge-ro">{st.readOnly ? 'SOMENTE LEITURA' : 'ESCRITA'}</span>}
        {pinned && <span className="badge badge-ro">FIXADO</span>}
        {id.env !== 'none' && !(c.prod && id.env === 'prod') && <span className={`env-tag env-${id.env}`}>{ENV_LABEL[id.env]}</span>}
        {c.namespace && <span className="muted small">ns: {c.namespace}</span>}
      </div>
      <div className="cc-foot">
        {connecting ? (
          <span className="cc-state"><span className="spinner" /> Conectando…</span>
        ) : st ? (
          <span className="cc-state on"><span className="dot d-ok" />Conectado · {st.version}</span>
        ) : err ? (
          <span className="cc-state err ellipsis" title={err.message}><TriangleAlert size={12} /> {err.message}</span>
        ) : (
          <span className="cc-state"><span className="dot d-neutral" />Desconectado</span>
        )}
        <span className="spacer" />
        {st ? (
          <button onClick={(e) => { e.stopPropagation(); useApp.getState().setActiveCluster(c.id) }}>Abrir</button>
        ) : (
          <button className="primary" disabled={connecting} onClick={(e) => { e.stopPropagation(); open() }}><Plug size={13} /> Conectar</button>
        )}
      </div>
    </div>
  )
}

function ClusterRow({ c }: { c: ClusterInfo }) {
  const id = useIdentity(c)
  const st = useApp((s) => s.connected[c.id])
  const connecting = useApp((s) => s.connecting[c.id])
  const err = useApp((s) => s.connectError[c.id])
  const open = async () => {
    const app = useApp.getState()
    if (st) return app.setActiveCluster(c.id)
    if (!connecting) await app.connect(c.id)
  }
  return (
    <div className="cluster-row-wrap">
      <div className={`cluster-row ${st ? 'is-connected' : ''}`} onClick={open} onContextMenu={(e) => openMenu(e, clusterMenu(c))} title={`${c.server}\n${c.source}`} data-testid={`cluster-row-${c.name}`}>
        <ClusterAvatar c={c} size={28} />
        <span className="cl-main">
          <span className="cluster-name">{id.name}</span>
          <span className="cl-sub">{id.host ?? c.server.replace(/^https?:\/\//, '')}</span>
        </span>
        <span className={`prov prov-${c.provider}`}>{providerLabel(c.provider)}</span>
        {c.prod && <span className="badge badge-prod" title="Servidor de produção: a escrita só é liberada digitando o nome do contexto">PROD</span>}
        {st?.readOnly && <span className="badge badge-ro" title="Somente leitura">RO</span>}
        {connecting && <span className="spinner" />}
        <button className="icon-btn" title="Ações" onClick={(e) => openMenu(e, clusterMenu(c))}><EllipsisVertical size={14} /></button>
      </div>
      {!st && err && (
        <div className="cluster-error small">
          <TriangleAlert size={12} className="s-error" />
          <span className="s-error ellipsis" title={err.message}>{err.message}</span>
          {(c.provider === 'openshift' || /unauthori|401|must be logged in|token/i.test(err.message)) && (
            <button className="link" onClick={() => openLogin(c)} data-testid="login-openshift">fazer login</button>
          )}
        </div>
      )}
    </div>
  )
}

/** Navigator of the active cluster: header with the cluster state and collapsible sections. */
function ClusterNavigator({ c }: { c: ClusterInfo }) {
  const st = useApp((s) => s.connected[c.id])
  const connecting = useApp((s) => s.connecting[c.id])
  const err = useApp((s) => s.connectError[c.id])
  const activePanel = useApp((s) => s.activePanelId)
  const collapsed = useUi((s) => s.collapsed)
  const id = useIdentity(c)
  const color = id.color
  const [filter, setFilter] = useState('')
  const sections = useMemo(() => (st ? buildSections(st.resources, filter) : []), [st, filter])
  const toggle = (id: string, def: boolean) => useUi.getState().update({ collapsed: { ...useUi.getState().collapsed, [id]: !(collapsed[id] ?? def) } })

  return (
    <div className="navigator" data-cluster={c.id}>
      <div className="nav-cluster" style={{ '--cc': color } as React.CSSProperties} onContextMenu={(e) => openMenu(e, clusterMenu(c))}>
        <ClusterAvatar c={c} size={30} />
        <div className="nav-cluster-main">
          <div className="nav-cluster-name">
            <span className="cluster-name" title={c.name}>{id.name}</span>
          </div>
          <div className="nav-cluster-sub">
            <span className={`prov prov-${c.provider}`}>{providerLabel(c.provider)}</span>
            {id.host && <span className="muted ellipsis" title={c.name}>{id.host}</span>}
            {c.prod && <span className="badge badge-prod" title="Servidor de produção: a escrita só é liberada digitando o nome do contexto">PROD</span>}
            {st && <span className="muted">{st.version}</span>}
          </div>
        </div>
        <button className="icon-btn" title="Personalizar cluster" data-testid="cluster-edit" onClick={() => openProfile(c)}><Pencil size={14} /></button>
        <button className="icon-btn" title="Ações do cluster" data-testid="cluster-menu" onClick={(e) => openMenu(e, clusterMenu(c))}><EllipsisVertical size={16} /></button>
      </div>
      {st && (
        <button
          className={`rw-pill ${st.readOnly ? 'ro' : 'rw'}`}
          data-testid="rw-toggle"
          title={st.readOnly ? 'Clique para liberar escrita' : 'Clique para voltar a somente leitura'}
          onClick={() => (st.readOnly ? unlockWrites(c) : useApp.getState().setReadOnly(c.id, true))}
        >
          {st.readOnly ? <Lock size={12} /> : <LockOpen size={12} />}
          {st.readOnly ? <><span className="badge badge-ro">RO</span> Somente leitura</> : 'Leitura e escrita'}
        </button>
      )}
      {!st && (
        <div className="nav-connect">
          <p className="muted small">Este cluster não está conectado.</p>
          <button className="primary" disabled={connecting} onClick={() => useApp.getState().connect(c.id)} data-testid="nav-connect">
            <Plug size={14} /> {connecting ? 'Conectando…' : 'Conectar'}
          </button>
          {err && <p className="s-error small wrap">{err.message}</p>}
          {err && <button className="link" onClick={() => openLogin(c)}>fazer login</button>}
        </div>
      )}
      {st && (
        <>
          <label className="search-box nav-filter">
            <Search size={14} />
            <input placeholder="Filtrar…" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </label>
          <div className="nav-tree">
            {sections.map((s) => (
              <Section key={s.id} s={s} c={c} open={!!filter || !(collapsed[s.id] ?? s.defaultCollapsed)} onToggle={() => toggle(s.id, !!s.defaultCollapsed)} activePanel={activePanel} />
            ))}
          </div>
        </>
      )}
    </div>
  )
}

function Section({ s, c, open, onToggle, activePanel }: { s: NavSection; c: ClusterInfo; open: boolean; onToggle: () => void; activePanel?: string }) {
  const Icon = s.icon
  return (
    <div className="nav-section" data-section={s.id}>
      <div className="nav-section-head" onClick={onToggle}>
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        <Icon size={15} />
        <span className="cat-title">{s.label}</span>
      </div>
      {open && (
        <div className="nav-items">
          {s.tools?.map((t) => {
            const id = TOOL_PANEL[t.id]?.(c.id)
            return (
              <NavItem key={t.id} icon={t.icon} label={t.label} active={!!id && id === activePanel} testid={`tool-${t.id}`}
                onOpen={(pin) => openTool(t.id, c.id, { preview: !pin, from: 'nav' })} />
            )
          })}
          {s.kinds.map((r) => <KindItem key={r.group + r.resource} r={r} c={c} activePanel={activePanel} />)}
          {s.groups?.map((g) => <CrdGroup key={g.group} group={g.group} kinds={g.kinds} c={c} activePanel={activePanel} />)}
        </div>
      )}
    </div>
  )
}

function CrdGroup({ group, kinds, c, activePanel }: { group: string; kinds: Resource[]; c: ClusterInfo; activePanel?: string }) {
  const key = `crd:${group}`
  const open = !useUi((s) => s.collapsed[key] ?? true)
  return (
    <div className="nav-crd">
      <div className="nav-item nav-crd-head" onClick={() => useUi.getState().update({ collapsed: { ...useUi.getState().collapsed, [key]: open } })} title={group}>
        {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
        <span className="ellipsis">{group}</span>
        <span className="muted small">{kinds.length}</span>
      </div>
      {open && kinds.map((r) => <KindItem key={r.resource} r={r} c={c} activePanel={activePanel} indent />)}
    </div>
  )
}

function KindItem({ r, c, activePanel, indent }: { r: Resource; c: ClusterInfo; activePanel?: string; indent?: boolean }) {
  return (
    <NavItem
      icon={kindIcon(r.kind)}
      label={plural(r.kind)}
      title={`${r.resource}${r.group ? '.' + r.group : ''}/${r.version}`}
      active={gridPanelId(c.id, r) === activePanel}
      testid={`nav-kind-${r.kind}`}
      indent={indent}
      onOpen={(pin) => openGrid(c.id, r, { preview: !pin, from: 'nav' })}
    />
  )
}

function NavItem({ icon: Icon, label, title, active, testid, indent, onOpen }: {
  icon: React.ComponentType<{ size?: number }>; label: string; title?: string; active?: boolean; testid?: string; indent?: boolean; onOpen: (pin: boolean) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [active])
  return (
    <div
      ref={ref}
      className={`nav-item kind ${active ? 'active' : ''} ${indent ? 'indent' : ''}`}
      title={title ?? label}
      data-testid={testid}
      onClick={() => onOpen(false)}
      onDoubleClick={() => onOpen(true)}
    >
      <Icon size={14} />
      <span className="ellipsis">{label}</span>
    </div>
  )
}

/** Left area: hotbar plus the navigator (all clusters, or the active one). */
export function Sidebar() {
  const active = useApp((s) => s.clusters.find((c) => c.id === s.activeCluster))
  const width = useUi((s) => s.sidebar)
  const hidden = useUi((s) => s.sidebarHidden)
  const startResize = (e: React.MouseEvent) => {
    e.preventDefault()
    const x0 = e.clientX, w0 = width
    const move = (ev: MouseEvent) => useUi.setState({ sidebar: Math.max(200, Math.min(480, w0 + ev.clientX - x0)) })
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      useUi.getState().update({})
      document.body.classList.remove('resizing')
    }
    document.body.classList.add('resizing')
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
  return (
    <>
      <Hotbar />
      {!hidden && (
        <aside className="sidebar" style={{ width }}>
          {active ? <ClusterNavigator c={active} /> : (
            <div className="navigator">
              <div className="nav-head-title">Clusters</div>
              <ClusterList compact />
            </div>
          )}
          <div className="resizer" onMouseDown={startResize} title="Arraste para redimensionar" />
        </aside>
      )}
    </>
  )
}
