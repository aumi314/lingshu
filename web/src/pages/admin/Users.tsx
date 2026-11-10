import { useCallback, useEffect, useRef, useState } from 'react'
import { App, Button, Drawer, Form, Input, InputNumber, Modal, Segmented, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { useSearchParams } from 'react-router-dom'
import { get, patch, post, type Group, type User, type UserGroup } from '../../api'
import { useApp } from '../../app'
import { rowEditing } from '../../rowEditing'

export default function Users() {
  const { user: me, money, t, currency, reload } = useApp()
  const { message, modal } = App.useApp()
  const [users, setUsers] = useState<User[]>([])
  const [params, setParams] = useSearchParams()
  // 默认只看启用的账号；全部、仅看停用记在网址上
  const status = params.get('status') === 'all' || params.get('status') === 'disabled' ? params.get('status')! : 'enabled'
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [editingUser, setEditingUser] = useState<User | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [toggling, setToggling] = useState<number[]>([])
  const editSession = useRef(0)
  const [createForm] = Form.useForm()
  const [editForm] = Form.useForm<{ username: string; password?: string; note?: string; role: User['role']; user_group_ids: number[]; enabled: boolean }>()
  const [balanceForm] = Form.useForm<{ mode: 'add' | 'set'; amount?: string; note: string }>()
  const editingGroupIDs = Form.useWatch('user_group_ids', editForm)

  const load = useCallback(async () => {
    try {
      const [u, g, sg] = await Promise.all([get<User[]>('/api/admin/users'), get<UserGroup[]>('/api/admin/user-groups'), get<Group[]>('/api/admin/groups')])
      setUsers(u)
      setUserGroups(g)
      setGroups(sg)
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
  // 超级管理员只多一项任免管理员；超级管理员的账号只有超级管理员能改（服务端同样校验）
  const isSuper = me?.role === 'super_admin'
  const roleOptions = [{ value: 'user', label: t('成员', 'Member') }, { value: 'admin', label: t('管理员', 'Admin') }, { value: 'super_admin', label: t('超级管理员', 'Super admin') }]

  // 用户能用的服务分组：启用中，且对所有用户开放或开放给他所在的某个用户分组
  const accessible = (userGroupIDs: number[]) => groups.filter((g) => g.enabled && (g.is_public || g.user_group_ids.some((id) => userGroupIDs.includes(id))))
  const groupNames = (ids: number[]) => ids.map((id) => userGroups.find((g) => g.id === id)?.name ?? id).join('、')
  const toggle = async (u: User, enabled: boolean) => {
    if (u.id === me?.id || (u.role === 'super_admin' && !isSuper) || toggling.includes(u.id)) return
    setToggling((ids) => [...ids, u.id])
    try {
      if (!enabled && !await modal.confirm({
        title: t('停用账号？', 'Disable account?'),
        content: t('停用后，该用户将退出登录，所有 API key 将无法继续调用。余额和历史记录保留。', 'The user will be signed out and all their API keys will stop working. Their balance and history will be retained.'),
        okText: t('提交', 'Submit'),
        cancelText: t('取消', 'Cancel'),
      })) return
      await run(() => patch(`/api/admin/users/${u.id}`, { enabled }), t('用户已更新', 'User updated'))
    } finally {
      setToggling((ids) => ids.filter((id) => id !== u.id))
    }
  }

  const closeEdit = () => {
    if (submitting) return
    ++editSession.current
    setEditingUser(null)
  }

  const openEdit = (u: User) => {
    if (u.role === 'super_admin' && !isSuper) return
    ++editSession.current
    setSubmitting(false)
    editForm.setFieldsValue({ username: u.username, password: '', note: u.note ?? '', role: u.role, user_group_ids: u.user_group_ids ?? [], enabled: u.enabled })
    balanceForm.setFieldsValue({ mode: 'add', amount: undefined, note: '' })
    setEditingUser(u)
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
        note: v.note ?? '',
        password: v.password ?? '',
        role: v.role,
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
        <Space wrap>
          <Segmented
            value={status}
            onChange={(v) => setParams(v === 'enabled' ? {} : { status: v as string }, { replace: true })}
            options={[
              { value: 'all', label: `${t('全部', 'All')} ${users.length}` },
              { value: 'enabled', label: `${t('仅看启用', 'Enabled')} ${users.filter((u) => u.enabled).length}` },
              { value: 'disabled', label: `${t('仅看停用', 'Disabled')} ${users.filter((u) => !u.enabled).length}` },
            ]}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { createForm.resetFields(); setCreating(true) }}>
            {t('创建用户', 'Create user')}
          </Button>
        </Space>
      </Space>
      <Table
        rowKey="id"
        loading={loading}
        onRow={(u) => u.role === 'super_admin' && !isSuper
          ? { 'aria-disabled': true, title: t('只有超级管理员能修改超级管理员的账号', 'Only super admins can edit a super admin') }
          : rowEditing(() => openEdit(u), `${t('编辑用户', 'Edit user')} · ${u.username}`)}
        dataSource={status === 'all' ? users : users.filter((u) => u.enabled === (status === 'enabled'))}
        locale={{ emptyText: status === 'disabled' ? t('没有停用的用户', 'No disabled users') : status === 'enabled' ? t('没有启用的用户', 'No enabled users') : undefined }}
        pagination={false}
        scroll={{ x: 'max-content' }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 48, render: (v) => <span className="muted">{v}</span> },
          {
            title: t('用户', 'User'),
            dataIndex: 'username',
            render: (v, u) => (
              <div style={{ maxWidth: 240 }}>
                <div>{v}</div>
                {u.note && <Typography.Text type="secondary" ellipsis={{ tooltip: u.note }} style={{ display: 'block', fontSize: 12 }}>{u.note}</Typography.Text>}
              </div>
            ),
          },
          { title: t('角色', 'Role'), dataIndex: 'role', render: (v) => v === 'super_admin' ? <Tag color="purple">{t('超级管理员', 'Super admin')}</Tag> : v === 'admin' ? <Tag color="blue">{t('管理员', 'Admin')}</Tag> : <span className="muted">{t('成员', 'Member')}</span> },
          { title: t('用户分组', 'User groups'), render: (_, u) => u.user_group_ids?.length ? <Space size={4} wrap>{u.user_group_ids.map((id) => <Tag key={id}>{groupNames([id])}</Tag>)}</Space> : <span className="muted">{t('无', 'None')}</span> },
          {
            title: t('余额', 'Balance'),
            dataIndex: 'balance',
            render: (v) => <span style={{ color: Number(v) <= 0 ? '#ff4d4f' : undefined }}>{money(v)}</span>,
          },
          { title: t('最近调用', 'Last call'), dataIndex: 'last_used_at', render: (v?: string) => v ? <span className="nowrap">{dayjs(v).format('MM-DD HH:mm')}</span> : <span className="muted">{t('从未', 'Never')}</span> },
          {
            title: t('开关', 'Enabled'),
            dataIndex: 'enabled',
            render: (v, u) => <Switch size="small" aria-label={`${t('启停用户', 'Enable user')} · ${u.username}`} checked={v} loading={toggling.includes(u.id)} disabled={u.id === me?.id || (u.role === 'super_admin' && !isSuper)} onChange={(enabled) => void toggle(u, enabled)} />,
          },
        ]}
      />

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
          {isSuper && (
            <Form.Item name="role" label={t('角色', 'Role')}>
              <Segmented options={roleOptions} />
            </Form.Item>
          )}
        </Form>
      </Modal>

      <Drawer
        open={!!editingUser}
        title={`${t('编辑用户', 'Edit user')} · ${editingUser?.username}`}
        onClose={closeEdit}
        extra={
          <Space>
            <Button disabled={submitting} onClick={closeEdit}>{t('取消', 'Cancel')}</Button>
            <Button type="primary" loading={submitting} onClick={saveUser}>{t('提交', 'Submit')}</Button>
          </Space>
        }
        closable={!submitting}
        keyboard={!submitting}
        maskClosable={!submitting}
        size={720}
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
          <Form.Item name="note" label={t('用户备注', 'User note')}>
            <Input.TextArea
              rows={2}
              maxLength={500}
              showCount
              placeholder={t('例如：外部合作方，额度按月给', 'e.g. External partner, monthly allowance')}
            />
          </Form.Item>
          <Form.Item name="role" label={t('角色', 'Role')} extra={!isSuper ? t('只有超级管理员能任免管理员', 'Only super admins can change roles') : editingUser?.id === me?.id ? t('不能取消自己的管理员身份', 'You cannot remove your own admin role') : t('管理员和超级管理员都能进入后台；超级管理员另外能任免管理员。能用哪些模型由下面的用户分组决定', 'Admins and super admins can manage the console; only super admins can change roles. Model access is decided by user groups below')}>
            <Segmented disabled={submitting || !isSuper || editingUser?.id === me?.id} options={roleOptions} />
          </Form.Item>
          <Form.Item name="user_group_ids" label={t('用户分组', 'User groups')} extra={<span>{t('可用服务分组：', 'Service groups: ')}{accessible(editingGroupIDs ?? []).map((g) => g.name).join('、') || t('无', 'None')}</span>}>
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
          <Form.Item name="note" label={t('额度备注', 'Adjustment note')}>
            <Input placeholder={t('例如：10 月额度', 'e.g. October allowance')} />
          </Form.Item>
        </Form>
      </Drawer>
    </div>
  )
}
