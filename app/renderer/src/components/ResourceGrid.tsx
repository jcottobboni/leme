import { useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type Column, type Row, type Usage } from '../api'
import { findResource, openPanel, useApp, useUi } from '../store'
import { useWatch } from '../useWatch'
import { kindIcon, plural } from '../features/shell/nav'
import { openMenu } from '../features/shell/menu'
import { RESTARTABLE, useRowActions } from '../features/grid/actions'
import { ObjectDetails, type ObjectParams } from './ObjectPanel'
import { ChevronDown, ChevronUp, Columns3, Copy, EllipsisVertical, RotateCw, Search, Square, SquareCheck, Trash2, TriangleAlert } from 'lucide-react'
import { age, bytes, cpu, healthOfStatus } from '../format'

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

function render(v: unknown, col: Column, max: Record<string, number>, onNamespace: (ns: string) => void) {
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
  if (col.type === 'status') return <span className={`pill pill-${healthOfStatus(String(v))}`}>{String(v)}</span>
  if (col.key === 'ready' && typeof v === 'string' && v.includes('/')) return <span className={`ready-chip ${readyHealth(v)}`}>{v}</span>
  if (col.key === 'namespace') {
    return <span className="ns-link" title={`Filtrar pelo namespace ${v}`} onClick={(e) => (e.stopPropagation(), onNamespace(String(v)))}>{String(v)}</span>
  }
  if (col.key === 'url' && /^https?:\/\//.test(String(v))) {
    return <a href={String(v)} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>{String(v)}</a>
  }
  return String(v)
}

const CHECK_W = 34
const ACTIONS_W = 40
const SCROLLBAR_W = 10 // matches ::-webkit-scrollbar in styles.css (both head and body reserve it)

export function ResourceGrid({ params }: IDockviewPanelProps<GridParams>) {
  const { clusterId, group, resource } = params
  const res = findResource(clusterId, group, resource)
  const selectedNs = useApp((s) => s.namespace[clusterId] ?? '')
  const namespace = res?.namespaced ? selectedNs : ''
  const { columns, rows, loading, error } = useWatch(clusterId, group, resource, namespace, !!res)
  const usage = useMetrics(clusterId, res?.kind, namespace)
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 }>({ key: 'name', dir: 1 })
  const [selected, setSelected] = useState<string>()
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [drawer, setDrawer] = useState<{ uid: string; name: string; namespace?: string }>()
  const colKey = `${group}/${resource}`
  const hidden = useUi((s) => s.columns[colKey]) ?? DEFAULT_HIDDEN
  const widths = useUi((s) => s.widths[colKey]) ?? NO_WIDTHS
  const drawerW = useUi((s) => s.drawer)
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const tableRef = useRef<HTMLDivElement>(null)
  const KindIcon = kindIcon(res?.kind)

  const visibleCols = useMemo(() => columns.filter((c) => !(c.key === 'namespace' && namespace) && (c.key === 'name' || !hidden.includes(c.key))), [columns, namespace, hidden])

  const sorted = useMemo(() => {
    const f = q.trim().toLowerCase()
    const list = f
      ? rows.filter((r) => r.name.toLowerCase().includes(f) || (r.namespace ?? '').includes(f) || Object.entries(r.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(f)) || Object.values(r.cells).some((v) => typeof v !== 'object' && String(v ?? '').toLowerCase().includes(f)))
      : rows.slice()
    const col = columns.find((c) => c.key === sort.key)
    list.sort((a, b) => {
      let va = col ? cellValue(a, col, usage, res?.kind) : a.name
      let vb = col ? cellValue(b, col, usage, res?.kind) : b.name
      if (col?.type === 'age') {
        // older first when ascending by age
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
  }, [rows, q, sort, columns, usage, res?.kind])

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

  const virt = useVirtualizer({ count: sorted.length, getScrollElement: () => parentRef.current, estimateSize: () => 32, overscan: 20 })
  // Columns share the panel width in proportion to their default size and shrink down to a floor
  // (the widest free-text column, e.g. Message, shrinks first); a width set by hand stays fixed.
  const flex = visibleCols.reduce<Column | undefined>((m, c) => (c.key !== 'name' && (c.width ?? 0) >= 280 && (c.width ?? 0) > (m?.width ?? 0) ? c : m), undefined)
  let minWidth = CHECK_W + ACTIONS_W
  const template = `${CHECK_W}px ` + visibleCols.map((c) => {
    if (widths[c.key]) return (minWidth += widths[c.key], `${widths[c.key]}px`)
    const w = c.width ?? 140
    const min = c === flex ? 140 : c.key === 'name' ? Math.min(w, 200) : c.type === 'status' ? w : Math.max(56, Math.round(w * 0.75))
    minWidth += min
    return `minmax(${min}px, ${w}fr)`
  }).join(' ') + ` ${ACTIONS_W}px`
  const counts = useMemo(() => {
    const c = { ok: 0, warn: 0, error: 0 }
    for (const r of rows) if (r.health in c) c[r.health as keyof typeof c]++
    return c
  }, [rows])

  const objParams = (r: Row): ObjectParams | undefined =>
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
  const openTab = (r: Row) => {
    const p = objParams(r)
    if (p) openPanel(`obj:${clusterId}:${group}/${resource}:${r.namespace ?? ''}/${r.name}`, 'object', r.name, { ...p }, {})
  }
  const actions = useRowActions(clusterId, res, { drawer: openDrawer, tab: openTab })
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
    openMenu(e, columns.filter((c) => c.key !== 'name').map((c) => ({
      label: c.label,
      icon: hidden.includes(c.key) ? Square : SquareCheck,
      onClick: () => useUi.getState().update({ columns: { ...useUi.getState().columns, [colKey]: hidden.includes(c.key) ? hidden.filter((k) => k !== c.key) : [...hidden, c.key] } }),
    })))

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

  if (!res) return <div className="pad muted">Recurso não disponível neste cluster.</div>
  const drawerParams = drawer && objParams({ uid: drawer.uid, name: drawer.name, namespace: drawer.namespace, health: 'neutral', cells: {} })

  return (
    <div className="grid-panel" onKeyDown={onKey} tabIndex={-1}>
      <div className="toolbar">
        <span className="grid-title"><KindIcon size={16} />{plural(res.kind)}</span>
        {res.namespaced && <NamespaceSelect clusterId={clusterId} />}
        <label className="search-box">
          <Search size={14} />
          <input ref={searchRef} className="search" placeholder="Buscar (/)…" value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
        <span className="count-pill">{sorted.length === rows.length ? `${rows.length} ${rows.length === 1 ? 'item' : 'itens'}` : `${sorted.length} de ${rows.length}`}</span>
        <span className="health-counts">
          {counts.ok > 0 && <span className="s-ok">● {counts.ok}</span>}
          {counts.warn > 0 && <span className="s-warn">● {counts.warn}</span>}
          {counts.error > 0 && <span className="s-error">● {counts.error}</span>}
        </span>
        {loading && <span className="spinner" />}
        <span className="spacer" />
        <button className="icon-btn" title="Colunas" onClick={columnMenu} data-testid="grid-columns"><Columns3 size={16} /></button>
      </div>
      {checked.size > 0 && (
        <div className="bulkbar" data-testid="bulkbar">
          <b>{checked.size} selecionado{checked.size === 1 ? '' : 's'}</b>
          <button onClick={() => navigator.clipboard.writeText(checkedRows.map((r) => r.name).join('\n')).then(() => useApp.getState().toast('ok', 'Nomes copiados'))}><Copy size={14} /> Copiar nomes</button>
          {RESTARTABLE.includes(res.kind) && <button disabled={actions.readOnly} onClick={() => actions.ask('restart', checkedRows)}><RotateCw size={14} /> Restart</button>}
          <button className="danger" disabled={actions.readOnly} title={actions.readOnly ? 'Cluster em somente leitura' : ''} onClick={() => actions.ask('delete', checkedRows)} data-testid="bulk-delete">
            <Trash2 size={14} /> Excluir {checked.size}
          </button>
          <span className="spacer" />
          <button className="link" onClick={() => setChecked(new Set())}>Limpar seleção</button>
        </div>
      )}
      {error && <div className="banner error"><TriangleAlert size={14} />{error}</div>}
      <div className="grid-main">
        <div className="grid-table" ref={tableRef}>
          <div className="grid-head" style={{ gridTemplateColumns: template, minWidth: minWidth + SCROLLBAR_W }}>
            <div className="th td-check" onClick={(e) => e.stopPropagation()}>
              <input type="checkbox" aria-label="Selecionar todos" checked={allChecked} onChange={() => setChecked(allChecked ? new Set() : new Set(sorted.map((r) => r.uid)))} />
            </div>
            {visibleCols.map((c) => (
              <div key={c.key} className={`th ${sort.key === c.key ? 'sorted' : ''}`} onClick={() => setSort((s) => ({ key: c.key, dir: s.key === c.key ? ((-s.dir) as 1 | -1) : 1 }))}>
                {c.label}
                {sort.key === c.key && <span className="sort">{sort.dir === 1 ? <ChevronUp size={12} /> : <ChevronDown size={12} />}</span>}
                <span className="th-resize" onClick={(e) => e.stopPropagation()} onMouseDown={(e) => startColResize(e, c)} />
              </div>
            ))}
            <div />
          </div>
          <div ref={parentRef} className="grid-body" style={{ minWidth: minWidth + SCROLLBAR_W }}>
            <div style={{ height: virt.getTotalSize(), position: 'relative' }}>
              {virt.getVirtualItems().map((vi) => {
                const r = sorted[vi.index]
                const isChecked = checked.has(r.uid)
                return (
                  <div
                    key={r.uid}
                    className={`tr h-${r.health} ${selected === r.uid ? 'selected' : ''} ${isChecked ? 'checked' : ''}`}
                    style={{ gridTemplateColumns: template, transform: `translateY(${vi.start}px)` }}
                    onMouseDown={(e) => e.detail > 1 && e.preventDefault()}
                    onClick={(e) => rowClick(e, r)}
                    onContextMenu={(e) => (setSelected(r.uid), openMenu(e, actions.menuFor(r)))}
                    data-testid={`row-${r.name}`}
                  >
                    <div className="td td-check" onClick={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" aria-label={`Selecionar ${r.name}`} checked={isChecked} onChange={() => toggle(r.uid)} />
                    </div>
                    {visibleCols.map((c) => {
                      const v = cellValue(r, c, usage, res.kind)
                      return (
                        <div key={c.key} className={`td ${c.key === 'name' ? 'name' : ''}`} title={typeof v === 'object' ? undefined : String(v ?? '')}>
                          {c.key === 'name' && <span className={`dot d-${r.health}`} />}
                          {render(v, c, max, setNs)}
                        </div>
                      )
                    })}
                    <div className="td td-actions">
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
              <div className="pad" style={{ display: 'grid', gap: 12 }}>
                {Array.from({ length: 8 }, (_, i) => <div key={i} className="skeleton" style={{ width: `${60 + ((i * 17) % 35)}%` }} />)}
              </div>
            )}
            {!loading && !error && sorted.length === 0 && (
              <div className="empty"><KindIcon size={32} /><div>{rows.length ? 'Nenhum item com esse filtro.' : `Nenhum ${res.kind} ${namespace ? `em ${namespace}` : 'no cluster'}.`}</div></div>
            )}
          </div>
        </div>
        {drawer && drawerParams && (
          <aside className="drawer" style={{ width: drawerW }} data-testid="drawer" onKeyDown={(e) => {
            if (e.key === 'Escape' && !(e.target as HTMLElement).closest('input, textarea, .monaco-editor')) (e.stopPropagation(), setDrawer(undefined))
          }}>
            <div className="drawer-resizer" onMouseDown={startDrawerResize} />
            <ObjectDetails key={drawer.uid} params={drawerParams} variant="drawer" onClose={() => setDrawer(undefined)}
              onPopOut={() => { openTab({ uid: drawer.uid, name: drawer.name, namespace: drawer.namespace, health: 'neutral', cells: {} }); setDrawer(undefined) }} />
          </aside>
        )}
      </div>
      {actions.dialogs}
    </div>
  )
}

const DEFAULT_HIDDEN: string[] = []
const NO_WIDTHS: Record<string, number> = {}
