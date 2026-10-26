import { useEffect, useMemo, useRef, useState } from 'react'
import { api, type SearchHit } from '../../api'
import { clusterName, useApp, useClusterColor } from '../../store'
import { openGrid } from '../shell/Sidebar'
import { openObject } from '../../components/ObjectPanel'
import { Search, type LucideIcon } from 'lucide-react'
import { COMPONENT_ICONS, kindIcon } from '../shell/nav'
import { TOOLS } from '../workspace/tools'

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
  useEffect(() => input.current?.focus(), [])

  useEffect(() => {
    const term = q.trim()
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
  }, [q, connected])

  const items: Item[] = useMemo(() => {
    const f = q.trim().toLowerCase()
    const out: Item[] = []
    // Empty query: suggest the screens and the most used types of the cluster in focus.
    const home = active && connected[active] ? active : Object.keys(connected)[0]
    if (!f && home) {
      for (const t of TOOLS) if (t.id !== 'create') out.push({ id: `tool:${home}:${t.id}`, clusterId: home, title: t.label, detail: clusterName(home), kind: 'tool', icon: COMPONENT_ICONS[TOOL_ICON[t.id] ?? t.id] ?? kindIcon(), run: () => t.open(home) })
      for (const k of SUGGESTED) {
        const r = connected[home].resources.find((x) => x.kind === k)
        if (r) out.push({ id: `type:${home}:${r.group}/${r.resource}`, clusterId: home, title: r.kind, detail: `tipo · ${clusterName(home)}`, kind: 'type', icon: kindIcon(r.kind), run: () => openGrid(home, r) })
      }
      return out
    }
    for (const [id, st] of Object.entries(connected)) {
      for (const t of TOOLS) {
        if (f && t.label.toLowerCase().includes(f)) out.push({ id: `tool:${id}:${t.id}`, clusterId: id, title: t.label, detail: clusterName(id), kind: 'tool', icon: COMPONENT_ICONS[TOOL_ICON[t.id] ?? t.id] ?? kindIcon(), run: () => t.open(id) })
      }
      if (f.length >= 2) {
        for (const r of st.resources) {
          if (r.kind.toLowerCase().startsWith(f) || r.resource.startsWith(f) || r.shortNames?.includes(f)) {
            out.push({ id: `type:${id}:${r.group}/${r.resource}`, clusterId: id, title: r.kind, detail: `tipo · ${clusterName(id)}`, kind: 'type', icon: kindIcon(r.kind), run: () => openGrid(id, r) })
          }
        }
      }
    }
    for (const { clusterId, hit } of hits) {
      out.push({
        id: `obj:${clusterId}:${hit.kind}/${hit.namespace}/${hit.name}`,
        clusterId,
        title: hit.name,
        detail: `${hit.kind}${hit.namespace ? ' · ' + hit.namespace : ''} · ${clusterName(clusterId)}${hit.match !== 'name' ? ' · ' + hit.match : ''}`,
        kind: 'object',
        icon: kindIcon(hit.kind),
        run: () => openObject(clusterId, hit.group, hit.kind, hit.name, hit.namespace),
      })
    }
    return out.slice(0, 80)
  }, [q, connected, hits, active])

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
          placeholder={Object.keys(connected).length ? 'Buscar objetos (nome, label, imagem), tipos e ferramentas…' : 'Conecte um cluster para buscar'}
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
        <div className="palette-list">
          {items.map((it, i) => (
            <div key={it.id} className={`palette-item ${i === sel ? 'selected' : ''}`} onMouseEnter={() => setSel(i)} onClick={() => go(it)} data-testid="palette-item">
              <it.icon size={16} className="p-icon" />
              <span className={`pk pk-${it.kind}`}>{it.kind === 'object' ? 'obj' : it.kind === 'type' ? 'tipo' : 'tela'}</span>
              <b>{it.title}</b>
              <span className="muted small ellipsis">{it.detail}</span>
              <span className="spacer" />
              <Dot id={it.clusterId} />
            </div>
          ))}
          {!busy && q.trim().length >= 2 && !items.length && <div className="pad small muted">Nada encontrado.</div>}
        </div>
        <div className="palette-foot muted"><span><kbd>↑</kbd> <kbd>↓</kbd> navegar</span><span><kbd>Enter</kbd> abrir</span><span><kbd>Esc</kbd> fechar</span></div>
      </div>
    </div>
  )
}
