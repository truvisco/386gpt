#!/usr/bin/env node

import WebSocket from '../../frontend/node_modules/ws/wrapper.mjs'
const accessHeaders = process.env.CF_ACCESS_CLIENT_ID ? { 'CF-Access-Client-Id': process.env.CF_ACCESS_CLIENT_ID, 'CF-Access-Client-Secret': process.env.CF_ACCESS_CLIENT_SECRET } : {}
const [origin] = process.argv.slice(2)
if (!origin) {
  console.error('usage: websocket.mjs BACKEND_ORIGIN')
  process.exit(2)
}

const response = await fetch(`${origin}/api/threads`, {
  method: 'POST',
  headers: { ...accessHeaders, 'Content-Type': 'application/json' },
  body: JSON.stringify({ title: 'Deployment smoke test' }),
})
if (!response.ok) {
  throw new Error(`could not create smoke-test thread: HTTP ${response.status}`)
}

const { thread } = await response.json()
async function checkTurn(content, statusCommand = false) {
  const websocketOrigin = origin.replace(/^http/, 'ws')
  const socket = new WebSocket(`${websocketOrigin}/ws?thread_id=${encodeURIComponent(thread.id)}`, { headers: accessHeaders })
  const assistantContent = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('LLM completion timed out')), 120_000)
    socket.addEventListener('message', (event) => {
      const payload = JSON.parse(event.data)
      if (payload.type === 'ready') {
        socket.send(JSON.stringify({ type: 'user_message', content }))
      }
      if (
        payload.type === 'message' &&
        payload.message?.role === 'assistant' &&
        payload.message.streaming !== true &&
        payload.message.content?.trim()
      ) {
        clearTimeout(timeout)
        socket.close()
        resolve(payload.message.content.trim())
      }
      if (payload.type === 'error') {
        clearTimeout(timeout)
        socket.close()
        reject(new Error(`LLM proxy failed: ${payload.error}`))
      }
    })
    socket.addEventListener('error', () => {
      clearTimeout(timeout)
      reject(new Error('WebSocket connection failed'))
    })
  })
  const valid = statusCommand ? assistantContent.includes('Hermes Gateway Status') : assistantContent.trim().split('\n').at(-1).trim().toUpperCase() === 'OK'
  if (!valid) {
    throw new Error(`unexpected LLM response: ${assistantContent}`)
  }
}

try {
  await checkTurn('/status', true)
  // Reopening the same thread exercises Hermes's persisted-model resolution,
  // which can differ from the provider resolution used for a fresh session.
  await checkTurn('Reply again with the single word OK.')
  console.log('Native gateway command and model reply smoke test passed.')
} finally {
  await fetch(`${origin}/api/threads/${encodeURIComponent(thread.id)}`, { method: 'DELETE', headers: accessHeaders })
}
