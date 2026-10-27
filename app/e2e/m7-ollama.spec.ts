// M7 RF07 / CA07: "Ollama (local)" provider against a FAKE Ollama (never a real model): the engine
// lists the installed models (/api/tags), a loopback endpoint needs no cloud consent, a remote one is
// treated as cloud, and "Exportar proposta" downloads the final YAML and the merge patch. The proposal
// is never applied: only a server-side dry-run touches the isolated kind cluster.
import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import http from 'node:http'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import type { AddressInfo } from 'node:net'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-m7o-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-m7o-'))
let engine: ChildProcess
let base = ''
let ollama: http.Server
let ollamaURL = ''
const tagsCalls: http.IncomingHttpHeaders[] = []
const chats: { model: string; messages: { role: string; content: string }[] }[] = []

function sse(res: http.ServerResponse, chunks: object[]) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  for (const c of chunks) res.write(`data: ${JSON.stringify(c)}\n\n`)
  res.end('data: [DONE]\n\n')
}

// Ollama-shaped fake: GET /api/tags and the OpenAI-compatible POST /v1/chat/completions.
function fakeOllama(req: http.IncomingMessage, res: http.ServerResponse) {
  if (req.method === 'GET' && req.url === '/api/tags') {
    tagsCalls.push(req.headers)
    res.writeHead(200, { 'Content-Type': 'application/json' })
    return res.end(JSON.stringify({ models: [
      { name: 'fake-llama:7b', model: 'fake-llama:7b', size: 4_100_000_000, modified_at: '2026-10-01T00:00:00Z', details: { family: 'llama', parameter_size: '7B' } },
      { name: 'fake-qwen:14b', model: 'fake-qwen:14b', size: 9_000_000_000, details: { family: 'qwen2', parameter_size: '14B' } },
    ] }))
  }
  if (req.method !== 'POST' || req.url !== '/v1/chat/completions') {
    res.writeHead(404)
    return res.end()
  }
  let raw = ''
  req.on('data', (d) => (raw += d))
  req.on('end', () => {
    const body = JSON.parse(raw)
    chats.push(body)
    const last = body.messages[body.messages.length - 1]
    if (last.role === 'tool') return sse(res, [{ choices: [{ delta: { content: 'Proposta pronta para revisão.' }, finish_reason: 'stop' }] }])
    const user = body.messages.find((m: { role: string }) => m.role === 'user')?.content ?? ''
    const m = /kind=Deployment group=apps namespace=(\S+) name=(\S+)/.exec(user)
    if (!m) return sse(res, [{ choices: [{ delta: { content: 'Sem objeto em foco.' }, finish_reason: 'stop' }] }])
    const args = JSON.stringify({
      kind: 'Deployment', group: 'apps', namespace: m[1], name: m[2], summary: 'Fixa a imagem numa tag que existe',
      merge_patch: { spec: { template: { spec: { containers: [{ name: 'nginx', image: 'nginx:1.27-alpine' }] } } } },
    })
    sse(res, [
      { choices: [{ delta: { content: `Local: a imagem de ${m[2]} não existe. ` } }] },
      { choices: [{ delta: { tool_calls: [{ index: 0, id: 'call_1', function: { name: 'propose_change', arguments: args } }] }, finish_reason: 'tool_calls' }] },
    ])
  })
}

test.beforeAll(async () => {
  ollama = http.createServer(fakeOllama)
  await new Promise<void>((r) => ollama.listen(0, '127.0.0.1', r))
  ollamaURL = `http://127.0.0.1:${(ollama.address() as AddressInfo).port}/v1`
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: CONFIG, LEME_AI_API_KEY: '', ANTHROPIC_API_KEY: '', OPENAI_API_KEY: '' },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  ollama?.close()
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

const api = (page: Page, p: string) => page.evaluate(async ({ base, token, p }) => {
  const r = await fetch(base + p, { headers: { Authorization: `Bearer ${token}` } })
  return { status: r.status, body: await r.json().catch(() => null) }
}, { base, token: TOKEN, p })

test('CA07: Ollama local lista os modelos pelo engine, não pede consentimento e exporta a proposta', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('tool-copilot').click()
  await page.getByTestId('copilot-setup').click()
  const dlg = page.getByTestId('ai-settings')
  await dlg.getByTestId('ai-enabled').check()
  await dlg.getByTestId('ai-provider-ollama').click()
  // The preset points at the local Ollama.
  await expect(dlg.getByTestId('ai-baseurl')).toHaveValue('http://127.0.0.1:11434/v1')
  await expect(dlg.getByTestId('ai-local-note')).toBeVisible()
  await expect(dlg.getByTestId('ai-key')).toHaveCount(0)

  // A remote Ollama is cloud: consent applies.
  await dlg.getByTestId('ai-baseurl').fill('https://ollama.example.com/v1')
  await expect(dlg.getByTestId('ai-remote-note')).toBeVisible()
  await expect(dlg.getByTestId('ai-anonymize')).toBeChecked()

  // The fake local Ollama: the model list comes through the engine.
  await dlg.getByTestId('ai-baseurl').fill(ollamaURL)
  await expect(dlg.getByTestId('ai-local-note')).toBeVisible()
  const models = dlg.getByTestId('ai-ollama-models')
  await expect(models.locator('option', { hasText: 'fake-llama:7b' })).toHaveCount(1)
  await models.selectOption('fake-llama:7b')
  await expect(dlg.getByTestId('ai-model')).toHaveValue('fake-llama:7b')
  await expect(dlg.getByTestId('ai-anonymize')).not.toBeChecked() // local default: off, still available
  expect(tagsCalls.length).toBeGreaterThan(0)
  expect(tagsCalls.every((h) => !h.origin)).toBe(true) // the engine called it, not the browser
  await page.getByTestId('ai-save').click()
  await expect(dlg).toHaveCount(0)

  const settings = await api(page, '/api/ai/settings')
  expect(settings.body.local).toBe(true)
  expect(settings.body.consented).toBe(true)
  expect(settings.body.ai.provider).toBe('ollama')
  // Listing is local only.
  expect((await api(page, '/api/ai/models?baseURL=' + encodeURIComponent('https://ollama.example.com/v1'))).status).toBe(400)

  // Ask about demo/broken: no cloud consent, the fake model proposes a change.
  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-broken').dblclick()
  await page.getByRole('button', { name: 'Perguntar à IA' }).click()
  const cp = page.getByTestId('copilot').filter({ visible: true })
  await cp.getByTestId('copilot-input').fill('Por que não sobe?')
  await cp.getByTestId('copilot-send').click()
  await expect(cp.getByTestId('copilot-answer').first()).toContainText('Local: a imagem de broken')
  await expect(cp.getByTestId('copilot-consent')).toHaveCount(0)
  await expect(cp.getByTestId('copilot-meta')).toContainText('fake-llama:7b')
  expect(chats[0].model).toBe('fake-llama:7b')
  const card = cp.getByTestId('copilot-proposal')
  await expect(card).toContainText('Fixa a imagem')

  // Exportar proposta: final YAML and the merge patch.
  await card.getByTestId('copilot-export').click()
  let dl = page.waitForEvent('download')
  await page.getByRole('menuitem', { name: 'YAML final (.yaml)' }).click()
  let file = await dl
  expect(file.suggestedFilename()).toBe('deployment-demo-broken.yaml')
  const yaml = fs.readFileSync(await file.path(), 'utf8')
  expect(yaml).toContain('# Proposta do Leme: Fixa a imagem numa tag que existe')
  expect(yaml).toContain('image: nginx:1.27-alpine')
  expect(yaml).toContain('kind: Deployment')
  expect(yaml).not.toMatch(/managedFields|resourceVersion:/)

  await card.getByTestId('copilot-export').click()
  dl = page.waitForEvent('download')
  await page.getByRole('menuitem', { name: 'Patch merge (.patch.yaml)' }).click()
  file = await dl
  expect(file.suggestedFilename()).toBe('deployment-demo-broken.patch.yaml')
  const patch = fs.readFileSync(await file.path(), 'utf8')
  expect(patch).toContain('kubectl patch deployment.apps broken -n demo --type merge --patch-file deployment-demo-broken.patch.yaml')
  expect(patch).toContain('image: nginx:1.27-alpine')
  expect(patch).not.toContain('kind: Deployment')
})
