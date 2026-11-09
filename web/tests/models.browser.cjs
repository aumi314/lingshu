// 「模型与价格」与管理端定价页回归。使用安装了 playwright 的 Node 环境运行：node web/tests/models.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')
const fs = require('node:fs')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25186', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25186')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const groups = [
      { id: 1, name: 'Official API', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '官方渠道，稳定', retry_count: 1 },
      { id: 8, name: 'Third-party Relay', ratio: '0.5', enabled: true, is_public: true, user_group_ids: [], description: '', retry_count: 3 },
    ]
    const priced = (group_id, name) => ({ group_id, name, protocols: ['chat'], input_price: '1', output_price: '2', cache_read_price: '0', cache_write_price: '0', price_rules: [] })
    // 故意打乱：期望按厂商（Anthropic → OpenAI → DeepSeek → 智谱 → 未识别）排列，同厂商内自然排序
    const names = ['GLM-5.10', 'deepseek-v4.1-flash', 'gpt-6-sol', 'unknown-model', 'GLM-5.3', 'claude-sonnet-5-5']
    const longRule = { name: '长上下文', input_above: 272000, input_price: '4', output_price: '15', cache_read_price: '0.4', cache_write_price: '0' }
    const models = [...names.map((n) => priced(1, n)), { ...priced(8, 'gpt-6-sol'), price_rules: [longRule], fast_multiplier: '2' }]
    let savedModel = null
    let savedGroup = null
    // 集合只在管理端分组列表里出现：包含两个分组，不挂渠道
    const collection = { id: 9, name: 'Relay All', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '', retry_count: 2, is_collection: true, member_ids: [1, 8] }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    await page.route('**/api/**', (route) => {
      const p = new URL(route.request().url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { theme: 'dark', language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/me/models') return json({ groups, models })
      if (p.startsWith('/api/admin/models/') && route.request().method() === 'PUT') { savedModel = route.request().postDataJSON(); return json(savedModel) }
      if (p === '/api/admin/models') return json(names.map((n, i) => ({ id: i + 1, name: n, input_price: '1', output_price: '2', cache_read_price: '0', cache_write_price: '0', price_rules: [], fast_multiplier: n === 'gpt-6-sol' ? '2.0000' : null })))
      if (p.startsWith('/api/admin/groups/') && route.request().method() === 'PUT') { savedGroup = route.request().postDataJSON(); return json(savedGroup) }
      if (p === '/api/admin/groups') return json([...groups, collection])
      if (p === '/api/admin/user-groups') return json([{ id: 1, name: 'default', is_default: true }])
      return json([])
    })
    // 每行第一个等宽文本是模型名（调用入口标签也是等宽，排除）
    const rowNames = () => page.locator('tbody tr').evaluateAll((rows) => rows.map((r) => r.querySelector('.mono')?.textContent?.trim()).filter(Boolean))
    const expected = ['claude-sonnet-5-5', 'gpt-6-sol', 'deepseek-v4.1-flash', 'GLM-5.3', 'GLM-5.10', 'unknown-model']

    // 成员页：分组前有标签；倍率只在 ≠1 时显示；带模型数；按厂商排列；选择记在网址上
    await page.goto('http://127.0.0.1:25186/console/models')
    await page.getByText('claude-sonnet-5-5', { exact: true }).waitFor()
    await page.getByText('服务分组', { exact: true }).waitFor()
    assert.equal(await page.getByText('×1', { exact: true }).count(), 0)
    await page.getByText('×0.5', { exact: true }).waitFor()
    assert.deepEqual(await rowNames(), expected)
    await page.getByText(/官方渠道，稳定/).waitFor()
    await page.locator('.ant-segmented-item').filter({ hasText: 'Third-party Relay' }).click()
    await page.getByText(/价格已按倍率 ×0.5 算好/).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('group'), '8')
    await page.reload()
    await page.getByText(/价格已按倍率 ×0.5 算好/).waitFor()
    assert.deepEqual(await rowNames(), ['gpt-6-sol'])
    // GPT Fast：模型名旁标倍率；展开后列出 Fast 价，长上下文规则价同样 ×2
    await page.getByText('Fast ×2', { exact: true }).waitFor()
    await page.locator('.ant-table-row-expand-icon').first().click()
    const fastLong = page.locator('div').filter({ has: page.getByText(/^Fast · 输入超过 272,000 tokens$/) }).last()
    assert.match(await fastLong.innerText(), /输入 \$8 · 输出 \$30/)

    // 管理端定价页同样按厂商排列；服务分组可拖拽排序
    await page.goto('http://127.0.0.1:25186/console/admin/pricing')
    await page.getByText('claude-sonnet-5-5', { exact: true }).waitFor()
    assert.deepEqual(await rowNames(), expected)
    // 管理端：Fast 列显示倍率；清空倍率保存后提交 null（不开放）
    const gptRow = page.locator('tbody tr').filter({ hasText: 'gpt-6-sol' })
    await gptRow.getByText('×2', { exact: true }).waitFor()
    // 行内编辑：行里没有编辑按钮，点行里的空白单元格（ID 列）打开编辑弹窗
    assert.equal(await gptRow.getByRole('button', { name: '编辑' }).count(), 0)
    assert.equal(await gptRow.getAttribute('aria-label'), '编辑模型 · gpt-6-sol')
    await gptRow.locator('td').first().click()
    const fastInput = page.locator('#fast_multiplier')
    await fastInput.waitFor()
    // 键盘也能打开：行拿到焦点后按 Enter
    await page.getByRole('dialog').getByRole('button', { name: /取\s*消/ }).click()
    await gptRow.focus()
    await page.keyboard.press('Enter')
    assert.equal(await fastInput.inputValue(), '2')
    await fastInput.fill('')
    await page.getByRole('dialog').getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 100 && !savedModel; i++) await page.waitForTimeout(30)
    assert.equal(savedModel.fast_multiplier, null)
    // 行里的删除（取消确认）不会打开编辑
    await gptRow.getByRole('button', { name: /删\s*除/ }).click()
    await page.locator('.ant-popconfirm').getByRole('button', { name: /取\s*消/ }).click()
    assert.equal(await page.getByRole('dialog').count(), 0)
    // 筛选：搜索框按模型名或厂商名匹配；厂商下拉只列出现的厂商并带数量，选择记在网址上，刷新保留；清空回到完整列表
    const search = page.getByPlaceholder('搜索模型名')
    await search.fill('glm')
    await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 2)
    assert.deepEqual(await rowNames(), ['GLM-5.3', 'GLM-5.10'])
    await search.fill('openai')
    await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 1)
    assert.deepEqual(await rowNames(), ['gpt-6-sol'])
    await search.fill('')
    await page.waitForFunction((n) => document.querySelectorAll('tbody tr').length === n, expected.length)
    const vendorSelect = page.getByLabel('厂商')
    await page.getByText('全部厂商 · 6', { exact: true }).waitFor()
    await vendorSelect.click()
    const options = page.locator('.ant-select-item-option')
    await options.first().waitFor()
    assert.deepEqual(await options.evaluateAll((els) => els.map((e) => e.innerText.replace(/\s+/g, ' '))), ['Anthropic 1', 'OpenAI 1', 'DeepSeek 1', 'Zhipu 2', '其他 1'])
    await options.filter({ hasText: 'Zhipu' }).click()
    await page.waitForFunction(() => document.querySelectorAll('tbody tr').length === 2)
    assert.deepEqual(await rowNames(), ['GLM-5.3', 'GLM-5.10'])
    assert.ok(page.url().includes('vendor=Zhipu'))
    await page.reload()
    await page.getByText('GLM-5.3', { exact: true }).waitFor()
    assert.deepEqual(await rowNames(), ['GLM-5.3', 'GLM-5.10'])
    await vendorSelect.click()
    await options.filter({ hasText: '其他' }).click()
    await page.getByText('unknown-model', { exact: true }).waitFor()
    assert.deepEqual(await rowNames(), ['unknown-model'])
    await page.locator('.ant-select-clear').click()
    await page.getByText('claude-sonnet-5-5', { exact: true }).waitFor()
    assert.deepEqual(await rowNames(), expected)
    assert.ok(!page.url().includes('vendor='))
    if (process.env.MODELS_SCREENSHOT_DIR) {
      fs.mkdirSync(process.env.MODELS_SCREENSHOT_DIR, { recursive: true })
      await page.screenshot({ path: path.join(process.env.MODELS_SCREENSHOT_DIR, 'pricing.png') })
    }
    await page.goto('http://127.0.0.1:25186/console/admin/pricing?tab=groups')
    await page.getByText('Third-party Relay', { exact: true }).waitFor()
    assert.equal(await page.locator('tr[data-row-key="1"]').getByRole('button').first().getAttribute('title'), '拖动排序（成员看到的分组顺序也随之改变）')
    // 失败重试：列表显示次数，编辑后提交新值
    await page.locator('tr[data-row-key="1"]').getByText('1 次', { exact: true }).waitFor()
    const relayRow = page.locator('tr[data-row-key="8"]')
    await relayRow.getByText('3 次', { exact: true }).waitFor()
    // 服务分组同样是点行打开编辑，行里没有编辑按钮
    assert.equal(await relayRow.getByRole('button', { name: /编\s*辑/ }).count(), 0)
    assert.equal(await relayRow.getAttribute('aria-label'), '编辑服务分组 · Third-party Relay')
    await relayRow.locator('td').nth(1).click()
    await page.locator('#retry_count').fill('4')
    await page.getByRole('dialog').getByRole('button', { name: /保\s*存|确\s*定/ }).click()
    for (let i = 0; i < 100 && !savedGroup; i++) await page.waitForTimeout(30)
    assert.equal(savedGroup.retry_count, 4)
    // 行里的操作不打开编辑：删除（取消确认）、启用开关、拖动把手
    await relayRow.getByRole('button', { name: /删\s*除/ }).click()
    await page.locator('.ant-popconfirm').getByRole('button', { name: /取\s*消/ }).click()
    assert.equal(await page.getByRole('dialog').count(), 0)
    await relayRow.getByRole('switch').click()
    assert.equal(await page.getByRole('dialog').count(), 0)
    await relayRow.locator('button[title*="拖动排序"]').click()
    assert.equal(await page.getByRole('dialog').count(), 0)
    // 集合：名字旁标“集合”并列出包含的分组，倍率显示“按成员分组”；编辑时没有倍率，改选包含的分组，保存带上类型和成员
    const colRow = page.locator('tbody tr').filter({ hasText: 'Relay All' })
    await colRow.getByText('集合', { exact: true }).waitFor()
    await colRow.getByText('包含：Official API、Third-party Relay', { exact: true }).waitFor()
    await colRow.getByText('按成员分组', { exact: true }).waitFor()
    assert.equal(await colRow.getAttribute('aria-label'), '编辑集合 · Relay All')
    await colRow.locator('td').nth(1).click()
    const colDialog = page.getByRole('dialog')
    await colDialog.getByText('包含的分组', { exact: true }).waitFor()
    assert.equal(await page.locator('#ratio').count(), 0)
    savedGroup = null
    await colDialog.getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 100 && !savedGroup; i++) await page.waitForTimeout(30)
    assert.equal(savedGroup.is_collection, true)
    assert.deepEqual(savedGroup.member_ids, [1, 8])
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    await page.getByRole('button', { name: '创建集合' }).click()
    await page.getByRole('dialog').getByText('创建集合', { exact: true }).waitFor()
    assert.equal(await page.locator('#ratio').count(), 0)
    await page.getByRole('dialog').getByRole('button', { name: /取\s*消/ }).click()
    await page.getByRole('dialog').waitFor({ state: 'detached' })
    if (process.env.MODELS_SCREENSHOT_DIR) {
      await page.screenshot({ path: path.join(process.env.MODELS_SCREENSHOT_DIR, 'groups.png') })
      await page.goto('http://127.0.0.1:25186/console/models')
      await page.getByText('claude-sonnet-5-5', { exact: true }).waitFor()
      await page.screenshot({ path: path.join(process.env.MODELS_SCREENSHOT_DIR, 'models.png') })
    }
    console.log('PASS: model search by name or vendor, vendor dropdown with counts kept in the URL, group selector label, ratio tag only when not 1, model counts, group in URL, vendor order on member and admin pages, GPT Fast prices incl. long context, admin Fast multiplier cleared to null, group drag handle, group retry count shown and saved, collection row/members/edit/create')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
