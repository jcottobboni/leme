import { readFileSync } from 'node:fs'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { thirdPartyLicenses } from './scripts/third-party-licenses.mts'

const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8')) as { version: string }

export default defineConfig({
  root: 'renderer',
  base: './',
  plugins: [react(), thirdPartyLicenses()],
  define: { __APP_VERSION__: JSON.stringify(pkg.version) },
  build: { outDir: '../renderer-dist', emptyOutDir: true, chunkSizeWarningLimit: 6000 },
  server: { port: 5173, strictPort: true },
})
