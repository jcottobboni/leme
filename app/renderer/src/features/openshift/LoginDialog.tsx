import { useEffect, useState } from 'react'
import { api3 } from '../../api'
import { useApp } from '../../store'
import { Modal } from '../../components/Dialogs'

/**
 * Renews an OpenShift login without leaving the app. The token stays in the engine's memory for this
 * run only (the kubeconfig is not changed) and the password is never stored.
 */
export function LoginDialog({ clusterId, name, onClose }: { clusterId: string; name: string; onClose: () => void }) {
  const toast = useApp((s) => s.toast)
  const [info, setInfo] = useState<{ available: boolean; issuer?: string; insecure?: boolean; error?: string }>()
  const [mode, setMode] = useState<'password' | 'web'>('password')
  const [user, setUser] = useState('')
  const [pass, setPass] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()

  useEffect(() => {
    api3.oauthInfo(clusterId).then(setInfo, (e) => setInfo({ available: false, error: e.message }))
  }, [clusterId])

  const done = async (who: string) => {
    toast('ok', `Login como ${who} em ${name}`)
    onClose()
    await useApp.getState().disconnect(clusterId)
    await useApp.getState().loadClusters()
    await useApp.getState().connect(clusterId)
  }

  const cancel = () => {
    if (busy && mode === 'web') api3.oauthWebCancel(clusterId).catch(() => {})
    onClose()
  }

  const submit = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      if (mode === 'password') {
        const r = await api3.oauthPassword(clusterId, user, pass)
        setPass('')
        await done(r.user)
      } else {
        const { url } = await api3.oauthWebStart(clusterId)
        // Opens in the system browser (the app never navigates away from its own origin).
        window.open(url, '_blank', 'noopener')
        const r = await api3.oauthWebWait(clusterId)
        await done(r.user)
      }
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={`Login OpenShift — ${name}`} onClose={busy && mode === 'password' ? () => {} : cancel}>
      {!info && <span className="spinner" />}
      {info && !info.available && <div className="banner error">Login OpenShift indisponível: {info.error}</div>}
      {info?.available && (
        <>
          {info.insecure && <div className="banner warn">Este contexto usa insecure-skip-tls-verify: o certificado do servidor de login não será verificado.</div>}
          <p className="small muted">
            Servidor OAuth: {info.issuer}. O token fica só na memória do app até fechá-lo; o kubeconfig não é alterado e a senha não é guardada.
          </p>
          <div className="row">
            <label className="check"><input type="radio" checked={mode === 'password'} onChange={() => setMode('password')} /> usuário e senha</label>
            <label className="check"><input type="radio" checked={mode === 'web'} onChange={() => setMode('web')} /> pelo navegador (SSO)</label>
          </div>
          {mode === 'password' ? (
            <>
              <label className="field">Usuário<input className="input" autoFocus value={user} onChange={(e) => setUser(e.target.value)} autoComplete="username" /></label>
              <label className="field">Senha<input className="input" type="password" value={pass} onChange={(e) => setPass(e.target.value)} autoComplete="current-password" onKeyDown={(e) => e.key === 'Enter' && user && pass && submit()} /></label>
            </>
          ) : (
            <p className="small">Abre a página de login do cluster no seu navegador. Depois de entrar, volte para o app.</p>
          )}
          {err && <div className="banner error">{err}</div>}
          <div className="modal-actions">
            <button onClick={cancel} disabled={busy && mode === 'password'}>Cancelar</button>
            <button className="primary" disabled={busy || (mode === 'password' && (!user || !pass))} onClick={submit}>
              {busy ? (mode === 'web' ? 'Aguardando o navegador…' : 'Entrando…') : 'Entrar'}
            </button>
          </div>
        </>
      )}
    </Modal>
  )
}
