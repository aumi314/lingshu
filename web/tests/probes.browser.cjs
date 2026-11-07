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
    const channel = { id: 24, name: 'relay-fixture', template: 'standard', api_key_masked: 'sk-…', priority: 0, enabled: true, base_url_chat: 'https://up.example/v1', base_url_messages: '', base_url_responses: '', base_url_systemone: '', proxy_url: '', group_ids: [1], models: [{ model_id: 1, model_name: 'gpt-6.1-sol', upstream_name: '' }, { model_id: 2, model_name: 'claude-x', upstream_name: '' }, { model_id: 3, model_name: 'claude-opus-5-5', upstream_name: '' }, { model_id: 4, model_name: 'claude-sonnet-5-5', upstream_name: '' }], balance: [], balance_checked_at: null, balance_error: '', stats: null }
    const msgOnly = { ...channel, id: 25, name: 'messages-only', base_url_chat: '', base_url_messages: 'https://up.example' } // 只有 Messages 地址：只能做 Claude 协议检测
    const noURL = { ...channel, id: 26, name: 'no-url', base_url_chat: '' } // 两个协议地址都没有，不能检测，不出现在下拉里
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
      if (p === '/api/admin/channels') return json([channel, msgOnly, noURL])
      if (p === '/api/admin/probe-baselines') return json({ models: ['claude-sonnet-5-5', 'gpt-6-astra', 'gpt-6-luna', 'gpt-6.1-sol', 'gpt-6.1-terra'], protocol_models: ['claude-opus-5-5', 'claude-sonnet-5-5'] })
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

    // Claude 走 Messages：只有 Messages 地址的渠道里 claude-opus-5-5 可选（协议检测），GPT 置灰
    await page.locator('#channel_id').click()
    assert.equal(await page.locator('.ant-select-dropdown:visible').getByText('no-url').count(), 0)
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'messages-only' }).click()
    await page.locator('#model').click()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option:not(.ant-select-item-option-disabled)').filter({ hasText: 'claude-opus-5-5（协议检测）' }).waitFor()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option-disabled').filter({ hasText: 'gpt-6.1-sol（渠道没有 Chat 地址）' }).waitFor()
    // 有官方样本的 Claude 不再标“协议检测”
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option:not(.ant-select-item-option-disabled)').filter({ hasText: /^claude-sonnet-5-5$/ }).waitFor()
    await page.keyboard.press('Escape')

    // 只选渠道和模型：没有官方样本的模型是灰的；Chat 渠道上的 Claude 也是灰的
    await page.locator('#channel_id').click()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'relay-fixture' }).click()
    await page.locator('#model').click()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option-disabled').filter({ hasText: 'claude-x（暂无官方样本）' }).waitFor()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option-disabled').filter({ hasText: 'claude-opus-5-5（渠道没有 Messages 地址）' }).waitFor()
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'gpt-6.1-sol' }).click()
    await page.waitForTimeout(100)
    await page.getByText('完整', { exact: true }).click()
    await page.getByRole('button', { name: /开始检测/ }).click()
    for (let i = 0; i < 100 && !posted; i++) await page.waitForTimeout(30)
    assert.deepEqual(posted, { channel_id: 24, model: 'gpt-6.1-sol', preset: 'full' })

    // 结果在弹窗里：先显示进度，再显示报告
    const dialog = page.getByRole('dialog')
    await dialog.getByText('30 / 60', { exact: true }).waitFor()
    await page.locator('tbody tr').filter({ hasText: 'relay-fixture' }).getByText('30 / 60', { exact: true }).waitFor() // 列表里也显示已完成 / 总数
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
    // 关闭弹窗回到列表；点检测记录再次打开
    await dialog.locator('.ant-modal-footer').getByRole('button', { name: /关\s*闭/ }).click()
    await dialog.waitFor({ state: 'hidden' })
    await page.locator('tbody tr').filter({ hasText: 'relay-fixture' }).first().click()
    await page.getByRole('dialog').getByText('88%', { exact: true }).first().waitFor()
    console.log('PASS: progress shown as done / total in the list, channel without any probe URL hidden, Claude protocol check only on channels with a Messages URL, only channel and model chosen, models without baseline disabled, result in a modal with close and reopen from history, progress then report, items and request quality, metrics, fingerprint distances, invisible characters shown in request details')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
