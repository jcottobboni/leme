import { useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { ChevronDown, ChevronRight, GitPullRequestArrow, Lock, RotateCw, ShieldAlert, Workflow } from 'lucide-react'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { age } from '../../format'
import { openObject } from '../../components/ObjectPanel'
import { openTopology } from '../topology/Topology'
import { ConfirmDialog, Modal } from '../../components/Dialogs'
import { YamlDiff } from '../../components/Monaco'
import { ClusterGate, Empty, ErrorBanner, PanelHeader, Partial, RefreshButton, useAsync } from '../common'
import {
  argoApi, healthTone, HEALTH_LABEL, phaseTone, syncTone, SYNC_LABEL,
  type ArgoAction, type ArgoApp, type ArgoProposal, type ArgoResource, type ArgoSource,
} from './api'
import './argo.css'

export function openArgo(clusterId: string, opts?: OpenOptions) {
  openPanel(`argocd:${clusterId}`, 'argocd', 'Argo CD', { clusterId }, opts)
}

const READ_ONLY_HINT = 'O cluster está em somente leitura: libere a escrita (cadeado na barra lateral) para pedir Sync ou Refresh.'

function SyncBadge({ s }: { s: string }) {
  return <span className={`pill pill-${syncTone(s)}`} data-testid="argo-sync">{SYNC_LABEL[s] ?? s}</span>
}
function HealthBadge({ h }: { h?: string }) {
  if (!h) return null
  return <span className={`pill pill-${healthTone(h)}`} data-testid="argo-health">{HEALTH_LABEL[h] ?? h}</span>
}

function sourceText(s?: ArgoSource) {
  if (!s) return '—'
  return [s.repoURL, s.chart ? `chart ${s.chart}` : s.path, s.targetRevision ? `@ ${s.targetRevision}` : ''].filter(Boolean).join(' · ')
}

function shortRev(r?: string) {
  return r && /^[0-9a-f]{40}$/.test(r) ? r.slice(0, 8) : r || '—'
}

function Argo({ clusterId }: { clusterId: string }) {
  const { data, error, loading, reload } = useAsync(() => argoApi.list(clusterId), [clusterId], 15000)
  const [q, setQ] = useState('')
  const [ns, setNs] = useState('')
  const [sync, setSync] = useState('')
  const [health, setHealth] = useState('')
  const [sel, setSel] = useState<{ namespace: string; name: string }>()
  const apps = useMemo(() => data?.apps ?? [], [data])
  const namespaces = useMemo(() => [...new Set(apps.map((a) => a.namespace))].sort(), [apps])
  const rows = useMemo(() => apps.filter((a) =>
    (!ns || a.namespace === ns) && (!sync || a.sync === sync) && (!health || a.health === health) &&
    (!q || `${a.name} ${a.namespace} ${a.project} ${a.destination.namespace ?? ''} ${sourceText(a.source ?? a.sources?.[0])}`.toLowerCase().includes(q.toLowerCase())),
  ), [apps, ns, sync, health, q])

  if (data && !data.installed) {
    return (
      <div className="grid-panel" data-testid="argocd-panel">
        <PanelHeader icon={<GitPullRequestArrow size={15} />} title="Argo CD"><span className="spacer" /><RefreshButton onClick={reload} loading={loading} /></PanelHeader>
        <div className="empty argo-absent" data-testid="argo-not-installed">
          <GitPullRequestArrow size={26} />
          <b>Argo CD não encontrado neste cluster</b>
          <span className="muted">{data.hint}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="grid-panel" data-testid="argocd-panel">
      <PanelHeader icon={<GitPullRequestArrow size={15} />} title="Argo CD">
        <input className="search" placeholder="Buscar application…" value={q} onChange={(e) => setQ(e.target.value)} data-testid="argo-search" />
        <select value={ns} onChange={(e) => setNs(e.target.value)} title="Namespace da Application" data-testid="argo-ns">
          <option value="">Todos os namespaces</option>
          {namespaces.map((n) => <option key={n}>{n}</option>)}
        </select>
        <select value={sync} onChange={(e) => setSync(e.target.value)} data-testid="argo-filter-sync">
          <option value="">Sync: todos</option>
          {Object.entries(SYNC_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <select value={health} onChange={(e) => setHealth(e.target.value)} data-testid="argo-filter-health">
          <option value="">Saúde: todas</option>
          {Object.entries(HEALTH_LABEL).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
        <span className="muted">{rows.length} de {apps.length}</span>
        <span className="spacer" />
        <RefreshButton onClick={reload} loading={loading} />
      </PanelHeader>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      <div className={`argo-body ${sel ? 'with-detail' : ''}`}>
        <div className="argo-list scroll">
          {!loading && !rows.length ? (
            <div className="empty">
              <GitPullRequestArrow size={22} />
              <span>{apps.length ? 'Nenhuma Application com estes filtros.' : 'Nenhuma Application do Argo CD neste cluster.'}</span>
            </div>
          ) : (
            <table className="table">
              <thead><tr><th>Application</th><th>Sync</th><th>Saúde</th><th>Projeto</th><th>Origem</th><th>Destino</th></tr></thead>
              <tbody>
                {rows.map((a) => (
                  <AppRow key={a.namespace + '/' + a.name} a={a} active={sel?.namespace === a.namespace && sel?.name === a.name}
                    onClick={() => setSel({ namespace: a.namespace, name: a.name })} />
                ))}
              </tbody>
            </table>
          )}
        </div>
        {sel && <AppDetail key={sel.namespace + '/' + sel.name} clusterId={clusterId} ns={sel.namespace} name={sel.name} onClose={() => setSel(undefined)} onChanged={reload} />}
      </div>
    </div>
  )
}

function AppRow({ a, active, onClick }: { a: ArgoApp; active: boolean; onClick: () => void }) {
  const src = a.source ?? a.sources?.[0]
  return (
    <tr className={`clickable ${active ? 'argo-active' : ''}`} onClick={onClick} data-testid={`argo-app-${a.name}`}>
      <td><span className={`dot d-${healthTone(a.health)}`} /><b>{a.name}</b> <span className="muted small">{a.namespace}</span></td>
      <td><SyncBadge s={a.sync} />{a.operationPhase === 'Running' && <span className="argo-running" title="Operação em andamento"><RotateCw size={12} className="spin-icon" /></span>}</td>
      <td><HealthBadge h={a.health} /></td>
      <td>{a.project || '—'}</td>
      <td className="argo-ellipsis" title={sourceText(src)}>{src ? <>{src.chart ?? src.path ?? src.repoURL} <span className="muted">{src.targetRevision}</span></> : '—'}{(a.sources?.length ?? 0) > 1 && <span className="muted small"> +{a.sources!.length - 1}</span>}</td>
      <td className="argo-ellipsis" title={`${a.destination.namespace || '—'} em ${a.local ? 'este cluster' : a.destination.name || a.destination.server}`}>{a.destination.namespace || '—'} <span className="muted small">{a.local ? 'este cluster' : a.destination.name || a.destination.server}</span></td>
    </tr>
  )
}

function groupByKind(rs: ArgoResource[]) {
  const m = new Map<string, ArgoResource[]>()
  for (const r of rs) m.set(r.kind, [...(m.get(r.kind) ?? []), r])
  return [...m.entries()]
}

function AppDetail({ clusterId, ns, name, onClose, onChanged }: { clusterId: string; ns: string; name: string; onClose: () => void; onChanged: () => void }) {
  const { data: d, error, loading, reload } = useAsync(() => argoApi.detail(clusterId, ns, name), [clusterId, ns, name], 15000)
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const [prune, setPrune] = useState(false)
  const [hard, setHard] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
  const [proposal, setProposal] = useState<ArgoProposal>()

  const ask = async (a: ArgoAction) => {
    setBusy(true)
    setActionError(undefined)
    try {
      setProposal(await argoApi.propose(clusterId, ns, name, a))
    } catch (e) {
      setActionError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const running = d?.operationState?.phase === 'Running' || !!d?.pendingOperation
  const blocked = readOnly ? READ_ONLY_HINT : ''

  return (
    <div className="argo-detail" data-testid="argo-detail">
      <div className="argo-detail-head">
        <GitPullRequestArrow size={15} />
        <b className="argo-title">{name}</b>
        <span className="muted small">{ns}</span>
        {d && <><SyncBadge s={d.sync} /><HealthBadge h={d.health} /></>}
        <span className="spacer" />
        <button className="icon-btn" onClick={onClose} title="Fechar detalhe">×</button>
      </div>
      <ErrorBanner error={error} />
      {!d ? (loading ? <div className="pad"><span className="spinner" /></div> : null) : (
        <div className="argo-detail-body">
          <div className="argo-actions">
            <label className="argo-check"><input type="checkbox" checked={prune} onChange={(e) => setPrune(e.target.checked)} disabled={!!blocked} data-testid="argo-prune" /> prune</label>
            <button className="primary" disabled={!!blocked || busy || running} onClick={() => ask({ action: 'sync', prune })} data-testid="argo-sync-btn"
              title={blocked || (running ? 'Já há uma operação em andamento ou pendente.' : 'Pede ao Argo CD que sincronize esta Application (com prévia e confirmação)')}>
              Sync…
            </button>
            <label className="argo-check"><input type="checkbox" checked={hard} onChange={(e) => setHard(e.target.checked)} disabled={!!blocked} data-testid="argo-hard" /> hard</label>
            <button disabled={!!blocked || busy} onClick={() => ask({ action: 'refresh', hard })} data-testid="argo-refresh-btn"
              title={blocked || 'Pede ao Argo CD que releia a origem (com prévia e confirmação)'}>
              Refresh…
            </button>
            {d.local && d.destination.namespace && (
              <button onClick={() => openTopology(clusterId, d.destination.namespace)} data-testid="argo-topology" title="Abre a Topologia no namespace de destino">
                <Workflow size={14} /> Ver na Topologia
              </button>
            )}
            <span className="spacer" />
            <RefreshButton onClick={reload} loading={loading} />
          </div>
          {blocked && <div className="argo-note" data-testid="argo-readonly"><Lock size={13} /> {blocked}</div>}
          {running && <div className="argo-note">Há uma operação {d.pendingOperation && d.operationState?.phase !== 'Running' ? 'pedida, aguardando o controlador do Argo CD' : 'em andamento'}: o Sync fica bloqueado até terminar.</div>}
          <ErrorBanner error={actionError} />

          <section className="argo-section">
            <h4>Origem e destino</h4>
            <table className="kv">
              <tbody>
                {d.source && <tr><td>Origem</td><td>{sourceText(d.source)}</td></tr>}
                {d.sources?.map((s, i) => <tr key={i}><td>Origem {i + 1}</td><td>{sourceText(s)}</td></tr>)}
                <tr><td>Revisão comparada</td><td><code>{shortRev(d.revision)}</code></td></tr>
                <tr><td>Destino</td><td>{d.destination.namespace || '—'} em {d.local ? 'este cluster' : d.destination.name || d.destination.server}</td></tr>
                <tr><td>Projeto</td><td>{d.project}</td></tr>
                <tr><td>Sync automático</td><td>{d.automated ? 'sim' : 'não'}</td></tr>
                {d.healthMessage && <tr><td>Saúde</td><td>{d.healthMessage}</td></tr>}
                {d.reconciledAt && <tr><td>Reconciliado</td><td>{age(d.reconciledAt)} atrás</td></tr>}
                {d.operationState && (
                  <tr><td>Última operação</td><td data-testid="argo-operation">
                    <span className={`status s-${phaseTone(d.operationState.phase)}`}>{d.operationState.phase || '—'}</span>
                    {d.operationState.revision && <> · <code>{shortRev(d.operationState.revision)}</code></>}
                    {d.operationState.initiatedBy && <> · por {d.operationState.initiatedBy}</>}
                    {d.operationState.message && <div className="muted small">{d.operationState.message}</div>}
                  </td></tr>
                )}
              </tbody>
            </table>
          </section>

          <section className="argo-section" data-testid="argo-resources">
            <h4>Recursos ({d.resources.length}){!d.local && <span className="muted small"> · destino remoto: o Leme não confere estes objetos</span>}</h4>
            {!d.resources.length ? <Empty>O Argo CD ainda não listou recursos para esta Application.</Empty> : groupByKind(d.resources).map(([kind, list]) => (
              <KindGroup key={kind} clusterId={clusterId} kind={kind} list={list} />
            ))}
          </section>

          <section className="argo-section">
            <h4>Condições</h4>
            {!d.conditions.length ? <div className="muted small">Nenhuma condição.</div> : (
              <ul className="argo-conditions" data-testid="argo-conditions">
                {d.conditions.map((c, i) => (
                  <li key={i} className={/Error|Warning/.test(c.type) ? 's-warn' : ''}><b>{c.type}</b> {c.message} {c.lastTransitionTime && <span className="muted small">· {age(c.lastTransitionTime)} atrás</span>}</li>
                ))}
              </ul>
            )}
          </section>

          <section className="argo-section">
            <h4>Histórico</h4>
            {!d.history.length ? <div className="muted small">Sem histórico de deploy.</div> : (
              <table className="table" data-testid="argo-history">
                <thead><tr><th>#</th><th>Revisão</th><th>Origem</th><th>Quando</th></tr></thead>
                <tbody>
                  {d.history.map((h) => (
                    <tr key={h.id}>
                      <td>{h.id}</td>
                      <td><code>{shortRev(h.revision ?? h.revisions?.[0])}</code></td>
                      <td className="argo-ellipsis" title={sourceText(h.source)}>{h.source ? (h.source.chart ?? h.source.path ?? h.source.repoURL) : '—'}</td>
                      <td>{h.deployedAt ? `${age(h.deployedAt)} atrás` : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </div>
      )}
      {proposal && (
        <ProposalReview clusterId={clusterId} p={proposal} readOnly={readOnly}
          remote={d && !d.local ? d.destination.name || d.destination.server || 'outro cluster' : undefined}
          onClose={() => setProposal(undefined)}
          onApplied={() => {
            setProposal(undefined)
            useApp.getState().toast('ok', `${proposal.summary}: pedido gravado em ${name}`)
            reload()
            onChanged()
          }} />
      )}
    </div>
  )
}

function KindGroup({ clusterId, kind, list }: { clusterId: string; kind: string; list: ArgoResource[] }) {
  const [open, setOpen] = useState(true)
  return (
    <div className="argo-kind" data-testid={`argo-kind-${kind}`}>
      <button className="argo-kind-head" onClick={() => setOpen(!open)}>
        {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />} {kind} <span className="muted">{list.length}</span>
      </button>
      {open && list.map((r) => {
        const missing = r.exists === false
        return (
          <div key={`${r.group}/${r.namespace}/${r.name}`} className={`argo-res ${missing ? 'missing' : ''}`} data-testid={`argo-res-${r.kind}-${r.name}`}>
            <span className={`dot d-${healthTone(r.health)}`} />
            <button className="link" disabled={missing} onClick={() => openObject(clusterId, r.group, r.kind, r.name, r.namespace)}
              title={missing ? 'Não existe no cluster conectado' : 'Abrir o objeto'}>{r.name}</button>
            {r.namespace && <span className="muted small">{r.namespace}</span>}
            <span className="spacer" />
            {r.status && <span className={`status s-${syncTone(r.status)} small`}>{SYNC_LABEL[r.status] ?? r.status}</span>}
            {r.health && <span className={`status s-${healthTone(r.health)} small`} title={r.message}>{HEALTH_LABEL[r.health] ?? r.health}</span>}
            {missing && <span className="chip missing">ausente no cluster</span>}
          </div>
        )
      })}
    </div>
  )
}

function ProposalReview({ clusterId, p, readOnly, remote, onClose, onApplied }: { clusterId: string; p: ArgoProposal; readOnly: boolean; remote?: string; onClose: () => void; onApplied: () => void }) {
  const [confirm, setConfirm] = useState(false)
  const [typed, setTyped] = useState('')
  // A sync on another cluster is not covered by the connected cluster's lock or production
  // confirmation: the Application name must be typed.
  const typedRequired = !!remote && p.summary.startsWith('Sync')
  const [applying, setApplying] = useState(false)
  const [err, setErr] = useState<string>()
  const risky = (p.risks?.length ?? 0) > 0
  const [ack, setAck] = useState(false)
  const blocked = readOnly ? READ_ONLY_HINT : !p.rbacAllowed ? 'Você não tem permissão de update nesta Application.' : p.dryRunError ? 'O dry-run no servidor falhou.' : risky && !ack ? 'Marque que revisou os riscos.' : ''
  const apply = async () => {
    setConfirm(false)
    setApplying(true)
    setErr(undefined)
    try {
      await argoApi.apply(clusterId, p.token)
      onApplied()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setApplying(false)
    }
  }
  return (
    <>
      <Modal title={`${p.summary} — ${p.ref.namespace}/${p.ref.name}`} onClose={onClose} width={1100}>
        <div className="argo-review" data-testid="argo-proposal">
          <div className="argo-pills">
            {p.dryRun ? <span className="pill pill-ok">dry-run ok</span> : p.dryRunError ? <span className="pill pill-error" title={p.dryRunError}>dry-run falhou</span> : <span className="pill pill-warn">sem dry-run (somente leitura)</span>}
            <span className={`pill pill-${p.rbacAllowed ? 'ok' : 'error'}`}>{p.rbacAllowed ? 'permissão ok' : 'sem permissão'}</span>
          </div>
          {p.dryRunError && <div className="banner error">{p.dryRunError}</div>}
          {!!p.notes?.length && <ul className="argo-notes" data-testid="argo-notes">{p.notes.map((n) => <li key={n}>{n}</li>)}</ul>}
          {risky && (
            <div className="argo-risks" data-testid="argo-risks">
              <b><ShieldAlert size={14} /> Esta mudança amplia privilégios ou exposição</b>
              <ul>{p.risks!.map((r) => <li key={r}>{r}</li>)}</ul>
              <label><input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} /> Revisei os riscos e quero aplicar mesmo assim</label>
            </div>
          )}
          <div className="argo-diff"><YamlDiff original={p.before} modified={p.after} /></div>
          {err && <div className="banner error" data-testid="argo-apply-error">{err}</div>}
        </div>
        <div className="modal-actions">
          {blocked && <span className="muted small argo-blocked">{blocked}</span>}
          <button onClick={onClose}>Descartar</button>
          <button className="primary" disabled={!!blocked || applying} onClick={() => setConfirm(true)} data-testid="argo-apply"
            title={blocked || 'Grava o pedido na Application com o mesmo resourceVersion da prévia, após novo dry-run'}>
            {applying ? 'Aplicando…' : 'Aplicar…'}
          </button>
        </div>
      </Modal>
      {confirm && typedRequired && (
        <Modal title="Confirmar pedido ao Argo CD" onClose={() => { setConfirm(false); setTyped('') }}>
          <div data-testid="argo-confirm-remote">
            <p>Gravar <b>{p.summary.toLowerCase()}</b> em <b>{p.ref.namespace}/{p.ref.name}</b>? O controlador do Argo CD executa a operação no cluster de destino <b>{remote}</b>, que não é o cluster conectado: a trava de somente leitura e a confirmação de produção do Leme não o protegem.</p>
            <p>Digite <code>{p.ref.name}</code> para confirmar.</p>
            <input autoFocus className="input" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="nome da Application" data-testid="argo-confirm-name" />
          </div>
          <div className="modal-actions">
            <button onClick={() => { setConfirm(false); setTyped('') }}>Cancelar</button>
            <button className="danger" disabled={typed !== p.ref.name} onClick={apply}>Aplicar</button>
          </div>
        </Modal>
      )}
      {confirm && !typedRequired && (
        <ConfirmDialog title="Confirmar pedido ao Argo CD" confirmLabel="Aplicar" danger={p.summary.includes('prune')}
          body={<p>Gravar <b>{p.summary.toLowerCase()}</b> em <b>{p.ref.namespace}/{p.ref.name}</b>? O controlador do Argo CD executa a operação no cluster de destino.</p>}
          onConfirm={apply} onCancel={() => setConfirm(false)} />
      )}
    </>
  )
}

/** Argo CD Applications — M5, feature argocd. */
export function ArgoPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Argo clusterId={params.clusterId} />
    </ClusterGate>
  )
}

