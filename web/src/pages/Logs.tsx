import { useCallback, useEffect, useState } from 'react'
import { Alert, Card, Col, DatePicker, Descriptions, Drawer, Row, Select, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import { get, type LogEntry, type LogsResponse, type User } from '../api'
import { priceText, tokens, useApp } from '../app'

type Filter = { range: [Dayjs, Dayjs]; model?: string; status?: string; user_id?: number }

export default function Logs({ admin = false }: { admin?: boolean }) {
  const { money, t } = useApp()
  const [filter, setFilter] = useState<Filter>({ range: [dayjs().subtract(6, 'day').startOf('day'), dayjs().endOf('day')] })
  const [page, setPage] = useState(1)
  const [data, setData] = useState<LogsResponse | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [detail, setDetail] = useState<LogEntry | null>(null)
  const pageSize = 50

  const statusTag: Record<LogEntry['status'], [string, string]> = {
    success: ['green', t('成功', 'Success')],
    client_cancelled: ['blue', t('客户端断开', 'Client cancelled')],
    upstream_error: ['red', t('上游出错', 'Upstream error')],
    rejected: ['orange', t('被拒绝', 'Rejected')],
  }

  const load = useCallback(async () => {
    setLoading(true)
    const q = new URLSearchParams({
      page: String(page),
      page_size: String(pageSize),
      from: filter.range[0].toISOString(),
      to: filter.range[1].toISOString(),
    })
    if (filter.model) q.set('model', filter.model)
    if (filter.status) q.set('status', filter.status)
    if (filter.user_id) q.set('user_id', String(filter.user_id))
    try {
      setData(await get<LogsResponse>(`${admin ? '/api/admin/logs' : '/api/me/logs'}?${q}`))
      setError('')
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [admin, filter, page])

  useEffect(() => {
    load()
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
        {admin ? t('全站日志', 'All logs') : t('使用记录', 'Usage')}
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
      </div>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}>
          <Card size="small">
            <Statistic title={t('花费', 'Cost')} value={s ? money(s.cost) : '-'} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small">
            <Statistic title={t('请求数', 'Requests')} value={s?.requests ?? '-'} suffix={s?.failed ? <Typography.Text type="danger" style={{ fontSize: 14 }}>{s.failed} {t('失败', 'failed')}</Typography.Text> : null} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small">
            <Statistic title={t('输入 tokens', 'Input tokens')} value={s ? tokens(s.input_tokens) : '-'} />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small">
            <Statistic title={t('输出 tokens', 'Output tokens')} value={s ? tokens(s.output_tokens) : '-'} />
          </Card>
        </Col>
      </Row>

      <Table
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={data?.logs}
        onRow={(r) => ({ onClick: () => setDetail(r), style: { cursor: 'pointer' } })}
        pagination={{ current: page, pageSize, total: s?.requests ?? 0, onChange: setPage, showSizeChanger: false }}
        locale={{ emptyText: t('这段时间没有请求记录', 'No requests in this period') }}
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
      />

      <Drawer open={!!detail} onClose={() => setDetail(null)} title={t('请求详情', 'Request details')} size={520}>
        {detail && (
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label={t('请求 ID', 'Request ID')}>
              <Typography.Text copyable className="mono">{detail.request_id}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('时间', 'Time')}>{dayjs(detail.created_at).format('YYYY-MM-DD HH:mm:ss')}</Descriptions.Item>
            {admin && <Descriptions.Item label={t('用户', 'User')}>{detail.username}</Descriptions.Item>}
            <Descriptions.Item label="Key">{detail.key_name}</Descriptions.Item>
            <Descriptions.Item label={t('分组 / 倍率', 'Group / ratio')}>{detail.group_name} ×{Number(detail.group_ratio)}</Descriptions.Item>
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
