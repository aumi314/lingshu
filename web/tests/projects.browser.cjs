// 项目额度页面回归：组件开着才出现「我的项目」；邀请码申请；创建人在项目里建 key（带 project_id）、转入额度、审批成员、保存规则；
// 待审批的项目只显示等待提示；组件关闭时给出提示。使用安装了 playwright 的 Node 环境运行。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
// 端口用 2 万段：本机 Windows 动态端口范围是 1024–15000，镜像网络下 51xx 会被随机占用
const path = require('node:path')

;(async () => {
  const server = spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '25199', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch('http://127.0.0.1:25199')).ok) break } catch {}
      await new Promise((resolve) => setTimeout(resolve, 50))
    }
    const user = { id: 2, username: 'bob', role: 'user', balance: '5', preferences: { language: 'zh', theme: 'dark' }, enabled: true, user_group_ids: [], created_at: new Date().toISOString(), can_create_projects: true }
    const group = { id: 1, name: 'default', ratio: '1', is_collection: false }
    const owned = { id: 1, name: '研发组', invite_code: 'ABCD2345', owner_id: 2, owner_name: 'bob', balance: '3', reserved: '0.5', total_spent: '9', my_limit: '10', my_period: 'week', my_spent: '2.5', frozen: false, closed: false, group_ids: [1], groups: [group], members: 2, my_status: 'owner', created_at: new Date().toISOString() }
    const pending = { ...owned, id: 2, name: '产品组', invite_code: undefined, owner_id: 9, owner_name: 'carol', my_status: 'pending', my_limit: null }
    const state = { enabled: true, writes: [] }
    const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
    const errors = []
    page.on('pageerror', (e) => errors.push(e.message))
    await page.route('**/api/**', (route) => {
      const req = route.request()
      const p = new URL(req.url()).pathname
      const json = (b, status = 200) => route.fulfill({ json: b, status })
      if (req.method() !== 'GET') state.writes.push([req.method(), p, req.postDataJSON?.() ?? null])
      if (p === '/api/me') return json({ user, cny_per_usd: '6.7' })
      if (p === '/api/me/projects' && req.method() === 'GET') return json({ enabled: state.enabled, can_create: true, projects: state.enabled ? [owned, pending] : [] })
      if (p === '/api/me/projects/join') return json({ name: '产品组' })
      if (p === '/api/me/projects/1' && req.method() === 'GET') return json({ project: owned, members: [
        { user_id: 2, username: 'bob', status: 'owner', period_limit: null, limit_period: 'week', period_spent: '2', total_spent: '9', created_at: '' },
        { user_id: 3, username: 'carol', status: 'pending', period_limit: null, limit_period: 'week', period_spent: '0', total_spent: '0', created_at: '' },
      ] })
      if (p === '/api/me/keys' && req.method() === 'GET') return json([{ id: 7, name: 'laptop', prefix: 'sk-ls-abc', group_id: 1, group_name: 'default', quota_limit: null, quota_used: '1.2', enabled: true, last_used_at: null, expires_at: null, created_at: '', project_id: 1, project_name: '研发组' }])
      if (p === '/api/me/keys') return json({ id: 8, key: 'sk-ls-newprojectkey' }, 201)
      if (p === '/api/me/models') return json({ groups: [{ ...group, enabled: true, is_public: true, user_group_ids: [], description: '' }], models: [] })
      return json({})
    })

    // 侧边栏入口和卡片：池子可用 = 余额 − 冻结，我本周已花 / 我的上限
    await page.goto('http://127.0.0.1:25199/console/projects')
    await page.getByRole('menuitem', { name: '我的项目' }).waitFor()
    await page.getByText('研发组', { exact: true }).first().waitFor()
    await page.getByText('$2.5000', { exact: true }).first().waitFor()
    await page.getByText('我本周已花 $2.5000 / $10.0000', { exact: true }).waitFor()
    await page.getByText('已申请，等待 carol 审批', { exact: true }).waitFor()

    // 邀请码申请
    await page.getByPlaceholder('输入邀请码').fill('wxyz6789')
    await page.getByRole('button', { name: '申请加入' }).click()
    await page.getByText(/已申请加入「产品组」/).waitFor()
    assert.deepEqual(state.writes.at(-1), ['POST', '/api/me/projects/join', { code: 'wxyz6789' }])

    // 选中的项目直接在卡片下方展开（不是弹窗）：默认第一个已加入的；点待审批的不切换
    const dialog = page.locator('.ant-card').filter({ has: page.getByRole('tab', { name: '我的 key' }) })
    await dialog.getByText('负责人 bob', { exact: true }).waitFor()
    assert.equal(await page.getByRole('dialog').count(), 0)
    await page.getByText('产品组', { exact: true }).click()
    await dialog.getByText('负责人 bob', { exact: true }).waitFor()
    assert.equal(new URL(page.url()).searchParams.get('project'), null)
    await page.getByText('研发组', { exact: true }).first().click()
    await page.waitForFunction(() => new URL(location.href).searchParams.get('project') === '1')

    // 建 key 带 project_id，明文只显示一次
    await dialog.getByText('laptop', { exact: true }).waitFor()
    await dialog.getByPlaceholder('key 名称，例如：笔记本').fill('server')
    await dialog.locator('.ant-select').first().click()
    await page.locator('.ant-select-item-option').filter({ hasText: 'default' }).click()
    await dialog.getByRole('button', { name: /创建 key/ }).click()
    await page.getByText('sk-ls-newprojectkey', { exact: true }).waitFor()
    assert.deepEqual(state.writes.at(-1), ['POST', '/api/me/keys', { name: 'server', group_id: 1, project_id: 1 }])
    await page.getByRole('dialog').filter({ hasText: 'API key 已创建' }).getByRole('button', { name: /确\s*定|OK/ }).click()

    // 额度池：转入
    await dialog.getByRole('tab', { name: '额度池' }).click()
    await dialog.getByPlaceholder('金额').fill('2')
    await dialog.getByRole('button', { name: /确\s*定/ }).click()
    for (let i = 0; i < 50 && state.writes.at(-1)[1] !== '/api/me/projects/1/transfer'; i++) await page.waitForTimeout(30)
    assert.deepEqual(state.writes.at(-1), ['POST', '/api/me/projects/1/transfer', { direction: 'in', amount: '2' }])

    // 成员：标签上带待审批数，通过申请
    await dialog.getByRole('tab', { name: '成员 · 1' }).click()
    await dialog.getByRole('button', { name: /通\s*过/ }).click()
    for (let i = 0; i < 50 && !state.writes.at(-1)[1].endsWith('/approve'); i++) await page.waitForTimeout(30)
    assert.deepEqual(state.writes.at(-1), ['POST', '/api/me/projects/1/members/3/approve', {}])

    // 成员额度：创建人给每个人单独设，按月 50
    state.writes.length = 0
    await dialog.getByText('本周 $2.0000 · 不限', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: /设\s*置/ }).first().click()
    const limitDialog = page.getByRole('dialog').filter({ hasText: 'bob 的额度上限' })
    await limitDialog.locator('.ant-segmented-item').filter({ hasText: '按月' }).click()
    await limitDialog.locator('#period_limit').fill('50')
    await limitDialog.getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 50 && !state.writes.some((w) => w[0] === 'PUT'); i++) await page.waitForTimeout(30)
    assert.deepEqual(state.writes.find((w) => w[0] === 'PUT'), ['PUT', '/api/me/projects/1/members/2/limit', { limit_period: 'month', period_limit: '50' }])

    // 规则：只剩名称和开放分组，没有项目级额度上限
    state.writes.length = 0
    await dialog.getByRole('tab', { name: '规则' }).click()
    await dialog.getByText('ABCD2345', { exact: true }).waitFor()
    assert.equal(await page.locator('#period_limit').count(), 0)
    await dialog.getByRole('button', { name: /保\s*存/ }).click()
    for (let i = 0; i < 50 && !state.writes.some((w) => w[0] === 'PATCH'); i++) await page.waitForTimeout(30)
    assert.deepEqual(state.writes.find((w) => w[0] === 'PATCH'), ['PATCH', '/api/me/projects/1', { name: '研发组', group_ids: [1] }])

    // 组件关闭：没有入口，页面提示
    state.enabled = false
    await page.goto('http://127.0.0.1:25199/console/projects')
    await page.getByText('项目功能没有开启，请联系管理员', { exact: true }).waitFor()
    assert.equal(await page.getByRole('menuitem', { name: '我的项目' }).count(), 0)
    assert.deepEqual(errors, [])
    console.log('PASS: projects menu only when enabled, selected project expands below the card strip (no dialog, kept in URL), pool and weekly cards, join by code, pending not openable, project key with project_id shown once, transfer, approve with pending count, owner sets a monthly cap per member, rules without project cap, disabled notice')
  } finally {
    await browser.close()
    server.kill()
  }
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
