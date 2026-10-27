// "Configurar Prometheus" on a cluster without any Prometheus Service: the engine answered
// candidates: null and the window went blank. Kind cluster ONLY; the empty answer is simulated.
import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

test.beforeAll(async () => {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => engine?.kill('SIGTERM'))

test('Configurar Prometheus sem nenhum Prometheus detectado abre o diálogo, sem tela preta', async ({ page }) => {
  // What an older engine (or any null list) answers on a cluster without Prometheus.
  await page.route('**/api/clusters/*/prometheus?*', (r) => r.fulfill({ contentType: 'application/json', body: '{"candidates":null,"saved":false}' }))
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-web').dblclick()
  await page.getByRole('button', { name: 'Métricas' }).click()
  await page.getByRole('button', { name: 'Configurar Prometheus' }).click()
  await expect(page.getByText('Prometheus deste cluster')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Testar' })).toBeVisible()
  await expect(page.getByTestId('error-boundary')).toHaveCount(0)
})

test('um erro de renderização fica no painel: o resto da janela continua', async ({ page }) => {
  // Malformed answer that still throws inside the dialog: the panel boundary must contain it.
  await page.route('**/api/clusters/*/prometheus?*', (r) => r.fulfill({ contentType: 'application/json', body: '{"candidates":{"length":1},"saved":false}' }))
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-web').dblclick()
  await page.getByRole('button', { name: 'Métricas' }).click()
  await page.getByRole('button', { name: 'Configurar Prometheus' }).click()
  await expect(page.getByTestId('error-boundary').first()).toBeVisible()
  // The shell still works: the navigator opens another grid.
  await page.getByTestId('nav-kind-Pod').click()
  await expect(page.locator('.grid-title', { hasText: 'Pods' }).first()).toBeVisible()
})
