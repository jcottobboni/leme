import { useMemo, useState } from 'react'
import type { Finding, Severity } from '../../api'
import { openObject } from '../../components/ObjectPanel'

export const SEVERITIES: Severity[] = ['critical', 'high', 'medium', 'low', 'info']
export const SEV_LABEL: Record<string, string> = { critical: 'Crítico', high: 'Alto', medium: 'Médio', low: 'Baixo', info: 'Info' }

export function Sev({ s }: { s: string }) {
  return <span className={`sev sev-${s}`}>{SEV_LABEL[s] ?? s}</span>
}

export function ScoreRing({ score, size = 84 }: { score: number; size?: number }) {
  const r = size / 2 - 7
  const c = 2 * Math.PI * r
  const cls = score >= 80 ? 'ok' : score >= 60 ? 'warn' : 'error'
  return (
    <svg width={size} height={size} className="score-ring" data-testid="score">
      <circle cx={size / 2} cy={size / 2} r={r} className="ring-bg" />
      <circle cx={size / 2} cy={size / 2} r={r} className={`ring-fg r-${cls}`} strokeDasharray={`${(score / 100) * c} ${c}`} transform={`rotate(-90 ${size / 2} ${size / 2})`} />
      <text x="50%" y="50%" dominantBaseline="central" textAnchor="middle">{score}</text>
    </svg>
  )
}

export function Counts({ counts }: { counts: Record<string, number> }) {
  return (
    <div className="sev-counts">
      {SEVERITIES.filter((s) => counts[s]).map((s) => (
        <span key={s} className={`sev sev-${s}`}>{counts[s]} {SEV_LABEL[s].toLowerCase()}</span>
      ))}
      {!SEVERITIES.some((s) => counts[s]) && <span className="muted">nenhum achado</span>}
    </div>
  )
}

/** Filterable findings table; rows open the object. */
export function FindingsTable({ clusterId, findings, showObject = true }: { clusterId: string; findings: Finding[]; showObject?: boolean }) {
  const [sev, setSev] = useState<string>('')
  const [cat, setCat] = useState('')
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<number>()
  const cats = useMemo(() => [...new Set(findings.map((f) => f.category).filter(Boolean))].sort(), [findings])
  const shown = useMemo(
    () =>
      findings
        .filter((f) => (!sev || f.severity === sev) && (!cat || f.category === cat) && (!q || `${f.title} ${f.name} ${f.namespace} ${f.container} ${f.id}`.toLowerCase().includes(q.toLowerCase())))
        .sort((a, b) => SEVERITIES.indexOf(a.severity) - SEVERITIES.indexOf(b.severity) || (a.category ?? '').localeCompare(b.category ?? '') || (a.title ?? '').localeCompare(b.title ?? '')),
    [findings, sev, cat, q],
  )
  return (
    <div className="findings">
      <div className="toolbar">
        <select value={sev} onChange={(e) => setSev(e.target.value)}>
          <option value="">Todas as severidades</option>
          {SEVERITIES.map((s) => <option key={s} value={s}>{SEV_LABEL[s]}</option>)}
        </select>
        <select value={cat} onChange={(e) => setCat(e.target.value)}>
          <option value="">Todas as categorias</option>
          {cats.map((c) => <option key={c}>{c}</option>)}
        </select>
        <input className="search" placeholder="Filtrar…" value={q} onChange={(e) => setQ(e.target.value)} />
        <span className="muted">{shown.length} de {findings.length}</span>
      </div>
      <table className="table" data-testid="findings">
        <thead><tr><th>Severidade</th><th>Achado</th>{showObject && <th>Objeto</th>}<th>Container</th><th>Categoria</th><th>Fonte</th></tr></thead>
        <tbody>
          {shown.slice(0, 1000).map((f, i) => (
            <FindingRow key={i} f={f} i={i} open={open === i} onToggle={() => setOpen(open === i ? undefined : i)} clusterId={clusterId} showObject={showObject} />
          ))}
          {!shown.length && <tr><td colSpan={6} className="muted">Nenhum achado com esses filtros.</td></tr>}
        </tbody>
      </table>
      {shown.length > 1000 && <div className="pad muted small">Mostrando 1.000 de {shown.length}; use os filtros.</div>}
    </div>
  )
}

function FindingRow({ f, open, onToggle, clusterId, showObject }: { f: Finding; i: number; open: boolean; onToggle: () => void; clusterId: string; showObject: boolean }) {
  return (
    <>
      <tr className="clickable" onClick={onToggle}>
        <td><Sev s={f.severity} /></td>
        <td className="wrap">{f.title}{f.effective && <span className="muted small" title="Avaliado no pod em execução (inclui o que a admissão aplicou, ex.: SCC do OpenShift)"> · efetivo</span>}</td>
        {showObject && (
          <td>
            <button className="link" onClick={(e) => { e.stopPropagation(); openObject(clusterId, f.group, f.kind, f.name, f.namespace) }}>
              {f.kind}/{f.namespace ? f.namespace + '/' : ''}{f.name}
            </button>
          </td>
        )}
        <td>{f.container}</td>
        <td className="muted">{f.category}</td>
        <td className="muted small">{f.source}</td>
      </tr>
      {open && (
        <tr className="finding-detail">
          <td />
          <td colSpan={5}>
            {f.detail && <div>{f.detail}</div>}
            {f.remediation && <div><b>Como corrigir:</b> {f.remediation}</div>}
            <div className="muted small mono">{f.id}</div>
          </td>
        </tr>
      )}
    </>
  )
}
