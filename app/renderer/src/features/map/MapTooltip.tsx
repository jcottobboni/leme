// Hover tooltip of the cluster map (tile, app region or namespace island).
import type { ReactNode } from 'react'
import { Server } from 'lucide-react'
import { kindIcon } from '../shell/nav'
import type { Layout } from './layout'

export type HoverTarget = { kind: 'tile' | 'isl' | 'app'; id: number }

/** Tooltip body for the hovered element, or null when there is nothing to show. */
export function tooltipContent(hover: HoverTarget | null, layout: Layout | null, compact?: boolean): ReactNode {
  if (!hover || !layout) return null
  if (hover.kind === 'tile') {
    const t = layout.tiles[hover.id]
    if (!t) return null
    const w = t.w
    const Icon = kindIcon(w.kind)
    return (
      <>
        <div className="tip-head">
          <span className={`tip-ico ${w.health}`}><Icon size={14} /></span>
          <div className="tip-title"><h5>{w.name}</h5><span>{w.kind} · {t.ns}</span></div>
          <span className={`tip-health ${w.health}`}>{HEALTH_LABEL[w.health] ?? w.health}</span>
        </div>
        <PodStrip c={w.pods} />
        {t.app && t.app !== w.name && <Row k="App" v={t.app} />}
        {w.status && <Row k="Status" v={w.status} />}
        {w.ready && <Row k="Prontos" v={w.ready} />}
        {!!w.restarts && <Row k="Reinícios" v={String(w.restarts)} warn={w.restarts >= 5} />}
        {!!w.services?.length && <Row k="Services" v={w.services.join(', ')} />}
        {!!w.hosts?.length && <Row k="Ingress" v={w.hosts.join(', ')} />}
        {!!w.pvcs && <Row k="PVCs" v={String(w.pvcs)} />}
        {compact && <div className="muted small" style={{ marginTop: 4 }}>Mapa grande: status e services no painel do objeto</div>}
        <div className="tip-hint">Clique para os detalhes · duplo clique para a topologia</div>
      </>
    )
  }
  if (hover.kind === 'app') {
    const a = layout.appsFlat[hover.id]
    const pods = a.tiles.reduce((x, t) => ({ total: x.total + t.w.pods.total, ok: x.ok + t.w.pods.ok, warn: x.warn + t.w.pods.warn, error: x.error + t.w.pods.error }), { total: 0, ok: 0, warn: 0, error: 0 })
    return (
      <>
        <div className="tip-head"><div className="tip-title"><h5>{a.name}</h5><span>app em {a.ns}</span></div></div>
        <PodStrip c={pods} />
        <Row k="Workloads" v={String(a.tiles.length)} />
        <div className="tip-hint">Clique para ampliar</div>
      </>
    )
  }
  const i = layout.islands[hover.id]
  if (!i) return null
  const n = i.ns
  return (
    <>
      <div className="tip-head">
        <span className="tip-ico ns" style={{ '--h': i.hue } as React.CSSProperties}><Server size={14} /></span>
        <div className="tip-title"><h5>{n.name}</h5><span>namespace</span></div>
        {n.status && <span className={`tip-health ${n.status === 'Active' ? 'ok' : 'warn'}`}>{n.status}</span>}
      </div>
      <PodStrip c={n.pods} />
      <Row k="Workloads" v={String(i.workloads)} />
      <Row k="Pods" v={`${n.pods.total} (${n.pods.ok} ok · ${n.pods.warn} atenção · ${n.pods.error} erro)`} />
      <Row k="Services" v={String(n.services)} /><Row k="Ingresses" v={String(n.ingresses)} /><Row k="PVCs" v={String(n.pvcs)} />
      <div className="tip-hint">Clique para ampliar · duplo clique para a topologia</div>
    </>
  )
}

function Row({ k, v, warn }: { k: string; v: string; warn?: boolean }) {
  return <div className="row"><span>{k}</span><b className={warn ? 'warn' : undefined}>{v}</b></div>
}

const HEALTH_LABEL: Record<string, string> = { ok: 'Saudável', warn: 'Atenção', error: 'Erro', neutral: 'Neutro' }

/** Stacked pod bar with the counts, for the tooltip. */
function PodStrip({ c }: { c: { total: number; ok: number; warn: number; error: number } }) {
  if (!c.total) return <div className="tip-strip-empty">Sem pods</div>
  const other = Math.max(0, c.total - c.ok - c.warn - c.error)
  const pct = (n: number) => `${(n / c.total) * 100}%`
  return (
    <div className="tip-strip">
      <div className="bar">
        {!!c.ok && <i className="ok" style={{ width: pct(c.ok) }} />}
        {!!c.warn && <i className="warn" style={{ width: pct(c.warn) }} />}
        {!!c.error && <i className="error" style={{ width: pct(c.error) }} />}
        {!!other && <i className="other" style={{ width: pct(other) }} />}
      </div>
      <div className="nums">
        <span><b>{c.total}</b> pod{c.total === 1 ? '' : 's'}</span>
        {!!c.ok && <span className="ok">{c.ok} ok</span>}
        {!!c.warn && <span className="warn">{c.warn} atenção</span>}
        {!!c.error && <span className="error">{c.error} erro</span>}
      </div>
    </div>
  )
}
