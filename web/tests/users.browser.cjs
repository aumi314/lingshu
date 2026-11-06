// 使用安装了 playwright 的 Node 环境运行：node web/tests/users.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = process.env.USERS_TEST_URL ? null : spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25179', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    if (server) {
      for (let i = 0; i < 100; i++) {
        try { if ((await fetch('http://127.0.0.1:25179')).ok) break } catch {}
        await new Promise((resolve) => setTimeout(resolve, 50))
      }
    }
    const page = await browser.newPage()
    const users = [1, 2, 3].map((id) => ({ id, username: `user${id}`, role: id === 1 ? 'admin' : 'user', balance: '5', enabled: true, user_group_ids: [1], preferences: {}, created_at: '2026-10-25' }))
    users[1].note = 'partner-note'
    users[1].last_used_at = '2026-11-03T08:00:00Z'
    users[2].user_group_ids = [1, 2]
    users[2].enabled = false
    const userGroups = [{ id: 1, name: 'default', is_default: true }, { id: 2, name: 'friend', is_default: false }]
    const groups = [
      { id: 1, name: 'open-group', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '' },
      { id: 2, name: 'vip-group', ratio: '0.5', enabled: true, is_public: false, user_group_ids: [2], description: '' },
    ]
    let statusFail = false
    const patches = []
    await page.route('**/api/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      const json = (body, status = 200) => route.fulfill({ status, json: body })
      if (path === '/api/me') return json({ user: users[0], cny_per_usd: '6.7' })
      if (path === '/api/admin/user-groups') return json(userGroups)
      if (path === '/api/admin/groups') return json(groups)
      if (path === '/api/admin/users') return json(users)
      const match = path.match(/\/users\/(\d+)$/)
      if (!match || route.request().method() !== 'PATCH') return json({})
      if (statusFail) return json({ error: 'status failed' }, 500)
      const id = Number(match[1])
      const body = route.request().postDataJSON()
      patches.push(body)
      Object.assign(users[id - 1], body)
      if (body.balance_adjustment) users[id - 1].balance = '8'
      return json({})
    })
    const waitFor = async (fn) => {
      for (let i = 0; i < 300; i++) {
        if (await fn()) return
        await page.waitForTimeout(30)
      }
      throw new Error('Timed out waiting for condition')
    }
    const row = (id) => page.getByRole('row').filter({ has: page.getByText(users[id - 1].username, { exact: true }) })
    const open = async (id) => row(id).getByRole('button', { name: '编辑', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: /^(编辑用户|Edit user)/ })
    const close = async () => {
      await dialog.getByRole('button', { name: /取\s*消/ }).click()
      await dialog.waitFor({ state: 'hidden' })
    }
    await page.goto(process.env.USERS_TEST_URL || 'http://127.0.0.1:25179/console/admin/users')

    // 列表：用户分组管理不再占顶部按钮；能看出谁能用哪些服务分组、最近有没有在用
    await row(1).waitFor()
    // 默认只看启用：停用的 user3 不显示，网址不带参数；切到全部后再做列表检查
    assert.equal(await row(3).count(), 0)
    assert.equal(new URL(page.url()).searchParams.get('status'), null)
    await page.getByText('全部 3', { exact: true }).click()
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'all')
    assert.equal(await page.getByRole('button', { name: /用户分组管理/ }).count(), 0)
    assert.equal(await row(1).getByRole('combobox').count(), 0) // 角色不再在表格里直接改
    await row(1).getByText('管理员', { exact: true }).waitFor()
    await row(2).getByText('partner-note', { exact: true }).waitFor()
    await row(2).getByText('公开分组 1 个', { exact: true }).waitFor()
    await row(3).getByText('vip-group', { exact: true }).waitFor()
    await row(3).getByText('+ 公开分组 1 个', { exact: true }).waitFor()
    await row(1).getByText('从未', { exact: true }).waitFor()
    assert.equal(await row(2).getByText('从未', { exact: true }).count(), 0)

    // 按启用状态筛选：选项带人数，选择记在网址上
    await page.getByText('仅看停用 1', { exact: true }).click()
    await waitFor(async () => (await row(1).count()) === 0)
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'disabled')
    await page.reload()
    await row(3).waitFor()
    assert.equal(await row(1).count(), 0)
    await page.getByText('仅看启用 2', { exact: true }).click()
    await waitFor(async () => (await row(3).count()) === 0)
    assert.equal(new URL(page.url()).searchParams.get('status'), null)
    await page.getByText('全部 3', { exact: true }).click()
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'all')

    // 编辑：角色在弹窗里改；选了用户分组，下方即时显示能用哪些服务分组
    await open(2)
    await dialog.getByText('可用服务分组：open-group', { exact: true }).waitFor()
    await dialog.getByText('成员', { exact: true }).click()
    await dialog.locator('#user_group_ids').click()
    await page.locator('.ant-select-dropdown:visible').getByText('friend', { exact: true }).click()
    await dialog.getByText('可用服务分组：open-group、vip-group', { exact: true }).waitFor()
    await close()
    assert.equal(patches.length, 0)

    // 取消丢弃全部草稿
    await open(2)
    await dialog.locator('#username').fill('discarded-name')
    await dialog.locator('#amount').fill('3')
    await dialog.getByRole('switch').click()
    await close()
    assert.equal(patches.length, 0)
    await open(2)
    assert.equal(await dialog.locator('#username').inputValue(), 'user2')
    assert.equal(await dialog.locator('#amount').inputValue(), '')
    assert.equal(await dialog.getByRole('switch').isChecked(), true)

    // 停用需确认；失败保留草稿；成功后资料、角色、状态和额度一起提交
    await dialog.locator('#username').fill('saved-name')
    await dialog.locator('#amount').fill('3')
    await dialog.getByText('管理员', { exact: true }).click()
    await dialog.getByRole('switch').click()
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    const confirmation = page.getByRole('dialog').filter({ hasText: '停用账号？' })
    await confirmation.getByRole('button', { name: /取\s*消/ }).click()
    await confirmation.waitFor({ state: 'hidden' })
    assert.equal(patches.length, 0)
    statusFail = true
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await confirmation.getByRole('button', { name: /提\s*交/ }).click()
    await page.getByText('status failed', { exact: true }).waitFor()
    await confirmation.waitFor({ state: 'hidden' })
    assert.equal(await dialog.locator('#username').inputValue(), 'saved-name')
    assert.equal(await dialog.locator('#amount').inputValue(), '3')
    assert.equal(await dialog.getByRole('switch').isChecked(), false)
    statusFail = false
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await confirmation.getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => patches.length === 1)
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(patches[0].username, 'saved-name')
    assert.equal(patches[0].role, 'admin')
    assert.equal(patches[0].enabled, false)
    assert.deepEqual(patches[0].balance_adjustment, { mode: 'add', amount: '3', currency: 'USD', note: '' })

    // 额度留空不调余额
    await open(2)
    await dialog.getByText('$8.0000', { exact: true }).waitFor()
    await dialog.getByRole('switch').click()
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => patches.length === 2)
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(patches[1].enabled, true)
    assert.equal('balance_adjustment' in patches[1], false)

    // 不能停用自己，也不能取消自己的管理员身份
    await open(1)
    assert.equal(await dialog.getByRole('switch').isDisabled(), true)
    await dialog.getByText('不能取消自己的管理员身份', { exact: true }).waitFor()
    await close()

    // 手机宽度：表格在内部左右滑，弹窗底部按钮可见；英文界面
    await page.setViewportSize({ width: 390, height: 844 })
    await page.reload()
    await row(1).waitFor()
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1))
    await open(1)
    await dialog.getByRole('button', { name: /提\s*交/ }).waitFor({ state: 'visible' })
    users[0].preferences = { language: 'en' }
    await page.reload()
    await row(2).getByRole('button', { name: 'Edit', exact: true }).click()
    await dialog.getByText('Account status', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: 'Submit', exact: true }).waitFor()
    console.log('PASS: enabled-only by default, status filter with counts and URL, service group access column, last call, note, role moved into dialog with self protection, access preview, cancel discards drafts, confirmation cancellation, failure preserves drafts, blank amount skips balance, mobile scroll and footer, English')
  } finally {
    await browser.close()
    server?.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
