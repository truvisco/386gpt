import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { chromium } from 'playwright'
const browser = await chromium.launch({headless:true})
try {
 const context = await browser.newContext()
 let status = {state:'disconnected',runtime:'local'}, submitted
 await context.route('**/api/**', async route => {
  const r=route.request(),path=new URL(r.url()).pathname
  let data={}
  if(path==='/api/account') data={account:{id:'account-one',email:'user@example.com',service:false},environment:'ready'}
  else if(path==='/api/threads') data={threads:[]}
  else if(path==='/api/runtime') data={healthy:true,hostname:'local',provider:'Unsloth',model:'Gemma'}
  else if(path==='/api/settings/github') data=status
  else if(path==='/api/settings/github/device') data=status={state:'pending',runtime:'local',user_code:'ABCD-1234',verification_uri:'https://github.com/login/device'}
  else if(path==='/api/settings/github/token') {submitted=r.postDataJSON();data=status={state:'connected',runtime:'local',login:'fixture-user'}}
  else if(path==='/api/settings/github/disconnect') data=status={state:'disconnected',runtime:'local'}
  await route.fulfill({json:data})
 })
 const page=await context.newPage()
 await page.goto(process.env.UI_TEST_ORIGIN || 'http://127.0.0.1:21350')
 await page.getByRole('button',{name:'Settings',exact:true}).click()
 await page.getByText('Status: disconnected').waitFor()
 const button=page.getByRole('button',{name:'Sign in with GitHub',exact:true})
 const colors=await button.evaluate(e=>{const s=getComputedStyle(e);return [s.color,s.backgroundColor]})
 const luminance=s=>s.match(/[\d.]+/g).slice(0,3).map(Number).map(x=>x/255).map(x=>x<=.04045?x/12.92:((x+.055)/1.055)**2.4).reduce((sum,x,i)=>sum+x*[.2126,.7152,.0722][i],0)
 const levels=colors.map(luminance).sort((a,b)=>b-a)
 assert.ok((levels[0]+.05)/(levels[1]+.05)>=4.5,`CTA contrast too low: ${colors}`)
 await mkdir('.tmp',{recursive:true})
 await page.screenshot({path:'.tmp/github-settings.png'})
 await button.click()
 await page.getByText('ABCD-1234',{exact:true}).waitFor()
 assert.equal(await page.getByRole('link',{name:'Open GitHub device authorization'}).getAttribute('href'),'https://github.com/login/device')
 await page.getByRole('button',{name:'Cancel sign-in'}).click()
 await page.getByText('Status: disconnected').waitFor()
 await page.getByLabel('Or use a personal access token').fill('github_pat_fixture_secret')
 await page.getByRole('button',{name:'Save GitHub token'}).click()
 await page.getByText('fixture-user',{exact:true}).waitFor()
 assert.equal(submitted.token,'github_pat_fixture_secret')
 assert.equal(await page.getByLabel('Or use a personal access token').inputValue(),'')
 assert.equal(await page.locator('body').innerText().then(s=>s.includes('github_pat_fixture_secret')),false)
 assert.equal(await page.evaluate(()=>JSON.stringify(localStorage).includes('github_pat_fixture_secret')),false)
 await page.getByRole('button',{name:'Disconnect GitHub',exact:true}).click()
 await page.getByText('Status: disconnected').waitFor()
 await page.getByRole('button',{name:'Close settings'}).click()
 assert.equal(await page.getByRole('dialog').count(),0)
 console.log('PASS GitHub settings: device flow, cancellation, private token entry, disconnect, and CTA contrast.')
} finally {await browser.close()}
