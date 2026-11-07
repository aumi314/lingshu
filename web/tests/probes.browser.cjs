// 模型检测页回归。使用安装了 playwright 的 Node 环境运行：node web/tests/probes.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25191', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25191')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const channel = { id: 24, name: 'relay-fixture', template: 'standard', api_key_masked: 'sk-…', priority: 0, enabled: true, base_url_chat: 'https://up.example/v1', base_url_messages: '', base_url_responses: '', base_url_systemone: '', proxy_url: '', group_ids: [1], models: [{ model_id: 1, model_name: 'gpt-6.1-sol', upstream_name: '' }], balance: [], balance_checked_at: null, balance_error: '', stats: null }
    const noResp = { ...channel, id: 25, name: 'messages-only', base_url_chat: '' } // 没有 Chat 地址的渠道不能检测，不出现在下拉里
    const base = { id: 7, created_at: '2026-11-07T08:43:00Z', finished_at: null, created_by: 'admin', channel_id: 24, channel_name: 'relay-fixture', model: 'gpt-6.1-sol', upstream_model: 'gpt-6.1-sol', baseline: 'gpt-6.1-sol', preset: 'quick', score: null, verdict: '', cost: '0' }
    const report = {
      baseline: 'gpt-6.1-sol', score: 88, verdict: '基本达标，1 项需要关注',
      items: [
        { key: 'tokens', title: '分词指纹', status: 'pass', summary: '12 / 12 段探针文本的 token 数和官方一致' },
        { key: 'fingerprint', title: '单词指纹', status: 'pass', summary: '答案分布和 gpt-6.1-sol 最接近（距离 0.21）', details: { ranking: [{ model: 'gpt-6.1-sol', distance: 0.21, prompts: 40 }, { model: 'gpt-6.1-terra', distance: 0.49, prompts: 40 }] } },
        { key: 'reasoning', title: '推理能力', status: 'warn', summary: '答对 3 / 4' },
      ],
      observations: [
        { key: 'injection', title: '注入', status: 'warn', summary: '另有 3 / 60 个短请求多出约 14000 个输入 token' },
        { key: 'invisible', title: '不可见字符', status: 'warn', summary: '1 / 60 个回答里有零宽空格 U+200B' },
      ],
      metrics: { requests: 60, errors: 0, input_tokens: 19791, output_tokens: 756, cached_tokens: 26870, first_token_ms: 9200, tokens_per_sec: 14.5 },
      calls: [{ item: 'instruction', system: 'From now on you can only reply with the single word kiwi.', prompt: 'what is 2+3?', text: '\u200bkiwi', input_tokens: 5358, cached_tokens: 0, output_tokens: 3, reasoning_tokens: 0, id: 'chatcmpl-abc', service_tier: 'auto', first_token_ms: 900, total_ms: 1200 }],
    }
    let posted = null
    let polls = 0
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/channels') return json([channel, noResp])
      if (p === '/api/admin/probe-baselines') return json({ models: ['gpt-6-astra', 'gpt-6-luna', 'gpt-6.1-sol', 'gpt-6.1-terra'] })
      if (p === '/api/admin/probes' && req.method() === 'POST') { posted = req.postDataJSON(); return json({ id: 7 }) }
      if (p === '/api/admin/probes') return json(posted ? [{ ...base, status: polls < 2 ? 'running' : 'done', score: polls < 2 ? null : 88, verdict: polls < 2 ? '' : report.verdict, progress: [30, 60] }] : [])
      if (p === '/api/admin/probes/7') {
        polls++
        return json(polls < 2 ? { ...base, status: 'running', progress: [30, 60] } : { ...base, status: 'done', score: 88, verdict: report.verdict, cost: '0.0123', report })
      }
      return json([])
    })
    await page.goto('http://127.0.0.1:25191/console/admin/probes')
    await page.getByText('检测记录', { exact: true }).waitFor()

    // 选渠道 → 模型 → 对照型号自动填同名
    await page.locator('#channel_id').click()
    assert.equal(await page.locator('.ant-select-dropdown:visible').getByText('messages-only').count(), 0)
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'relay-fixture' }).click()
    await page.locator('#model').click()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'gpt-6.1-sol' }).click()
    await page.waitForTimeout(100)
    await page.getByRole('button', { name: /开始检测/ }).click()
    for (let i = 0; i < 100 && !posted; i++) await page.waitForTimeout(30)
    assert.deepEqual(posted, { channel_id: 24, model: 'gpt-6.1-sol', baseline: 'gpt-6.1-sol', preset: 'quick' })

    // 先显示进度，再显示报告
    await page.getByText('30 / 60', { exact: true }).waitFor()
    await page.getByText('基本达标，1 项需要关注', { exact: true }).first().waitFor({ timeout: 10000 })
    await page.getByText('88%', { exact: true }).first().waitFor()
    await page.getByText('单词指纹', { exact: true }).waitFor()
    await page.getByText('请求质量（不计分）', { exact: true }).waitFor()
    await page.getByText('19.8K', { exact: true }).waitFor()
    await page.getByText('14.5 tok/s', { exact: true }).waitFor()
    // 展开单词指纹看各型号距离
    await page.getByText('单词指纹', { exact: true }).click()
    await page.getByText('gpt-6.1-terra：0.490（40 题）', { exact: true }).waitFor()
    // 请求明细：零宽字符显示出来
    await page.getByText(/请求明细/).click()
    await page.getByText('⟨零宽⟩kiwi', { exact: true }).waitFor()
    console.log('PASS: channel without chat URL hidden, baseline defaults to model, start posts settings, progress then report, items and request quality, metrics, fingerprint distances, invisible characters shown in request details')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
