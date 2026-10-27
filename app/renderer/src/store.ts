import { useMemo } from 'react'
import { create } from 'zustand'
import type { DockviewApi } from 'dockview-react'
import { persist } from './persist'
import { api, ApiError, type ClusterInfo, type Resource } from './api'

export interface ClusterState {
  resources: Resource[]
  version: string
  defaultNamespace: string
  readOnly: boolean
}

interface Toast {
  id: number
  kind: 'info' | 'error' | 'ok'
  text: string
}

interface AppState {
  clusters: ClusterInfo[]
  connected: Record<string, ClusterState>
  connecting: Record<string, boolean>
  connectError: Record<string, { message: string; status: number } | undefined>
  namespace: Record<string, string> // selected namespace per cluster ('' = all)
  dock?: DockviewApi
  toasts: Toast[]
  activeCluster?: string // cluster shown in the navigator
  activePanelId?: string
  setActiveCluster: (id?: string) => void
  loadClusters: () => Promise<void>
  connect: (id: string) => Promise<void>
  disconnect: (id: string) => Promise<void>
  setReadOnly: (id: string, readOnly: boolean, confirm?: string) => Promise<void>
  setNamespace: (id: string, ns: string) => void
  setDock: (d: DockviewApi) => void
  toast: (kind: Toast['kind'], text: string) => void
}

let toastSeq = 0

export const useApp = create<AppState>((set, get) => ({
  clusters: [],
  connected: {},
  connecting: {},
  connectError: {},
  namespace: {},
  toasts: [],
  setActiveCluster: (id) => set({ activeCluster: id }),
  loadClusters: async () => {
    const { clusters } = await api.clusters()
    set({ clusters })
  },
  connect: async (id) => {
    set((s) => ({ connecting: { ...s.connecting, [id]: true }, connectError: { ...s.connectError, [id]: undefined } }))
    try {
      const r = await api.connect(id)
      set((s) => ({
        connected: { ...s.connected, [id]: { resources: r.resources, version: r.version, defaultNamespace: r.namespace, readOnly: r.readOnly } },
        namespace: { ...s.namespace, [id]: s.namespace[id] ?? '' },
        activeCluster: id,
      }))
    } catch (e) {
      const status = e instanceof ApiError ? e.status : 0
      set((s) => ({ connectError: { ...s.connectError, [id]: { message: (e as Error).message, status } } }))
      get().toast('error', `Falha ao conectar: ${(e as Error).message}`)
    } finally {
      set((s) => ({ connecting: { ...s.connecting, [id]: false } }))
    }
  },
  disconnect: async (id) => {
    await api.disconnect(id).catch(() => {})
    const dock = get().dock
    dock?.panels.filter((p) => (p.params as { clusterId?: string })?.clusterId === id).forEach((p) => p.api.close())
    set((s) => {
      const c = { ...s.connected }
      delete c[id]
      return { connected: c }
    })
  },
  setReadOnly: async (id, readOnly, confirm) => {
    const r = await api.setReadOnly(id, readOnly, confirm)
    set((s) => ({ connected: { ...s.connected, [id]: { ...s.connected[id], readOnly: r.readOnly } } }))
  },
  setNamespace: (id, ns) => set((s) => ({ namespace: { ...s.namespace, [id]: ns } })),
  setDock: (d) => set({ dock: d }),
  toast: (kind, text) => {
    const id = ++toastSeq
    set((s) => ({ toasts: [...s.toasts, { id, kind, text }] }))
    setTimeout(() => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })), kind === 'error' ? 8000 : 4000)
  },
}))

/** Display name of a cluster (alias or the readable part of the context name). */
export function clusterName(id: string) {
  const c = useApp.getState().clusters.find((x) => x.id === id)
  return c ? identityOf(id, c.name, useProfiles.getState().profiles[id], c.prod).name : id
}

export function findResource(clusterId: string, group: string, resource: string) {
  return useApp.getState().connected[clusterId]?.resources.find((r) => r.group === group && r.resource === resource)
}

export function findKind(clusterId: string, group: string, kind: string) {
  return useApp.getState().connected[clusterId]?.resources.find((r) => r.group === group && r.kind === kind)
}

export interface OpenOptions {
  /** Temporary tab (italic) reused by the next preview open in the same group; double-click pins it. */
  preview?: boolean
  /** 'bottom' opens in the bottom dock group (terminal, logs), like an IDE panel. */
  area?: 'main' | 'bottom'
  /** 'nav': opened from the navigator. Opens from inside a preview tab pin that tab instead of replacing it. */
  from?: 'nav'
}

// Bottom-dock components: they always open below the main area.
export const BOTTOM_COMPONENTS = new Set(['terminal', 'logs', 'buildLog', 'create'])

export const useTabs = create<{ preview: Record<string, true>; bottomGroup?: string; closed: { id: string; component: string; title: string; params: Record<string, unknown> }[] }>(() => ({
  preview: {},
  closed: [],
}))

export function pinTab(id: string) {
  useTabs.setState((s) => {
    if (!s.preview[id]) return s
    const preview = { ...s.preview }
    delete preview[id]
    return { preview }
  })
}

/** Opens (or focuses) a dock panel. */
export function openPanel(id: string, component: string, title: string, params: Record<string, unknown>, opts: OpenOptions = {}) {
  const dock = useApp.getState().dock
  if (!dock) return
  const existing = dock.getPanel(id)
  if (existing) {
    // Opening the same thing "for keeps" pins its preview tab.
    if (!opts.preview) pinTab(id)
    existing.api.setActive()
    return
  }
  const area = opts.area ?? (BOTTOM_COMPONENTS.has(component) ? 'bottom' : 'main')
  const tabs = useTabs.getState()
  if (area === 'bottom') {
    const group = tabs.bottomGroup ? dock.getGroup(tabs.bottomGroup) : undefined
    if (group) {
      dock.addPanel({ id, component, title, params, position: { referenceGroup: group.id } })
    } else {
      const p = dock.addPanel({ id, component, title, params, position: { direction: 'below' } })
      useTabs.setState({ bottomGroup: p.group.id })
      try {
        p.group.api.setSize({ height: Math.round(window.innerHeight * 0.36) })
      } catch {
        /* sizing is cosmetic */
      }
    }
    return
  }
  // Main area: never drop a screen into the bottom dock.
  let group = dock.activeGroup
  if (group && group.id === tabs.bottomGroup) group = dock.groups.find((g) => g.id !== tabs.bottomGroup && g.api.location.type === 'grid')
  let old = opts.preview && group ? group.panels.find((p) => tabs.preview[p.id]) : undefined
  if (old && opts.from !== 'nav' && old.id === dock.activePanel?.id) {
    // Drilling down from a preview tab (grid row, topology node): keep the source tab.
    pinTab(old.id)
    old = undefined
  }
  const panel = dock.addPanel({ id, component, title, params, position: old ? { referencePanel: old, direction: 'within' } : group ? { referenceGroup: group } : undefined })
  if (old) old.api.close()
  if (opts.preview) useTabs.setState((s) => ({ preview: { ...s.preview, [panel.id]: true } }))
}

/** Remembers closed tabs so Ctrl+Shift+T can reopen them (shells are not reopened). */
export function rememberClosed(p: { id: string; api: { component: string; title?: string }; params?: Record<string, unknown> }) {
  useTabs.setState((s) => {
    const preview = { ...s.preview }
    delete preview[p.id]
    if (p.api.component === 'terminal') return { preview }
    const closed = [...s.closed.filter((c) => c.id !== p.id), { id: p.id, component: p.api.component, title: p.api.title ?? p.id, params: p.params ?? {} }].slice(-20)
    return { preview, closed }
  })
}

export function reopenClosed() {
  const last = useTabs.getState().closed.at(-1)
  if (!last) return
  useTabs.setState((s) => ({ closed: s.closed.slice(0, -1) }))
  openPanel(last.id, last.component, last.title, last.params)
}

// UI preferences (per viewer, best effort): sidebar width, collapsed sections, hotbar pins.
const UI_KEY = 'leme.ui.v1'
interface UiPrefs {
  theme: 'system' | 'dark' | 'light'
  drawer: number
  columns: Record<string, string[]> // hidden grid columns per resource
  widths: Record<string, Record<string, number>> // grid column widths per resource
  sidebar: number
  sidebarHidden: boolean
  collapsed: Record<string, boolean>
  pinned: string[]
  hotbarLabels: boolean
  /** M5 map-webgl: workloads from which the cluster map switches from SVG to WebGL. */
  mapWebglThreshold: number
  /** Cluster map: hide namespaces without workloads; show the minimap. */
  mapHideEmpty: boolean
  mapMinimap: boolean
}
function loadUi(): UiPrefs {
  const d: UiPrefs = { theme: 'dark', drawer: 560, columns: {}, widths: {}, sidebar: 260, sidebarHidden: false, collapsed: {}, pinned: [], hotbarLabels: true, mapWebglThreshold: 1500, mapHideEmpty: false, mapMinimap: true }
  try {
    return { ...d, ...JSON.parse(localStorage.getItem(UI_KEY) ?? '{}') }
  } catch {
    return d
  }
}
export const useUi = create<UiPrefs & { update: (p: Partial<UiPrefs>) => void }>((set, get) => ({
  ...loadUi(),
  update: (p) => {
    set(p)
    try {
      const { theme, drawer, columns, widths, sidebar, sidebarHidden, collapsed, pinned, hotbarLabels, mapWebglThreshold, mapHideEmpty, mapMinimap } = get()
      persist(UI_KEY, JSON.stringify({ theme, drawer, columns, widths, sidebar, sidebarHidden, collapsed, pinned, hotbarLabels, mapWebglThreshold, mapHideEmpty, mapMinimap }))
    } catch {
      /* convenience only */
    }
  },
}))

// Cluster identity: color, short label, alias and icon tag tabs, hotbar and panels so clusters
// side by side are easy to tell apart. Per viewer, kept in localStorage.
export const PALETTE = ['#38bdf8', '#a78bfa', '#f472b6', '#34d399', '#fbbf24', '#fb7185', '#60a5fa', '#c084fc', '#4ade80', '#f97316']
/** Swatches offered when customizing (the hashed default always comes from PALETTE). */
export const SWATCHES = [...PALETTE, '#2dd4bf', '#a3e635', '#e879f9', '#94a3b8', '#ef4444', '#eab308', '#0ea5e9', '#8b5cf6']

export type ClusterEnv = 'prod' | 'hml' | 'dev' | 'test' | 'none'
export interface ClusterProfile {
  alias?: string
  short?: string
  color?: string
  icon?: string // an emoji shown instead of the short label
  env?: ClusterEnv // unset = inferred from the context name
}

const PROFILE_KEY = 'leme.clusterProfiles.v1'
const OLD_COLOR_KEY = 'leme.clusterColors'

function loadProfiles(): Record<string, ClusterProfile> {
  try {
    const saved = localStorage.getItem(PROFILE_KEY)
    if (saved) return JSON.parse(saved)
    const colors: Record<string, string> = JSON.parse(localStorage.getItem(OLD_COLOR_KEY) ?? '{}')
    return Object.fromEntries(Object.entries(colors).map(([id, color]) => [id, { color }]))
  } catch {
    return {}
  }
}

export const useProfiles = create<{ profiles: Record<string, ClusterProfile>; setProfile: (id: string, p: ClusterProfile | undefined) => void }>((set) => ({
  profiles: loadProfiles(),
  setProfile: (id, p) =>
    set((s) => {
      const profiles = { ...s.profiles }
      const clean = p && Object.fromEntries(Object.entries(p).filter(([, v]) => v !== undefined && v !== ''))
      if (clean && Object.keys(clean).length) profiles[id] = clean
      else delete profiles[id]
      try {
        persist(PROFILE_KEY, JSON.stringify(profiles))
      } catch {
        /* per-viewer convenience only */
      }
      return { profiles }
    }),
}))

export function defaultColor(id: string) {
  let h = 0
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return PALETTE[h % PALETTE.length]
}

/** Splits an OpenShift-style context "namespace/api-host:port/user" into a readable name and the cluster host. */
export function contextParts(name: string): { base: string; host?: string } {
  const m = /^([^/]+)\/(?:api-)?([^/:]+)(?::\d+)?\/[^/]+$/.exec(name)
  return m ? { base: m[1], host: m[2] } : { base: name }
}

const ENV_WORDS: [ClusterEnv, RegExp][] = [
  ['prod', /^(prd|prod|production|producao|prd\d*)$/],
  ['hml', /^(hml|hmg|homolog|homologacao|stag|stage|staging|stg|uat|qa)$/],
  ['dev', /^(dev|dsv|develop|development|local|kind|minikube|sandbox)$/],
  ['test', /^(test|tst|teste)$/],
]

function envOfWord(w: string): ClusterEnv | undefined {
  return ENV_WORDS.find(([, re]) => re.test(w))?.[0]
}

/** Environment guessed from the words of the context name ("payroll-api-prd" → prod). */
export function inferEnv(name: string): ClusterEnv {
  const words = contextParts(name).base.toLowerCase().split(/[^a-z0-9]+/)
  for (const w of [...words].reverse()) {
    const e = envOfWord(w)
    if (e) return e
  }
  return 'none'
}

/** Up to 3 letters from the meaningful words of the name, skipping environment words and hashes. */
export function defaultShort(name: string) {
  const words = contextParts(name).base.split(/[^a-zA-Z0-9]+/).filter((w) => w && !envOfWord(w.toLowerCase()) && !/^[0-9a-f]{10,}$/i.test(w))
  if (!words.length) return name.slice(0, 2).toUpperCase()
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase()
  // Two words: "payroll-api" → PAP, "payroll-web" → PWE (initials alone collide too often).
  if (words.length === 2) return (words[0][0] + words[1].slice(0, 2)).toUpperCase()
  return words.slice(0, 3).map((w) => w[0]).join('').toUpperCase()
}

/** Short tag of a cluster host to tell same-named contexts apart ("ocp-c01-acme-dc1" → "c01"). */
export function hostTag(host?: string) {
  return host?.split(/[-.]/).find((w) => w && !/^(api|ocp|k8s|kube|openshift|cluster)$/i.test(w))
}

export const ENV_LABEL: Record<ClusterEnv, string> = { prod: 'PROD', hml: 'HML', dev: 'DEV', test: 'TST', none: '' }

export interface ClusterIdentity { name: string; host?: string; short: string; color: string; icon?: string; env: ClusterEnv; custom: boolean }

export function identityOf(id: string, name: string, p?: ClusterProfile, prod?: boolean): ClusterIdentity {
  const { base, host } = contextParts(name)
  return {
    name: p?.alias || base,
    host,
    short: (p?.short || defaultShort(name)).slice(0, 4),
    color: p?.color ?? defaultColor(id),
    icon: p?.icon,
    // The name says the environment ("-stag"); without a hint, the engine's production detection decides.
    env: p?.env ?? (inferEnv(name) !== 'none' ? inferEnv(name) : prod ? 'prod' : 'none'),
    custom: !!p,
  }
}

/** Display identity of a cluster (alias, short label, color, icon, environment). */
export function useIdentity(c: Pick<ClusterInfo, 'id' | 'name' | 'prod'>) {
  const p = useProfiles((s) => s.profiles[c.id])
  return useMemo(() => identityOf(c.id, c.name, p, c.prod), [c.id, c.name, c.prod, p])
}

export function useClusterColor(id?: string) {
  const c = useProfiles((s) => (id ? s.profiles[id]?.color : undefined))
  return id ? c ?? defaultColor(id) : undefined
}
