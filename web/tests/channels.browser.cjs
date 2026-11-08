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
    const oneKey = [{ id: 90, masked: 'sk-z…zzzz', enabled: true, note: '', status: { state: 'ok' } }]
    const base = { template: 'standard', note: '', keys: oneKey, api_key_masked: 'sk-…', priority: 0, enabled: true, base_url_chat: 'https://up.example/v1', base_url_messages: '', base_url_responses: '', base_url_systemone: '', proxy_url: '', group_ids: [1], models: [{ model_id: 1, model_name: 'gpt-6-sol', upstream_name: '' }], balance: [], balance_checked_at: null, balance_error: '' }
    const stat = { success: 0, failed: 0, first_token_ms: null, tokens_per_sec: null, cache_read_tokens: 0, input_tokens: 0, last_success_at: null, last_failure_at: null }
    const channels = [
      // 健康：97 / 100 → 97%，首字 2.35s → 2.4s（保留一位），缓存 870 / 1000
      { ...base, id: 1, name: 'healthy', template: 'custom', note: '质量好，0.15 倍', keys: [
        { id: 11, masked: 'sk-a…aaaa', enabled: true, note: '主号', status: { state: 'ok' }, stats: { success: 12, failed: 0 } },
        { id: 12, masked: 'sk-b…bbbb', enabled: true, note: '', status: { state: 'cooling', until: '2026-11-07T12:00:00Z', reason: 'HTTP 429 rate limited' }, stats: { success: 3, failed: 2 } },
        { id: 13, masked: 'sk-c…cccc', enabled: true, note: '', status: { state: 'invalid', until: '2026-11-07T12:30:00Z', reason: 'HTTP 401 invalid key' } },
      ], stats: { ...stat, success: 97, failed: 3, first_token_ms: 2350, tokens_per_sec: 45, cache_read_tokens: 870, input_tokens: 1000, last_success_at: '2026-11-06T07:00:00Z', last_failure_at: '2026-11-06T06:00:00Z' } },
      // 199 / 200 也只显示 99%，不四舍五入成 100%
      { ...base, id: 2, name: 'almost', stats: { ...stat, success: 199, failed: 1 } },
      { ...base, id: 3, name: 'broken', cooling: true, stats: { ...stat, success: 1, failed: 9 } },
      { ...base, id: 4, name: 'idle', stats: null },
      { ...base, id: 5, name: 'empty', models: [], stats: null },
    ]
    let saved = null
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await page.route('**/api/**', (route) => {
      const p = new URL(route.request().url()).pathname
      const json = (b) => route.fulfill({ json: b })
      if (p === '/api/me') return json({ user: { id: 1, username: 'admin', role: 'admin', balance: '5', enabled: true, user_group_ids: [1], preferences: { language: 'zh' }, created_at: '2026-10-25' }, cny_per_usd: '6.7' })
      if (p === '/api/admin/channels/1' && route.request().method() === 'PUT') { saved = route.request().postDataJSON(); return json({ id: 1, keys_deduped: 1 }) }
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
    // 渠道备注显示在名称下方；多 key 的渠道标出 key 数，有失效的标红
    await row('healthy').getByText('质量好，0.15 倍', { exact: true }).waitFor()
    assert.equal(await row('healthy').locator('.ant-tag-red').filter({ hasText: '3 把 key' }).count(), 1)
    assert.equal(await row('idle').getByText(/把 key/).count(), 0)
    // 编辑抽屉：每把 key 的掩码、状态、24 小时次数。行里没有编辑按钮，点行里的空白单元格（ID 列）打开抽屉
    assert.equal(await row('healthy').getByRole('button', { name: /编\s*辑/ }).count(), 0)
    assert.equal(await row('healthy').getAttribute('aria-label'), '编辑渠道 · healthy')
    await row('healthy').locator('td').nth(1).click()
    await page.getByText('编辑渠道 · healthy', { exact: true }).waitFor()
    const drawer = page.locator('.ant-drawer-section')
    await drawer.getByText('sk-a…aaaa', { exact: true }).waitFor()
    await drawer.getByText('正常', { exact: true }).waitFor()
    await drawer.getByText(/^冷却中（到 /).waitFor()
    await drawer.getByText(/^失效（/).waitFor()
    await drawer.getByText('12 / 0').waitFor()
    // 追加：粘贴里的重复当场去掉并提示
    await drawer.getByRole('button', { name: /添加 key/ }).click()
    const dlg = page.getByRole('dialog').filter({ hasText: '追加' })
    await dlg.locator('textarea').fill('sk-new-key-12345678\n\nsk-new-key-12345678')
    await dlg.getByRole('button', { name: /确\s*定/ }).click()
    await page.getByText('将追加 1 把 key（粘贴里去掉 1 把重复），保存后生效', { exact: true }).waitFor()
    await drawer.getByText('待保存', { exact: true }).waitFor()
    // 覆盖：只留这次粘贴的
    await drawer.getByRole('button', { name: /添加 key/ }).click()
    await dlg.getByText('覆盖', { exact: true }).click()
    await dlg.getByText(/覆盖会替换现有的全部 key/).waitFor()
    await dlg.locator('textarea').fill('sk-only-one-12345678\nsk-a-same-as-old-123')
    await dlg.getByRole('button', { name: /确\s*定/ }).click()
    await drawer.getByText('sk-o…5678', { exact: true }).waitFor()
    await drawer.getByText('sk-a…-123', { exact: true }).waitFor()
    for (const old of ['sk-a…aaaa', 'sk-b…bbbb', 'sk-c…cccc', 'sk-n…5678']) assert.equal(await drawer.getByText(old, { exact: true }).count(), 0)
    // 保存：新 key 带完整内容；服务端去掉的重复数提示出来
    await drawer.locator('.ant-drawer-extra').getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 100 && !saved; i++) await page.waitForTimeout(30)
    assert.deepEqual(saved.keys, [{ api_key: 'sk-only-one-12345678', enabled: true, note: '' }, { api_key: 'sk-a-same-as-old-123', enabled: true, note: '' }])
    assert.equal(saved.note, '质量好，0.15 倍')
    assert.equal(saved.api_key, undefined)
    await page.getByText('渠道已保存，自动去掉了 1 把重复的 key', { exact: true }).waitFor()
    // 行里的操作各自独立，不打开编辑：删除（取消确认）、启用开关、拖动把手、额度
    const drawerTitle = page.getByText('编辑渠道 · healthy', { exact: true })
    await row('healthy').getByRole('button', { name: /删\s*除/ }).click()
    await page.locator('.ant-popconfirm').getByRole('button', { name: /取\s*消/ }).click()
    assert.equal(await drawerTitle.isVisible(), false)
    await row('almost').getByRole('switch').click()
    assert.equal(await drawerTitle.isVisible(), false)
    await row('healthy').locator('button[title*="拖动排序"]').click()
    assert.equal(await drawerTitle.isVisible(), false)
    await row('healthy').locator('[data-row-action]').click()
    assert.equal(await drawerTitle.isVisible(), false)
    // 拖动把手真的能改顺序，拖的过程中不会打开编辑
    const order = () => page.locator('tbody tr').evaluateAll((rows) => rows.map((r) => r.cells[1]?.textContent?.trim()).filter(Boolean))
    const before = await order()
    const handle = row('healthy').locator('button[title*="拖动排序"]')
    const box = await handle.boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2 + 60, { steps: 12 })
    await page.mouse.up()
    await page.waitForTimeout(400)
    assert.notDeepEqual(await order(), before)
    assert.equal(await drawerTitle.isVisible(), false)
    console.log('PASS: channel note under the name, key count tag colored by key health, per-key status and 24h counts in the drawer, append dedups pasted keys, replace keeps only pasted keys, save payload carries new keys only in full, server dedup count shown; channel list shows connection status instead of models: success rate floors, colors, TTFT/speed/cache line, cooling tag, idle and no-model states, tooltip details')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
