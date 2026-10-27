import { useEffect, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { ArrowLeftRight, CircleCheck, GitCompareArrows } from 'lucide-react'
import { api7, type DiffSide } from '../../api'
import { clusterName } from '../../store'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { YamlDiff } from '../../components/Monaco'
import { Empty, ErrorBanner, Loading, useAsync } from '../common'
import { summary } from '../history/format'
import type { DiffParams } from './CompareDialog'
import './diff.css'

function label(s: DiffSide) {
  return `${clusterName(s.cluster)} · ${s.namespace ? `${s.namespace}/` : ''}${s.name}`
}

function show(v: unknown) {
  if (v === undefined) return ''
  const s = typeof v === 'string' ? v : JSON.stringify(v)
  return s.length > 160 ? s.slice(0, 160) + '…' : s
}

/** Side-by-side diff of the same object in two clusters and/or namespaces (normalized by the engine). */
export function DiffPanel({ params }: IDockviewPanelProps<DiffParams>) {
  const [swap, setSwap] = useState(false)
  const left = swap ? params.right : params.left
  const right = swap ? params.left : params.right
  const key = JSON.stringify([left, right])
  const { data, error, loading, reload } = useAsync(() => api7.diff(left, right), [key])
  const [showChanges, setShowChanges] = useState(true)
  useEffect(() => setShowChanges(true), [key])

  const missing = data && (!data.left.found || !data.right.found)
  return (
    <div className="object-panel diff-panel" data-testid="diff-panel">
      <PanelHeader icon={GitCompareArrows} title={`Comparar ${params.kind} ${params.left.name}`} actions={[
        { key: 'swap', label: 'Inverter lados', icon: ArrowLeftRight, onClick: () => setSwap((v) => !v) },
        refreshAction(reload, loading),
      ]} />
      <div className="diff-sides">
        <div className="diff-side" data-testid="diff-left" title={label(left)}><span className="diff-tag">A</span>{label(left)}</div>
        <div className="diff-side" data-testid="diff-right" title={label(right)}><span className="diff-tag">B</span>{label(right)}</div>
      </div>
      <ErrorBanner error={error} />
      {data?.left.error && <div className="banner error">A: {data.left.error}</div>}
      {data?.right.error && <div className="banner error">B: {data.right.error}</div>}
      {data && !data.left.found && !data.left.error && <div className="banner warn" data-testid="diff-missing">A: {params.kind} {left.name} não existe {left.namespace ? `em ${left.namespace}` : 'neste cluster'}.</div>}
      {data && !data.right.found && !data.right.error && <div className="banner warn" data-testid="diff-missing">B: {params.kind} {right.name} não existe {right.namespace ? `em ${right.namespace}` : 'neste cluster'}.</div>}
      {data?.hiddenDiffer?.length ? <div className="banner warn" data-testid="diff-hidden">Valores ocultos diferentes: {data.hiddenDiffer.join(', ')}</div> : null}
      {!data && loading && <Loading label="Comparando…" />}
      {data?.equal && (
        <Empty icon={<CircleCheck size={28} />} title="Sem diferenças" testId="diff-equal">
          O YAML normalizado dos dois lados é igual{data.namespaceIgnored ? ' (o namespace não entra na comparação)' : ''}.
        </Empty>
      )}
      {data && !data.equal && !missing && (
        <>
          {!!data.changes?.length && (
            <div className={`diff-changes ${showChanges ? '' : 'closed'}`} data-testid="diff-changes" data-count={data.changes.length}>
              <button className="link small" onClick={() => setShowChanges((v) => !v)}>
                {data.changes.length} {data.changes.length === 1 ? 'diferença' : 'diferenças'}: {summary(data.changes)}
              </button>
              {showChanges && (
                <ul>
                  {data.changes.map((c, i) => (
                    <li key={i} className="mono small" data-testid="diff-change" data-path={c.path}>
                      <span className={`hist-op hist-${c.op}`}>{c.op === 'add' ? '+' : c.op === 'remove' ? '−' : '~'}</span> {c.path}
                      {c.op !== 'add' && c.old !== undefined && <span className="hist-old"> {show(c.old)}</span>}
                      {c.op === 'replace' && <span className="muted"> →</span>}
                      {c.op !== 'remove' && c.value !== undefined && <span className="hist-new"> {show(c.value)}</span>}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
          {data.namespaceIgnored && <div className="muted small diff-note">Namespaces diferentes: metadata.namespace não entra na comparação.</div>}
          <div className="diff-editor"><YamlDiff original={data.left.yaml} modified={data.right.yaml} /></div>
        </>
      )}
      {data && !data.equal && missing && (data.left.found || data.right.found) && (
        <div className="diff-editor"><YamlDiff original={data.left.yaml} modified={data.right.yaml} /></div>
      )}
    </div>
  )
}
