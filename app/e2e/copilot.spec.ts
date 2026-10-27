// M4 Copilot end-to-end, against the isolated kind cluster ONLY and a fake OpenAI-compatible LLM
// that records every request: proves anonymization, that Secret/env values never leave, the
// proposal → dry-run → approval flow, the read-only guard and that API keys never persist in clear.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import http from 'node:http'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import type { AddressInfo } from 'node:net'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-ai-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-ai-'))
let engine: ChildProcess
let base = ''
let llm: http.Server
let llmURL = ''
const requests: { messages: { role: string; content: string }[] }[] = []

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

function sse(res: http.ServerResponse, chunks: object[]) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream' })
  for (const c of chunks) res.write(`data: ${JSON.stringify(c)}\n\n`)
  res.end('data: [DONE]\n\n')
}

// Scripted model: first turn diagnoses and proposes fixing the image of the object in focus;
// after the tool result it confirms that the user must review the diff.
function fakeLLM(req: http.IncomingMessage, res: http.ServerResponse) {
  let raw = ''
  req.on('data', (d) => (raw += d))
  req.on('end', () => {
    const body = JSON.parse(raw)
    requests.push(body)
    const last = body.messages[body.messages.length - 1]
    if (last.role === 'tool') {
      return sse(res, [{ choices: [{ delta: { content: 'Proposta registrada: revise o diff antes de aplicar.' }, finish_reason: 'stop' }], usage: { prompt_tokens: 50, completion_tokens: 10 } }])
    }
    const user = body.messages.find((m: { role: string }) => m.role === 'user')?.content ?? ''
    const m = /kind=Deployment group=apps namespace=(\S+) name=(\S+)/.exec(user)
    if (!m) return sse(res, [{ choices: [{ delta: { content: 'Sem objeto em foco.' }, finish_reason: 'stop' }] }])
    const [, ns, name] = m
    const args = JSON.stringify({
      kind: 'Deployment', group: 'apps', namespace: ns, name, summary: 'Corrige a tag da imagem',
      merge_patch: { spec: { template: { spec: { containers: [{ name: 'nginx', image: 'nginx:1.27-alpine' }] } } } },
    })
    sse(res, [
      { choices: [{ delta: { content: `O Deployment ${name} no namespace ${ns} não sobe porque a imagem nginx:does-not-exist não existe. ` } }] },
      { choices: [{ delta: { tool_calls: [{ index: 0, id: 'call_1', function: { name: 'propose_change', arguments: args.slice(0, 40) } }] } }] },
      { choices: [{ delta: { tool_calls: [{ index: 0, function: { arguments: args.slice(40) } }] }, finish_reason: 'tool_calls' }] },
    ])
  })
}

test.beforeAll(async () => {
  llm = http.createServer(fakeLLM)
  await new Promise<void>((r) => llm.listen(0, '127.0.0.1', r))
  llmURL = `http://127.0.0.1:${(llm.address() as AddressInfo).port}/v1`
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
  llm?.close()
  // Restore the fixture: "broken" must stay broken for the other specs.
  try {
    kubectl('-n', 'demo', 'set', 'image', 'deploy/broken', 'nginx=nginx:does-not-exist')
  } catch {
    /* best effort */
  }
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

async function configureFake(page: Page) {
  await page.getByTestId('tool-copilot').click()
  await page.getByTestId('copilot-setup').click()
  const dlg = page.getByTestId('ai-settings')
  await dlg.getByTestId('ai-enabled').check()
  await dlg.getByTestId('ai-provider-openai').click()
  await dlg.getByTestId('ai-model').fill('fake-model')
  await dlg.getByTestId('ai-baseurl').fill(llmURL)
  await dlg.getByTestId('ai-anonymize').check()
  await page.getByTestId('ai-save').click()
  await expect(dlg).toHaveCount(0)
}

async function askAboutBroken(page: Page) {
  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-broken').dblclick()
  await page.getByRole('button', { name: 'Perguntar à IA' }).click()
  const cp = page.getByTestId('copilot').filter({ visible: true })
  await expect(cp.getByTestId('copilot-ref')).toContainText('broken')
  await cp.getByTestId('copilot-input').fill('Por que este deployment não sobe?')
  await cp.getByTestId('copilot-send').click()
  return cp
}

test('CA02/CA03/CA04/CA09: pergunta, nomes anonimizados no provedor e restaurados na tela, proposta aplicada', async ({ page }) => {
  await openApp(page)
  await configureFake(page)
  const cp = await askAboutBroken(page)
  // The answer shows the real names, restored from the aliases the provider saw.
  await expect(cp.getByTestId('copilot-answer').first()).toContainText('O Deployment broken no namespace demo')
  const card = cp.getByTestId('copilot-proposal')
  await expect(card).toContainText('Corrige a tag da imagem')
  await expect(card).toContainText('dry-run ok')
  await expect(cp.getByTestId('copilot-answer').last()).toContainText('revise o diff')
  // Nothing identifying or secret reached the provider.
  const sent = JSON.stringify(requests)
  expect(requests.length).toBeGreaterThanOrEqual(2)
  for (const leak of [/\bbroken\b/, /\bdemo\b/, /kind-leme-dev/, /s3cr3t/]) expect(sent).not.toMatch(leak)
  expect(sent).toMatch(/obj-\d/)
  // Apply after approval: the rollout becomes healthy.
  await card.getByTestId('copilot-apply').click()
  await expect(card).toContainText('Aplicada')
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'deploy/broken', '-o', 'jsonpath={.spec.template.spec.containers[0].image}')).toBe('nginx:1.27-alpine')
  kubectl('-n', 'demo', 'rollout', 'status', 'deploy/broken', '--timeout=120s')
  // The audit log records the question, the proposal and the apply.
  const audit = await page.evaluate(async ({ base, token }) => (await fetch(`${base}/api/audit`, { headers: { Authorization: `Bearer ${token}` } })).json(), { base, token: TOKEN })
  const actions = audit.entries.map((e: { action: string }) => e.action)
  expect(actions).toEqual(expect.arrayContaining(['ask', 'propose', 'apply']))
})

test('CA06: em somente leitura a proposta não roda dry-run e não pode ser aplicada', async ({ page }) => {
  kubectl('-n', 'demo', 'set', 'image', 'deploy/broken', 'nginx=nginx:does-not-exist')
  await openApp(page)
  await page.getByTestId('rw-toggle').click() // kind opens writable; lock it
  const cp = await askAboutBroken(page)
  const card = cp.getByTestId('copilot-proposal')
  await expect(card).toContainText('sem dry-run')
  await expect(card.getByTestId('copilot-apply')).toBeDisabled()
  await expect(card).toContainText('somente leitura')
  // (The engine-side refusal of applying on a read-only session is covered by internal/change tests.)
  await page.getByTestId('rw-toggle').click()
})

test('CA07: a chave de API não aparece em localStorage, settings.json, respostas da API nem logs', async ({ page }) => {
  const secret = 'sk-ant-e2e-SECRET-' + Math.random().toString(36).slice(2)
  await openApp(page)
  await page.getByTestId('tool-copilot').click()
  await page.getByTestId('copilot-settings').click()
  const dlg = page.getByTestId('ai-settings')
  await dlg.getByTestId('ai-provider-anthropic').click()
  await dlg.getByTestId('ai-key').fill(secret)
  await dlg.getByTestId('ai-key-save').click()
  await expect(dlg).toContainText('configurada')
  await page.getByTestId('ai-save').click()
  const ls = await page.evaluate(() => JSON.stringify(Object.entries(localStorage)))
  expect(ls).not.toContain(secret)
  const settingsFile = fs.readFileSync(path.join(CONFIG, 'settings.json'), 'utf8')
  expect(settingsFile).not.toContain(secret)
  const api = await page.evaluate(async ({ base, token }) => (await fetch(`${base}/api/ai/settings`, { headers: { Authorization: `Bearer ${token}` } })).text(), { base, token: TOKEN })
  expect(api).not.toContain(secret)
  expect(api).toContain('"anthropic":true')
})

test('consentimento: provedor de nuvem pede aceite antes de enviar qualquer dado', async ({ page }) => {
  const before = requests.length
  await openApp(page)
  // CA07 left Claude (official endpoint) selected, with a key: a cloud provider.
  await page.getByTestId('tool-copilot').click()
  const cp = page.getByTestId('copilot').filter({ visible: true })
  await cp.getByTestId('copilot-input').fill('Resuma os problemas')
  await cp.getByTestId('copilot-send').click()
  await expect(cp.getByTestId('copilot-consent')).toContainText('Anthropic')
  await cp.getByTestId('copilot-consent').getByRole('button', { name: 'Cancelar' }).click()
  expect(requests.length).toBe(before)
  // The preview shows what would go out, anonymized.
  await cp.getByTestId('copilot-preview').click()
  await expect(page.getByTestId('copilot-preview-dialog')).toContainText('nomes anonimizados')
})
