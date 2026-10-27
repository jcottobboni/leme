import { lazy, Suspense } from 'react'

// Monaco (and its worker) is loaded the first time a YAML editor or diff is shown, never in the initial bundle.
// Same API as before: callers import YamlEditor / YamlDiff from here.
const load = () => import('./MonacoEditor')
const LazyEditor = lazy(() => load().then((m) => ({ default: m.YamlEditorImpl })))
const LazyDiff = lazy(() => load().then((m) => ({ default: m.YamlDiffImpl })))

function Placeholder() {
  return (
    <div className="monaco monaco-loading" role="status" aria-label="Carregando editor">
      <span className="spinner" />
    </div>
  )
}

export function YamlEditor(props: { value: string; readOnly?: boolean; onChange?: (v: string) => void }) {
  return (
    <Suspense fallback={<Placeholder />}>
      <LazyEditor {...props} />
    </Suspense>
  )
}

export function YamlDiff(props: { original: string; modified: string }) {
  return (
    <Suspense fallback={<Placeholder />}>
      <LazyDiff {...props} />
    </Suspense>
  )
}

/** Starts downloading Monaco ahead of time (e.g. on hover of an "Editar YAML" button). */
export function preloadMonaco() {
  void load()
}
