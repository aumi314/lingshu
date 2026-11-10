// API key 有效期：创建、回填、清空、过期校验与本地时区往返。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25197', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25197')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, timezoneId: 'Asia/Shanghai' })
    const writes = []
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    const common = { prefix: 'sk-ls-fixture', group_id: 1, group_name: 'default', quota_limit: null, quota_used: '0', enabled: true, last_used_at: null, created_at: '2026-01-01T00:00:00Z' }
    const keys = [
      { ...common, id: 1, name: 'permanent', expires_at: null },
      { ...common, id: 2, name: 'temporary', expires_at: '2099-12-25T09:30:00Z' },
      { ...common, id: 3, name: 'expired', expires_at: '2020-01-01T12:00:00.123456Z' },
    ]
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (body, status = 200) => route.fulfill({ json: body, status })
      if (p === '/api/me') return json({ user: { id: 2, username: 'member-fixture', role: 'user', balance: '8', enabled: true, user_group_ids: [], preferences: { theme: 'dark', language: 'zh' }, created_at: '2026-01-01' }, cny_per_usd: '6.7' })
      if (p === '/api/me/models') return json({ groups: [{ id: 1, name: 'default', ratio: '1', enabled: true, description: '' }], models: [] })
      if (p === '/api/me/endpoints') return json({ api_base_url: 'https://gateway.example/v1' })
      if (p === '/api/me/keys' && req.method() === 'GET') return json(keys)
      if (p === '/api/me/keys' && req.method() === 'POST') {
        const body = req.postDataJSON()
        writes.push(body)
        const id = keys.length + 1
        keys.push({ ...common, ...body, id })
        return json({ id, key: 'sk-ls-browser-fixture' }, 201)
      }
      if (p.startsWith('/api/me/keys/') && req.method() === 'PATCH') {
        const body = req.postDataJSON()
        writes.push(body)
        Object.assign(keys.find((k) => k.id === Number(p.split('/').at(-1))), body)
        return json({ ok: true })
      }
      return json({})
    })
    const row = (id) => page.locator(`tr[data-row-key="${id}"]`)
    const dialog = () => page.getByRole('dialog', { name: /^(创建|编辑) API key$/ })
    const input = () => dialog().getByPlaceholder('永不过期', { exact: true })
    const submit = async () => {
      const previous = writes.length
      await dialog().getByRole('button', { name: /创\s*建|保\s*存/ }).click()
      await page.waitForFunction(() => !document.querySelector('.ant-modal input#name'))
      assert.equal(writes.length, previous + 1)
    }
    const cancel = async () => {
      await dialog().getByRole('button', { name: /取\s*消/ }).click()
      await dialog().waitFor({ state: 'hidden' })
    }
    const edit = async (id) => { await row(id).getByRole('button', { name: /编\s*辑/ }).click(); await input().waitFor(); await page.waitForTimeout(350) }
    const create = async () => { await page.getByRole('button', { name: /创建 API key/ }).click(); await input().waitFor(); await page.waitForTimeout(350) }
    await page.goto('http://127.0.0.1:25197/console/keys')
    await row(1).getByText('永不过期', { exact: true }).waitFor()
    await row(2).getByText('2099-12-25 17:30', { exact: true }).waitFor()
    await row(3).getByText('已过期', { exact: true }).waitFor()

    await create()
    assert.equal(await input().inputValue(), '')
    await dialog().getByLabel('名称', { exact: true }).fill('default-never')
    await submit()
    assert.equal(writes.at(-1).expires_at, null)
    await page.getByRole('dialog', { name: 'API key 已创建' }).getByRole('button', { name: /我已保存/ }).click()
    await page.getByRole('dialog', { name: 'API key 已创建' }).waitFor({ state: 'hidden' })

    await create()
    await dialog().getByLabel('名称', { exact: true }).fill('future')
    await input().fill('2099-12-25 17:30:00')
    await input().press('Enter')
    await submit()
    assert.equal(writes.at(-1).expires_at, '2099-12-25T09:30:00.000Z')
    await page.getByRole('dialog', { name: 'API key 已创建' }).getByRole('button', { name: /我已保存/ }).click()
    await page.getByRole('dialog', { name: 'API key 已创建' }).waitFor({ state: 'hidden' })

    await edit(2)
    assert.equal(await input().inputValue(), '2099-12-25 17:30:00')
    await dialog().getByLabel('名称', { exact: true }).fill('temporary-renamed')
    await submit()
    assert.equal(writes.at(-1).expires_at, '2099-12-25T09:30:00.000Z')
    await edit(2)
    await input().hover()
    await dialog().getByRole('button', { name: '清除', exact: true }).click()
    await submit()
    assert.equal(writes.at(-1).expires_at, null)
    await row(2).getByText('永不过期', { exact: true }).waitFor()

    await edit(3)
    assert.equal(await input().inputValue(), '2020-01-01 20:00:00')
    await dialog().getByLabel('名称', { exact: true }).fill('expired-renamed')
    await submit()
    assert.equal(writes.at(-1).expires_at, '2020-01-01T12:00:00.123Z')
    await edit(5)
    await cancel()
    await create()
    assert.equal(await input().inputValue(), '') // 编辑后的新建表单重置
    await cancel()

    // 今天较早时刻可选日期，但保存时要拦截。
    await edit(1)
    const earlier = await page.evaluate(() => {
      const d = new Date(Date.now() - 1000)
      const pad = (n) => String(n).padStart(2, '0')
      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
    })
    await input().fill(earlier)
    await input().press('Enter')
    const previous = writes.length
    await dialog().getByRole('button', { name: /保\s*存/ }).click()
    await dialog().getByText('过期时间要晚于现在', { exact: true }).waitFor()
    assert.equal(writes.length, previous)
    await cancel()

    await edit(5)
    if (process.env.KEYS_TEST_SCREENSHOT) await page.screenshot({ path: process.env.KEYS_TEST_SCREENSHOT, fullPage: true })
    const box = await dialog().boundingBox()
    assert.ok(box && box.y >= 0 && box.y + box.height <= 1000, '编辑窗口应在可见区域内')
    assert.deepEqual(errors, [])
    console.log('API key expiration browser regression passed')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((e) => { console.error(e); process.exitCode = 1 })
