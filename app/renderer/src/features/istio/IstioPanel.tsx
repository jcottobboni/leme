import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { Network, Play, ShieldCheck, Waypoints } from 'lucide-react'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { openObject } from '../../components/ObjectPanel'
import { openTopology } from '../topology/Topology'
import { ClusterGate, ErrorBanner, PanelHeader, Partial, RefreshButton, Tabs, useAsync } from '../common'
import { istioApi, type AnalyzeResult, type Destination, type IstioReport, type IstioRoute, type MTLSMode, type ServiceRef, type VirtualService } from './api'
import './istio.css'

type Tab = 'mtls' | 'routes' | 'rules' | 'gateways' | 'analyze'

export function openIstio(clusterId: string, opts?: OpenOptions) {
  openPanel(`istio:${clusterId}`, 'istio', 'Istio', { clusterId }, opts)
}

const MODE_LABEL: Record<MTLSMode, string> = { STRICT: 'STRICT', PERMISSIVE: 'PERMISSIVE', DISABLE: 'DISABLE', UNSET: 'não definido' }
const SOURCE_LABEL = { namespace: 'política do namespace', mesh: 'política da malha', default: 'padrão do Istio (aceita texto puro e mTLS)' }

function Mode({ mode }: { mode: MTLSMode }) {
  return <span className={`ist-mode ist-${mode.toLowerCase()}`}>{MODE_LABEL[mode]}</span>
}

function injectionLabel(v: string) {
  if (v === 'enabled') return 'sidecar habilitado'
  if (v === 'disabled') return 'sidecar desabilitado'
  if (v === 'ambient') return 'ambient'
  if (v.startsWith('rev:')) return `revisão ${v.slice(4)}`
  return '—'
}

function labels(l: Record<string, string>) {
  const e = Object.entries(l)
  return e.length ? e.map(([k, v]) => `${k}=${v}`).join(', ') : 'todos os pods'
}

function ServiceLink({ clusterId, svc, host }: { clusterId: string; svc: ServiceRef; host: string }) {
  if (svc.found && svc.name) {
    return (
      <button className="link" onClick={() => openObject(clusterId, '', 'Service', svc.name!, svc.namespace)} title={`Abrir o Service ${svc.namespace}/${svc.name}`}>
        {host}
      </button>
    )
  }
  return <span title={svc.name ? `Service ${svc.namespace}/${svc.name} não encontrado` : 'host externo ou curinga'}>{host}{svc.name && <span className="ist-missing"> (Service não encontrado)</span>}</span>
}

function destKey(d: Destination) {
  return d.subset || d.host
}

function WeightBar({ dests }: { dests: Destination[] }) {
  const total = dests.reduce((s, d) => s + d.weight, 0)
  return (
    <div className="ist-bar" role="img" aria-label={dests.map((d) => `${destKey(d)} ${d.weight}%`).join(', ')}>
      {dests.map((d, i) => (
        <div key={i} className={`ist-seg ist-seg-${i % 5}`} style={{ flexGrow: total ? d.weight : 1 }} title={`${d.host}${d.subset ? ` · ${d.subset}` : ''}: ${d.weight}%`}>
          {d.weight >= 12 && <span>{destKey(d)} {d.weight}%</span>}
        </div>
      ))}
    </div>
  )
}

function RouteView({ clusterId, vs, proto, r, i }: { clusterId: string; vs: VirtualService; proto: string; r: IstioRoute; i: number }) {
  const total = r.destinations.reduce((s, d) => s + d.weight, 0)
  return (
    <div className="ist-route" data-testid={`ist-route-${vs.namespace}-${vs.name}-${proto}-${i}`}>
      <div className="ist-route-head">
        <span className="chip">{proto.toUpperCase()}</span>
        {r.name && <b>{r.name}</b>}
        <span className="muted small">{r.match.length ? r.match.join('  ou  ') : 'qualquer requisição'}</span>
        {r.notes?.map((n) => <span key={n} className="chip">{n}</span>)}
      </div>
      {r.destinations.length > 0 ? (
        <>
          <WeightBar dests={r.destinations} />
          <table className="table ist-dests">
            <tbody>
              {r.destinations.map((d, j) => (
                <tr key={j} data-testid={`ist-dest-${destKey(d)}`}>
                  <td><span className={`ist-dot ist-seg-${j % 5}`} /> <ServiceLink clusterId={clusterId} svc={d.service} host={d.host} /></td>
                  <td>{d.subset ? <span className="chip">subset {d.subset}</span> : <span className="muted">sem subset</span>}</td>
                  <td>{d.port ? `porta ${d.port}` : ''}</td>
                  <td className="ist-weight">{d.weight}%</td>
                </tr>
              ))}
            </tbody>
          </table>
          {r.destinations.length > 1 && total !== 100 && <div className="banner warn">Os pesos somam {total}%, não 100%: o Istio rejeita esta rota.</div>}
        </>
      ) : (
        <div className="muted small">Sem destinos (redirect ou resposta direta).</div>
      )}
    </div>
  )
}

function Routes({ clusterId, rep }: { clusterId: string; rep: IstioReport }) {
  if (!rep.virtualServices.length) return <div className="empty"><Waypoints size={22} /><span>Nenhum VirtualService neste escopo.</span></div>
  return (
    <div className="overview">
      {rep.virtualServices.map((vs) => (
        <section className="card" key={`${vs.namespace}/${vs.name}`} data-testid={`ist-vs-${vs.namespace}-${vs.name}`}>
          <h4 className="ist-card-title">
            {vs.namespace}/{vs.name}
            <span className="spacer" />
            <button className="link" onClick={() => openObject(clusterId, 'networking.istio.io', 'VirtualService', vs.name, vs.namespace)}>YAML</button>
            <button className="link" onClick={() => openTopology(clusterId, vs.namespace)} title="Abrir a topologia do namespace">Topologia</button>
          </h4>
          <div className="ist-meta">
            <span>Hosts: {vs.hosts.map((h) => <span key={h} className="chip">{h}</span>)}</span>
            <span>Gateways: {vs.gateways.length ? vs.gateways.map((g) => <span key={g} className="chip">{g}</span>) : <span className="muted">mesh (sidecars)</span>}</span>
          </div>
          {(['http', 'tcp', 'tls'] as const).flatMap((p) => vs[p].map((r, i) => <RouteView key={`${p}${i}`} clusterId={clusterId} vs={vs} proto={p} r={r} i={i} />))}
        </section>
      ))}
    </div>
  )
}

function MTLS({ rep, clusterId }: { rep: IstioReport; clusterId: string }) {
  return (
    <div className="pad-panel">
      <p className="small muted">
        Modo efetivo por namespace: a PeerAuthentication sem selector do namespace prevalece sobre a da malha (namespace raiz <b>{rep.rootNamespace}</b>); políticas com selector viram exceções por workload ou porta.
      </p>
      <table className="table" data-testid="ist-mtls">
        <thead><tr><th>Namespace</th><th>mTLS</th><th>Origem</th><th>Injeção</th><th>Exceções</th></tr></thead>
        <tbody>
          {rep.namespaces.map((n) => (
            <tr key={n.name} data-testid={`ist-ns-${n.name}`}>
              <td><button className="link" onClick={() => openTopology(clusterId, n.name)} title="Abrir a topologia do namespace">{n.name}</button></td>
              <td><Mode mode={n.mtls} /></td>
              <td className="muted">{SOURCE_LABEL[n.source]}{n.policy && <span className="small"> · {n.policy}</span>}</td>
              <td>{injectionLabel(n.injection)}</td>
              <td className="ist-wrap">
                {n.exceptions.length === 0 && <span className="muted">—</span>}
                {n.exceptions.map((e) => (
                  <div key={e.policy} className="small">
                    <b>{e.policy}</b> ({labels(e.selector)}): <Mode mode={e.effective} />
                    {e.ports?.map((p) => <span key={p.port}> · porta {p.port}: <Mode mode={p.effective} /></span>)}
                  </div>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Rules({ clusterId, rep }: { clusterId: string; rep: IstioReport }) {
  if (!rep.destinationRules.length) return <div className="empty"><Network size={22} /><span>Nenhuma DestinationRule neste escopo.</span></div>
  return (
    <div className="pad-panel">
      <table className="table" data-testid="ist-rules">
        <thead><tr><th>DestinationRule</th><th>Host</th><th>Subsets</th><th>TLS</th></tr></thead>
        <tbody>
          {rep.destinationRules.map((d) => (
            <tr key={`${d.namespace}/${d.name}`} data-testid={`ist-dr-${d.namespace}-${d.name}`}>
              <td><button className="link" onClick={() => openObject(clusterId, 'networking.istio.io', 'DestinationRule', d.name, d.namespace)}>{d.namespace}/{d.name}</button></td>
              <td><ServiceLink clusterId={clusterId} svc={d.service} host={d.host} /></td>
              <td className="ist-wrap">{d.subsets.map((s) => <span key={s.name} className="chip" title={labels(s.labels)}><b>{s.name}</b> {labels(s.labels)}{s.tlsMode ? ` · ${s.tlsMode}` : ''}</span>)}</td>
              <td>{d.tlsMode || <span className="muted">—</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Gateways({ clusterId, rep }: { clusterId: string; rep: IstioReport }) {
  if (!rep.gateways.length) return <div className="empty"><Network size={22} /><span>Nenhum Gateway do Istio neste escopo.</span></div>
  return (
    <div className="pad-panel">
      <table className="table" data-testid="ist-gateways">
        <thead><tr><th>Gateway</th><th>Selector</th><th>Servidores</th></tr></thead>
        <tbody>
          {rep.gateways.map((g) => (
            <tr key={`${g.namespace}/${g.name}`} data-testid={`ist-gw-${g.namespace}-${g.name}`}>
              <td><button className="link" onClick={() => openObject(clusterId, 'networking.istio.io', 'Gateway', g.name, g.namespace)}>{g.namespace}/{g.name}</button></td>
              <td>{labels(g.selector)}</td>
              <td className="ist-wrap">
                {g.servers.map((s, i) => (
                  <div key={i} className="small">{s.protocol} :{s.port}{s.tlsMode ? ` · TLS ${s.tlsMode}` : ''} → {s.hosts.join(', ')}</div>
                ))}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Analyze({ clusterId, ns }: { clusterId: string; ns: string }) {
  const tool = useAsync(() => istioApi.tool(), [])
  const [res, setRes] = useState<AnalyzeResult>()
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  if (tool.data && !tool.data.installed) {
    return (
      <div className="pad-panel" data-testid="ist-install">
        <h4>istioctl não encontrado</h4>
        <p>O Analyze roda o <code>istioctl analyze</code> localmente, sobre uma exportação dos objetos (sem Secrets nem ConfigMaps, valores de env mascarados), sem acesso ao cluster.</p>
        <ol className="small">
          <li>Baixe o istioctl da release oficial do Istio (github.com/istio/istio/releases) e confira o SHA-256 publicado.</li>
          <li>Coloque o binário no PATH ou em <code>~/.local/bin</code>.</li>
          <li>Volte a esta aba e clique em Recarregar.</li>
        </ol>
        <p className="small muted">{tool.data.hint}</p>
      </div>
    )
  }
  const run = () => {
    setBusy(true)
    setErr(undefined)
    istioApi.analyze(clusterId, ns).then(setRes, (e) => setErr(e.message)).finally(() => setBusy(false))
  }
  return (
    <div className="pad-panel">
      <div className="row">
        <button className="primary" onClick={run} disabled={busy || !tool.data} data-testid="ist-analyze-run">
          <Play size={14} /> {busy ? 'Analisando…' : `Analisar ${ns || 'todos os namespaces'}`}
        </button>
        {tool.data?.path && <span className="small muted">istioctl: {tool.data.path} · offline (--use-kube=false)</span>}
      </div>
      <ErrorBanner error={err ?? tool.error} />
      {res && !res.installed && <div className="banner warn">{res.hint}</div>}
      <Partial errors={res?.errors} />
      {res?.messages && (
        res.messages.length === 0 ? (
          <div className="empty" data-testid="ist-analyze-empty"><ShieldCheck size={22} /><span>Nenhum problema encontrado pelo istioctl analyze.</span></div>
        ) : (
          <table className="table" data-testid="ist-analyze-messages">
            <thead><tr><th>Nível</th><th>Código</th><th>Recurso</th><th>Mensagem</th></tr></thead>
            <tbody>
              {res.messages.map((m, i) => (
                <tr key={i}>
                  <td><span className={`ist-level ist-level-${m.level.toLowerCase()}`}>{m.level}</span></td>
                  <td>{m.documentationUrl ? <span title={m.documentationUrl}>{m.code}</span> : m.code}</td>
                  <td>{m.resource}</td>
                  <td className="ist-wrap">{m.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )
      )}
    </div>
  )
}

function NotInstalled() {
  return (
    <div className="empty" data-testid="istio-not-installed">
      <Waypoints size={22} />
      <span><b>Istio não detectado neste cluster.</b></span>
      <span className="ist-explain">
        O painel lê PeerAuthentication (security.istio.io) e VirtualService, DestinationRule e Gateway (networking.istio.io), mas a API do cluster não tem esses CRDs.
        Instale o Istio (ou só os CRDs dele) e reconecte o cluster para o Leme redescobrir as APIs.
      </span>
    </div>
  )
}

function Istio({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const [tab, setTab] = useState<Tab>('mtls')
  const [rev, setRev] = useState(0)
  const { data, error, loading } = useAsync(() => istioApi.report(clusterId, ns), [clusterId, ns, rev])
  const strict = data?.namespaces.filter((n) => n.mtls === 'STRICT').length ?? 0
  return (
    <div className="object-panel" data-testid="istio-panel">
      <PanelHeader icon={<Waypoints size={16} />} title="Istio">
        <NamespaceSelect clusterId={clusterId} />
        {data?.installed && (
          <span className="muted" data-testid="ist-summary">
            {strict} de {data.namespaces.length} namespaces em STRICT · {data.virtualServices.length} VirtualServices · {data.destinationRules.length} DestinationRules · {data.gateways.length} Gateways
          </span>
        )}
        <span className="spacer" />
        <RefreshButton onClick={() => setRev((r) => r + 1)} loading={loading} />
      </PanelHeader>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      {data && !data.installed && <NotInstalled />}
      {data?.installed && (
        <>
          <Tabs<Tab> tabs={[['mtls', 'mTLS'], ['routes', 'Rotas'], ['rules', 'DestinationRules'], ['gateways', 'Gateways'], ['analyze', 'Analyze']]} value={tab} onChange={setTab} />
          <div className="obj-body">
            {tab === 'mtls' && <MTLS rep={data} clusterId={clusterId} />}
            {tab === 'routes' && <Routes clusterId={clusterId} rep={data} />}
            {tab === 'rules' && <Rules clusterId={clusterId} rep={data} />}
            {tab === 'gateways' && <Gateways clusterId={clusterId} rep={data} />}
            {tab === 'analyze' && <Analyze key={rev} clusterId={clusterId} ns={ns} />}
          </div>
        </>
      )}
    </div>
  )
}

/** Istio mTLS, routes and analyzer — M5, feature istio. */
export function IstioPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Istio clusterId={params.clusterId} />
    </ClusterGate>
  )
}
