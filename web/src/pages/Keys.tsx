import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Card, DatePicker, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { ExportOutlined, PlusOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import { del, get, patch, post, type APIKey, type Group, type PricedModel } from '../api'
import { useApp } from '../app'
import { magpieImportURL } from '../guide'
import { rowEditing } from '../rowEditing'

type KeyForm = { name: string; group_id: number; quota_limit?: number | null; expires_at?: Dayjs | null }

export default function Keys() {
  const { money, t, currency, rate } = useApp()
  const { message } = App.useApp()
  const [keys, setKeys] = useState<APIKey[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [apiBaseURL, setAPIBaseURL] = useState('')
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<APIKey | 'new' | null>(null)
  const [created, setCreated] = useState('')
  // 打开编辑时记下原来的过期时间：原来就过期的 key 允许只改名字，不算新选的过期时间
  const [originalExpiry, setOriginalExpiry] = useState<Dayjs | null>(null)
  const [form] = Form.useForm<KeyForm>()

  const load = useCallback(async () => {
    try {
      const [k, m, endpoints] = await Promise.all([
        get<APIKey[]>('/api/me/keys'),
        get<{ groups: Group[]; models: PricedModel[] }>('/api/me/models'),
        get<{ api_base_url: string }>('/api/me/endpoints'),
      ])
      setKeys(k)
      setGroups(m.groups)
      setAPIBaseURL(endpoints.api_base_url)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  // 限额在界面上按当前货币输入，提交时换回 USD
  const toUSD = (v?: number | null) => (v == null ? null : String(currency === 'CNY' ? v / rate : v))
  const fromUSD = (v: string | null) => (v == null ? null : Number((Number(v) * (currency === 'CNY' ? rate : 1)).toFixed(4)))

  const openNew = () => {
    form.setFieldsValue({ name: '', group_id: groups[0]?.id, quota_limit: null, expires_at: null })
    setOriginalExpiry(null)
    setEditing('new')
  }
  const openEdit = (k: APIKey) => {
    const expires = k.expires_at ? dayjs(k.expires_at) : null
    form.setFieldsValue({ name: k.name, group_id: k.group_id, quota_limit: fromUSD(k.quota_limit), expires_at: expires })
    setOriginalExpiry(expires)
    setEditing(k)
  }

  const save = async () => {
    let v: KeyForm
    try {
      v = await form.validateFields()
    } catch {
      return
    }
    const body = { name: v.name, group_id: v.group_id, quota_limit: toUSD(v.quota_limit), expires_at: v.expires_at ? v.expires_at.toISOString() : null }
    try {
      if (editing === 'new') {
        const r = await post<{ key: string }>('/api/me/keys', body)
        setCreated(r.key)
      } else if (editing) {
        await patch(`/api/me/keys/${editing.id}`, body)
        message.success(t('已保存', 'Saved'))
      }
      setEditing(null)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const toggle = async (k: APIKey, enabled: boolean) => {
    try {
      await patch(`/api/me/keys/${k.id}`, { enabled })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const remove = async (k: APIKey) => {
    try {
      await del(`/api/me/keys/${k.id}`)
      message.success(t('已删除', 'Deleted'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const base = apiBaseURL || `${window.location.origin}/v1`
  const urls = { base, root: base.replace(/\/v1\/?$/, '') }

  return (
    <div className="page">
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          API keys
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={openNew} disabled={!groups.length}>
          {t('创建 API key', 'Create API key')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {t(
          '每个 key 属于一个服务分组，按该分组倍率计费。可以给 key 设置限额，用完后这个 key 会被拒绝，账户余额不受影响；也可以设置过期时间，到期后同样会被拒绝。',
          'Each key belongs to a service group and is billed at that group\'s ratio. A key can have a spending limit; once reached, that key is rejected while your balance stays untouched. You can also set an expiration time; an expired key is rejected too.',
        )}
      </Typography.Paragraph>
      <Card size="small" style={{ marginBottom: 16 }}>
        <Space size={12} wrap>
          <Typography.Text type="secondary">{t('接入地址', 'Base URL')}</Typography.Text>
          <Typography.Text code copyable className="mono">
            {base}
          </Typography.Text>
        </Space>
      </Card>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={keys}
        onRow={(k) => rowEditing(() => openEdit(k), `${t('编辑 API key', 'Edit API key')} · ${k.name}`)}
        pagination={false}
        locale={{ emptyText: t('还没有 key，点右上角创建一个', 'No keys yet. Create one from the top right.') }}
        columns={[
          { title: t('名称', 'Name'), dataIndex: 'name', render: (v, k) => <span>{v}{k.project_name && <Tag color="gold" style={{ marginLeft: 6 }}>{t('项目', 'Project')} · {k.project_name}</Tag>}</span> },
          { title: 'Key', dataIndex: 'prefix', render: (v) => <span className="mono">{v}…</span> },
          { title: t('服务分组', 'Service group'), dataIndex: 'group_name', render: (v) => <Tag>{v}</Tag> },
          {
            title: t('已用 / 限额', 'Used / limit'),
            render: (_, k) => (
              <span className="nowrap">
                {money(k.quota_used)} / {k.quota_limit ? money(k.quota_limit, 2) : t('不限', 'No limit')}
              </span>
            ),
          },
          {
            title: t('过期时间', 'Expires'),
            render: (_, k) => {
              if (!k.expires_at) return <span className="muted">{t('永不过期', 'Never')}</span>
              const d = dayjs(k.expires_at)
              return (
                <span className="nowrap">
                  {d.isBefore(dayjs()) && <Tag color="red">{t('已过期', 'Expired')}</Tag>}
                  {d.format('YYYY-MM-DD HH:mm')}
                </span>
              )
            },
          },
          {
            title: t('最后使用', 'Last used'),
            dataIndex: 'last_used_at',
            render: (v) => (v ? dayjs(v).format('YYYY-MM-DD HH:mm') : <span className="muted">{t('从未', 'Never')}</span>),
          },
          { title: t('启用', 'Enabled'), dataIndex: 'enabled', render: (v, k) => <Switch size="small" checked={v} onChange={(c) => toggle(k, c)} /> },
          {
            title: '',
            align: 'right',
            render: (_, k) => (
              <Popconfirm
                title={t('删除后用这个 key 的程序会立即失效，确定删除？', 'Apps using this key will stop working immediately. Delete it?')}
                onConfirm={() => remove(k)}
              >
                <Button size="small" type="link" danger>
                  {t('删除', 'Delete')}
                </Button>
              </Popconfirm>
            ),
          },
        ]}
      />

      <Modal
        open={editing !== null}
        title={editing === 'new' ? t('创建 API key', 'Create API key') : t('编辑 API key', 'Edit API key')}
        onOk={save}
        onCancel={() => setEditing(null)}
        okText={editing === 'new' ? t('创建', 'Create') : t('保存', 'Save')}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true, message: t('起个名字，方便区分用途', 'Give it a name') }]}>
            <Input placeholder={t('例如：笔记本 Claude Code', 'e.g. Laptop Claude Code')} maxLength={50} />
          </Form.Item>
          <Form.Item name="group_id" label={t('服务分组', 'Service group')} rules={[{ required: true }]}>
            <Select
              options={groups.map((g) => ({
                value: g.id,
                label: `${g.name} · ${g.is_collection ? t('按模型自动选分组', 'routes by model') : `${t('倍率', 'ratio')} ${Number(g.ratio)}`}${g.description ? ' · ' + g.description : ''}`,
              }))}
            />
          </Form.Item>
          <Form.Item
            name="quota_limit"
            label={t('限额', 'Spending limit')}
            extra={t('留空表示不限额', 'Leave empty for no limit')}
          >
            <InputNumber min={0.01} step={1} style={{ width: '100%' }} prefix={currency === 'CNY' ? '¥' : '$'} />
          </Form.Item>
          <Form.Item
            name="expires_at"
            label={t('过期时间', 'Expires')}
            extra={t('按本地时间选择，留空表示永不过期', 'Choose in local time. Leave empty to never expire.')}
            rules={[
              {
                validator: (_rule, value?: Dayjs | null) => {
                  if (!value) return Promise.resolve()
                  // 原样保留已有的过期时间（key 已经过期时也允许只改名字）
                  if (originalExpiry && originalExpiry.isSame(value)) return Promise.resolve()
                  if (!value.isAfter(dayjs())) return Promise.reject(new Error(t('过期时间要晚于现在', 'Expiration must be later than now')))
                  return Promise.resolve()
                },
              },
            ]}
          >
            <DatePicker showTime style={{ width: '100%' }} placeholder={t('永不过期', 'Never')} disabledDate={(d) => d.isBefore(dayjs(), 'day')} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!created}
        title={t('API key 已创建', 'API key created')}
        onOk={() => setCreated('')}
        onCancel={() => setCreated('')}
        cancelButtonProps={{ style: { display: 'none' } }}
        okText={t('我已保存', 'I\'ve saved it')}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          title={t('完整的 key 只显示这一次，关闭后无法再查看。请现在复制保存。', 'This is the only time the full key is shown. Copy and save it now.')}
        />
        <Typography.Paragraph copyable={{ text: created }} className="mono" style={{ fontSize: 15, wordBreak: 'break-all' }}>
          {created}
        </Typography.Paragraph>
        {/* 只有本人此刻能看到完整 key，在这里一键导入 Magpie 最顺手；链接参数在 # 后，不经过任何服务器 */}
        <Space wrap>
          <Button href={magpieImportURL(urls, created)} target="_blank" rel="noreferrer" icon={<ExportOutlined />}>{t('连同 key 添加到 Magpie', 'Add to Magpie with this key')}</Button>
          <Typography.Text type="secondary">{t('需要先安装 Magpie', 'Requires Magpie')}</Typography.Text>
        </Space>
      </Modal>
    </div>
  )
}
