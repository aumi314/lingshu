import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  App,
  Button,
  Checkbox,
  Collapse,
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
import { rowEditing } from '../../rowEditing'
import VendorIcon from '../../components/VendorIcon'
import ChannelStatus from '../../components/ChannelStatus'
import { BalanceCell, BalanceModal } from '../../components/ChannelBalance'
import { DragHandle, SortableBody, SortableRow } from '../../components/SortableRows'
import { arrayMove } from '@dnd-kit/sortable'
import { parseStandardRoot, recoverStandardRoot, standardBases } from '../../channelUrls'
import ChannelKeys, { toPayload, toRows, type KeyRow } from '../../components/ChannelKeys'


type Row = { key: number; upstream: string; model_id?: number }
type ProbeDraft = Pick<Channel, 'id' | 'template' | 'base_url_chat' | 'base_url_messages' | 'base_url_responses' | 'base_url_systemone' | 'proxy_url'> & { api_key: string; keys?: Channel['keys'] }
type FormValues = {
  template: string
  root_url: string
  name: string
  note: string
  base_url_chat: string
  base_url_messages: string
  base_url_responses: string
  base_url_systemone: string
  proxy_url: string
  priority: number
  enabled: boolean
  group_ids: number[]
}

const protocolLabel: Record<string, string> = { chat: 'chat', messages: 'messages', responses: 'responses', systemone: 'System One' }
const supportsSystemOne = (template?: string) => template === 'typesafe' || template === 'nace'
const configuredProtocols = (d: ProbeDraft) =>
  (['chat', 'messages', 'responses', 'systemone'] as const).filter((p) => d[`base_url_${p}`] && (p !== 'systemone' || supportsSystemOne(d.template)))
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
  const [keyRows, setKeyRows] = useState<KeyRow[]>([])
  const [probeKeyID, setProbeKeyID] = useState<number | undefined>()
  const [fetched, setFetched] = useState<string[] | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [modelSearch, setModelSearch] = useState('')
  const [fetching, setFetching] = useState(false)
  const [probing, setProbing] = useState(false)
  const [probeDraft, setProbeDraft] = useState<ProbeDraft | null>(null)
  const [probeModels, setProbeModels] = useState<string[]>([])
  const [probeProtocol, setProbeProtocol] = useState('')
  const [probeBatch, setProbeBatch] = useState(false)
  const [probingModel, setProbingModel] = useState('')
  const [probe, setProbe] = useState<ProbeResult[]>([])
  const [saving, setSaving] = useState(false)
  const [balanceOf, setBalanceOf] = useState<Channel | null>(null)
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
      template: 'standard',
      root_url: '',
      priority: 0,
      enabled: true,
      group_ids: groups.filter((g) => g.name === 'default' && g.enabled).map((g) => g.id),
    })
    setRows([])
    setKeyRows([])
    setEditing('new')
  }

  const openEdit = (c: Channel) => {
    form.resetFields()
    form.setFieldsValue({ ...c, root_url: c.template === 'standard' ? c.base_url_messages : '' })
    setKeyRows(toRows(c.keys))
    setRows(c.models.map((m) => ({ key: rowSeq++, upstream: m.upstream_name || m.model_name, model_id: m.model_id })))
    setEditing(c)
  }

  const applyTemplate = (id: string) => {
    const tpl = templates.find((x) => x.id === id)
    if (!tpl) return
    const current = form.getFieldsValue(true)
    if (id === 'standard') {
      const root = recoverStandardRoot(current)
      form.setFieldsValue({
        template: id,
        root_url: root ?? '',
        base_url_chat: '',
        base_url_messages: '',
        base_url_responses: '',
        base_url_systemone: '',
      })
      return
    }

    const previousRoot = current.root_url ? parseStandardRoot(current.root_url) : undefined
    const previousBases = previousRoot?.root ? standardBases(previousRoot.root) : undefined
    form.setFieldsValue({
      template: id,
      name: form.getFieldValue('name') || (tpl.id === 'custom' ? '' : tpl.id),
      root_url: '',
      base_url_chat: previousBases?.base_url_chat ?? tpl.base_url_chat,
      base_url_messages: previousBases?.base_url_messages ?? tpl.base_url_messages,
      base_url_responses: previousBases?.base_url_responses ?? tpl.base_url_responses,
      base_url_systemone: tpl.base_url_systemone,
      proxy_url: tpl.proxy_url ?? '',
    })
    if (tpl.preset_models?.length) addRows(tpl.preset_models)
  }

  // 拉取模型和冒烟测试用当前表单内容（可以是还没保存的渠道）
  const draft = () => {
    const v = form.getFieldsValue(true)
    const bases = v.template === 'standard'
      ? (() => {
          const parsed = parseStandardRoot(v.root_url ?? '')
          return parsed.root ? standardBases(parsed.root) : null
        })()
      : v
    if (!bases) return null
    return {
      id: editing && editing !== 'new' ? editing.id : 0,
      template: v.template ?? (editing !== 'new' ? editing?.template : '') ?? '',
      // 还没保存的新渠道用第一把新加的 key 拉模型、测试；已保存的渠道由服务端用第一把启用的 key
      api_key: editing === 'new' ? keyRows.find((k) => k.enabled && k.api_key)?.api_key ?? '' : '',
      keys: editing && editing !== 'new' ? editing.keys : undefined,
      base_url_chat: bases.base_url_chat ?? '',
      base_url_messages: bases.base_url_messages ?? '',
      base_url_responses: bases.base_url_responses ?? '',
      base_url_systemone: v.base_url_systemone ?? '',
      proxy_url: v.proxy_url ?? '',
    }
  }

  const fetchModels = async () => {
    const d = draft()
    if (!d) {
      void form.validateFields(['root_url']).catch(() => {})
      return
    }
    setFetching(true)
    try {
      const names = await post<string[]>('/api/admin/channels/fetch-models', d)
      const have = new Set(rows.map((r) => r.upstream))
      setPicked(names.filter((n) => have.has(n)))
      setModelSearch('')
      setFetched(names)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setFetching(false)
    }
  }

  const openProbe = (d: ProbeDraft, upstreams: string[]) => {
    const protocols = configuredProtocols(d)
    if (!protocols.length) {
      message.warning(t('先填好协议地址', 'Configure a protocol URL first'))
      return
    }
    const names = [...new Set(upstreams.map((name) => name.trim()).filter(Boolean))]
    if (!names.length) {
      message.warning(t('先映射模型', 'Add a model mapping first'))
      return
    }
    setProbeDraft(d)
    setProbeKeyID(undefined)
    setProbeModels(names)
    setProbeProtocol(protocols[0])
    setProbe([])
  }

  const runProbe = async (upstreams: string[], batch: boolean) => {
    if (!probeDraft || !probeProtocol) return
    if (!upstreams.length) {
      message.warning(t('这个渠道还没有映射模型', 'This channel has no mapped models'))
      return
    }
    const targets = upstreams.map((model) => ({ protocol: probeProtocol, model }))
    if (targets.length > 20) {
      message.warning(t('一次最多批量测试 20 个模型', 'At most 20 models can be tested at once'))
      return
    }
    setProbing(true)
    setProbeBatch(batch)
    setProbingModel(batch ? '' : upstreams[0])
    try {
      const results = await post<ProbeResult[]>('/api/admin/channels/probe', { ...probeDraft, keys: undefined, test_key_id: probeKeyID, targets })
      setProbe((current) => [
        ...current.filter((item) => !targets.some((target) => target.protocol === item.protocol && target.model === item.model)),
        ...results,
      ])
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setProbing(false)
      setProbeBatch(false)
      setProbingModel('')
    }
  }

  const save = async () => {
    const v = await form.validateFields()
    const d = draft()
    if (!d) return
    const missing = rows.findIndex((r) => !r.upstream.trim() || !r.model_id)
    if (missing >= 0) {
      message.error(t(`第 ${missing + 1} 行模型没填完整：上游模型名和站内模型都要填`, `Row ${missing + 1} is incomplete`))
      return
    }
    if (!keyRows.length) {
      message.error(t('至少要有一把 API key', 'Add at least one API key'))
      return
    }
    const ids = rows.map((r) => r.model_id)
    if (new Set(ids).size !== ids.length) {
      message.error(t('同一个站内模型在这个渠道里只能映射一次', 'Each site model can only be mapped once per channel'))
      return
    }
    const body = {
      ...d,
      name: v.name,
      note: v.note ?? '',
      api_key: undefined,
      keys: toPayload(keyRows),
      priority: v.priority,
      enabled: v.enabled,
      group_ids: v.group_ids,
      models: rows.map((r) => ({ model_id: r.model_id, model_name: modelByID.get(r.model_id!)?.name, upstream_name: r.upstream.trim() })),
    }
    setSaving(true)
    try {
      const res = editing === 'new' ? await post<{ keys_deduped?: number }>('/api/admin/channels', body) : editing ? await put<{ keys_deduped?: number }>(`/api/admin/channels/${editing.id}`, body) : null
      setEditing(null)
      const dedup = res?.keys_deduped ?? 0
      message.success(dedup ? t(`渠道已保存，自动去掉了 ${dedup} 把重复的 key`, `Saved; ${dedup} duplicate key(s) removed`) : t('渠道已保存', 'Channel saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  // 拖拽排序：先在页面上换位置，再保存；失败就重新加载
  const move = async (from: number, to: number) => {
    const next = arrayMove(channels, from, to)
    setChannels(next)
    try {
      await put('/api/admin/channels/order', { ids: next.map((c) => c.id) })
    } catch (e) {
      message.error((e as Error).message)
      load()
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

  const protocols = (c: Pick<Channel, 'template' | 'base_url_chat' | 'base_url_messages' | 'base_url_responses' | 'base_url_systemone'>) =>
    (['chat', 'messages', 'responses', 'systemone'] as const).filter((p) => c[`base_url_${p}`] && (p !== 'systemone' || supportsSystemOne(c.template)))

  const templateID = Form.useWatch('template', form)
  const rootURL = Form.useWatch('root_url', form)
  const currentTemplate = templates.find((x) => x.id === templateID)
  const probeByModel = useMemo(() => new Map(probe.filter((item) => item.protocol === probeProtocol).map((item) => [item.model, item])), [probe, probeProtocol])
  const visibleProtocols = supportsSystemOne(templateID)
    ? (['systemone'] as const)
    : (['chat', 'messages', 'responses'] as const)

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
          '渠道就是一个上游厂商的地址 + key。同一模型挂多个渠道时，先用优先级最高的，同优先级随机分流，失败自动换下一个。连续失败的渠道会暂停 5 分钟。左侧把手可以拖动调整列表顺序，只影响显示，不影响优先级。',
          'A channel is an upstream endpoint plus key. When several channels serve a model, the highest priority is used first, ties are load-balanced, and failures fall over automatically. Channels that keep failing are paused for 5 minutes. Drag the handle to reorder the list — display only, not priority.',
        )}
      </Typography.Paragraph>
      <SortableBody ids={channels.map((c) => c.id)} onMove={move}>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={channels}
        onRow={(c) => rowEditing(() => openEdit(c), `${t('编辑渠道', 'Edit channel')} · ${c.name}`)}
        pagination={false}
        components={{ body: { row: SortableRow } }}
        columns={[
          {
            key: 'sort',
            width: 40,
            render: () => <DragHandle title={t('拖动排序（只影响列表显示，不影响优先级）', 'Drag to reorder (display only, not routing priority)')} />,
          },
          {
            title: 'ID',
            dataIndex: 'id',
            width: 48,
            render: (v) => <span className="muted">{v}</span>,
          },
          {
            title: t('名称', 'Name'),
            dataIndex: 'name',
            render: (v, c) => (
              <Space size={8} align="center">
                <Tooltip title={templates.find((x) => x.id === c.template)?.name ?? t('自定义', 'Custom')}>
                  <span style={{ display: 'inline-flex', width: 20, justifyContent: 'center', alignItems: 'center' }}>
                    <VendorIcon template={c.template} />
                  </span>
                </Tooltip>
                <span>
                  <span>{v}</span>
                  {c.note && <div className="muted" style={{ fontSize: 12, whiteSpace: 'normal', maxWidth: 260 }}>{c.note}</div>}
                </span>
                {c.keys.length > 1 && (() => {
                  const bad = c.keys.filter((k) => k.enabled && k.status && k.status.state !== 'ok').length
                  const invalid = c.keys.some((k) => k.enabled && k.status?.state === 'invalid')
                  return (
                    <Tooltip title={t(`${c.keys.filter((k) => k.enabled).length} 把启用${bad ? `，${bad} 把冷却中或失效` : ''}`, `${c.keys.filter((k) => k.enabled).length} enabled${bad ? `, ${bad} cooling or invalid` : ''}`)}>
                      <Tag color={invalid ? 'red' : bad ? 'orange' : undefined}>{t(`${c.keys.length} 把 key`, `${c.keys.length} keys`)}</Tag>
                    </Tooltip>
                  )
                })()}
                {c.proxy_url && (
                  <Tooltip title={`${t('经代理', 'Via proxy')} ${c.proxy_url}`}>
                    <Tag color="geekblue">{t('代理', 'Proxy')}</Tag>
                  </Tooltip>
                )}
              </Space>
            ),
          },
          { title: t('协议', 'Protocols'), render: (_, c) => protocols(c).map((p) => <Tag key={p}>{protocolLabel[p]}</Tag>) },
          {
            title: (
              <Tooltip title={t('最近 24 小时：成功率（次数）；首字、输出速度为中位数，首字从发给上游算起；缓存为命中率。失败只算渠道自身的问题，参数错、上下文超长这类请求本身的问题不算。', 'Last 24 hours: success rate (attempts); first token and speed are medians, first token measured from sending upstream; cache is hit rate. Only failures caused by the channel count.')}>
                {t('连接状态', 'Connection')}
              </Tooltip>
            ),
            render: (_, c) => <ChannelStatus channel={c} />,
          },
          { title: t('服务分组', 'Service groups'), dataIndex: 'group_ids', render: (ids: number[]) => ids.map((id) => <Tag key={id}>{groupByID.get(id)?.name}</Tag>) },
          { title: t('优先级', 'Priority'), dataIndex: 'priority' },
          {
            title: (
              <Tooltip title={t('厂商后台的余额，只有管理员能看到，和用户余额无关', 'Vendor account balance; admin-only, unrelated to user balances')}>
                {t('额度', 'Balance')}
              </Tooltip>
            ),
            render: (_, c) => <span data-row-action onClick={(event) => event.stopPropagation()}><BalanceCell channel={c} onOpen={() => setBalanceOf(c)} /></span>,
          },
          {
            title: '',
            align: 'right',
            render: (_, c) => (
              <Space>
                <Button size="small" type="link" onClick={() => openProbe({ id: c.id, template: c.template, api_key: '', keys: c.keys, base_url_chat: c.base_url_chat, base_url_messages: c.base_url_messages, base_url_responses: c.base_url_responses, base_url_systemone: c.base_url_systemone, proxy_url: c.proxy_url }, c.models.map((m) => m.upstream_name || m.model_name))}>
                  {t('测试', 'Test')}
                </Button>
                <Popconfirm title={t('删除后经过这个渠道的请求会换到其他渠道，没有其他渠道的模型将不可用。确定？', 'Requests will move to other channels; models with no other channel become unavailable. Delete?')} onConfirm={() => remove(c)}>
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
          {
            title: t('启用', 'Enabled'),
            render: (_, c) => (
              <Switch size="small" checked={c.enabled} onChange={(v) => toggle(c, v)} />
            ),
          },
        ]}
      />
      </SortableBody>

      <BalanceModal channel={balanceOf} onClose={() => setBalanceOf(null)} onChanged={load} />

      <Drawer
        open={editing !== null}
        onClose={() => setEditing(null)}
        size={760}
        title={editing === 'new' ? t('添加渠道', 'Add channel') : `${t('编辑渠道', 'Edit channel')} · ${editing && typeof editing === 'object' ? editing.name : ''}`}
        extra={
          <Space>
            <Button onClick={() => {
              const d = draft()
              if (d) openProbe(d, rows.map((r) => r.upstream))
              else void form.validateFields(['root_url']).catch(() => {})
            }}>
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
            label={t('预设模板', 'Preset template')}
            extra={
              <>
                {currentTemplate?.note}
                {editing !== 'new' && !supportsSystemOne(templateID) && (
                  <div>{t('换模板会按模板重新填写各协议地址，保存后才生效，可以先点"测试"。', 'Switching templates refills the protocol URLs; nothing changes until you save, so you can test first.')}</div>
                )}
              </>
            }
          >
            <Select
              placeholder={t('选一个厂商，自动填好地址', 'Pick a vendor to prefill the URLs')}
              onChange={applyTemplate}
              options={templates.map((x) => ({
                value: x.id,
                label: (
                  <Space size={8} align="center">
                    <VendorIcon template={x.id} size={16} />
                    {x.name}
                  </Space>
                ),
              }))}
            />
          </Form.Item>
          <Space style={{ display: 'flex' }} align="start" wrap>
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
          <Form.Item name="note" label={t('备注', 'Note')} extra={t('质量结论、价格、来源等，显示在渠道列表名称下方', 'Quality notes, price, source; shown under the name in the list')}>
            <Input.TextArea rows={2} maxLength={500} showCount />
          </Form.Item>
          <Form.Item label="API key" required>
            <ChannelKeys value={keyRows} onChange={setKeyRows} />
          </Form.Item>
          <Typography.Text type="secondary">
            {templateID === 'standard'
              ? t('标准模板默认生成三种协议地址', 'The standard template generates URLs for all three protocols')
              : t('各协议的地址（上游不支持的协议留空）', 'Base URL per protocol (leave unsupported ones empty)')}
          </Typography.Text>
          {templateID === 'standard' ? (
            <>
              <Form.Item
                name="root_url"
                label={t('根地址', 'Root URL')}
                style={{ marginTop: 8, marginBottom: 8 }}
                rules={[{
                  validator: async (_, value: string) => {
                    const result = parseStandardRoot(value ?? '')
                    if (result.root) return
                    if (result.error === 'required') throw new Error(t('填写上游根地址', 'Enter the upstream root URL'))
                    if (result.error === 'path') throw new Error(t('只填域名根地址，不要带路径、查询参数或片段', 'Enter only the origin; paths, query strings and fragments are not allowed'))
                    throw new Error(t('请输入有效的 HTTP 或 HTTPS 地址', 'Enter a valid HTTP or HTTPS URL'))
                  },
                }]}
              >
                <Input className="mono" placeholder="https://api.example.com" />
              </Form.Item>
              {(() => {
                const parsed = parseStandardRoot(rootURL ?? '')
                if (!parsed.root) return null
                const root = parsed.root
                return (
                  <Collapse
                    size="small"
                    style={{ marginBottom: 8 }}
                    items={[{
                      key: 'urls',
                      label: t('查看最终请求地址', 'Show final request URLs'),
                      children: (
                        <Space direction="vertical" size={4}>
                          <Typography.Text className="mono">Chat · {root}/v1/chat/completions</Typography.Text>
                          <Typography.Text className="mono">Messages · {root}/v1/messages</Typography.Text>
                          <Typography.Text className="mono">Responses · {root}/v1/responses</Typography.Text>
                        </Space>
                      ),
                    }]}
                  />
                )
              })()}
            </>
          ) : (
            <div style={{ marginTop: 8 }}>
              {visibleProtocols.map((p) => (
                <Form.Item key={p} name={`base_url_${p}`} style={{ marginBottom: 8 }}>
                  <Input addonBefore={<span style={{ display: 'inline-block', width: 88 }}>{protocolLabel[p]}</span>} placeholder={{ chat: 'https://…/v1', messages: 'https://…/anthropic', responses: 'https://…/v1', systemone: 'https://…/v1' }[p]} className="mono" />
                </Form.Item>
              ))}
            </div>
          )}
          <Form.Item
            name="proxy_url"
            label={t('代理', 'Proxy')}
            style={{ marginTop: 16 }}
            extra={t(
              '空表示直连。填了以后，这个渠道的所有请求（转发、测试、拉取模型、查余额）都经这个代理。支持 socks5、http、https。',
              'Empty for direct. When set, all traffic for this channel (requests, tests, model lists, balance checks) goes through it. socks5, http and https are supported.',
            )}
          >
            <Input className="mono" placeholder="socks5://host:port" allowClear />
          </Form.Item>
          <Form.Item name="group_ids" label={t('所属服务分组', 'Service groups')}>
            <Select mode="multiple" options={groups.map((g) => ({ value: g.id, label: `${g.name}${g.enabled ? '' : t('（已停用）', ' (disabled)')}` }))} />
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
        okText={picked.length ? t(`添加 ${picked.length} 个`, `Add ${picked.length}`) : t('添加', 'Add')}
        width={820}
      >
        <Input.Search
          allowClear
          placeholder={t('搜索模型名，例如 claude、gpt-6', 'Search, e.g. claude, gpt-6')}
          value={modelSearch}
          onChange={(e) => setModelSearch(e.target.value)}
          style={{ marginBottom: 12 }}
        />
        <Checkbox.Group
          className="model-pick"
          value={picked}
          onChange={(v) => setPicked(v as string[])}
          style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '6px 16px', maxHeight: 460, overflowY: 'auto', overflowX: 'hidden', alignContent: 'start' }}
        >
          {fetched
            ?.filter((n) => !modelSearch.trim() || n.toLowerCase().includes(modelSearch.trim().toLowerCase()))
            .map((n) => (
              <Checkbox key={n} value={n} style={{ minWidth: 0 }}>
                <span style={{ display: 'flex', alignItems: 'center', minWidth: 0 }}>
                  <span className="mono" title={n} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {n}
                  </span>
                  {matchModel(n) && <Tag color="green" style={{ marginLeft: 6, flex: 'none' }}>{t('站内已有', 'on site')}</Tag>}
                </span>
              </Checkbox>
            ))}
        </Checkbox.Group>
      </Modal>

      <Modal
        open={probeDraft !== null}
        title={t('冒烟测试', 'Smoke test')}
        footer={null}
        onCancel={() => {
          if (!probing) {
            setProbeDraft(null)
            setProbe([])
          }
        }}
        width={900}
      >
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <Space style={{ width: '100%', justifyContent: 'space-between' }}>
            <Space>
              <Typography.Text>{t('协议', 'Protocol')}</Typography.Text>
              <Select
                value={probeProtocol || undefined}
                disabled={probing}
                style={{ width: 220 }}
                options={probeDraft ? configuredProtocols(probeDraft).map((protocol) => ({ value: protocol, label: protocolLabel[protocol] })) : []}
                onChange={setProbeProtocol}
              />
              {(probeDraft?.keys?.length ?? 0) > 1 && (
                <>
                  <Typography.Text>Key</Typography.Text>
                  <Select
                    allowClear
                    value={probeKeyID}
                    disabled={probing}
                    style={{ width: 240 }}
                    placeholder={t('第一把启用的', 'First enabled')}
                    options={(probeDraft?.keys ?? []).filter((k) => k.id).map((k) => ({ value: k.id!, label: `${k.masked}${k.note ? ` · ${k.note}` : ''}${k.enabled ? '' : t('（停用）', ' (off)')}` }))}
                    onChange={setProbeKeyID}
                  />
                </>
              )}
            </Space>
            <Button
              type="primary"
              loading={probing && probeBatch}
              disabled={probing || probeModels.length === 0}
              onClick={() => void runProbe(probeModels, true)}
            >
              {t(`批量测试全部模型（${probeModels.length}）`, `Test all models (${probeModels.length})`)}
            </Button>
          </Space>
          <Typography.Text type="secondary">
            {t('每项都会发送真实请求到上游，可能产生上游费用。', 'Each check sends a real request upstream and may incur provider charges.')}
          </Typography.Text>
          <Table
            size="small"
            rowKey="model"
            pagination={false}
            dataSource={probeModels.map((model) => ({ model }))}
            columns={[
              { title: t('上游模型', 'Upstream model'), dataIndex: 'model', render: (model) => <span className="mono">{model}</span> },
              {
                title: t('结果', 'Result'),
                render: (_, row) => {
                  const result = probeByModel.get(row.model)
                  if (!result) return <Typography.Text type="secondary">{t('未测试', 'Not tested')}</Typography.Text>
                  return result.ok ? (
                    <Tag icon={<CheckCircleFilled />} color="success">{t('通过', 'OK')}</Tag>
                  ) : (
                    <Tooltip title={result.error}>
                      <Tag icon={<CloseCircleFilled />} color="error">{result.http_status ? `HTTP ${result.http_status}` : t('失败', 'Failed')}</Tag>
                      <div style={{ maxWidth: 260, fontSize: 12 }} className="muted">{result.error?.slice(0, 120)}</div>
                    </Tooltip>
                  )
                },
              },
              {
                title: t('首字', 'First byte'),
                render: (_, row) => {
                  const result = probeByModel.get(row.model)
                  return result?.first_byte_ms ? `${(result.first_byte_ms / 1000).toFixed(1)}s` : '-'
                },
              },
              {
                title: t('总耗时', 'Total'),
                render: (_, row) => {
                  const result = probeByModel.get(row.model)
                  return result ? `${(result.total_ms / 1000).toFixed(1)}s` : '-'
                },
              },
              {
                title: t('用量（输入 / 输出）', 'Usage (input / output)'),
                render: (_, row) => {
                  const result = probeByModel.get(row.model)
                  if (!result?.ok) return '-'
                  return result.usage_reported ? (
                    <span>{result.input_tokens} / {result.output_tokens}</span>
                  ) : (
                    <Tooltip title={t('上游没有返回用量，这个渠道只能按内容估算计费', 'No usage returned; billing will be estimated')}>
                      <Tag icon={<WarningFilled />} color="warning">{t('无', 'none')}</Tag>
                    </Tooltip>
                  )
                },
              },
              {
                title: '',
                align: 'right',
                render: (_, row) => (
                  <Button
                    size="small"
                    type="link"
                    loading={probing && !probeBatch && probingModel === row.model}
                    disabled={probing}
                    onClick={() => void runProbe([row.model], false)}
                  >
                    {t('测试', 'Test')}
                  </Button>
                ),
              },
            ]}
          />
        </Space>
      </Modal>
    </div>
  )
}
