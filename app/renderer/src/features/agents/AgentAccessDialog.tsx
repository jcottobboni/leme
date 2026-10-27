import type { ClusterInfo } from '../../api'
import { Modal } from '../../components/Dialogs'

/** Per-context write access for MCP agents (M4, group G2). */
export function AgentAccessDialog({ c, onClose }: { c: ClusterInfo; onClose: () => void }) {
  return (
    <Modal title={`Acesso de agentes (MCP) — ${c.name}`} onClose={onClose}>
      <div className="muted">Em construção.</div>
    </Modal>
  )
}
