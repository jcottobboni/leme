import { useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { ExternalLink, GitCompareArrows, Lock, Pause, Play, RotateCw, ShieldAlert } from 'lucide-react'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { age } from '../../format'
import { openObject } from '../../components/ObjectPanel'
import { ConfirmDialog, Modal } from '../../components/Dialogs'
import { YamlDiff } from '../../components/Monaco'
import { ClusterGate, Empty, ErrorBanner, Partial, Tabs, useAsync } from '../common'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { pressable } from '../../components/a11y'
import { fluxApi, fluxState, shortRevision, type FluxAction, type FluxObject, type FluxOverview, type FluxProposal } from './api'
import './flux.css'

export function openFlux(clusterId: string, opts?: OpenOptions) {
  openPanel(`flux:${clusterId}`, 'flux', 'Flux', { clusterId }, opts)
}

const READ_ONLY_HINT = 'O cluster está em somente leitura: libere a escrita (cadeado na barra lateral) para pedir reconcile, suspender ou retomar.'

type Tab = 'kustomizations' | 'helmReleases' | 'sources'
const TAB_KINDS: Record<Tab, string> = { kustomizations: 'Kustomizations', helmReleases: 'HelmReleases', sources: 'Fontes' }

function key(o: { kind: string; namespace: string; name: string }) {
  return `${o.kind}/${o.namespace}/${o.name}`
}

function StateBadge({ o }: { o: FluxObject }) {
  const s = fluxState(o)
  return <span className={`pill pill-${s.tone}`} data-testid="flux-ready" title={o.reason ? `${o.reason}${o.message ? ': ' + o.message : ''}` : undefined}>{s.label}</span>
}

function sourceText(o: FluxObject) {
  if (o.source) return `${o.source.kind} ${o.source.namespace && o.source.namespace !== o.namespace ? o.source.namespace + '/' : ''}${o.source.name}`
  return '—'
}

function Flux({ clusterId }: { clusterId: string }) {
  const { data, error, loading, reload } = useAsync(() => fluxApi.list(clusterId), [clusterId], 15000)
  const [tab, setTab] = useState<Tab>('kustomizations')
  const [q, setQ] = useState('')
  const [ns, setNs] = useState('')
  const [state, setState] = useState('')
  const [sel, setSel] = useState<string>()
  const all = useMemo(() => (data ? [...data.kustomizations, ...data.helmReleases, ...data.sources] : []), [data])
  const namespaces = useMemo(() => [...new Set(all.map((o) => o.namespace))].sort(), [all])
  const list = data?.[tab] ?? []
  const rows = useMemo(() => list.filter((o) =>
    (!ns || o.namespace === ns) && (!state || fluxState(o).label === state) &&
    (!q || `${o.kind} ${o.name} ${o.namespace} ${sourceText(o)} ${o.revision ?? ''} ${o.url ?? ''} ${o.chart ?? ''} ${o.path ?? ''}`.toLowerCase().includes(q.toLowerCase())),
  ), [list, ns, state, q])
  const selected = all.find((o) => key(o) === sel)
  // With the detail open the list keeps only name, state and revision (the rest is in the detail).
  const compact = !!selected

  if (data && !data.installed) {
    return (
      <div className="grid-panel" data-testid="flux-panel">
        <PanelHeader icon={GitCompareArrows} title="Flux" actions={[refreshAction(reload, loading)]} />
        <Empty testId="flux-not-installed" icon={<GitCompareArrows size={22} />} title="Flux não encontrado neste cluster"
          code={'flux check --pre\nflux install'}>
          {data.hint}
        </Empty>
      </div>
    )
  }

  const count = (t: Tab) => data?.[t].length ?? 0
  return (
    <div className="grid-panel" data-testid="flux-panel">
      <PanelHeader icon={GitCompareArrows} title="Flux" actions={[refreshAction(reload, loading)]}>
        <input className="search" placeholder="Buscar…" aria-label="Buscar objeto do Flux" value={q} onChange={(e) => setQ(e.target.value)} data-testid="flux-search" />
        <select value={ns} onChange={(e) => setNs(e.target.value)} title="Namespace" data-testid="flux-ns">
          <option value="">Todos os namespaces</option>
          {namespaces.map((n) => <option key={n}>{n}</option>)}
        </select>
        <select value={state} onChange={(e) => setState(e.target.value)} title="Estado" data-testid="flux-filter-state">
          <option value="">Estado: todos</option>
          {['Pronto', 'Falhou', 'Reconciliando', 'Suspenso', 'Desconhecido'].map((s) => <option key={s}>{s}</option>)}
        </select>
        <span className="muted">{rows.length} de {list.length}</span>
      </PanelHeader>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      <MissingGroups data={data} />
      <div className="flux-tabs" data-testid="flux-tabs">
        <Tabs<Tab> tabs={(Object.keys(TAB_KINDS) as Tab[]).map((t) => [t, `${TAB_KINDS[t]} (${count(t)})`])} value={tab} onChange={(t) => setTab(t)} />
      </div>
      <div className={`flux-body ${selected ? 'with-detail' : ''}`}>
        <div className="flux-list scroll">
          {!data && loading ? null : !rows.length ? (
            <Empty>{list.length ? 'Nenhum objeto com estes filtros.' : tab === 'sources' ? 'Nenhuma fonte do Flux (GitRepository, OCIRepository, HelmRepository, Bucket).' : `Nenhuma ${tab === 'kustomizations' ? 'Kustomization' : 'HelmRelease'} do Flux.`}</Empty>
          ) : (
            <table className="table" data-testid={`flux-table-${tab}`}>
              <thead>
                <tr>
                  <th>Nome</th><th>Estado</th>
                  {tab === 'sources' && !compact && <th>Tipo</th>}
                  <th>{tab === 'sources' ? 'Revisão do artefato' : 'Revisão aplicada'}</th>
                  {!compact && <><th>{tab === 'sources' ? 'URL' : 'Fonte'}</th><th>Atualizado</th></>}
                </tr>
              </thead>
              <tbody>
                {rows.map((o) => (
                  <tr key={key(o)} className={`clickable ${sel === key(o) ? 'flux-active' : ''}`} {...pressable(() => setSel(key(o)), null)} aria-selected={sel === key(o)}
                    data-testid={`flux-row-${o.kind}-${o.name}`}>
                    <td><span className={`dot d-${fluxState(o).tone}`} /><b>{o.name}</b> <span className="muted small">{o.namespace}</span></td>
                    <td><StateBadge o={o} />{o.reconciling && !o.suspended && <RotateCw size={12} className="spin-icon flux-spin" aria-label="Reconciliando" />}</td>
                    {tab === 'sources' && !compact && <td>{o.kind}</td>}
                    <td className="flux-ellipsis" title={o.revision}><code>{shortRevision(o.revision)}</code></td>
                    {!compact && (
                      <>
                        {tab === 'sources'
                          ? <td className="flux-ellipsis" title={o.url}>{o.url || '—'}{o.ref && <span className="muted small"> · {o.ref}</span>}</td>
                          : <td className="flux-ellipsis" title={sourceText(o)}>{sourceText(o)}</td>}
                        <td className="muted">{o.updatedAt ? `${age(o.updatedAt)} atrás` : '—'}</td>
                      </>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        {selected && <Detail key={key(selected)} clusterId={clusterId} o={selected} onClose={() => setSel(undefined)} onChanged={reload} />}
      </div>
    </div>
  )
}

/** Some Flux controllers are installed and others are not (e.g. only source and kustomize). */
function MissingGroups({ data }: { data?: FluxOverview }) {
  if (!data?.installed) return null
  const missing = Object.entries(data.groups).filter(([, ok]) => !ok).map(([g]) => g)
  if (!missing.length) return null
  return <div className="banner warn" data-testid="flux-missing-groups">CRDs ausentes neste cluster: {missing.join(', ')}. Os objetos desses grupos não aparecem aqui.</div>
}

function Detail({ clusterId, o, onClose, onChanged }: { clusterId: string; o: FluxObject; onClose: () => void; onChanged: () => void }) {
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
  const [proposal, setProposal] = useState<FluxProposal>()
  const blocked = readOnly ? READ_ONLY_HINT : ''
  const ask = async (a: FluxAction) => {
    setBusy(true)
    setActionError(undefined)
    try {
      setProposal(await fluxApi.propose(clusterId, o, a))
    } catch (e) {
      setActionError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const pending = o.requestedAt && o.requestedAt !== o.handledAt
  return (
    <div className="flux-detail" data-testid="flux-detail">
      <div className="flux-detail-head">
        <GitCompareArrows size={15} />
        <b className="flux-title">{o.name}</b>
        <span className="muted small">{o.kind} · {o.namespace}</span>
        <StateBadge o={o} />
        <span className="spacer" />
        <button className="icon-btn" onClick={onClose} title="Fechar detalhe" aria-label="Fechar detalhe">×</button>
      </div>
      <div className="flux-detail-body">
        <div className="flux-actions">
          <button className="primary" disabled={!!blocked || busy || o.suspended} onClick={() => ask('reconcile')} data-testid="flux-reconcile-btn"
            title={blocked || (o.suspended ? 'Objeto suspenso: retome antes de reconciliar.' : 'Pede ao Flux que reconcilie agora (com prévia e confirmação)')}>
            <RotateCw size={14} /> Reconcile…
          </button>
          {o.suspended ? (
            <button disabled={!!blocked || busy} onClick={() => ask('resume')} data-testid="flux-resume-btn" title={blocked || 'Remove spec.suspend e pede um reconcile (com prévia e confirmação)'}>
              <Play size={14} /> Retomar…
            </button>
          ) : (
            <button disabled={!!blocked || busy} onClick={() => ask('suspend')} data-testid="flux-suspend-btn" title={blocked || 'Grava spec.suspend: o Flux para de reconciliar este objeto (com prévia e confirmação)'}>
              <Pause size={14} /> Suspender…
            </button>
          )}
          <span className="spacer" />
          <button onClick={() => openObject(clusterId, o.group, o.kind, o.name, o.namespace)} data-testid="flux-open" title="Abrir o objeto (YAML, eventos)">
            <ExternalLink size={14} /> Abrir objeto
          </button>
        </div>
        {blocked && <div className="flux-note" data-testid="flux-readonly"><Lock size={13} /> {blocked}</div>}
        {pending && <div className="flux-note" data-testid="flux-pending">Reconcile pedido {age(o.requestedAt!)} atrás e ainda não atendido pelo controlador.</div>}
        <ErrorBanner error={actionError} />
        {o.ready === 'False' && o.message && <div className="banner error" data-testid="flux-message"><b>{o.reason}</b> {o.message}</div>}

        <section className="flux-section">
          <h4>Resumo</h4>
          <table className="kv">
            <tbody>
              {o.source && (
                <tr><td>Fonte</td><td>
                  <button className="link" onClick={() => openObject(clusterId, 'source.toolkit.fluxcd.io', o.source!.kind, o.source!.name, o.source!.namespace || o.namespace)}>{sourceText(o)}</button>
                </td></tr>
              )}
              {o.path && <tr><td>Caminho</td><td><code>{o.path}</code></td></tr>}
              {o.chart && <tr><td>Chart</td><td><code>{o.chart}</code></td></tr>}
              {o.url && <tr><td>URL</td><td className="flux-wrap">{o.url}</td></tr>}
              {o.ref && <tr><td>Referência</td><td><code>{o.ref}</code></td></tr>}
              <tr><td>{o.group === 'source.toolkit.fluxcd.io' ? 'Revisão do artefato' : 'Revisão aplicada'}</td><td><code className="flux-wrap">{o.revision || '—'}</code></td></tr>
              {o.attempted && <tr><td>Última tentativa</td><td><code className="flux-wrap">{o.attempted}</code></td></tr>}
              {o.targetNamespace && <tr><td>Namespace de destino</td><td>{o.targetNamespace}</td></tr>}
              {o.interval && <tr><td>Intervalo</td><td>{o.interval}</td></tr>}
              <tr><td>Suspenso</td><td>{o.suspended ? 'sim' : 'não'}</td></tr>
              {o.handledAt && <tr><td>Último reconcile pedido atendido</td><td>{o.handledAt}</td></tr>}
            </tbody>
          </table>
        </section>

        <section className="flux-section">
          <h4>Condições</h4>
          {!o.conditions.length ? <div className="muted small">Nenhuma condição: o controlador ainda não processou este objeto.</div> : (
            <ul className="flux-conditions" data-testid="flux-conditions">
              {o.conditions.map((c) => (
                <li key={c.type} className={c.status === 'False' && c.type === 'Ready' ? 's-error' : ''}>
                  <b>{c.type}</b>={c.status} {c.reason && <span className="muted">({c.reason})</span>} {c.message}
                  {c.lastTransitionTime && <span className="muted small"> · {age(c.lastTransitionTime)} atrás</span>}
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
      {proposal && (
        <ProposalReview clusterId={clusterId} p={proposal} readOnly={readOnly} onClose={() => setProposal(undefined)}
          onApplied={() => {
            setProposal(undefined)
            useApp.getState().toast('ok', `${proposal.summary}: pedido gravado em ${o.name}`)
            onChanged()
          }} />
      )}
    </div>
  )
}

function ProposalReview({ clusterId, p, readOnly, onClose, onApplied }: { clusterId: string; p: FluxProposal; readOnly: boolean; onClose: () => void; onApplied: () => void }) {
  const [confirm, setConfirm] = useState(false)
  const [applying, setApplying] = useState(false)
  const [err, setErr] = useState<string>()
  const risky = (p.risks?.length ?? 0) > 0
  const [ack, setAck] = useState(false)
  const blocked = readOnly ? READ_ONLY_HINT : !p.rbacAllowed ? 'Você não tem permissão de update neste objeto.' : p.dryRunError ? 'O dry-run no servidor falhou.' : risky && !ack ? 'Marque que revisou os riscos.' : ''
  const apply = async () => {
    setConfirm(false)
    setApplying(true)
    setErr(undefined)
    try {
      await fluxApi.apply(clusterId, p.token)
      onApplied()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setApplying(false)
    }
  }
  return (
    <>
      <Modal title={`${p.summary} — ${p.ref.kind} ${p.ref.namespace}/${p.ref.name}`} onClose={onClose} width={1100}>
        <div className="flux-review" data-testid="flux-proposal">
          <div className="flux-pills">
            {p.dryRun ? <span className="pill pill-ok">dry-run ok</span> : p.dryRunError ? <span className="pill pill-error" title={p.dryRunError}>dry-run falhou</span> : <span className="pill pill-warn">sem dry-run (somente leitura)</span>}
            <span className={`pill pill-${p.rbacAllowed ? 'ok' : 'error'}`}>{p.rbacAllowed ? 'permissão ok' : 'sem permissão'}</span>
          </div>
          {p.dryRunError && <div className="banner error">{p.dryRunError}</div>}
          {!!p.notes?.length && <ul className="flux-notes" data-testid="flux-notes">{p.notes.map((n) => <li key={n}>{n}</li>)}</ul>}
          {risky && (
            <div className="flux-risks">
              <b><ShieldAlert size={14} /> Esta mudança amplia privilégios ou exposição</b>
              <ul>{p.risks!.map((r) => <li key={r}>{r}</li>)}</ul>
              <label><input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} /> Revisei os riscos e quero aplicar mesmo assim</label>
            </div>
          )}
          <div className="flux-diff"><YamlDiff original={p.before} modified={p.after} /></div>
          {err && <div className="banner error" data-testid="flux-apply-error">{err}</div>}
        </div>
        <div className="modal-actions">
          {blocked && <span className="muted small flux-blocked">{blocked}</span>}
          <button onClick={onClose}>Descartar</button>
          <button className="primary" disabled={!!blocked || applying} onClick={() => setConfirm(true)} data-testid="flux-apply"
            title={blocked || 'Grava o pedido com o mesmo resourceVersion da prévia, após novo dry-run'}>
            {applying ? 'Aplicando…' : 'Aplicar…'}
          </button>
        </div>
      </Modal>
      {confirm && (
        <ConfirmDialog title="Confirmar pedido ao Flux" confirmLabel="Aplicar" danger={p.summary.startsWith('Suspender')}
          body={<p>Gravar <b>{p.summary.toLowerCase()}</b> em <b>{p.ref.kind} {p.ref.namespace}/{p.ref.name}</b>? O controlador do Flux age a partir desse pedido.</p>}
          onConfirm={apply} onCancel={() => setConfirm(false)} />
      )}
    </>
  )
}

/** Flux CD — M7, RF04. */
export function FluxPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Flux clusterId={params.clusterId} />
    </ClusterGate>
  )
}
