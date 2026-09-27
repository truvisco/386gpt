// Run through with-cloudflare-env.sh; writes secrets only to an owner-only file.
import { writeFileSync, existsSync, readFileSync, chmodSync } from 'node:fs'
const output = process.argv[2]
if (!output) throw new Error('Usage: configure-access.mjs OUTPUT_JSON')
const root = `https://api.cloudflare.com/client/v4/accounts/${process.env.CLOUDFLARE_ACCOUNT_ID}/access/`
async function api(path, method='GET', body) {
 const response = await fetch(root+path,{method,headers:{Authorization:`Bearer ${process.env.CLOUDFLARE_API_TOKEN}`,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined})
 const data=await response.json(); if(!response.ok || !data.success) throw new Error(`${method} ${path}: ${JSON.stringify(data.errors)}`)
 return data.result
}
if (existsSync(output)) chmodSync(output,0o600)
let saved=existsSync(output)?JSON.parse(readFileSync(output,'utf8')):{}
const save=()=>writeFileSync(output,JSON.stringify(saved,null,2)+'\n',{mode:0o600})
const organization=await api('organizations')
if (!organization.auth_domain) throw new Error('Cloudflare Zero Trust organization has no team domain')
saved.issuer='https://'+organization.auth_domain
save()
const idps=await api('identity_providers')
const otp=idps.find(x=>x.type==='onetimepin') || await api('identity_providers','POST',{name:'386GPT email code',type:'onetimepin',config:{}})
if(!saved.service) {saved.service=await api('service_tokens','POST',{name:'386GPT Jenkins',duration:'8760h'});save()}
const apps=await api('apps'), existing=apps.find(x=>x.name==='386GPT owner')
const config={name:'386GPT owner',type:'self_hosted',domain:'386gpt.truvis.co',destinations:[{type:'public',uri:'386gpt.truvis.co'},{type:'public',uri:'api-386gpt.truvis.co'}],session_duration:'8h',allowed_idps:[otp.id],auto_redirect_to_identity:true,app_launcher_visible:false,
 policies:[{name:'Owner only',decision:'allow',precedence:1,include:[{email:{email:'mauriciootta@gmail.com'}}]},{name:'Jenkins smoke',decision:'non_identity',precedence:2,include:[{service_token:{token_id:saved.service.id}}]}]}
saved.app=await api(existing?`apps/${existing.id}`:'apps',existing?'PUT':'POST',config);save()
console.log(`Access application ready: ${saved.app.id}; audience ${saved.app.aud}. Credentials saved privately.`)
