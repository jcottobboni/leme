import { memo, useEffect, useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { Background, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { api3, type NetAnalysis, type NetCheck, type NetEndpoint } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { ClusterGate, ErrorBanner, Partial, Tabs, useAsync } from '../common'
import { layout, NODE_H, NODE_W } from '../topology/layout'

type Tab = 'graph' | 'pods' | 'policies' | 'simulate'

export function openNetpol(clusterId: string, opts?: OpenOptions) {
  openPanel(`netpol:${clusterId}`, 'netpol', 'NetworkPolicy', { clusterId }, opts)
}

type NData = { label: string; kind: 'group' | 'peer'; open?: boolean; isolated?: boolean; pods?: number }

const NetNode = memo(function NetNode({ data }: NodeProps<Node<NData>>) {
  const cls = data.kind === 'peer' ? 'peer' : data.isolated ? 'h-ok' : 'h-warn'
  return (
    <div className={`topo-node net-node ${cls}`} style={{ width: NODE_W, height: NODE_H }} title={data.label} data-testid={`net-${data.label}`}>
      <Handle type="target" position={Position.Left} />
      <div className="topo-kind">{data.kind === 'peer' ? 'origem' : data.isolated ? 'isolado (ingress)' : 'aberto'}</div>
      <div className="topo-name">{data.label}</div>
      {data.pods !== undefined && <div className="topo-status">{data.pods} pod(s)</div>}
      <Handle type="source" position={Position.Right} />
    </div>
  )
})
const nodeTypes = { net: NetNode }

function FlowGraph({ a }: { a: NetAnalysis }) {
  const [pos, setPos] = useState<Map<string, { x: number; y: number }>>()
  const ids = useMemo(() => [...a.groups.map((g) => g.id), ...a.peers.map((p) => p.id)], [a])
  useEffect(() => {
    let stop = false
    const g = { nodes: ids.map((id) => ({ id, group: '', kind: '', name: id, health: 'neutral' as const })), edges: a.flows.map((f) => ({ from: f.from, to: f.to, rel: f.ports })) }
    layout(g).then((p) => !stop && setPos(p))
    return () => {
      stop = true
    }
  }, [a, ids])
  if (!pos) return <div className="pad"><span className="spinner" /></div>
  const nodes: Node<NData>[] = [
    ...a.groups.map((g) => ({ id: g.id, type: 'net', position: pos.get(g.id) ?? { x: 0, y: 0 }, data: { label: g.label, kind: 'group' as const, isolated: g.ingressIsolated, pods: g.pods.length } })),
    ...a.peers.map((p) => ({ id: p.id, type: 'net', position: pos.get(p.id) ?? { x: 0, y: 0 }, data: { label: p.label, kind: 'peer' as const } })),
  ]
  const edges: Edge[] = a.flows.map((f, i) => ({
    id: `f${i}`, source: f.from, target: f.to, label: f.ports === 'todas as portas' ? undefined : f.ports,
    className: f.from === 'any' ? 'rel-open' : 'rel-allowed', markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14 },
    labelStyle: { fill: 'var(--muted)', fontSize: 10 }, labelBgStyle: { fill: 'var(--panel)' },
  }))
  return (
    <div className="flow" data-testid="netpol-graph">
      <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} fitView fitViewOptions={{ maxZoom: 1 }} minZoom={0.1} nodesConnectable={false} proOptions={{ hideAttribution: true }} colorMode="dark">
        <Background gap={24} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}

// Outside Simulator so inputs keep focus between renders.
function EndpointPicker({ v, set, label, testid, ns, pods }: { v: NetEndpoint; set: (e: NetEndpoint) => void; label: string; testid: string; ns: string; pods: string[] }) {
  return (
    <fieldset className="ep">
      <legend>{label}</legend>
      <label className="field">Namespace<input value={v.namespace ?? ''} onChange={(e) => set({ ...v, namespace: e.target.value })} style={{ width: 130 }} /></label>
      <label className="field">Pod
        {v.namespace === ns ? (
          <select value={v.pod ?? ''} onChange={(e) => set({ ...v, pod: e.target.value, ip: undefined })} data-testid={`${testid}-pod`}>
            <option value="">(IP externo)</option>
            {pods.map((p) => <option key={p}>{p}</option>)}
          </select>
        ) : (
          <input value={v.pod ?? ''} onChange={(e) => set({ ...v, pod: e.target.value })} />
        )}
      </label>
      {!v.pod && <label className="field">IP<input value={v.ip ?? ''} onChange={(e) => set({ ...v, ip: e.target.value })} placeholder="10.1.2.3" style={{ width: 120 }} /></label>}
    </fieldset>
  )
}

function Simulator({ clusterId, a }: { clusterId: string; a: NetAnalysis }) {
  const pods = a.groups.flatMap((g) => g.pods).sort()
  const [src, setSrc] = useState<NetEndpoint>({ namespace: a.namespace, pod: pods[0] })
  const [dst, setDst] = useState<NetEndpoint>({ namespace: a.namespace, pod: pods[1] ?? pods[0] })
  const [port, setPort] = useState(80)
  const [res, setRes] = useState<NetCheck>()
  const [err, setErr] = useState<string>()
  return (
    <div className="pad-panel">
      <p className="small muted">Avalia as NetworkPolicies como a API define (egress na origem e ingress no destino). Não considera o CNI, tráfego do node nem AdminNetworkPolicy.</p>
      <div className="row">
        <EndpointPicker v={src} set={setSrc} label="Origem" testid="src" ns={a.namespace} pods={pods} />
        <EndpointPicker v={dst} set={setDst} label="Destino" testid="dst" ns={a.namespace} pods={pods} />
        <label className="field">Porta<input type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} style={{ width: 80 }} data-testid="sim-port" /></label>
        <button className="primary" onClick={() => api3.netpolCheck(clusterId, src, dst, port).then((r) => (setErr(undefined), setRes(r.result)), (e) => setErr(e.message))} data-testid="sim-run">Testar</button>
      </div>
      <ErrorBanner error={err} />
      {res && (
        <div className={`answer ${res.allowed ? 'yes' : 'no'}`} data-testid="sim-answer">
          <b>{res.allowed ? 'Permitido' : 'Bloqueado'}</b> — {res.reason}
          <div className="small muted">
            Saída: {res.egress.isolated ? `isolada por ${res.egress.policies.join(', ')}` : 'sem política'} · Entrada: {res.ingress.isolated ? `isolada por ${res.ingress.policies.join(', ')}` : 'sem política'}
          </div>
        </div>
      )}
    </div>
  )
}

function Netpol({ clusterId }: { clusterId: string }) {
  const selected = useApp((s) => s.namespace[clusterId] ?? '')
  const fallback = useApp((s) => s.connected[clusterId]?.defaultNamespace || 'default')
  const ns = selected || fallback
  const [tab, setTab] = useState<Tab>('graph')
  const [rev, setRev] = useState(0)
  const { data, error, loading } = useAsync(() => api3.netpol(clusterId, ns), [clusterId, ns, rev])
  const a = data?.analysis
  const open = a?.groups.filter((g) => !g.ingressIsolated).length ?? 0
  return (
    <div className="object-panel">
      <div className="toolbar">
        <NamespaceSelect clusterId={clusterId} effective={ns} />
        {a && <span className={open ? 's-warn' : 's-ok'}>{a.policies.length} políticas · {open} de {a.groups.length} workloads abertos para entrada</span>}
        {loading && <span className="spinner" />}
        <span className="spacer" />
        <button onClick={() => setRev((r) => r + 1)}>Recarregar</button>
      </div>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      <Tabs<Tab> tabs={[['graph', 'Fluxos permitidos'], ['pods', 'Workloads'], ['policies', 'Políticas'], ['simulate', 'Simulador']]} value={tab} onChange={setTab} />
      <div className={`obj-body ${tab === 'graph' ? 'flex-col' : ''}`}>
        {a && tab === 'graph' && (a.groups.length ? <FlowGraph a={a} /> : <div className="empty">Nenhum workload com pods em <b>{ns}</b>. Escolha outro namespace.</div>)}
        {a && tab === 'pods' && (
          <table className="table" data-testid="netpol-groups">
            <thead><tr><th>Workload</th><th>Pods</th><th>Entrada</th><th>Saída</th></tr></thead>
            <tbody>
              {a.groups.map((g) => (
                <tr key={g.id}>
                  <td>{g.label} <span className="muted small">{g.id}</span></td>
                  <td>{g.pods.length}</td>
                  <td className={g.ingressIsolated ? 's-ok' : 's-warn'}>{g.ingressIsolated ? `isolada (${g.ingressPolicies.join(', ')})` : 'aberta'}</td>
                  <td className={g.egressIsolated ? 's-ok' : 'muted'}>{g.egressIsolated ? `isolada (${g.egressPolicies.join(', ')})` : 'aberta'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {a && tab === 'policies' && (
          <div className="overview">
            {!a.policies.length && <div className="muted">Nenhuma NetworkPolicy neste namespace: todo pod aceita conexões de qualquer lugar.</div>}
            {a.policies.map((p) => (
              <section className="card" key={p.name}>
                <h4>{p.name} <span className="muted">· seleciona {p.selector} · {p.types.join(' + ')}</span></h4>
                {p.ingress.length > 0 && <div><b>Entrada:</b><ul>{p.ingress.map((x, i) => <li key={i}>{x}</li>)}</ul></div>}
                {p.egress.length > 0 && <div><b>Saída:</b><ul>{p.egress.map((x, i) => <li key={i}>{x}</li>)}</ul></div>}
                <div className="small muted">Afeta: {p.selects.join(', ') || 'nenhum pod em execução'}</div>
              </section>
            ))}
          </div>
        )}
        {a && tab === 'simulate' && <Simulator clusterId={clusterId} a={a} />}
      </div>
    </div>
  )
}

export function NetpolPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Netpol clusterId={params.clusterId} />
    </ClusterGate>
  )
}
