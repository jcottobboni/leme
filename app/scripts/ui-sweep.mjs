// UI sweep: screenshots every screen on the kind cluster + detects overflowing/clipped elements.
// Usage (from app/):
//   KUBECONFIG_SWEEP=$HOME/.kube/leme-dev/kubeconfig:e2e/fixtures/sweep-fakes.kubeconfig \
//     node scripts/ui-sweep.mjs <outdir> [width] [height] [dark|light] [suffix]
// Needs `npx vite build --outDir <outdir>/dist` and engine/bin/leme. Never point it at real clusters.
import { chromium } from 'playwright'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..')
import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
const S = process.argv[2]
// Guard: without an explicit kubeconfig the engine would read ~/.kube/config (production contexts).
if (!process.env.KUBECONFIG_SWEEP || !S) throw new Error('set KUBECONFIG_SWEEP (kind only) and pass an output dir')
const W = +(process.argv[3] || 1500), H = +(process.argv[4] || 900), THEME = process.argv[5] || 'dark'
const OUT = `${S}/out-${W}-${THEME}${process.argv[6] || ""}`
fs.mkdirSync(OUT, { recursive: true })
const eng = spawn(path.join(ROOT, 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', S + '/dist'], { env: { ...process.env, KUBECONFIG: process.env.KUBECONFIG_SWEEP, LEME_TOKEN: 'tok', LEME_CONFIG_DIR: S + '/cfg' } })
const base = await new Promise((r) => createInterface({ input: eng.stdout }).on('line', (l) => { try { const m = JSON.parse(l); if (m.event === 'ready') r(`http://127.0.0.1:${m.port}`) } catch {} }))
const b = await chromium.launch({ channel: 'chrome' })
const p = await b.newPage({ viewport: { width: W, height: H } })
const report = []
p.on('pageerror', (e) => report.push(`PAGEERR ${e.stack.slice(0, 500).replace(/\n/g, " | ")}`))
p.on('console', (m) => { if (m.type() === 'error') report.push(`CONSOLE ${m.text().slice(0, 200)}`) })

function detect() {
  const path = (el) => {
    const parts = []
    for (let e = el; e && e !== document.body && parts.length < 4; e = e.parentElement) {
      let s = e.tagName.toLowerCase()
      if (e.dataset.testid) s += `[${e.dataset.testid}]`
      else if (typeof e.className === 'string' && e.className.trim()) s += '.' + e.className.trim().split(/\s+/).slice(0, 2).join('.')
      parts.unshift(s)
    }
    return parts.join(' > ')
  }
  const out = []
  const vw = innerWidth, vh = innerHeight
  for (const el of document.querySelectorAll('body *')) {
    if (el.closest('svg, canvas, .monaco-editor, .xterm, .react-flow__viewport, .dv-tabs-container')) continue
    const r = el.getBoundingClientRect()
    if (r.width < 2 || r.height < 2) continue
    const cs = getComputedStyle(el)
    if (cs.visibility === 'hidden' || +cs.opacity === 0) continue
    // Outside the viewport horizontally.
    if (r.right > vw + 1 && cs.position !== 'fixed') out.push(`VIEWPORT ${Math.round(r.right - vw)}px ${path(el)}`)
    // Child sticking out of its parent's box when the parent does not scroll.
    const par = el.parentElement
    if (par && !['absolute', 'fixed'].includes(cs.position)) {
      const pr = par.getBoundingClientRect(), pcs = getComputedStyle(par)
      const scrolls = /(auto|scroll)/.test(pcs.overflowX)
      if (!scrolls && pr.width > 0 && r.right - pr.right > 3 && r.left < pr.right) out.push(`SPILL ${Math.round(r.right - pr.right)}px ${pcs.overflowX === 'hidden' ? '(clipped) ' : ''}${path(el)}`)
    }
    // Text clipped without ellipsis.
    if (cs.overflowX === 'hidden' && el.scrollWidth > el.clientWidth + 2 && cs.textOverflow !== 'ellipsis' && [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim()))
      out.push(`CLIP ${el.scrollWidth - el.clientWidth}px ${path(el)} "${el.textContent.trim().slice(0, 30)}"`)
  }
  return [...new Set(out)].slice(0, 40)
}

let n = 0
async function shot(name) {
  await p.waitForTimeout(700)
  n++
  const file = `${String(n).padStart(2, '0')}-${name}`
  await p.screenshot({ path: `${OUT}/${file}.png` })
  const issues = await p.evaluate(detect)
  report.push(`## ${file}`, ...issues)
}
const tryDo = async (label, fn) => { try { await fn() } catch (e) { report.push(`FAIL ${label}: ${e.message.split('\n')[0]}`) } }

await p.goto(`${base}/#token=tok`)
await p.evaluate((t) => { localStorage.clear(); localStorage.setItem('leme.ui.v1', JSON.stringify({ theme: t })) }, THEME)
await p.reload()
await shot('catalog')
await tryDo('profile', async () => {
  await p.getByTestId('cluster-card-payroll-api-prd/api-ocp-c01-acme-dc1:6443/alice').getByTitle('Personalizar').click()
  await shot('profile-dialog')
  await p.keyboard.press('Escape'); await p.getByRole('button', { name: 'Cancelar' }).click().catch(() => {})
})
await p.getByTestId('cluster-row-kind-leme-dev').first().click()
await p.getByTestId('nav-kind-Pod').waitFor()
await shot('connected')
const tools = await p.locator('[data-testid^="tool-"]').evaluateAll((els) => els.map((e) => e.dataset.testid))
for (const t of tools) await tryDo(t, async () => { await p.getByTestId(t).click(); await p.waitForTimeout(1500); await shot(t) })
await p.locator('.ns-select:visible').first().selectOption('demo').catch(() => {})
const kinds = await p.locator('[data-testid^="nav-kind-"]').evaluateAll((els) => els.map((e) => e.dataset.testid))
for (const k of kinds) await tryDo(k, async () => { await p.getByTestId(k).click(); await p.waitForTimeout(500); await shot(k) })
// Drawer + object tabs on a pod.
await tryDo('object', async () => {
  await p.getByTestId('nav-kind-Pod').click()
  await p.locator('[data-testid^="row-web-"]').first().click()
  await shot('drawer')
  await p.getByTestId('drawer').getByRole('button', { name: 'Abrir em aba' }).click()
  const tabs = await p.locator('.tabs:visible button').allTextContents()
  for (const [i, t] of tabs.entries()) { await p.locator('.tabs:visible button').nth(i).click(); await p.waitForTimeout(800); await shot(`obj-${t.trim().replace(/\W+/g, '_')}`) }
})
await tryDo('deploy-obj', async () => {
  await p.getByTestId('nav-kind-Deployment').click()
  await p.getByTestId('row-web').dblclick()
  const tabs = await p.locator('.tabs:visible button').allTextContents()
  for (const [i, t] of tabs.entries()) { await p.locator('.tabs:visible button').nth(i).click(); await p.waitForTimeout(800); await shot(`deploy-${t.trim().replace(/\W+/g, '_')}`) }
})
await tryDo('palette', async () => { await p.keyboard.press('Control+k'); await shot('palette'); await p.keyboard.press('Escape') })
await tryDo('rowmenu', async () => { await p.getByTestId('nav-kind-Deployment').click(); await p.getByTestId('row-web').click({ button: 'right' }); await shot('rowmenu'); await p.keyboard.press('Escape') })
await tryDo('scale', async () => { await p.getByTestId('row-web').click({ button: 'right' }); await p.getByRole('menuitem', { name: 'Scale…' }).click(); await shot('scale-dialog'); await p.keyboard.press('Escape'); await p.getByRole('button', { name: 'Cancelar' }).click().catch(() => {}) })
await tryDo('clustermenu', async () => { await p.getByTestId('cluster-menu').click(); await shot('cluster-menu'); await p.keyboard.press('Escape') })
await tryDo('hovercard', async () => { await p.getByTestId('hotbar-kind-leme-dev').hover(); await shot('hovercard') })
fs.writeFileSync(`${OUT}/report.txt`, report.join('\n'))
await b.close(); eng.kill()
console.log(`done ${n} shots -> ${OUT}`)
