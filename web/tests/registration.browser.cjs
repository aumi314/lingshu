// 邮箱注册回归：邮件验证码、模式切换和发信账号管理。API 使用模拟响应，不发送真实邮件。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const path = require('node:path')

;(async () => {
  const base = 'http://127.0.0.1:25192'
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25192', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch(base)).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const state = { mode: 'email', user: null, sent: [], registrations: [], accounts: [], saves: [], tests: [] }
    const admin = { id: 1, username: 'admin', role: 'super_admin', balance: '1', enabled: true, user_group_ids: [1], preferences: { language: 'zh', theme: 'dark' }, created_at: '2026-11-08' }
    const errors = []
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    page.on('pageerror', (error) => errors.push(error.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const url = new URL(req.url()).pathname
      const json = (body, status = 200) => route.fulfill({ json: body, status })
      if (url === '/api/me') return state.user ? json({ user: state.user, cny_per_usd: '6.7' }) : json({ error: '请登录' }, 401)
      if (url === '/api/auth/registration') return json({ enabled: state.mode !== 'manual', mode: state.mode })
      if (url === '/api/auth/challenge') return json({ token: 'browser-test-challenge', difficulty: 0 })
      if (url === '/api/auth/email-code') { state.sent.push(req.postDataJSON()); return json({ retry_after: 60 }) }
      if (url === '/api/auth/register') { state.registrations.push(req.postDataJSON()); state.user = { ...admin, id: 2, role: 'user', username: 'newmember' }; return json({ status: 'created' }, 201) }
      if (url === '/api/auth/login') { state.user = { ...admin, id: 2, role: 'user', username: req.postDataJSON().username }; return json({ status: 'ok' }) }
      if (url === '/api/admin/settings') {
        if (req.method() === 'PUT') { if (req.postDataJSON().registration_mode) state.mode = req.postDataJSON().registration_mode; return json({}) }
        return json({ cny_per_usd: '6.7', payment_enabled: false, registration_mode: state.mode, api_base_url: '', deployment_mode: 'commercial_relay', fx_mode: 'manual', fx_status: { source: 'Frankfurter' }, call_limits: { user_concurrency: 10, user_rpm: 120, reserve_output_tokens: 8192 } })
      }
      if (url === '/api/admin/registration-codes') return json([])
      if (url === '/api/admin/mail-accounts') {
        if (req.method() === 'POST') {
          const input = req.postDataJSON(); state.saves.push(input)
          const account = { id: state.accounts.length + 1, email: input.email, client_id: input.client_id, enabled: input.enabled, sort_order: input.sort_order ?? 0, refresh_token_configured: true, checked_at: null, last_status: '', last_error: '' }
          state.accounts.push(account); return json(account, 201)
        }
        return json(state.accounts)
      }
      const mail = url.match(/^\/api\/admin\/mail-accounts\/(\d+)(?:\/(check|test))?$/)
      if (mail) {
        const account = state.accounts.find((item) => item.id === Number(mail[1]))
        if (mail[2] === 'check') { account.checked_at = '2026-11-08T06:00:00Z'; account.last_status = 'authorized'; return json(account) }
        if (mail[2] === 'test') { state.tests.push(req.postDataJSON()); return json({ status: 'submitted' }) }
        if (req.method() === 'PUT') { const input = req.postDataJSON(); state.saves.push(input); Object.assign(account, { email: input.email, client_id: input.client_id, enabled: input.enabled, sort_order: input.sort_order }); return json(account) }
        if (req.method() === 'DELETE') { state.accounts = state.accounts.filter((item) => item.id !== account.id); return json({}) }
      }
      return json([])
    })

    await page.goto(base + '/login')
    await page.getByRole('link', { name: '使用邮箱创建账号' }).waitFor()
    await page.goto(base + '/register')
    await page.getByLabel('邮箱', { exact: true }).fill('member@example.com')
    await page.getByRole('button', { name: '发送验证码' }).click()
    await page.getByRole('button', { name: /[0-9]+s/ }).waitFor()
    assert.equal(state.sent.length, 1)
    assert.equal(state.sent[0].email, 'member@example.com')
    assert.equal(state.sent[0].captcha_token, 'browser-test-challenge')
    assert.equal(typeof state.sent[0].captcha_nonce, 'string')
    assert.equal(await page.getByRole('button', { name: /[0-9]+s/ }).isDisabled(), true)
    await page.getByLabel('邮件验证码', { exact: true }).fill('123456')
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.deepEqual(state.registrations[0], { email: 'member@example.com', email_code: '123456', username: 'newmember', password: 'valid-password-123' })

    // 旧模式仍展示原注册码字段；关闭时不展示注册表单。
    state.user = null; state.mode = 'code'
    await page.goto(base + '/register')
    await page.getByLabel('注册码', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('邮箱', { exact: true }).count(), 0)
    state.mode = 'manual'
    await page.reload()
    await page.getByText('当前未开放自助注册', { exact: true }).waitFor()
    state.mode = 'email'
    await page.setViewportSize({ width: 375, height: 812 })
    await page.reload()
    await page.getByLabel('邮箱', { exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true)

    // 登录继续提交用户名和密码。
    await page.goto(base + '/login')
    await page.locator('input[autocomplete="username"]').fill('newmember')
    await page.locator('input[autocomplete="current-password"]').fill('valid-password-123')
    await page.getByRole('button', { name: /^登\s*录$/ }).click()
    await page.waitForURL('**/console')

    // 管理员添加账号、检查授权、发送测试邮件；已有授权保持空白。
    state.user = admin; state.mode = 'manual'
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(base + '/console/admin/system?tab=registration')
    await page.getByText('发信账号', { exact: true }).waitFor()
    assert.equal(await page.getByRole('radio', { name: /开放邮箱注册/ }).isDisabled(), true)
    await page.getByRole('button', { name: '添加账号', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: '添加发信账号', exact: true })
    await dialog.getByLabel('Outlook 邮箱', { exact: true }).fill('sender@outlook.com')
    await dialog.getByLabel('Client ID', { exact: true }).fill('test-client-id')
    await dialog.getByLabel('Refresh Token', { exact: true }).fill('test-refresh-secret')
    await dialog.getByRole('button', { name: /^保\s*存$/ }).click()
    await dialog.waitFor({ state: 'hidden' })
    await page.getByText('sender@outlook.com', { exact: true }).waitFor()
    await page.getByRole('button', { name: '检查授权', exact: true }).click()
    await page.getByText('授权检查通过', { exact: true }).waitFor()
    assert.equal(await page.getByRole('radio', { name: /开放邮箱注册/ }).isDisabled(), false)
    await page.getByRole('button', { name: '编辑', exact: true }).click()
    dialog = page.getByRole('dialog', { name: '编辑发信账号', exact: true })
    assert.equal(await dialog.getByLabel('Refresh Token', { exact: true }).inputValue(), '')
    assert.equal(await page.getByText('test-refresh-secret', { exact: true }).count(), 0)
    await dialog.getByRole('button', { name: /^取\s*消$/ }).click()
    await page.getByRole('button', { name: '测试邮件', exact: true }).click()
    dialog = page.getByRole('dialog', { name: '发送测试邮件', exact: true })
    assert.equal(await dialog.getByLabel('收件邮箱', { exact: true }).inputValue(), 'support@piqode.com')
    await dialog.getByRole('button', { name: '发送邮件', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.deepEqual(state.tests, [{ recipient: 'support@piqode.com' }])
    await page.getByRole('radio', { name: /开放邮箱注册/ }).check()
    await Promise.all([page.waitForResponse((response) => response.url().endsWith('/api/admin/settings') && response.request().method() === 'PUT'), page.getByRole('button', { name: /^保\s*存$/ }).click()])
    assert.equal(state.mode, 'email')
    await page.setViewportSize({ width: 375, height: 812 })
    await page.waitForTimeout(350)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true)

    // 未登录时沿用语言偏好，英文和手机布局同样可用。
    state.user = null
    await page.evaluate(() => localStorage.setItem('lingshu.prefs', JSON.stringify({ language: 'en' })))
    await page.goto(base + '/register')
    await page.getByLabel('Email', { exact: true }).waitFor()
    await page.getByLabel('Email code', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Send code', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Register and continue', exact: true }).waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true)
    assert.deepEqual(errors, [])
    console.log('PASS: email registration with PoW/cooldown, code/manual modes, mail account management, Chinese/English mobile layout, username/password login')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
