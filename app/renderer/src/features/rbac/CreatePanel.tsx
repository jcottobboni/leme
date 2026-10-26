import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api } from '../../api'
import { useApp } from '../../store'
import { YamlEditor } from '../../components/Monaco'
import { openObject } from '../../components/ObjectPanel'
import { ClusterGate } from '../common'
import { Review, type ReviewState } from '../helm/Review'
import { TEMPLATES, templateYAML } from './templates'

interface Params {
  clusterId: string
  template: string
  namespace: string
}

function Create({ clusterId, template, namespace }: Params & { onDone: () => void }) {
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const toast = useApp((s) => s.toast)
  const [tpl, setTpl] = useState(template)
  const [yaml, setYaml] = useState(() => templateYAML(template, namespace))
  const [review, setReview] = useState<ReviewState>()

  const preview = async () => {
    try {
      const res = await api.apply(clusterId, yaml, true)
      setReview({
        title: res.created ? 'Criar recurso' : 'Atualizar recurso existente',
        previous: '',
        next: res.yaml,
        confirmLabel: res.created ? 'Criar' : 'Atualizar',
        onConfirm: async () => {
          try {
            const out = await api.apply(clusterId, yaml, false)
            const m = /^kind:\s*(\S+)/m.exec(out.yaml)
            const name = /^ {2}name:\s*(\S+)/m.exec(out.yaml)
            const ns = /^ {2}namespace:\s*(\S+)/m.exec(out.yaml)
            const api_ = /^apiVersion:\s*(\S+)/m.exec(out.yaml)
            toast('ok', `${m?.[1] ?? 'Recurso'} ${name?.[1] ?? ''} ${out.created ? 'criado' : 'atualizado'}`)
            if (m && name && api_) openObject(clusterId, api_[1].includes('/') ? api_[1].split('/')[0] : '', m[1], name[1], ns?.[1])
          } catch (e) {
            toast('error', `Apply falhou: ${(e as Error).message}`)
          }
        },
      })
    } catch (e) {
      toast('error', `Dry-run falhou: ${(e as Error).message}`)
    }
  }

  if (review) return <Review r={review} onClose={() => setReview(undefined)} />
  return (
    <div className="yaml-tab">
      <div className="toolbar">
        <select value={tpl} onChange={(e) => { setTpl(e.target.value); setYaml(templateYAML(e.target.value, namespace)) }}>
          {TEMPLATES.map((t) => <option key={t.id} value={t.id}>{t.group}: {t.label}</option>)}
        </select>
        <button className="primary" disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : ''} onClick={preview}>Revisar (dry-run)</button>
        {readOnly && <span className="muted">cluster em somente leitura</span>}
      </div>
      <YamlEditor value={yaml} onChange={setYaml} />
    </div>
  )
}

export function CreatePanel({ params, api: panelApi }: IDockviewPanelProps<Params>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Create {...params} onDone={() => panelApi.close()} />
    </ClusterGate>
  )
}
