import { call, qs } from '../../api'

export type MTLSMode = 'STRICT' | 'PERMISSIVE' | 'DISABLE' | 'UNSET'

export interface PortMode { port: number; mode: MTLSMode; effective: MTLSMode }
export interface MTLSException { policy: string; selector: Record<string, string>; mode: MTLSMode; effective: MTLSMode; ports?: PortMode[] }
export interface NamespaceMTLS {
  name: string
  mtls: MTLSMode
  source: 'namespace' | 'mesh' | 'default'
  policy?: string
  exceptions: MTLSException[]
  injection: string
}
export interface ServiceRef { namespace?: string; name?: string; found: boolean }
export interface Destination { host: string; subset?: string; port?: number; weight: number; service: ServiceRef }
export interface IstioRoute { name?: string; match: string[]; destinations: Destination[]; notes?: string[] }
export interface VirtualService { namespace: string; name: string; hosts: string[]; gateways: string[]; http: IstioRoute[]; tcp: IstioRoute[]; tls: IstioRoute[] }
export interface Subset { name: string; labels: Record<string, string>; tlsMode?: string }
export interface DestinationRule { namespace: string; name: string; host: string; service: ServiceRef; subsets: Subset[]; tlsMode?: string }
export interface GatewayServer { port: number; name?: string; protocol: string; hosts: string[]; tlsMode?: string }
export interface Gateway { namespace: string; name: string; selector: Record<string, string>; servers: GatewayServer[] }
export interface IstioReport {
  installed: boolean
  kinds: string[]
  rootNamespace: string
  namespaces: NamespaceMTLS[]
  virtualServices: VirtualService[]
  destinationRules: DestinationRule[]
  gateways: Gateway[]
  errors?: Record<string, string>
}
export interface AnalyzeMessage { code: string; level: string; message: string; resource: string; documentationUrl?: string }
export interface AnalyzeResult { installed: boolean; hint?: string; messages?: AnalyzeMessage[]; errors?: Record<string, string> }
export interface IstioTool { installed: boolean; path?: string; hint?: string }

const base = (id: string) => `/api/clusters/${encodeURIComponent(id)}/istio`

export const istioApi = {
  report: (clusterId: string, namespace: string) => call<IstioReport>('GET', `${base(clusterId)}?${qs({ namespace })}`),
  tool: () => call<IstioTool>('GET', '/api/istio/tool'),
  analyze: (clusterId: string, namespace: string) => call<AnalyzeResult>('POST', `${base(clusterId)}/analyze`, { namespace }),
}
