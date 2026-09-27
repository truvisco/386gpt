import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const browser=await chromium.launch({headless:true})
try {
 const context=await browser.newContext()
 let state='signed-out'
 await context.route('**/api/account',route=>state==='signed-out'?route.fulfill({status:401,json:{error:'Sign in with Google'}}):route.fulfill({json:{account:{id:'account-one',email:'one@gmail.com'},environment:state}}))
 const page=await context.newPage()
 await page.goto(process.env.UI_TEST_ORIGIN || 'http://127.0.0.1:15173')
 const login=page.getByRole('link',{name:'Sign in with Google'})
 await login.waitFor();assert.equal(await login.getAttribute('href'),'/api/auth/google')
 assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).count(),0)
 state='preparing';await page.reload();await page.getByText('Preparing your private workspace…',{exact:true}).waitFor()
 assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).count(),0)
 console.log('PASS: signed-out Google login and pending-environment screen do not expose conversation UI')
} finally {await browser.close()}
