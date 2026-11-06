// 渠道页「连接状态」列回归。使用安装了 playwright 的 Node 环境运行：node web/tests/channels.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25190', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25190')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const base = { template: 'standard', api_key_masked: 'sk-…', priority: 0, enabled: true, base_url_chat: 'https://up.example/v1', base_url_messages: '', base_url_responses: '', base_url_systemone: '', proxy_url: '', group_ids: [1], models: [{ model_id: 1, model_name: 'gpt-6-sol', upstream_name: '' }], balance: [], balance_checked_at: null, balance_error: '' }
    const stat = { success: 0, failed: 0, first_token_ms: null, tokens_per_sec: null, cache_read_tokens: 0, input_tokens: 0, last_success_at: null, last_failure_at: null }
    const channels = [
      // 健康：97 / 100 → 97%，首字 2.35s → 2.4s（保留一位），缓存 870 / 1000
      { ...base, id: 1, name: 'healthy', stats: { ...stat, success: 97, failed: 3, first_token_ms: 2350, tokens_per_sec: 45, cache_read_tokens: 870, input_tokens: 1000, last_success_at: '2026-11-06T07:00:00Z', last_failure_at: '2026-11-06T06:00:00Z' } },
      // 199 / 200 也只显示 99%，不四舍五入成 100%
      { ...base, id: 2, name: 'almost', stats: { ...stat, success: 199, failed: 1 } },
      { ...base, id: 3, name: 'broken', cooling: true, stats: { ...stat, success: 1, failed: 9 } },
      { ...base, id: 4, name: 'idle', stats: null },
      { ...base, id: 5, name: 'empty', models: [], stats: null },
    ]
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await page.route('**/api/**', (route) => {
      const p = new URL(route.request().url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/channels') return json(channels)
      if (p === '/api/admin/groups') return json([{ id: 1, name: 'default', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '' }])
      return json([])
    })
    await page.goto('http://127.0.0.1:25190/console/admin/channels')
    const row = (name) => page.locator('tbody tr').filter({ has: page.getByText(name, { exact: true }) })
    await row('healthy').waitFor()

    // 不再有模型列，改为连接状态
    assert.equal(await page.getByRole('columnheader', { name: '模型' }).count(), 0)
    await page.getByRole('columnheader', { name: '连接状态' }).waitFor()
    await row('healthy').getByText('成功率 97% · 100 次', { exact: true }).waitFor()
    await row('healthy').getByText('首字 2.4s · 45 tok/s · 缓存 87%', { exact: true }).waitFor()
    await row('almost').getByText('成功率 99% · 200 次', { exact: true }).waitFor()
    await row('broken').getByText('成功率 10% · 10 次', { exact: true }).waitFor()
    await row('broken').getByText('冷却中', { exact: true }).waitFor()
    assert.equal(await row('broken').locator('.ant-badge-status-error').count(), 1)
    assert.equal(await row('healthy').locator('.ant-badge-status-success').count(), 1)
    await row('idle').getByText('24 小时无调用', { exact: true }).waitFor()
    await row('empty').getByText('未配置模型', { exact: true }).waitFor()
    // 悬停看次数和最近成功 / 失败时间
    await row('healthy').getByText('成功率 97% · 100 次', { exact: true }).hover()
    await page.getByText('最近 24 小时：成功 97 次，失败 3 次', { exact: true }).waitFor()
    console.log('PASS: channel list shows connection status instead of models: success rate floors, colors, TTFT/speed/cache line, cooling tag, idle and no-model states, tooltip details')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
