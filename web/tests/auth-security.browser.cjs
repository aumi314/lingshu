// 登录被限流（HTTP 429）时的前端表现：原样显示服务端给的提示，并且不自动重试。
// 接口用模拟响应，不打真实后端。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const path = require('node:path')

;(async () => {
  const base = 'http://127.0.0.1:25193'
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25193', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch(base)).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const message = '请求过于频繁，请 12 秒后再试'
    const state = { logins: [], challenges: 0 }
    const errors = []
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    page.on('pageerror', (error) => errors.push(error.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const url = new URL(req.url()).pathname
      const json = (body, status = 200) => route.fulfill({ json: body, status })
      if (url === '/api/auth/registration') return json({ enabled: false })
      if (url === '/api/me') return json({ error: '请登录' }, 401)
      if (url === '/api/auth/challenge') {
        state.challenges++
        return json({ token: 'browser-test-challenge', difficulty: 0 })
      }
      if (url === '/api/auth/login') {
        state.logins.push(req.postDataJSON())
        return route.fulfill({
          status: 429,
          headers: { 'Content-Type': 'application/json', 'Retry-After': '12' },
          body: JSON.stringify({ error: message }),
        })
      }
      return json({})
    })

    await page.goto(base + '/login')
    await page.getByPlaceholder('用户名').fill('bob')
    await page.getByPlaceholder('密码').fill('password-1')
    await page.getByRole('button', { name: /登\s*录/ }).click()

    // 服务端给的提示原样显示出来。
    await page.getByText(message, { exact: true }).waitFor()
    assert.equal(state.logins.length, 1)
    assert.deepEqual(state.logins[0], { username: 'bob', password: 'password-1' })

    // 不自动重试：等一会儿也不该再发登录请求，也不该转去要人机验证。
    await new Promise((resolve) => setTimeout(resolve, 3000))
    assert.equal(state.logins.length, 1, '429 之后不该自动重试登录')
    assert.equal(state.challenges, 0, '429 不该触发人机验证')
    assert.deepEqual(errors, [])
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
