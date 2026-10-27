import { useState } from 'react'
import { Check, Copy } from 'lucide-react'

/** Documentation of the project (opened in the browser). */
export const DOCS_URL = 'https://github.com/jcottobboni/leme/blob/main/docs'

/** Code block with a copy button, for empty states that teach how to enable something. */
export function CodeCopy({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <pre className="code-copy">
      {text}
      <button className="icon-btn" title={done ? 'Copiado' : 'Copiar'} aria-label="Copiar"
        onClick={() => navigator.clipboard.writeText(text).then(() => {
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        })}>
        {done ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </pre>
  )
}
