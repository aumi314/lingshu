// 概览页回归。使用安装了 playwright 的 Node 环境运行：node web/tests/overview.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')
const fs = require('node:fs')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25184', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25184')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const state = { balance: '0.29', keys: [{ id: 1, name: 'laptop', prefix: 'sk-ls-AB12', group_id: 1, group_name: 'default', quota_limit: null, quota_used: '0', enabled: true, last_used_at: null, created_at: '2026-11-01' }], topup: true }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } })
    await page.route('**/api/**', (route) => {
      const p = new URL(route.request().url()).pathname
      const json = (b) => route.fulfill({ json: b })
      const user = { id: 2, username: 'member', role: 'user', balance: state.balance, enabled: true, user_group_ids: [1], preferences: { theme: 'dark', language: 'zh' }, created_at: '2026-10-25' }
      if (p === '/api/me') return json({ user, cny_per_usd: '6.7' })
      if (p === '/api/me/logs') return json({ logs: [], summary: { requests: 589, failed: 0, input_tokens: 100000000, cache_read_tokens: 0, output_tokens: 3420000, cost: '31.95' } })
      if (p === '/api/me/keys') return json(state.keys)
      if (p === '/api/me/payment-methods') return json({ enabled: state.topup, methods: [] })
      if (p === '/api/me/endpoints') return json({ api_base_url: 'https://gateway.example.com/v1' })
      return json({})
    })
    const goto = async () => {
      await page.goto('http://127.0.0.1:25184/console')
      await page.getByText('接入', { exact: true }).waitFor()
    }

    // 余额偏低：提前提醒，带充值入口；步骤按数据自动打勾
    await goto()
    await page.getByText('余额不多了', { exact: true }).waitFor()
    await page.getByRole('button', { name: /^充\s*值$/ }).first().waitFor()
    await page.getByText('已完成 3/4', { exact: true }).waitFor()
    await page.getByText('已有 1 个 key', { exact: true }).waitFor()

    // 默认是 Agent 配置：提示词带两个地址、不含 key
    await page.getByRole('tabpanel').getByText(/Configure this coding agent to use my Lingshu API gateway/).waitFor()
    await page.getByRole('tabpanel').getByText(/ANTHROPIC_BASE_URL to https:\/\/gateway\.example\.com and ANTHROPIC_AUTH_TOKEN/).waitFor()
    assert.equal(await page.getByText(/sk-ls-你的key/).count(), 0)

    // Magpie 一键导入：三个协议地址，Anthropic 用根地址，不带 key
    await page.getByRole('tab', { name: /Magpie/ }).click()
    const href = await page.getByRole('link', { name: /添加到 Magpie/ }).getAttribute('href')
    assert.ok(href.startsWith('https://usemagpie.ai/import#'), href)
    const params = new URLSearchParams(href.split('#')[1])
    assert.equal(params.get('chat'), 'https://gateway.example.com/v1')
    assert.equal(params.get('responses'), 'https://gateway.example.com/v1')
    assert.equal(params.get('anthropic'), 'https://gateway.example.com')
    assert.equal(params.get('key'), null)
    // Magpie 余额查询：用量接口和字段
    await page.getByRole('tabpanel').getByText(/余额查询地址（GET）  https:\/\/gateway\.example\.com\/v1\/usage/).waitFor()

    // Claude Code：根地址、AUTH_TOKEN；Codex：带 /v1
    await page.getByRole('tab', { name: 'Claude Code' }).click()
    await page.getByRole('tabpanel').getByText(/"ANTHROPIC_BASE_URL": "https:\/\/gateway\.example\.com"/).waitFor()
    await page.getByText('地址填根地址，不要加 /v1，Claude Code 会自己拼。', { exact: true }).waitFor()
    await page.getByRole('tab', { name: 'Codex' }).click()
    await page.getByRole('tabpanel').getByText(/base_url = "https:\/\/gateway\.example\.com\/v1"/).waitFor()

    // 协议地址表已移除
    assert.equal(await page.getByText('全部协议地址', { exact: true }).count(), 0)
    if (process.env.OVERVIEW_SCREENSHOT_DIR) {
      fs.mkdirSync(process.env.OVERVIEW_SCREENSHOT_DIR, { recursive: true })
      await page.getByRole('tab', { name: /Magpie/ }).click()
      await page.screenshot({ path: path.join(process.env.OVERVIEW_SCREENSHOT_DIR, 'overview.png'), fullPage: true })
    }

    // 余额为零且未开放充值：提示联系管理员
    state.balance = '0'
    state.topup = false
    await goto()
    await page.getByText('余额已用完，调用会被拒绝', { exact: true }).waitFor()
    assert.ok(await page.getByText('联系管理员充值', { exact: true }).count() > 0)
    assert.equal(await page.getByRole('button', { name: /^充\s*值$/ }).count(), 0)
    await page.getByText('已完成 2/4', { exact: true }).waitFor()

    // 全部完成：步骤收起成一行，可以展开
    state.balance = '20'
    state.keys[0].last_used_at = '2026-11-05T10:00:00Z'
    await goto()
    await page.getByText('已完成上手', { exact: true }).waitFor()
    assert.equal(await page.getByText('创建并保存 API key', { exact: true }).isVisible(), false)
    await page.getByRole('button', { name: '查看步骤' }).click()
    await page.getByText('创建并保存 API key', { exact: true }).waitFor()

    // 手机宽度不被撑宽
    await page.setViewportSize({ width: 390, height: 844 })
    await goto()
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1))
    console.log('PASS: low balance hint with top-up, auto-checked steps, agent setup prompt without key, Magpie import link, Claude Code root URL, Codex /v1, endpoint table removed, zero balance without top-up, all-done collapse, mobile width')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
