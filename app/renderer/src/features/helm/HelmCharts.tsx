import { useEffect, useState } from 'react'
import { Package } from 'lucide-react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type ChartHit, type ChartInfo } from '../../api'
import { clusterName, useApp } from '../../store'
import { YamlEditor } from '../../components/Monaco'
import { ErrorBanner, Tabs, useAsync } from '../common'
import { pressable } from '../../components/a11y'
import { Review, type ReviewState } from './Review'
import { openRelease } from './HelmReleases'

function Repos({ onChanged }: { onChanged: () => void }) {
  const toast = useApp((s) => s.toast)
  const { data, error, reload } = useAsync(() => api.helmRepos(), [])
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true)
    try {
      await fn()
      toast('ok', ok)
      reload()
      onChanged()
    } catch (e) {
      toast('error', (e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <details className="card repos" open={!data || !data.length || !!error}>
      <summary>Repositórios <span className="muted small">{data ? `${data.length} configurado${data.length === 1 ? '' : 's'} · clique para gerenciar` : ''}</span></summary>
      <ErrorBanner error={error} />
      <table className="table">
        <tbody>
          {data?.map((r) => (
            <tr key={r.name}>
              <td><b>{r.name}</b></td>
              <td className="mono small">{r.url}</td>
              <td>{r.indexed ? `${r.charts} charts` : <span className="s-warn">sem índice</span>}</td>
              <td><button disabled={busy} onClick={() => run(() => api.helmRemoveRepo(r.name), `${r.name} removido`)}>Remover</button></td>
            </tr>
          ))}
          {data && !data.length && <tr><td className="muted">Nenhum repositório configurado no Helm.</td></tr>}
        </tbody>
      </table>
      <div className="row">
        <input placeholder="nome" value={name} onChange={(e) => setName(e.target.value)} style={{ width: 140 }} />
        <input placeholder="https://charts.exemplo.com" value={url} onChange={(e) => setUrl(e.target.value)} style={{ flex: 1 }} />
        <button disabled={busy || !name || !url} onClick={() => run(() => api.helmAddRepo(name, url).then(() => (setName(''), setUrl(''))), `${name} adicionado`)}>Adicionar</button>
        <button disabled={busy} onClick={() => run(async () => {
          const r = await api.helmUpdateRepos()
          const failed = Object.entries(r.errors ?? {})
          if (failed.length) throw new Error(failed.map(([k, v]) => `${k}: ${v}`).join('; '))
        }, 'Índices atualizados')}>Atualizar todos</button>
      </div>
      <p className="small muted">Usa a mesma configuração do Helm CLI (repositories.yaml).</p>
    </details>
  )
}

type DetailTab = 'readme' | 'values' | 'install'

function ChartDetail({ hit, clusterId: initialCluster }: { hit: ChartHit; clusterId?: string }) {
  const toast = useApp((s) => s.toast)
  const connected = useApp((s) => s.connected)
  const [version, setVersion] = useState(hit.version)
  const [info, setInfo] = useState<ChartInfo>()
  const [err, setErr] = useState<string>()
  const [tab, setTab] = useState<DetailTab>('readme')
  const [clusterId, setClusterId] = useState(initialCluster && connected[initialCluster] ? initialCluster : Object.keys(connected)[0] ?? '')
  const selectedNs = useApp((s) => s.namespace[clusterId] ?? '')
  const [ns, setNs] = useState(selectedNs || 'default')
  const [release, setRelease] = useState(hit.name)
  const [values, setValues] = useState('')
  const [createNs, setCreateNs] = useState(false)
  const [review, setReview] = useState<ReviewState>()
  const readOnly = connected[clusterId]?.readOnly ?? true

  useEffect(() => {
    setInfo(undefined)
    setErr(undefined)
    api.helmShow(hit.ref, version).then(setInfo, (e) => setErr(e.message))
  }, [hit.ref, version])

  const preview = async () => {
    const req = { namespace: ns, name: release, chart: hit.ref, version, values, createNamespace: createNs }
    try {
      const res = await api.helmInstall(clusterId, { ...req, dryRun: true })
      setReview({
        title: `Instalar ${hit.ref} ${version}`,
        subtitle: `${release} em ${ns} · ${clusterName(clusterId)}`,
        previous: '',
        next: res.manifest,
        confirmLabel: 'Instalar',
        onConfirm: async () => {
          await api.helmInstall(clusterId, { ...req, dryRun: false }).then(() => {
            toast('ok', `${release} instalado em ${ns}`)
            openRelease(clusterId, ns, release)
          }, (e) => toast('error', `Instalação falhou: ${e.message}`))
        },
      })
    } catch (e) {
      toast('error', `Dry-run falhou: ${(e as Error).message}`)
    }
  }

  if (review) return <Review r={review} onClose={() => setReview(undefined)} />
  return (
    <div className="chart-detail">
      <div className="toolbar">
        <b>{hit.ref}</b>
        <select value={version} onChange={(e) => setVersion(e.target.value)}>
          {(hit.versions ?? [hit.version]).map((v) => <option key={v}>{v}</option>)}
        </select>
        <span className="muted">{info?.description ?? hit.description}</span>
      </div>
      <Tabs<DetailTab> tabs={[['readme', 'README'], ['values', 'Values padrão'], ['install', 'Instalar']]} value={tab} onChange={setTab} />
      <ErrorBanner error={err} />
      {!info && !err && <div className="pad"><span className="spinner" /></div>}
      {info && tab === 'readme' && <pre className="readme">{info.readme || 'Sem README.'}</pre>}
      {info && tab === 'values' && <YamlEditor value={info.values} readOnly />}
      {info && tab === 'install' && (
        <div className="install-form">
          <div className="row">
            <label className="field">Cluster
              <select value={clusterId} onChange={(e) => setClusterId(e.target.value)}>
                {Object.keys(connected).map((id) => <option key={id} value={id}>{clusterName(id)}{connected[id].readOnly ? ' (somente leitura)' : ''}</option>)}
              </select>
            </label>
            <label className="field">Namespace<input value={ns} onChange={(e) => setNs(e.target.value)} /></label>
            <label className="field">Nome do release<input value={release} onChange={(e) => setRelease(e.target.value)} /></label>
            <label className="check"><input type="checkbox" checked={createNs} onChange={(e) => setCreateNs(e.target.checked)} /> criar namespace</label>
            <button onClick={() => setValues(info.values)}>Copiar values padrão</button>
            <button className="primary" disabled={!clusterId || readOnly || !ns || !release} title={readOnly ? 'Cluster em somente leitura' : ''} onClick={preview}>Revisar (dry-run)</button>
          </div>
          {!Object.keys(connected).length && <div className="banner">Conecte um cluster para instalar.</div>}
          <p className="small muted">Values do usuário (sobrescrevem os padrões do chart):</p>
          <YamlEditor value={values} onChange={setValues} />
        </div>
      )}
    </div>
  )
}

export function HelmChartsPanel({ params }: IDockviewPanelProps<{ clusterId?: string }>) {
  const [q, setQ] = useState('')
  const [rev, setRev] = useState(0)
  const { data: hits, error, loading } = useAsync(() => api.helmSearch(q), [q, rev])
  const [sel, setSel] = useState<ChartHit>()
  return (
    <div className="split">
      <div className="split-left">
        <Repos onChanged={() => setRev((r) => r + 1)} />
        <div className="toolbar">
          <input className="search" placeholder="Buscar charts…" value={q} onChange={(e) => setQ(e.target.value)} data-testid="chart-search" />
          {loading && <span className="spinner" />}
        </div>
        <ErrorBanner error={error} />
        <div className="list">
          {hits?.map((h) => (
            <div key={h.ref} className={`list-item ${sel?.ref === h.ref ? 'selected' : ''}`} {...pressable(() => setSel(h))} aria-pressed={sel?.ref === h.ref} data-testid={`chart-${h.ref}`}>
              <div><b>{h.ref}</b> <span className="muted small">{h.version}</span>{h.deprecated && <span className="badge badge-ro">deprecated</span>}</div>
              <div className="small muted ellipsis">{h.description}</div>
            </div>
          ))}
          {hits && !hits.length && <div className="pad muted small">Nenhum chart. Adicione um repositório acima.</div>}
        </div>
      </div>
      <div className="split-right">{sel ? <ChartDetail key={sel.ref} hit={sel} clusterId={params.clusterId} /> : <div className="empty"><Package size={22} /><span>Selecione um chart à esquerda para ver detalhes.</span></div>}</div>
    </div>
  )
}
