import { useCallback, useEffect, useState } from 'react'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { del, get, patch, post, put, type BalanceAdjustment, type User, type UserGroup } from '../../api'
import { useApp } from '../../app'

export default function Users() {
  const { user: me, money, t, currency, rate, reload } = useApp()
  const { message } = App.useApp()
  const [users, setUsers] = useState<User[]>([])
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [managingGroups, setManagingGroups] = useState(false)
  const [editingUserGroup, setEditingUserGroup] = useState<UserGroup | 'new' | null>(null)
  const [balanceOf, setBalanceOf] = useState<User | null>(null)
  const [history, setHistory] = useState<BalanceAdjustment[]>([])
  const [passwordOf, setPasswordOf] = useState<User | null>(null)
  const [createForm] = Form.useForm()
  const [balanceForm] = Form.useForm<{ mode: 'add' | 'set'; amount: number; note: string }>()
  const [passwordForm] = Form.useForm<{ password: string }>()
  const [userGroupForm] = Form.useForm<{ name: string }>()

  const load = useCallback(async () => {
    try {
      const [u, g] = await Promise.all([get<User[]>('/api/admin/users'), get<UserGroup[]>('/api/admin/user-groups')])
      setUsers(u)
      setUserGroups(g)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn()
      if (ok) message.success(ok)
      await load()
      return true
    } catch (e) {
      message.error((e as Error).message)
      return false
    }
  }

  const symbol = currency === 'CNY' ? '¥' : '$'
  const toUSD = (v: number) => String(currency === 'CNY' ? v / rate : v)

  const openBalance = async (u: User) => {
    balanceForm.setFieldsValue({ mode: 'add', amount: undefined, note: '' })
    setBalanceOf(u)
    setHistory(await get<BalanceAdjustment[]>(`/api/admin/users/${u.id}/balance`).catch(() => []))
  }

  const saveBalance = async () => {
    const v = await balanceForm.validateFields()
    const done = await run(
      () => post(`/api/admin/users/${balanceOf!.id}/balance`, { mode: v.mode, amount: toUSD(v.amount), note: v.note }),
      t('余额已更新', 'Balance updated'),
    )
    if (done) {
      setBalanceOf(null)
      if (balanceOf!.id === me?.id) reload()
    }
  }

  return (
    <div className="page">
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          {t('用户', 'Users')}
        </Typography.Title>
        <Space>
          <Button onClick={() => setManagingGroups(true)}>{t('用户分组管理', 'Manage user groups')}</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { createForm.resetFields(); setCreating(true) }}>
            {t('创建用户', 'Create user')}
          </Button>
        </Space>
      </Space>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={users}
        pagination={false}
        columns={[
          { title: t('用户名', 'Username'), dataIndex: 'username' },
          {
            title: t('角色', 'Role'),
            dataIndex: 'role',
            render: (v, u) => (
              <Select
                size="small"
                value={v}
                style={{ width: 100 }}
                disabled={u.id === me?.id}
                onChange={(role) => run(() => patch(`/api/admin/users/${u.id}`, { role }))}
                options={[{ value: 'user', label: t('成员', 'Member') }, { value: 'admin', label: t('管理员', 'Admin') }]}
              />
            ),
          },
          {
            title: t('余额', 'Balance'),
            dataIndex: 'balance',
            render: (v, u) => (
              <Button type="link" size="small" style={{ padding: 0, color: Number(v) <= 0 ? '#ff4d4f' : undefined }} onClick={() => openBalance(u)}>
                {money(v)}
              </Button>
            ),
          },
          {
            title: t('用户分组', 'User groups'),
            dataIndex: 'user_group_ids',
            render: (ids: number[], u) =>
              userGroups.length ? (
                <Select
                  mode="multiple"
                  size="small"
                  style={{ minWidth: 190 }}
                  placeholder={t('选择用户分组', 'Select user groups')}
                  value={ids}
                  onChange={(user_group_ids) => run(() => put(`/api/admin/users/${u.id}/user-groups`, { user_group_ids }))}
                  options={userGroups.map((g) => ({ value: g.id, label: g.name }))}
                />
              ) : (
                <span className="muted">{t('还没有用户分组', 'No user groups')}</span>
              ),
          },
          {
            title: t('启用', 'Enabled'),
            dataIndex: 'enabled',
            render: (v, u) => <Switch size="small" checked={v} disabled={u.id === me?.id} onChange={(enabled) => run(() => patch(`/api/admin/users/${u.id}`, { enabled }))} />,
          },
          { title: t('创建时间', 'Created'), dataIndex: 'created_at', render: (v) => dayjs(v).format('YYYY-MM-DD') },
          {
            title: '',
            align: 'right',
            render: (_, u) => (
              <Space>
                <Button size="small" type="link" onClick={() => openBalance(u)}>
                  {t('调整余额', 'Adjust balance')}
                </Button>
                <Button size="small" type="link" onClick={() => { passwordForm.resetFields(); setPasswordOf(u) }}>
                  {t('重置密码', 'Reset password')}
                </Button>
              </Space>
            ),
          },
        ]}
      />

      <Modal open={managingGroups} title={t('用户分组', 'User groups')} footer={null} onCancel={() => setManagingGroups(false)} destroyOnHidden>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Text type="secondary">
            {t('用户分组只用于授权服务分组，不影响管理员或成员角色。新用户会自动加入 default。', 'User groups grant access to service groups and do not change admin or member roles. New users join default automatically.')}
          </Typography.Text>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { userGroupForm.resetFields(); setEditingUserGroup('new') }}>
            {t('新建用户分组', 'Create user group')}
          </Button>
          <Table
            rowKey="id"
            size="small"
            dataSource={userGroups}
            pagination={false}
            columns={[
              { title: t('名称', 'Name'), dataIndex: 'name' },
              { title: t('用途', 'Type'), render: (_, g) => g.is_default ? <Tag color="blue">{t('默认', 'Default')}</Tag> : null },
              {
                title: '', align: 'right', render: (_, g) => (
                  <Space>
                    <Button size="small" type="link" onClick={() => { userGroupForm.setFieldsValue({ name: g.name }); setEditingUserGroup(g) }}>{t('编辑', 'Edit')}</Button>
                    {!g.is_default && (
                      <Popconfirm title={t(`删除「${g.name}」？`, `Delete “${g.name}”?`)} description={t('成员会失去通过此分组获得的服务分组权限。', 'Members will lose service-group access granted through this group.')} onConfirm={() => run(() => del(`/api/admin/user-groups/${g.id}`))}>
                        <Button size="small" type="link" danger>{t('删除', 'Delete')}</Button>
                      </Popconfirm>
                    )}
                  </Space>
                ),
              },
            ]}
          />
        </Space>
      </Modal>

      <Modal
        open={editingUserGroup !== null}
        title={editingUserGroup === 'new' ? t('新建用户分组', 'Create user group') : t('编辑用户分组', 'Edit user group')}
        okText={t('保存', 'Save')}
        onCancel={() => setEditingUserGroup(null)}
        onOk={async () => {
          const v = await userGroupForm.validateFields()
          const done = await run(() => editingUserGroup === 'new'
            ? post('/api/admin/user-groups', v)
            : put(`/api/admin/user-groups/${editingUserGroup!.id}`, v), t('用户分组已保存', 'User group saved'))
          if (done) setEditingUserGroup(null)
        }}
        destroyOnHidden
      >
        <Form form={userGroupForm} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]}>
            <Input autoFocus />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={creating}
        title={t('创建用户', 'Create user')}
        okText={t('创建', 'Create')}
        onCancel={() => setCreating(false)}
        onOk={async () => {
          const v = await createForm.validateFields()
          if (await run(() => post('/api/admin/users', v), t('用户已创建，把用户名和密码告诉对方即可登录', 'User created. Share the username and password with them.'))) setCreating(false)
        }}
        destroyOnHidden
      >
        <Form form={createForm} layout="vertical" requiredMark={false} initialValues={{ role: 'user' }}>
          <Form.Item name="username" label={t('用户名', 'Username')} rules={[{ required: true }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item name="password" label={t('初始密码', 'Initial password')} rules={[{ required: true, min: 12, message: t('至少 12 位', 'At least 12 characters') }]} extra={t('对方登录后可以在设置里自己改', 'They can change it in Settings after signing in')}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="role" label={t('角色', 'Role')}>
            <Segmented options={[{ value: 'user', label: t('成员', 'Member') }, { value: 'admin', label: t('管理员', 'Admin') }]} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!balanceOf}
        title={`${t('调整余额', 'Adjust balance')} · ${balanceOf?.username}`}
        okText={t('确定', 'Confirm')}
        onCancel={() => setBalanceOf(null)}
        onOk={saveBalance}
        width={600}
        destroyOnHidden
      >
        <Typography.Paragraph>
          {t('当前余额', 'Current balance')}：<b>{balanceOf && money(balanceOf.balance)}</b>
        </Typography.Paragraph>
        <Form form={balanceForm} layout="vertical" requiredMark={false}>
          <Form.Item name="mode">
            <Segmented options={[{ value: 'add', label: t('加减', 'Add / subtract') }, { value: 'set', label: t('直接设为', 'Set to') }]} />
          </Form.Item>
          <Form.Item noStyle dependencies={['mode']}>
            {({ getFieldValue }) => (
              <Form.Item
                name="amount"
                label={getFieldValue('mode') === 'set' ? t('新余额', 'New balance') : t('金额（负数表示扣减）', 'Amount (negative to subtract)')}
                rules={[{ required: true, message: t('填写金额', 'Enter an amount') }]}
              >
                <InputNumber style={{ width: '100%' }} prefix={symbol} step={1} />
              </Form.Item>
            )}
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input placeholder={t('例如：10 月额度', 'e.g. October allowance')} />
          </Form.Item>
        </Form>
        {history.length > 0 && (
          <>
            <Typography.Text type="secondary">{t('最近调整', 'Recent adjustments')}</Typography.Text>
            <Table
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={history.slice(0, 10)}
              style={{ marginTop: 8 }}
              columns={[
                { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v) => dayjs(v).format('MM-DD HH:mm') },
                { title: t('变动', 'Change'), dataIndex: 'amount', render: (v) => <Tag color={Number(v) >= 0 ? 'green' : 'red'}>{Number(v) >= 0 ? '+' : ''}{money(v)}</Tag> },
                { title: t('调整后', 'After'), dataIndex: 'balance_after', render: (v) => money(v) },
                { title: t('操作人', 'By'), dataIndex: 'operator' },
                { title: t('备注', 'Note'), dataIndex: 'note' },
              ]}
            />
          </>
        )}
      </Modal>

      <Modal
        open={!!passwordOf}
        title={`${t('重置密码', 'Reset password')} · ${passwordOf?.username}`}
        okText={t('重置', 'Reset')}
        onCancel={() => setPasswordOf(null)}
        onOk={async () => {
          const v = await passwordForm.validateFields()
          if (await run(() => patch(`/api/admin/users/${passwordOf!.id}`, { password: v.password }), t('密码已重置，对方需要用新密码重新登录', 'Password reset. They must sign in again.'))) setPasswordOf(null)
        }}
        destroyOnHidden
      >
        <Form form={passwordForm} layout="vertical" requiredMark={false}>
          <Form.Item name="password" label={t('新密码', 'New password')} rules={[{ required: true, min: 12, message: t('至少 12 位', 'At least 12 characters') }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
