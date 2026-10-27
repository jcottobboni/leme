import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { AppWindow, ChartColumn, ExternalLink, KeyRound, Square } from 'lucide-react'
import { api } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { ClusterGate, ErrorBanner, useAsync } from '../common'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { embedInApp, openEmbed } from './embed'
import { grafanaApi, KIND_LABEL, type GrafanaTarget } from './api'
import './grafana.css'

export function openGrafana(clusterId: string, opts?: OpenOptions) {
  openPanel(`grafana:${clusterId}`, 'grafana', 'Grafana e Prometheus', { clusterId }, opts)
}

const keyOf = (t: GrafanaTarget) => `${t.namespace}-${t.service}`

function TargetCard({ clusterId, t, readOnly, onChange }: { clusterId: string; t: GrafanaTarget; readOnly: boolean; onChange: () => void }) {
  const toast = useApp((s) => s.toast)
  const [busy, setBusy] = useState(false)
  const label = KIND_LABEL[t.kind]
  const open = async () => {
    setBusy(true)
    try {
      const r = await grafanaApi.open(clusterId, t)
      const res = await openEmbed(r.url, `${label} — ${t.namespace}/${t.service}`)
      if (!res.ok) toast('error', `Não foi possível abrir a janela: ${res.error ?? 'recusado'}`)
    } catch (e) {
      toast('error', `Falha ao abrir ${label}: ${(e as Error).message}`)
    } finally {
      setBusy(false)
      onChange()
    }
  }
  const stop = async () => {
    if (!t.forward) return
    setBusy(true)
    try {
      await api.stopForward(t.forward.id)
    } catch (e) {
      toast('error', `Falha ao parar o port-forward: ${(e as Error).message}`)
    } finally {
      setBusy(false)
      onChange()
    }
  }
  const https = t.scheme !== 'http'
  return (
    <div className="gfn-card" data-testid={`gfn-target-${keyOf(t)}`}>
      <div className="gfn-card-head">
        <span className={`gfn-kind gfn-kind-${t.kind}`}>{label}</span>
        <span className="gfn-name" title={`${t.namespace}/${t.service}`}>{t.namespace}/{t.service}</span>
      </div>
      <div className="gfn-meta">
        <span>porta {t.port}</span>
        <span>{t.scheme}</span>
        {t.forward ? (
          <span className="gfn-live" data-testid={`gfn-forward-${keyOf(t)}`}>port-forward ativo: {t.forward.url.replace(/\/$/, '')}</span>
        ) : (
          <span className="muted">sem port-forward</span>
        )}
      </div>
      <div className="gfn-actions">
        <button
          className="primary"
          disabled={busy || https || (readOnly && !t.forward)}
          title={https ? 'Serviços HTTPS não abrem no app' : readOnly && !t.forward ? 'Somente leitura: libere a escrita para abrir o port-forward' : undefined}
          onClick={open}
          data-testid={`gfn-open-${keyOf(t)}`}
        >
          {embedInApp ? <AppWindow size={14} /> : <ExternalLink size={14} />} {embedInApp ? 'Abrir no app' : 'Abrir em nova aba'}
        </button>
        {t.forward && (
          <button disabled={busy} onClick={stop} data-testid={`gfn-stop-${keyOf(t)}`}>
            <Square size={14} /> Parar
          </button>
        )}
        {t.adminSecret && (
          <button
            onClick={() => openObject(clusterId, '', 'Secret', t.adminSecret!.name, t.adminSecret!.namespace)}
            title="Abre o Secret; o valor só aparece com Revelar, que fica registrado"
            data-testid={`gfn-secret-${keyOf(t)}`}
          >
            <KeyRound size={14} /> Credencial de admin
          </button>
        )}
      </div>
    </div>
  )
}

function EmptyState() {
  return (
    <div className="gfn-empty" data-testid="gfn-empty">
      <ChartColumn size={28} />
      <b>Nenhum Grafana ou Prometheus encontrado neste cluster</b>
      <p>O Leme procura Services com o label <code>app.kubernetes.io/name=grafana</code> (ou <code>app=grafana</code>) e porta 3000 ou chamada <code>http</code>, e Services de Prometheus/Thanos (porta 9090, <code>web</code> ou <code>http</code>).</p>
      <p>Para instalar, use os charts <code>grafana/grafana</code> ou <code>prometheus-community/kube-prometheus-stack</code> (tela Charts). Se já estão instalados, confira os labels e a porta do Service, ou se você tem permissão para listar Services.</p>
      <p className="muted">Nada é exposto para fora: a interface abre por um port-forward local em 127.0.0.1.</p>
    </div>
  )
}

/** Grafana / Prometheus UIs through a local port-forward — M5, feature grafana. */
export function GrafanaPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  const { clusterId } = params
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const { data, error, loading, reload } = useAsync(() => grafanaApi.targets(clusterId), [clusterId])
  return (
    <ClusterGate clusterId={clusterId}>
      <div className="object-panel gfn-panel" data-testid="grafana-panel">
        <PanelHeader icon={ChartColumn} title="Grafana e Prometheus" actions={[refreshAction(reload, loading)]} />
        <ErrorBanner error={error} />
        <div className="obj-body">
          <div className="gfn-note muted">
            {embedInApp
              ? 'A interface abre numa janela isolada do app (sem acesso ao Leme, sessão em memória), por um port-forward local.'
              : 'No navegador, a interface abre numa nova aba, por um port-forward local.'}{' '}
            O Leme nunca preenche o login nem mostra senhas.
          </div>
          {readOnly && <div className="banner warn gfn-ro">Cluster em somente leitura: abrir um port-forward exige liberar a escrita (o túnel ignora a trava).</div>}
          {data && data.length === 0 && <EmptyState />}
          {data && data.length > 0 && (
            <div className="gfn-grid">
              {data.map((t) => <TargetCard key={`${t.kind}-${keyOf(t)}-${t.port}`} clusterId={clusterId} t={t} readOnly={readOnly} onChange={reload} />)}
            </div>
          )}
          {!data && loading && <div className="pad"><span className="spinner" /></div>}
        </div>
      </div>
    </ClusterGate>
  )
}
