import { useState } from 'react'
import { api3, type ImageReport } from '../../api'
import { ErrorBanner, useAsync } from '../common'
import { Counts, FindingsTable, ScoreRing, Sev } from './common'
import { age } from '../../format'

/** "Segurança" tab of a workload: rule findings plus image vulnerabilities. */
export function ObjectSecurity({ clusterId, group, kind, namespace, name }: { clusterId: string; group: string; kind: string; namespace: string; name: string }) {
  const { data, error } = useAsync(() => api3.securityObject(clusterId, kind, namespace, name, group), [clusterId, group, kind, namespace, name])
  if (error) return <ErrorBanner error={error} />
  if (!data) return <div className="pad"><span className="spinner" /></div>
  return (
    <div className="overview">
      <div className="sec-head">
        <ScoreRing score={data.score} size={70} />
        <div>
          <div className="sec-title">Achados das regras do Leme</div>
          <Counts counts={data.findings.reduce((m, f) => ((m[f.severity] = (m[f.severity] ?? 0) + 1), m), {} as Record<string, number>)} />
        </div>
      </div>
      <FindingsTable clusterId={clusterId} findings={data.findings} showObject={false} />
      <Images clusterId={clusterId} group={group} kind={kind} namespace={namespace} name={name} />
    </div>
  )
}

function Images({ clusterId, group, kind, namespace, name }: { clusterId: string; group: string; kind: string; namespace: string; name: string }) {
  const { data, error } = useAsync(() => api3.securityImages(clusterId, group, kind, namespace, name), [clusterId, group, kind, namespace, name])
  const [reports, setReports] = useState<ImageReport[]>()
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const scan = async (refresh: boolean) => {
    if (!data) return
    setBusy(true)
    setErr(undefined)
    try {
      setReports(await api3.scanImages(clusterId, data.images.map((i) => i.image), refresh))
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const shown = reports ?? (data?.operator ? data.reports : undefined)
  return (
    <section className="card">
      <h4>Vulnerabilidades das imagens</h4>
      <ErrorBanner error={error ?? err} />
      {data && (
        <div className="row img-row">
          <span className="mono">{data.images.map((i) => i.image).join(', ')}</span>
          {data.operator && <span className="chip">relatórios do trivy-operator</span>}
          <button disabled={busy} onClick={() => scan(false)} data-testid="trivy-scan">{busy ? 'Escaneando…' : 'Escanear com Trivy'}</button>
          {reports && <button disabled={busy} onClick={() => scan(true)}>Escanear de novo</button>}
          {busy && <span className="muted small"><span className="spinner" /> o Trivy baixa a imagem e o banco de CVEs; pode levar alguns minutos na primeira vez</span>}
        </div>
      )}
      {shown?.map((r) => <ImageResult key={r.image + (r.container ?? '')} r={r} />)}
    </section>
  )
}

function ImageResult({ r }: { r: ImageReport }) {
  const [all, setAll] = useState(false)
  const vulns = all ? r.vulns : r.vulns.filter((v) => v.severity === 'critical' || v.severity === 'high')
  return (
    <div className="image-report" data-testid="image-report">
      <div className="row">
        <b className="mono">{r.image}</b>
        {r.os && <span className="muted small">{r.os}</span>}
        <span className="muted small">{r.source} · {age(r.scanned)}</span>
        <Counts counts={r.counts} />
      </div>
      {r.error && <div className="banner error">{r.error}</div>}
      {r.vulns.length > 0 && (
        <>
          <table className="table small">
            <thead><tr><th>Severidade</th><th>CVE</th><th>Pacote</th><th>Instalado</th><th>Corrigido em</th><th>Título</th></tr></thead>
            <tbody>
              {vulns.slice(0, 300).map((v) => (
                <tr key={v.id + v.package}>
                  <td><Sev s={v.severity} /></td>
                  <td className="mono">{v.id}</td>
                  <td>{v.package}</td>
                  <td className="mono">{v.installed}</td>
                  <td className="mono">{v.fixed || <span className="muted">sem correção</span>}</td>
                  <td className="wrap">{v.title}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <button className="link" onClick={() => setAll(!all)}>{all ? 'Só críticas e altas' : `Mostrar todas (${r.vulns.length})`}</button>
        </>
      )}
    </div>
  )
}
