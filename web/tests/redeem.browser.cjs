// 兑换码回归：管理员生成（明文只显示一次）、用户在钱包兑换。使用安装了 playwright 的 Node 环境运行。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25188', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25188')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const state = { balance: '1', codes: [], posted: null, redeemed: null, redeemEnabled: true, failSave: false, settingsWrites: [], holdMethods: false, releaseMethods: null }
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await page.route('**/api/**', async (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b, status = 200) => route.fulfill({ json: b, status })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: state.balance, enabled: true, user_group_ids: [1], preferences: { theme: 'dark', language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/settings' && req.method() === 'PUT') {
        const input = req.postDataJSON()
        state.settingsWrites.push(input)
        if (state.failSave) return json({ error: '保存失败，请重试' }, 500)
        state.redeemEnabled = input.redeem_enabled
        return json({ ok: true })
      }
      if (p === '/api/admin/settings') return json({ redeem_enabled: state.redeemEnabled, cny_per_usd: '6.7', payment_enabled: false, registration_mode: 'manual', api_base_url: '', deployment_mode: 'commercial_relay', fx_mode: 'manual', fx_status: { source: 'Frankfurter' }, call_limits: { user_concurrency: 10, user_rpm: 120, reserve_output_tokens: 8192 } })
      if (p === '/api/admin/redeem-codes' && req.method() === 'POST') {
        state.posted = req.postDataJSON()
        state.codes = [{ id: 1, prefix: 'ABCDEFGHIJ', amount: '5', note: state.posted.note, expires_at: null, created_at: '2026-11-06T00:00:00Z', used_by: '', used_at: null, revoked_at: null }]
        return json([{ id: 1, prefix: 'ABCDEFGHIJ', code: 'ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z2' }], 201)
      }
      if (p === '/api/admin/redeem-codes') return json(state.codes)
      if (p === '/api/admin/payments') return json({ services: [], public_url: 'https://gateway.example.com' })
      if (p === '/api/me/redeem') {
        state.redeemed = req.postDataJSON()
        if (state.redeemed.code.trim() !== 'ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z2') return json({ error: '兑换码无效、已使用、已撤销或已过期，请检查是否输入完整' }, 400)
        state.balance = '6'
        return json({ amount: '5', balance: '6' })
      }
      if (p === '/api/me/payment-methods') {
        if (state.holdMethods) await new Promise((resolve) => { state.releaseMethods = resolve })
        return json({ enabled: false, redeem_enabled: state.redeemEnabled, methods: [] })
      }
      if (p === '/api/me/topups') return json({ items: [], total: 0 })
      if (p === '/api/me/logs') return json({ logs: [], summary: { requests: 0, failed: 0, input_tokens: 0, cache_read_tokens: 0, output_tokens: 0, cost: '0' } })
      return json([])
    })

    // 兑换码并入支付设置，作为第二张卡片；旧链接 ?tab=redeem 落到支付设置
    await page.goto('http://127.0.0.1:25188/console/admin/system?tab=redeem')
    await page.getByRole('tab', { name: '支付设置', selected: true }).waitFor()
    await page.locator('#redeem-codes').getByText('兑换码', { exact: true }).waitFor()
    await page.goto('http://127.0.0.1:25188/console/admin/system?tab=payments')
    await page.getByText('在线支付', { exact: true }).waitFor()
    await page.getByText('开放在线支付', { exact: true }).waitFor()
    assert.equal(await page.getByRole('tab', { name: '兑换码' }).count(), 0)
    assert.equal(await page.locator('#redeem-codes a[href="https://wzyp.cn"]').count(), 1)
    await page.locator('#redeem-codes').getByText(/不受上面“开放在线支付”开关影响/).waitFor()
    // 开关单独保存；失败保持原状态，关闭并刷新后仍为关闭。
    let toggle = page.getByRole('switch', { name: '开启兑换码', exact: true })
    await toggle.waitFor()
    assert.equal(await toggle.getAttribute('aria-checked'), 'true')
    state.failSave = true
    await toggle.click()
    await page.getByText('保存失败，请重试', { exact: true }).waitFor()
    assert.equal(await toggle.getAttribute('aria-checked'), 'true')
    state.failSave = false
    await toggle.click()
    await page.waitForFunction(() => document.querySelector('#redeem-codes [role=switch]')?.getAttribute('aria-checked') === 'false')
    assert.deepEqual(state.settingsWrites, [{ redeem_enabled: false }, { redeem_enabled: false }])
    await page.reload()
    toggle = page.getByRole('switch', { name: '开启兑换码', exact: true })
    await toggle.waitFor()
    await page.waitForFunction(() => document.querySelector('#redeem-codes [role=switch]')?.getAttribute('aria-checked') === 'false')
    // 关闭时管理员仍能生成：数量、面额、备注提交；明文在弹窗里只显示一次
    await page.locator('#amount').fill('5')
    await page.locator('#note').fill('11 月活动')
    await page.getByRole('button', { name: /^生\s*成$/ }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByText('ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z2', { exact: true }).waitFor()
    assert.deepEqual({ count: state.posted.count, amount: state.posted.amount, currency: state.posted.currency, note: state.posted.note }, { count: 1, amount: '5', currency: 'USD', note: '11 月活动' })
    await dialog.getByRole('button', { name: '下载 CSV' }).waitFor()
    await dialog.getByRole('button', { name: '我已保存' }).click()
    await page.getByText('ABCDEFGHIJ…', { exact: true }).waitFor()
    await page.getByText('ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z2', { exact: true }).waitFor({ state: 'detached' }) // 关闭后明文不留在页面里

    // 关闭时只隐藏钱包兑换卡片，余额、在线充值及历史仍在。
    state.holdMethods = true
    await page.goto('http://127.0.0.1:25188/console/wallet')
    await page.getByText('当前余额', { exact: true }).waitFor()
    assert.equal(await page.getByPlaceholder(/输入兑换码/).count(), 0)
    for (let i = 0; i < 100 && !state.releaseMethods; i++) await new Promise((resolve) => setTimeout(resolve, 50))
    assert.equal(typeof state.releaseMethods, 'function')
    const methodsResponse = page.waitForResponse('**/api/me/payment-methods')
    state.holdMethods = false
    state.releaseMethods()
    await methodsResponse
    await page.getByText('在线充值尚未开放，如需充值请联系管理员。', { exact: true }).waitFor()
    await page.getByText('充值记录', { exact: true }).waitFor()
    assert.equal(await page.getByPlaceholder(/输入兑换码/).count(), 0)
    // 重新开启，刷新钱包后恢复兑换卡片。
    await page.goto('http://127.0.0.1:25188/console/admin/system?tab=payments')
    toggle = page.getByRole('switch', { name: '开启兑换码', exact: true })
    await toggle.waitFor()
    await toggle.click()
    await page.waitForFunction(() => document.querySelector('#redeem-codes [role=switch]')?.getAttribute('aria-checked') === 'true')
    assert.deepEqual(state.settingsWrites.at(-1), { redeem_enabled: true })
    await page.goto('http://127.0.0.1:25188/console/wallet')
    const input = page.getByPlaceholder(/输入兑换码/)
    await input.fill('WRONG')
    await input.press('Enter')
    await page.getByText(/兑换码无效/).waitFor()
    await input.fill('ABCDE-FGHIJ-KLMNO-PQRST-UVWXY-Z2')
    await page.getByRole('button').filter({ hasText: /兑\s*换/ }).click()
    await page.getByText('兑换成功，到账 $5.0000', { exact: true }).waitFor()
    await page.getByText('$6.0000', { exact: true }).first().waitFor()
    assert.equal(await input.inputValue(), '')
    console.log('PASS: redeem switch persists independently, failed saves retain state, disabled wallet hides redemption without affecting balances/payments/history, admin generates while disabled, re-enabled member redemption refreshes balance')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
