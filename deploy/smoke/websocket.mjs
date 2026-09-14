#!/usr/bin/env node

const [origin] = process.argv.slice(2)
if (!origin) {
  console.error('usage: websocket.mjs BACKEND_ORIGIN')
  process.exit(2)
}

const response = await fetch(`${origin}/api/threads`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ title: 'Deployment smoke test' }),
})
if (!response.ok) {
  throw new Error(`could not create smoke-test thread: HTTP ${response.status}`)
}

const { thread } = await response.json()
try {
  const websocketOrigin = origin.replace(/^http/, 'ws')
  const socket = new WebSocket(`${websocketOrigin}/ws?thread_id=${encodeURIComponent(thread.id)}`)
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('LLM completion timed out')), 120_000)
    socket.addEventListener('message', (event) => {
      const payload = JSON.parse(event.data)
      if (payload.type === 'ready') {
        socket.send(JSON.stringify({ type: 'user_message', content: 'Reply with the single word OK.' }))
      }
      if (
        payload.type === 'message' &&
        payload.message?.role === 'assistant' &&
        payload.message.streaming === false &&
        payload.message.content?.trim()
      ) {
        clearTimeout(timeout)
        socket.close()
        resolve(payload.message.content)
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
  console.log('Public WebSocket and LLM proxy smoke test passed.')
} finally {
  await fetch(`${origin}/api/threads/${encodeURIComponent(thread.id)}`, { method: 'DELETE' })
}
