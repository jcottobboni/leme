// Overlays of the cluster map: legend and the traffic note.
import { Activity, Globe, HardDrive, Network, RotateCcw } from 'lucide-react'
import { kindIcon } from '../shell/nav'
import type { useTrafficEdges } from '../map-webgl/trafficEdges'
import { hexPath } from './geometry'
import { kindTag } from './SvgStage'

export function Legend({ onClose }: { onClose: () => void }) {
  const kinds = ['Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job', 'Pod']
  const hex = (cls: string) => <svg width="16" height="18" viewBox="-16 -18 32 36"><path className={`lg-hex ${cls}`} d={hexPath(14)} /></svg>
  return (
    <div className="map-legend" data-testid="map-legend" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
      <div className="lg-head"><b>Legenda</b><button onClick={onClose} aria-label="Fechar legenda">×</button></div>
      <div className="lg-sec">
        <div className="lg-row"><span className="lg-isl" />Namespace (ilha)</div>
        <div className="lg-row"><span className="lg-app" />App (região tracejada)</div>
        <div className="lg-row">{hex('ok')}Workload saudável</div>
        <div className="lg-row">{hex('warn')}Workload com atenção</div>
        <div className="lg-row">{hex('error')}Workload com erro</div>
        <div className="lg-row"><span className="lg-pods"><i className="ok" /><i className="warn" /><i className="error" /></span>Pods (favos dentro do workload)</div>
      </div>
      <div className="lg-sec lg-kinds">
        {kinds.map((k) => { const I = kindIcon(k); return <span key={k}><I size={12} /><b>{kindTag(k)}</b>{k}</span> })}
      </div>
      <div className="lg-sec lg-kinds">
        <span><Network size={12} />Services</span><span><Globe size={12} />Hosts de Ingress</span>
        <span><HardDrive size={12} />PVCs</span><span><RotateCcw size={12} />Reinícios</span>
      </div>
    </div>
  )
}

export function TrafficNote({ q, dropped, shown }: { q: ReturnType<typeof useTrafficEdges>; dropped: number; shown: number }) {
  const d = q.data
  let body: React.ReactNode
  if (q.error) body = <span>Tráfego: {q.error}</span>
  else if (q.loading && !d.available && !d.hint) body = <span>Lendo tráfego observado…</span>
  else if (!d.available) {
    body = (
      <span>
        Tráfego observado indisponível. <span className="mgl-sub">{d.hint || 'Precisa do Hubble (Cilium) ou do Caretta com o Prometheus do cluster configurado. Nada é instalado pelo Leme.'}</span>
      </span>
    )
  } else {
    const src = d.source === 'hubble' ? 'Hubble' : d.source === 'caretta' ? 'Caretta' : d.source
    body = (
      <span>
        {shown ? `${shown} conexão(ões)` : 'Nenhuma conexão observada'}{src ? ` · ${src}` : ''}{d.window ? ` · janela ${d.window}` : ''}
        {!!dropped && <span className="mgl-sub"> · {dropped} fora do mapa (nós, externos ou namespaces filtrados)</span>}
        <span className="mgl-sub"> · espessura = volume, seta = direção</span>
      </span>
    )
  }
  return <div className="mgl-note" data-testid="map-traffic-note"><Activity size={14} />{body}</div>
}
