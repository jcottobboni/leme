import { Component, type ErrorInfo, type ReactNode } from 'react'
import { RotateCw, TriangleAlert } from 'lucide-react'

/**
 * Keeps a rendering error inside the panel (or dialog) that threw it. Without it, any exception
 * unmounts the whole React tree and the window goes blank.
 */
export class ErrorBoundary extends Component<{ children: ReactNode; label?: string; full?: boolean }, { error?: Error }> {
  state: { error?: Error } = {}

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`[Leme] erro em ${this.props.label ?? 'painel'}`, error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    return (
      <div className={`error-boundary${this.props.full ? ' full' : ''}`} role="alert" data-testid="error-boundary">
        <TriangleAlert size={22} />
        <b>{this.props.label ? `Erro em ${this.props.label}` : 'Este painel encontrou um erro'}</b>
        <code>{error.message}</code>
        <div className="row">
          <button onClick={() => this.setState({ error: undefined })}><RotateCw size={14} /> Tentar de novo</button>
          {this.props.full && <button onClick={() => location.reload()}>Recarregar a janela</button>}
        </div>
      </div>
    )
  }
}
