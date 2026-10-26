import { useCallback, useEffect, useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, ApiError, type KubeEvent, type ObjectView, type Ref } from '../api'
import { findKind, findResource, openPanel, useApp, type OpenOptions } from '../store'
import { kindIcon } from '../features/shell/nav'
import { age, healthOfStatus } from '../format'
import { useWatch } from '../useWatch'
import { YamlDiff, YamlEditor } from './Monaco'
import { ConfirmDialog, PromptDialog } from './Dialogs'
import { TopologyView } from '../features/topology/Topology'
import { MetricsView } from '../features/metrics/Metrics'
import { Revisions } from '../features/workspace/Revisions'
import { ObjectSecurity } from '../features/security/ObjectSecurity'
import { api3 } from '../api'
import { ExternalLink, FilePen, Hammer, PanelTopOpen, Rocket, RotateCw, Scaling, ScrollText, SquareTerminal, Trash2, Waypoints, X, type LucideIcon } from 'lucide-react'

export interface ObjectParams {
  clusterId: string
  group: string
  version: string
  resource: string
  kind: string
  namespace?: string
  name: string
}

type Tab = 'overview' | 'yaml' | 'events' | 'topology' | 'metrics' | 'revisions' | 'security'

const METRIC_KINDS = ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'Node', 'DeploymentConfig']
const SECURITY_KINDS = ['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'ReplicationController', 'DeploymentConfig']

const SCALABLE = ['Deployment', 'StatefulSet', 'ReplicaSet', 'DeploymentConfig', 'ReplicationController']
const RESTARTABLE = ['Deployment', 'StatefulSet', 'DaemonSet']

export function openObject(clusterId: string, group: string, kind: string, name: string, namespace?: string, opts?: OpenOptions) {
  const r = findKind(clusterId, group, kind)
  if (!r) return
  openPanel(`obj:${clusterId}:${r.group}/${r.resource}:${namespace ?? ''}/${name}`, 'object', name, {
    clusterId, group: r.group, version: r.version, resource: r.resource, kind: r.kind, namespace: r.namespaced ? namespace : undefined, name,
  }, opts)
}

export function ObjectPanel({ params, api: panelApi }: IDockviewPanelProps<ObjectParams>) {
  useEffect(() => panelApi.setTitle(params.name), [panelApi, params.name])
  return <ObjectDetails params={params} />
}

/** Action button: icon + label in a tab, icon only (with tooltip) in the narrow drawer. */
function Act({ icon: Icon, label, title, compact, danger, disabled, onClick }: {
  icon: LucideIcon; label: string; title?: string; compact?: boolean; danger?: boolean; disabled?: boolean; onClick: () => void
}) {
  return (
    <button
      className={compact ? `icon-btn ${danger ? 'danger' : ''}` : danger ? 'danger' : ''}
      aria-label={label}
      title={title ?? label}
      disabled={disabled}
      onClick={onClick}
    >
      <Icon size={compact ? 16 : 14} />
      {!compact && label}
    </button>
  )
}

/** Object details: used as a dock tab and as the drawer next to a grid. */
export function ObjectDetails({ params, variant = 'tab', onClose, onPopOut }: { params: ObjectParams; variant?: 'tab' | 'drawer'; onClose?: () => void; onPopOut?: () => void }) {
  const { clusterId, kind, name, namespace } = params
  const compact = variant === 'drawer'
  const ref: Ref = useMemo(() => ({ group: params.group, version: params.version, resource: params.resource, namespace, name }), [params, namespace, name])
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const toast = useApp((s) => s.toast)
  const [tab, setTab] = useState<Tab>('overview')
  const [view, setView] = useState<ObjectView>()
  const [error, setError] = useState<string>()
  const [gone, setGone] = useState(false)
  const [draft, setDraft] = useState<string>()
  const [review, setReview] = useState<{ original: string; proposed: string }>()
  const [confirm, setConfirm] = useState<'delete' | 'restart' | 'apply' | 'scale' | 'rollout-latest' | 'start-build'>()
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    async (reveal = false) => {
      try {
        const v = await api.object(clusterId, ref, reveal)
        setView(v)
        setError(undefined)
        setGone(false)
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) setGone(true)
        else setError((e as Error).message)
      }
    },
    [clusterId, ref],
  )

  useEffect(() => {
    load()
  }, [load])
  useEffect(() => {
    if (draft !== undefined || tab !== 'overview' || view?.masked === false && kind === 'Secret') return
    const t = setInterval(() => load(), 5000)
    return () => clearInterval(t)
  }, [load, draft, tab, view?.masked, kind])

  const obj = view?.object
  const editing = draft !== undefined

  const startReview = async () => {
    if (draft === undefined || !view) return
    setBusy(true)
    try {
      const res = await api.apply(clusterId, draft, true)
      setReview({ original: view.yaml, proposed: res.yaml })
    } catch (e) {
      toast('error', `Dry-run falhou: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const doApply = async () => {
    if (draft === undefined) return
    setBusy(true)
    try {
      await api.apply(clusterId, draft, false)
      toast('ok', `${kind} ${name} atualizado`)
      setDraft(undefined)
      setReview(undefined)
      setConfirm(undefined)
      await load()
    } catch (e) {
      toast('error', `Apply falhou: ${(e as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const act = async (fn: () => Promise<unknown>, okMsg: string) => {
    setConfirm(undefined)
    try {
      await fn()
      toast('ok', okMsg)
      load()
    } catch (e) {
      toast('error', (e as Error).message)
    }
  }

  const replicas = (obj?.spec?.replicas as number | undefined) ?? 1

  const KindIcon = kindIcon(kind)

  return (
    <div className="object-panel">
      <div className="obj-header">
        <div className="obj-title">
          <span className="obj-icon"><KindIcon size={20} /></span>
          <div className="obj-names">
            <div className="obj-kind">
              <span className="kind-chip">{kind}</span>
              {obj?.metadata?.creationTimestamp && <span className="muted">criado há {age(obj.metadata.creationTimestamp)}</span>}
              {gone && <span className="badge badge-prod">EXCLUÍDO</span>}
            </div>
            <div className="obj-name">
              {namespace && <span className="muted">{namespace}:</span>}
              <b>{name}</b>
            </div>
          </div>
        </div>
        <div className="obj-actions">
          {kind === 'Pod' && (
            <>
              <Act compact={compact} icon={ScrollText} label="Logs" onClick={() => openPanel(`logs:${clusterId}:${namespace}/${name}`, 'logs', `logs ${name}`, { clusterId, namespace, pod: name })} />
              <Act compact={compact} icon={SquareTerminal} label="Shell" disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : 'Shell no container'}
                onClick={() => openPanel(`term:${clusterId}:${namespace}/${name}:${Date.now()}`, 'terminal', `sh ${name}`, { clusterId, namespace, pod: name })} />
            </>
          )}
          {(kind === 'Pod' || kind === 'Service') && <ForwardButton compact={compact} clusterId={clusterId} kind={kind} namespace={namespace!} name={name} obj={obj} disabled={readOnly} />}
          {SCALABLE.includes(kind) && <Act compact={compact} icon={Scaling} label="Scale" disabled={readOnly} onClick={() => setConfirm('scale')} />}
          {RESTARTABLE.includes(kind) && <Act compact={compact} icon={RotateCw} label="Restart" disabled={readOnly} onClick={() => setConfirm('restart')} />}
          {kind === 'DeploymentConfig' && <Act compact={compact} icon={Rocket} label="Rollout latest" disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : 'oc rollout latest'} onClick={() => setConfirm('rollout-latest')} />}
          {kind === 'BuildConfig' && <Act compact={compact} icon={Hammer} label="Iniciar build" disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : 'oc start-build'} onClick={() => setConfirm('start-build')} />}
          {kind === 'Build' && <Act compact={compact} icon={ScrollText} label="Logs" onClick={() => openPanel(`buildlog:${clusterId}:${namespace}/${name}`, 'buildLog', `build ${name}`, { clusterId, namespace, build: name })} />}
          {kind === 'Route' && obj && routeURL(obj) && <a className="btn" href={routeURL(obj)} target="_blank" rel="noreferrer"><ExternalLink size={14} />{!compact && 'Abrir URL'}</a>}
          <Act compact={compact} icon={FilePen} label="Editar YAML" disabled={readOnly || gone} onClick={() => { setTab('yaml'); if (view && !view.masked) setDraft(view.yaml) }} />
          <Act compact={compact} icon={Trash2} label="Excluir" danger disabled={readOnly || gone} onClick={() => setConfirm('delete')} />
          {(onPopOut || onClose) && <span className="sep" />}
          {onPopOut && <Act compact icon={PanelTopOpen} label="Abrir em aba" onClick={onPopOut} />}
          {onClose && <Act compact icon={X} label="Fechar painel (Esc)" onClick={onClose} />}
        </div>
      </div>
      <div className="tabs">
        {(['overview', 'yaml', 'events', ...(namespace ? ['topology'] : []), ...(METRIC_KINDS.includes(kind) ? ['metrics'] : []), ...(kind === 'Deployment' ? ['revisions'] : []), ...(SECURITY_KINDS.includes(kind) && namespace ? ['security'] : [])] as Tab[]).map((t) => (
          <button key={t} className={`tab ${tab === t ? 'active' : ''}`} onClick={() => setTab(t)} data-testid={`tab-${t}`}>
            {{ overview: 'Visão geral', yaml: 'YAML', events: 'Eventos', topology: 'Topologia', metrics: 'Métricas', revisions: 'Revisões', security: 'Segurança' }[t]}
          </button>
        ))}
      </div>
      {error && <div className="banner error">{error}</div>}
      <div className="obj-body">
        {tab === 'overview' && obj && <Overview clusterId={clusterId} obj={obj} kind={kind} onReveal={() => load(true)} masked={!!view?.masked} />}
        {tab === 'yaml' && view && (
          <div className="yaml-tab">
            <div className="toolbar">
              {!editing ? (
                <>
                  <button disabled={readOnly || gone} onClick={() => (view.masked ? toast('info', 'Revele os valores do Secret antes de editar') : setDraft(view.yaml))}>Editar</button>
                  <button onClick={() => navigator.clipboard.writeText(view.yaml).then(() => toast('ok', 'YAML copiado'))}>Copiar</button>
                  <button onClick={() => load()}>Recarregar</button>
                  {view.masked && <button onClick={() => load(true)}>Revelar Secret</button>}
                  {readOnly && <span className="muted">somente leitura</span>}
                </>
              ) : review ? (
                <>
                  <span className="muted">Diff: atual × resultado do dry-run no servidor</span>
                  <button onClick={() => setReview(undefined)}>Voltar a editar</button>
                  <button className="primary" disabled={busy} onClick={() => setConfirm('apply')}>Aplicar no cluster</button>
                </>
              ) : (
                <>
                  <button className="primary" disabled={busy} onClick={startReview}>Revisar alterações (dry-run)</button>
                  <button onClick={() => setDraft(undefined)}>Cancelar</button>
                </>
              )}
            </div>
            {review ? <YamlDiff original={review.original} modified={review.proposed} /> : <YamlEditor value={editing ? draft! : view.yaml} readOnly={!editing} onChange={setDraft} />}
          </div>
        )}
        {tab === 'events' && obj && <EventsList clusterId={clusterId} namespace={namespace ?? obj.metadata?.namespace ?? ''} uid={obj.metadata?.uid} />}
        {tab === 'topology' && namespace && <TopologyView clusterId={clusterId} namespace={namespace} kind={kind} name={name} />}
        {tab === 'metrics' && obj && <ObjectMetrics clusterId={clusterId} kind={kind} obj={obj} />}
        {tab === 'security' && namespace && <ObjectSecurity clusterId={clusterId} group={params.group} kind={kind} namespace={namespace} name={name} />}
        {tab === 'revisions' && namespace && <Revisions clusterId={clusterId} namespace={namespace} name={name} readOnly={readOnly} />}
      </div>
      {confirm === 'delete' && (
        <ConfirmDialog title={`Excluir ${kind}`} danger confirmLabel="Excluir" body={<p>Excluir <b>{namespace ? `${namespace}/` : ''}{name}</b>? Esta ação não pode ser desfeita.</p>}
          onCancel={() => setConfirm(undefined)} onConfirm={() => act(() => api.remove(clusterId, ref), `${name} excluído`)} />
      )}
      {confirm === 'restart' && (
        <ConfirmDialog title={`Rollout restart`} confirmLabel="Reiniciar" body={<p>Reiniciar os pods de <b>{name}</b> (rollout restart)?</p>}
          onCancel={() => setConfirm(undefined)} onConfirm={() => act(() => api.restart(clusterId, ref), `Restart de ${name} disparado`)} />
      )}
      {confirm === 'rollout-latest' && (
        <ConfirmDialog title="Rollout latest" confirmLabel="Iniciar rollout" body={<p>Iniciar uma nova implantação de <b>{name}</b> (oc rollout latest)?</p>}
          onCancel={() => setConfirm(undefined)} onConfirm={() => act(() => api3.rolloutLatest(clusterId, namespace!, name), `Rollout de ${name} iniciado`)} />
      )}
      {confirm === 'start-build' && (
        <ConfirmDialog title="Iniciar build" confirmLabel="Iniciar" body={<p>Iniciar um build de <b>{name}</b> (oc start-build)?</p>}
          onCancel={() => setConfirm(undefined)} onConfirm={() => {
            setConfirm(undefined)
            api3.startBuild(clusterId, namespace!, name).then((r) => {
              toast('ok', `Build ${r.build} iniciado`)
              openPanel(`buildlog:${clusterId}:${namespace}/${r.build}`, 'buildLog', `build ${r.build}`, { clusterId, namespace, build: r.build })
            }, (e) => toast('error', e.message))
          }} />
      )}
      {confirm === 'apply' && (
        <ConfirmDialog title="Aplicar alterações" confirmLabel="Aplicar" body={<p>Aplicar o YAML revisado em <b>{name}</b>?</p>} onCancel={() => setConfirm(undefined)} onConfirm={doApply} />
      )}
      {confirm === 'scale' && (
        <PromptDialog title={`Scale ${name}`} label="Réplicas" type="number" initial={String(replicas)} onCancel={() => setConfirm(undefined)}
          onConfirm={(v) => {
            const n = Number(v)
            if (!Number.isInteger(n) || n < 0) return toast('error', 'Número de réplicas inválido')
            act(() => api.scale(clusterId, ref, n), `${name} escalado para ${n}`)
          }} />
      )}
    </div>
  )
}

function ForwardButton({ compact, clusterId, kind, namespace, name, obj, disabled }: { compact?: boolean; clusterId: string; kind: string; namespace: string; name: string; obj?: Record<string, any>; disabled: boolean }) {
  const [ask, setAsk] = useState(false)
  const toast = useApp((s) => s.toast)
  const ports: number[] = useMemo(() => {
    if (!obj) return []
    if (kind === 'Service') return (obj.spec?.ports ?? []).map((p: { port: number }) => p.port)
    return (obj.spec?.containers ?? []).flatMap((c: { ports?: { containerPort: number }[] }) => (c.ports ?? []).map((p) => p.containerPort))
  }, [obj, kind])
  return (
    <>
      <Act compact={compact} icon={Waypoints} label="Port-forward" disabled={disabled} title={disabled ? 'Cluster em somente leitura' : 'Port-forward'} onClick={() => setAsk(true)} />
      {ask && (
        <PromptDialog title={`Port-forward ${name}`} label={`Porta remota${ports.length ? ` (disponíveis: ${ports.join(', ')})` : ''}`} type="number" initial={String(ports[0] ?? 80)} onCancel={() => setAsk(false)}
          onConfirm={async (v) => {
            setAsk(false)
            try {
              const f = await api.startForward(clusterId, { namespace, kind: kind === 'Service' ? 'service' : 'pod', name, remotePort: Number(v), localPort: 0 })
              toast('ok', `Encaminhando ${f.url} → ${name}:${f.remotePort}`)
              openPanel('forwards', 'forwards', 'Port-forwards', {})
            } catch (e) {
              toast('error', (e as Error).message)
            }
          }} />
      )}
    </>
  )
}

function Chips({ m }: { m?: Record<string, string> }) {
  if (!m || !Object.keys(m).length) return <span className="muted">—</span>
  return (
    <div className="chips">
      {Object.entries(m).map(([k, v]) => (
        <span className="chip" key={k} title={`${k}=${v}`}>
          {k}
          {v ? <b>={v.length > 60 ? v.slice(0, 60) + '…' : v}</b> : null}
        </span>
      ))}
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="card">
      <h4>{title}</h4>
      {children}
    </section>
  )
}

function Overview({ clusterId, obj, kind, masked, onReveal }: { clusterId: string; obj: Record<string, any>; kind: string; masked: boolean; onReveal: () => void }) {
  const md = obj.metadata ?? {}
  const conditions: any[] = obj.status?.conditions ?? []
  return (
    <div className="overview">
      <Section title="Metadados">
        <table className="kv">
          <tbody>
            <tr><td>UID</td><td className="mono">{md.uid}</td></tr>
            <tr><td>Criado</td><td>{md.creationTimestamp} ({age(md.creationTimestamp)})</td></tr>
            {md.ownerReferences?.length > 0 && (
              <tr><td>Controlado por</td><td>
                {md.ownerReferences.map((o: any) => (
                  <button key={o.uid} className="link" onClick={() => openObject(clusterId, o.apiVersion.includes('/') ? o.apiVersion.split('/')[0] : '', o.kind, o.name, md.namespace)}>{o.kind}/{o.name}</button>
                ))}
              </td></tr>
            )}
            <tr><td>Labels</td><td><Chips m={md.labels} /></td></tr>
            <tr><td>Annotations</td><td><Chips m={md.annotations} /></td></tr>
          </tbody>
        </table>
      </Section>
      {kind === 'Pod' && <PodDetails clusterId={clusterId} obj={obj} />}
      {['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'DeploymentConfig', 'ReplicationController'].includes(kind) && <WorkloadDetails clusterId={clusterId} obj={obj} />}
      {kind === 'Route' && <RouteDetails clusterId={clusterId} obj={obj} />}
      {kind === 'Service' && <ServiceDetails clusterId={clusterId} obj={obj} />}
      {(kind === 'ConfigMap' || kind === 'Secret') && <DataDetails obj={obj} secret={kind === 'Secret'} masked={masked} onReveal={onReveal} />}
      {conditions.length > 0 && (
        <Section title="Conditions">
          <table className="table">
            <thead><tr><th>Tipo</th><th>Status</th><th>Motivo</th><th>Mensagem</th><th>Última transição</th></tr></thead>
            <tbody>
              {conditions.map((c) => (
                <tr key={c.type}>
                  <td>{c.type}</td>
                  <td><span className={`status s-${c.status === 'True' ? (/(Fail|Pressure)/.test(c.type) ? 'error' : 'ok') : c.status === 'False' && !/(Fail|Pressure)/.test(c.type) ? 'warn' : 'neutral'}`}>{c.status}</span></td>
                  <td>{c.reason}</td>
                  <td className="wrap">{c.message}</td>
                  <td>{age(c.lastTransitionTime)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Section>
      )}
    </div>
  )
}

function ObjectMetrics({ clusterId, kind, obj }: { clusterId: string; kind: string; obj: Record<string, any> }) {
  const ns: string = obj.metadata?.namespace ?? ''
  const workload = kind !== 'Pod' && kind !== 'Node'
  const sel = podSelector(obj)
  const { rows, loading } = useWatch(clusterId, '', 'pods', ns, workload)
  const pods = useMemo(() => rows.filter((r) => selectorMatches(sel, r.labels)).map((r) => r.name).sort(), [rows, sel])
  const scope = useMemo(() => {
    if (kind === 'Pod') return { kind: 'pod' as const, namespace: ns, name: obj.metadata.name }
    if (kind === 'Node') return { kind: 'node' as const, name: obj.metadata.name }
    if (loading) return undefined
    return { kind: 'workload' as const, namespace: ns, name: obj.metadata.name, pods }
  }, [kind, ns, obj.metadata.name, pods, loading])
  if (workload && !loading && !pods.length) return <div className="pad muted">Este workload não tem pods agora.</div>
  return <MetricsView clusterId={clusterId} scope={scope} />
}

function containerState(cs?: any): string {
  if (!cs?.state) return 'Unknown'
  if (cs.state.running) return 'Running'
  if (cs.state.waiting) return cs.state.waiting.reason ?? 'Waiting'
  if (cs.state.terminated) return cs.state.terminated.reason ?? 'Terminated'
  return 'Unknown'
}

function PodDetails({ clusterId, obj }: { clusterId: string; obj: Record<string, any> }) {
  const statuses: any[] = [...(obj.status?.initContainerStatuses ?? []), ...(obj.status?.containerStatuses ?? [])]
  const containers: any[] = [...(obj.spec?.initContainers ?? []).map((c: any) => ({ ...c, init: true })), ...(obj.spec?.containers ?? [])]
  return (
    <>
      <Section title="Pod">
        <table className="kv">
          <tbody>
            <tr><td>Fase</td><td><span className={`status s-${healthOfStatus(obj.status?.phase ?? '')}`}>{obj.status?.phase}</span></td></tr>
            <tr><td>Node</td><td>{obj.spec?.nodeName ? <button className="link" onClick={() => openObject(clusterId, '', 'Node', obj.spec.nodeName)}>{obj.spec.nodeName}</button> : '—'}</td></tr>
            <tr><td>IP</td><td className="mono">{obj.status?.podIP ?? '—'}</td></tr>
            <tr><td>QoS</td><td>{obj.status?.qosClass}</td></tr>
            <tr><td>Service account</td><td>{obj.spec?.serviceAccountName}</td></tr>
          </tbody>
        </table>
      </Section>
      <Section title="Containers">
        <div className="containers">
          {containers.map((c) => {
            const st = statuses.find((s) => s.name === c.name)
            const state = containerState(st)
            return (
              <div className="container-card" key={c.name}>
                <div className="cc-head">
                  <span className={`dot d-${healthOfStatus(state)}`} />
                  <b>{c.name}</b>
                  {c.init && <span className="chip">init</span>}
                  <span className={`status s-${healthOfStatus(state)}`}>{state}</span>
                  <span className="muted">restarts: {st?.restartCount ?? 0}</span>
                </div>
                <div className="mono small">{c.image}</div>
                {c.ports?.length > 0 && <div className="small">Portas: {c.ports.map((p: any) => `${p.containerPort}/${p.protocol ?? 'TCP'}${p.name ? ` (${p.name})` : ''}`).join(', ')}</div>}
                {c.resources && (c.resources.requests || c.resources.limits) && (
                  <div className="small">
                    Requests: {Object.entries(c.resources.requests ?? {}).map(([k, v]) => `${k}=${v}`).join(' ') || '—'} · Limits: {Object.entries(c.resources.limits ?? {}).map(([k, v]) => `${k}=${v}`).join(' ') || '—'}
                  </div>
                )}
                {st?.state?.waiting?.message && <div className="small s-error wrap">{st.state.waiting.message}</div>}
                {st?.lastState?.terminated && <div className="small muted">Último término: {st.lastState.terminated.reason} (exit {st.lastState.terminated.exitCode})</div>}
              </div>
            )
          })}
        </div>
      </Section>
    </>
  )
}

function selectorMatches(sel: Record<string, string> | undefined, labels: Record<string, string> | undefined) {
  if (!sel || !Object.keys(sel).length) return false
  return Object.entries(sel).every(([k, v]) => labels?.[k] === v)
}

function PodsFor({ clusterId, namespace, selector }: { clusterId: string; namespace: string; selector?: Record<string, string> }) {
  const res = findResource(clusterId, '', 'pods')
  const { rows } = useWatch(clusterId, '', 'pods', namespace, !!res)
  const pods = rows.filter((r) => selectorMatches(selector, r.labels)).sort((a, b) => a.name.localeCompare(b.name))
  return (
    <table className="table">
      <thead><tr><th>Pod</th><th>Ready</th><th>Status</th><th>Restarts</th><th>Node</th><th>Idade</th></tr></thead>
      <tbody>
        {pods.map((p) => (
          <tr key={p.uid} className="clickable" onClick={() => openObject(clusterId, '', 'Pod', p.name, namespace)}>
            <td><span className={`dot d-${p.health}`} />{p.name}</td>
            <td>{String(p.cells.ready)}</td>
            <td><span className={`status s-${healthOfStatus(String(p.cells.status))}`}>{String(p.cells.status)}</span></td>
            <td>{String(p.cells.restarts)}</td>
            <td>{String(p.cells.node ?? '')}</td>
            <td>{age(p.created)}</td>
          </tr>
        ))}
        {!pods.length && <tr><td colSpan={6} className="muted">Nenhum pod correspondente.</td></tr>}
      </tbody>
    </table>
  )
}

/** Pod selector of a workload: matchLabels, or the plain map used by ReplicationController/DeploymentConfig. */
function podSelector(obj: Record<string, any>): Record<string, string> | undefined {
  const s = obj.spec?.selector
  if (!s) return undefined
  if (s.matchLabels) return s.matchLabels
  return typeof Object.values(s)[0] === 'string' ? (s as Record<string, string>) : undefined
}

/** Link for an OpenShift Route; host and path come from the cluster, so they are validated. */
function routeURL(obj: Record<string, any>) {
  if (!String(obj.apiVersion ?? '').startsWith('route.openshift.io/')) return ''
  const host = String(obj.spec?.host ?? obj.status?.ingress?.[0]?.host ?? '')
  if (!/^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$/.test(host)) return ''
  const rawPath = String(obj.spec?.path ?? '')
  const path = /^\/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*$/.test(rawPath) ? rawPath : ''
  return `${obj.spec?.tls?.termination ? 'https' : 'http'}://${host}${path}`
}

function RouteDetails({ clusterId, obj }: { clusterId: string; obj: Record<string, any> }) {
  const url = routeURL(obj)
  return (
    <section className="card">
      <h4>Route</h4>
      <table className="kv">
        <tbody>
          <tr><td>URL</td><td>{url ? <a href={url} target="_blank" rel="noreferrer" data-testid="route-url">{url}</a> : '—'}</td></tr>
          <tr><td>Service</td><td><button className="link" onClick={() => openObject(clusterId, '', 'Service', obj.spec?.to?.name, obj.metadata?.namespace)}>{obj.spec?.to?.name}</button>{obj.spec?.port?.targetPort ? ` : ${obj.spec.port.targetPort}` : ''}</td></tr>
          <tr><td>TLS</td><td>{obj.spec?.tls ? `${obj.spec.tls.termination}${obj.spec.tls.insecureEdgeTerminationPolicy ? ` (HTTP: ${obj.spec.tls.insecureEdgeTerminationPolicy})` : ''}` : 'sem TLS'}</td></tr>
          {obj.spec?.wildcardPolicy && obj.spec.wildcardPolicy !== 'None' && <tr><td>Wildcard</td><td>{obj.spec.wildcardPolicy}</td></tr>}
        </tbody>
      </table>
    </section>
  )
}

function WorkloadDetails({ clusterId, obj }: { clusterId: string; obj: Record<string, any> }) {
  const sel = podSelector(obj)
  const s = obj.status ?? {}
  return (
    <>
      <Section title="Réplicas">
        <div className="stats">
          {obj.spec?.replicas !== undefined && <div className="stat"><b>{obj.spec.replicas}</b><span>desejadas</span></div>}
          {s.readyReplicas !== undefined && <div className="stat"><b>{s.readyReplicas ?? 0}</b><span>prontas</span></div>}
          {s.updatedReplicas !== undefined && <div className="stat"><b>{s.updatedReplicas}</b><span>atualizadas</span></div>}
          {s.availableReplicas !== undefined && <div className="stat"><b>{s.availableReplicas}</b><span>disponíveis</span></div>}
          {s.numberReady !== undefined && <div className="stat"><b>{s.numberReady}/{s.desiredNumberScheduled}</b><span>prontas (DS)</span></div>}
          {obj.spec?.strategy?.type && <div className="stat"><b>{obj.spec.strategy.type}</b><span>estratégia</span></div>}
        </div>
        <div className="small">Imagens: {(obj.spec?.template?.spec?.containers ?? []).map((c: any) => c.image).join(', ')}</div>
      </Section>
      {sel && (
        <Section title="Pods">
          <PodsFor clusterId={clusterId} namespace={obj.metadata?.namespace} selector={sel} />
        </Section>
      )}
    </>
  )
}

function ServiceDetails({ clusterId, obj }: { clusterId: string; obj: Record<string, any> }) {
  return (
    <>
      <Section title="Service">
        <table className="kv">
          <tbody>
            <tr><td>Tipo</td><td>{obj.spec?.type}</td></tr>
            <tr><td>Cluster IP</td><td className="mono">{obj.spec?.clusterIP}</td></tr>
            <tr><td>Portas</td><td>{(obj.spec?.ports ?? []).map((p: any) => `${p.name ? p.name + ' ' : ''}${p.port}→${p.targetPort}/${p.protocol ?? 'TCP'}${p.nodePort ? ` node:${p.nodePort}` : ''}`).join(', ')}</td></tr>
            <tr><td>Selector</td><td><Chips m={obj.spec?.selector} /></td></tr>
          </tbody>
        </table>
      </Section>
      {obj.spec?.selector && (
        <Section title="Pods selecionados">
          <PodsFor clusterId={clusterId} namespace={obj.metadata?.namespace} selector={obj.spec.selector} />
        </Section>
      )}
    </>
  )
}

function decode(b64: string) {
  try {
    return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)))
  } catch {
    return '(binário)'
  }
}

function DataDetails({ obj, secret, masked, onReveal }: { obj: Record<string, any>; secret: boolean; masked: boolean; onReveal: () => void }) {
  const data: Record<string, string> = { ...(obj.data ?? {}), ...(obj.binaryData ?? {}) }
  return (
    <Section title={secret ? `Dados (${obj.type})` : 'Dados'}>
      {secret && masked && <button onClick={onReveal}>Revelar valores</button>}
      {Object.entries(data).map(([k, v]) => (
        <div key={k} className="data-item">
          <div className="data-key">{k}</div>
          <pre className="data-val">{secret ? (masked ? '••••••••' : decode(v)) : v}</pre>
        </div>
      ))}
      {!Object.keys(data).length && <span className="muted">Sem dados.</span>}
    </Section>
  )
}

function EventsList({ clusterId, namespace, uid }: { clusterId: string; namespace: string; uid?: string }) {
  const [events, setEvents] = useState<KubeEvent[]>()
  const [err, setErr] = useState<string>()
  useEffect(() => {
    if (!uid) return
    let stop = false
    const load = () => api.events(clusterId, namespace, uid).then((e) => !stop && setEvents(e)).catch((e) => !stop && setErr(e.message))
    load()
    const t = setInterval(load, 5000)
    return () => {
      stop = true
      clearInterval(t)
    }
  }, [clusterId, namespace, uid])
  if (err) return <div className="banner error">{err}</div>
  if (!events) return <div className="pad"><span className="spinner" /></div>
  const sorted = events.slice().sort((a, b) => (b.lastTimestamp ?? b.eventTime ?? '').localeCompare(a.lastTimestamp ?? a.eventTime ?? ''))
  return (
    <table className="table">
      <thead><tr><th>Tipo</th><th>Motivo</th><th>Mensagem</th><th>Qtd</th><th>Origem</th><th>Última vez</th></tr></thead>
      <tbody>
        {sorted.map((e) => (
          <tr key={e.metadata.uid}>
            <td><span className={`status s-${e.type === 'Warning' ? 'error' : 'ok'}`}>{e.type}</span></td>
            <td>{e.reason}</td>
            <td className="wrap">{e.message}</td>
            <td>{e.count ?? 1}</td>
            <td>{e.source?.component}</td>
            <td>{age(e.lastTimestamp ?? e.eventTime ?? e.metadata.creationTimestamp)}</td>
          </tr>
        ))}
        {!sorted.length && <tr><td colSpan={6} className="muted">Nenhum evento recente.</td></tr>}
      </tbody>
    </table>
  )
}
