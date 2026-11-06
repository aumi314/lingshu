// 在可解析 playwright 且已安装 Chromium 的 Node 环境运行。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')
const fs = require('node:fs')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25181', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25181')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    for (const admin of [true, false]) {
      const en = !admin
      const text = (zh, english) => en ? english : zh
      const page = await browser.newPage({ viewport: admin ? { width: 1440, height: 1050 } : { width: 390, height: 844 } })
      const user = { id: 2, username: 'member-fixture', role: admin ? 'admin' : 'user', balance: '8', preferences: { language: en ? 'en' : 'zh', theme: 'dark' }, enabled: true, user_group_ids: [], created_at: new Date().toISOString() }
      const common = { id: 1, user_id: 2, username: user.username, created_at: new Date().toISOString(), status: 'success', operator: '', note: '' }
      const request = { ...common, type: 'request', event_key: 'request:1', request_id: 'req-fixture', key_name: 'fixture-key', model: 'fixture-model', upstream_model: admin ? 'upstream-fixture' : '', channel_name: admin ? 'fixture-channel' : '', group_name: 'fixture-group', group_ratio: '1', protocol: 'chat', stream: false, error: '', http_status: 200, input_tokens: 100, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 20, reasoning_tokens: 0, cost: '0.25', balance_delta: '-0.25', attempts: 1, first_byte_ms: 100, total_ms: 200, input_price: '1', output_price: '2', price_rule: '', client_ip: '203.0.113.9' }
      // 流式：输入 100 + 缓存读 300 → 命中 75%；可见输出 200 − 推理 100 ÷（3.0s − 1.0s）→ 50 tok/s
      const streamed = { ...request, id: 3, event_key: 'request:3', request_id: 'req-stream', model: 'stream-model', stream: true, cache_read_tokens: 300, output_tokens: 200, reasoning_tokens: 100, first_byte_ms: 1000, total_ms: 3000, reasoning_effort: 'high' }
      // 上游攒好整段一次发出：首字后 0.05s 就结束，测不出速度
      const burst = { ...request, id: 4, event_key: 'request:4', request_id: 'req-burst', model: 'burst-model', stream: true, output_tokens: 300, first_byte_ms: 2000, total_ms: 2050 }
      const op = { created_at: common.created_at, user_id: 2, target_id: '2', target_name: 'member-fixture', note: '' }
      const topup = { ...op, id: 1, actor_id: null, actor: '', action: 'topup.credit', target_type: 'balance', changes: [{ field: 'balance', from: '5', to: '6' }], note: '在线充值 · epay', amount: '1', order: { id: 'order-fixture', provider: 'epay', method: 'alipay', currency: 'CNY', amount: '6.70', tax: '0.00', total: '6.70', completed_at: common.created_at } }
      const adjustment = { ...op, id: 2, actor_id: 1, actor: 'admin-fixture', action: 'balance.adjust', target_type: 'balance', changes: [{ field: 'balance', from: '6', to: '5.5' }], note: 'manual fixture', amount: '-0.5' }
      const channelOp = { ...op, id: 3, actor_id: 1, actor: 'admin-fixture', user_id: null, action: 'channel.update', target_type: 'channel', target_id: '7', target_name: 'channel-fixture', changes: [{ field: 'api_key', secret: true }, { field: 'enabled', from: true, to: false }] }
      const summary = { requests: 2, failed: 0, cost: '0.5', input_tokens: 500, cache_read_tokens: 300, output_tokens: 220, topup_total: '1', adjustment_net: '-0.5' }
      let hold = false
      let fail = false
      let emptyBalance = false
      const delayed = []
      const queries = []
      await page.route('**/api/**', async (route) => {
        const url = new URL(route.request().url())
        const json = (body, status = 200) => route.fulfill({ json: body, status })
        if (url.pathname === '/api/me') return json({ user, cny_per_usd: '6.7' })
        if (url.pathname === '/api/admin/users') return json([user])
        if (!url.pathname.endsWith('/logs')) return json({})
        queries.push(url.searchParams)
        if (fail) return json({ error: 'fixture load error' }, 500)
        const type = url.searchParams.get('type')
        const logs = type === 'request' ? [request, streamed, burst] : type === 'operation' && !emptyBalance ? [topup, adjustment, ...(admin ? [channelOp] : [])] : []
        const body = { logs, total: logs.length, summary, models: ['fixture-model', 'stream-model'] }
        if (hold && type === 'request') delayed.push(() => json(body))
        else return json(body)
      })
      const tab = (zh, english) => page.getByRole('tab', { name: text(zh, english), exact: true }).click()
      const base = `http://127.0.0.1:25181/console/${admin ? 'admin/' : ''}logs`

      // 默认是调用日志：只请求调用，列里直接能看到分组、缓存率、首字和速度
      await page.goto(base)
      await page.getByText('fixture-model', { exact: true }).waitFor()
      assert.equal(queries.at(-1).get('type'), 'request')
      await page.getByText('fixture-group', { exact: true }).first().waitFor()
      await page.getByText('1.0s · 50 tok/s', { exact: true }).waitFor()
      await page.getByText(text('Chat · 流式 · high', 'Chat · stream · high'), { exact: true }).waitFor()
      await page.getByText(text('Chat · 非流式', 'Chat · non-stream'), { exact: true }).first().waitFor()
      // 表格比屏幕宽时只在表格内左右滑，页面本身不能被撑宽
      assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1))
      await page.getByText('2.0s', { exact: true }).waitFor()
      assert.equal(await page.getByText(/tok\/s/).count(), 1)
      await page.getByText(text('缓存 75%', 'Cache 75%'), { exact: true }).waitFor()
      await page.getByText(text('缓存 0%', 'Cache 0%'), { exact: true }).first().waitFor()
      await page.getByText('60%', { exact: true }).waitFor() // 汇总卡片：缓存读 300 ÷ 输入 500
      await page.getByText('fixture-model', { exact: true }).click()
      await page.getByText('req-fixture', { exact: true }).waitFor()
      await page.locator('.ant-drawer').getByText('203.0.113.9', { exact: true }).waitFor()
      if (!admin) assert.equal(await page.getByText('fixture-channel', { exact: true }).count(), 0)
      await page.locator('.ant-drawer-close').click()

      // 操作日志：充值到账记为系统；余额类显示金额；改渠道 key 只显示“已更换”
      await tab('操作日志', 'Operation logs')
      await page.getByText(text('额度调整', 'Balance adjustment'), { exact: true }).waitFor()
      assert.equal(queries.at(-1).get('type'), 'operation')
      assert.equal(new URL(page.url()).searchParams.get('tab'), 'operations')
      await page.getByText(text('系统', 'System'), { exact: true }).waitFor()
      await page.getByText('admin-fixture', { exact: true }).first().waitFor()
      if (admin) {
        await page.getByText(text('修改渠道', 'Update Channel'), { exact: true }).waitFor()
        await page.getByText(text('API key 已更换；状态 启用 → 停用', 'API key Changed；Status Enabled → Disabled'), { exact: true }).waitFor()
      } else assert.equal(await page.getByText('channel-fixture', { exact: true }).count(), 0)
      await page.getByRole('table').getByText(text('充值到账', 'Top-up credited'), { exact: true }).click()
      await page.getByText('order-fixture', { exact: true }).waitFor()
      await page.getByText('6.70 CNY', { exact: true }).first().waitFor()
      await page.locator('.ant-drawer-close:visible').click()
      // 按对象类型筛选
      await page.getByRole('combobox').last().click()
      // 成员只列与自己有关的对象类型
      await page.locator('.ant-select-dropdown:visible').getByText(admin ? '渠道' : 'Key', { exact: true }).click()
      await page.waitForTimeout(100)
      assert.equal(queries.at(-1).get('target'), admin ? 'channel' : 'api_key')

      // 刷新保留标签
      await page.reload()
      await page.getByText(text('额度调整', 'Balance adjustment'), { exact: true }).waitFor()

      // 慢响应不能覆盖后切换的标签
      hold = true
      await tab('调用日志', 'Call logs')
      for (let i = 0; i < 100 && delayed.length === 0; i++) await page.waitForTimeout(20) // 等请求被拦下，机器忙时 100ms 不够
      assert.ok(delayed.length > 0)
      await tab('操作日志', 'Operation logs')
      await page.getByText(text('额度调整', 'Balance adjustment'), { exact: true }).waitFor()
      for (const release of delayed) await release()
      await page.waitForTimeout(100)
      assert.equal(await page.getByText('fixture-model', { exact: true }).count(), 0)
      hold = false

      // 失败和空状态
      fail = true
      await tab('调用日志', 'Call logs')
      await page.getByText('fixture load error', { exact: true }).waitFor()
      fail = false
      emptyBalance = true
      await tab('操作日志', 'Operation logs')
      await page.getByText(text('这段时间没有操作记录', 'No operations in this period'), { exact: true }).waitFor()
      emptyBalance = false
      if (process.env.ACTIVITY_SCREENSHOT_DIR) {
        fs.mkdirSync(process.env.ACTIVITY_SCREENSHOT_DIR, { recursive: true })
        await tab('调用日志', 'Call logs')
        await page.getByText('stream-model', { exact: true }).waitFor()
        await page.getByRole('heading', { name: text('全站日志', 'Account logs'), exact: true }).click()
        await page.locator('.ant-table-content').evaluate((element) => { element.scrollLeft = 0 })
        await page.screenshot({ path: path.join(process.env.ACTIVITY_SCREENSHOT_DIR, admin ? 'admin.png' : 'member.png'), fullPage: true })
        await tab('操作日志', 'Operation logs')
        await page.getByText(text('额度调整', 'Balance adjustment'), { exact: true }).waitFor()
        await page.waitForTimeout(400)
        await page.screenshot({ path: path.join(process.env.ACTIVITY_SCREENSHOT_DIR, admin ? 'admin-ops.png' : 'member-ops.png'), fullPage: true })
      }
      await page.close()
    }
    console.log('PASS: admin/member call/operation tabs, cache rate, first token and speed, operation labels and secret masking, system actor, target filter, details, order currency, tab in URL, empty/error states, delayed responses, Chinese/English, mobile')
  } finally { await browser.close(); server.kill() }
})().catch((error) => { console.error(error); process.exitCode = 1 })
