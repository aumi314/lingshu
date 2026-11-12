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
    const users = [1, 2, 3, 4].map((id) => ({ id, username: `user${id}`, role: id === 1 ? 'super_admin' : 'user', balance: '5', enabled: true, user_group_ids: [1], preferences: {}, created_at: '2026-10-25' }))
    users[1].note = 'partner-note'
    users[1].last_used_at = '2026-11-03T08:00:00Z'
    // user3 属于两个用户分组，但能用哪些服务分组是另一回事：这里用来看清「用户分组」列显示的是分组本身
    users[2].user_group_ids = [1, 2]
    users[2].enabled = false
    users[3].user_group_ids = []
    const userGroups = [{ id: 1, name: 'default', is_default: true }, { id: 2, name: 'friend', is_default: false }]
    const groups = [
      { id: 1, name: 'open-group', ratio: '1', enabled: true, is_public: true, user_group_ids: [], description: '' },
      { id: 2, name: 'vip-group', ratio: '0.5', enabled: true, is_public: false, user_group_ids: [2], description: '' },
    ]
    let failNext = false
    let patchDelay = 0
    const togglePatches = [] // 只带 enabled 的开关操作
    const savePatches = [] // 弹窗提交的完整保存
    await page.route('**/api/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      const json = (body, status = 200) => route.fulfill({ status, json: body })
      if (path === '/api/me') return json({ user: users[0], cny_per_usd: '6.7' })
      if (path === '/api/admin/user-groups') return json(userGroups)
      if (path === '/api/admin/groups') return json(groups)
      if (path === '/api/admin/users') return json(users)
      const match = path.match(/\/users\/(\d+)$/)
      if (!match || route.request().method() !== 'PATCH') return json({})
      if (patchDelay) await new Promise((resolve) => setTimeout(resolve, patchDelay))
      if (failNext) return json({ error: 'status failed' }, 500)
      const id = Number(match[1])
      const body = route.request().postDataJSON()
      if (Object.keys(body).length === 1 && 'enabled' in body) togglePatches.push(body)
      else savePatches.push(body)
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
    // 编辑入口：行里没有编辑按钮，点行里的空白单元格（ID 列）打开编辑抽屉
    const open = async (id) => row(id).locator('td').first().click()
    const dialog = page.getByRole('dialog', { name: /^(编辑用户|Edit user)/ })
    const close = async () => {
      await dialog.getByRole('button', { name: /取\s*消|Cancel/ }).click()
      await dialog.waitFor({ state: 'hidden' })
    }
    // 停用确认弹窗和编辑抽屉是两回事
    const disableConfirm = () => page.getByRole('dialog').filter({ hasText: '停用账号？' })
    const editorOpen = () => page.getByRole('dialog', { name: /^(编辑用户|Edit user)/ }).count()
    const switchIn = (id) => row(id).getByRole('switch')
    await page.goto(process.env.USERS_TEST_URL || 'http://127.0.0.1:25179/console/admin/users')

    // 列表：用户分组管理不再占顶部按钮；能看出每人属于哪些用户分组、最近有没有在用
    await row(1).waitFor()
    assert.equal(await row(1).getByRole('button', { name: '编辑', exact: true }).count(), 0)
    assert.equal(await row(1).getAttribute('aria-label'), '编辑用户 · user1')
    // 默认只看启用：停用的 user3 不显示，网址不带参数；切到全部后再做列表检查
    assert.equal(await row(3).count(), 0)
    assert.equal(new URL(page.url()).searchParams.get('status'), null)
    await page.getByText('全部 4', { exact: true }).click()
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'all')
    assert.equal(await page.getByRole('button', { name: /用户分组管理/ }).count(), 0)
    assert.equal(await row(1).getByRole('combobox').count(), 0) // 角色不再在表格里直接改
    await row(1).getByText('超级管理员', { exact: true }).waitFor()
    await row(2).getByText('partner-note', { exact: true }).waitFor()
    await row(1).getByText('从未', { exact: true }).waitFor()
    assert.equal(await row(2).getByText('从未', { exact: true }).count(), 0)

    // 「用户分组」列显示用户实际所属的用户分组，和「能用哪些服务分组」不是一回事
    await page.getByRole('columnheader', { name: '用户分组', exact: true }).waitFor()
    assert.equal(await page.getByRole('columnheader', { name: '可用服务分组' }).count(), 0)
    await row(3).getByText('default', { exact: true }).waitFor()
    await row(3).getByText('friend', { exact: true }).waitFor()
    // user3 的服务分组是 open-group、vip-group，表里不该再出现服务分组或「公开分组 N 个」
    assert.equal(await row(3).getByText('open-group', { exact: true }).count(), 0)
    assert.equal(await row(3).getByText('vip-group', { exact: true }).count(), 0)
    assert.equal(await row(3).getByText(/公开分组/).count(), 0)
    // 没有用户分组的显示「无」
    await row(4).getByText('无', { exact: true }).waitFor()

    // 「状态」标签换成可操作的开关列
    await page.getByRole('columnheader', { name: '开关', exact: true }).waitFor()
    assert.equal(await page.getByRole('columnheader', { name: '状态' }).count(), 0)
    await row(2).getByRole('switch', { name: '启停用户 · user2' }).waitFor()
    assert.equal(await switchIn(2).getAttribute('aria-checked'), 'true')
    assert.equal(await switchIn(3).getAttribute('aria-checked'), 'false')
    // 不能停用当前登录的账号
    assert.equal(await switchIn(1).isDisabled(), true)
    assert.equal(await switchIn(4).isDisabled(), false)

    // 按启用状态筛选：选项带人数，选择记在网址上
    await page.getByText('仅看停用 1', { exact: true }).click()
    await waitFor(async () => (await row(1).count()) === 0)
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'disabled')
    await page.reload()
    await row(3).waitFor()
    assert.equal(await row(1).count(), 0)
    await page.getByText('仅看启用 3', { exact: true }).click()
    await waitFor(async () => (await row(3).count()) === 0)
    assert.equal(new URL(page.url()).searchParams.get('status'), null)
    await page.getByText('全部 4', { exact: true }).click()
    await row(3).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('status'), 'all')

    // 启用：不弹确认，直接只提交 enabled；顺带确认不会顺手打开编辑抽屉
    await switchIn(3).click()
    await waitFor(() => togglePatches.length === 1)
    assert.deepEqual(togglePatches[0], { enabled: true })
    assert.equal(await disableConfirm().count(), 0)
    assert.equal(await editorOpen(), 0)
    await waitFor(async () => (await switchIn(3).getAttribute('aria-checked')) === 'true')
    // 状态留在服务端：刷新后仍是启用
    await page.reload()
    await row(3).waitFor()
    assert.equal(await switchIn(3).getAttribute('aria-checked'), 'true')

    // 停用要确认：取消不动数据，开关保持原状态
    await switchIn(3).click()
    await disableConfirm().waitFor()
    await disableConfirm().getByRole('button', { name: /取\s*消/ }).click()
    await disableConfirm().waitFor({ state: 'hidden' })
    assert.equal(togglePatches.length, 1)
    assert.equal(await switchIn(3).getAttribute('aria-checked'), 'true')
    assert.equal(await editorOpen(), 0)

    // 确认停用：只提交 enabled，成功后开关变回停用
    await switchIn(3).click()
    await disableConfirm().waitFor()
    await disableConfirm().getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => togglePatches.length === 2)
    assert.deepEqual(togglePatches[1], { enabled: false })
    await waitFor(async () => (await switchIn(3).getAttribute('aria-checked')) === 'false')
    assert.equal(await editorOpen(), 0)

    // 启用失败：保留原来的状态，不会误报成已启用
    failNext = true
    await switchIn(3).click()
    await page.getByText('status failed', { exact: true }).waitFor()
    assert.equal(togglePatches.length, 2)
    assert.equal(await switchIn(3).getAttribute('aria-checked'), 'false')
    failNext = false

    // 提交期间只锁住这一行的开关，其他行照常用
    patchDelay = 1200
    await switchIn(4).click()
    await disableConfirm().waitFor()
    await disableConfirm().getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(async () => (await switchIn(4).getAttribute('class')).includes('ant-switch-loading'))
    assert.equal(await switchIn(4).isDisabled(), true)
    assert.equal(await switchIn(3).isDisabled(), false)
    await waitFor(() => togglePatches.length === 3)
    patchDelay = 0
    assert.deepEqual(togglePatches[2], { enabled: false })
    await waitFor(async () => (await switchIn(4).getAttribute('aria-checked')) === 'false')

    // 编辑：角色在抽屉里改；选了用户分组，下方即时显示能用哪些服务分组
    await open(2)
    await page.waitForTimeout(350)
    const drawer = page.locator('.ant-drawer-content-wrapper').filter({ has: dialog })
    const desktopBox = await drawer.boundingBox()
    assert.ok(desktopBox && Math.abs(desktopBox.x + desktopBox.width - page.viewportSize().width) <= 1, '用户编辑从右侧展开')
    assert.equal(Math.round(desktopBox.width), 720)
    const submitBeforeScroll = await dialog.getByRole('button', { name: /提\s*交/ }).boundingBox()
    await dialog.locator('.ant-drawer-body').evaluate((el) => { el.scrollTop = el.scrollHeight })
    const submitAfterScroll = await dialog.getByRole('button', { name: /提\s*交/ }).boundingBox()
    assert.ok(submitBeforeScroll && submitAfterScroll && Math.abs(submitAfterScroll.y - submitBeforeScroll.y) <= 1, '提交按钮固定在抽屉顶部')
    if (process.env.USERS_TEST_SCREENSHOT) await page.screenshot({ path: process.env.USERS_TEST_SCREENSHOT, fullPage: true })
    await dialog.locator('.ant-drawer-body').evaluate((el) => { el.scrollTop = 0 })
    await dialog.getByText('可用服务分组：open-group', { exact: true }).waitFor()
    await dialog.getByText('成员', { exact: true }).click()
    await dialog.locator('#user_group_ids').click()
    await page.locator('.ant-select-dropdown:visible').getByText('friend', { exact: true }).click()
    await dialog.getByText('可用服务分组：open-group、vip-group', { exact: true }).waitFor()
    await close()
    assert.equal(savePatches.length, 0)

    // 取消丢弃全部草稿
    await open(2)
    await dialog.locator('#username').fill('discarded-name')
    await dialog.locator('#amount').fill('3')
    await dialog.locator('#enabled').click()
    await close()
    assert.equal(savePatches.length, 0)
    await open(2)
    assert.equal(await dialog.locator('#username').inputValue(), 'user2')
    assert.equal(await dialog.locator('#amount').inputValue(), '')
    assert.equal(await dialog.locator('#enabled').isChecked(), true)

    // 停用需确认；失败保留草稿；成功后资料、角色、状态和额度一起提交
    await dialog.locator('#username').fill('saved-name')
    await dialog.locator('#amount').fill('3')
    await dialog.getByText('管理员', { exact: true }).click()
    await dialog.locator('#enabled').click()
    await dialog.locator('#can_create_projects').click()
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await disableConfirm().waitFor()
    await disableConfirm().getByRole('button', { name: /取\s*消/ }).click()
    await disableConfirm().waitFor({ state: 'hidden' })
    assert.equal(savePatches.length, 0)
    failNext = true
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await disableConfirm().getByRole('button', { name: /提\s*交/ }).click()
    await page.getByText('status failed', { exact: true }).waitFor()
    await disableConfirm().waitFor({ state: 'hidden' })
    assert.equal(await dialog.locator('#username').inputValue(), 'saved-name')
    assert.equal(await dialog.locator('#amount').inputValue(), '3')
    assert.equal(await dialog.locator('#enabled').isChecked(), false)
    failNext = false
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await disableConfirm().getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => savePatches.length === 1)
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(savePatches[0].username, 'saved-name')
    assert.equal(savePatches[0].role, 'admin')
    assert.equal(savePatches[0].enabled, false)
    assert.equal(savePatches[0].can_create_projects, true)
    assert.deepEqual(savePatches[0].balance_adjustment, { mode: 'add', amount: '3', currency: 'USD', note: '' })

    // 额度留空不调余额
    await open(2)
    await dialog.getByText('$8.0000', { exact: true }).waitFor()
    await dialog.locator('#enabled').click()
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => savePatches.length === 2)
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(savePatches[1].enabled, true)
    assert.equal('balance_adjustment' in savePatches[1], false)

    // 不能停用自己，也不能取消自己的管理员身份
    await open(1)
    assert.equal(await dialog.locator('#enabled').isDisabled(), true)
    await dialog.getByText('不能取消自己的管理员身份', { exact: true }).waitFor()
    await close()

    // 手机宽度：表格在内部左右滑，抽屉顶部按钮可见；英文界面
    await page.setViewportSize({ width: 390, height: 844 })
    await page.reload()
    await row(1).waitFor()
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1))
    await open(1)
    await dialog.getByRole('button', { name: /提\s*交/ }).waitFor({ state: 'visible' })
    await page.waitForTimeout(350)
    const mobileBox = await page.locator('.ant-drawer-content-wrapper').filter({ has: dialog }).boundingBox()
    assert.ok(mobileBox && mobileBox.x >= -1 && mobileBox.width <= 391, '手机抽屉不超出屏幕')
    const mobileSubmit = await dialog.getByRole('button', { name: /提\s*交/ }).boundingBox()
    assert.ok(mobileSubmit && mobileSubmit.y >= 0 && mobileSubmit.y + mobileSubmit.height <= 844)
    if (process.env.USERS_TEST_MOBILE_SCREENSHOT) await page.screenshot({ path: process.env.USERS_TEST_MOBILE_SCREENSHOT, fullPage: true })
    users[0].preferences = { language: 'en' }
    await page.reload()
    assert.equal(await row(2).getByRole('button', { name: 'Edit', exact: true }).count(), 0)
    await row(2).locator('td').first().click()
    await dialog.getByText('Account status', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: 'Submit', exact: true }).waitFor()
    await close()
    // 英文下开关的无障碍标签也跟着翻译
    await page.getByRole('switch', { name: /^Enable user · / }).first().waitFor()

    // 普通管理员：不能编辑超级管理员的账号，弹窗里不能改角色，也不能动超级管理员的开关
    users[0].role = 'admin'
    users[1].role = 'super_admin'
    await page.reload()
    await row(2).waitFor()
    // 普通管理员点超级管理员的行不打开编辑
    assert.equal(await row(2).getAttribute('aria-disabled'), 'true')
    assert.equal(await row(2).getAttribute('aria-label'), null)
    await row(2).locator('td').first().click()
    assert.equal(await dialog.isVisible(), false)
    await row(3).locator('td').first().click()
    await dialog.getByText('Only super admins can change roles', { exact: true }).waitFor()
    await close()
    // 超级管理员的账号和当前登录账号的开关都锁住，其他账号照常
    assert.equal(await switchIn(2).isDisabled(), true)
    assert.equal(await switchIn(1).isDisabled(), true)
    assert.equal(await switchIn(3).isDisabled(), false)
    assert.equal(togglePatches.length, 3)
    console.log('PASS: user groups column shows actual memberships instead of accessible service groups, status tag replaced by an operable per-user switch, disable asks for confirmation, cancel keeps the old state, enable/disable sends only enabled and survives reload, failures keep the old state, only the row being changed is locked while saving, plain admins cannot toggle super admins or themselves, and the edit dialog still guards the current account')
  } finally {
    await browser.close()
    server?.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
