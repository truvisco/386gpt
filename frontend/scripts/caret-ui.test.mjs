import { chromium } from 'playwright'
import assert from 'node:assert/strict'

const browser = await chromium.launch({ headless: true })
try {
  const page = await browser.newPage()
  await page.route('**/api/**', route => {
    const path = new URL(route.request().url()).pathname
    const json = path === '/api/account' ? { account: { id: 'test', email: 'test@example.com' }, environment: 'ready' }
      : path === '/api/runtime' ? { model: 'Test', provider: 'Test', healthy: true } : { threads: [] }
    return route.fulfill({ json })
  })
  await page.goto(process.env.UI_TEST_ORIGIN || 'http://127.0.0.1:21350')
  await page.evaluate(() => document.fonts.ready)
  const input = page.getByRole('textbox', { name: 'Message', exact: true })
  const measure = () => page.evaluate(() => {
    const textarea = document.querySelector('textarea')
    const mirror = document.querySelector('.caret-mirror')
    const marker = mirror.querySelector('span')
    const caret = document.querySelector('.input-block-caret')
    const style = getComputedStyle(textarea)
    const canvas = document.createElement('canvas').getContext('2d')
    canvas.font = style.font
    const advance = canvas.measureText('M').width
    const columns = Math.floor((textarea.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)) / advance)
    return {
      font: style.fontSize, mirrorFont: getComputedStyle(mirror).fontSize, markerFont: getComputedStyle(marker).fontSize,
      left: parseFloat(caret.style.left), top: parseFloat(caret.style.top), width: caret.getBoundingClientRect().width,
      advance, columns, lineHeight: parseFloat(style.lineHeight), scrollTop: textarea.scrollTop,
    }
  })
  for (const length of [1, 10, 60]) {
    await input.fill('M'.repeat(length))
    const actual = await measure()
    assert.equal(actual.mirrorFont, actual.font, 'Mirror must match textarea font despite theme overrides')
    assert.equal(actual.markerFont, actual.font, 'Marker must inherit textarea font')
    assert.ok(Math.abs(actual.left - actual.advance * length) < 1, `Caret drifts at ${length} characters`)
    assert.ok(Math.abs(actual.width - actual.advance) < 1, 'Block should be one character wide')
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await input.fill('M')
  const first = await measure()
  const length = first.columns * 3 + 7
  await input.fill('M'.repeat(length))
  const wrapped = await measure()
  assert.ok(Math.abs(wrapped.left - 7 * wrapped.advance) < 1, 'Caret aligns after narrow-screen wrapping')
  assert.ok(Math.abs(wrapped.top + wrapped.scrollTop - first.top - first.scrollTop - 3 * wrapped.lineHeight) < 1, 'Caret follows wrapped lines and scrolling')
  await input.fill('M\nMMMMMMM')
  const multiline = await measure()
  assert.ok(Math.abs(multiline.left - 7 * multiline.advance) < 1, 'Caret aligns after a newline')
  assert.ok(Math.abs(multiline.top + multiline.scrollTop - first.top - first.scrollTop - multiline.lineHeight) < 1, 'Caret follows explicit newlines')
  console.log('PASS: caret font parity, long-line alignment, character width, mobile wrapping, scrolling, and newlines')
} finally {
  await browser.close()
}
