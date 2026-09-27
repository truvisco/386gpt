import { createRemoteJWKSet, jwtVerify } from 'jose'
type AccessEnv = Env & {
  ASSETS: Fetcher
  ACCESS_ISSUER: string
  ACCESS_AUDIENCE: string
  ACCESS_SERVICE_CLIENT_ID?: string
}
const sets = new Map<string, ReturnType<typeof createRemoteJWKSet>>()
export default {
  async fetch(request: Request, env: AccessEnv) {
    if (import.meta.env.DEV) return env.ASSETS.fetch(request)
    try {
      if (!/^https:\/\/[a-zA-Z0-9-]+\.cloudflareaccess\.com$/.test(env.ACCESS_ISSUER) || !env.ACCESS_AUDIENCE) throw new Error('Access configuration missing')
      let keys = sets.get(env.ACCESS_ISSUER)
      if (!keys) { keys = createRemoteJWKSet(new URL(`${env.ACCESS_ISSUER}/cdn-cgi/access/certs`)); sets.set(env.ACCESS_ISSUER, keys) }
      const { payload } = await jwtVerify(request.headers.get('Cf-Access-Jwt-Assertion') ?? '', keys, { issuer: env.ACCESS_ISSUER, audience: env.ACCESS_AUDIENCE, algorithms: ['RS256'], requiredClaims: ['exp', 'type'] })
      if (payload.type !== 'app' || !(payload.email === 'mauriciootta@gmail.com' || (env.ACCESS_SERVICE_CLIENT_ID && payload.common_name === env.ACCESS_SERVICE_CLIENT_ID))) throw new Error('Identity denied')
    } catch { return new Response('Owner login required', { status: 401, headers: { 'Cache-Control': 'no-store' } }) }
    const url = new URL(request.url)
    if (url.pathname.startsWith('/api/') || url.pathname === '/ws' || url.pathname === '/health') {
      url.protocol = 'https:'; url.host = 'api-386gpt.truvis.co'
      const headers = new Headers(request.headers)
      headers.set('Cookie', `CF_Authorization=${request.headers.get('Cf-Access-Jwt-Assertion')}`)
      return fetch(new Request(url, { method: request.method, headers, body: ['GET', 'HEAD'].includes(request.method) ? undefined : request.body, redirect: 'manual' }))
    }
    return env.ASSETS.fetch(request)
  },
} satisfies ExportedHandler<AccessEnv>
