import { X } from 'lucide-react'
import { withoutTerm, type Filter, type TermKind } from './filter'
import './search.css'

const LABEL: Record<TermKind, string> = { alias: 'tipo', label: 'label', ns: 'ns', text: 'texto', not: 'sem', regex: 'regex', notregex: 'sem regex' }

/** Active filter terms as chips (each one removable) plus inline errors such as an invalid regex. */
export function FilterChips({ filter, onChange, testid = 'filter-chips', hideText = true }: { filter: Filter; onChange: (q: string) => void; testid?: string; hideText?: boolean }) {
  const shown = filter.terms.map((t, i) => ({ t, i })).filter(({ t }) => !(hideText && t.kind === 'text' && !t.error))
  if (!shown.length) return null
  return (
    <div className="filter-chips" data-testid={testid} role="list" aria-label="Filtros ativos">
      {shown.map(({ t, i }) => (
        <span key={i} role="listitem" className={`fchip fchip-${t.kind} ${t.error ? 'bad' : ''}`} title={t.error ?? t.raw} data-testid={`fchip-${t.kind}`}>
          <span className="fchip-k">{LABEL[t.kind]}</span>
          <span className="fchip-v">{t.kind === 'regex' || t.kind === 'notregex' ? `/${t.value}/` : t.value || '…'}</span>
          <button className="fchip-x" aria-label={`Remover o filtro ${t.raw}`} onClick={() => onChange(withoutTerm(filter, i))}><X size={11} /></button>
        </span>
      ))}
      {filter.errors.map((e) => <span key={e} className="filter-error" role="alert" data-testid="filter-error">{e}</span>)}
    </div>
  )
}

/** One-line syntax reminder (palette footer and the grid search tooltip). */
export const FILTER_HELP = ':tipo [ns] navega · -l app=web label · ns:prod namespace · !texto exclui · /regex/'
