import { readFileSync } from 'node:fs'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { thirdPartyLicenses } from './scripts/third-party-licenses.mts'

const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')) as { version: string }

// Initial JS budget (entry chunk plus its static imports). Panels, Monaco, xterm and pixi load on demand.
const INITIAL_JS_BUDGET = 1200 * 1024

function initialBudget(): Plugin {
  return {
    name: 'leme-initial-budget',
    apply: 'build',
    generateBundle(_opts, bundle) {
      const seen = new Set<string>()
      const walk = (name: string) => {
        const c = bundle[name]
        if (seen.has(name) || !c || c.type !== 'chunk') return
        seen.add(name)
        c.imports.forEach(walk)
      }
      for (const [name, c] of Object.entries(bundle)) if (c.type === 'chunk' && c.isEntry) walk(name)
      let total = 0
      for (const n of seen) {
        const c = bundle[n]
        if (c.type === 'chunk') total += Buffer.byteLength(c.code)
      }
      const kb = (total / 1024).toFixed(0)
      if (total > INITIAL_JS_BUDGET) this.warn(`initial JS is ${kb} KiB, over the ${INITIAL_JS_BUDGET / 1024} KiB budget: something heavy is statically imported`)
      else this.info?.(`initial JS ${kb} KiB (budget ${INITIAL_JS_BUDGET / 1024} KiB)`)
    },
  }
}

export default defineConfig({
  root: 'renderer',
  base: './',
  plugins: [react(), thirdPartyLicenses(), initialBudget()],
  define: { __APP_VERSION__: JSON.stringify(pkg.version) },
  // One CSS file loaded up front: dockview copies the page's stylesheets into a popout window when it opens, so CSS
  // that arrived later with a lazy chunk would be missing there. JS stays split (panels, Monaco, xterm, xyflow).
  build: { outDir: '../renderer-dist', emptyOutDir: true, cssCodeSplit: false },
  server: { port: 5173, strictPort: true },
})
