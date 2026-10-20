import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import { PlusOutlined, SyncOutlined, WarningFilled } from '@ant-design/icons'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'
import { del, get, post, put, type AccountEntry, type Channel, type UpstreamAccount } from '../../api'
import { useApp } from '../../app'
import VendorIcon from '../../components/VendorIcon'

dayjs.extend(relativeTime)

type EditValues = {
  name: string
  channel_ids: number[]
  cost_factor: number
  alert_below?: number | null
  auto_sync: boolean
  note: string
  initial_balance?: number | null
}

// 上游账户：估算各厂商账号还剩多少钱。纯运维工具，不影响用户余额和扣费。
export default function Accounts() {
  const { t, money, currency, rate, lang } = useApp()
  const { message } = App.useApp()
  const [accounts, setAccounts] = useState<UpstreamAccount[]>([])
  const [syncTemplates, setSyncTemplates] = useState<string[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<UpstreamAccount | 'new' | null>(null)
  const [entryOf, setEntryOf] = useState<{ account: UpstreamAccount; kind: 'recharge' | 'calibrate' } | null>(null)
  const [historyOf, setHistoryOf] = useState<UpstreamAccount | null>(null)
  const [history, setHistory] = useState<AccountEntry[]>([])
  const [syncing, setSyncing] = useState<number | null>(null)
  const [form] = Form.useForm<EditValues>()
  const [entryForm] = Form.useForm<{ amount: number; note: string }>()
  const linked = Form.useWatch('channel_ids', form)

  const symbol = currency === 'CNY' ? '¥' : '$'
  // 金额按显示货币输入，服务端换算成美元并记下原始金额
  const toDisplay = (usd: string | null | undefined) => (usd == null ? undefined : Number(usd) * (currency === 'CNY' ? rate : 1))
  const toUSD = (v: number | null | undefined) => (v == null ? null : String(currency === 'CNY' ? v / rate : v))
  dayjs.locale(lang === 'zh' ? 'zh-cn' : 'en')

  const load = useCallback(async () => {
    try {
      const [a, c] = await Promise.all([
        get<{ accounts: UpstreamAccount[]; sync_templates: string[] }>('/api/admin/upstream-accounts'),
        get<Channel[]>('/api/admin/channels'),
      ])
      setAccounts(a.accounts)
      setSyncTemplates(a.sync_templates)
      setChannels(c)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const channelByID = new Map(channels.map((c) => [c.id, c]))
  const ownerOf = new Map<number, UpstreamAccount>()
  accounts.forEach((a) => a.channel_ids.forEach((id) => ownerOf.set(id, a)))
  const canSync = (ids: number[] | undefined) => (ids ?? []).some((id) => syncTemplates.includes(channelByID.get(id)?.template ?? ''))

  const openEdit = (a: UpstreamAccount | 'new') => {
    form.resetFields()
    if (a === 'new') {
      form.setFieldsValue({ cost_factor: 1, auto_sync: true, channel_ids: [], note: '' })
    } else {
      form.setFieldsValue({
        name: a.name,
        channel_ids: a.channel_ids,
        cost_factor: Number(a.cost_factor),
        alert_below: toDisplay(a.alert_below),
        auto_sync: a.auto_sync,
        note: a.note,
      })
    }
    setEditing(a)
  }

  const save = async () => {
    const v = await form.validateFields()
    const body = {
      name: v.name,
      channel_ids: v.channel_ids ?? [],
      cost_factor: String(v.cost_factor),
      alert_below: toUSD(v.alert_below),
      auto_sync: v.auto_sync && canSync(v.channel_ids),
      note: v.note ?? '',
      ...(editing === 'new' && v.initial_balance != null ? { initial_balance: String(v.initial_balance), initial_currency: currency } : {}),
    }
    try {
      if (editing === 'new') await post('/api/admin/upstream-accounts', body)
      else await put(`/api/admin/upstream-accounts/${(editing as UpstreamAccount).id}`, body)
      message.success(t('已保存', 'Saved'))
      setEditing(null)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const saveEntry = async () => {
    const v = await entryForm.validateFields()
    try {
      await post(`/api/admin/upstream-accounts/${entryOf!.account.id}/entries`, {
        kind: entryOf!.kind,
        amount: String(v.amount),
        currency,
        note: v.note ?? '',
      })
      message.success(entryOf!.kind === 'recharge' ? t('已记录充值', 'Recharge recorded') : t('已校准', 'Calibrated'))
      setEntryOf(null)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const sync = async (a: UpstreamAccount) => {
    setSyncing(a.id)
    try {
      await post(`/api/admin/upstream-accounts/${a.id}/sync`)
      message.success(t('已按官方余额校准', 'Synced with the vendor balance'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSyncing(null)
      load()
    }
  }

  const openHistory = async (a: UpstreamAccount) => {
    setHistoryOf(a)
    setHistory([])
    setHistory(await get<AccountEntry[]>(`/api/admin/upstream-accounts/${a.id}/entries`).catch(() => []))
  }

  const remove = async (a: UpstreamAccount) => {
    try {
      await del(`/api/admin/upstream-accounts/${a.id}`)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const original = (e: AccountEntry) =>
    e.original_amount == null ? '' : `${e.original_currency === 'CNY' ? '¥' : '$'}${Number(e.original_amount).toFixed(2)}`

  const kindLabel: Record<AccountEntry['kind'], string> = {
    recharge: t('充值', 'Recharge'),
    calibrate: t('手动校准', 'Manual calibration'),
    sync: t('自动校准', 'Auto calibration'),
  }

  return (
    <div className="page">
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 8 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          {t('上游账户', 'Upstream accounts')}
        </Typography.Title>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => openEdit('new')}>
          {t('添加账户', 'Add account')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {t(
          '估算你在各厂商账号里还剩多少钱：每次请求按上游成本（站内基础价 × 成本系数）扣减估算余额，校准时改成真实余额。DeepSeek、Kimi 每小时自动用官方余额接口校准。只有管理员能看到，和用户余额、扣费无关。',
          'Estimates how much is left in each vendor account: every request deducts its upstream cost (base price × cost factor); calibration resets it to the real balance. DeepSeek and Kimi are calibrated hourly via their balance APIs. Admin-only; unrelated to user balances and billing.',
        )}
      </Typography.Paragraph>

      <Table
        rowKey="id"
        loading={loading}
        dataSource={accounts}
        pagination={false}
        columns={[
          {
            title: t('账户', 'Account'),
            dataIndex: 'name',
            render: (v, a) => (
              <Space size={8} align="center">
                <span style={{ display: 'inline-flex', gap: 4 }}>
                  {[...new Set(a.channel_ids.map((id) => channelByID.get(id)?.template))].map((tpl) => (
                    <VendorIcon key={tpl ?? ''} template={tpl} />
                  ))}
                </span>
                <span>
                  {v}
                  <div className="muted" style={{ fontSize: 12 }}>
                    {a.channel_ids.length
                      ? a.channel_ids.map((id) => channelByID.get(id)?.name ?? `#${id}`).join('、')
                      : t('还没关联渠道', 'No channels linked')}
                  </div>
                </span>
              </Space>
            ),
          },
          {
            title: t('估算余额', 'Estimated balance'),
            dataIndex: 'balance',
            render: (v, a) => {
              const low = a.alert_below != null && Number(v) < Number(a.alert_below)
              return (
                <div>
                  <span style={{ fontSize: 16, fontWeight: 500, color: low || Number(v) <= 0 ? '#ff4d4f' : undefined }}>{money(v, 2)}</span>
                  {low && (
                    <Tag color="red" style={{ marginLeft: 8 }}>
                      {t('低于提醒线', 'Below alert')} {money(a.alert_below!, 2)}
                    </Tag>
                  )}
                  <div className="muted" style={{ fontSize: 12 }}>
                    {a.calibrated_at ? `${dayjs(a.calibrated_at).fromNow()}${t('校准', ' calibrated')}` : t('还没校准过', 'Never calibrated')}
                  </div>
                  {a.auto_sync && a.sync_error && (
                    <Tooltip title={a.sync_error}>
                      <Typography.Text type="warning" style={{ fontSize: 12 }}>
                        <WarningFilled /> {t('自动校准失败', 'Auto calibration failed')}
                      </Typography.Text>
                    </Tooltip>
                  )}
                </div>
              )
            },
          },
          {
            title: (
              <Tooltip title={t('上游成本是估算值；向用户收费 = 站内价 × 分组倍率', 'Upstream cost is estimated; revenue = site price × group ratio')}>
                {t('近 7 天成本 / 收费', '7-day cost / revenue')}
              </Tooltip>
            ),
            render: (_, a) => (
              <span>
                {money(a.cost_7d, 2)} <span className="muted">/ {money(a.revenue_7d, 2)}</span>
              </span>
            ),
          },
          { title: t('成本系数', 'Cost factor'), dataIndex: 'cost_factor', render: (v) => `× ${Number(v)}` },
          {
            title: t('校准方式', 'Calibration'),
            dataIndex: 'auto_sync',
            render: (v) => (v ? <Tag color="blue">{t('每小时自动', 'Hourly, automatic')}</Tag> : <Tag>{t('手动', 'Manual')}</Tag>),
          },
          {
            title: '',
            align: 'right',
            render: (_, a) => (
              <Space size={0} wrap>
                <Button size="small" type="link" onClick={() => { entryForm.resetFields(); setEntryOf({ account: a, kind: 'recharge' }) }}>
                  {t('充值', 'Recharge')}
                </Button>
                {canSync(a.channel_ids) ? (
                  <Button size="small" type="link" icon={<SyncOutlined spin={syncing === a.id} />} onClick={() => sync(a)}>
                    {t('立即校准', 'Sync now')}
                  </Button>
                ) : (
                  <Button size="small" type="link" onClick={() => { entryForm.resetFields(); setEntryOf({ account: a, kind: 'calibrate' }) }}>
                    {t('校准', 'Calibrate')}
                  </Button>
                )}
                <Button size="small" type="link" onClick={() => openHistory(a)}>
                  {t('流水', 'History')}
                </Button>
                <Button size="small" type="link" onClick={() => openEdit(a)}>
                  {t('编辑', 'Edit')}
                </Button>
                <Popconfirm
                  title={t('删除这个账户？', 'Delete this account?')}
                  description={t('充值和校准记录一起删除；渠道和请求日志不受影响。', 'Its history is deleted too; channels and request logs are kept.')}
                  onConfirm={() => remove(a)}
                >
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Modal
        open={editing !== null}
        title={editing === 'new' ? t('添加上游账户', 'Add upstream account') : t('编辑上游账户', 'Edit upstream account')}
        okText={t('保存', 'Save')}
        onOk={save}
        onCancel={() => setEditing(null)}
        width={560}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true, message: t('填写名称', 'Enter a name') }]}>
            <Input placeholder={t('例如：DeepSeek 主账号', 'e.g. DeepSeek main')} />
          </Form.Item>
          <Form.Item
            name="channel_ids"
            label={t('关联渠道', 'Channels')}
            extra={t('这些渠道的请求从这个账户扣钱。一个渠道只能属于一个账户。', 'Requests on these channels are charged to this account. A channel belongs to one account.')}
          >
            <Select
              mode="multiple"
              optionFilterProp="title"
              options={channels.map((c) => {
                const owner = ownerOf.get(c.id)
                const elsewhere = owner && (editing === 'new' || owner.id !== (editing as UpstreamAccount | null)?.id)
                return {
                  value: c.id,
                  title: c.name,
                  label: (
                    <Space size={6} align="center">
                      <VendorIcon template={c.template} size={14} />
                      {c.name}
                      {elsewhere && <span className="muted">（{t('现属于', 'now in')} {owner.name}）</span>}
                    </Space>
                  ),
                }
              })}
            />
          </Form.Item>
          {editing === 'new' && (
            <Form.Item name="initial_balance" label={t('当前余额', 'Current balance')} extra={t('填厂商后台显示的余额；能自动校准的可以不填。', 'As shown in the vendor console; optional if it can auto-sync.')}>
              <InputNumber style={{ width: '100%' }} prefix={symbol} min={0} />
            </Form.Item>
          )}
          <Space style={{ display: 'flex' }} align="start" wrap>
            <Form.Item
              name="cost_factor"
              label={t('成本系数', 'Cost factor')}
              rules={[{ required: true }]}
              extra={t('上游成本 = 站内基础价 × 系数。和官方价一样就填 1。', 'Upstream cost = base price × factor. Use 1 for list price.')}
              style={{ width: 240 }}
            >
              <InputNumber style={{ width: '100%' }} min={0.0001} step={0.05} prefix="×" />
            </Form.Item>
            <Form.Item name="alert_below" label={t('提醒线', 'Alert below')} extra={t('估算余额低于它时标红；空表示不提醒', 'Highlight when below; empty for none')} style={{ width: 240 }}>
              <InputNumber style={{ width: '100%' }} prefix={symbol} min={0} />
            </Form.Item>
          </Space>
          <Form.Item
            name="auto_sync"
            label={t('每小时自动校准', 'Hourly auto calibration')}
            valuePropName="checked"
            extra={canSync(linked) ? t('用关联渠道的 key 查询官方余额。', 'Uses a linked channel key to query the vendor balance.') : t('目前只有 DeepSeek、Kimi 有余额接口；关联这两家的渠道后才能开。', 'Only DeepSeek and Kimi have balance APIs; link one of their channels first.')}
          >
            <Switch disabled={!canSync(linked)} />
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input placeholder={t('例如：账号是谁的、在哪充值', 'e.g. who owns it, where to recharge')} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={entryOf !== null}
        title={`${entryOf?.kind === 'recharge' ? t('记录充值', 'Record recharge') : t('校准余额', 'Calibrate balance')} · ${entryOf?.account.name ?? ''}`}
        okText={t('确定', 'Confirm')}
        onOk={saveEntry}
        onCancel={() => setEntryOf(null)}
        destroyOnHidden
      >
        <Typography.Paragraph>
          {t('当前估算余额', 'Current estimate')}：<b>{entryOf && money(entryOf.account.balance, 2)}</b>
        </Typography.Paragraph>
        <Form form={entryForm} layout="vertical" requiredMark={false}>
          <Form.Item
            name="amount"
            label={entryOf?.kind === 'recharge' ? t('充值金额', 'Amount') : t('真实余额', 'Actual balance')}
            extra={entryOf?.kind === 'calibrate' ? t('填厂商后台显示的余额，估算余额会直接改成它，差额记为估算偏差。', 'Enter the balance shown in the vendor console; the difference is recorded as estimate error.') : undefined}
            rules={[{ required: true, message: t('填写金额', 'Enter an amount') }]}
          >
            <InputNumber style={{ width: '100%' }} prefix={symbol} min={entryOf?.kind === 'recharge' ? 0.01 : undefined} />
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>

      <Drawer open={historyOf !== null} onClose={() => setHistoryOf(null)} title={`${t('流水', 'History')} · ${historyOf?.name ?? ''}`} size={760}>
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          title={t(
            '校准的"偏差"= 真实余额 − 估算余额。偏差持续为负，说明上游实际收得比估算多，可以调高成本系数；没登记的充值也会表现为正偏差。',
            'Calibration "error" = actual − estimate. Consistently negative means the vendor charges more than estimated — raise the cost factor. Unrecorded recharges show up as positive error.',
          )}
        />
        <Table
          size="small"
          rowKey="id"
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          dataSource={history}
          columns={[
            { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v) => dayjs(v).format('MM-DD HH:mm') },
            { title: t('类型', 'Type'), dataIndex: 'kind', render: (v: AccountEntry['kind']) => <Tag color={v === 'recharge' ? 'green' : v === 'sync' ? 'blue' : undefined}>{kindLabel[v]}</Tag> },
            {
              title: t('变动 / 偏差', 'Change / error'),
              dataIndex: 'amount',
              render: (v, e) => {
                const n = Number(v)
                const spent = e.estimated_spent != null ? Number(e.estimated_spent) : 0
                return (
                  <span style={{ color: e.kind === 'recharge' ? '#52c41a' : Math.abs(n) < 0.005 ? undefined : n < 0 ? '#ff4d4f' : '#faad14' }}>
                    {n >= 0 ? '+' : ''}
                    {money(v, 2)}
                    {e.kind !== 'recharge' && spent > 0 && (
                      <span className="muted" style={{ fontSize: 12 }}>
                        {' '}
                        （{t('估算花费', 'est. spent')} {money(spent, 2)}，{((n / spent) * 100).toFixed(0)}%）
                      </span>
                    )}
                  </span>
                )
              },
            },
            { title: t('余额', 'Balance'), render: (_, e) => `${money(e.balance_before, 2)} → ${money(e.balance_after, 2)}` },
            { title: t('原始金额', 'Original'), render: (_, e) => original(e) },
            { title: t('操作人', 'By'), dataIndex: 'operator', render: (v) => v || <span className="muted">{t('系统', 'System')}</span> },
            { title: t('备注', 'Note'), dataIndex: 'note' },
          ]}
        />
      </Drawer>
    </div>
  )
}
