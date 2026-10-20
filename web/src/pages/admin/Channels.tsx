import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Checkbox,
  Divider,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import { CheckCircleFilled, CloseCircleFilled, DeleteOutlined, PlusOutlined, WarningFilled } from '@ant-design/icons'
import { del, get, post, put, type Channel, type ChannelTemplate, type Group, type Model, type ProbeResult } from '../../api'
import { useApp } from '../../app'


type Row = { key: number; upstream: string; model_id?: number }
type FormValues = {
  template: string
  name: string
  api_key: string
  base_url_chat: string
  base_url_messages: string
  base_url_responses: string
  priority: number
  enabled: boolean
  group_ids: number[]
}

const protocolLabel: Record<string, string> = { chat: 'chat', messages: 'messages', responses: 'responses' }
let rowSeq = 1

export default function Channels() {
  const { t } = useApp()
  const { message } = App.useApp()
  const [channels, setChannels] = useState<Channel[]>([])
  const [templates, setTemplates] = useState<ChannelTemplate[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [models, setModels] = useState<Model[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Channel | 'new' | null>(null)
  const [rows, setRows] = useState<Row[]>([])
  const [fetched, setFetched] = useState<string[] | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [fetching, setFetching] = useState(false)
  const [probing, setProbing] = useState(false)
  const [probe, setProbe] = useState<ProbeResult[] | null>(null)
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<FormValues>()

  const load = useCallback(async () => {
    try {
      const [c, tpl, g, m] = await Promise.all([
        get<Channel[]>('/api/admin/channels'),
        get<ChannelTemplate[]>('/api/admin/channel-templates'),
        get<Group[]>('/api/admin/groups'),
        get<Model[]>('/api/admin/models'),
      ])
      setChannels(c)
      setTemplates(tpl)
      setGroups(g)
      setModels(m)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const modelByID = useMemo(() => new Map(models.map((m) => [m.id, m])), [models])
  const groupByID = useMemo(() => new Map(groups.map((g) => [g.id, g])), [groups])

  // 上游名和站内名一致（不区分大小写）时自动对上，管理员只需确认
  const matchModel = (upstream: string) => models.find((m) => m.name.toLowerCase() === upstream.toLowerCase())?.id

  const addRows = (names: string[]) => {
    setRows((old) => {
      const have = new Set(old.map((r) => r.upstream))
      return [...old, ...names.filter((n) => n && !have.has(n)).map((n) => ({ key: rowSeq++, upstream: n, model_id: matchModel(n) }))]
    })
  }

  const openNew = () => {
    form.resetFields()
    form.setFieldsValue({
      template: undefined,
      priority: 0,
      enabled: true,
      group_ids: groups.filter((g) => g.is_public).map((g) => g.id),
    })
    setRows([])
    setEditing('new')
  }

  const openEdit = (c: Channel) => {
    form.resetFields()
    form.setFieldsValue({ ...c, api_key: '' })
    setRows(c.models.map((m) => ({ key: rowSeq++, upstream: m.upstream_name || m.model_name, model_id: m.model_id })))
    setEditing(c)
  }

  const applyTemplate = (id: string) => {
    const tpl = templates.find((x) => x.id === id)
    if (!tpl) return
    form.setFieldsValue({
      name: form.getFieldValue('name') || (tpl.id === 'custom' ? '' : tpl.id),
      base_url_chat: tpl.base_url_chat,
      base_url_messages: tpl.base_url_messages,
      base_url_responses: tpl.base_url_responses,
    })
    if (tpl.preset_models?.length) addRows(tpl.preset_models)
  }

  // 拉取模型和冒烟测试用当前表单内容（可以是还没保存的渠道）
  const draft = () => {
    const v = form.getFieldsValue()
    return {
      id: editing && editing !== 'new' ? editing.id : 0,
      api_key: v.api_key ?? '',
      base_url_chat: v.base_url_chat ?? '',
      base_url_messages: v.base_url_messages ?? '',
      base_url_responses: v.base_url_responses ?? '',
    }
  }

  const fetchModels = async () => {
    setFetching(true)
    try {
      const names = await post<string[]>('/api/admin/channels/fetch-models', draft())
      const have = new Set(rows.map((r) => r.upstream))
      setPicked(names.filter((n) => have.has(n)))
      setFetched(names)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setFetching(false)
    }
  }

  const runProbe = async (d: ReturnType<typeof draft>, upstreams: string[]) => {
    const protocols = (['chat', 'messages', 'responses'] as const).filter((p) => d[`base_url_${p}`])
    const targets = upstreams.flatMap((m) => protocols.map((p) => ({ protocol: p, model: m })))
    if (!targets.length) {
      message.warning(t('先添加模型、填好协议地址', 'Add models and protocol URLs first'))
      return
    }
    if (targets.length > 20) {
      message.warning(t('一次最多测 20 项，请减少模型数量', 'At most 20 checks at a time'))
      return
    }
    setProbing(true)
    setProbe(null)
    try {
      setProbe(await post<ProbeResult[]>('/api/admin/channels/probe', { ...d, targets }))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setProbing(false)
    }
  }

  const save = async () => {
    const v = await form.validateFields()
    const missing = rows.findIndex((r) => !r.upstream.trim() || !r.model_id)
    if (missing >= 0) {
      message.error(t(`第 ${missing + 1} 行模型没填完整：上游模型名和站内模型都要填`, `Row ${missing + 1} is incomplete`))
      return
    }
    const ids = rows.map((r) => r.model_id)
    if (new Set(ids).size !== ids.length) {
      message.error(t('同一个站内模型在这个渠道里只能映射一次', 'Each site model can only be mapped once per channel'))
      return
    }
    const body = {
      ...v,
      template: v.template ?? (editing !== 'new' ? editing?.template : '') ?? '',
      models: rows.map((r) => ({ model_id: r.model_id, model_name: modelByID.get(r.model_id!)?.name, upstream_name: r.upstream.trim() })),
    }
    setSaving(true)
    try {
      if (editing === 'new') await post('/api/admin/channels', body)
      else if (editing) await put(`/api/admin/channels/${editing.id}`, body)
      setEditing(null)
      message.success(t('渠道已保存', 'Channel saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const toggle = async (c: Channel, enabled: boolean) => {
    try {
      await put(`/api/admin/channels/${c.id}`, { ...c, api_key: '', enabled })
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const remove = async (c: Channel) => {
    try {
      await del(`/api/admin/channels/${c.id}`)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const protocols = (c: Pick<Channel, 'base_url_chat' | 'base_url_messages' | 'base_url_responses'>) =>
    (['chat', 'messages', 'responses'] as const).filter((p) => c[`base_url_${p}`])

  const templateID = Form.useWatch('template', form)
  const currentTemplate = templates.find((x) => x.id === templateID)

  return (
    <div className="page" style={{ maxWidth: 1400 }}>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          {t('渠道', 'Channels')}
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={openNew}>
          {t('添加渠道', 'Add channel')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {t(
          '渠道就是一个上游厂商的地址 + key。同一模型挂多个渠道时，先用优先级最高的，同优先级随机分流，失败自动换下一个。连续失败的渠道会暂停 5 分钟。',
          'A channel is an upstream endpoint plus key. When several channels serve a model, the highest priority is used first, ties are load-balanced, and failures fall over automatically. Channels that keep failing are paused for 5 minutes.',
        )}
      </Typography.Paragraph>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={channels}
        pagination={false}
        columns={[
          {
            title: t('名称', 'Name'),
            dataIndex: 'name',
            render: (v, c) => (
              <>
                {v} {c.template && <Tag>{templates.find((x) => x.id === c.template)?.name ?? c.template}</Tag>}
              </>
            ),
          },
          { title: t('协议', 'Protocols'), render: (_, c) => protocols(c).map((p) => <Tag key={p}>{protocolLabel[p]}</Tag>) },
          {
            title: t('模型', 'Models'),
            dataIndex: 'models',
            render: (ms: Channel['models']) =>
              ms.length ? (
                <Tooltip
                  title={ms.map((m) => (
                    <div key={m.model_id} className="mono">
                      {m.model_name}
                      {m.upstream_name && ` ← ${m.upstream_name}`}
                    </div>
                  ))}
                >
                  <span>{ms.map((m) => m.model_name).join(', ')}</span>
                </Tooltip>
              ) : (
                <Typography.Text type="warning">{t('未配置模型', 'No models')}</Typography.Text>
              ),
          },
          { title: t('分组', 'Groups'), dataIndex: 'group_ids', render: (ids: number[]) => ids.map((id) => <Tag key={id}>{groupByID.get(id)?.name}</Tag>) },
          { title: t('优先级', 'Priority'), dataIndex: 'priority' },
          { title: 'Key', dataIndex: 'api_key_masked', render: (v) => <span className="mono">{v}</span> },
          {
            title: t('状态', 'Status'),
            render: (_, c) => (
              <Space>
                <Switch size="small" checked={c.enabled} onChange={(v) => toggle(c, v)} />
                {c.cooling && (
                  <Tooltip title={t('连续失败，暂停 5 分钟后自动恢复', 'Paused after repeated failures; resumes automatically in 5 minutes')}>
                    <Tag color="orange">{t('冷却中', 'Cooling')}</Tag>
                  </Tooltip>
                )}
              </Space>
            ),
          },
          {
            title: '',
            align: 'right',
            render: (_, c) => (
              <Space>
                <Button size="small" type="link" onClick={() => runProbe({ id: c.id, api_key: '', base_url_chat: c.base_url_chat, base_url_messages: c.base_url_messages, base_url_responses: c.base_url_responses }, c.models.map((m) => m.upstream_name || m.model_name))}>
                  {t('测试', 'Test')}
                </Button>
                <Button size="small" type="link" onClick={() => openEdit(c)}>
                  {t('编辑', 'Edit')}
                </Button>
                <Popconfirm title={t('删除后经过这个渠道的请求会换到其他渠道，没有其他渠道的模型将不可用。确定？', 'Requests will move to other channels; models with no other channel become unavailable. Delete?')} onConfirm={() => remove(c)}>
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Drawer
        open={editing !== null}
        onClose={() => setEditing(null)}
        size={760}
        title={editing === 'new' ? t('添加渠道', 'Add channel') : `${t('编辑渠道', 'Edit channel')} · ${editing && typeof editing === 'object' ? editing.name : ''}`}
        extra={
          <Space>
            <Button loading={probing} onClick={() => runProbe(draft(), rows.map((r) => r.upstream).filter(Boolean))}>
              {t('测试', 'Test')}
            </Button>
            <Button type="primary" loading={saving} onClick={save}>
              {t('保存', 'Save')}
            </Button>
          </Space>
        }
      >
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item
            name="template"
            label={t('模板', 'Template')}
            extra={
              <>
                {currentTemplate?.note}
                {editing !== 'new' && (
                  <div>{t('换模板会按模板重新填写各协议地址，保存后才生效，可以先点"测试"。', 'Switching templates refills the protocol URLs; nothing changes until you save, so you can test first.')}</div>
                )}
              </>
            }
          >
            <Select
              placeholder={t('选一个厂商，自动填好地址', 'Pick a vendor to prefill the URLs')}
              onChange={applyTemplate}
              options={templates.map((x) => ({ value: x.id, label: x.name }))}
            />
          </Form.Item>
          <Space style={{ display: 'flex' }} align="start">
            <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]} style={{ width: 260 }}>
              <Input />
            </Form.Item>
            <Form.Item name="priority" label={t('优先级', 'Priority')} extra={t('大的优先，小的作兜底', 'Higher first; lower is fallback')} style={{ width: 160 }}>
              <InputNumber style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked">
              <Switch />
            </Form.Item>
          </Space>
          <Form.Item
            name="api_key"
            label="API key"
            rules={editing === 'new' ? [{ required: true, message: t('填写上游的 API key', 'Enter the upstream API key') }] : []}
            extra={editing && editing !== 'new' ? t(`留空表示不修改（当前 ${editing.api_key_masked}）`, `Leave empty to keep the current key (${editing.api_key_masked})`) : undefined}
          >
            <Input.Password autoComplete="new-password" className="mono" />
          </Form.Item>
          <Typography.Text type="secondary">{t('各协议的地址（上游不支持的协议留空）', 'Base URL per protocol (leave unsupported ones empty)')}</Typography.Text>
          <div style={{ marginTop: 8 }}>
            {(['chat', 'messages', 'responses'] as const).map((p) => (
              <Form.Item key={p} name={`base_url_${p}`} style={{ marginBottom: 8 }}>
                <Input addonBefore={<span style={{ display: 'inline-block', width: 72 }}>{protocolLabel[p]}</span>} placeholder={{ chat: 'https://…/v1', messages: 'https://…/anthropic', responses: 'https://…/v1' }[p]} className="mono" />
              </Form.Item>
            ))}
          </div>
          <Form.Item name="group_ids" label={t('所属分组', 'Groups')} style={{ marginTop: 16 }}>
            <Select mode="multiple" options={groups.map((g) => ({ value: g.id, label: `${g.name}${g.is_public ? '' : t('（特殊）', ' (special)')}` }))} />
          </Form.Item>
        </Form>

        <Divider titlePlacement="start">{t('模型映射', 'Model mapping')}</Divider>
        <Typography.Paragraph type="secondary">
          {t('左边是上游的模型名，右边选它对应的站内模型。名字相同的会自动对上。', 'Upstream model name on the left, the site model it maps to on the right. Identical names are matched automatically.')}
        </Typography.Paragraph>
        <Space style={{ marginBottom: 12 }}>
          <Button loading={fetching} onClick={fetchModels}>
            {t('从上游拉取模型', 'Fetch from upstream')}
          </Button>
          <Button icon={<PlusOutlined />} onClick={() => setRows((r) => [...r, { key: rowSeq++, upstream: '' }])}>
            {t('手动添加', 'Add manually')}
          </Button>
        </Space>
        <Table
          size="small"
          rowKey="key"
          pagination={false}
          dataSource={rows}
          locale={{ emptyText: t('还没有模型', 'No models yet') }}
          columns={[
            {
              title: t('上游模型名', 'Upstream model'),
              render: (_, r) => (
                <Input
                  size="small"
                  className="mono"
                  value={r.upstream}
                  onChange={(e) => setRows((rs) => rs.map((x) => (x.key === r.key ? { ...x, upstream: e.target.value, model_id: x.model_id ?? matchModel(e.target.value) } : x)))}
                />
              ),
            },
            {
              title: t('站内模型', 'Site model'),
              render: (_, r) => (
                <Select
                  size="small"
                  style={{ width: '100%' }}
                  showSearch={{ optionFilterProp: 'label' }}
                  placeholder={t('选择站内模型', 'Choose a site model')}
                  value={r.model_id}
                  status={r.model_id ? undefined : 'warning'}
                  onChange={(v) => setRows((rs) => rs.map((x) => (x.key === r.key ? { ...x, model_id: v } : x)))}
                  options={models.map((m) => ({ value: m.id, label: m.name }))}
                />
              ),
            },
            {
              width: 40,
              render: (_, r) => <Button size="small" type="text" icon={<DeleteOutlined />} onClick={() => setRows((rs) => rs.filter((x) => x.key !== r.key))} />,
            },
          ]}
        />
        {rows.some((r) => r.upstream && !r.model_id) && (
          <Alert
            style={{ marginTop: 12 }}
            type="warning"
            showIcon
            title={t('有模型还没选站内模型。站内还没有的模型，先到「模型」页添加并定好价格。', 'Some rows have no site model. Add missing models with prices on the Models page first.')}
          />
        )}
      </Drawer>

      <Modal
        open={fetched !== null}
        title={t(`上游有 ${fetched?.length ?? 0} 个模型，勾选要接入的`, `${fetched?.length ?? 0} upstream models — pick the ones to add`)}
        onCancel={() => setFetched(null)}
        onOk={() => {
          addRows(picked)
          setFetched(null)
        }}
        okText={t('添加', 'Add')}
        width={560}
      >
        <Checkbox.Group value={picked} onChange={(v) => setPicked(v as string[])} style={{ display: 'flex', flexDirection: 'column', gap: 6, maxHeight: 420, overflowY: 'auto' }}>
          {fetched?.map((n) => (
            <Checkbox key={n} value={n}>
              <span className="mono">{n}</span>
              {matchModel(n) && <Tag color="green" style={{ marginLeft: 8 }}>{t('站内已有', 'on site')}</Tag>}
            </Checkbox>
          ))}
        </Checkbox.Group>
      </Modal>

      <Modal open={probing || probe !== null} title={t('冒烟测试', 'Smoke test')} footer={null} onCancel={() => setProbe(null)} width={760}>
        {probing && !probe ? (
          <Typography.Paragraph>{t('正在用真实请求测试每个模型，推理模型可能要十几秒…', 'Sending real requests to each model; reasoning models may take a few seconds…')}</Typography.Paragraph>
        ) : (
          <Table
            size="small"
            rowKey={(r) => r.protocol + r.model}
            pagination={false}
            dataSource={probe ?? []}
            columns={[
              { title: t('协议', 'Protocol'), dataIndex: 'protocol' },
              { title: t('上游模型', 'Upstream model'), dataIndex: 'model', render: (v) => <span className="mono">{v}</span> },
              {
                title: t('结果', 'Result'),
                render: (_, r) =>
                  r.ok ? (
                    <Tag icon={<CheckCircleFilled />} color="success">{t('通过', 'OK')}</Tag>
                  ) : (
                    <Tooltip title={r.error}>
                      <Tag icon={<CloseCircleFilled />} color="error">{r.http_status ? `HTTP ${r.http_status}` : t('失败', 'Failed')}</Tag>
                      <div style={{ maxWidth: 260, fontSize: 12 }} className="muted">{r.error?.slice(0, 120)}</div>
                    </Tooltip>
                  ),
              },
              { title: t('首字', 'First byte'), dataIndex: 'first_byte_ms', render: (v) => (v ? `${(v / 1000).toFixed(1)}s` : '-') },
              { title: t('总耗时', 'Total'), dataIndex: 'total_ms', render: (v) => `${(v / 1000).toFixed(1)}s` },
              {
                title: t('流式用量', 'Stream usage'),
                render: (_, r) =>
                  !r.ok ? '-' : r.usage_reported ? (
                    <span>{r.input_tokens} / {r.output_tokens}</span>
                  ) : (
                    <Tooltip title={t('上游流里没有返回用量，这个渠道只能按内容估算计费', 'No usage in the stream; billing will be estimated')}>
                      <Tag icon={<WarningFilled />} color="warning">{t('无', 'none')}</Tag>
                    </Tooltip>
                  ),
              },
            ]}
          />
        )}
      </Modal>
    </div>
  )
}
