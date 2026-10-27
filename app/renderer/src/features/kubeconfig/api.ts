import { call } from '../../api'

export interface KubeconfigSource {
  path: string
  kind: 'file' | 'dir' | 'missing'
  files: string[]
}

export interface KubeconfigSettings {
  base: string[]
  sources: KubeconfigSource[]
}

export const getKubeconfigs = () => call<KubeconfigSettings>('GET', '/api/kubeconfigs')

export const setKubeconfigs = (paths: string[]) => call<KubeconfigSettings>('PUT', '/api/kubeconfigs', { paths })

interface PickBridge {
  pick(kind: 'file' | 'dir'): Promise<string[]>
}

/** Native picker of the desktop app; undefined in the browser (serve mode), where paths are typed. */
export const pickBridge = (window as unknown as { leme?: { kubeconfig?: PickBridge } }).leme?.kubeconfig
