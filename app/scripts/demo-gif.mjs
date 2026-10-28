// Records the README demo (docs/images/demo.gif) against the local kind cluster ONLY.
// Usage: node scripts/demo-gif.mjs   (needs `make engine ui`, the kind cluster and ffmpeg)
import { chromium } from '@playwright/test'
import { spawn, execFileSync } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const root = path.resolve(import.meta.dirname, '..', '..')
const KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const TOKEN = 'demo-' + Math.random().toString(36).slice(2)
const work = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-demo-'))
const config = path.join(work, 'config')
fs.mkdirSync(path.join(config, 'plugins'), { recursive: true })
for (const f of ['rails.yaml', 'k9s-style.yaml']) fs.copyFileSync(path.join(root, 'examples', 'plugins', f), path.join(config, 'plugins', f))

// Only the kind context is visible; k9s files of this machine are not read.
const env = { ...process.env, KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: config, K9S_CONFIG_DIR: path.join(work, 'k9s'), XDG_DATA_HOME: path.join(work, 'data') }
const engine = spawn(path.join(root, 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(root, 'app', 'renderer-dist')], { env })
const base = await new Promise((resolve, reject) => {
  createInterface({ input: engine.stdout }).on('line', (l) => {
    const m = JSON.parse(l)
    if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
  })
  setTimeout(() => reject(new Error('engine timeout')), 10000)
})

const size = { width: 1280, height: 760 }
const browser = await chromium.launch({ channel: 'chrome' })
const ctx = await browser.newContext({ viewport: size, recordVideo: { dir: work, size }, colorScheme: 'dark' })
const page = await ctx.newPage()
const pause = (ms) => page.waitForTimeout(ms)
try {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId('cluster-row-kind-leme-dev').first().waitFor()
  await pause(1200)
  await page.getByTestId('cluster-row-kind-leme-dev').first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
  // Show the production guard: the cluster stays read-only during the whole demo.
  const { clusters } = await (await fetch(`${base}/api/clusters`, { headers: { Authorization: `Bearer ${TOKEN}` } })).json()
  const id = clusters.find((c) => c.name === 'kind-leme-dev').id
  await fetch(`${base}/api/clusters/${id}/readonly`, { method: 'POST', headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ readOnly: true }) })
  await page.reload()
  await page.getByTestId('cluster-row-kind-leme-dev').first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
  await page.getByTestId('tool-overview').click()
  await page.getByTestId('ov-nodes').waitFor({ timeout: 30000 })
  await pause(2500)

  await page.getByTestId('tool-map').click()
  await pause(3500)

  await page.getByTestId('nav-kind-Pod').click()
  const ns = page.locator('.ns-select:visible').first()
  await ns.selectOption('demo')
  const pod = execFileSync('kubectl', ['--kubeconfig', KUBECONFIG, '--context', 'kind-leme-dev', '-n', 'demo', 'get', 'pods', '-l', 'app=web', '-o', 'jsonpath={.items[0].metadata.name}'], { encoding: 'utf8' }).trim()
  const row = page.getByTestId(`row-${pod}`).first()
  await row.waitFor()
  await pause(1500)
  const box = await row.boundingBox()
  await page.mouse.click(box.x + 260, box.y + box.height / 2, { button: 'right' })
  await pause(2800)
  await page.keyboard.press('Escape')

  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-web').first().dblclick()
  await page.getByTestId('tab-topology').last().click()
  await pause(3000)

  await page.keyboard.press('Control+k')
  await pause(400)
  await page.keyboard.type(':deploy demo -l app=web', { delay: 70 })
  await pause(1800)
  await page.keyboard.press('Escape')

  await page.getByTestId('tool-security').click()
  await page.getByTestId('score').first().waitFor({ timeout: 30000 })
  await pause(2500)
} finally {
  await ctx.close()
  await browser.close()
  engine.kill('SIGTERM')
}

const video = fs.readdirSync(work).find((f) => f.endsWith('.webm'))
const out = path.join(root, 'docs', 'images', 'demo.gif')
execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-ss', '3', '-i', path.join(work, video), '-vf',
  'fps=8,scale=900:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle', out])
fs.rmSync(work, { recursive: true, force: true })
console.log(`${out}: ${(fs.statSync(out).size / 1e6).toFixed(1)} MB`)
