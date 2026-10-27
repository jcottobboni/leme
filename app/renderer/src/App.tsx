import { lazy, Suspense, useCallback, useEffect, useState, type ComponentType, type FunctionComponent } from 'react'
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
// Static on purpose: the grid's drawer already imports them (ObjectDetails, openCopilot), so a lazy import would not
// split anything.
import { ObjectPanel } from './components/ObjectPanel'
import { CopilotPanel } from './features/ai/CopilotPanel'
import { ClusterGate, Loading } from './features/common'
import { Palette } from './features/search/Palette'
import { PanelVisibilityProvider } from './hooks/visibility'
import { ErrorBoundary } from './components/ErrorBoundary'
// Registers the traffic source for the map and the topology at startup (the Traffic panel itself is lazy).
import './features/traffic/provider'
import { ClusterTab, HeaderActions, restoreLayout, saveLayout } from './features/workspace/Dock'

type PanelComponent = ComponentType<IDockviewPanelProps<any>>

/** A dock panel loaded on first use (its code, and heavy libraries such as xterm or xyflow, stay out of the initial bundle). */
function lazyPanel<M>(load: () => Promise<M>, name: keyof M): PanelComponent {
  return lazy(() => load().then((m) => ({ default: m[name] as unknown as PanelComponent })))
}

// Only the grid and the welcome page are in the initial bundle; every other panel is a chunk of its own.
const LogsPanel = lazyPanel(() => import('./components/LogsPanel'), 'LogsPanel')
const TerminalPanel = lazyPanel(() => import('./components/TerminalPanel'), 'TerminalPanel')
const ForwardsPanel = lazyPanel(() => import('./components/ForwardsPanel'), 'ForwardsPanel')
const HelmReleasesPanel = lazyPanel(() => import('./features/helm/HelmReleases'), 'HelmReleasesPanel')
const HelmReleasePanel = lazyPanel(() => import('./features/helm/HelmRelease'), 'HelmReleasePanel')
const HelmChartsPanel = lazyPanel(() => import('./features/helm/HelmCharts'), 'HelmChartsPanel')
const RbacPanel = lazyPanel(() => import('./features/rbac/RbacPanel'), 'RbacPanel')
const CreatePanel = lazyPanel(() => import('./features/rbac/CreatePanel'), 'CreatePanel')
const OverviewPanel = lazyPanel(() => import('./features/overview/OverviewPanel'), 'OverviewPanel')
const TopologyPanel = lazyPanel(() => import('./features/topology/Topology'), 'TopologyPanel')
const MapPanel = lazyPanel(() => import('./features/map/MapPanel'), 'MapPanel')
const ProjectTopologyPanel = lazyPanel(() => import('./features/projtopo/ProjectTopology'), 'ProjectTopologyPanel')
const TimelinePanel = lazyPanel(() => import('./features/timeline/Timeline'), 'TimelinePanel')
const SecurityPanel = lazyPanel(() => import('./features/security/SecurityPanel'), 'SecurityPanel')
const NetpolPanel = lazyPanel(() => import('./features/netpol/NetpolPanel'), 'NetpolPanel')
const BuildLogPanel = lazyPanel(() => import('./features/openshift/BuildLogPanel'), 'BuildLogPanel')
const ProblemsPanel = lazyPanel(() => import('./features/analyze/ProblemsPanel'), 'ProblemsPanel')
// M5 extension points: each feature replaces its own panel.
const TrafficPanel = lazyPanel(() => import('./features/traffic/TrafficPanel'), 'TrafficPanel')
const ArgoPanel = lazyPanel(() => import('./features/argocd/ArgoPanel'), 'ArgoPanel')
const GrafanaPanel = lazyPanel(() => import('./features/grafana/GrafanaPanel'), 'GrafanaPanel')
const IstioPanel = lazyPanel(() => import('./features/istio/IstioPanel'), 'IstioPanel')
// M7 (front O): aggregated logs and diff between clusters/namespaces.
const MultiLogsPanel = lazyPanel(() => import('./features/logs/MultiLogsPanel'), 'MultiLogsPanel')
const DiffPanel = lazyPanel(() => import('./features/diff/DiffPanel'), 'DiffPanel')

function Welcome(_: IDockviewPanelProps) {
  return <Home />
}

// Cluster-bound panels restored from a saved layout wait for the user to connect (nothing connects by itself).
function gated<P extends { clusterId?: string }>(C: ComponentType<IDockviewPanelProps<P>>) {
  return (props: IDockviewPanelProps<P>) => (
    <ClusterGate clusterId={props.params?.clusterId}>
      <C {...props} />
    </ClusterGate>
  )
}

/**
 * Every dock component: visibility context (pauses polling of hidden tabs), a loading state while its
 * chunk loads, and an error boundary so a failure stays in that tab instead of blanking the window.
 */
function panel(C: PanelComponent, name: string): FunctionComponent<IDockviewPanelProps<any>> {
  return (props: IDockviewPanelProps<any>) => (
    <PanelVisibilityProvider api={props.api}>
      <ErrorBoundary label={props.api?.title ?? name}>
        <Suspense fallback={<Loading />}>
          <C {...props} />
        </Suspense>
      </ErrorBoundary>
    </PanelVisibilityProvider>
  )
}

const components = Object.fromEntries(
  Object.entries({
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
    projtopo: ProjectTopologyPanel,
    timeline: TimelinePanel,
    security: SecurityPanel,
    netpol: NetpolPanel,
    buildLog: BuildLogPanel,
    problems: ProblemsPanel,
    copilot: CopilotPanel,
    traffic: TrafficPanel,
    argocd: ArgoPanel,
    grafana: GrafanaPanel,
    istio: IstioPanel,
    multilogs: gated(MultiLogsPanel),
    diff: DiffPanel,
  } as Record<string, PanelComponent>).map(([k, C]) => [k, panel(C, k)]),
)

// Panels that start streams or shells are never restored from a saved layout.
const NOT_RESTORED = new Set(['terminal', 'logs', 'create', 'buildLog', 'multilogs'])

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
