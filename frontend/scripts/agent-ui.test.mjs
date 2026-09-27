import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const origin = process.env.UI_TEST_ORIGIN || 'http://127.0.0.1:15173'
const browser = await chromium.launch({ headless: true })
try {
  const context = await browser.newContext()
  const now = new Date().toISOString()
  const threads = ['one', 'two'].map(id => ({ id, title: `Conversation ${id}`, createdAt: now, updatedAt: now, messageCount: 1 }))
  let run = { id: 'run-one', threadId: 'one', status: 'waiting_for_approval', approval: { request_id: 'approval-one', command: 'python fixture.py', choices: ['once', 'deny'] } }
  const controls = [], submissions = []
  let failAcceptance = true
  const evidence = [
    { seq: 1, kind: 'tool.call', runId: 'run-one', createdAt: now, data: { callId: 'call-one', tool: 'terminal', arguments: { command: 'false' } } },
    { seq: 2, kind: 'tool.result', runId: 'run-one', createdAt: now, data: { callId: 'call-one', tool: 'terminal', result: JSON.stringify({ exit_code: 1, output: 'Fixture failed', cwd: '/tmp/fixture' }) } },
  ]
  await context.route('**/api/**', async route => {
    const req = route.request(), path = new URL(req.url()).pathname, id = path.split('/')[3]
    let data = {}
    if (path === '/api/runtime') data = { hostname: 'book14', os: 'Darwin', cwd: '/Users/test', healthy: true, model: 'Gemma', provider: 'unsloth' }
    else if (path === '/api/skills') data = { data: [{ name: 'coding', description: 'Implement and verify code' }] }
    else if (path === '/api/threads' && req.method() === 'POST') {
      const thread = { ...threads[0], id: 'three', title: 'Conversation three' }; threads.push(thread); data = { thread }
    } else if (path === '/api/threads') data = { threads }
    else if (path.endsWith('/messages')) data = { messages: [{ id: `message-${id}`, threadId: id, role: 'assistant', content: `History for ${id}`, createdAt: now }] }
    else if (path.endsWith('/activity')) data = { events: id === 'one' && new URL(req.url()).searchParams.get('after') === '0' ? evidence : [], hasMore: false }
    else if (path.endsWith('/runs') && req.method() === 'POST') {
      submissions.push(req.postDataJSON())
      if (failAcceptance) { failAcceptance = false; await route.abort('failed'); return }
      run = { id: submissions[0].requestId, threadId: id, status: 'completed', output: 'Verified reply' }; data = { run }
    } else if (path.endsWith('/runs')) data = { runs: run.threadId === id ? [run] : [] }
    else if (req.method() === 'POST') {
      const action = path.split('/').at(-1); controls.push({ action, ...req.postDataJSON() })
      if (action === 'approval') run = { ...run, status: 'running', approval: undefined }
      if (action === 'steer') run = { ...run, pendingSteer: ['Check tests'] }
      if (action === 'stop') run = { ...run, status: 'cancelled' }
    }
    await route.fulfill({ json: data })
  })
  await context.routeWebSocket('**/ws?*', socket => { socket.send(JSON.stringify({ type: 'ready' })) })
  const page = await context.newPage(), errors = []
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(`${origin}/conversations/one`)
  await page.getByText('APPROVAL REQUIRED', { exact: true }).waitFor()
  await page.getByText('terminal · TOOL FAILED', { exact: true }).click()
  await page.getByText(/Fixture failed/).waitFor()
  await page.reload()
  await page.getByText('terminal · TOOL FAILED', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'ONCE', exact: true }).click()
  await page.getByRole('textbox', { name: 'Message', exact: true }).fill('Check tests')
  await page.getByRole('button', { name: 'Send update', exact: true }).click()
  await page.getByText(/Undelivered update:/).waitFor()
  await page.getByRole('button', { name: 'STOP', exact: true }).click()
  await page.locator('.agent-run-status').filter({ hasText: 'CANCELLED' }).waitFor()
  assert.deepEqual(controls.map(c => c.action), ['approval', 'steer', 'stop'])
  assert.equal(controls[0].request_id, 'approval-one')
  await page.getByRole('link', { name: /Conversation two/ }).click()
  await page.getByText('History for two', { exact: true }).waitFor()
  assert.equal(await page.getByText('terminal · TOOL FAILED', { exact: true }).count(), 0)
  await page.goBack()
  await page.getByText('terminal · TOOL FAILED', { exact: true }).waitFor()
  await page.getByRole('button', { name: 'NEW CHAT', exact: true }).click()
  await page.locator('.agent-skills summary').click()
  await page.getByRole('checkbox', { name: /coding/ }).check()
  await page.getByRole('textbox', { name: 'Message', exact: true }).fill('Implement fixture')
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
  await page.waitForURL('**/conversations/three')
  await page.waitForFunction(() => document.querySelector('textarea')?.value === 'Implement fixture')
  await page.getByRole('button', { name: 'Send message', exact: true }).click()
  await page.getByText('Verified reply', { exact: true }).waitFor()
  assert.equal(threads.length, 3)
  assert.equal(submissions.length, 2)
  assert.deepEqual(submissions[0], submissions[1])
  assert.deepEqual(submissions[0].skills, ['coding'])
  assert.deepEqual(errors, [])
  console.log('Passed: durable evidence, reload, approvals, steer, stop, routes, skills, and idempotent submission retry.')
} finally { await browser.close() }
