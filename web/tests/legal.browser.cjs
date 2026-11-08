// 用户协议 / 隐私政策：注册页强制勾选、拉取失败时关闭注册入口、公开文档页保留换行、管理端保存。
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
    const state = {
      open: true,
      agreement: '',
      privacy: '',
      legalFails: false,
      user: null,
      registrations: [],
      settings: { cny_per_usd: '6.7', payment_enabled: false, api_base_url: '', deployment_mode: 'commercial_relay', fx_mode: 'manual', fx_status: { source: 'Frankfurter' }, call_limits: { user_concurrency: 10, user_rpm: 120, reserve_output_tokens: 8192 } },
      settingsSaves: [],
    }
    const admin = { id: 1, username: 'admin', role: 'super_admin', balance: '1', enabled: true, user_group_ids: [1], preferences: { language: 'zh', theme: 'dark' }, created_at: '2026-11-08' }
    const version = () => {
      // 版本号由两份正文算出，这里只关心前端原样回传，用固定值即可
      return state.agreement + state.privacy ? 'v-' + (state.agreement.length + state.privacy.length) : 'v-empty'
    }
    const errors = []
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    page.on('pageerror', (error) => errors.push(error.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const url = new URL(req.url()).pathname
      const json = (body, status = 200) => route.fulfill({ json: body, status })
      if (url === '/api/me') return state.user ? json({ user: state.user, cny_per_usd: '6.7' }) : json({ error: '请登录' }, 401)
      if (url === '/api/auth/registration') return json({ enabled: state.open, code_required: false, email_required: false })
      if (url === '/api/auth/legal') {
        if (state.legalFails) return json({ error: '服务暂时不可用' }, 500)
        if (state.legalShape === 'array') return json([])
        return json({ user_agreement: state.agreement, privacy_policy: state.privacy, version: version() })
      }
      if (url === '/api/auth/register') {
        const body = req.postDataJSON()
        // 后端契约：配置了协议就必须勾选同意且版本号一致
        if (state.agreement || state.privacy) {
          if (body.legal_accepted !== true) return json({ error: '请先阅读并同意用户协议和隐私政策' }, 400)
          if (body.legal_version !== version()) return json({ error: '协议版本已更新，请重新阅读后同意' }, 400)
        }
        state.registrations.push(body)
        state.user = { ...admin, id: 2, role: 'user', username: body.username }
        return json({ status: 'created' }, 201)
      }
      if (url === '/api/admin/settings') {
        if (req.method() === 'PUT') {
          const body = req.postDataJSON()
          state.settingsSaves.push(body)
          if ('user_agreement' in body) state.agreement = body.user_agreement
          if ('privacy_policy' in body) state.privacy = body.privacy_policy
          return json({})
        }
        return json({ ...state.settings, user_agreement: state.agreement, privacy_policy: state.privacy })
      }
      return json([])
    })

    const noScroll = () => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)

    // 没有配置任何协议：注册页不出现勾选框，注册请求也不带同意字段。
    await page.goto(base + '/register')
    await page.getByLabel('用户名', { exact: true }).waitFor()
    assert.equal(await page.getByRole('checkbox', { name: /我已阅读并同意/ }).count(), 0)
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.deepEqual(state.registrations[0], { username: 'newmember', password: 'valid-password-123' })

    // 配置了协议：勾选框默认未勾选，不勾选不能提交。
    state.user = null
    state.agreement = '第一条 用户协议正文\n第二条 说明'
    state.privacy = '隐私政策正文'
    await page.goto(base + '/register')
    const consent = page.getByRole('checkbox', { name: /我已阅读并同意/ })
    await consent.waitFor()
    assert.equal(await consent.isChecked(), false)
    await page.getByLabel('用户名', { exact: true }).fill('newmember')
    await page.getByLabel('密码', { exact: true }).fill('valid-password-123')
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.getByText('请先阅读并同意用户协议和隐私政策', { exact: true }).waitFor()
    assert.equal(state.registrations.length, 1)

    // 勾选后才能提交，请求带上 legal_accepted 和 legal_version。
    await consent.check()
    await page.getByRole('button', { name: '注册并进入' }).click()
    await page.waitForURL('**/console')
    assert.equal(state.registrations[1].legal_accepted, true)
    assert.equal(state.registrations[1].legal_version, version())

    // 勾选框旁边的两个链接分别指向文档页。
    state.user = null
    await page.goto(base + '/register')
    await page.getByRole('link', { name: '《用户协议》', exact: true }).waitFor()
    assert.equal(await page.getByRole('link', { name: '《隐私政策》', exact: true }).count(), 1)
    const termsLink = page.getByRole('link', { name: '《用户协议》', exact: true })
    assert.equal(await termsLink.getAttribute('target'), '_blank')
    assert.equal(((await termsLink.getAttribute('rel')) || '').includes('noopener'), true)
    // 文档在新标签打开，注册页已经填的内容不受影响。
    await page.getByLabel('用户名', { exact: true }).fill('keepme')
    const [popup] = await Promise.all([page.waitForEvent('popup'), termsLink.click()])
    await popup.close()
    assert.equal(page.url(), base + '/register')
    assert.equal(await page.getByLabel('用户名', { exact: true }).inputValue(), 'keepme')

    // 协议拉取失败：注册入口关闭，不允许在没看到条款的情况下注册。
    state.legalFails = true
    await page.goto(base + '/register')
    await page.getByText('无法读取用户协议和隐私政策，暂时不能注册', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '注册并进入' }).isDisabled(), true)
    assert.equal(state.registrations.length, 2)
    // 点重试能重新读到条款，之后正常注册。
    state.legalFails = false
    await page.getByRole('button', { name: '重新加载' }).click()
    await page.getByRole('checkbox', { name: /我已阅读并同意/ }).waitFor()
    assert.equal(await page.getByRole('button', { name: '注册并进入' }).isDisabled(), false)
    state.legalFails = false

    // 返回结构不对（不是对象）时同样按读不到处理，关掉注册入口。
    state.legalShape = 'array'
    await page.goto(base + '/register')
    await page.getByText('无法读取用户协议和隐私政策，暂时不能注册', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '注册并进入' }).isDisabled(), true)
    assert.equal(state.registrations.length, 2)
    state.legalShape = null

    // 公开文档页：未登录可访问，纯文本原样保留换行，不当成 HTML 渲染。
    await page.goto(base + '/terms')
    await page.getByText('第一条 用户协议正文', { exact: false }).waitFor()
    const content = page.locator('div[style*="pre-wrap"]').first()
    const agreementText = await content.innerText()
    assert.equal(agreementText.includes('第一条 用户协议正文\n第二条 说明'), true)
    assert.equal(await content.evaluate((el) => el.children.length), 0)
    await page.getByRole('link', { name: '返回注册' }).waitFor()
    await page.goto(base + '/privacy')
    await page.getByText('隐私政策正文', { exact: false }).waitFor()
    assert.equal(await page.getByRole('link', { name: '返回注册' }).count(), 1)

    // 管理端：两张卡片各自保存自己的正文。
    state.user = admin
    await page.goto(base + '/console/admin/system')
    await page.getByText('用户协议', { exact: true }).waitFor()
    const agreementCard = page.locator('.ant-card').filter({ has: page.getByText('用户协议', { exact: true }) })
    const privacyCard = page.locator('.ant-card').filter({ has: page.getByText('隐私政策', { exact: true }) })
    await agreementCard.locator('textarea').fill('新的用户协议正文')
    const [agreementReq] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith('/api/admin/settings') && r.request().method() === 'PUT'),
      agreementCard.locator('button').filter({ hasText: /保\s*存/ }).first().click(),
    ])
    assert.deepEqual(JSON.parse(agreementReq.request().postData()), { user_agreement: '新的用户协议正文' })
    await privacyCard.locator('textarea').fill('新的隐私政策正文')
    const [privacyReq] = await Promise.all([
      page.waitForResponse((r) => r.url().endsWith('/api/admin/settings') && r.request().method() === 'PUT'),
      privacyCard.locator('button').filter({ hasText: /保\s*存/ }).first().click(),
    ])
    assert.deepEqual(JSON.parse(privacyReq.request().postData()), { privacy_policy: '新的隐私政策正文' })
    assert.equal(state.agreement, '新的用户协议正文')
    assert.equal(state.privacy, '新的隐私政策正文')

    // 英文和手机布局同样可用。
    await page.evaluate(() => localStorage.setItem('lingshu.prefs', JSON.stringify({ language: 'en' })))
    state.user = null
    await page.setViewportSize({ width: 375, height: 812 })
    await page.goto(base + '/register')
    await page.getByRole('checkbox', { name: /I have read and agree/ }).waitFor()
    await page.getByRole('link', { name: 'Terms of Service', exact: true }).waitFor()
    assert.equal(await noScroll(), true)
    await page.goto(base + '/terms')
    await page.getByText('新的用户协议正文', { exact: false }).waitFor()
    await page.getByRole('link', { name: 'Back to registration' }).waitFor()
    assert.equal(await noScroll(), true)

    assert.deepEqual(errors, [])
    console.log('PASS: legal documents in registration (mandatory consent, fail closed on fetch error), public terms/privacy pages, admin editing, English and mobile layout')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
