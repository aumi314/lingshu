// 使用安装了 playwright 的 Node 环境运行：node web/tests/users.browser.cjs。
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const path = require('node:path')

;(async () => {
  const server = process.env.USERS_TEST_URL ? null : spawn(process.execPath, [path.join(__dirname, '../node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '5179', '--strictPort'], { cwd: path.join(__dirname, '..'), stdio: 'ignore' })
  const browser = await chromium.launch({ headless: true })
  try {
    if (server) {
      for (let i = 0; i < 100; i++) {
        try { if ((await fetch('http://127.0.0.1:5179')).ok) break } catch {}
        await new Promise((resolve) => setTimeout(resolve, 50))
      }
    }
    const page = await browser.newPage()
    const users = [1, 2, 3].map((id) => ({ id, username: `user${id}`, role: id === 1 ? 'admin' : 'user', balance: '5', enabled: true, user_group_ids: [1], preferences: {}, created_at: '2026-10-25' }))
    const pending = []
    let fail = false
    let empty = false
    let statusFail = false
    const patches = []
    await page.route('**/api/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      const json = (body, status = 200) => route.fulfill({ status, json: body })
      if (path === '/api/me') return json({ user: users[0], cny_per_usd: '6.7' })
      if (path === '/api/admin/user-groups') return json([{ id: 1, name: 'default', is_default: true }])
      if (path === '/api/admin/users') return json(users)
      const match = path.match(/\/users\/(\d+)(\/balance)?$/)
      if (!match) return json({})
      const id = Number(match[1])
      if (route.request().method() === 'PATCH') {
        if (statusFail) return json({ error: 'status failed' }, 500)
        const body = route.request().postDataJSON()
        patches.push(body)
        Object.assign(users[id - 1], body)
        if (body.balance_adjustment) users[id - 1].balance = '8'
        return json({})
      }
      if (route.request().method() === 'POST') return json({ balance: '8' })
      if (fail) return json({ error: 'history failed' }, 500)
      pending.push({ id, release: () => json(empty ? [] : [{ id, amount: String(id), balance_after: '5', operator: 'user1', note: `ledger-user${id}`, created_at: '2026-10-25' }]) })
    })
    const waitFor = async (fn) => {
      for (let i = 0; i < 300; i++) {
        if (await fn()) return
        await page.waitForTimeout(30)
      }
      throw new Error('Timed out waiting for condition')
    }
    const open = async (id) => page.getByRole('row').filter({ has: page.getByText(users[id - 1].username, { exact: true }) }).getByRole('button', { name: '编辑', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: /^(编辑用户|Edit user)/ })
    const close = async () => {
      await dialog.getByRole('button', { name: /取\s*消/ }).click()
      await dialog.waitFor({ state: 'hidden' })
    }
    const release = async (id) => {
      await waitFor(() => pending.some((p) => p.id === id))
      await pending.splice(pending.findIndex((p) => p.id === id), 1)[0].release()
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    }
    await page.goto(process.env.USERS_TEST_URL || 'http://127.0.0.1:5179/console/admin/users')
    await open(2)
    await waitFor(() => pending.length === 1)
    await close()
    await open(3)
    await release(3)
    await waitFor(() => dialog.getByText('ledger-user3').count())
    await release(2)
    assert.equal(await dialog.getByText('ledger-user2').count(), 0)
    await close()
    await open(2)
    assert.equal(await dialog.getByText('ledger-user3').count(), 0)
    await release(2)
    await dialog.locator('#username').fill('discarded-name')
    await dialog.locator('#amount').fill('3')
    await dialog.getByRole('switch').click()
    assert.equal(patches.length, 0)
    await close()
    assert.equal(patches.length, 0)
    await open(2)
    await release(2)
    assert.equal(await dialog.locator('#username').inputValue(), 'user2')
    assert.equal(await dialog.locator('#amount').inputValue(), '')
    assert.equal(await dialog.getByRole('switch').isChecked(), true)
    await dialog.locator('#username').fill('saved-name')
    await dialog.locator('#amount').fill('3')
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
    assert.equal(patches[0].enabled, false)
    assert.deepEqual(patches[0].balance_adjustment, { mode: 'add', amount: '3', currency: 'USD', note: '' })
    await open(2)
    await release(2)
    await dialog.getByText('$8.0000', { exact: true }).waitFor()
    await dialog.getByRole('switch').click()
    assert.equal(patches.length, 1)
    await dialog.getByRole('button', { name: /提\s*交/ }).click()
    await waitFor(() => patches.length === 2)
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(patches[1].enabled, true)
    assert.equal('balance_adjustment' in patches[1], false)
    fail = true
    await open(3)
    await dialog.getByText('history failed', { exact: true }).waitFor()
    fail = false
    empty = true
    await dialog.getByRole('button', { name: /重\s*试/ }).click()
    await release(3)
    await dialog.getByText('暂无流水', { exact: true }).waitFor()
    await close()
    await open(1)
    assert.equal(await dialog.getByRole('switch').isDisabled(), true)
    await release(1)
    await close()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.reload()
    await open(1)
    await release(1)
    await dialog.getByRole('button', { name: /提\s*交/ }).waitFor({ state: 'visible' })
    users[0].preferences = { language: 'en' }
    await page.reload()
    await page.getByRole('row').filter({ has: page.getByText(users[1].username, { exact: true }) }).getByRole('button', { name: 'Edit', exact: true }).click()
    await release(2)
    await dialog.getByText('No adjustments yet', { exact: true }).waitFor()
    await dialog.getByText('Account status', { exact: true }).waitFor()
    await dialog.getByRole('button', { name: 'Submit', exact: true }).waitFor()
    console.log('PASS: stale responses, reopen, unified submit, cancel discards all drafts, confirmation cancellation, failure preserves drafts, blank amount skips balance, errors/retry, empty history, self protection, mobile footer, English')
  } finally {
    await browser.close()
    server?.kill()
  }
})().catch((error) => { console.error(error); process.exitCode = 1 })
