import { useEffect, useMemo, useRef, useState } from 'react'
import { api, type SearchHit } from '../../api'
import { clusterName, findByKind, useApp, useClusterColor } from '../../store'
import { openGrid } from '../shell/Sidebar'
import { openObject } from '../../components/ObjectPanel'
import { Search, type LucideIcon } from 'lucide-react'
import { behaviorKind, COMPONENT_ICONS, isBuiltinGroup, kindIcon } from '../shell/nav'
import { TOOLS } from '../workspace/tools'
import { gridText, isStructured, matchHit, parseFilter, presetGridFilter, resolveAlias } from './filter'
import { FILTER_HELP, FilterChips } from './FilterChips'

interface Item {
  id: string
  clusterId: string
  title: string
  detail: string
  kind: 'object' | 'type' | 'tool'
  icon: LucideIcon
  run: () => void
}

const TOOL_ICON: Record<string, string> = { helm: 'helmReleases', charts: 'helmCharts' }
const GROUP_LABEL: Record<Item['kind'], string> = { tool: 'Telas', type: 'Tipos de recurso', object: 'Objetos' }
const ORDER: Item['kind'][] = ['tool', 'type', 'object']
const SUGGESTED = ['Pod', 'Deployment', 'Service', 'ConfigMap', 'Secret', 'Ingress', 'Event', 'Node']

function Dot({ id }: { id: string }) {
  const c = useClusterColor(id)
  return <span className="cdot" style={{ background: c }} />
}

/** Ctrl+K: jump to resource types, tools or objects (name, label, image) across connected clusters. */
export function Palette({ onClose }: { onClose: () => void }) {
  const connected = useApp((s) => s.connected)
  const active = useApp((s) => s.activeCluster)
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<{ clusterId: string; hit: SearchHit }[]>([])
  const [busy, setBusy] = useState(false)
  const [sel, setSel] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  // With a single cluster connected, repeating its name on every row is noise.
  const multi = Object.keys(connected).length > 1
  const where = (id: string, extra = '') => [extra, multi ? clusterName(id) : ''].filter(Boolean).join(' · ')
  useEffect(() => input.current?.focus(), [])
  // M7 RF05: the same k9s syntax as the grid search. `:alias` navigates; the other terms either go to
  // the grid it opens or filter the object hits here.
  const filter = useMemo(() => parseFilter(q), [q])
  const selector = filter.labelSelector
  // The engine search takes one word: the first free text, else a simple k=v selector (labels are indexed).
  const term = filter.alias ? '' : filter.include[0] ?? (selector && /^[\w./-]+=[\w.-]+$/.test(selector) ? selector : '')

  useEffect(() => {
    if (term.length < 2) {
      setHits([])
      return
    }
    let stop = false
    const t = setTimeout(() => {
      setBusy(true)
      Promise.all(
        Object.keys(connected).map((id) =>
          api.search(id, term).then(
            (r) => r.hits.map((hit) => ({ clusterId: id, hit })),
            () => [],
          ),
        ),
      )
        .then((all) => !stop && setHits(all.flat()))
        .finally(() => !stop && setBusy(false))
    }, 200)
    return () => {
      stop = true
      clearTimeout(t)
    }
  }, [term, connected])

  const items: Item[] = useMemo(() => {
    const f = filter.include.join(' ')
    const out: Item[] = []
    if (filter.alias) {
      // `:deploy demo -l app=web`: resource types for the alias; Enter opens the grid already filtered.
      const ns = filter.namespaces[0]
      const rest = gridText(filter)
      for (const [id, st] of Object.entries(connected)) {
        for (const r of resolveAlias(st.resources, filter.alias)) {
          const scope = r.namespaced ? (ns ? `ns ${ns}` : '') : ''
          out.push({
            id: `type:${id}:${r.group}/${r.resource}`, clusterId: id, title: r.kind, kind: 'type', icon: kindIcon(behaviorKind(r.group, r.kind)),
            detail: [where(id, isBuiltinGroup(r.group) ? '' : r.group), scope, rest].filter(Boolean).join(' · '),
            run: () => {
              if (r.namespaced && ns) useApp.getState().setNamespace(id, ns)
              presetGridFilter(id, r.group, r.resource, rest)
              openGrid(id, r)
            },
          })
        }
      }
      return out.slice(0, 80)
    }
    // Empty query: suggest the screens and the most used types of the cluster in focus.
    const home = active && connected[active] ? active : Object.keys(connected)[0]
    if (!q.trim() && home) {
      for (const t of TOOLS) if (t.id !== 'create') out.push({ id: `tool:${home}:${t.id}`, clusterId: home, title: t.label, detail: where(home), kind: 'tool', icon: COMPONENT_ICONS[TOOL_ICON[t.id] ?? t.id] ?? kindIcon(), run: () => t.open(home) })
      for (const k of SUGGESTED) {
        // Built-in first: a CRD reusing the name (kind Node) is not the suggested Nodes.
        const r = findByKind(home, k)
        if (r) out.push({ id: `type:${home}:${r.group}/${r.resource}`, clusterId: home, title: r.kind, detail: where(home), kind: 'type', icon: kindIcon(behaviorKind(r.group, r.kind)), run: () => openGrid(home, r) })
      }
      return out
    }
    for (const [id, st] of Object.entries(connected)) {
      for (const t of TOOLS) {
        if (f && t.label.toLowerCase().includes(f)) out.push({ id: `tool:${id}:${t.id}`, clusterId: id, title: t.label, detail: where(id), kind: 'tool', icon: COMPONENT_ICONS[TOOL_ICON[t.id] ?? t.id] ?? kindIcon(), run: () => t.open(id) })
      }
      if (f.length >= 2) {
        for (const r of st.resources) {
          if (r.kind.toLowerCase().startsWith(f) || r.resource.startsWith(f) || r.shortNames?.includes(f)) {
            out.push({ id: `type:${id}:${r.group}/${r.resource}`, clusterId: id, title: r.kind, detail: where(id, isBuiltinGroup(r.group) ? '' : r.group), kind: 'type', icon: kindIcon(behaviorKind(r.group, r.kind)), run: () => openGrid(id, r) })
          }
        }
      }
    }
    for (const { clusterId, hit } of hits) {
      if (!matchHit(hit, filter)) continue
      out.push({
        id: `obj:${clusterId}:${hit.kind}/${hit.namespace}/${hit.name}`,
        clusterId,
        title: hit.name,
        detail: [`${hit.kind}${hit.namespace ? ' · ' + hit.namespace : ''}`, multi ? clusterName(clusterId) : '', hit.match !== 'name' ? hit.match : ''].filter(Boolean).join(' · '),
        kind: 'object',
        icon: kindIcon(behaviorKind(hit.group, hit.kind)),
        run: () => openObject(clusterId, hit.group, hit.kind, hit.name, hit.namespace),
      })
    }
    return out.sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind)).slice(0, 80)
  }, [q, filter, connected, hits, active]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => setSel(0), [q])
  const go = (it?: Item) => {
    if (!it) return
    it.run()
    onClose()
  }

  return (
    <div className="modal-backdrop palette-backdrop" onMouseDown={onClose}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()} role="dialog" aria-label="Busca global">
        <div className="palette-search">
        <Search size={18} />
        <input
          ref={input}
          className="palette-input"
          placeholder={Object.keys(connected).length ? 'Buscar objetos, tipos e ferramentas… (:deploy demo, -l app=web, !texto, /regex/)' : 'Conecte um cluster para buscar'}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          data-testid="palette-input"
          onKeyDown={(e) => {
            if (e.key === 'Escape') onClose()
            if (e.key === 'ArrowDown') (e.preventDefault(), setSel((s) => Math.min(items.length - 1, s + 1)))
            if (e.key === 'ArrowUp') (e.preventDefault(), setSel((s) => Math.max(0, s - 1)))
            if (e.key === 'Enter') go(items[sel])
          }}
        />
        {busy && <span className="spinner" />}
        </div>
        <FilterChips filter={filter} onChange={setQ} testid="palette-chips" />
        {isStructured(filter) && !filter.alias && !term && <div className="palette-help" data-testid="palette-hint">Combine com um texto (ex.: <code>web !canary</code>) ou comece com um tipo: <code>:deploy demo -l app=web</code>.</div>}
        {/^[:\-!/]|^ns:/.test(q.trim()) && q.trim().length < 3 && <div className="palette-help">{FILTER_HELP}</div>}
        <div className="palette-list" role="listbox" aria-label="Resultados">
          {items.map((it, i) => (
            <div key={it.id} role="option" aria-selected={i === sel}>
              {(i === 0 || items[i - 1].kind !== it.kind) && <div className="palette-group" role="presentation">{GROUP_LABEL[it.kind]}</div>}
              <div className={`palette-item ${i === sel ? 'selected' : ''}`} onMouseEnter={() => setSel(i)} onClick={() => go(it)} data-testid="palette-item">
                <it.icon size={16} className="p-icon" />
                <b>{it.title}</b>
                <span className="muted small ellipsis">{it.detail}</span>
                <span className="spacer" />
                {multi && <Dot id={it.clusterId} />}
              </div>
            </div>
          ))}
          {!busy && q.trim().length >= 2 && !items.length && <div className="pad small muted">{filter.alias ? `Nenhum tipo de recurso para :${filter.alias}.` : 'Nada encontrado.'}</div>}
        </div>
        <div className="palette-foot muted"><span><kbd>↑</kbd> <kbd>↓</kbd> navegar</span><span><kbd>Enter</kbd> abrir</span><span><kbd>Esc</kbd> fechar</span></div>
      </div>
    </div>
  )
}
