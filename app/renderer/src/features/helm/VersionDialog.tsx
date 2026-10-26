import { useEffect, useState } from 'react'
import { api, type ChartHit } from '../../api'
import { Modal } from '../../components/Dialogs'

/** Picks a chart reference and version for an upgrade, from the local repository indexes or a free reference. */
export function VersionDialog({ chart, current, onConfirm, onCancel }: { chart: string; current: string; onConfirm: (ref: string, version: string) => void; onCancel: () => void }) {
  const [hits, setHits] = useState<ChartHit[]>()
  const [ref, setRef] = useState('')
  const [version, setVersion] = useState('')
  const [err, setErr] = useState<string>()
  useEffect(() => {
    api.helmSearch(chart).then((h) => {
      const exact = h.filter((x) => x.name === chart)
      setHits(exact)
      if (exact[0]) {
        setRef(exact[0].ref)
        setVersion(exact[0].version)
      }
    }, (e) => setErr(e.message))
  }, [chart])
  const versions = hits?.find((h) => h.ref === ref)?.versions ?? []
  return (
    <Modal title={`Mudar versão do chart ${chart}`} onClose={onCancel}>
      <p className="small muted">Versão atual: {current}. Os values atuais são mantidos; o próximo passo mostra o diff do dry-run.</p>
      {err && <div className="banner error">{err}</div>}
      {hits && !hits.length && <p className="small">Nenhum repositório local tem o chart <b>{chart}</b>. Adicione o repositório em “Charts” ou informe uma referência (repo/chart, oci://…).</p>}
      <label className="field">
        Referência
        <input className="input" list="chart-refs" value={ref} onChange={(e) => setRef(e.target.value)} placeholder="repo/chart ou oci://registro/chart" />
        <datalist id="chart-refs">{hits?.map((h) => <option key={h.ref} value={h.ref} />)}</datalist>
      </label>
      <label className="field">
        Versão
        {versions.length ? (
          <select value={version} onChange={(e) => setVersion(e.target.value)}>
            {versions.map((v) => <option key={v} value={v}>{v}{v === current ? ' (atual)' : ''}</option>)}
          </select>
        ) : (
          <input className="input" value={version} onChange={(e) => setVersion(e.target.value)} placeholder="ex.: 1.2.3" />
        )}
      </label>
      <div className="modal-actions">
        <button onClick={onCancel}>Cancelar</button>
        <button className="primary" disabled={!ref || !version} onClick={() => onConfirm(ref, version)}>Revisar (dry-run)</button>
      </div>
    </Modal>
  )
}
