// Topology v2: cards, kind chips, selection + info card, app regions. Kind cluster ONLY.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  // Always pin both kubeconfig and context so nothing else can be targeted.
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

test.beforeAll(async () => {
  const bin = process.env.TOPO_ENGINE ?? path.join(__dirname, '..', '..', 'engine', 'bin', 'leme')
  const dist = process.env.TOPO_DIST ?? path.join(__dirname, '..', 'renderer-dist')
  engine = spawn(bin, ['serve', '--addr', '127.0.0.1:0', '--static', dist], { env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN } })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => engine?.kill('SIGTERM'))

async function openTopology(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-topology').click()
  await page.locator('.ns-select:visible').first().selectOption('demo')
  await expect(page.getByTestId('topology').locator('.tc').first()).toBeVisible({ timeout: 30_000 })
}

test('TV2-1: cartões com ícone, status e quadrados de pods', async ({ page }) => {
  await openTopology(page)
  const web = page.getByTestId('topo-Deployment/demo/web')
  await expect(web.locator('.tc-ico svg')).toBeVisible()
  await expect(web.locator('.tc-name')).toHaveText('web')
  await expect(web.locator('.tc-sub')).toContainText('Deployment · demo')
  await expect(web.getByTestId('pod-squares').locator('.sq')).toHaveCount(3)
  await expect(page.getByTestId('topo-head')).toContainText('objetos')
  await expect(page.getByTestId('topo-zoom-pct')).toContainText('%')
})

test('TV2-2: chip de tipo oculta e mostra os objetos', async ({ page }) => {
  await openTopology(page)
  const cards = page.locator('.tc[data-testid^="topo-CronJob/"]')
  await expect(cards).toHaveCount(1)
  const chip = page.getByTestId('kind-chip-CronJob')
  await chip.click()
  await expect(cards).toHaveCount(0)
  await expect(chip).toHaveAttribute('aria-pressed', 'false')
  await chip.click()
  await expect(cards).toHaveCount(1)
})

test('TV2-3: seleção destaca a vizinhança, mostra o cartão e Esc limpa', async ({ page }) => {
  await openTopology(page)
  const web = page.getByTestId('topo-Deployment/demo/web')
  await web.click()
  const info = page.getByTestId('topo-info')
  await expect(info).toContainText('web')
  await expect(info).toContainText('depende dele')
  await expect(info.getByRole('button', { name: 'Abrir detalhes' })).toBeVisible()
  await expect(page.locator('.react-flow__node.topo-dim').first()).toBeAttached()
  await expect(page.locator('.react-flow__edge.topo-edge-hot').first()).toBeAttached()
  await page.keyboard.press('Escape')
  await expect(info).toHaveCount(0)
  await expect(page.locator('.react-flow__node.topo-dim')).toHaveCount(0)
  // Focar re-roots the graph on the object.
  await web.click()
  await page.getByTestId('topo-info').getByRole('button', { name: 'Focar' }).click()
  await expect(page.locator('.tp-chip.focus')).toContainText('Deployment/web')
})

test('TV2-4: agrupar por app cria regiões e o toggle as remove', async ({ page }) => {
  await openTopology(page)
  const regions = page.getByTestId('topo-region')
  await expect(regions.filter({ hasText: /web/i }).first()).toBeVisible()
  await expect(regions.filter({ hasText: /broken/i }).first()).toBeVisible()
  await page.getByTestId('group-toggle').click()
  await expect(regions).toHaveCount(0)
  await page.getByTestId('group-toggle').click()
  await expect(regions.first()).toBeVisible()
})
