import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, App, Button, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { del, get, patch, post, put, type BalanceAdjustment, type User, type UserGroup } from '../../api'
import { useApp } from '../../app'

export default function Users() {
  const { user: me, money, t, currency, reload } = useApp()
  const { message, modal } = App.useApp()
  const [users, setUsers] = useState<User[]>([])
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [managingGroups, setManagingGroups] = useState(false)
  const [editingUserGroup, setEditingUserGroup] = useState<UserGroup | 'new' | null>(null)
  const [editingUser, setEditingUser] = useState<User | null>(null)
  const [history, setHistory] = useState<BalanceAdjustment[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyError, setHistoryError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const editSession = useRef(0)
  const historyRequest = useRef(0)
  const [createForm] = Form.useForm()
  const [editForm] = Form.useForm<{ username: string; password?: string; user_group_ids: number[]; enabled: boolean }>()
  const [balanceForm] = Form.useForm<{ mode: 'add' | 'set'; amount?: string; note: string }>()
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

  const loadHistory = async (id: number) => {
    const request = ++historyRequest.current
    const session = editSession.current
    setHistory([])
    setHistoryError('')
    setHistoryLoading(true)
    try {
      const rows = await get<BalanceAdjustment[]>(`/api/admin/users/${id}/balance`)
      if (session === editSession.current && request === historyRequest.current) setHistory(rows)
    } catch (e) {
      if (session === editSession.current && request === historyRequest.current) setHistoryError((e as Error).message)
    } finally {
      if (session === editSession.current && request === historyRequest.current) setHistoryLoading(false)
    }
  }

  const closeEdit = () => {
    if (submitting) return
    ++editSession.current
    setEditingUser(null)
  }

  const openEdit = (u: User) => {
    ++editSession.current
    setSubmitting(false)
    editForm.setFieldsValue({ username: u.username, password: '', user_group_ids: u.user_group_ids ?? [], enabled: u.enabled })
    balanceForm.setFieldsValue({ mode: 'add', amount: undefined, note: '' })
    setEditingUser(u)
    void loadHistory(u.id)
  }

  const saveUser = async () => {
    if (!editingUser || submitting) return
    const session = editSession.current
    let v
    let balance
    try {
      ;[v, balance] = await Promise.all([editForm.validateFields(), balanceForm.validateFields()])
    } catch {
      return
    }
    if (session !== editSession.current) return
    setSubmitting(true)
    try {
      if (editingUser.enabled && !v.enabled) {
        const confirmed = await modal.confirm({
          title: t('停用账号？', 'Disable account?'),
          content: t('停用后，该用户将退出登录，所有 API key 将无法继续调用。余额和历史记录保留。', 'The user will be signed out and all their API keys will stop working. Their balance and history will be retained.'),
          okText: t('提交', 'Submit'),
          cancelText: t('取消', 'Cancel'),
        })
        if (!confirmed) return
      }
      await patch(`/api/admin/users/${editingUser.id}`, {
        username: v.username,
        password: v.password ?? '',
        user_group_ids: v.user_group_ids ?? [],
        enabled: v.enabled,
        ...(balance.amount != null && balance.amount !== '' ? {
          balance_adjustment: { mode: balance.mode, amount: balance.amount, currency, note: balance.note ?? '' },
        } : {}),
      })
      message.success(t('用户已更新', 'User updated'))
      ++editSession.current
      setEditingUser(null)
      await load()
      if (editingUser.id === me?.id) void reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSubmitting(false)
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
            render: (v) => <span style={{ color: Number(v) <= 0 ? '#ff4d4f' : undefined }}>{money(v)}</span>,
          },
          {
            title: t('状态', 'Status'),
            dataIndex: 'enabled',
            render: (v) => <Tag color={v ? 'green' : undefined}>{v ? t('已启用', 'Enabled') : t('已停用', 'Disabled')}</Tag>,
          },
          { title: t('创建时间', 'Created'), dataIndex: 'created_at', render: (v) => dayjs(v).format('YYYY-MM-DD') },
          {
            title: '',
            align: 'right',
            render: (_, u) => (
              <Button size="small" type="link" onClick={() => openEdit(u)}>{t('编辑', 'Edit')}</Button>
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
        open={!!editingUser}
        title={`${t('编辑用户', 'Edit user')} · ${editingUser?.username}`}
        okText={t('提交', 'Submit')}
        cancelText={t('取消', 'Cancel')}
        onCancel={closeEdit}
        onOk={saveUser}
        confirmLoading={submitting}
        cancelButtonProps={{ disabled: submitting }}
        closable={!submitting}
        keyboard={!submitting}
        maskClosable={!submitting}
        width={720}
        destroyOnHidden
      >
        <Form form={editForm} layout="vertical" requiredMark={false} disabled={submitting}>
          <Form.Item name="username" label={t('用户名', 'Username')} rules={[{ required: true, whitespace: true, message: t('用户名不能为空', 'Username is required') }]}>
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            name="password"
            label={t('密码', 'Password')}
            extra={t('留空不修改；填入新密码则重置，至少 12 位。', 'Leave blank to keep the current password. Enter a new password (at least 12 characters) to reset it.')}
            rules={[{ validator: (_, value) => !value || value.length >= 12 ? Promise.resolve() : Promise.reject(new Error(t('新密码至少 12 位', 'New password must be at least 12 characters'))) }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="user_group_ids" label={t('用户分组', 'User groups')}>
            <Select mode="multiple" options={userGroups.map((g) => ({ value: g.id, label: g.name }))} placeholder={t('选择用户分组', 'Select user groups')} />
          </Form.Item>
          <Form.Item name="enabled" label={t('账号状态', 'Account status')} valuePropName="checked" extra={editingUser?.id === me?.id ? t('不能停用当前登录账号', 'You cannot disable your own account') : undefined}>
            <Switch checkedChildren={t('启用', 'Enabled')} unCheckedChildren={t('停用', 'Disabled')} disabled={submitting || editingUser?.id === me?.id} />
          </Form.Item>
        </Form>

        <Typography.Title level={5} style={{ marginTop: 24 }}>{t('额度调整', 'Quota adjustment')}</Typography.Title>
        <Typography.Paragraph>
          {t('当前余额', 'Current balance')}：<b>{editingUser && money(editingUser.balance)}</b>
        </Typography.Paragraph>
        <Form form={balanceForm} layout="vertical" requiredMark={false} disabled={submitting}>
          <Form.Item name="mode" style={{ marginBottom: 12 }}>
            <Segmented options={[{ value: 'add', label: t('加减', 'Add / subtract') }, { value: 'set', label: t('设为余额', 'Set balance') }]} />
          </Form.Item>
          <Form.Item noStyle dependencies={['mode']}>
            {({ getFieldValue }) => (
              <Form.Item
                name="amount"
                label={getFieldValue('mode') === 'set' ? t('调整后的余额', 'New balance') : t('变动金额（负数为扣减）', 'Change (negative to subtract)')}
                extra={t('留空不调整额度，填写后随资料一起提交。', 'Leave blank to keep the balance. Changes are saved with the user details.')}
              >
                <InputNumber stringMode style={{ width: '100%' }} prefix={symbol} step="1" />
              </Form.Item>
            )}
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input placeholder={t('例如：10 月额度', 'e.g. October allowance')} />
          </Form.Item>
        </Form>
        {historyError ? (
          <Alert style={{ marginTop: 16 }} type="error" title={historyError} action={<Button size="small" onClick={() => editingUser && loadHistory(editingUser.id)}>{t('重试', 'Retry')}</Button>} />
        ) : (
          <>
            <Typography.Text type="secondary">{t('最近调整', 'Recent adjustments')}</Typography.Text>
            <Table
              size="small"
              rowKey="id"
              pagination={false}
              dataSource={history.slice(0, 10)}
              loading={historyLoading}
              locale={{ emptyText: historyLoading ? t('加载中…', 'Loading…') : t('暂无流水', 'No adjustments yet') }}
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
    </div>
  )
}
