import { useEffect, useRef } from 'react'
import * as monaco from 'monaco-editor/editor/editor.api'
import 'monaco-editor/languages/definitions/yaml/register'
import { useTheme } from '../features/shell/theme'
import EditorWorker from 'monaco-editor/editor/editor.worker?worker'

;(self as unknown as { MonacoEnvironment: monaco.Environment }).MonacoEnvironment = { getWorker: () => new EditorWorker() }

// Editor themes matching the app tokens; they follow the app theme.
monaco.editor.defineTheme('k8s-dark', { base: 'vs-dark', inherit: true, rules: [], colors: { 'editor.background': '#14171b', 'editorGutter.background': '#14171b', 'editor.lineHighlightBackground': '#1c2026' } })
monaco.editor.defineTheme('k8s-light', { base: 'vs', inherit: true, rules: [], colors: { 'editor.background': '#ffffff', 'editor.lineHighlightBackground': '#f3f5f8' } })
const monacoTheme = () => (useTheme.getState().theme === 'light' ? 'k8s-light' : 'k8s-dark')
useTheme.subscribe(() => monaco.editor.setTheme(monacoTheme()))

const base: monaco.editor.IStandaloneEditorConstructionOptions = {
  fontFamily: "'JetBrains Mono Variable', 'JetBrains Mono', monospace",
  language: 'yaml',
  automaticLayout: true,
  minimap: { enabled: false },
  fontSize: 13,
  scrollBeyondLastLine: false,
  tabSize: 2,
  renderWhitespace: 'boundary',
  // Native EditContext breaks some IMEs and automation; the textarea path is the battle-tested one.
  editContext: false,
}

export function YamlEditor({ value, readOnly, onChange }: { value: string; readOnly?: boolean; onChange?: (v: string) => void }) {
  const el = useRef<HTMLDivElement>(null)
  const ed = useRef<monaco.editor.IStandaloneCodeEditor>(undefined)
  const cb = useRef(onChange)
  cb.current = onChange

  useEffect(() => {
    ed.current = monaco.editor.create(el.current!, { ...base, theme: monacoTheme(), value, readOnly })
    const sub = ed.current.onDidChangeModelContent(() => cb.current?.(ed.current!.getValue()))
    return () => {
      sub.dispose()
      ed.current?.getModel()?.dispose()
      ed.current?.dispose()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (ed.current && ed.current.getValue() !== value) ed.current.setValue(value)
  }, [value])
  useEffect(() => {
    ed.current?.updateOptions({ readOnly })
  }, [readOnly])

  return <div className="monaco" ref={el} data-testid="yaml-editor" />
}

export function YamlDiff({ original, modified }: { original: string; modified: string }) {
  const el = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const d = monaco.editor.createDiffEditor(el.current!, { ...base, theme: monacoTheme(), readOnly: true, renderSideBySide: true, originalEditable: false })
    const o = monaco.editor.createModel(original, 'yaml')
    const m = monaco.editor.createModel(modified, 'yaml')
    d.setModel({ original: o, modified: m })
    return () => {
      d.dispose()
      o.dispose()
      m.dispose()
    }
  }, [original, modified])
  return <div className="monaco" ref={el} data-testid="yaml-diff" />
}
