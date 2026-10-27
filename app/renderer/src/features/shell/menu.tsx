import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { create } from 'zustand'
import { Bot, LogIn, LogOut, Lock, LockOpen, type LucideIcon, Pencil, Pin, PinOff, Plug, Unplug } from 'lucide-react'
import { api3, type ClusterInfo } from '../../api'
import { useApp, useUi } from '../../store'
import { UnlockDialog } from '../../components/Dialogs'
import { LoginDialog } from '../openshift/LoginDialog'
import { ClusterProfileDialog } from './ClusterProfile'
import { AgentAccessDialog } from '../agents/AgentAccessDialog'

export interface MenuItem {
  label?: string
  icon?: LucideIcon
  hint?: string
  danger?: boolean
  disabled?: boolean
  separator?: boolean
  onClick?: () => void
}

const useMenu = create<{ menu?: { x: number; y: number; items: MenuItem[] } }>(() => ({}))

/** Opens a context menu at the mouse position. */
export function openMenu(e: { clientX: number; clientY: number; preventDefault: () => void; stopPropagation: () => void }, items: MenuItem[]) {
  e.preventDefault()
  e.stopPropagation()
  useMenu.setState({ menu: { x: e.clientX, y: e.clientY, items } })
}

export function ContextMenuHost() {
  const menu = useMenu((s) => s.menu)
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number }>()
  const close = () => useMenu.setState({ menu: undefined })
  useEffect(() => {
    if (!menu) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    const onDown = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close()
    window.addEventListener('keydown', onKey)
    window.addEventListener('mousedown', onDown, true)
    window.addEventListener('blur', close)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onDown, true)
      window.removeEventListener('blur', close)
    }
  }, [menu])
  // Keep the menu inside the window.
  useLayoutEffect(() => {
    if (!menu || !ref.current) return setPos(undefined)
    const r = ref.current.getBoundingClientRect()
    setPos({ left: Math.min(menu.x, window.innerWidth - r.width - 4), top: Math.min(menu.y, window.innerHeight - r.height - 4) })
  }, [menu])
  if (!menu) return null
  return (
    <div ref={ref} className="ctx-menu" role="menu" style={pos ?? { left: menu.x, top: menu.y, visibility: 'hidden' }}>
      {menu.items.map((it, i) =>
        it.separator ? (
          <div key={i} className="ctx-sep" />
        ) : (
          <button
            key={i}
            role="menuitem"
            className={`ctx-item ${it.danger ? 'danger' : ''}`}
            disabled={it.disabled}
            onClick={() => {
              close()
              it.onClick?.()
            }}
          >
            <span className="ctx-icon">{it.icon && <it.icon size={14} />}</span>
            <span className="ctx-label">{it.label}</span>
            {it.hint && <span className="ctx-hint">{it.hint}</span>}
          </button>
        ),
      )}
    </div>
  )
}

// Cluster dialogs live at the app root so the hotbar, the navigator and the catalog can open them.
const useDialogs = create<{ unlock?: ClusterInfo; login?: ClusterInfo; profile?: ClusterInfo; agents?: ClusterInfo }>(() => ({}))

export function DialogHost() {
  const { unlock, login, profile, agents } = useDialogs()
  return (
    <>
      {profile && <ClusterProfileDialog c={profile} onClose={() => useDialogs.setState({ profile: undefined })} />}
      {agents && <AgentAccessDialog c={agents} onClose={() => useDialogs.setState({ agents: undefined })} />}
      {login && <LoginDialog clusterId={login.id} name={login.name} onClose={() => useDialogs.setState({ login: undefined })} />}
      {unlock && (
        <UnlockDialog
          contextName={unlock.name}
          onCancel={() => useDialogs.setState({ unlock: undefined })}
          onConfirm={async (typed) => {
            try {
              await useApp.getState().setReadOnly(unlock.id, false, typed)
              useDialogs.setState({ unlock: undefined })
            } catch (e) {
              useApp.getState().toast('error', (e as Error).message)
            }
          }}
        />
      )}
    </>
  )
}

export function openLogin(c: ClusterInfo) {
  useDialogs.setState({ login: c })
}

/** Leaves read-only mode; production contexts require typing the context name. */
export function unlockWrites(c: ClusterInfo) {
  if (c.prod) useDialogs.setState({ unlock: c })
  else useApp.getState().setReadOnly(c.id, false).catch((e) => useApp.getState().toast('error', (e as Error).message))
}

export function togglePin(id: string) {
  const { pinned, update } = useUi.getState()
  update({ pinned: pinned.includes(id) ? pinned.filter((p) => p !== id) : [...pinned, id] })
}

export function openProfile(c: ClusterInfo) {
  useDialogs.setState({ profile: c })
}

/** Actions of a cluster, shared by the hotbar, the navigator header and the catalog. */
export function clusterMenu(c: ClusterInfo): MenuItem[] {
  const s = useApp.getState()
  const st = s.connected[c.id]
  const pinned = useUi.getState().pinned.includes(c.id)
  const items: MenuItem[] = []
  if (st) {
    items.push(
      st.readOnly
        ? { label: c.prod ? 'Liberar escrita (PROD)…' : 'Liberar escrita', icon: LockOpen, onClick: () => unlockWrites(c) }
        : { label: 'Voltar para somente leitura', icon: Lock, onClick: () => s.setReadOnly(c.id, true) },
      { label: 'Desconectar', icon: Unplug, onClick: () => s.disconnect(c.id) },
    )
  } else {
    items.push({ label: 'Conectar', icon: Plug, disabled: s.connecting[c.id], onClick: () => s.connect(c.id) })
  }
  if (c.provider === 'openshift' || s.connectError[c.id]) items.push({ label: 'Login OpenShift…', icon: LogIn, onClick: () => openLogin(c) })
  if (c.inAppLogin) {
    items.push({
      label: 'Sair do login (usar kubeconfig)', icon: LogOut, onClick: async () => {
        await api3.oauthLogout(c.id)
        await s.disconnect(c.id)
        await s.loadClusters()
      },
    })
  }
  items.push(
    { separator: true },
    { label: pinned ? 'Remover da hotbar' : 'Fixar na hotbar', icon: pinned ? PinOff : Pin, onClick: () => togglePin(c.id) },
    { label: 'Personalizar…', icon: Pencil, hint: 'cor, sigla, nome', onClick: () => openProfile(c) },
    { label: 'Acesso de agentes (MCP)…', icon: Bot, onClick: () => useDialogs.setState({ agents: c }) },
  )
  return items
}
