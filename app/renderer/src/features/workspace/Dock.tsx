import { useEffect, useState } from 'react'
import type { IDockviewHeaderActionsProps, IDockviewPanelHeaderProps, IDockviewPanel, SerializedDockview, DockviewApi, DockviewGroupPanel } from 'dockview-react'
import { ArrowDownToLine, Columns2, ExternalLink, Maximize2, Minimize2, Pin, Rows2, X, XCircle } from 'lucide-react'
import { clusterName, identityOf, pinTab, useApp, useClusterColor, useProfiles, useTabs } from '../../store'
import { behaviorKind, COMPONENT_ICONS, kindIcon } from '../shell/nav'
import { openMenu, type MenuItem } from '../shell/menu'
import { applyThemeEverywhere } from '../shell/theme'
import { persist } from '../../persist'

function iconOf(component: string, params?: { kind?: string; group?: string }) {
  if (component === 'grid' || component === 'object') return kindIcon(params?.kind && behaviorKind(params.group ?? '', params.kind))
  return COMPONENT_ICONS[component] ?? kindIcon()
}

function clusterOf(p: IDockviewPanel) {
  return (p.params as { clusterId?: string } | undefined)?.clusterId
}

/** Closes panels of a group, keeping one (others / to the right). */
function closeWhere(group: DockviewGroupPanel, keep: (p: IDockviewPanel, i: number) => boolean) {
  group.panels.filter((p, i) => !keep(p, i)).forEach((p) => p.api.close())
}

export function tabMenu(panel: IDockviewPanel, dock: DockviewApi): MenuItem[] {
  const group = panel.group
  const idx = group.panels.indexOf(panel)
  const cid = clusterOf(panel)
  const preview = !!useTabs.getState().preview[panel.id]
  return [
    { label: 'Fechar', icon: X, hint: 'Ctrl+W', onClick: () => panel.api.close() },
    { label: 'Fechar outras', disabled: group.panels.length < 2, onClick: () => closeWhere(group, (p) => p === panel) },
    { label: 'Fechar à direita', disabled: idx === group.panels.length - 1, onClick: () => closeWhere(group, (_p, i) => i <= idx) },
    { label: 'Fechar todas do grupo', onClick: () => closeWhere(group, () => false) },
    ...(cid ? [{ label: `Fechar abas de ${clusterName(cid)}`, icon: XCircle, onClick: () => dock.panels.filter((p) => clusterOf(p) === cid).forEach((p) => p.api.close()) }] : []),
    { separator: true },
    ...(preview ? [{ label: 'Manter aba aberta', icon: Pin, hint: 'duplo clique', onClick: () => pinTab(panel.id) }] : []),
    { label: 'Dividir à direita', icon: Columns2, disabled: group.panels.length < 2, onClick: () => panel.api.moveTo({ group, position: 'right' }) },
    { label: 'Dividir abaixo', icon: Rows2, disabled: group.panels.length < 2, onClick: () => panel.api.moveTo({ group, position: 'bottom' }) },
    ...(group.api.location.type !== 'popout' ? [{ label: 'Abrir em nova janela', icon: ExternalLink, onClick: () => dock.addPopoutGroup(panel, { popoutUrl: './popout.html' }).then(applyThemeEverywhere) }] : []),
  ]
}

/** Tab with icon, cluster color and a close button; italic while it is a preview tab. */
export function ClusterTab({ api, containerApi, params }: IDockviewPanelHeaderProps) {
  const clusterId = (params as { clusterId?: string })?.clusterId
  const color = useClusterColor(clusterId)
  const preview = useTabs((s) => !!s.preview[api.id])
  // With more than one cluster connected, the tab shows the cluster's short label (CSS content, not text).
  const multi = useApp((s) => Object.keys(s.connected).length > 1)
  const cluster = useApp((s) => (clusterId ? s.clusters.find((c) => c.id === clusterId) : undefined))
  const profile = useProfiles((s) => (clusterId ? s.profiles[clusterId] : undefined))
  const short = multi && cluster ? (profile?.icon || identityOf(cluster.id, cluster.name, profile, cluster.prod).short) : undefined
  const [title, setTitle] = useState(api.title ?? '')
  useEffect(() => {
    setTitle(api.title ?? '')
    const d = api.onDidTitleChange((e) => setTitle(e.title))
    return () => d.dispose()
  }, [api])
  const Icon = iconOf(api.component, params as { kind?: string; group?: string })
  const panel = () => containerApi.getPanel(api.id)
  return (
    <div
      className={`ctab ${preview ? 'preview' : ''}`}
      style={color ? ({ '--cc': color } as React.CSSProperties) : undefined}
      title={clusterId ? `${clusterName(clusterId)} · ${title}` : title}
      data-cluster={clusterId}
      data-testid={`tab-${api.id}`}
      onDoubleClick={() => pinTab(api.id)}
      onMouseDown={(e) => e.button === 1 && e.preventDefault()}
      onAuxClick={(e) => e.button === 1 && api.close()}
      onContextMenu={(e) => {
        const p = panel()
        // Menus render in the main window; a popout window keeps the browser default.
        if (p && p.group.api.location.type !== 'popout') openMenu(e, tabMenu(p, containerApi))
      }}
    >
      {short && <span className="ctab-cl" data-short={short} />}
      <Icon size={13} className="ctab-icon" />
      <span className="ctab-title">{title}</span>
      <button
        className="ctab-close"
        title="Fechar (Ctrl+W)"
        aria-label={`Fechar ${title}`}
        onMouseDown={(e) => e.stopPropagation()}
        onClick={(e) => {
          e.stopPropagation()
          api.close()
        }}
      >
        <X size={13} />
      </button>
    </div>
  )
}

export function HeaderActions({ group, containerApi }: IDockviewHeaderActionsProps) {
  const popout = group.api.location.type === 'popout'
  const bottom = useTabs((s) => s.bottomGroup === group.id)
  const [max, setMax] = useState(group.api.isMaximized())
  useEffect(() => {
    const d = containerApi.onDidMaximizedGroupChange(() => setMax(group.api.isMaximized()))
    return () => d.dispose()
  }, [containerApi, group])
  return (
    <div className="dv-actions">
      {!popout && (
        <button className="icon-btn" title="Abrir este grupo em outra janela" data-testid="popout" onClick={() => containerApi.addPopoutGroup(group, { popoutUrl: './popout.html' }).then(applyThemeEverywhere)}>
          <ExternalLink size={14} />
        </button>
      )}
      {!popout && (
        <button className="icon-btn" title={max ? 'Restaurar' : 'Maximizar'} onClick={() => (max ? group.api.exitMaximized() : group.api.maximize())}>
          {max ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
        </button>
      )}
      {bottom && (
        <button className="icon-btn" title="Fechar o painel inferior" onClick={() => closeWhere(group, () => false)}>
          <ArrowDownToLine size={14} />
        </button>
      )}
      {!bottom && group.panels.length > 1 && (
        <button className="icon-btn" title="Fechar todas as abas deste grupo" onClick={() => closeWhere(group, () => false)}>
          <X size={14} />
        </button>
      )}
    </div>
  )
}

const LAYOUT_KEY = 'leme.layout.v1'

/** Saves the dock layout (per viewer, best effort). Popout windows are not restored. */
export function saveLayout(dock: DockviewApi) {
  try {
    const json = dock.toJSON() as SerializedDockview & { popoutGroups?: unknown }
    delete json.popoutGroups
    persist(LAYOUT_KEY, JSON.stringify(json))
  } catch {
    /* storage unavailable: layout is a convenience only */
  }
}

export function restoreLayout(dock: DockviewApi): boolean {
  try {
    const raw = localStorage.getItem(LAYOUT_KEY)
    if (!raw) return false
    const json = JSON.parse(raw) as SerializedDockview
    if (!json?.grid || !Object.keys(json.panels ?? {}).length) return false
    dock.fromJSON(json)
    return dock.panels.length > 0
  } catch {
    try {
      persist(LAYOUT_KEY, null)
    } catch {
      /* ignore */
    }
    return false
  }
}
