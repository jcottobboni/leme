import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type Column, type Row, type Usage } from '../api'
import { findResource, openPanel, useApp, useUi } from '../store'
import { useWatch } from '../useWatch'
import { behaviorKind, kindIcon, plural } from '../features/shell/nav'
import { openMenu } from '../features/shell/menu'
import { useRowActions } from '../features/grid/actions'
import { behaviorOf } from '../features/shell/kinds'
import { PanelHeader } from './PanelHeader'
import { ObjectDetails, type ObjectParams } from './ObjectPanel'
import { Braces, ChevronDown, ChevronUp, Columns3, Copy, EllipsisVertical, RotateCw, Search, Square, SquareCheck, Trash2, TriangleAlert } from 'lucide-react'
import { matchRow, parseFilter, takeGridPreset } from '../features/search/filter'
import { FILTER_HELP, FilterChips } from '../features/search/FilterChips'
import { JsonPathDialog } from '../features/search/JsonPathDialog'
import { absDate, age, bytes, cpu, healthOfStatus } from '../format'

export interface GridParams {
  clusterId: string
  group: string
  resource: string
}

/** Namespace picker. Screens that always work on one namespace pass `effective` (the one in use when
 * nothing is selected): the "all namespaces" option is hidden so the picker never lies about the scope. */
export function NamespaceSelect({ clusterId, effective }: { clusterId: string; effective?: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '') || effective || ''
  const { rows, error } = useWatch(clusterId, '', 'namespaces', '')
  // OpenShift users without cluster-wide namespace access can still list their Projects.
  const hasProjects = !!findResource(clusterId, 'project.openshift.io', 'projects')
  const projects = useWatch(clusterId, 'project.openshift.io', 'projects', '', !!error && hasProjects)
  const names = useMemo(() => (error && hasProjects ? projects.rows : rows).map((r) => r.name).sort(), [rows, projects.rows, error, hasProjects])
  return (
    <select className="ns-select" value={ns} onChange={(e) => useApp.getState().setNamespace(clusterId, e.target.value)} title={error ? `Sem permissão para listar namespaces: ${error}` : 'Namespace'}>
      {!effective && <option value="">Todos os namespaces</option>}
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
      {ns && !names.includes(ns) && <option value={ns}>{ns}</option>}
    </select>
  )
}

function useMetrics(clusterId: string, kind: string | undefined, namespace: string) {
  const [usage, setUsage] = useState<Record<string, Usage>>({})
  useEffect(() => {
    if (kind !== 'Pod' && kind !== 'Node') return
    let stop = false
    const load = () =>
      (kind === 'Pod' ? api.podMetrics(clusterId, namespace) : api.nodeMetrics(clusterId))
        .then((u) => !stop && setUsage(u))
        .catch(() => !stop && setUsage({}))
    load()
    const t = setInterval(load, 15000)
    return () => {
      stop = true
      clearInterval(t)
    }
  }, [clusterId, kind, namespace])
  return usage
}

function cellValue(row: Row, col: Column, usage: Record<string, Usage>, kind?: string): unknown {
  if (col.key === 'name') return row.name
  if (col.key === 'namespace') return row.namespace ?? ''
  if (col.key === 'age') return row.created ?? ''
  if (col.key === 'cpu' || col.key === 'memory') {
    const u = usage[kind === 'Node' ? row.name : `${row.namespace}/${row.name}`]
    return u ? (col.key === 'cpu' ? u.cpu : u.memory) : undefined
  }
  return row.cells[col.key]
}

function readyHealth(v: string) {
  const m = /^(\d+)\/(\d+)$/.exec(v)
  if (!m) return 'neutral'
  const [a, b] = [Number(m[1]), Number(m[2])]
  return b === 0 ? 'neutral' : a === b ? 'ok' : a === 0 ? 'error' : 'warn'
}

function render(v: unknown, col: Column, max: Record<string, number>, onNamespace: (ns: string) => void, row: Row) {
  if (col.type === 'containers' && Array.isArray(v)) {
    return (
      <span className="ctr-squares">
        {(v as { name: string; state: string }[]).map((c) => <i key={c.name} className={`sq ${c.state}`} title={`${c.name}: ${c.state}`} />)}
      </span>
    )
  }
  if (v === undefined || v === null || v === '') return <span className="muted">—</span>
  if (col.type === 'age') return age(String(v))
  if (col.key === 'cpu' || col.key === 'memory') {
    const n = v as number
    const pct = max[col.key] ? Math.max(4, Math.round((n / max[col.key]) * 100)) : 0
    return (
      <span className="usage">
        <span className="mini"><i style={{ width: `${pct}%` }} /></span>
        {col.key === 'cpu' ? cpu(n) : bytes(n)}
      </span>
    )
  }
  // The engine's row health wins over the label text, so the pill agrees with the dot and the header counts.
  if (col.type === 'status') return <span className={`pill pill-${row.health && row.health !== 'neutral' ? row.health : healthOfStatus(String(v))}`}>{String(v)}</span>
  if (col.key === 'ready' && typeof v === 'string' && v.includes('/')) return <span className={`ready-chip ${row.health === 'neutral' ? 'neutral' : readyHealth(v)}`}>{v}</span>
  if (col.key === 'namespace') {
    return (
      <span className="ns-link" role="link" tabIndex={0} title={`Filtrar pelo namespace ${v}`} onClick={(e) => (e.stopPropagation(), onNamespace(String(v)))}
        onKeyDown={(e) => e.key === 'Enter' && (e.stopPropagation(), onNamespace(String(v)))}>{String(v)}</span>
    )
  }
  if (col.key === 'url' && /^https?:\/\//.test(String(v))) {
    return <a href={String(v)} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>{String(v)}</a>
  }
  return String(v)
}

const CHECK_W = 34
const ACTIONS_W = 40
const HEAD_H = 32
// Short fixed-format values (addresses, versions) get a fixed track so the spare width goes to free text.
const FIXED_KEYS = ['ip', 'internalIP', 'clusterIP', 'version']

let measureCtx: CanvasRenderingContext2D | null | undefined
/** Rendered text width with the app font (canvas), with a per-character fallback. */
function textWidth(text: string, font: string, fallback: number) {
  if (measureCtx === undefined) measureCtx = document.createElement('canvas').getContext('2d')
  if (!measureCtx) return text.length * fallback
  const fam = getComputedStyle(document.documentElement).fontFamily || 'Inter, sans-serif'
  measureCtx.font = `${font} ${fam}`
  return measureCtx.measureText(text).width
}
const labelCache = new Map<string, number>()
/** Width an uppercase header label needs (11px, letter-spacing, padding and the sort chevron): never cut. */
function labelMin(c: Column) {
  let w = labelCache.get(c.label)
  if (w === undefined) {
    w = Math.ceil(textWidth(c.label.toUpperCase(), '600 11px', 8) + c.label.length * 0.5) + 16 + 6 + 16
    labelCache.set(c.label, w)
  }
  return w
}

export function ResourceGrid({ params }: IDockviewPanelProps<GridParams>) {
  const { clusterId, group, resource } = params
  const res = findResource(clusterId, group, resource)
  const selectedNs = useApp((s) => s.namespace[clusterId] ?? '')
  const namespace = res?.namespaced ? selectedNs : ''
  // M7 RF05: k9s-style search. The label selector runs in the engine (debounced: each change reopens
  // the watch); the other terms filter here. The palette can hand a query over (`:deploy -l app=web`).
  const [q, setQ] = useState(() => takeGridPreset(clusterId, group, resource) ?? '')
  const filter = useMemo(() => parseFilter(q), [q])
  const [selector, setSelector] = useState(filter.labelSelector)
  useEffect(() => {
    if (filter.labelSelector === selector) return
    const t = setTimeout(() => setSelector(filter.labelSelector), filter.labelSelector ? 350 : 0)
    return () => clearTimeout(t)
  }, [filter.labelSelector, selector])
  useEffect(() => {
    const onPreset = (e: Event) => {
      const d = (e as CustomEvent<{ clusterId: string; group: string; resource: string }>).detail
      if (d.clusterId !== clusterId || d.group !== group || d.resource !== resource) return
      const text = takeGridPreset(clusterId, group, resource)
      if (text !== undefined) setQ(text)
    }
    window.addEventListener('leme:grid-filter', onPreset)
    return () => window.removeEventListener('leme:grid-filter', onPreset)
  }, [clusterId, group, resource])
  const colKey = `${group}/${resource}`
  const jpCols = useUi((s) => s.jpCols?.[colKey]) ?? NO_JP
  const jpExprs = useMemo(() => jpCols.map((c) => c.expr), [jpCols])
  const [jpOpen, setJpOpen] = useState(false)
  const watch = useWatch(clusterId, group, resource, namespace, !!res, { labelSelector: selector, cols: jpExprs })
  const { rows, loading, error, colErrors } = watch
  // JSONPath columns show the name the user gave them.
  const columns = useMemo(() => watch.columns.map((c) => {
    if (!c.key.startsWith('jp:')) return c
    const p = jpCols.find((j) => `jp:${j.expr}` === c.key)
    return p ? { ...c, label: p.label } : c
  }), [watch.columns, jpCols])
  const bkind = res ? behaviorKind(res.group, res.kind) : undefined
  const usage = useMetrics(clusterId, bkind, namespace)
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 }>({ key: 'name', dir: 1 })
  const [selected, setSelected] = useState<string>()
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [drawer, setDrawer] = useState<{ uid: string; name: string; namespace?: string }>()
  const hidden = useUi((s) => s.columns[colKey]) ?? DEFAULT_HIDDEN
  const widths = useUi((s) => s.widths[colKey]) ?? NO_WIDTHS
  const drawerW = useUi((s) => s.drawer)
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const tableRef = useRef<HTMLDivElement>(null)
  const KindIcon = kindIcon(bkind)
  const behavior = behaviorOf(res?.group ?? group, res?.kind)
  // Horizontal scroll indicators: shadows beside the fixed Name and actions columns.
  const [edges, setEdges] = useState({ left: false, right: false })
  const updateEdges = useCallback(() => {
    const el = parentRef.current
    if (!el) return
    const left = el.scrollLeft > 1
    const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    el.style.setProperty('--vw', `${el.clientWidth}px`)
    setEdges((e) => (e.left === left && e.right === right ? e : { left, right }))
  }, [])
  // One element scrolls both ways, so the header (sticky top) and the fixed columns (sticky left/right) line up.
  const setScrollEl = useCallback((el: HTMLDivElement | null) => {
    parentRef.current = el
    tableRef.current = el
  }, [])

  // Without columns that carry a sort key (e.g. Events have no Name column) rows would sort by a hidden field.
  useEffect(() => {
    if (columns.length && !columns.some((c) => c.key === sort.key)) setSort(columns.some((c) => c.key === 'lastSeen') ? { key: 'lastSeen', dir: 1 } : { key: columns[0].key, dir: 1 })
  }, [columns]) // eslint-disable-line react-hooks/exhaustive-deps

  const visibleCols = useMemo(() => columns.filter((c) => !(c.key === 'namespace' && namespace) && (c.key === 'name' || !hidden.includes(c.key))), [columns, namespace, hidden])

  const sorted = useMemo(() => {
    const list = q.trim() ? rows.filter((r) => matchRow(r, filter)) : rows.slice()
    const col = columns.find((c) => c.key === sort.key)
    list.sort((a, b) => {
      let va = col ? cellValue(a, col, usage, bkind) : a.name
      let vb = col ? cellValue(b, col, usage, bkind) : b.name
      if (col?.type === 'age') {
        // newest first when ascending by age
        va = va ? -new Date(String(va)).getTime() : 0
        vb = vb ? -new Date(String(vb)).getTime() : 0
      }
      if (col?.type === 'containers') {
        va = (va as unknown[] | undefined)?.length ?? 0
        vb = (vb as unknown[] | undefined)?.length ?? 0
      }
      if (typeof va === 'number' || typeof vb === 'number') return ((Number(va ?? -1) - Number(vb ?? -1)) || a.name.localeCompare(b.name)) * sort.dir
      return (String(va ?? '').localeCompare(String(vb ?? ''), undefined, { numeric: true }) || a.name.localeCompare(b.name)) * sort.dir
    })
    return list
  }, [rows, q, filter, sort, columns, usage, bkind])

  const max = useMemo(() => {
    const m = { cpu: 0, memory: 0 }
    for (const u of Object.values(usage)) {
      m.cpu = Math.max(m.cpu, u.cpu)
      m.memory = Math.max(m.memory, u.memory)
    }
    return m
  }, [usage])

  // Selection only keeps rows that still exist.
  useEffect(() => {
    if (!checked.size) return
    const live = new Set(rows.map((r) => r.uid))
    if ([...checked].some((u) => !live.has(u))) setChecked(new Set([...checked].filter((u) => live.has(u))))
  }, [rows, checked])
  useEffect(() => setChecked(new Set()), [namespace])

  const virt = useVirtualizer({ count: sorted.length, getScrollElement: () => parentRef.current, estimateSize: () => 32, overscan: 20, scrollMargin: HEAD_H })
  // Columns share the panel width in proportion to their default size and shrink down to a floor
  // (the widest free-text column, e.g. Message, shrinks first); a width set by hand stays fixed.
  const flex = visibleCols.reduce<Column | undefined>((m, c) => (c.key !== 'name' && (c.width ?? 0) >= 280 && (c.width ?? 0) > (m?.width ?? 0) ? c : m), undefined)
  let minWidth = CHECK_W + ACTIONS_W
  const template = `${CHECK_W}px ` + visibleCols.map((c) => {
    if (widths[c.key]) return (minWidth += widths[c.key], `${widths[c.key]}px`)
    const w = c.width ?? 140
    if (c.type === 'age' || FIXED_KEYS.includes(c.key)) {
      const fw = Math.max(w, labelMin(c))
      return (minWidth += fw, `${fw}px`)
    }
    const min = Math.max(labelMin(c), c === flex ? 140 : c.key === 'name' ? Math.max(200, Math.min(w, 260)) : c.key === 'node' ? 100 : c.type === 'status' ? w : c.type === 'number' ? Math.max(w, 72) : w >= 200 ? Math.round(w * 0.6) : Math.max(56, Math.round(w * 0.75)))
    minWidth += min
    // Name gets the largest share of spare width: it is the column people read first.
    return `minmax(${min}px, ${c.key === 'name' ? w * 2 : w}fr)`
  }).join(' ') + ` ${ACTIONS_W}px`
  const counts = useMemo(() => {
    const c = { ok: 0, warn: 0, error: 0 }
    for (const r of rows) if (r.health in c) c[r.health as keyof typeof c]++
    return c
  }, [rows])
  // Kinds with no status (ConfigMaps, Secrets, RBAC…) skip the dot: a grey dot would read as "inactive".
  const hasHealth = useMemo(() => columns.some((c) => c.type === 'status') || rows.some((r) => r.health !== 'neutral'), [columns, rows])

  useLayoutEffect(() => {
    updateEdges()
    const el = parentRef.current
    if (!el) return
    const ro = new ResizeObserver(updateEdges)
    ro.observe(el)
    return () => ro.disconnect()
  }, [updateEdges, minWidth, drawer, visibleCols.length])

  const objParams = (r: Pick<Row, 'name' | 'namespace'>): ObjectParams | undefined =>
    res && { clusterId, group, version: res.version, resource, kind: res.kind, namespace: r.namespace, name: r.name }
  const openDrawer = (r: Row) => {
    setSelected(r.uid)
    setDrawer({ uid: r.uid, name: r.name, namespace: r.namespace })
  }
  // When the drawer is closed it would open over the row and swallow the second click of a
  // double-click, so a single click waits out the double-click window first.
  const clickTimer = useRef<number>(undefined)
  useEffect(() => () => clearTimeout(clickTimer.current), [])
  const rowClick = (e: React.MouseEvent, r: Row) => {
    clearTimeout(clickTimer.current)
    if (e.detail > 1) return openTab(r)
    setSelected(r.uid)
    if (drawer) openDrawer(r)
    else clickTimer.current = window.setTimeout(() => openDrawer(r), 230)
  }
  const openTab = (r: Pick<Row, 'name' | 'namespace'>) => {
    const p = objParams(r)
    if (p) openPanel(`obj:${clusterId}:${group}/${resource}:${r.namespace ?? ''}/${r.name}`, 'object', r.name, { ...p }, {})
  }
  const actions = useRowActions(clusterId, res, { drawer: openDrawer, tab: openTab }, { columns, filter: q })
  const setNs = (ns: string) => useApp.getState().setNamespace(clusterId, ns)

  const toggle = (uid: string) => setChecked((s) => {
    const n = new Set(s)
    if (n.has(uid)) n.delete(uid)
    else n.add(uid)
    return n
  })
  const allChecked = sorted.length > 0 && sorted.every((r) => checked.has(r.uid))
  const checkedRows = sorted.filter((r) => checked.has(r.uid))

  const move = (delta: number) => {
    if (!sorted.length) return
    const i = sorted.findIndex((r) => r.uid === selected)
    const next = sorted[Math.max(0, Math.min(sorted.length - 1, i === -1 ? 0 : i + delta))]
    setSelected(next.uid)
    if (drawer) setDrawer({ uid: next.uid, name: next.name, namespace: next.namespace })
    virt.scrollToIndex(sorted.indexOf(next), { align: 'auto' })
  }
  const onKey = (e: React.KeyboardEvent) => {
    const typing = (e.target as HTMLElement).closest('input, textarea, select, .monaco-editor')
    if (e.key === '/' && !typing) return (e.preventDefault(), searchRef.current?.focus())
    if (typing) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') return (e.preventDefault(), move(e.key === 'ArrowDown' ? 1 : -1))
    if (e.key === 'Enter') {
      const r = sorted.find((x) => x.uid === selected)
      if (r) openDrawer(r)
    }
    if (e.key === 'Escape' && drawer) setDrawer(undefined)
    if (e.key === ' ' && selected) (e.preventDefault(), toggle(selected))
  }

  const columnMenu = (e: React.MouseEvent) =>
    openMenu(e, [
      ...columns.filter((c) => c.key !== 'name').map((c) => ({
        label: c.label,
        icon: hidden.includes(c.key) ? Square : SquareCheck,
        hint: c.key.startsWith('jp:') ? 'JSONPath' : undefined,
        onClick: () => useUi.getState().update({ columns: { ...useUi.getState().columns, [colKey]: hidden.includes(c.key) ? hidden.filter((k) => k !== c.key) : [...hidden, c.key] } }),
      })),
      // Expressions the engine refused are not columns: list them with the error.
      ...jpCols.filter((c) => colErrors?.[`jp:${c.expr}`]).map((c) => ({ label: `${c.label}: ${colErrors![`jp:${c.expr}`]}`, icon: TriangleAlert, danger: true, hint: 'editar', onClick: () => setJpOpen(true) })),
      { separator: true },
      { label: 'Adicionar coluna JSONPath…', icon: Braces, onClick: () => setJpOpen(true) },
    ])

  const startColResize = (e: React.MouseEvent, c: Column) => {
    e.preventDefault()
    e.stopPropagation()
    const x0 = e.clientX, w0 = widths[c.key] ?? (e.currentTarget as HTMLElement).parentElement?.getBoundingClientRect().width ?? c.width ?? 140
    const move = (ev: MouseEvent) => {
      const all = useUi.getState().widths
      useUi.setState({ widths: { ...all, [colKey]: { ...(all[colKey] ?? {}), [c.key]: Math.max(50, w0 + ev.clientX - x0) } } })
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.classList.remove('resizing')
      useUi.getState().update({})
    }
    document.body.classList.add('resizing')
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
  /** Double click on a column edge: width of the widest value (and the label), like a spreadsheet. */
  const autoFit = (c: Column) => {
    let w = labelMin(c)
    const sample = sorted.length > 3000 ? sorted.slice(0, 3000) : sorted
    for (const r of sample) {
      const v = cellValue(r, c, usage, bkind)
      if (v === undefined || v === null || typeof v === 'object') continue
      const text = c.type === 'age' ? age(String(v)) : c.key === 'cpu' ? cpu(v as number) : c.key === 'memory' ? bytes(v as number) : String(v)
      const extra = c.key === 'name' ? 15 : c.type === 'status' ? 26 : c.key === 'cpu' || c.key === 'memory' ? 56 : 0
      w = Math.max(w, textWidth(text, c.key === 'name' ? '500 13px' : '400 13px', 7.5) + 16 + extra + 4)
    }
    const all = useUi.getState().widths
    useUi.getState().update({ widths: { ...all, [colKey]: { ...(all[colKey] ?? {}), [c.key]: Math.min(900, Math.max(50, Math.ceil(w))) } } })
  }
  const startDrawerResize = (e: React.MouseEvent) => {
    e.preventDefault()
    const x0 = e.clientX, w0 = drawerW
    const max = (tableRef.current?.parentElement?.clientWidth ?? 1400) - 320
    const move = (ev: MouseEvent) => useUi.setState({ drawer: Math.max(360, Math.min(max, w0 - (ev.clientX - x0))) })
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.classList.remove('resizing')
      useUi.getState().update({})
    }
    document.body.classList.add('resizing')
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // Built from primitives so a watch delta (a new `res` or row object) does not refetch the drawer.
  const drawerParams = useMemo<ObjectParams | undefined>(
    () => (drawer && res ? { clusterId, group, version: res.version, resource, kind: res.kind, namespace: drawer.namespace, name: drawer.name } : undefined),
    [drawer, clusterId, group, resource, res?.version, res?.kind], // eslint-disable-line react-hooks/exhaustive-deps
  )
  if (!res) return <div className="pad muted">Recurso não disponível neste cluster.</div>

  const nameCol = visibleCols.find((c) => c.key === 'name')
  const stickyCls = (c: Column) => (c === nameCol ? 'stk stk-name' : '')
  const nameLeft = { left: CHECK_W }
  const sortBy = (key: string) => setSort((s) => ({ key, dir: s.key === key ? ((-s.dir) as 1 | -1) : 1 }))

  return (
    <div className="grid-panel" onKeyDown={onKey} tabIndex={-1}>
      <PanelHeader icon={<KindIcon size={16} />} title={plural(res.kind)} scope={res.namespaced ? <NamespaceSelect clusterId={clusterId} /> : undefined}
        actions={[{ key: 'columns', label: 'Colunas', icon: Columns3, iconOnly: true, keep: true, testid: 'grid-columns', onClick: (e) => e && columnMenu(e) }]}>
        <label className="search-box">
          <Search size={14} />
          <input ref={searchRef} className="search" placeholder="Buscar (/)… -l app=web, !texto, /regex/" aria-label="Buscar na lista" title={FILTER_HELP} value={q} onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && q && (e.stopPropagation(), setQ(''))} data-testid="grid-search" />
        </label>
        <span className="count-pill">{sorted.length === rows.length ? `${rows.length} ${rows.length === 1 ? 'item' : 'itens'}` : `${sorted.length} de ${rows.length}`}</span>
        {counts.warn + counts.error > 0 && (
          <span className="health-counts">
            {counts.ok > 0 && <span className="s-ok" title="saudáveis">● {counts.ok} ok</span>}
            {counts.warn > 0 && <span className="s-warn" title="com alerta">● {counts.warn} alerta</span>}
            {counts.error > 0 && <span className="s-error" title="com erro">● {counts.error} erro</span>}
          </span>
        )}
        {loading && <span className="spinner" />}
      </PanelHeader>
      {checked.size > 0 && (
        <div className="bulkbar" data-testid="bulkbar">
          <b>{checked.size} selecionado{checked.size === 1 ? '' : 's'}</b>
          <button onClick={() => navigator.clipboard.writeText(checkedRows.map((r) => r.name).join('\n')).then(() => useApp.getState().toast('ok', 'Nomes copiados'))}><Copy size={14} /> Copiar nomes</button>
          {behavior.restartable && <button disabled={actions.readOnly} onClick={() => actions.ask('restart', checkedRows)}><RotateCw size={14} /> Reiniciar</button>}
          <button className="danger" disabled={actions.readOnly} title={actions.readOnly ? 'Cluster em somente leitura' : ''} onClick={() => actions.ask('delete', checkedRows)} data-testid="bulk-delete">
            <Trash2 size={14} /> Excluir {checked.size}
          </button>
          <span className="spacer" />
          <button className="link" onClick={() => setChecked(new Set())}>Limpar seleção</button>
        </div>
      )}
      <FilterChips filter={filter} onChange={setQ} />
      {error && <div className="banner error"><TriangleAlert size={14} />{error}</div>}
      {colErrors && Object.keys(colErrors).length > 0 && (
        <div className="banner warn" data-testid="jp-banner">
          <TriangleAlert size={14} />
          {Object.keys(colErrors).length === 1 ? 'Uma coluna JSONPath não pôde ser usada' : `${Object.keys(colErrors).length} colunas JSONPath não puderam ser usadas`}: {Object.values(colErrors)[0]}
          <button className="link" onClick={() => setJpOpen(true)}>Ajustar colunas</button>
        </div>
      )}
      <div className="grid-main">
        <div ref={setScrollEl} className={`grid-scroll ${edges.left ? 'sl' : ''} ${edges.right ? 'sr' : ''}`} onScroll={updateEdges} role="grid" aria-rowcount={sorted.length} aria-label={plural(res.kind)}>
          <div className="grid-inner" style={{ minWidth }}>
            <div className="grid-head" role="row" style={{ gridTemplateColumns: template, height: HEAD_H }}>
              <div className="th td-check stk" role="columnheader" onClick={(e) => e.stopPropagation()}>
                <input type="checkbox" aria-label="Selecionar todos" disabled={sorted.length === 0} checked={allChecked} onChange={() => setChecked(allChecked ? new Set() : new Set(sorted.map((r) => r.uid)))} />
              </div>
              {visibleCols.map((c) => (
                <div key={c.key} role="columnheader" aria-sort={sort.key === c.key ? (sort.dir === 1 ? 'ascending' : 'descending') : 'none'} tabIndex={0}
                  className={`th ${sort.key === c.key ? 'sorted' : ''} ${stickyCls(c)}`} style={c === nameCol ? nameLeft : undefined} title={c.label}
                  onClick={() => sortBy(c.key)}
                  onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), e.stopPropagation(), sortBy(c.key))}>
                  <span className="th-label">{c.label}</span>
                  {sort.key === c.key && sorted.length > 0 && <span className="sort">{sort.dir === 1 ? <ChevronUp size={12} /> : <ChevronDown size={12} />}</span>}
                  <span className="th-resize" title="Arraste para redimensionar; duplo clique ajusta ao conteúdo" onClick={(e) => e.stopPropagation()} onMouseDown={(e) => startColResize(e, c)}
                    onDoubleClick={(e) => (e.stopPropagation(), autoFit(c))} />
                </div>
              ))}
              <div className="th th-actions stk stk-act" role="columnheader" aria-label="Ações" />
            </div>
            <div className="grid-rows" style={{ height: virt.getTotalSize() }}>
              {virt.getVirtualItems().map((vi) => {
                const r = sorted[vi.index]
                const isChecked = checked.has(r.uid)
                return (
                  <div
                    key={r.uid}
                    role="row"
                    aria-rowindex={vi.index + 1}
                    aria-selected={selected === r.uid}
                    className={`tr h-${r.health} ${selected === r.uid ? 'selected' : ''} ${isChecked ? 'checked' : ''}`}
                    style={{ gridTemplateColumns: template, transform: `translateY(${vi.start - HEAD_H}px)` }}
                    onMouseDown={(e) => e.detail > 1 && e.preventDefault()}
                    onClick={(e) => rowClick(e, r)}
                    onContextMenu={(e) => (setSelected(r.uid), openMenu(e, actions.menuFor(r)))}
                    data-testid={`row-${r.name}`}
                  >
                    <div className="td td-check stk" role="gridcell" onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" aria-label={`Selecionar ${r.name}`} checked={isChecked} onChange={() => toggle(r.uid)} />
                    </div>
                    {visibleCols.map((c) => {
                      const v = cellValue(r, c, usage, bkind)
                      return (
                        <div key={c.key} role="gridcell" style={c === nameCol ? nameLeft : undefined}
                          className={`td ${c.key === 'name' ? 'name' : ''} ${c.type === 'number' || c.type === 'age' || c.key === 'ready' ? 'num' : ''} ${stickyCls(c)}`}
                          title={typeof v === 'object' ? undefined : c.type === 'age' && v ? absDate(String(v)) : String(v ?? '')}>
                          {c.key === 'name' && hasHealth && <span className={`dot d-${r.health}`} />}
                          {render(v, c, max, setNs, r)}
                        </div>
                      )
                    })}
                    <div className="td td-actions stk stk-act" role="gridcell">
                      <button className="icon-btn row-kebab" title="Ações" aria-label={`Ações de ${r.name}`} onDoubleClick={(e) => e.stopPropagation()}
                        onClick={(e) => (setSelected(r.uid), openMenu(e, actions.menuFor(r)))}>
                        <EllipsisVertical size={15} />
                      </button>
                    </div>
                  </div>
                )
              })}
            </div>
            {loading && !rows.length && (
              <div className="pad grid-sticky-msg" style={{ display: 'grid', gap: 12 }}>
                {Array.from({ length: 8 }, (_, i) => <div key={i} className="skeleton" style={{ width: `${60 + ((i * 17) % 35)}%` }} />)}
              </div>
            )}
            {!loading && !error && sorted.length === 0 && (
              <div className="empty grid-sticky-msg">
                <KindIcon size={32} />
                <div>{rows.length ? 'Nenhum item com esse filtro.' : `Nenhum recurso do tipo ${plural(res.kind)} ${namespace ? `em ${namespace}` : 'no cluster'}.`}</div>
                {!rows.length && namespace && <div className="muted">Selecione "Todos os namespaces" para ver os demais.</div>}
              </div>
            )}
          </div>
        </div>
        {drawer && drawerParams && (
          <aside className="drawer" style={{ width: drawerW }} data-testid="drawer" onKeyDown={(e) => {
            if (e.key === 'Escape' && !(e.target as HTMLElement).closest('input, textarea, .monaco-editor')) (e.stopPropagation(), setDrawer(undefined))
          }}>
            <div className="drawer-resizer" onMouseDown={startDrawerResize} />
            <ObjectDetails key={drawer.uid} params={drawerParams} variant="drawer" onClose={() => setDrawer(undefined)}
              onPopOut={() => { openTab(drawer); setDrawer(undefined) }} />
          </aside>
        )}
      </div>
      {actions.dialogs}
      {jpOpen && <JsonPathDialog colKey={colKey} kind={res.kind} errors={colErrors} onClose={() => setJpOpen(false)} />}
    </div>
  )
}

const DEFAULT_HIDDEN: string[] = []
const NO_WIDTHS: Record<string, number> = {}
const NO_JP: { expr: string; label: string }[] = []
