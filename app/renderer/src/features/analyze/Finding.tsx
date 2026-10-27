import { openCopilot } from '../ai/CopilotPanel'
import { openObject } from '../../components/ObjectPanel'
import { SEV_LABEL, type Diagnosis } from './api'
import './analyze.css'

/** One analyzer finding: severity, explanation, collapsible evidence and suggestion. */
export function FindingCard({ clusterId, f, showObject }: { clusterId: string; f: Diagnosis; showObject: boolean }) {
  const o = f.object
  return (
    <div className="an-finding" data-testid={`finding-${f.id}`}>
      <div className="an-head">
        <span className={`sev sev-${f.severity}`}>{SEV_LABEL[f.severity] ?? f.severity}</span>
        <span className="an-title">{f.title}</span>
        <button onClick={() => openCopilot(clusterId, { group: o.group, kind: o.kind, namespace: o.namespace, name: o.name })} data-testid="finding-ask-ai">Perguntar à IA</button>
      </div>
      {showObject && (
        <div className="an-obj">
          <span className="muted">{o.kind}</span>
          <button className="link mono" onClick={() => openObject(clusterId, o.group, o.kind, o.name, o.namespace)}>{o.name}</button>
          {o.namespace && <span className="muted">{o.namespace}</span>}
        </div>
      )}
      <div>{f.explanation}</div>
      {f.evidence.length > 0 && (
        <details className="an-evidence">
          <summary>Evidências ({f.evidence.length})</summary>
          <pre data-testid="finding-evidence">{f.evidence.join('\n')}</pre>
        </details>
      )}
      <div className="an-suggestion"><b>Sugestão:</b> {f.suggestion}</div>
    </div>
  )
}
