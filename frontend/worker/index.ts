// OAuth, session validation and account authorization live in the Go API.
// The Worker serves public assets and forwards same-origin API/WebSocket traffic.
export default {
  async fetch(request: Request, env: Env & { ASSETS: Fetcher }) {
    if (import.meta.env.DEV) return env.ASSETS.fetch(request)
    const url = new URL(request.url)
    if (url.pathname.startsWith('/api/') || url.pathname === '/ws' || url.pathname === '/health') {
      url.protocol = 'https:'
      url.host = 'api-386gpt.truvis.co'
      const headers = new Headers(request.headers)
      // Only app session cookies cross to the fixed API origin. Never turn an
      // edge identity header or unrelated browser cookie into app authorization.
      const cookies = (headers.get('Cookie') ?? '').split(';').map(c => c.trim()).filter(c => /^(?:__Host-386gpt-session|__Host-386gpt-oauth)=/.test(c))
      headers.delete('Cookie')
      if (cookies.length) headers.set('Cookie', cookies.join('; '))
      headers.delete('Cf-Access-Jwt-Assertion')
      headers.delete('Cf-Access-Authenticated-User-Email')
      return fetch(new Request(url, { method: request.method, headers, body: ['GET', 'HEAD'].includes(request.method) ? undefined : request.body, redirect: 'manual' }))
    }
    return env.ASSETS.fetch(request)
  },
} satisfies ExportedHandler<Env & { ASSETS: Fetcher }>
