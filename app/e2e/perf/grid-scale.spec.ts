// Perf probe (manual): grid with all pods of the kind scale lab (scripts/kwok-load.sh, run here if missing).
// Runs with PERF=1:  PERF=1 npx playwright test e2e/perf/grid-scale.spec.ts
import { test, expect } from '@playwright/test'
import { execFileSync, spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

test.skip(!process.env.PERF, 'perf probe: set PERF=1')

test('grid: 5k pods — tempo até renderizar e FPS na rolagem', async ({ page }) => {
  test.setTimeout(300_000)
  const kubeconfig = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
  const pods = execFileSync('kubectl', ['--kubeconfig', kubeconfig, '--context', 'kind-leme-dev', 'get', 'pods', '-n', 'scale', '--no-headers', '--ignore-not-found'], { encoding: 'utf8' }).split('\n').filter(Boolean).length
  if (pods < 4000) execFileSync('bash', [path.join(__dirname, '..', '..', '..', 'scripts', 'kwok-load.sh')], { stdio: 'inherit' })
  const token = 'perf-' + Date.now()
  const eng = spawn(path.join(__dirname, '..', '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig'), LEME_TOKEN: token },
  })
  const port = await new Promise<string>((r) => createInterface({ input: eng.stdout! }).on('line', (l) => r(JSON.parse(l).port)))
  await page.goto(`http://127.0.0.1:${port}/#token=${token}`)
  await page.getByTestId('cluster-row-kind-leme-dev').first().click()
  const t0 = Date.now()
  await page.getByTestId('nav-kind-Pod').click()
  await expect(page.locator('.count-pill', { hasText: /\d{4,} itens/ })).toBeVisible({ timeout: 30_000 })
  const firstRender = Date.now() - t0
  const fps = await page.evaluate(async () => {
    const el = document.querySelector('.grid-body') as HTMLElement
    let frames = 0
    let running = true
    const tick = () => { frames++; if (running) requestAnimationFrame(tick) }
    requestAnimationFrame(tick)
    const start = performance.now()
    while (performance.now() - start < 3000) {
      el.scrollTop += 400
      if (el.scrollTop + el.clientHeight >= el.scrollHeight) el.scrollTop = 0
      await new Promise((r) => requestAnimationFrame(r))
    }
    running = false
    return frames / ((performance.now() - start) / 1000)
  })
  const count = await page.locator('.count-pill').first().innerText()
  const heap = await page.evaluate(() => (performance as any).memory?.usedJSHeapSize ?? 0)
  console.log(`PERF rows="${count}" firstRenderMs=${firstRender} scrollFps=${fps.toFixed(1)} heapMB=${(heap / 1048576).toFixed(0)}`)
  eng.kill()
})
