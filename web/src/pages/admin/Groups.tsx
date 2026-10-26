import { useCallback, useEffect, useState } from 'react'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, get, post, put, type Group, type UserGroup } from '../../api'
import { useApp } from '../../app'

// 放在「定价」页的标签里（pages/admin/Pricing.tsx），不单独成页
export default function Groups() {
  const { t } = useApp()
  const { message } = App.useApp()
  const [groups, setGroups] = useState<Group[]>([])
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Group | 'new' | null>(null)
  const [form] = Form.useForm()

  const load = useCallback(async () => {
    try {
      const [g, u] = await Promise.all([get<Group[]>('/api/admin/groups'), get<UserGroup[]>('/api/admin/user-groups')])
      setGroups(g)
      setUserGroups(u)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const save = async () => {
    const v = await form.validateFields()
    const body = { ...v, ratio: String(v.ratio), user_group_ids: v.is_public ? [] : v.user_group_ids }
    try {
      if (editing === 'new') await post('/api/admin/groups', body)
      else if (editing) await put(`/api/admin/groups/${editing.id}`, body)
      setEditing(null)
      message.success(t('已保存', 'Saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const remove = async (g: Group) => {
    try {
      await del(`/api/admin/groups/${g.id}`)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16, marginBottom: 12 }}>
        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          {t(
            '服务分组决定倍率和使用哪些渠道。适用范围可以设为所有用户，或指定一个或多个用户分组；新用户默认加入 default。',
            'Service groups set the price ratio and available channels. Limit access to all users or selected user groups. New users join default automatically.',
          )}
        </Typography.Paragraph>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            form.setFieldsValue({ name: '', ratio: 1, enabled: true, is_public: true, user_group_ids: [], description: '' })
            setEditing('new')
          }}
        >
          {t('创建服务分组', 'Create service group')}
        </Button>
      </div>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={groups}
        pagination={false}
        columns={[
          { title: '#', dataIndex: 'id', width: 48, render: (v) => <span className="muted">{v}</span> },
          { title: t('名称', 'Name'), dataIndex: 'name' },
          { title: t('倍率', 'Ratio'), dataIndex: 'ratio', render: (v) => `×${Number(v)}` },
          { title: t('适用范围', 'Availability'), dataIndex: 'is_public', render: (v, g) => v
            ? <Tag color="green">{t('所有用户', 'All users')}</Tag>
            : <Space wrap><Tag color="gold">{t('指定用户分组', 'Selected user groups')}</Tag>{g.user_group_ids.map((id) => <Tag key={id}>{userGroups.find((u) => u.id === id)?.name ?? id}</Tag>)}</Space> },
          { title: t('启用', 'Enabled'), dataIndex: 'enabled', render: (v, g) => <Switch checked={v} onChange={async (enabled) => {
            try {
              await put(`/api/admin/groups/${g.id}`, { ...g, enabled, ratio: String(g.ratio) })
              message.success(t('状态已更新', 'Status updated'))
              load()
            } catch (e) { message.error((e as Error).message) }
          }} /> },
          { title: t('说明', 'Description'), dataIndex: 'description' },
          {
            title: '',
            align: 'right',
            render: (_, g) => (
              <Space>
                <Button size="small" type="link" onClick={() => { form.setFieldsValue({ ...g, ratio: Number(g.ratio) }); setEditing(g) }}>
                  {t('编辑', 'Edit')}
                </Button>
                <Popconfirm title={t('确定删除这个服务分组？', 'Delete this service group?')} onConfirm={() => remove(g)}>
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal open={editing !== null} title={editing === 'new' ? t('创建服务分组', 'Create service group') : t('编辑服务分组', 'Edit service group')} onOk={save} onCancel={() => setEditing(null)} okText={t('保存', 'Save')} destroyOnHidden>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="ratio" label={t('倍率', 'Ratio')} rules={[{ required: true }]} extra={t('1 表示按标价收费，0.5 表示半价', '1 = list price, 0.5 = half price')}>
            <InputNumber min={0} step={0.1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="is_public" label={t('适用范围', 'Availability')} valuePropName="checked" extra={t('所有用户指全部已启用的灵枢用户，不代表免登录或对互联网开放。', 'All users means every enabled Lingshu user; login is still required.') }>
            <Switch checkedChildren={t('所有用户', 'All users')} unCheckedChildren={t('指定用户分组', 'Selected groups')} />
          </Form.Item>
          <Form.Item noStyle dependencies={['is_public']}>
            {({ getFieldValue }) => getFieldValue('is_public') ? null : (
              <Form.Item name="user_group_ids" label={t('允许的用户分组', 'Allowed user groups')} rules={[{ required: true, type: 'array', min: 1, message: t('至少选择一个用户分组', 'Select at least one user group') }]}>
                <Select mode="multiple" options={userGroups.map((g) => ({ value: g.id, label: g.name }))} />
              </Form.Item>
            )}
          </Form.Item>
          <Form.Item name="description" label={t('说明', 'Description')}>
            <Input placeholder={t('例如：走官方渠道，稳定', 'e.g. official channels, stable')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
