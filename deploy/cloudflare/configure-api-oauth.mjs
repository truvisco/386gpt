// Switch edge login off only after the API proves it has its own Google OAuth
// flow and rejects anonymous access. Google OAuth is owned by the Go backend.
import { execFileSync } from 'node:child_process'
const appId=process.env.ACCESS_APP_ID
if(!/^[a-f0-9-]+$/.test(appId ?? '')) throw new Error('ACCESS_APP_ID is required')
const checks=JSON.parse(execFileSync('ssh',['-o','BatchMode=yes','web1','python3 -'],{encoding:'utf8',input:`
import json,urllib.request,urllib.error
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None
opener=urllib.request.build_opener(NoRedirect)
results={}
for path in ['/api/account','/api/auth/google']:
 try:r=opener.open('http://127.0.0.1:20386'+path,timeout=10)
 except urllib.error.HTTPError as e:r=e
 results[path]={'status':r.status,'location':r.headers.get('Location','')}
print(json.dumps(results))
`}))
if(checks['/api/account'].status!==401 || checks['/api/auth/google'].status!==302 || !checks['/api/auth/google'].location.startsWith('https://accounts.google.com/o/oauth2/v2/auth?')) throw new Error('API Google OAuth is not enforced; refusing to change edge policy')
const root=`https://api.cloudflare.com/client/v4/accounts/${process.env.CLOUDFLARE_ACCOUNT_ID}/access/apps/${appId}`
const headers={Authorization:`Bearer ${process.env.CLOUDFLARE_API_TOKEN}`,'Content-Type':'application/json'}
const response=await fetch(root,{headers}),document=await response.json()
if(!document.success)throw new Error('Cannot read existing Access app')
const app=document.result
const update={name:'386GPT API OAuth',type:'self_hosted',domain:app.domain,destinations:app.destinations,session_duration:'8h',app_launcher_visible:false,policies:[{name:'Authentication in 386GPT API',decision:'bypass',precedence:1,include:[{everyone:{}}]}]}
const result=await fetch(root,{method:'PUT',headers,body:JSON.stringify(update)})
if(!result.ok || !(await result.json()).success)throw new Error('Cannot configure API-owned login')
console.log('Cloudflare routes to API-owned Google OAuth; anonymous API access remains denied.')
