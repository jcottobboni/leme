import { useEffect, useState } from 'react'
import { api, type Forward } from '../api'
import { age } from '../format'
import { useApp } from '../store'

export function ForwardsPanel() {
  const [list, setList] = useState<Forward[]>([])
  const toast = useApp((s) => s.toast)
  const load = () => api.forwards().then(setList).catch(() => {})
  useEffect(() => {
    load()
    const t = setInterval(load, 3000)
    return () => clearInterval(t)
  }, [])
  return (
    <div className="pad-panel">
      <table className="table">
        <thead><tr><th>URL local</th><th>Destino</th><th>Pod</th><th>Contexto</th><th>Há</th><th /></tr></thead>
        <tbody>
          {list.map((f) => (
            <tr key={f.id}>
              <td>
                <a href={f.url} target="_blank" rel="noreferrer">{f.url}</a>
                <button className="link" onClick={() => navigator.clipboard.writeText(f.url).then(() => toast('ok', 'URL copiada'))}>copiar</button>
              </td>
              <td>{f.kind}/{f.namespace}/{f.name}:{f.remotePort}</td>
              <td>{f.pod}</td>
              <td>{f.context}</td>
              <td>{age(f.started)}</td>
              <td>
                {f.error && <span className="s-error" title={f.error}>erro </span>}
                <button onClick={() => api.stopForward(f.id).then(load)}>Encerrar</button>
              </td>
            </tr>
          ))}
          {!list.length && <tr><td colSpan={6} className="muted">Nenhum port-forward ativo. Abra um Pod ou Service e clique em “Port-forward”.</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
