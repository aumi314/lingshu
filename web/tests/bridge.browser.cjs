// Responses → Messages 转接的界面回归：模型页和渠道页标出「转接」的 Responses 入口，
// 调用日志标出实际走的协议、压缩用途和转接诊断。运行：node web/tests/bridge.browser.cjs
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25187', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25187')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const groups = [{ id: 1, name: 'Official API', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '' }]
    const priced = (name, protocols, native, bridged) => ({
      group_id: 1, name, protocols, native_protocols: native, bridged_protocols: bridged,
      input_price: '1', output_price: '2', cache_read_price: '0', cache_write_price: '0', price_rules: [], fast_multiplier: null,
    })
    const models = [
      priced('claude-bridge', ['chat', 'responses'], ['chat'], ['responses']),
      priced('gpt-native', ['responses'], ['responses'], []),
    ]
    const channel = (id, name, urls) => ({
      id, name, template: 'standard', note: '', api_key_masked: 'sk-***', keys: [], priority: 0, enabled: true,
      base_url_chat: '', base_url_messages: '', base_url_responses: '', base_url_systemone: '',
      proxy_url: '', group_ids: [1], models: [], stats: null, balance: [], balance_checked_at: null, balance_error: '', ...urls,
    })
    const channels = [
      channel(1, 'anthropic-bridge', { base_url_messages: 'https://up.example/anthropic' }),
      channel(2, 'openai-native', { base_url_responses: 'https://up.example/v1' }),
    ]
    const common = { id: 1, user_id: 2, username: 'member-fixture', created_at: new Date().toISOString(), status: 'success', operator: '', note: '' }
    const request = {
      ...common, type: 'request', event_key: 'request:1', request_id: 'req-fixture', key_name: 'fixture-key',
      model: 'claude-bridge', upstream_model: '', channel_name: '', group_name: 'fixture-group', group_ratio: '1',
      protocol: 'responses', upstream_protocol: 'messages', operation: 'compaction',
      conversion_diagnostics: [{ field: 'input[0].content', note: 'dropped' }],
      stream: false, error: '', http_status: 200, input_tokens: 100, cache_read_tokens: 0, cache_write_tokens: 0,
      output_tokens: 20, reasoning_tokens: 0, cost: '0.25', balance_delta: '-0.25', attempts: 1,
      first_byte_ms: 100, total_ms: 200, input_price: '1', output_price: '2', price_rule: '', client_ip: '203.0.113.9',
    }
    const summary = { requests: 1, failed: 0, cost: '0.25', input_tokens: 100, cache_read_tokens: 0, output_tokens: 20, topup_total: '0', adjustment_net: '0' }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    const user = { id: 2, username: 'member-fixture', role: 'admin', balance: '8', enabled: true, user_group_ids: [1], preferences: { language: 'zh', theme: 'dark' }, created_at: new Date().toISOString() }
    await page.route('**/api/**', (route) => {
      const p = new URL(route.request().url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user, cny_per_usd: '6.7' })
      if (p === '/api/me/models') return json({ groups, models })
      if (p === '/api/admin/channels') return json(channels)
      if (p === '/api/admin/channel-templates') return json([])
      if (p === '/api/admin/groups') return json(groups)
      if (p === '/api/admin/models') return json([])
      if (p.endsWith('/logs')) {
        const logs = new URL(route.request().url()).searchParams.get('type') === 'request' ? [request] : []
        return json({ logs, total: logs.length, summary, models: ['claude-bridge'] })
      }
      return json({})
    })

    // 模型页：转接的 Responses 入口标出来，原生 Responses 的模型不标
    await page.goto('http://127.0.0.1:25187/console/models')
    await page.getByText('claude-bridge', { exact: true }).waitFor()
    const bridgedModelRow = page.locator('tbody tr').filter({ hasText: 'claude-bridge' })
    await bridgedModelRow.getByText('Responses（转接）', { exact: true }).waitFor()
    await bridgedModelRow.getByText('/v1/responses', { exact: true }).waitFor()
    const nativeRow = page.locator('tbody tr').filter({ hasText: 'gpt-native' })
    assert.equal(await nativeRow.getByText('Responses（转接）', { exact: true }).count(), 0)

    // 渠道页：只有 Messages 地址的渠道标出 Responses 转接
    await page.goto('http://127.0.0.1:25187/console/admin/channels')
    await page.getByText('anthropic-bridge', { exact: true }).waitFor()
    const bridgeRow = page.locator('tbody tr').filter({ hasText: 'anthropic-bridge' })
    await bridgeRow.getByText('Responses（转接）', { exact: true }).waitFor()
    await bridgeRow.getByText('messages', { exact: true }).waitFor()
    const nativeChannel = page.locator('tbody tr').filter({ hasText: 'openai-native' })
    assert.equal(await nativeChannel.getByText('Responses（转接）', { exact: true }).count(), 0)

    // 调用日志：标出入口 → 上游协议、压缩用途；详情里有转接诊断
    await page.goto('http://127.0.0.1:25187/console/logs')
    await page.getByText('claude-bridge', { exact: true }).waitFor()
    await page.getByText(/Responses → Messages/).waitFor()
    await page.getByText('压缩', { exact: true }).waitFor()
    await page.getByText('claude-bridge', { exact: true }).click()
    const drawer = page.locator('.ant-drawer')
    await drawer.getByText('上下文压缩', { exact: true }).waitFor()
    await drawer.getByText(/input\[0\]\.content/).waitFor()
    await drawer.getByText('转接', { exact: true }).waitFor()

    console.log('PASS: models and channels mark bridged Responses entries, call logs show protocol->upstream, compaction purpose and conversion diagnostics')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
