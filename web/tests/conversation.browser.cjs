// 对话留存回归：管理员在全站日志的请求详情里看到留存的对话（各角色、工具调用和结果、本次回复），能下载原文；没留存的调用给出开关位置提示。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25218', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25218')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const user = { id: 1, username: 'admin', role: 'admin', balance: '8', preferences: { language: 'zh', theme: 'dark' }, enabled: true, user_group_ids: [], created_at: new Date().toISOString() }
    const base = { user_id: 1, username: 'admin', created_at: new Date().toISOString(), status: 'success', operator: '', note: '', type: 'request', key_name: 'k', upstream_model: 'u', channel_name: 'c', group_name: 'g', group_ratio: '1', stream: true, error: '', http_status: 200, input_tokens: 10, cache_read_tokens: 0, cache_write_tokens: 0, output_tokens: 5, reasoning_tokens: 0, cost: '0.01', balance_delta: '-0.01', attempts: 1, first_byte_ms: 100, total_ms: 200, input_price: '1', output_price: '2', price_rule: '', client_ip: '' }
    const logs = [
      { ...base, id: 11, event_key: 'request:11', request_id: 'req-saved', model: 'saved-model', protocol: 'responses' },
      { ...base, id: 12, event_key: 'request:12', request_id: 'req-plain', model: 'plain-model', protocol: 'chat' },
    ]
    const saved = {
      captured: true, session: 'sess-1', response: 'data: {"type":"response.output_text.delta","delta":"改好了"}\n\n', response_truncated: false, reply_text: '改好了',
      request: { model: 'saved-model', instructions: '你是编码助手', input: [
        { type: 'message', role: 'user', content: [{ type: 'input_text', text: '把 README 的标题改一下' }] },
        { type: 'function_call', call_id: 'c1', name: 'exec_command', arguments: '{"cmd":"cat README.md"}' },
        { type: 'function_call_output', call_id: 'c1', output: '# 旧标题' },
      ] },
    }
    const summary = { requests: 2, failed: 0, cost: '0.02', input_tokens: 20, cache_read_tokens: 0, output_tokens: 10, topup_total: '0', adjustment_net: '0', peak_rpm: 1, peak_rpm_at: '2026-11-11T04:00:00Z', peak_tpm: 30, peak_tpm_at: '2026-11-11T04:00:00Z', median_first_token_ms: 100, median_total_ms: 200 }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, acceptDownloads: true })
    let captureReads = 0
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.route('**/api/**', (route) => {
      const url = new URL(route.request().url())
      const json = (b, status = 200) => route.fulfill({ json: b, status })
      if (url.pathname === '/api/me') return json({ user, cny_per_usd: '6.7' })
      if (url.pathname === '/api/admin/users') return json([user])
      if (url.pathname === '/api/admin/logs/11/conversation') {
        captureReads++
        if (captureReads === 1) return json({ error: 'capture temporarily unavailable' }, 500)
        return json(saved)
      }
      if (url.pathname === '/api/admin/logs/12/conversation') return json({ captured: false })
      if (url.pathname.endsWith('/logs')) return json({ logs: url.searchParams.get('type') === 'request' ? logs : [], total: 2, summary, models: [] })
      return json({})
    })

    await page.goto('http://127.0.0.1:25218/console/admin/logs')
    await page.getByText('saved-model', { exact: true }).click()
    const drawer = page.locator('.ant-drawer-section')
    await drawer.getByRole('tab', { name: '对话内容' }).click()
    await drawer.getByText('读取失败，请重试', { exact: true }).waitFor()
    await drawer.getByText('capture temporarily unavailable', { exact: true }).waitFor()
    assert.equal(await drawer.getByText('加载中…', { exact: true }).count(), 0)
    await drawer.getByRole('button', { name: /重\s*试/ }).click()
    for (const text of ['你是编码助手', '把 README 的标题改一下', 'exec_command({"cmd":"cat README.md"})', '# 旧标题', '改好了', 'sess-1']) await drawer.getByText(text, { exact: true }).waitFor()
    for (const tag of ['system', 'user', 'assistant', 'tool', '本次回复']) assert.ok((await drawer.getByText(tag, { exact: true }).count()) > 0, tag)
    const [download] = await Promise.all([page.waitForEvent('download'), drawer.getByRole('button', { name: /下载原文/ }).click()])
    assert.equal(download.suggestedFilename(), 'req-saved.json')
    const file = JSON.parse(require('node:fs').readFileSync(await download.path(), 'utf8'))
    assert.equal(file.request.instructions, '你是编码助手')
    assert.equal(file.session, 'sess-1')
    assert.equal(file.response_truncated, false)
    assert.equal(captureReads, 2)

    saved.response_truncated = true
    await page.locator('.ant-drawer-close').click()
    await page.getByText('saved-model', { exact: true }).click()
    await drawer.getByRole('tab', { name: '对话内容' }).click()
    await drawer.getByText('超过 8 MiB，已截断', { exact: true }).waitFor()
    const [cutDownload] = await Promise.all([page.waitForEvent('download'), drawer.getByRole('button', { name: /下载原文/ }).click()])
    const cutFile = JSON.parse(require('node:fs').readFileSync(await cutDownload.path(), 'utf8'))
    assert.equal(cutFile.response_truncated, true)
    assert.equal(cutFile.response, saved.response)

    await page.locator('.ant-drawer-close').click()
    await page.getByText('plain-model', { exact: true }).click()
    await drawer.getByRole('tab', { name: '对话内容' }).click()
    await drawer.getByText(/这次调用没有留存对话/).waitFor()
    assert.deepEqual(errors, [])
    console.log('PASS: admin sees saved conversation turns (system, user, tool call and result, reply) with session, failed reads retry successfully, raw downloads preserve both truncation states; uncaptured call shows where the switch is')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
