import { useEffect, useState } from 'react'
import { api, ApiError, type PromChart, type PromConfig, type PromScope } from '../../api'
import { useApp } from '../../store'
import { Modal } from '../../components/Dialogs'
import { MetricChart } from './Chart'
import { RefreshCw } from 'lucide-react'
import './charts.css'

const RANGES = ['15m', '1h', '6h', '24h', '7d']
const METRICS = ['cpu', 'memory', 'network', 'fs']

/** Prometheus history charts for a scope; offers configuration when Prometheus is not found. */
export function MetricsView({ clusterId, scope }: { clusterId: string; scope: PromScope | undefined }) {
  const [range, setRange] = useState('1h')
  const [charts, setCharts] = useState<(PromChart | null)[]>()
  const [errors, setErrors] = useState<string[]>([])
  const [source, setSource] = useState<PromConfig>()
  const [missing, setMissing] = useState(false)
  const [err, setErr] = useState<string>()
  const [settings, setSettings] = useState(false)
  const [rev, setRev] = useState(0)
  const [loading, setLoading] = useState(false)
  const [wide, setWide] = useState<Record<string, boolean>>({})
  const key = JSON.stringify(scope)

  useEffect(() => {
    if (!scope) return
    let stop = false
    const load = () => {
      setLoading(true)
      return api.promChart(clusterId, scope, METRICS, range).then(
        (r) => {
          if (stop) return
          setLoading(false)
          setCharts(r.charts)
          setErrors(r.errors)
          setSource(r.source)
          setMissing(false)
          setErr(undefined)
        },
        (e) => {
          if (stop) return
          setLoading(false)
          if (e instanceof ApiError && e.status === 404) setMissing(true)
          else setErr(e.message)
        },
      )
    }
    load()
    const t = setInterval(load, 30000)
    return () => {
      stop = true
      clearInterval(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, key, range, rev])

  if (!scope) return <div className="pad muted">Aguardando os pods do workload…</div>
  return (
    <div className="metrics">
      <div className="toolbar">
        {RANGES.map((r) => (
          <button key={r} className={r === range ? 'primary' : ''} onClick={() => setRange(r)}>{r}</button>
        ))}
        <button onClick={() => setRev((r) => r + 1)} title="Atualizar"><RefreshCw size={14} className={loading ? 'mc-spin' : ''} /> Atualizar</button>
        <span className="spacer" />
        {source && <span className="muted small">{source.mode === 'url' ? source.url : `${source.namespace}/${source.service}:${source.port}`}</span>}
        <button onClick={() => setSettings(true)}>Configurar Prometheus</button>
      </div>
      {missing && (
        <div className="pad">
          <p>Nenhum Prometheus encontrado neste cluster.</p>
          <button className="primary" onClick={() => setSettings(true)}>Configurar</button>
        </div>
      )}
      {err && <div className="banner error">{err}</div>}
      <div className="charts">
        {!charts && !missing && !err && METRICS.map((m) => (
          <div key={m} className="metric-chart mc-card" data-testid={`chart-skeleton-${m}`}>
            <div className="skeleton" style={{ width: 90, height: 14 }} />
            <div className="skeleton" style={{ width: '60%', height: 22, margin: '12px 0' }} />
            <div className="skeleton" style={{ height: 150 }} />
          </div>
        ))}
        {charts?.map((c, i) => (c
          ? <MetricChart key={c.metric + range} chart={c} refreshing={loading} onRefresh={() => setRev((r) => r + 1)} expanded={!!wide[c.metric]} onToggleExpand={() => setWide((w) => ({ ...w, [c.metric]: !w[c.metric] }))} />
          : <div key={i} className="metric-chart mc-card"><div className="banner error small">{METRICS[i]}: {errors[i]}</div></div>))}
      </div>
      {settings && <PromSettings clusterId={clusterId} onClose={(changed) => { setSettings(false); if (changed) setRev((r) => r + 1) }} />}
    </div>
  )
}

export function PromSettings({ clusterId, onClose }: { clusterId: string; onClose: (changed: boolean) => void }) {
  const toast = useApp((s) => s.toast)
  const [cfg, setCfg] = useState<PromConfig>({ mode: 'service' })
  const [cands, setCands] = useState<(PromConfig & { score: number })[]>([])
  const [saved, setSaved] = useState(false)
  const [test, setTest] = useState<string>()
  useEffect(() => {
    api.promConfig(clusterId, true).then((r) => {
      setCands(r.candidates)
      setSaved(r.saved)
      if (r.active) setCfg(r.active)
    }, (e) => setTest(e.message))
  }, [clusterId])
  const set = (p: Partial<PromConfig>) => setCfg((c) => ({ ...c, ...p }))
  return (
    <Modal title="Prometheus deste cluster" onClose={() => onClose(false)}>
      {cands.length > 0 && (
        <label className="field">Detectados
          <select onChange={(e) => e.target.value && setCfg(cands[Number(e.target.value)])} defaultValue="">
            <option value="">Escolha…</option>
            {cands.map((c, i) => <option key={i} value={i}>{c.namespace}/{c.service}:{c.port}</option>)}
          </select>
        </label>
      )}
      <div className="row">
        <label className="check"><input type="radio" checked={cfg.mode !== 'url'} onChange={() => set({ mode: 'service' })} /> Service (proxy do API server)</label>
        <label className="check"><input type="radio" checked={cfg.mode === 'url'} onChange={() => set({ mode: 'url' })} /> URL direta</label>
      </div>
      {cfg.mode !== 'url' ? (
        <div className="row">
          <label className="field">Namespace<input value={cfg.namespace ?? ''} onChange={(e) => set({ namespace: e.target.value })} /></label>
          <label className="field">Service<input value={cfg.service ?? ''} onChange={(e) => set({ service: e.target.value })} /></label>
          <label className="field">Porta<input value={cfg.port ?? ''} onChange={(e) => set({ port: e.target.value })} style={{ width: 80 }} /></label>
          <label className="field">Esquema
            <select value={cfg.scheme ?? 'http'} onChange={(e) => set({ scheme: e.target.value })}><option>http</option><option>https</option></select>
          </label>
          <label className="field">Prefixo<input value={cfg.path ?? ''} onChange={(e) => set({ path: e.target.value })} placeholder="/prometheus" style={{ width: 110 }} /></label>
        </div>
      ) : (
        <>
          <label className="field">URL (https)<input className="input" value={cfg.url ?? ''} onChange={(e) => set({ url: e.target.value })} placeholder="https://thanos-querier-openshift-monitoring.apps.cluster.example" /></label>
          <label className="check"><input type="checkbox" checked={!!cfg.sendToken} onChange={(e) => set({ sendToken: e.target.checked })} /> enviar o token do kubeconfig (ex.: OpenShift Thanos)</label>
        </>
      )}
      {test && <div className={`banner ${test.startsWith('OK') ? '' : 'error'}`}>{test}</div>}
      <div className="modal-actions">
        {saved && <button onClick={() => api.promSave(clusterId, { mode: '' }).then(() => (toast('ok', 'Configuração removida; voltando à detecção automática'), onClose(true)))}>Usar detecção automática</button>}
        <button onClick={() => api.promTest(clusterId, cfg).then((r) => setTest(r.ok ? `OK — ${r.targets} alvos com métricas` : r.error), (e) => setTest(e.message))}>Testar</button>
        <button onClick={() => onClose(false)}>Cancelar</button>
        <button className="primary" onClick={() => api.promSave(clusterId, cfg).then(() => (toast('ok', 'Prometheus salvo'), onClose(true)), (e) => setTest(e.message))}>Salvar</button>
      </div>
    </Modal>
  )
}
