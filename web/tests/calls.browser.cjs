// 调用设置回归：调用限制从运营设置搬到这里；协议转接两个应急开关切换即保存；渠道亲和、违禁词拦截的说明卡片。使用安装了 playwright 的 Node 环境运行。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25196', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25196')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const writes = []
    const limits = { user_concurrency: 10, user_rpm: 120, reserve_output_tokens: 8192, bridge_responses_to_messages: true, bridge_messages_to_responses: false }
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b, status = 200) => route.fulfill({ json: b, status })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '1', enabled: true, user_group_ids: [1], preferences: { theme: 'dark', language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/settings' && req.method() === 'PUT') { writes.push(req.postDataJSON()); return json({ ok: true }) }
      if (p === '/api/admin/settings') return json({ redeem_enabled: true, cny_per_usd: '6.7', payment_enabled: false, registration_mode: 'manual', api_base_url: '', deployment_mode: 'commercial_relay', fx_mode: 'manual', fx_status: { source: 'Frankfurter' }, call_limits: limits })
      return json([])
    })

    // 运营设置里不再有调用限制
    await page.goto('http://127.0.0.1:25196/console/admin/system')
    await page.getByText('使用模式', { exact: true }).waitFor()
    assert.equal(await page.getByRole('tabpanel').getByText('调用限制', { exact: true }).count(), 0)

    // 调用设置：四张卡片；调用限制读回原值并能保存
    await page.getByRole('tab', { name: '调用设置' }).click()
    for (const title of ['调用限制', '协议转接', '渠道亲和', '违禁词拦截']) await page.getByRole('tabpanel').getByText(title, { exact: true }).waitFor()
    await page.getByText('自动生效', { exact: true }).waitFor()
    await page.getByText('规划中', { exact: true }).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('tab'), 'calls')
    assert.equal(await page.locator('#user_rpm').inputValue(), '120')
    await page.locator('#user_rpm').fill('60')
    await page.getByRole('tabpanel').getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 100 && writes.length < 1; i++) await page.waitForTimeout(30)
    assert.deepEqual(writes[0], { user_concurrency: 10, user_rpm: 60, reserve_output_tokens: 8192 })

    // 转接开关：读回各自状态，切换立即保存，只提交这一项
    const r2m = page.getByRole('switch', { name: /Codex 等 Responses 客户端/ })
    const m2r = page.getByRole('switch', { name: /Claude Code 等 Messages 客户端/ })
    assert.equal(await r2m.getAttribute('aria-checked'), 'true')
    assert.equal(await m2r.getAttribute('aria-checked'), 'false')
    await m2r.click()
    for (let i = 0; i < 100 && writes.length < 2; i++) await page.waitForTimeout(30)
    assert.deepEqual(writes[1], { bridge_messages_to_responses: true })
    for (let i = 0; i < 100 && (await m2r.getAttribute('aria-checked')) !== 'true'; i++) await page.waitForTimeout(30)
    assert.equal(await m2r.getAttribute('aria-checked'), 'true')
    assert.equal(await r2m.getAttribute('aria-checked'), 'true')

    // 刷新后仍在调用设置标签
    await page.reload()
    await page.getByRole('tabpanel').getByText('协议转接', { exact: true }).waitFor()
    assert.deepEqual(errors, [])
    console.log('PASS: call limits moved to the Calls tab and saved, bridge switches read back and save individually, affinity and planned blocking cards, tab kept in URL')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
