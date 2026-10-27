import { useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type AccessRequest, type PolicyRule, type Subject, type WhoCanEntry } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { openObject } from '../../components/ObjectPanel'
import { ClusterGate, ErrorBanner, Partial, Tabs, useAsync } from '../common'
import { TEMPLATES } from './templates'

const VERBS = ['get', 'list', 'watch', 'create', 'update', 'patch', 'delete', 'deletecollection']

type Tab = 'mine' | 'can' | 'whocan' | 'subjects' | 'create'

export function openRbac(clusterId: string, opts?: OpenOptions) {
  openPanel(`rbac:${clusterId}`, 'rbac', 'RBAC', { clusterId }, opts)
}

function Mine({ clusterId }: { clusterId: string }) {
  const fallback = useApp((s) => s.connected[clusterId]?.defaultNamespace || 'default')
  const ns = useApp((s) => s.namespace[clusterId] ?? '') || fallback
  const { data, error, loading } = useAsync(() => api.rbacMine(clusterId, ns), [clusterId, ns])
  const [q, setQ] = useState('')
  const [onlyAllowed, setOnlyAllowed] = useState(false)
  const rows = useMemo(
    () =>
      (data?.matrix ?? [])
        .filter((r) => !q || `${r.kind} ${r.resource} ${r.group}`.toLowerCase().includes(q.toLowerCase()))
        .filter((r) => !onlyAllowed || Object.values(r.verbs).some(Boolean))
        .sort((a, b) => a.kind.localeCompare(b.kind)),
    [data, q, onlyAllowed],
  )
  return (
    <div className="grid-panel">
      <div className="toolbar">
        <NamespaceSelect clusterId={clusterId} effective={ns} />
        <input className="search" placeholder="Filtrar recursos…" value={q} onChange={(e) => setQ(e.target.value)} />
        <label className="check"><input type="checkbox" checked={onlyAllowed} onChange={(e) => setOnlyAllowed(e.target.checked)} /> só com alguma permissão</label>
        {loading && <span className="spinner" />}
      </div>
      <ErrorBanner error={error} />
      {data?.incomplete && <div className="banner warn">O servidor marcou a resposta como incompleta (webhook ou autorizador externo): use “Posso…?” para confirmar casos específicos.{data.evaluationError ? ` ${data.evaluationError}` : ''}</div>}
      <div className="scroll">
        <table className="table matrix" data-testid="rbac-matrix">
          <thead><tr><th>Recurso</th><th>Grupo</th>{VERBS.map((v) => <th key={v} title={v}>{v === 'deletecollection' ? 'del·col' : v}</th>)}</tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.group + '/' + r.resource}>
                <td>{r.kind} <span className="muted small">{r.resource}</span></td>
                <td className="muted small">{r.group || 'core'}</td>
                {VERBS.map((v) => <td key={v} className={r.verbs[v] ? 'yes' : 'no'}>{r.verbs[v] ? '✓' : '·'}</td>)}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function useResourceOptions(clusterId: string) {
  const resources = useApp((s) => s.connected[clusterId]?.resources ?? [])
  return useMemo(() => resources.map((r) => ({ key: `${r.group}/${r.resource}`, label: `${r.kind} (${r.resource}${r.group ? '.' + r.group : ''})`, group: r.group, resource: r.resource, namespaced: r.namespaced })), [resources])
}

function AccessForm({ clusterId, onSubmit, submitLabel, needNamespaceHint }: { clusterId: string; onSubmit: (r: AccessRequest) => void; submitLabel: string; needNamespaceHint?: boolean }) {
  const opts = useResourceOptions(clusterId)
  const selectedNs = useApp((s) => s.namespace[clusterId] ?? '')
  const [verb, setVerb] = useState('get')
  const [res, setRes] = useState('/secrets')
  const [sub, setSub] = useState('')
  const [ns, setNs] = useState(selectedNs || 'default')
  const [name, setName] = useState('')
  const sel = opts.find((o) => o.key === res)
  return (
    <div className="row access-form">
      <label className="field">Verbo
        <select value={verb} onChange={(e) => setVerb(e.target.value)} data-testid="access-verb">{[...VERBS, '*', 'impersonate', 'escalate', 'bind'].map((v) => <option key={v}>{v}</option>)}</select>
      </label>
      <label className="field">Recurso
        <select value={res} onChange={(e) => setRes(e.target.value)} data-testid="access-resource">{opts.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}</select>
      </label>
      <label className="field">Subrecurso<input value={sub} onChange={(e) => setSub(e.target.value)} placeholder="log, exec…" style={{ width: 90 }} /></label>
      <label className="field">Namespace<input value={sel?.namespaced === false ? '' : ns} disabled={sel?.namespaced === false} onChange={(e) => setNs(e.target.value)} placeholder={needNamespaceHint ? 'vazio = cluster' : ''} data-testid="access-ns" /></label>
      <label className="field">Nome<input value={name} onChange={(e) => setName(e.target.value)} placeholder="(qualquer)" style={{ width: 120 }} /></label>
      <button className="primary" disabled={!sel} onClick={() => sel && onSubmit({ verb, group: sel.group, resource: sel.resource, subresource: sub || undefined, name: name || undefined, namespace: sel.namespaced ? ns : undefined })}>
        {submitLabel}
      </button>
    </div>
  )
}

function Can({ clusterId }: { clusterId: string }) {
  const [res, setRes] = useState<{ q: AccessRequest; allowed: boolean; reason?: string }>()
  const [err, setErr] = useState<string>()
  return (
    <div className="pad-panel">
      <p className="muted small">Pergunta ao API server (SelfSubjectAccessReview) se <b>você</b> pode fazer a ação.</p>
      <AccessForm clusterId={clusterId} submitLabel="Posso?" onSubmit={(q) => api.rbacCan(clusterId, q).then((r) => (setErr(undefined), setRes({ q, ...r })), (e) => setErr(e.message))} />
      <ErrorBanner error={err} />
      {res && (
        <div className={`answer ${res.allowed ? 'yes' : 'no'}`} data-testid="can-answer">
          <b>{res.allowed ? 'Sim' : 'Não'}</b> — {res.q.verb} {res.q.resource}{res.q.subresource ? '/' + res.q.subresource : ''}{res.q.name ? ' ' + res.q.name : ''}{res.q.namespace ? ` em ${res.q.namespace}` : ' (cluster)'}
          {res.reason && <div className="small muted">{res.reason}</div>}
        </div>
      )}
    </div>
  )
}

function SubjectLink({ e }: { e: { kind: string; name: string; namespace?: string } }) {
  return <span><span className="chip">{e.kind}</span> {e.namespace ? `${e.namespace}/` : ''}{e.name}</span>
}

function WhoCan({ clusterId }: { clusterId: string }) {
  const [res, setRes] = useState<{ entries: WhoCanEntry[]; errors: Record<string, string> }>()
  const [err, setErr] = useState<string>()
  return (
    <div className="pad-panel">
      <p className="muted small">Calculado a partir dos RoleBindings e ClusterRoleBindings que você consegue ler. Webhooks e autorizadores externos não entram.</p>
      <AccessForm clusterId={clusterId} needNamespaceHint submitLabel="Quem pode?" onSubmit={(q) => api.rbacWhoCan(clusterId, q).then((r) => (setErr(undefined), setRes(r)), (e) => setErr(e.message))} />
      <ErrorBanner error={err} />
      <Partial errors={res?.errors} />
      {res && (
        <table className="table" data-testid="whocan-table">
          <thead><tr><th>Subject</th><th>Via</th><th>Role</th></tr></thead>
          <tbody>
            {res.entries.map((e, i) => (
              <tr key={i}>
                <td><SubjectLink e={e} /></td>
                <td>
                  <button className="link" onClick={() => openObject(clusterId, 'rbac.authorization.k8s.io', e.bindingKind, e.bindingName, e.bindingNamespace)}>{e.bindingKind}/{e.bindingNamespace ? e.bindingNamespace + '/' : ''}{e.bindingName}</button>
                </td>
                <td>
                  <button className="link" onClick={() => openObject(clusterId, 'rbac.authorization.k8s.io', e.roleKind, e.roleName, e.roleKind === 'Role' ? e.bindingNamespace : undefined)}>{e.roleKind}/{e.roleName}</button>
                </td>
              </tr>
            ))}
            {!res.entries.length && <tr><td colSpan={3} className="muted">Ninguém (entre os bindings visíveis).</td></tr>}
          </tbody>
        </table>
      )}
    </div>
  )
}

function Rules({ rules }: { rules?: PolicyRule[] }) {
  if (!rules?.length) return <span className="muted small">sem regras visíveis</span>
  return (
    <table className="table small rules">
      <tbody>
        {rules.map((r, i) => (
          <tr key={i}>
            <td className="mono">{r.verbs.join(',')}</td>
            <td className="mono">{r.nonResourceURLs?.length ? r.nonResourceURLs.join(',') : (r.resources ?? []).join(',')}</td>
            <td className="mono muted">{(r.apiGroups ?? []).map((g) => g || 'core').join(',')}</td>
            <td className="mono muted">{r.resourceNames?.join(',')}</td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Subjects({ clusterId }: { clusterId: string }) {
  const [refresh, setRefresh] = useState(0)
  const { data, error, loading } = useAsync(() => api.rbacSubjects(clusterId, refresh > 0), [clusterId, refresh])
  const [q, setQ] = useState('')
  const [kind, setKind] = useState('')
  const [open, setOpen] = useState<string>()
  const list = useMemo(() => (data?.subjects ?? []).filter((s) => (!kind || s.kind === kind) && (!q || `${s.namespace ?? ''}/${s.name}`.toLowerCase().includes(q.toLowerCase()))), [data, q, kind])
  const key = (s: Subject) => `${s.kind}|${s.namespace ?? ''}|${s.name}`
  return (
    <div className="grid-panel">
      <div className="toolbar">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="">Todos</option>
          <option value="User">Users</option>
          <option value="Group">Groups</option>
          <option value="ServiceAccount">ServiceAccounts</option>
        </select>
        <input className="search" placeholder="Buscar subject…" value={q} onChange={(e) => setQ(e.target.value)} />
        <span className="muted">{list.length} subjects</span>
        {loading && <span className="spinner" />}
        <span className="spacer" />
        <button onClick={() => setRefresh((r) => r + 1)}>Recarregar</button>
      </div>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      <div className="scroll tree">
        {list.map((s) => (
          <div key={key(s)} className="tree-node">
            <div className="tree-head" onClick={() => setOpen(open === key(s) ? undefined : key(s))}>
              <span className="chev">{open === key(s) ? '▾' : '▸'}</span>
              <SubjectLink e={s} />
              <span className="muted small"> · {s.grants.length} binding{s.grants.length > 1 ? 's' : ''}</span>
            </div>
            {open === key(s) && (
              <div className="tree-body">
                {s.grants.map((g, i) => (
                  <div key={i} className="grant">
                    <div>
                      <button className="link" onClick={() => openObject(clusterId, 'rbac.authorization.k8s.io', g.bindingKind, g.bindingName, g.bindingNamespace)}>{g.bindingKind} {g.bindingNamespace ? g.bindingNamespace + '/' : ''}{g.bindingName}</button>
                      {' → '}
                      <button className="link" onClick={() => openObject(clusterId, 'rbac.authorization.k8s.io', g.roleKind, g.roleName, g.roleKind === 'Role' ? g.bindingNamespace : undefined)}>{g.roleKind} {g.roleName}</button>
                      {!g.roleFound && <span className="s-warn small"> (role não encontrada ou sem acesso)</span>}
                      {g.bindingKind === 'RoleBinding' && <span className="muted small"> · válido só em {g.bindingNamespace}</span>}
                    </div>
                    <Rules rules={g.rules} />
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function Create({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] || 'default')
  return (
    <div className="pad-panel">
      <p className="muted small">Abre o modelo no editor de criação: você revisa o YAML, roda o dry-run no servidor e confirma.</p>
      <div className="templates">
        {TEMPLATES.filter((t) => t.group === 'RBAC').map((t) => (
          <button key={t.id} onClick={() => openCreate(clusterId, t.id, ns)}>{t.label}</button>
        ))}
      </div>
    </div>
  )
}

export function openCreate(clusterId: string, template = 'blank', namespace = 'default') {
  openPanel(`create:${clusterId}:${Date.now()}`, 'create', 'Novo recurso', { clusterId, template, namespace })
}

export function RbacPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  const [tab, setTab] = useState<Tab>('mine')
  return (
    <ClusterGate clusterId={params.clusterId}>
      <div className="object-panel">
        <Tabs<Tab> tabs={[['mine', 'Minhas permissões'], ['can', 'Posso…?'], ['whocan', 'Quem pode…?'], ['subjects', 'Subjects'], ['create', 'Criar role/binding']]} value={tab} onChange={setTab} />
        <div className="obj-body">
          {tab === 'mine' && <Mine clusterId={params.clusterId} />}
          {tab === 'can' && <Can clusterId={params.clusterId} />}
          {tab === 'whocan' && <WhoCan clusterId={params.clusterId} />}
          {tab === 'subjects' && <Subjects clusterId={params.clusterId} />}
          {tab === 'create' && <Create clusterId={params.clusterId} />}
        </div>
      </div>
    </ClusterGate>
  )
}
