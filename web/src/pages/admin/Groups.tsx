import { useCallback, useEffect, useState } from 'react'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, get, post, put, type Group, type UserGroup } from '../../api'
import { useApp } from '../../app'
import { rowEditing } from '../../rowEditing'
import UserGroupManager from '../../components/UserGroupManager'
import { DragHandle, SortableBody, SortableRow } from '../../components/SortableRows'
import { arrayMove } from '@dnd-kit/sortable'

// 放在「定价」页的标签里（pages/admin/Pricing.tsx），不单独成页
export default function Groups() {
  const { t } = useApp()
  const { message } = App.useApp()
  const [groups, setGroups] = useState<Group[]>([])
  const [userGroups, setUserGroups] = useState<UserGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Group | 'new' | 'new-collection' | null>(null)
  const [managingUserGroups, setManagingUserGroups] = useState(false)
  const [form] = Form.useForm()

  const isCollection = editing === 'new-collection' || (typeof editing === 'object' && editing?.is_collection === true)
  const nameOf = (id: number) => groups.find((g) => g.id === id)?.name ?? id
  const create = (collection: boolean) => {
    form.setFieldsValue({ name: '', ratio: 1, enabled: true, is_public: true, user_group_ids: [], description: '', retry_count: 2, member_ids: [] })
    setEditing(collection ? 'new-collection' : 'new')
  }

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
    const body = { ...v, ratio: String(v.ratio ?? 1), user_group_ids: v.is_public ? [] : v.user_group_ids, is_collection: isCollection }
    try {
      if (editing === 'new' || editing === 'new-collection') await post('/api/admin/groups', body)
      else if (editing) await put(`/api/admin/groups/${editing.id}`, body)
      setEditing(null)
      message.success(t('已保存', 'Saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  // 拖拽排序：先在页面上换位置，再保存；失败就重新加载
  const move = async (from: number, to: number) => {
    const next = arrayMove(groups, from, to)
    setGroups(next)
    try {
      await put('/api/admin/groups/order', { ids: next.map((g) => g.id) })
    } catch (e) {
      message.error((e as Error).message)
      load()
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
            '服务分组决定倍率和使用哪些渠道。适用范围设为所有用户，或只开放给指定的用户分组（名单在「用户分组」里维护，成员在用户编辑里调整）。集合把几个分组合成一个，一把 key 就能用它们的全部模型，每个模型按所在分组的渠道和倍率。',
            'Service groups set the price ratio and available channels. Open one to all users, or only to selected user groups (maintain the lists under User groups; change membership when editing a user). A collection combines several groups so one key reaches all their models, each billed at its own group\'s ratio.',
          )}
        </Typography.Paragraph>
        <Space>
          <Button onClick={() => setManagingUserGroups(true)}>{t('用户分组', 'User groups')}</Button>
          <Button onClick={() => create(true)}>{t('创建集合', 'Create collection')}</Button>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => create(false)}>
            {t('创建服务分组', 'Create service group')}
          </Button>
        </Space>
      </div>
      <SortableBody ids={groups.map((g) => g.id)} onMove={move}>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={groups}
        onRow={(g) => rowEditing(() => { form.setFieldsValue({ ...g, ratio: Number(g.ratio) }); setEditing(g) }, `${g.is_collection ? t('编辑集合', 'Edit collection') : t('编辑服务分组', 'Edit service group')} · ${g.name}`)}
        pagination={false}
        components={{ body: { row: SortableRow } }}
        columns={[
          { key: 'sort', width: 40, render: () => <DragHandle title={t('拖动排序（成员看到的分组顺序也随之改变）', 'Drag to reorder (members see the same order)')} /> },
          { title: 'ID', dataIndex: 'id', width: 48, render: (v) => <span className="muted">{v}</span> },
          {
            title: t('名称', 'Name'),
            dataIndex: 'name',
            render: (v, g) => g.is_collection ? (
              <div>
                <Space size={6}>{v}<Tag color="blue">{t('集合', 'Collection')}</Tag></Space>
                <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>
                  {g.member_ids?.length ? `${t('包含', 'Includes')}：${g.member_ids.map(nameOf).join('、')}` : t('还没有包含分组', 'No groups yet')}
                </div>
              </div>
            ) : v,
          },
          { title: t('倍率', 'Ratio'), dataIndex: 'ratio', render: (v, g) => g.is_collection ? <span className="muted">{t('按成员分组', 'Per member')}</span> : `×${Number(v)}` },
          {
            title: <Tooltip title={t('渠道出故障（5xx、超时、断流）后最多换几次渠道重试；key 限流换 key 不算次数', 'How many times to retry on channel failures (5xx, timeouts, broken streams); switching keys on rate limits does not count')}>{t('失败重试', 'Retries')}</Tooltip>,
            dataIndex: 'retry_count',
            render: (v?: number) => t(`${v ?? 2} 次`, `${v ?? 2}`),
          },
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
      </SortableBody>
      <Modal open={editing !== null} title={isCollection
        ? (editing === 'new-collection' ? t('创建集合', 'Create collection') : t('编辑集合', 'Edit collection'))
        : (editing === 'new' ? t('创建服务分组', 'Create service group') : t('编辑服务分组', 'Edit service group'))} onOk={save} onCancel={() => setEditing(null)} okText={t('保存', 'Save')} destroyOnHidden>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          {isCollection ? (
            <Form.Item
              name="member_ids"
              label={t('包含的分组', 'Included groups')}
              extra={t(
                '请求某个模型时，按列表里的分组顺序取第一个提供它的分组，走它的渠道、按它的倍率计费；停用的分组跳过。顺序在列表里拖动调整。',
                'For each model, the first group in list order that offers it is used, with its channels and ratio; disabled groups are skipped. Drag rows in the list to change the order.',
              )}
            >
              <Select mode="multiple" options={groups.filter((g) => !g.is_collection).map((g) => ({ value: g.id, label: `${g.name} · ×${Number(g.ratio)}${g.enabled ? '' : t('（已停用）', ' (disabled)')}` }))} />
            </Form.Item>
          ) : (
            <Form.Item name="ratio" label={t('倍率', 'Ratio')} rules={[{ required: true }]} extra={t('1 表示按标价收费，0.5 表示半价', '1 = list price, 0.5 = half price')}>
              <InputNumber min={0} step={0.1} style={{ width: '100%' }} />
            </Form.Item>
          )}
          <Form.Item
            name="retry_count"
            label={t('失败重试次数', 'Retries on failure')}
            rules={[{ required: true }]}
            extra={t(
              '渠道出故障（5xx、超时、断流）时换下一个渠道重试几次；没有别的渠道时等 1 秒同渠道再试。key 被限流或失效时换同渠道的其他 key，不占次数。只在还没给用户返回内容之前能重试，整个请求最多 60 秒。',
              'Retries on channel failures (5xx, timeouts, broken streams), moving to the next channel, or retrying the same one after 1s when it is the only one. Switching keys on rate limits or invalid keys does not count. Retries happen only before any output reaches the user, within 60s per request.',
            )}
          >
            <InputNumber min={0} max={4} precision={0} style={{ width: 120 }} />
          </Form.Item>
          <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="is_public" label={t('适用范围', 'Availability')} valuePropName="checked" extra={t('所有用户指全部已启用的灵枢用户，不代表免登录或对互联网开放。', 'All users means every enabled Lingshu user; login is still required.') }>
            <Switch checkedChildren={t('所有用户', 'All users')} unCheckedChildren={t('指定用户分组', 'Selected groups')} />
          </Form.Item>
          <Form.Item noStyle dependencies={['is_public']}>
            {({ getFieldValue }) => getFieldValue('is_public') ? null : (
              <Form.Item
                name="user_group_ids"
                label={t('允许的用户分组', 'Allowed user groups')}
                rules={[{ required: true, type: 'array', min: 1, message: t('至少选择一个用户分组', 'Select at least one user group') }]}
                extra={<Button type="link" size="small" style={{ padding: 0 }} onClick={() => setManagingUserGroups(true)}>{t('没有合适的？新建或管理用户分组', 'None fits? Create or manage user groups')}</Button>}
              >
                <Select mode="multiple" options={userGroups.map((g) => ({ value: g.id, label: g.name }))} />
              </Form.Item>
            )}
          </Form.Item>
          <Form.Item name="description" label={t('说明', 'Description')}>
            <Input placeholder={t('例如：走官方渠道，稳定', 'e.g. official channels, stable')} />
          </Form.Item>
        </Form>
      </Modal>
      <UserGroupManager open={managingUserGroups} userGroups={userGroups} onClose={() => setManagingUserGroups(false)} onChanged={load} />
    </div>
  )
}
