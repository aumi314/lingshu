// 注册开关回归：开放注册总开关 + 注册码验证/邮箱认证两项可选条件，四种组合的表单和提交字段，以及发信账号管理。
// API 使用模拟响应，不发送真实邮件。
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
    const state = { open: false, code: false, email: false, legacy: false, user: null, sent: [], registrations: [], accounts: [], saves: [], tests: [], savesSettings: [] }
    const admin = { id: 1, username: 'admin', role: 'super_admin', balance: '1', enabled: true, user_group_ids: [1], preferences: { language: 'zh', theme: 'dark' }, created_at: '2026-11-08' }
    const errors = []
    // 旧接口只有 mode，用来验证前端的兼容解析。
    const legacyMode = () => (!state.open ? 'manual' : state.code && state.email ? 'both' : state.code ? 'code' : state.email ? 'email' : 'open')
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    page.on('pageerror', (error) => errors.push(error.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const url = new URL(req.url()).pathname
      const json = (body, status = 200) => route.fulfill({ json: body, status })
      if (url === '/api/me') return state.user ? json({ user: state.user, cny_per_usd: '6.7' }) : json({ error: '请登录' }, 401)
      if (url === '/api/auth/registration') {
        if (state.legacy) return json({ enabled: state.open, mode: legacyMode() })
        return json({ enabled: state.open, code_required: state.code, email_required: state.email })
      }
      // 没有配置协议时，注册页不出现勾选框。
      if (url === '/api/auth/legal') return json({ user_agreement: '', privacy_policy: '', version: 'v-empty' })
      if (url === '/api/auth/challenge') return json({ token: 'browser-test-challenge', difficulty: 0 })
      if (url === '/api/auth/email-code') { state.sent.push(req.postDataJSON()); return json({ retry_after: 60 }) }
      if (url === '/api/auth/register') { state.registrations.push(req.postDataJSON()); state.user = { ...admin, id: 2, role: 'user', username: 'newmember' }; return json({ status: 'created' }, 201) }
      if (url === '/api/auth/login') { state.user = { ...admin, id: 2, role: 'user', username: req.postDataJSON().username }; return json({ status: 'ok' }) }
      if (url === '/api/admin/settings') {
        if (req.method() === 'PUT') {
          const body = req.postDataJSON()
          state.savesSettings.push(body)
          if ('registration_enabled' in body) state.open = body.registration_enabled
          if ('registration_code_required' in body) state.code = body.registration_code_required
          if ('registration_email_required' in body) state.email = body.registration_email_required
          return json({})
        }
        const settings = { cny_per_usd: '6.7', payment_enabled: false, api_base_url: '', deployment_mode: 'commercial_relay', fx_mode: 'manual', fx_status: { source: 'Frankfurter' }, call_limits: { user_concurrency: 10, user_rpm: 120, reserve_output_tokens: 8192 } }
        if (state.legacy) return json({ ...settings, registration_mode: legacyMode() })
        return json({ ...settings, registration_enabled: state.open, registration_code_required: state.code, registration_email_required: state.email })
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

    const noScroll = () => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)

    // 组合一：关闭开放注册。登录页隐藏注册入口，注册页提示未开放。
    await page.goto(base + '/login')
    assert.equal(await page.getByRole('link', { name: '创建账号' }).count(), 0)
    await page.goto(base + '/register')
    await page.getByText('当前未开放自助注册', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '注册并进入' }).count(), 0)

    // 组合二：开放注册但不勾任何条件，只提交用户名和密码。
    state.open = true
    await page.goto(base + '/login')
    await page.getByRole('link', { name: '创建账号' }).waitFor()
    await page.goto(base + '/register')
    await page.getByLabel('用户名', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('注册码', { exact: true }).count(), 0)
    assert.equal(await page.getByLabel('邮箱', { exact: true }).count(), 0)
    assert.equal(await page.getByRole('tab').count(), 0)
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.deepEqual(state.registrations[0], { username: 'newmember', password: 'valid-password-123' })

    // 组合三：只要求注册码。
    state.user = null; state.code = true
    await page.goto(base + '/register')
    await page.getByLabel('注册码', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('邮箱', { exact: true }).count(), 0)
    await page.getByLabel('注册码', { exact: true }).fill('TEAMCODE')
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.deepEqual(state.registrations[1], { code: 'TEAMCODE', username: 'newmember', password: 'valid-password-123' })

    // 组合四：只要求邮箱认证，发码走 PoW 和倒计时。
    state.user = null; state.code = false; state.email = true
    await page.goto(base + '/register')
    await page.getByLabel('邮箱', { exact: true }).fill('member@example.com')
    assert.equal(await page.getByLabel('注册码', { exact: true }).count(), 0)
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
    assert.deepEqual(state.registrations[2], { email: 'member@example.com', email_code: '123456', username: 'newmember', password: 'valid-password-123' })

    // 组合五：两项同时要求，同一张表单同时提交注册码和邮箱验证码，不带 method。
    state.user = null; state.code = true
    await page.goto(base + '/register')
    await page.getByLabel('邮箱', { exact: true }).waitFor()
    await page.getByLabel('注册码', { exact: true }).waitFor()
    assert.equal(await page.getByRole('tab').count(), 0)
    await page.getByLabel('邮箱', { exact: true }).fill('both@example.com')
    await page.getByRole('button', { name: '发送验证码' }).click()
    await page.getByRole('button', { name: /[0-9]+s/ }).waitFor()
    await page.getByLabel('邮件验证码', { exact: true }).fill('123456')
    await page.getByLabel('注册码', { exact: true }).fill('TEAMCODE')
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.deepEqual(state.registrations[3], { email: 'both@example.com', email_code: '123456', code: 'TEAMCODE', username: 'newmember', password: 'valid-password-123' })
    assert.equal(state.registrations.every((body) => !('method' in body)), true)
    assert.equal(state.sent.length, 2)

    // 未登录时用户名密码登录仍按原样提交。
    state.user = null
    await page.goto(base + '/login')
    await page.waitForTimeout(500)
    await page.locator('input[autocomplete="username"]').fill('newmember')
    await page.locator('input[autocomplete="current-password"]').fill('valid-password-123')
    await page.getByRole('button', { name: /^登\s*录$/ }).click()
    await page.waitForURL('**/console')

    // 旧接口只返回 mode 时前端仍然能推断字段要求。
    state.user = null; state.legacy = true; state.open = true; state.code = true; state.email = true
    await page.goto(base + '/register')
    await page.getByLabel('注册码', { exact: true }).waitFor()
    await page.getByLabel('邮箱', { exact: true }).waitFor()
    state.code = true; state.email = false
    await page.reload()
    await page.getByLabel('注册码', { exact: true }).waitFor()
    assert.equal(await page.getByLabel('邮箱', { exact: true }).count(), 0)
    state.code = false; state.email = false; state.open = false
    await page.reload()
    await page.getByText('当前未开放自助注册', { exact: true }).waitFor()
    state.legacy = false

    await page.setViewportSize({ width: 375, height: 812 })
    state.open = true; state.code = true; state.email = true
    await page.reload()
    await page.getByLabel('邮箱', { exact: true }).waitFor()
    assert.equal(await noScroll(), true)

    // 管理员：开关关闭时不显示条件项；打开后注册码可勾选，邮箱认证需要先有授权通过的账号。
    state.user = admin
    state.open = false; state.code = false; state.email = false
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(base + '/console/admin/system?tab=registration')
    await page.getByText('发信账号', { exact: true }).waitFor()
    const openSwitch = page.getByRole('switch', { name: '开放注册' })
    const codeBox = page.getByRole('checkbox', { name: /注册码验证/ })
    const emailBox = page.getByRole('checkbox', { name: /邮箱认证/ })
    const switchOn = async (want) => {
      await page.waitForFunction((expected) => document.querySelector('[aria-label="开放注册"]')?.getAttribute('aria-checked') === expected, String(want))
    }
    const saveSettings = async () => {
      const btn = page.locator('.ant-card').filter({ has: page.getByText('注册方式', { exact: true }) }).locator('button').filter({ hasText: /保\s*存/ })
      await page.waitForFunction(() => !document.querySelector('#root[aria-hidden="true"]'))
      await btn.first().waitFor({ state: 'visible' })
      const [req] = await Promise.all([page.waitForResponse((r) => r.url().endsWith('/api/admin/settings') && r.request().method() === 'PUT'), btn.first().click()])
      return JSON.parse(req.request().postData())
    }
    await switchOn(false)
    assert.equal(await codeBox.count(), 0)
    assert.equal(await emailBox.count(), 0)
    assert.equal(await page.getByRole('checkbox', { name: /手动/ }).count(), 0)

    // 打开总开关：两项默认都不勾，保存后只写开关本身。
    await openSwitch.click()
    assert.equal(await codeBox.isChecked(), false)
    assert.equal(await emailBox.isChecked(), false)
    assert.equal(await emailBox.isDisabled(), true)
    assert.deepEqual(await saveSettings(), { registration_enabled: true, registration_code_required: false, registration_email_required: false })

    // 只勾注册码：注册码可以随时勾选，邮箱认证要等有授权通过的发信账号。
    await codeBox.check()
    assert.deepEqual(await saveSettings(), { registration_enabled: true, registration_code_required: true, registration_email_required: false })
    assert.equal(await page.getByText('开启邮箱认证前', { exact: false }).count(), 1)

    // 管理员添加账号、检查授权、发送测试邮件；已有授权保持空白。
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
    assert.equal(await emailBox.isDisabled(), false)
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

    // 邮箱认证和注册码可以同时勾选，两个条件都要求。
    await emailBox.check()
    assert.deepEqual(await saveSettings(), { registration_enabled: true, registration_code_required: true, registration_email_required: true })
    assert.deepEqual([state.open, state.code, state.email], [true, true, true])
    await page.reload()
    await page.getByText('发信账号', { exact: true }).waitFor()
    await switchOn(true)
    assert.equal(await codeBox.isChecked(), true)
    assert.equal(await emailBox.isChecked(), true)

    // 关闭总开关：条件项收起并保留偏好，重新开启时沿用原验证要求。
    await openSwitch.click()
    assert.equal(await codeBox.count(), 0)
    assert.equal(await emailBox.count(), 0)
    assert.deepEqual(await saveSettings(), { registration_enabled: false, registration_code_required: true, registration_email_required: true })
    assert.deepEqual([state.open, state.code, state.email], [false, true, true])

    await page.setViewportSize({ width: 375, height: 812 })
    await page.waitForTimeout(350)
    assert.equal(await noScroll(), true)

    // 未登录时沿用语言偏好，英文和手机布局同样可用。
    state.user = null; state.open = true
    await page.evaluate(() => localStorage.setItem('lingshu.prefs', JSON.stringify({ language: 'en' })))
    await page.goto(base + '/register')
    await page.getByLabel('Email', { exact: true }).waitFor()
    await page.getByLabel('Email code', { exact: true }).waitFor()
    await page.getByLabel('Registration code', { exact: true }).waitFor()
    await page.getByRole('button', { name: 'Send code', exact: true }).waitFor()
    await page.getByRole('button', { name: 'Register and continue', exact: true }).waitFor()
    assert.equal(await noScroll(), true)
    assert.deepEqual(errors, [])
    console.log('PASS: open-registration switch with optional code/email conditions (four combinations, AND form), legacy mode fallback, mail account management, Chinese/English mobile layout, username/password login')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
