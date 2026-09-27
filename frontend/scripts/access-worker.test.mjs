import assert from 'node:assert/strict'
import { generateKeyPair, exportJWK, SignJWT } from 'jose'
import worker from '../node_modules/.tmp/access-worker.mjs'
const {privateKey,publicKey}=await generateKeyPair('RS256')
const jwk={...await exportJWK(publicKey),kid:'test',alg:'RS256',use:'sig'}
const env={ACCESS_ISSUER:'https://test.cloudflareaccess.com',ACCESS_AUDIENCE:'app',ACCESS_SERVICE_CLIENT_ID:'ci.access',ASSETS:{fetch:async()=>new Response('asset')}}
let forwarded
const originalFetch=globalThis.fetch
globalThis.fetch=async(input)=>{
 const url=String(input.url??input)
 if(url.endsWith('/cdn-cgi/access/certs'))return Response.json({keys:[jwk]})
 forwarded=input;return Response.json({ok:true})
}
const token=async(overrides={},options={})=>new SignJWT({type:'app',email:'mauriciootta@gmail.com',...overrides}).setProtectedHeader({alg:'RS256',kid:'test'}).setIssuer(options.issuer??env.ACCESS_ISSUER).setAudience(options.audience??'app').setExpirationTime(options.expires??'1h').sign(privateKey)
const request=(path,raw)=>new Request('https://386gpt.truvis.co'+path,{headers:raw?{'Cf-Access-Jwt-Assertion':raw,'Cookie':'unrelated=private'}:{}})
try{
 for(const raw of ['', 'forged',await token({email:'other@example.com'}),await token({}, {audience:'other'}),await token({}, {issuer:'https://other.cloudflareaccess.com'}),await token({}, {expires:946684800})]){
  assert.equal((await worker.fetch(request('/api/threads',raw),env)).status,401)
 }
 const owner=await token()
 assert.equal(await (await worker.fetch(request('/',owner),env)).text(),'asset')
 assert.equal((await worker.fetch(request('/api/threads',owner),env)).status,200)
 assert.equal(forwarded.url,'https://api-386gpt.truvis.co/api/threads')
 assert.equal(forwarded.headers.get('Cookie'),'CF_Authorization='+owner)
 assert.equal((await worker.fetch(request('/',await token({email:undefined,common_name:'ci.access'})),env)).status,200)
 assert.equal((await worker.fetch(request('/',owner),{...env,ACCESS_AUDIENCE:''})).status,401)
 console.log('PASS Worker: owner/service identity, signature, issuer, audience, expiry, fail-closed config, fixed upstream and verified token forwarding.')
} finally {globalThis.fetch=originalFetch}
