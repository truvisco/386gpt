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
    const timeout = setTimeout(() => reject(new Error('WebSocket ready event timed out')), 15_000)
    socket.addEventListener('message', (event) => {
      const payload = JSON.parse(event.data)
      if (payload.type === 'ready') {
        clearTimeout(timeout)
        socket.close()
        resolve()
      }
    })
    socket.addEventListener('error', () => {
      clearTimeout(timeout)
      reject(new Error('WebSocket connection failed'))
    })
  })
  console.log('Public WebSocket smoke test passed.')
} finally {
  await fetch(`${origin}/api/threads/${encodeURIComponent(thread.id)}`, { method: 'DELETE' })
}
