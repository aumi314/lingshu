import { useState } from 'react'
import { Descriptions, Drawer, Table, Tag, Typography } from 'antd'
import type { TablePaginationConfig } from 'antd'
import dayjs from 'dayjs'
import type { OperationChange, OperationEntry } from '../api'
import { useApp } from '../app'
import { paymentNames } from '../payment'

type T = ReturnType<typeof useApp>['t']
type Money = ReturnType<typeof useApp>['money']

export function targetLabel(t: T, type: string): string {
  return ({
    user: t('用户', 'User'), api_key: 'Key', balance: t('余额', 'Balance'), group: t('服务分组', 'Service group'),
    user_group: t('用户分组', 'User group'), model: t('模型', 'Model'), channel: t('渠道', 'Channel'),
    settings: t('系统设置', 'Settings'), payment: t('支付配置', 'Payment'), registration_code: t('注册码', 'Registration code'), redeem_code: t('兑换码', 'Redeem code'),
    topup: t('充值订单', 'Top-up order'),
  } as Record<string, string>)[type] ?? type
}

export const targetTypes = ['user', 'api_key', 'balance', 'topup', 'group', 'user_group', 'model', 'channel', 'settings', 'payment', 'registration_code', 'redeem_code']

// 充值订单的当前状态（发起充值那条记录上实时显示）
function orderStatus(t: T, status: string): [string, string] {
  return ({
    pending: ['default', t('待付款', 'Pending')],
    success: ['green', t('已到账', 'Credited')],
    failed: ['red', t('失败', 'Failed')],
    expired: ['default', t('已过期', 'Expired')],
  } as Record<string, [string, string]>)[status] ?? ['default', status]
}

function actionLabel(t: T, o: OperationEntry): string {
  const special: Record<string, string> = {
    'balance.adjust': t('额度调整', 'Balance adjustment'),
    'topup.create': t('发起充值', 'Top-up started'),
    'topup.credit': t('充值到账', 'Top-up credited'),
    'registration_code.create': t('生成注册码', 'Generate codes'),
    'registration_code.update': t('撤销注册码', 'Revoke code'),
    'redeem.credit': t('兑换码充值', 'Code redeemed'),
    'redeem_code.create': t('生成兑换码', 'Generate redeem codes'),
    'redeem_code.update': t('撤销兑换码', 'Revoke redeem code'),
  }
  if (special[o.action]) return special[o.action]
  if (o.action === 'user.create' && o.actor_id !== null && o.actor_id === o.user_id) return t('注册', 'Sign up')
  if (o.action === 'user.update' && o.changes.length === 1 && o.changes[0].field === 'password') return t('修改密码', 'Change password')
  const verb = o.action.endsWith('.create') ? t('新建', 'Create ') : o.action.endsWith('.delete') ? t('删除', 'Delete ') : t('修改', 'Update ')
  return verb + targetLabel(t, o.target_type)
}

function fieldLabel(t: T, field: string): string {
  if (field.startsWith('values.')) return t('参数 ', 'Param ') + field.slice(7)
  if (field.startsWith('secrets.')) return t('密钥 ', 'Secret ') + field.slice(8)
  return ({
    username: t('用户名', 'Username'), role: t('角色', 'Role'), enabled: t('状态', 'Status'), note: t('备注', 'Note'),
    password: t('密码', 'Password'), user_groups: t('用户分组', 'User groups'), name: t('名称', 'Name'), prefix: t('前缀', 'Prefix'),
    group: t('服务分组', 'Service group'), quota_limit: t('限额', 'Quota'), ratio: t('倍率', 'Ratio'), is_public: t('所有用户可用', 'Open to all'),
    description: t('说明', 'Description'), input_price: t('输入价', 'Input price'), output_price: t('输出价', 'Output price'),
    cache_read_price: t('缓存读价', 'Cache read price'), cache_write_price: t('缓存写价', 'Cache write price'), price_rules: t('价格规则', 'Price rules'), fast_multiplier: t('Fast 倍率', 'Fast multiplier'),
    template: t('模板', 'Template'), api_key: 'API key', priority: t('优先级', 'Priority'), base_url_chat: 'Chat URL',
    base_url_messages: 'Messages URL', base_url_responses: 'Responses URL', base_url_systemone: 'System One URL', proxy_url: t('代理', 'Proxy'),
    groups: t('服务分组', 'Service groups'), models: t('模型映射', 'Models'), balance: t('余额', 'Balance'), status: t('状态', 'Status'),
    prefixes: t('码', 'Codes'), amount: t('面额', 'Amount'), cny_per_usd: t('显示汇率', 'Exchange rate'), payment_enabled: t('开放充值', 'Top-up enabled'),
    registration_mode: t('注册方式', 'Registration'), fx_mode: t('汇率模式', 'Rate mode'), user_concurrency: t('每用户并发上限', 'Per-user concurrency'), user_rpm: t('每用户每分钟请求数', 'Per-user RPM'), reserve_output_tokens: t('默认预扣输出', 'Default held output'), api_base_url: t('接入地址', 'API base URL'), deployment_mode: t('使用模式', 'Usage mode'),
    environment: t('环境', 'Environment'), currency: t('币种', 'Currency'), unit_price: t('单价', 'Unit price'),
    min_usd: t('最低额度', 'Min'), max_usd: t('最高额度', 'Max'), methods: t('支付方式', 'Methods'),
    credit_usd: t('到账额度', 'Credit'), pay_amount: t('应付', 'To pay'), provider: t('支付服务', 'Provider'), method: t('支付方式', 'Method'),
  } as Record<string, string>)[field] ?? field
}

function valueText(t: T, money: Money, field: string, v: unknown): string {
  if (v === undefined || v === null || v === '') return '—'
  if (field === 'balance' || field === 'credit_usd') return money(v as string)
  if (field === 'provider') return paymentNames[v as string] ?? String(v)
  if (field.endsWith('_price') && typeof v === 'string') return money(v, 6)
  if (field === 'enabled') return v ? t('启用', 'Enabled') : t('停用', 'Disabled')
  if (typeof v === 'boolean') return v ? t('是', 'Yes') : t('否', 'No')
  if (Array.isArray(v) && v.every((x) => typeof x !== 'object')) return v.length ? v.join('、') : t('（空）', '(none)')
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

function secretText(t: T, o: OperationEntry): string {
  return o.action.endsWith('.create') ? t('已设置', 'Set') : o.action.endsWith('.delete') ? '—' : t('已更换', 'Changed')
}

// 一行摘要：余额类显示金额；修改显示前两项；新建、删除只给项数，细节点开看
function summary(t: T, money: Money, o: OperationEntry) {
  if (o.action === 'topup.create') {
    const v = (field: string) => o.changes.find((c) => c.field === field)?.to
    const [color, label] = orderStatus(t, o.order?.status ?? '')
    return <>
      {money(String(v('credit_usd') ?? '0'))}
      <span className="muted"> · {t('应付', 'to pay')} {String(v('pay_amount') ?? '—')} · {paymentNames[String(v('provider'))] ?? String(v('provider') ?? '')}</span>
      {o.order && <Tag color={color} style={{ marginLeft: 8 }}>{label}</Tag>}
    </>
  }
  if (o.amount !== undefined) {
    const n = Number(o.amount)
    const c = o.changes[0]
    return <>
      <span style={{ color: n < 0 ? '#ff4d4f' : '#52c41a' }}>{n > 0 ? '+' : ''}{money(o.amount)}</span>
      {c && <span className="muted"> · {valueText(t, money, 'balance', c.from)} → {valueText(t, money, 'balance', c.to)}</span>}
    </>
  }
  if (!o.action.endsWith('.update')) return <span className="muted">{t(`${o.changes.length} 项`, `${o.changes.length} fields`)}</span>
  const shown = o.changes.slice(0, 2).map((c) => {
    if (c.secret) return `${fieldLabel(t, c.field)} ${secretText(t, o)}`
    if (typeof c.from === 'object' && c.from !== null && !Array.isArray(c.from)) return `${fieldLabel(t, c.field)} ${t('已修改', 'changed')}`
    return `${fieldLabel(t, c.field)} ${valueText(t, money, c.field, c.from)} → ${valueText(t, money, c.field, c.to)}`
  })
  const more = o.changes.length - shown.length
  return <>{shown.join('；')}{more > 0 && <span className="muted">{t(` 等 ${o.changes.length} 项`, ` +${more} more`)}</span>}</>
}

export default function OperationLogTable({ logs, loading, pagination }: { logs?: OperationEntry[]; loading: boolean; pagination: TablePaginationConfig }) {
  const { money, t } = useApp()
  const [detail, setDetail] = useState<OperationEntry | null>(null)
  const actor = (o: OperationEntry) => o.actor_id === null ? t('系统', 'System') : o.actor || t('未知', 'Unknown')
  const target = (o: OperationEntry) => o.target_name || (o.action === 'registration_code.create' ? t(`${(o.changes[0]?.to as unknown[] | undefined)?.length ?? 0} 个`, `${(o.changes[0]?.to as unknown[] | undefined)?.length ?? 0} codes`) : targetLabel(t, o.target_type))
  const tagColor = (o: OperationEntry) => o.action.endsWith('.delete') ? 'red' : o.action.endsWith('.create') ? 'green' : o.target_type === 'balance' ? 'gold' : 'blue'

  return (
    <>
      <Table<OperationEntry>
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={logs}
        onRow={(o) => ({ onClick: () => setDetail(o), style: { cursor: 'pointer' } })}
        pagination={pagination}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('这段时间没有操作记录', 'No operations in this period') }}
        columns={[
          { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v: string) => <span className="nowrap">{dayjs(v).format('MM-DD HH:mm:ss')}</span> },
          { title: t('操作人', 'Operator'), render: (_, o) => actor(o) },
          { title: t('操作', 'Operation'), render: (_, o) => <Tag color={tagColor(o)}>{actionLabel(t, o)}</Tag> },
          { title: t('对象', 'Target'), render: (_, o) => <span className="nowrap">{target(o)}</span> },
          { title: t('变更', 'Changes'), render: (_, o) => summary(t, money, o) },
        ]}
      />
      <Drawer open={!!detail} onClose={() => setDetail(null)} title={detail ? actionLabel(t, detail) : ''} size={560}>
        {detail && <>
          <Descriptions column={1} size="small" bordered style={{ marginBottom: 16 }}>
            <Descriptions.Item label={t('时间', 'Time')}>{dayjs(detail.created_at).format('YYYY-MM-DD HH:mm:ss')}</Descriptions.Item>
            <Descriptions.Item label={t('操作人', 'Operator')}>{actor(detail)}</Descriptions.Item>
            <Descriptions.Item label={t('对象', 'Target')}>{targetLabel(t, detail.target_type)} · {target(detail)}{detail.target_id && <span className="muted"> （ID {detail.target_id}）</span>}</Descriptions.Item>
            {detail.amount !== undefined && <Descriptions.Item label={t('金额', 'Amount')}>{money(detail.amount, 10)}</Descriptions.Item>}
            {detail.note && <Descriptions.Item label={t('备注', 'Note')}>{detail.note}</Descriptions.Item>}
            {detail.order && <>
              <Descriptions.Item label={t('订单号', 'Order ID')}><Typography.Text copyable>{detail.order.id}</Typography.Text></Descriptions.Item>
              <Descriptions.Item label={t('支付服务 / 方式', 'Provider / method')}>{paymentNames[detail.order.provider] ?? detail.order.provider} / {detail.order.method || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('订单状态', 'Order status')}><Tag color={orderStatus(t, detail.order.status)[0]}>{orderStatus(t, detail.order.status)[1]}</Tag></Descriptions.Item>
              {detail.order.status === 'success' && <Descriptions.Item label={t('实际支付合计', 'Total paid')}>{detail.order.total} {detail.order.currency}</Descriptions.Item>}
            </>}
          </Descriptions>
          <Table<OperationChange>
            rowKey="field"
            size="small"
            pagination={false}
            dataSource={detail.changes}
            columns={[
              { title: t('字段', 'Field'), render: (_, c) => fieldLabel(t, c.field) },
              { title: t('改前', 'Before'), render: (_, c) => c.secret ? '—' : <span style={{ wordBreak: 'break-all' }}>{valueText(t, money, c.field, c.from)}</span> },
              { title: t('改后', 'After'), render: (_, c) => c.secret ? secretText(t, detail) : <span style={{ wordBreak: 'break-all' }}>{valueText(t, money, c.field, c.to)}</span> },
            ]}
          />
        </>}
      </Drawer>
    </>
  )
}
