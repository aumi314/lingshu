import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Card, Col, DatePicker, Descriptions, Drawer, Row, Select, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import { get, type LogEntry, type ActivityEntry, type ActivityLogsResponse, type LogType, type RequestActivity, type User } from '../api'
import { priceText, tokens, useApp } from '../app'

type Filter = { type: LogType; range: [Dayjs, Dayjs]; model?: string; status?: string; user_id?: number }

export default function Logs({ admin = false }: { admin?: boolean }) {
  const { money, t } = useApp()
  const [filter, setFilter] = useState<Filter>({ type: 'all', range: [dayjs().subtract(6, 'day').startOf('day'), dayjs().endOf('day')] })
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ActivityLogsResponse | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [detail, setDetail] = useState<ActivityEntry | null>(null)
  const pageSize = 50
  const requestSequence = useRef(0)
  const typeLabel = (type: LogType) => ({ all: t('全部', 'All'), request: t('模型调用', 'Model calls'), topup: t('在线充值', 'Online top-ups'), adjustment: t('额度调整', 'Balance adjustments') })[type]
  const operatorLabel = (entry: ActivityEntry) => entry.type === 'topup' ? t('系统', 'System') : entry.operator || t('未知', 'Unknown')


  const statusTag: Record<LogEntry['status'], [string, string]> = {
    success: ['green', t('成功', 'Success')],
    client_cancelled: ['blue', t('客户端断开', 'Client cancelled')],
    upstream_error: ['red', t('上游出错', 'Upstream error')],
    rejected: ['orange', t('被拒绝', 'Rejected')],
  }

  const load = useCallback(async () => {
    const sequence = ++requestSequence.current
    setLoading(true)
    setDetail(null)
    const q = new URLSearchParams({
      type: filter.type,
      page: String(page),
      page_size: String(pageSize),
      from: filter.range[0].toISOString(),
      to: filter.range[1].toISOString(),
    })
    if (filter.model) q.set('model', filter.model)
    if (filter.status) q.set('status', filter.status)
    if (filter.user_id) q.set('user_id', String(filter.user_id))
    try {
      const result = await get<ActivityLogsResponse>(`${admin ? '/api/admin/logs' : '/api/me/logs'}?${q}`)
      if (sequence === requestSequence.current) { setData(result); setError('') }
    } catch (e) {
      if (sequence === requestSequence.current) { setData(null); setError((e as Error).message) }
    } finally {
      if (sequence === requestSequence.current) setLoading(false)
    }
  }, [admin, filter, page])

  useEffect(() => {
    void load()
    return () => { ++requestSequence.current }
  }, [load])

  useEffect(() => {
    if (admin) get<User[]>('/api/admin/users').then(setUsers).catch(() => {})
  }, [admin])

  const update = (f: Partial<Filter>) => {
    setPage(1)
    setFilter((old) => ({ ...old, ...f }))
  }

  const s = data?.summary
  return (
    <div className="page" style={{ maxWidth: 1400 }}>
      <Typography.Title level={3} className="page-title">
        {admin ? t('全站日志', 'All logs') : t('账户日志', 'Account logs')}
      </Typography.Title>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 16 }}>
        <DatePicker.RangePicker
          value={filter.range}
          allowClear={false}
          onChange={(v) => v && v[0] && v[1] && update({ range: [v[0].startOf('day'), v[1].endOf('day')] })}
          presets={[
            { label: t('今天', 'Today'), value: [dayjs().startOf('day'), dayjs().endOf('day')] },
            { label: t('最近 7 天', 'Last 7 days'), value: [dayjs().subtract(6, 'day').startOf('day'), dayjs().endOf('day')] },
            { label: t('本月', 'This month'), value: [dayjs().startOf('month'), dayjs().endOf('day')] },
            { label: t('最近 30 天', 'Last 30 days'), value: [dayjs().subtract(29, 'day').startOf('day'), dayjs().endOf('day')] },
          ]}
        />
        {admin && (
          <Select
            allowClear
            placeholder={t('全部用户', 'All users')}
            style={{ width: 160 }}
            value={filter.user_id}
            onChange={(v) => update({ user_id: v })}
            options={users.map((u) => ({ value: u.id, label: u.username }))}
          />
        )}
        <Select
          aria-label={t('日志类型', 'Log type')}
          value={filter.type}
          style={{ width: 180 }}
          options={(['all', 'request', 'topup', 'adjustment'] as LogType[]).map((type) => ({ value: type, label: typeLabel(type) }))}
          onChange={(type) => update({ type, model: undefined, status: undefined })}
        />
        {filter.type === 'request' && <>
        <Select
          allowClear
          placeholder={t('全部模型', 'All models')}
          style={{ width: 200 }}
          value={filter.model}
          onChange={(v) => update({ model: v })}
          options={(data?.models ?? []).map((m) => ({ value: m, label: m }))}
        />
        <Select
          allowClear
          placeholder={t('全部状态', 'All statuses')}
          style={{ width: 150 }}
          value={filter.status}
          onChange={(v) => update({ status: v })}
          options={Object.entries(statusTag).map(([k, [, label]]) => ({ value: k, label }))}
        />
        </>}
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={8}>
          <Card size="small">
            <Statistic title={t('消费金额', 'Consumption')} value={s ? money(s.cost) : '-'} />
          </Card>
        </Col>
        <Col xs={12} md={8}>
          <Card size="small">
            <Statistic title={t('请求数', 'Requests')} value={s?.requests ?? '-'} suffix={s?.failed ? <Typography.Text type="danger" style={{ fontSize: 14 }}>{s.failed} {t('失败', 'failed')}</Typography.Text> : null} />
          </Card>
        </Col>
        <Col xs={12} md={8}><Card size="small"><Statistic title={t('充值到账', 'Top-up credits')} value={s ? money(s.topup_total) : '-'} /></Card></Col>
        <Col xs={12} md={8}><Card size="small"><Statistic title={t('额度净调整', 'Net adjustments')} value={s ? money(s.adjustment_net) : '-'} /></Card></Col>
        <Col xs={12} md={8}>
          <Card size="small">
            <Statistic title={t('输入 tokens', 'Input tokens')} value={s ? tokens(s.input_tokens) : '-'} />
          </Card>
        </Col>
        <Col xs={12} md={8}>
          <Card size="small">
            <Statistic title={t('输出 tokens', 'Output tokens')} value={s ? tokens(s.output_tokens) : '-'} />
          </Card>
        </Col>
      </Row>

      {filter.type !== 'request' ? <Table<ActivityEntry>
        rowKey="event_key"
        size="small"
        loading={loading}
        dataSource={data?.logs}
        onRow={(entry) => ({ onClick: () => setDetail(entry), style: { cursor: 'pointer' } })}
        pagination={{ current: page, pageSize, total: data?.total ?? 0, onChange: setPage, showSizeChanger: false }}
        locale={{ emptyText: t('这段时间没有日志记录', 'No logs in this period') }}
        columns={[
          { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v) => <span className="nowrap">{dayjs(v).format('MM-DD HH:mm:ss')}</span> },
          ...(admin ? [{ title: t('用户', 'User'), dataIndex: 'username' }] : []),
          { title: t('类型', 'Type'), render: (_, entry) => <Tag color={entry.type === 'request' ? 'blue' : entry.type === 'topup' ? 'green' : 'orange'}>{typeLabel(entry.type)}</Tag> },
          { title: t('事件摘要', 'Summary'), render: (_, entry) => entry.type === 'request' ? <><span className="mono">{entry.model}</span><div className="muted">{entry.key_name || '—'}</div></> : entry.type === 'topup' ? entry.order?.provider || t('在线充值', 'Online top-up') : entry.note || t('额度调整', 'Balance adjustment') },
          { title: t('状态', 'Status'), render: (_, entry) => <Tag color={statusTag[entry.status][0]}>{statusTag[entry.status][1]}</Tag> },
          { title: t('余额变动', 'Balance change'), align: 'right', render: (_, entry) => <span style={{ color: Number(entry.balance_delta) < 0 ? '#ff4d4f' : Number(entry.balance_delta) > 0 ? '#52c41a' : undefined }}>{Number(entry.balance_delta) > 0 ? '+' : ''}{money(entry.balance_delta)}</span> },
          { title: t('调整后余额', 'Balance after'), align: 'right', render: (_, entry) => entry.type === 'request' ? '—' : money(entry.balance_after) },
          { title: t('操作人', 'Operator'), render: (_, entry) => entry.type === 'request' ? '—' : operatorLabel(entry) },
        ]}
      /> : <Table<RequestActivity>
        rowKey="event_key"
        size="small"
        loading={loading}
        dataSource={data?.logs.filter((entry): entry is RequestActivity => entry.type === 'request')}
        onRow={(r) => ({ onClick: () => setDetail(r), style: { cursor: 'pointer' } })}
        pagination={{ current: page, pageSize, total: data?.total ?? 0, onChange: setPage, showSizeChanger: false }}
        locale={{ emptyText: t('这段时间没有日志记录', 'No logs in this period') }}
        columns={[
          { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v) => <span className="nowrap">{dayjs(v).format('MM-DD HH:mm:ss')}</span> },
          ...(admin ? [{ title: t('用户', 'User'), dataIndex: 'username' }] : []),
          { title: 'Key', dataIndex: 'key_name' },
          { title: t('模型', 'Model'), dataIndex: 'model', render: (v: string) => <span className="mono">{v}</span> },
          ...(admin ? [{ title: t('渠道', 'Channel'), dataIndex: 'channel_name', render: (v: string, r: LogEntry) => <>{v}{r.attempts > 1 && <Tag style={{ marginLeft: 4 }}>{t(`试了 ${r.attempts} 次`, `${r.attempts} tries`)}</Tag>}</> }] : []),
          {
            title: t('状态', 'Status'),
            dataIndex: 'status',
            render: (v: LogEntry['status'], r) => (
              <Tooltip title={r.error || undefined}>
                <Tag color={statusTag[v][0]}>{statusTag[v][1]}</Tag>
              </Tooltip>
            ),
          },
          {
            title: t('输入 / 缓存 / 输出', 'In / cache / out'),
            render: (_, r) => (
              <span className="nowrap">
                {tokens(r.input_tokens)} / {tokens(r.cache_read_tokens + r.cache_write_tokens)} / {tokens(r.output_tokens)}
                {r.usage_estimated && (
                  <Tooltip title={t('上游没返回用量，按内容估算', 'Upstream returned no usage; estimated from content')}>
                    <Tag style={{ marginLeft: 4 }}>{t('估算', 'est.')}</Tag>
                  </Tooltip>
                )}
              </span>
            ),
          },
          { title: t('首字 / 总耗时', 'First byte / total'), render: (_, r) => <span className="nowrap">{(r.first_byte_ms / 1000).toFixed(1)}s / {(r.total_ms / 1000).toFixed(1)}s</span> },
          { title: t('花费', 'Cost'), dataIndex: 'cost', align: 'right', render: (v) => <span className="nowrap">{money(v, 6)}</span> },
        ]}
      />}

      <Drawer open={!!detail} onClose={() => setDetail(null)} title={detail?.type === 'request' ? t('请求详情', 'Request details') : t('资金变动详情', 'Balance details')} size={520}>
        {detail && detail.type !== 'request' && (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label={t('时间', 'Time')}>{dayjs(detail.created_at).format('YYYY-MM-DD HH:mm:ss')}</Descriptions.Item>
            <Descriptions.Item label={t('用户', 'User')}>{detail.username}</Descriptions.Item>
            <Descriptions.Item label={t('类型', 'Type')}>{typeLabel(detail.type)}</Descriptions.Item>
            <Descriptions.Item label={t('余额变动', 'Balance change')}>{money(detail.balance_delta, 10)}</Descriptions.Item>
            <Descriptions.Item label={t('调整后余额', 'Balance after')}>{money(detail.balance_after, 10)}</Descriptions.Item>
            <Descriptions.Item label={t('操作人', 'Operator')}>{operatorLabel(detail)}</Descriptions.Item>
            <Descriptions.Item label={t('备注', 'Note')}>{detail.note || '—'}</Descriptions.Item>
            {detail.order && <>
              <Descriptions.Item label={t('订单号', 'Order ID')}><Typography.Text copyable>{detail.order.id}</Typography.Text></Descriptions.Item>
              <Descriptions.Item label={t('支付服务 / 方式', 'Provider / method')}>{detail.order.provider} / {detail.order.method || '—'}</Descriptions.Item>
              <Descriptions.Item label={t('付款报价', 'Payment quote')}>{detail.order.amount} {detail.order.currency}</Descriptions.Item>
              <Descriptions.Item label={t('税费', 'Tax')}>{detail.order.tax} {detail.order.currency}</Descriptions.Item>
              <Descriptions.Item label={t('实际支付合计', 'Total paid')}>{detail.order.total} {detail.order.currency}</Descriptions.Item>
              <Descriptions.Item label={t('完成时间', 'Completed')}>{detail.order.completed_at ? dayjs(detail.order.completed_at).format('YYYY-MM-DD HH:mm:ss') : '—'}</Descriptions.Item>
            </>}
          </Descriptions>
        )}
        {detail?.type === 'request' && (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label={t('请求 ID', 'Request ID')}>
              <Typography.Text copyable className="mono">{detail.request_id}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('时间', 'Time')}>{dayjs(detail.created_at).format('YYYY-MM-DD HH:mm:ss')}</Descriptions.Item>
            {admin && <Descriptions.Item label={t('用户', 'User')}>{detail.username}</Descriptions.Item>}
            <Descriptions.Item label="Key">{detail.key_name}</Descriptions.Item>
            <Descriptions.Item label={t('服务分组 / 倍率', 'Service group / ratio')}>{detail.group_name} ×{Number(detail.group_ratio)}</Descriptions.Item>
            <Descriptions.Item label={t('协议', 'Protocol')}>{detail.protocol}{detail.stream ? t('（流式）', ' (stream)') : ''}</Descriptions.Item>
            <Descriptions.Item label={t('模型', 'Model')}>{detail.model}</Descriptions.Item>
            {admin && <Descriptions.Item label={t('渠道 / 上游模型', 'Channel / upstream model')}>{detail.channel_name || '-'} / {detail.upstream_model || '-'}</Descriptions.Item>}
            {admin && <Descriptions.Item label={t('尝试次数', 'Attempts')}>{detail.attempts}</Descriptions.Item>}
            <Descriptions.Item label={t('状态', 'Status')}>
              <Tag color={statusTag[detail.status][0]}>{statusTag[detail.status][1]}</Tag> HTTP {detail.http_status || '-'}
            </Descriptions.Item>
            {detail.error && (
              <Descriptions.Item label={t('错误', 'Error')}>
                <Typography.Text type="danger" style={{ wordBreak: 'break-all' }}>{detail.error}</Typography.Text>
              </Descriptions.Item>
            )}
            <Descriptions.Item label={t('输入', 'Input')}>{detail.input_tokens.toLocaleString()}</Descriptions.Item>
            <Descriptions.Item label={t('缓存命中 / 写入', 'Cache hit / write')}>
              {detail.cache_read_tokens.toLocaleString()} / {detail.cache_write_tokens.toLocaleString()}
            </Descriptions.Item>
            <Descriptions.Item label={t('输出（含推理）', 'Output (incl. reasoning)')}>
              {detail.output_tokens.toLocaleString()}（{t('推理', 'reasoning')} {detail.reasoning_tokens.toLocaleString()}）
            </Descriptions.Item>
            <Descriptions.Item label={t('首字 / 总耗时', 'First byte / total')}>{detail.first_byte_ms} ms / {detail.total_ms} ms</Descriptions.Item>
            <Descriptions.Item label={t('计价', 'Pricing')}>
              {detail.price_rule ? <Tag color="purple">{detail.price_rule}</Tag> : t('基础价', 'Base price')}
              <span className="muted" style={{ marginLeft: 8 }}>
                {t('输入', 'in')} {priceText(money, detail.input_price)} · {t('输出', 'out')} {priceText(money, detail.output_price)}
              </span>
            </Descriptions.Item>
            <Descriptions.Item label={t('花费', 'Cost')}>
              {money(detail.cost, 8)}
              {detail.usage_estimated && <Tag style={{ marginLeft: 6 }}>{t('估算', 'est.')}</Tag>}
            </Descriptions.Item>
          </Descriptions>
        )}
      </Drawer>
    </div>
  )
}
