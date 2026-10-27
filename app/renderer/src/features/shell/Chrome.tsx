import { useEffect, useState } from 'react'
import { create } from 'zustand'
import { ArrowLeft, ArrowRight, Command, Keyboard, ShieldCheck, Lock, LockOpen, Folder, Monitor, Moon, PanelLeft, Plug, Server, RefreshCw, Search, Sun, Waypoints } from 'lucide-react'
import { Logo } from './Logo'
import { cycleTheme } from './theme'
import { api } from '../../api'
import { identityOf, openPanel, reopenClosed, useApp, useClusterColor, useProfiles, useUi } from '../../store'
import { ClusterCards } from './Sidebar'

// Back/forward over the tabs the user visited, like a browser.
const useHistory = create<{ stack: string[]; pos: number }>(() => ({ stack: [], pos: -1 }))
let navigating = false

export function trackActive(id?: string) {
  useApp.setState({ activePanelId: id })
  if (!id || navigating) return
  useHistory.setState((h) => {
    if (h.stack[h.pos] === id) return h
    const stack = [...h.stack.slice(0, h.pos + 1), id].slice(-50)
    return { stack, pos: stack.length - 1 }
  })
}

function go(delta: number) {
  const dock = useApp.getState().dock
  const h = useHistory.getState()
  let pos = h.pos + delta
  // Skip tabs that were closed in the meantime.
  while (pos >= 0 && pos < h.stack.length && !dock?.getPanel(h.stack[pos])) pos += delta
  if (pos < 0 || pos >= h.stack.length) return
  useHistory.setState({ pos })
  navigating = true
  dock?.getPanel(h.stack[pos])?.api.setActive()
  navigating = false
}

export function TopBar({ onSearch }: { onSearch: () => void }) {
  const { stack, pos } = useHistory()
  const hidden = useUi((s) => s.sidebarHidden)
  const theme = useUi((s) => s.theme)
  const ThemeIcon = { system: Monitor, dark: Moon, light: Sun }[theme]
  return (
    <header className="topbar">
      <div className="brand"><Logo /> Leme</div>
      <button className="icon-btn" title={`${hidden ? 'Mostrar' : 'Ocultar'} barra lateral (Ctrl+B)`} onClick={() => useUi.getState().update({ sidebarHidden: !hidden })}>
        <PanelLeft size={16} />
      </button>
      <button className="icon-btn" title="Voltar (Alt+←)" disabled={pos <= 0} onClick={() => go(-1)}><ArrowLeft size={16} /></button>
      <button className="icon-btn" title="Avançar (Alt+→)" disabled={pos >= stack.length - 1} onClick={() => go(1)}><ArrowRight size={16} /></button>
      <button className="top-search" onClick={onSearch} data-testid="open-search">
        <Search size={14} />
        <span>Buscar recursos, objetos e telas…</span>
        <kbd>Ctrl</kbd><kbd>K</kbd>
      </button>
      <div className="top-actions">
        <button className="icon-btn" title="Port-forwards" onClick={() => openPanel('forwards', 'forwards', 'Port-forwards', {})}><Waypoints size={16} /></button>
        <button className="icon-btn" title="Recarregar kubeconfig" onClick={() => useApp.getState().loadClusters()}><RefreshCw size={16} /></button>
        <button className="icon-btn" data-testid="theme-toggle" title={`Tema: ${{ system: 'do sistema', dark: 'escuro', light: 'claro' }[theme]} (clique para trocar)`} onClick={cycleTheme}>
          <ThemeIcon size={16} />
        </button>
      </div>
    </header>
  )
}

export function StatusBar() {
  const activeId = useApp((s) => s.activeCluster)
  const c = useApp((s) => s.clusters.find((x) => x.id === s.activeCluster))
  const st = useApp((s) => (s.activeCluster ? s.connected[s.activeCluster] : undefined))
  const ns = useApp((s) => (s.activeCluster ? s.namespace[s.activeCluster] : undefined))
  const nConnected = useApp((s) => Object.keys(s.connected).length)
  const color = useClusterColor(activeId)
  const profile = useProfiles((s) => (activeId ? s.profiles[activeId] : undefined))
  const ident = c && identityOf(c.id, c.name, profile, c.prod)
  const clusterLabel = ident?.name
  const clusterShort = ident?.icon || ident?.short
  const [forwards, setForwards] = useState(0)
  useEffect(() => {
    let stop = false
    const poll = () => api.forwards().then((f) => !stop && setForwards(f.length), () => {})
    poll()
    const t = setInterval(poll, 5000)
    return () => {
      stop = true
      clearInterval(t)
    }
  }, [])
  return (
    <footer className="statusbar">
      {c ? (
        <span className="sb-cluster" style={{ '--cc': color } as React.CSSProperties} title={`${c.name}\n${c.server}`}>
          <Server size={12} /> <b className="sb-short">{clusterShort}</b> {clusterLabel}
        </span>
      ) : (
        <span className="sb-cluster none">Nenhum cluster selecionado</span>
      )}
      {c?.prod && <span className="sb-item sb-prod">PRODUÇÃO</span>}
      {st && <span className="sb-item">{st.version}</span>}
      {st && (
        <span className={`sb-item ${st.readOnly ? '' : 'sb-rw'}`} data-testid="sb-rw">
          {st.readOnly ? <Lock size={11} /> : <LockOpen size={11} />} {st.readOnly ? 'somente leitura' : 'leitura e escrita'}
        </span>
      )}
      {st && <span className="sb-item"><Folder size={11} /> ns: {ns || 'todos'}</span>}
      <div className="spacer" />
      <span className="sb-item"><Plug size={11} /> {nConnected} conectado{nConnected === 1 ? '' : 's'}</span>
      <button className="sb-btn" onClick={() => openPanel('forwards', 'forwards', 'Port-forwards', {})}>
        <Waypoints size={11} /> {forwards} port-forward{forwards === 1 ? '' : 's'}
      </button>
      <span className="sb-item"><Command size={11} /> Ctrl+K</span>
      <span className="sb-item">Leme {__APP_VERSION__}</span>
    </footer>
  )
}

/** Home tab: cluster catalog as cards, plus the safety rules and shortcuts. */
export function Home() {
  const n = useApp((s) => s.clusters.length)
  const nConnected = useApp((s) => Object.keys(s.connected).length)
  const prod = useApp((s) => s.clusters.filter((c) => c.prod).length)
  return (
    <div className="home">
      <div className="home-hero">
        <Logo />
        <div>
          <h2>Clusters</h2>
          <p>{n} contextos no kubeconfig · {nConnected} conectado{nConnected === 1 ? '' : 's'} · conexão sempre manual</p>
        </div>
      </div>
      <ClusterCards />
      <div className="home-tips">
        <div className="card">
          <h4><ShieldCheck size={14} /> Segurança</h4>
          <ul>
            <li>Clusters remotos abrem em <b>somente leitura</b>; só os locais (kind, minikube…) abrem com escrita.</li>
            {prod > 0 && <li><span className="badge badge-prod">PROD</span> {prod} contexto(s) de produção: para liberar escrita é preciso digitar o nome.</li>}
            <li>OpenShift: “Login OpenShift” no menu do cluster renova o token sem sair do app.</li>
          </ul>
        </div>
        <div className="card">
          <h4><Keyboard size={14} /> Atalhos</h4>
          <ul className="keys">
            <li><kbd>Ctrl</kbd> <kbd>K</kbd> busca em todos os clusters conectados</li>
            <li><kbd>Ctrl</kbd> <kbd>W</kbd> fecha a aba · <kbd>Ctrl</kbd> <kbd>Shift</kbd> <kbd>T</kbd> reabre</li>
            <li><kbd>Ctrl</kbd> <kbd>Tab</kbd> próxima aba · <kbd>Ctrl</kbd> <kbd>B</kbd> barra lateral</li>
            <li>Na lista: <kbd>↑</kbd> <kbd>↓</kbd> navegam no painel de detalhes, <kbd>Esc</kbd> fecha.</li>
            <li>Clique abre em <i>aba temporária</i>; duplo clique mantém a aba aberta.</li>
          </ul>
        </div>
      </div>
    </div>
  )
}

/** Global keyboard shortcuts. Ctrl+W is left to the shell when the terminal has focus. */
export function useShortcuts(onPalette: () => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const dock = useApp.getState().dock
      const ctrl = e.ctrlKey || e.metaKey
      const k = e.key.toLowerCase()
      const inTerminal = !!(e.target as HTMLElement | null)?.closest?.('.xterm')
      if (ctrl && !e.shiftKey && k === 'k') {
        e.preventDefault()
        onPalette()
      } else if (ctrl && !e.shiftKey && k === 'b') {
        e.preventDefault()
        useUi.getState().update({ sidebarHidden: !useUi.getState().sidebarHidden })
      } else if (ctrl && !e.shiftKey && k === 'w' && !inTerminal) {
        e.preventDefault()
        dock?.activePanel?.api.close()
      } else if (ctrl && e.shiftKey && k === 't' && !inTerminal) {
        e.preventDefault()
        reopenClosed()
      } else if (ctrl && (e.key === 'Tab' || e.key === 'PageDown' || e.key === 'PageUp')) {
        const group = dock?.activeGroup
        if (!group || group.panels.length < 2) return
        e.preventDefault()
        const back = e.key === 'PageUp' || (e.key === 'Tab' && e.shiftKey)
        const i = group.panels.indexOf(group.activePanel!)
        group.panels[(i + (back ? -1 : 1) + group.panels.length) % group.panels.length].api.setActive()
      } else if (e.altKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight') && !inTerminal && !(e.target as HTMLElement)?.closest?.('input,textarea,.monaco-editor')) {
        e.preventDefault()
        go(e.key === 'ArrowLeft' ? -1 : 1)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onPalette])
}
