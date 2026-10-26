import { useCallback, useEffect, useState } from 'react'
import { CircleAlert, CircleCheck, Info, X } from 'lucide-react'
import { DockviewReact, type DockviewReadyEvent, type IDockviewPanelProps } from 'dockview-react'
import 'dockview-react/dist/styles/dockview.css'
import { api } from './api'
import { rememberClosed, useApp, useTabs } from './store'
import { Sidebar } from './features/shell/Sidebar'
import { Home, StatusBar, TopBar, trackActive, useShortcuts } from './features/shell/Chrome'
import { ContextMenuHost, DialogHost } from './features/shell/menu'
import { useApplyTheme, useTheme } from './features/shell/theme'
import { ResourceGrid } from './components/ResourceGrid'
import { ObjectPanel } from './components/ObjectPanel'
import { LogsPanel } from './components/LogsPanel'
import { TerminalPanel } from './components/TerminalPanel'
import { ForwardsPanel } from './components/ForwardsPanel'
import { ClusterGate } from './features/common'
import { HelmReleasesPanel } from './features/helm/HelmReleases'
import { HelmReleasePanel } from './features/helm/HelmRelease'
import { HelmChartsPanel } from './features/helm/HelmCharts'
import { RbacPanel } from './features/rbac/RbacPanel'
import { CreatePanel } from './features/rbac/CreatePanel'
import { OverviewPanel } from './features/overview/OverviewPanel'
import { TopologyPanel } from './features/topology/Topology'
import { MapPanel } from './features/map/MapPanel'
import { TimelinePanel } from './features/timeline/Timeline'
import { Palette } from './features/search/Palette'
import { SecurityPanel } from './features/security/SecurityPanel'
import { NetpolPanel } from './features/netpol/NetpolPanel'
import { BuildLogPanel } from './features/openshift/BuildLogPanel'
import { ClusterTab, HeaderActions, restoreLayout, saveLayout } from './features/workspace/Dock'

function Welcome(_: IDockviewPanelProps) {
  return <Home />
}

// Cluster-bound panels restored from a saved layout wait for the user to connect (nothing connects by itself).
function gated<P extends { clusterId?: string }>(C: React.FunctionComponent<IDockviewPanelProps<P>>) {
  return (props: IDockviewPanelProps<P>) => (
    <ClusterGate clusterId={props.params?.clusterId}>
      <C {...props} />
    </ClusterGate>
  )
}

const components = {
  welcome: Welcome,
  grid: gated(ResourceGrid),
  object: gated(ObjectPanel),
  logs: gated(LogsPanel),
  terminal: gated(TerminalPanel),
  forwards: ForwardsPanel,
  helmReleases: HelmReleasesPanel,
  helmRelease: HelmReleasePanel,
  helmCharts: HelmChartsPanel,
  rbac: RbacPanel,
  create: CreatePanel,
  overview: OverviewPanel,
  topology: TopologyPanel,
  map: MapPanel,
  timeline: TimelinePanel,
  security: SecurityPanel,
  netpol: NetpolPanel,
  buildLog: BuildLogPanel,
} as Record<string, React.FunctionComponent<IDockviewPanelProps<any>>>

// Panels that start streams or shells are never restored from a saved layout.
const NOT_RESTORED = new Set(['terminal', 'logs', 'create', 'buildLog'])

const TOAST_ICONS = { ok: CircleCheck, error: CircleAlert, info: Info }

function Toasts() {
  const toasts = useApp((s) => s.toasts)
  return (
    <div className="toasts" role="status">
      {toasts.map((t) => {
        const Icon = TOAST_ICONS[t.kind]
        return (
          <div key={t.id} className={`toast toast-${t.kind}`}>
            <Icon size={16} className="t-icon" />
            <span className="toast-text">{t.text}</span>
            <button className="icon-btn" title="Fechar" onClick={() => useApp.setState((s) => ({ toasts: s.toasts.filter((x) => x.id !== t.id) }))}>
              <X size={14} />
            </button>
          </div>
        )
      })}
    </div>
  )
}

export function App() {
  const [err, setErr] = useState<string>()
  const [palette, setPalette] = useState(false)
  useEffect(() => {
    if (!api.hasToken()) {
      setErr('Token ausente. Abra pelo aplicativo ou use o link impresso pelo engine (…/#token=…).')
      return
    }
    useApp.getState().loadClusters().catch((e) => setErr(e.message))
  }, [])
  useShortcuts(useCallback(() => setPalette((p) => !p), []))
  useApplyTheme()
  const theme = useTheme((s) => s.theme)

  const onReady = (e: DockviewReadyEvent) => {
    const dock = e.api
    useApp.getState().setDock(dock)
    if (restoreLayout(dock)) {
      for (const p of [...dock.panels]) if (NOT_RESTORED.has(p.api.component)) p.api.close()
    }
    if (!dock.panels.length) dock.addPanel({ id: 'welcome', component: 'welcome', title: 'Início' })
    trackActive(dock.activePanel?.id)
    dock.onDidActivePanelChange(({ panel: p }) => {
      trackActive(p?.id)
      // The navigator follows the cluster of the active tab.
      const cid = (p?.params as { clusterId?: string } | undefined)?.clusterId
      if (cid && useApp.getState().connected[cid]) useApp.getState().setActiveCluster(cid)
    })
    dock.onDidRemovePanel((p) => rememberClosed(p))
    dock.onDidRemoveGroup((g) => useTabs.getState().bottomGroup === g.id && useTabs.setState({ bottomGroup: undefined }))
    let t: ReturnType<typeof setTimeout> | undefined
    dock.onDidLayoutChange(() => {
      clearTimeout(t)
      t = setTimeout(() => saveLayout(dock), 500)
      // Never leave the main area empty.
      if (!dock.panels.length) setTimeout(() => !dock.panels.length && dock.addPanel({ id: 'welcome', component: 'welcome', title: 'Início' }), 0)
    })
  }

  if (err) return <div className="fatal">{err}</div>
  return (
    <div className="app">
      <TopBar onSearch={() => setPalette(true)} />
      <div className="workbench">
        <Sidebar />
        <main className="dock">
          <DockviewReact
            className={theme === 'light' ? 'dockview-theme-light' : 'dockview-theme-abyss'}
            components={components}
            defaultTabComponent={ClusterTab}
            rightHeaderActionsComponent={HeaderActions}
            onReady={onReady}
          />
        </main>
      </div>
      <StatusBar />
      {palette && <Palette onClose={() => setPalette(false)} />}
      <ContextMenuHost />
      <DialogHost />
      <Toasts />
    </div>
  )
}
