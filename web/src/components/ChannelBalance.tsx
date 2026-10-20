// 渠道的余额估算（上游账户）：渠道列表里的"余额"列 + 点开的面板。
// 纯运维工具，不影响用户余额和扣费。一个渠道一把 key，对应一个上游账户。
import { useEffect, useState } from 'react'
import { Alert, App, Button, Descriptions, Divider, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import { SyncOutlined, WarningFilled } from '@ant-design/icons'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'
import { del, get, post, put, type AccountEntry, type Channel, type UpstreamAccount } from '../api'
import { useApp } from '../app'

dayjs.extend(relativeTime)

export function isLow(a: UpstreamAccount) {
  return Number(a.balance) <= 0 || (a.alert_below != null && Number(a.balance) < Number(a.alert_below))
}

// BalanceCell 是渠道列表里的"余额"列。
export function BalanceCell({ account, onOpen }: { account?: UpstreamAccount; onOpen: () => void }) {
  const { t, money, lang } = useApp()
  dayjs.locale(lang === 'zh' ? 'zh-cn' : 'en')
  if (!account) {
    return (
      <Button size="small" type="link" style={{ padding: 0 }} onClick={onOpen}>
        {t('开启估算', 'Track')}
      </Button>
    )
  }
  return (
    <div style={{ cursor: 'pointer', lineHeight: 1.3 }} onClick={onOpen}>
      <Typography.Link style={{ color: isLow(account) ? '#ff4d4f' : undefined, fontWeight: 500 }}>{money(account.balance, 2)}</Typography.Link>
      {account.auto_sync && account.sync_error ? (
        <Tooltip title={account.sync_error}>
          <div style={{ fontSize: 12 }}>
            <Typography.Text type="warning" style={{ fontSize: 12 }}>
              <WarningFilled /> {t('自动校准失败', 'Sync failed')}
            </Typography.Text>
          </div>
        </Tooltip>
      ) : (
        <div className="muted" style={{ fontSize: 12 }}>
          {account.calibrated_at ? `${dayjs(account.calibrated_at).fromNow()}${t('校准', '')}` : t('未校准', 'Not calibrated')}
        </div>
      )}
    </div>
  )
}

type SettingsValues = { cost_factor: number; alert_below?: number | null; auto_sync: boolean; note: string }

// BalanceDrawer：没开启时是开启表单；开启后是余额、操作、设置和流水。
export function BalanceDrawer({
  channel,
  account,
  canSync,
  onClose,
  onChanged,
}: {
  channel: Channel | null
  account?: UpstreamAccount
  canSync: boolean
  onClose: () => void
  onChanged: () => void
}) {
  const { t, money, currency, rate } = useApp()
  const { message } = App.useApp()
  const [history, setHistory] = useState<AccountEntry[]>([])
  const [entryKind, setEntryKind] = useState<'recharge' | 'calibrate' | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [form] = Form.useForm<SettingsValues & { initial_balance?: number | null }>()
  const [entryForm] = Form.useForm<{ amount: number; note: string }>()

  const symbol = currency === 'CNY' ? '¥' : '$'
  // 金额按显示货币输入，服务端换算成美元并记下原始金额
  const toDisplay = (usd: string | null | undefined) => (usd == null ? undefined : Number(usd) * (currency === 'CNY' ? rate : 1))
  const toUSD = (v: number | null | undefined) => (v == null ? null : String(currency === 'CNY' ? v / rate : v))

  const accountID = account?.id
  const loadHistory = async (id: number) => setHistory(await get<AccountEntry[]>(`/api/admin/upstream-accounts/${id}/entries`).catch(() => []))

  useEffect(() => {
    setHistory([])
    if (accountID) loadHistory(accountID)
  }, [accountID])

  // 设置表单按账户重新挂载，初始值直接给，不在打开后再 setFieldsValue
  const initialSettings = account
    ? { cost_factor: Number(account.cost_factor), alert_below: toDisplay(account.alert_below), auto_sync: account.auto_sync, note: account.note }
    : { cost_factor: 1, auto_sync: canSync, note: '' }

  const fail = (e: unknown) => message.error((e as Error).message)

  const saveSettings = async () => {
    const v = await form.validateFields()
    const body = {
      name: channel!.name,
      channel_ids: [channel!.id],
      cost_factor: String(v.cost_factor),
      alert_below: toUSD(v.alert_below),
      auto_sync: canSync && v.auto_sync,
      note: v.note ?? '',
    }
    try {
      if (account) {
        await put(`/api/admin/upstream-accounts/${account.id}`, body)
        message.success(t('已保存', 'Saved'))
      } else {
        const { id } = await post<{ id: number }>('/api/admin/upstream-accounts', {
          ...body,
          ...(v.initial_balance != null ? { initial_balance: String(v.initial_balance), initial_currency: currency } : {}),
        })
        if (canSync && v.initial_balance == null) await post(`/api/admin/upstream-accounts/${id}/sync`).catch(fail)
        message.success(t('已开启余额估算', 'Balance tracking enabled'))
      }
      onChanged()
    } catch (e) {
      fail(e)
    }
  }

  const saveEntry = async () => {
    const v = await entryForm.validateFields()
    try {
      await post(`/api/admin/upstream-accounts/${account!.id}/entries`, { kind: entryKind, amount: String(v.amount), currency, note: v.note ?? '' })
      message.success(entryKind === 'recharge' ? t('已记录充值', 'Recharge recorded') : t('已校准', 'Calibrated'))
      setEntryKind(null)
      onChanged()
      loadHistory(account!.id)
    } catch (e) {
      fail(e)
    }
  }

  const sync = async () => {
    setSyncing(true)
    try {
      await post(`/api/admin/upstream-accounts/${account!.id}/sync`)
      message.success(t('已按官方余额校准', 'Synced with the vendor balance'))
      loadHistory(account!.id)
    } catch (e) {
      fail(e)
    } finally {
      setSyncing(false)
      onChanged()
    }
  }

  const disable = async () => {
    try {
      await del(`/api/admin/upstream-accounts/${account!.id}`)
      onChanged()
      onClose()
    } catch (e) {
      fail(e)
    }
  }

  const kindLabel: Record<AccountEntry['kind'], string> = {
    recharge: t('充值', 'Recharge'),
    calibrate: t('手动校准', 'Manual'),
    sync: t('自动校准', 'Auto'),
  }

  const settings = (
    <Form form={form} key={accountID ?? `new-${channel?.id}`} initialValues={initialSettings} layout="vertical" requiredMark={false}>
      {!account && (
        <Form.Item
          name="initial_balance"
          label={t('当前余额', 'Current balance')}
          extra={canSync ? t('可以不填，开启后会自动读取官方余额。', 'Optional; the vendor balance is read automatically.') : t('填厂商后台显示的余额。', 'As shown in the vendor console.')}
        >
          <InputNumber style={{ width: '100%' }} prefix={symbol} min={0} />
        </Form.Item>
      )}
      <Space style={{ display: 'flex' }} align="start" wrap>
        <Form.Item
          name="cost_factor"
          label={t('成本系数', 'Cost factor')}
          rules={[{ required: true }]}
          extra={t('上游成本 = 站内基础价 × 系数。和官方价一样就填 1。', 'Upstream cost = base price × factor.')}
          style={{ width: 300 }}
        >
          <InputNumber style={{ width: '100%' }} min={0.0001} step={0.05} prefix="×" />
        </Form.Item>
        <Form.Item name="alert_below" label={t('提醒线', 'Alert below')} extra={t('估算余额低于它时标红；空表示不提醒', 'Highlight when below; empty for none')} style={{ width: 300 }}>
          <InputNumber style={{ width: '100%' }} prefix={symbol} min={0} />
        </Form.Item>
      </Space>
      <Form.Item
        name="auto_sync"
        label={t('每小时自动校准', 'Hourly auto calibration')}
        valuePropName="checked"
        extra={canSync ? t('用这个渠道的 key 查询官方余额。', 'Uses this channel key to query the vendor balance.') : t('目前只有 DeepSeek、Kimi 有余额接口，其他厂商只能手动校准。', 'Only DeepSeek and Kimi have balance APIs; others are calibrated manually.')}
      >
        <Switch disabled={!canSync} />
      </Form.Item>
      <Form.Item name="note" label={t('备注', 'Note')}>
        <Input placeholder={t('例如：账号是谁的、在哪充值', 'e.g. who owns it, where to recharge')} />
      </Form.Item>
      <Space>
        <Button type="primary" onClick={saveSettings}>
          {account ? t('保存设置', 'Save settings') : t('开启估算', 'Enable')}
        </Button>
        {account && (
          <Popconfirm
            title={t('关闭余额估算？', 'Stop tracking?')}
            description={t('充值和校准记录会一起删除；渠道和请求日志不受影响。', 'Recharge and calibration history is deleted; the channel and request logs are kept.')}
            onConfirm={disable}
          >
            <Button danger>{t('关闭估算', 'Stop tracking')}</Button>
          </Popconfirm>
        )}
      </Space>
    </Form>
  )

  return (
    <Drawer open={channel !== null} onClose={onClose} title={`${t('余额估算', 'Balance')} · ${channel?.name ?? ''}`} size={760} destroyOnHidden>
      {!account ? (
        <>
          <Typography.Paragraph type="secondary">
            {t(
              '估算这个渠道的上游账号还剩多少钱：每次请求按上游成本扣减估算余额，校准时改成真实余额。只有管理员能看到，和用户余额、扣费无关。',
              'Estimates what is left in this channel’s vendor account: each request deducts its upstream cost; calibration resets it to the real balance. Admin-only; unrelated to user billing.',
            )}
          </Typography.Paragraph>
          {settings}
        </>
      ) : (
        <>
          <Space align="center" size={16} wrap>
            <span style={{ fontSize: 28, fontWeight: 600, color: isLow(account) ? '#ff4d4f' : undefined }}>{money(account.balance, 2)}</span>
            {account.alert_below != null && Number(account.balance) < Number(account.alert_below) && (
              <Tag color="red">
                {t('低于提醒线', 'Below alert')} {money(account.alert_below, 2)}
              </Tag>
            )}
            <Space wrap>
              <Button onClick={() => { entryForm.resetFields(); setEntryKind('recharge') }}>{t('充值', 'Recharge')}</Button>
              {canSync && (
                <Button icon={<SyncOutlined spin={syncing} />} onClick={sync}>
                  {t('立即校准', 'Sync now')}
                </Button>
              )}
              <Button onClick={() => { entryForm.resetFields(); setEntryKind('calibrate') }}>{t('手动校准', 'Calibrate')}</Button>
            </Space>
          </Space>
          {account.auto_sync && account.sync_error && (
            <Alert type="warning" showIcon style={{ marginTop: 12 }} title={`${t('自动校准失败', 'Auto calibration failed')}：${account.sync_error}`} />
          )}
          <Descriptions size="small" column={2} style={{ marginTop: 16 }}>
            <Descriptions.Item label={t('最近校准', 'Last calibrated')}>
              {account.calibrated_at ? dayjs(account.calibrated_at).format('MM-DD HH:mm') : t('未校准', 'Never')}
            </Descriptions.Item>
            <Descriptions.Item label={t('校准方式', 'Calibration')}>{account.auto_sync ? t('每小时自动', 'Hourly, automatic') : t('手动', 'Manual')}</Descriptions.Item>
            <Descriptions.Item label={<Tooltip title={t('上游成本是估算值', 'Upstream cost is estimated')}>{t('近 7 天上游成本', '7-day upstream cost')}</Tooltip>}>
              {money(account.cost_7d, 2)}
            </Descriptions.Item>
            <Descriptions.Item label={t('近 7 天向用户收费', '7-day revenue')}>{money(account.revenue_7d, 2)}</Descriptions.Item>
            <Descriptions.Item label={t('成本系数', 'Cost factor')}>
              × {Number(account.cost_factor)}
            </Descriptions.Item>
          </Descriptions>

          <Divider titlePlacement="start">{t('流水', 'History')}</Divider>
          <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
            {t(
              '校准的"偏差"= 真实余额 − 估算余额。偏差持续为负，说明上游实际收得比估算多，可以调高成本系数；没登记的充值会表现为正偏差。',
              'Calibration "error" = actual − estimate. Consistently negative means the vendor charges more than estimated; raise the cost factor. Unrecorded recharges show up as positive error.',
            )}
          </Typography.Paragraph>
          <Table
            size="small"
            rowKey="id"
            pagination={{ pageSize: 20, hideOnSinglePage: true }}
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
              {
                title: t('原始金额', 'Original'),
                render: (_, e) => (e.original_amount == null ? '' : `${e.original_currency === 'CNY' ? '¥' : '$'}${Number(e.original_amount).toFixed(2)}`),
              },
              { title: t('操作人', 'By'), dataIndex: 'operator', render: (v) => v || <span className="muted">{t('系统', 'System')}</span> },
              { title: t('备注', 'Note'), dataIndex: 'note' },
            ]}
          />

          <Divider titlePlacement="start">{t('设置', 'Settings')}</Divider>
          {settings}
        </>
      )}

      <Modal
        open={entryKind !== null}
        title={entryKind === 'recharge' ? t('记录充值', 'Record recharge') : t('手动校准', 'Calibrate')}
        okText={t('确定', 'Confirm')}
        onOk={saveEntry}
        onCancel={() => setEntryKind(null)}
        destroyOnHidden
      >
        <Typography.Paragraph>
          {t('当前估算余额', 'Current estimate')}：<b>{account && money(account.balance, 2)}</b>
        </Typography.Paragraph>
        <Form form={entryForm} layout="vertical" requiredMark={false}>
          <Form.Item
            name="amount"
            label={entryKind === 'recharge' ? t('充值金额', 'Amount') : t('真实余额', 'Actual balance')}
            extra={entryKind === 'calibrate' ? t('填厂商后台显示的余额，估算余额会直接改成它，差额记为估算偏差。', 'The balance shown in the vendor console; the difference is recorded as estimate error.') : undefined}
            rules={[{ required: true, message: t('填写金额', 'Enter an amount') }]}
          >
            <InputNumber style={{ width: '100%' }} prefix={symbol} min={entryKind === 'recharge' ? 0.01 : undefined} />
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input />
          </Form.Item>
        </Form>
      </Modal>
    </Drawer>
  )
}
