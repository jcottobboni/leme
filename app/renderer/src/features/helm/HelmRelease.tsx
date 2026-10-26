import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type HelmDetail } from '../../api'
import { useApp } from '../../store'
import { age } from '../../format'
import { YamlEditor } from '../../components/Monaco'
import { ConfirmDialog } from '../../components/Dialogs'
import { openObject } from '../../components/ObjectPanel'
import { ClusterGate, ErrorBanner, Tabs, useAsync } from '../common'
import { helmStatusHealth } from './HelmReleases'
import { Review, type ReviewState } from './Review'
import { VersionDialog } from './VersionDialog'

type Tab = 'summary' | 'values' | 'manifest' | 'history'

interface Params {
  clusterId: string
  namespace: string
  name: string
}

function Release({ clusterId, namespace, name, onGone }: Params & { onGone: () => void }) {
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const toast = useApp((s) => s.toast)
  const [reveal, setReveal] = useState(false)
  const { data: d, error, reload } = useAsync(() => api.helmRelease(clusterId, namespace, name, reveal), [clusterId, namespace, name, reveal])
  const [tab, setTab] = useState<Tab>('summary')
  const [draft, setDraft] = useState<string>()
  const [review, setReview] = useState<ReviewState>()
  const [ask, setAsk] = useState<'uninstall' | 'version'>()

  const fail = (what: string) => (e: unknown) => toast('error', `${what}: ${(e as Error).message}`)

  const previewUpgrade = async (values: string) => {
    try {
      const res = await api.helmUpgrade(clusterId, namespace, name, { values, replaceValues: true, dryRun: true })
      setReview({
        title: `Upgrade de ${name}`,
        subtitle: `revisão ${d!.revision} → ${res.revision}`,
        previous: res.previous,
        next: res.manifest,
        confirmLabel: 'Aplicar upgrade',
        onConfirm: async () => {
          await api.helmUpgrade(clusterId, namespace, name, { values, replaceValues: true, dryRun: false }).then((r) => {
            toast('ok', `${name} atualizado (revisão ${r.revision})`)
            setDraft(undefined)
            reload()
          }, fail('Upgrade falhou'))
        },
      })
    } catch (e) {
      fail('Dry-run falhou')(e)
    }
  }

  const previewRollback = async (revision: number) => {
    try {
      const res = await api.helmRollback(clusterId, namespace, name, revision, true)
      setReview({
        title: `Rollback de ${name} para a revisão ${revision}`,
        previous: res.previous,
        next: res.manifest,
        confirmLabel: 'Fazer rollback',
        onConfirm: async () => {
          await api.helmRollback(clusterId, namespace, name, revision, false).then((r) => {
            toast('ok', `Rollback feito (revisão ${r.revision})`)
            reload()
          }, fail('Rollback falhou'))
        },
      })
    } catch (e) {
      fail('Rollback')(e)
    }
  }

  const previewVersion = async (chart: string, version: string) => {
    setAsk(undefined)
    try {
      const res = await api.helmUpgrade(clusterId, namespace, name, { chart, version, replaceValues: false, dryRun: true })
      setReview({
        title: `Upgrade de ${name} para ${chart} ${version}`,
        subtitle: 'values atuais mantidos',
        previous: res.previous,
        next: res.manifest,
        confirmLabel: 'Aplicar upgrade',
        onConfirm: async () => {
          await api.helmUpgrade(clusterId, namespace, name, { chart, version, replaceValues: false, dryRun: false }).then((r) => {
            toast('ok', `${name} atualizado para ${version} (revisão ${r.revision})`)
            reload()
          }, fail('Upgrade falhou'))
        },
      })
    } catch (e) {
      fail('Dry-run falhou')(e)
    }
  }

  if (error) return <ErrorBanner error={error} />
  if (!d) return <div className="pad"><span className="spinner" /></div>
  if (review) return <Review r={review} onClose={() => setReview(undefined)} />

  const health = helmStatusHealth(d.status)
  return (
    <div className="object-panel">
      <div className="obj-header">
        <div className="obj-title">
          <span className="kind-chip">Helm</span>
          <b>{d.name}</b>
          <span className="muted"> · {d.namespace} · rev {d.revision}</span>
          <span className={`status s-${health}`}>{d.status}</span>
        </div>
        <div className="obj-actions">
          <button disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : ''} onClick={() => setAsk('version')}>Mudar versão…</button>
          <button className="danger" disabled={readOnly} onClick={() => setAsk('uninstall')}>Desinstalar</button>
        </div>
      </div>
      <Tabs<Tab> tabs={[['summary', 'Resumo'], ['values', 'Values'], ['manifest', 'Manifest'], ['history', `Histórico (${d.history.length})`]]} value={tab} onChange={setTab} />
      <div className="obj-body">
        {tab === 'summary' && <Summary d={d} clusterId={clusterId} onReveal={() => setReveal(true)} />}
        {tab === 'values' && (
          <div className="yaml-tab">
            <div className="toolbar">
              {d.masked ? (
                <>
                  <span className="muted">Values podem conter senhas e ficam ocultos até você revelar.</span>
                  <button onClick={() => setReveal(true)}>Revelar values</button>
                </>
              ) : draft === undefined ? (
                <>
                  <span className="muted">Values definidos pelo usuário</span>
                  <button disabled={readOnly} onClick={() => setDraft(d.values || '# values do usuário (sobrescrevem os defaults do chart)\n')}>Editar values</button>
                </>
              ) : (
                <>
                  <button className="primary" onClick={() => previewUpgrade(draft)}>Revisar upgrade (dry-run)</button>
                  <button onClick={() => setDraft(undefined)}>Cancelar</button>
                </>
              )}
            </div>
            {!d.masked && <YamlEditor value={draft ?? d.values ?? ''} readOnly={draft === undefined} onChange={setDraft} />}
            {!d.masked && draft === undefined && d.computed && (
              <details className="pad">
                <summary>Values computados (defaults do chart + usuário)</summary>
                <pre className="data-val">{d.computed}</pre>
              </details>
            )}
          </div>
        )}
        {tab === 'manifest' && (
          <div className="yaml-tab">
            {d.masked && (
              <div className="toolbar">
                <span className="muted">Secrets mascarados.</span>
                <button onClick={() => setReveal(true)}>Revelar</button>
              </div>
            )}
            <YamlEditor value={d.manifest} readOnly />
          </div>
        )}
        {tab === 'history' && (
          <table className="table">
            <thead><tr><th>Revisão</th><th>Status</th><th>Chart</th><th>App</th><th>Quando</th><th>Descrição</th><th /></tr></thead>
            <tbody>
              {d.history.map((h) => (
                <tr key={h.revision} data-testid={`revision-${h.revision}`}>
                  <td>{h.revision}</td>
                  <td><span className={`status s-${helmStatusHealth(h.status)}`}>{h.status}</span></td>
                  <td>{h.chart}-{h.chartVersion}</td>
                  <td>{h.appVersion}</td>
                  <td>{age(h.updated)}</td>
                  <td className="wrap">{h.description}</td>
                  <td>{h.revision !== d.revision && <button disabled={readOnly} onClick={() => previewRollback(h.revision)}>Rollback</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {ask === 'uninstall' && (
        <ConfirmDialog
          title={`Desinstalar ${name}`}
          danger
          confirmLabel="Desinstalar"
          body={
            <>
              <p>Remover o release <b>{namespace}/{name}</b> e os {d.objects.length} objetos dele?</p>
              <ul className="small">{d.objects.slice(0, 12).map((o) => <li key={o.kind + o.name}>{o.kind}/{o.name}</li>)}</ul>
            </>
          }
          onCancel={() => setAsk(undefined)}
          onConfirm={() => {
            setAsk(undefined)
            api.helmUninstall(clusterId, namespace, name, false).then(() => {
              toast('ok', `${name} desinstalado`)
              onGone()
            }, fail('Uninstall falhou'))
          }}
        />
      )}
      {ask === 'version' && <VersionDialog chart={d.chart} current={d.chartVersion} onCancel={() => setAsk(undefined)} onConfirm={previewVersion} />}
    </div>
  )
}

function Summary({ d, clusterId, onReveal }: { d: HelmDetail; clusterId: string; onReveal: () => void }) {
  return (
    <div className="overview">
      <section className="card">
        <h4>Release</h4>
        <table className="kv">
          <tbody>
            <tr><td>Chart</td><td>{d.chart} {d.chartVersion}</td></tr>
            <tr><td>Versão do app</td><td>{d.appVersion || '—'}</td></tr>
            <tr><td>Atualizado</td><td>{d.updated} ({age(d.updated)})</td></tr>
            <tr><td>Descrição</td><td>{d.description}</td></tr>
          </tbody>
        </table>
      </section>
      <section className="card">
        <h4>Notas</h4>
        {d.masked ? (
          <p className="small muted">
            As notas são geradas a partir dos values e podem conter senhas. <button className="link" onClick={onReveal}>Revelar</button>
          </p>
        ) : d.notes ? (
          <pre className="data-val" data-testid="helm-notes">{d.notes}</pre>
        ) : (
          <span className="muted small">Sem notas.</span>
        )}
      </section>
      <section className="card">
        <h4>Objetos ({d.objects.length})</h4>
        <table className="table">
          <thead><tr><th>Tipo</th><th>Nome</th><th>Namespace</th></tr></thead>
          <tbody>
            {d.objects.map((o) => {
              const group = o.apiVersion.includes('/') ? o.apiVersion.split('/')[0] : ''
              return (
                <tr key={o.kind + '/' + o.namespace + '/' + o.name} className="clickable" onClick={() => openObject(clusterId, group, o.kind, o.name, o.namespace)}>
                  <td>{o.kind}</td>
                  <td>{o.name}</td>
                  <td>{o.namespace ?? '—'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </section>
    </div>
  )
}

export function HelmReleasePanel({ params, api: panelApi }: IDockviewPanelProps<Params>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Release {...params} onGone={() => panelApi.close()} />
    </ClusterGate>
  )
}
