import assert from 'node:assert/strict'
import worker from '../node_modules/.tmp/access-worker.mjs'
const env={ASSETS:{fetch:async()=>new Response('asset')}}
let forwarded
const originalFetch=globalThis.fetch
globalThis.fetch=async input=>{forwarded=input;return new Response('Sign in with Google',{status:401})}
try {
 assert.equal(await (await worker.fetch(new Request('https://386gpt.truvis.co/'),env)).text(),'asset')
 const request=new Request('https://386gpt.truvis.co/api/account',{headers:{Cookie:'unrelated=private; __Host-386gpt-session=opaque; CF_Authorization=edge; __Host-386gpt-oauth=state','Cf-Access-Jwt-Assertion':'forged','Cf-Access-Authenticated-User-Email':'owner@example.com'}})
 assert.equal((await worker.fetch(request,env)).status,401)
 assert.equal(forwarded.url,'https://api-386gpt.truvis.co/api/account')
 assert.equal(forwarded.headers.get('Cookie'),'__Host-386gpt-session=opaque; __Host-386gpt-oauth=state')
 assert.equal(forwarded.headers.get('Cf-Access-Jwt-Assertion'),null)
 assert.equal(forwarded.headers.get('Cf-Access-Authenticated-User-Email'),null)
 await worker.fetch(new Request('https://386gpt.truvis.co/ws?thread_id=abc',{headers:{Upgrade:'websocket',Origin:'https://386gpt.truvis.co'}}),env)
 assert.equal(forwarded.headers.get('Upgrade'),'websocket')
 assert.equal(forwarded.headers.get('Origin'),'https://386gpt.truvis.co')
 assert.equal(forwarded.redirect,'manual')
 console.log('PASS Worker: public login assets, fixed API origin, session cookie forwarding, identity-header stripping, WebSocket upgrade.')
} finally {globalThis.fetch=originalFetch}
