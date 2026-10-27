// Desktop smoke test for every OS (Linux, Windows, macOS): the shell starts the engine, the UI lists
// the kubeconfig contexts and the UI state survives a restart. Fake contexts only; nothing connects.
import { test, expect, _electron as electron } from '@playwright/test'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const FAKES = path.join(__dirname, 'fixtures', 'sweep-fakes.kubeconfig')
const CONTEXT = 'payroll-api-prd/api-ocp-c01-acme-dc1:6443/alice'

test('smoke: abre, lista os contextos e mantém o estado ao reiniciar', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-smoke-'))
  const launch = () =>
    electron.launch({
      args: [...(process.platform === 'linux' ? ['--no-sandbox'] : []), `--user-data-dir=${path.join(tmp, 'user-data')}`, path.join(__dirname, '..')],
      env: { ...process.env, KUBECONFIG: FAKES, LEME_CONFIG_DIR: path.join(tmp, 'config') },
    })

  let app = await launch()
  try {
    const win = await app.firstWindow()
    await expect(win.getByTestId(`cluster-card-${CONTEXT}`)).toBeVisible({ timeout: 60_000 })
    expect(await win.title()).toBe('Leme')
    await expect(win.locator('[data-testid^="cluster-card-"]')).toHaveCount(6)
    // Production detection works on every OS (engine side).
    await expect(win.getByTestId(`cluster-card-${CONTEXT}`).locator('.badge-prod')).toBeVisible()
    await win.getByTestId(`cluster-card-${CONTEXT}`).click({ button: 'right' })
    await win.getByRole('menuitem', { name: 'Fixar na hotbar' }).click()
    await expect(win.getByTestId(`hotbar-${CONTEXT}`)).toBeVisible()
    await win.waitForTimeout(800) // the shell writes the UI state 300 ms after the last change
  } finally {
    await app.close()
  }
  expect(fs.existsSync(path.join(tmp, 'user-data', 'ui-state.json'))).toBe(true)

  app = await launch()
  try {
    const win = await app.firstWindow()
    await expect(win.getByTestId(`hotbar-${CONTEXT}`)).toBeVisible({ timeout: 60_000 })
  } finally {
    await app.close()
  }
})
