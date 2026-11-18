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
    const channel = { id: 24, name: 'relay-fixture', template: 'standard', api_key_masked: 'sk-…', priority: 0, enabled: true, base_url_chat: 'https://up.example/v1', base_url_messages: '', base_url_responses: 'https://up.example/v1', base_url_systemone: '', proxy_url: '', group_ids: [1], models: [{ model_id: 1, model_name: 'gpt-6.1-sol', upstream_name: '' }, { model_id: 2, model_name: 'claude-x', upstream_name: '' }, { model_id: 3, model_name: 'claude-opus-5-5', upstream_name: '' }, { model_id: 4, model_name: 'claude-sonnet-5-5', upstream_name: '' }], balance: [], balance_checked_at: null, balance_error: '', stats: null }
    const msgOnly = { ...channel, id: 25, name: 'messages-only', base_url_chat: '', base_url_responses: '', base_url_messages: 'https://up.example' }
    const noURL = { ...channel, id: 26, name: 'no-url', base_url_chat: '', base_url_responses: '' }
    const base = { id: 7, created_at: '2026-11-07T08:43:00Z', finished_at: null, created_by: 'admin', channel_id: 24, channel_name: 'relay-fixture', model: 'gpt-6.1-sol', upstream_model: 'gpt-6.1-sol', baseline: 'gpt-6.1-sol', preset: 'quality', score: null, verdict: '', cost: '0' }
    const report = {
      baseline: 'gpt-6.1-sol', protocol: 'responses', disguise: true, score: 88, verdict: '基本达标，1 项需要关注',
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
    const fingerprintReport = {
      baseline: 'gpt-6.1-sol', probe_mode: 'fingerprint', score: 82, verdict: '4 个会话一致，最像 gpt-6.1-sol',
      counts: { planned: 4, sent: 4, completed: 4, failed: 0, skipped: 0 },
      items: [{ key: 'fingerprint', title: '单词指纹', status: 'pass', summary: '4 个会话一致', details: { ranking: [{ model: 'gpt-6.1-sol', distance: 0.18, prompts: 40 }] } }],
      observations: [{ key: 'injection', title: '注入', status: 'warn', summary: '请求质量细节' }],
      metrics: { requests: 4, errors: 0 }, calls: [],
    }
    const failedReport = {
      baseline: 'gpt-6.1-sol', probe_mode: 'quality', score: null, verdict: '上游计费的输入 token 异常，已中止检测',
      stop_reason: 'billing_anomaly', stop_call_index: 1,
      counts: { planned: 12, sent: 3, completed: 2, failed: 1, skipped: 9 },
      items: [], observations: [], metrics: { requests: 3, errors: 1 },
      calls: [
        { item: 'tokens', prompt: '第一题', text: 'ok', input_tokens: 100, output_tokens: 2, reasoning_tokens: 0, first_token_ms: 100, total_ms: 200 },
        { item: 'tokens', prompt: '计费异常题', text: '', error: 'upstream reported 50000 input tokens', input_tokens: 50000, output_tokens: 0, reasoning_tokens: 0, first_token_ms: 0, total_ms: 300 },
      ],
    }
    let posted = null
    let polls = 0
    let selectedRunID = 7
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/channels') return json([channel, msgOnly, noURL])
      if (p === '/api/admin/probe-baselines') return json({ models: ['claude-sonnet-5-5', 'gpt-6-astra', 'gpt-6-luna', 'gpt-6.1-sol', 'gpt-6.1-terra'], protocol_models: ['claude-opus-5-5', 'claude-sonnet-5-5'] })
      if (p === '/api/admin/probes' && req.method() === 'POST') { posted = req.postDataJSON(); selectedRunID = posted.preset === 'fingerprint' ? 8 : 9; return json({ id: selectedRunID }) }
      if (p === '/api/admin/probes') return json(posted ? [{ ...base, id: 9, status: polls < 2 ? 'running' : 'done', score: polls < 2 ? null : 88, verdict: polls < 2 ? '' : report.verdict, progress: [30, 60] }] : [])
      if (p === '/api/admin/probes/8') return json({ ...base, id: 8, status: 'done', preset: 'fingerprint', score: fingerprintReport.score, verdict: fingerprintReport.verdict, cost: '0.0200', report: fingerprintReport })
      if (p === '/api/admin/probes/9') {
        polls++
        return json(polls < 2 ? { ...base, id: 9, status: 'running', progress: [30, 60] } : { ...base, id: 9, status: 'done', score: 88, verdict: report.verdict, cost: '0.0123', report })
      }
      if (p === '/api/admin/probes/10') {
        const failed = { ...base, id: 10, status: 'failed', score: null, verdict: failedReport.verdict, cost: '0.0010', report: failedReport }
        return json(polls < 11 ? { ...failed, status: 'running', report: undefined } : failed)
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
    await page.locator('.ant-select-dropdown:visible .ant-select-item-option-disabled').filter({ hasText: 'gpt-6.1-sol（渠道没有 Responses / Chat 地址）' }).waitFor()
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
    await page.getByRole('button', { name: '渠道质量测试', exact: true }).click()
    for (let i = 0; i < 100 && !posted; i++) await page.waitForTimeout(30)
    assert.deepEqual(posted, { channel_id: 24, model: 'gpt-6.1-sol', preset: 'quality' })

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
    await page.getByRole('dialog').locator('.ant-modal-footer').getByRole('button', { name: /关\s*闭/ }).click()
    await page.getByRole('dialog').waitFor({ state: 'hidden' })

    // 指纹报告显示匹配度，不显示分词和请求质量
    posted = null
    polls = 0
    await page.getByRole('button', { name: '模型指纹辨别', exact: true }).click()
    for (let i = 0; i < 100 && !posted; i++) await page.waitForTimeout(30)
    assert.deepEqual(posted, { channel_id: 24, model: 'gpt-6.1-sol', preset: 'fingerprint' })
    const fingerprintDialog = page.getByRole('dialog')
    await fingerprintDialog.getByText(fingerprintReport.verdict, { exact: true }).waitFor()
    await fingerprintDialog.getByText('指纹匹配度', { exact: true }).waitFor()
    await fingerprintDialog.getByText('82%', { exact: true }).waitFor()
    assert.equal(await fingerprintDialog.getByText('分词指纹', { exact: true }).count(), 0)
    assert.equal(await fingerprintDialog.getByText('请求质量（不计分）', { exact: true }).count(), 0)
    assert.equal(await fingerprintDialog.getByText('14.5 tok/s', { exact: true }).count(), 0)
    await fingerprintDialog.locator('.ant-modal-footer').getByRole('button', { name: /关\s*闭/ }).click()
    await fingerprintDialog.waitFor({ state: 'hidden' })

    // 历史档位保留中文标签；失败报告展示触发原因、调用计数和请求证据
    await page.unroute('**/api/**')
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/channels') return json([channel])
      if (p === '/api/admin/probe-baselines') return json({ models: ['gpt-6.1-sol'], protocol_models: [] })
      if (p === '/api/admin/probes' && req.method() === 'POST') return json({ id: 10 })
      if (p === '/api/admin/probes') return json([{ ...base, id: 10, preset: 'quick', status: 'failed', score: null, verdict: failedReport.verdict }])
      if (p === '/api/admin/probes/10') {
        const failed = { ...base, id: 10, status: 'failed', score: null, verdict: failedReport.verdict, cost: '0.0010', report: failedReport }
        return json(failed)
      }
      return json([])
    })
    await page.reload()
    await page.getByText('检测记录', { exact: true }).waitFor()
    await page.locator('tbody tr').filter({ hasText: 'relay-fixture' }).getByText('快速', { exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: '渠道质量测试', exact: true }).count(), 1)
    await page.locator('tbody tr').filter({ hasText: 'relay-fixture' }).click()
    const failedDialog = page.getByRole('dialog')
    await failedDialog.getByText('检测未完成', { exact: true }).waitFor()
    await failedDialog.getByText('失败证据', { exact: true }).waitFor()
    await failedDialog.getByText(/触发原因：.*上游异常计费/).waitFor()
    await failedDialog.getByText(/第 2 个请求/).waitFor()
    await failedDialog.getByText('调用统计：计划 12 · 已发 3 · 完成 2 · 失败 1 · 跳过 9', { exact: true }).waitFor()
    await failedDialog.getByText('计费异常题', { exact: true }).waitFor()
    await failedDialog.getByText('upstream reported 50000 input tokens', { exact: true }).waitFor()
    console.log('PASS: two direct check buttons send quality/fingerprint, historical presets keep their labels, fingerprint shows similarity without tokenization or request quality, failed report shows trigger, counts and request evidence')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
