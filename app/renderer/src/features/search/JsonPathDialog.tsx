import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'
import { Modal } from '../../components/Dialogs'
import { useUi, type JPColumn } from '../../store'
import { normalizeJP } from './filter'
import './search.css'

const EXAMPLES: Record<string, string[]> = {
  Pod: ['.spec.nodeName', '.spec.containers[*].image', '.status.podIP', '.spec.serviceAccountName'],
  Deployment: ['.spec.replicas', '.spec.template.spec.containers[*].image', '.spec.strategy.type'],
  Service: ['.spec.type', '.spec.ports[*].port', '.spec.selector'],
  default: ['.metadata.labels.app', '.metadata.annotations', '.spec'],
}

const NONE: JPColumn[] = []

/** Quick local check (the engine is the judge): balanced braces/brackets and no empty expression. */
function localError(expr: string) {
  if (!expr.trim()) return 'Informe uma expressão.'
  let depth = 0
  for (const ch of expr) {
    if (ch === '{' || ch === '[' || ch === '(') depth++
    if (ch === '}' || ch === ']' || ch === ')') depth--
    if (depth < 0) return 'Chaves, colchetes ou parênteses desbalanceados.'
  }
  if (depth !== 0) return 'Chaves, colchetes ou parênteses desbalanceados.'
  if (expr.length > 256) return 'Expressão longa demais (máximo 256 caracteres).'
  return ''
}

/** "Adicionar coluna JSONPath": custom columns per resource type, saved in the UI preferences. */
export function JsonPathDialog({ colKey, kind, errors, onClose }: { colKey: string; kind: string; errors?: Record<string, string>; onClose: () => void }) {
  const cols = useUi((s) => s.jpCols?.[colKey]) ?? NONE
  const [expr, setExpr] = useState('')
  const [label, setLabel] = useState('')
  const [err, setErr] = useState('')
  const save = (next: JPColumn[]) => useUi.getState().update({ jpCols: { ...useUi.getState().jpCols, [colKey]: next } })
  const add = () => {
    const e = localError(expr)
    if (e) return setErr(e)
    const n = normalizeJP(expr)
    if (cols.some((c) => c.expr === n)) return setErr('Essa coluna já existe.')
    if (cols.length >= 12) return setErr('Limite de 12 colunas JSONPath por tipo.')
    save([...cols, { expr: n, label: label.trim() || expr.trim().replace(/^\{|\}$/g, '') }])
    setExpr('')
    setLabel('')
    setErr('')
  }
  return (
    <Modal title={`Colunas JSONPath · ${kind}`} onClose={onClose} width={560} testId="jp-dialog">
      <div className="jp-dialog">
        <p className="muted small" style={{ margin: 0 }}>
          Mostra um campo do objeto como coluna, como no <code>kubectl -o custom-columns</code>. Valores de Secret aparecem sempre mascarados.
        </p>
        <div className="row">
          <label className="field grow">Expressão
            <input className="input" autoFocus data-testid="jp-expr" placeholder=".spec.replicas" value={expr} spellCheck={false}
              onChange={(e) => (setExpr(e.target.value), setErr(''))} onKeyDown={(e) => e.key === 'Enter' && add()} />
          </label>
          <label className="field">Nome
            <input className="input" data-testid="jp-label" placeholder="Réplicas" value={label} onChange={(e) => setLabel(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && add()} />
          </label>
        </div>
        <div className="jp-examples" aria-label="Exemplos">
          {(EXAMPLES[kind] ?? EXAMPLES.default).map((x) => <button key={x} onClick={() => (setExpr(x), setErr(''))}>{x}</button>)}
        </div>
        {err && <div className="filter-error" role="alert" data-testid="jp-error">{err}</div>}
        {cols.length > 0 && (
          <div className="jp-list" data-testid="jp-list">
            {cols.map((c) => {
              const e = errors?.[`jp:${c.expr}`]
              return (
                <div key={c.expr} className="jp-item">
                  <b>{c.label}</b>
                  <code title={c.expr}>{c.expr}</code>
                  {e && <span className="jp-err" data-testid="jp-col-error">{e}</span>}
                  <button className="icon-btn" title="Remover a coluna" aria-label={`Remover a coluna ${c.label}`} onClick={() => save(cols.filter((x) => x !== c))}><Trash2 size={14} /></button>
                </div>
              )
            })}
          </div>
        )}
      </div>
      <div className="modal-actions">
        <span className="spacer" />
        <button onClick={onClose}>Fechar</button>
        <button className="primary" onClick={add} disabled={!expr.trim()} data-testid="jp-add"><Plus size={14} /> Adicionar coluna</button>
      </div>
    </Modal>
  )
}
