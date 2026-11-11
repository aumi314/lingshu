import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Alert, Button, Card, Col, DatePicker, Descriptions, Drawer, Row, Segmented, Select, Statistic, Table, Tabs, Tag, Tooltip, Typography } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import { useSearchParams } from 'react-router-dom'
import { get, type LogEntry, type ActivityLogsResponse, type OperationLogsResponse, type RequestActivity, type User } from '../api'
import { priceText, tokens, useApp } from '../app'
import { tierText } from '../pricing'
import OperationLogTable, { targetLabel, targetTypes } from './OperationLogs'
import Conversation, { type ConversationData } from '../components/Conversation'

type Tab = 'calls' | 'operations'
type Filter = { range: [Dayjs, Dayjs]; model?: string; status?: string; user_id?: number; target?: string }

const seconds = (ms: number) => `${(ms / 1000).toFixed(1)}s`
// 汇总卡片的数字：6 张排一行时“448.80M / 1.22M”这类也不折行
const statValue = { fontSize: 20, whiteSpace: 'nowrap' as const }

// 日志的快捷时间段：今日、本周、本月、近 7 天、近 30 天（都截到今天结束）
type Translate = (zh: string, en: string) => string
const quickRanges = (): { key: string; label: (t: Translate) => string; range: [Dayjs, Dayjs] }[] => {
  const end = dayjs().endOf('day')
  return [
    { key: 'today', label: (t) => t('今日', 'Today'), range: [dayjs().startOf('day'), end] },
    { key: 'week', label: (t) => t('本周', 'This week'), range: [dayjs().startOf('week'), end] },
    { key: 'month', label: (t) => t('本月', 'This month'), range: [dayjs().startOf('month'), end] },
    { key: '7d', label: (t) => t('近 7 天', 'Last 7 days'), range: [dayjs().subtract(6, 'day').startOf('day'), end] },
    { key: '30d', label: (t) => t('近 30 天', 'Last 30 days'), range: [dayjs().subtract(29, 'day').startOf('day'), end] },
  ]
}
const secs2 = (ms: number) => `${(ms / 1000).toFixed(2)}s`
const protocolLabel: Record<string, string> = { chat: 'Chat', messages: 'Messages', responses: 'Responses', systemone: 'System One' }
// 转接时（Responses 入口 → Messages 上游）把上游协议也标出来，一眼看出这次请求实际走了哪种协议
const protocolText = (r: LogEntry) =>
  r.upstream_protocol && r.upstream_protocol !== r.protocol
    ? `${protocolLabel[r.protocol] ?? r.protocol} → ${protocolLabel[r.upstream_protocol] ?? r.upstream_protocol}`
    : (protocolLabel[r.protocol] ?? r.protocol)
const operationText = (r: LogEntry, t: Translate) =>
  r.operation === 'compaction' ? t('上下文压缩', 'Compaction') : r.operation === 'generation' ? t('正常生成', 'Generation') : r.operation || '—'
// 缓存命中率 = 缓存读 ÷（输入 + 缓存读 + 缓存写），向下取整，避免没全中却显示 100%
const cacheRate = (read: number, total: number) => total > 0 ? `${Math.floor((read * 100) / total)}%` : '—'

// 首字从发给上游算起（和上游自身的响应速度对应），不含读体和网关处理；旧日志没有发出时刻，按旧口径从进入网关算
const firstToken = (r: LogEntry) => r.first_byte_ms - (r.upstream_start_ms || 0)

// 输出速度 = 可见输出 ÷ 首字之后的传输时间。推理 token 在首字前的等待里生成、测不到时间，不算进分子。
// 上游攒好整段一次发出时（传输不足 1 秒）测不出生成速度，可见输出太少时也不稳定，都不给。
function outputTPS(r: LogEntry): number | null {
  const streaming = r.total_ms - r.first_byte_ms
  const visible = r.output_tokens - r.reasoning_tokens
  if (!r.stream || r.first_byte_ms <= 0 || streaming < 1000 || visible < 50) return null
  return Math.round((visible * 1000) / streaming)
}

export default function Logs({ admin = false }: { admin?: boolean }) {
  const { money, t } = useApp()
  const [params, setParams] = useSearchParams()
  // 只有调用日志和操作日志两类（2026-11-06）：充值并入操作日志，旧链接 ?tab=orders 落到操作日志
  const tab: Tab = params.get('tab') === 'operations' || params.get('tab') === 'orders' ? 'operations' : 'calls'
  const [filter, setFilter] = useState<Filter>({ range: [dayjs().subtract(6, 'day').startOf('day'), dayjs().endOf('day')] })
  // 当前高亮的快捷时间段：点了哪个亮哪个（本月和近 7 天在月初几天可能是同一段，不能靠日期反推）；手动改日期时再按日期匹配
  const [quick, setQuick] = useState('7d')
  const [page, setPage] = useState(1)
  const [data, setData] = useState<ActivityLogsResponse | null>(null)
  const [ops, setOps] = useState<OperationLogsResponse | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [detail, setDetail] = useState<RequestActivity | null>(null)
  // 对话留存（只有管理员能看）：打开请求详情时取回这次调用的请求和回复
  const [conversation, setConversation] = useState<ConversationData | null>(null)
  const [conversationError, setConversationError] = useState('')
  const [conversationRetry, setConversationRetry] = useState(0)
  useEffect(() => {
    setConversation(null)
    setConversationError('')
    if (!admin || !detail) return
    let live = true
    get<ConversationData>(`/api/admin/logs/${detail.id}/conversation`).then((c) => live && setConversation(c)).catch((e) => {
      if (live) setConversationError(e instanceof Error ? e.message : String(e))
    })
    return () => {
      live = false
    }
  }, [admin, detail, conversationRetry])
  // 管理员的请求详情分两页：明细和对话内容（对话长，放在明细下面要滚很远）
  const withConversation = (details: ReactNode) =>
    !admin || !detail ? details : (
      <Tabs
        items={[
          { key: 'details', label: t('详情', 'Details'), children: details },
          {
            key: 'conversation',
            label: t('对话内容', 'Conversation'),
            children: conversationError ? (
              <Alert type="error" showIcon title={t('读取失败，请重试', 'Could not load conversation. Try again.')} description={conversationError}
                action={<Button size="small" onClick={() => setConversationRetry((n) => n + 1)}>{t('重试', 'Retry')}</Button>} />
            ) : conversation ? <Conversation protocol={detail.protocol} requestID={detail.request_id} data={conversation} /> : <Typography.Text type="secondary">{t('加载中…', 'Loading…')}</Typography.Text>,
          },
        ]}
      />
    )
  const pageSize = 50
  const requestSequence = useRef(0)

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
      type: tab === 'calls' ? 'request' : 'operation',
      page: String(page),
      page_size: String(pageSize),
      from: filter.range[0].toISOString(),
      to: filter.range[1].toISOString(),
    })
    if (tab === 'calls' && filter.model) q.set('model', filter.model)
    if (tab === 'calls' && filter.status) q.set('status', filter.status)
    if (tab === 'operations' && filter.target) q.set('target', filter.target)
    if (filter.user_id) q.set('user_id', String(filter.user_id))
    const path = `${admin ? '/api/admin/logs' : '/api/me/logs'}?${q}`
    try {
      if (tab === 'calls') {
        const result = await get<ActivityLogsResponse>(path)
        if (sequence === requestSequence.current) { setData(result); setError('') }
      } else {
        const result = await get<OperationLogsResponse>(path)
        if (sequence === requestSequence.current) { setOps(result); setError('') }
      }
    } catch (e) {
      if (sequence === requestSequence.current) { setData(null); setOps(null); setError((e as Error).message) }
    } finally {
      if (sequence === requestSequence.current) setLoading(false)
    }
  }, [admin, filter, page, tab])

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

  const switchTab = (key: string) => {
    setPage(1)
    setData(null)
    setOps(null)
    setFilter((old) => ({ ...old, model: undefined, status: undefined, target: undefined }))
    setParams(key === 'calls' ? {} : { tab: key }, { replace: true })
  }

  const s = data?.summary
  const pagination = { current: page, pageSize, total: (tab === 'calls' ? data?.total : ops?.total) ?? 0, onChange: setPage, showSizeChanger: false }
  const time = { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v: string) => <span className="nowrap">{dayjs(v).format('MM-DD HH:mm:ss')}</span> }

  const calls = (
    <>
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={8} xl={4}><Card size="small"><Statistic styles={{ content: statValue }} title={t('花费', 'Cost')} value={s ? money(s.cost) : '-'} /></Card></Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic styles={{ content: statValue }} title={t('请求数', 'Requests')} value={s?.requests ?? '-'} suffix={s?.failed ? <Typography.Text type="danger" style={{ fontSize: 14 }}>{s.failed} {t('失败', 'failed')}</Typography.Text> : null} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small"><Statistic styles={{ content: statValue }} title={t('输入 / 输出 tokens', 'Input / output tokens')} value={s ? `${tokens(s.input_tokens)} / ${tokens(s.output_tokens)}` : '-'} /></Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Tooltip title={t('缓存命中的输入占全部输入的比例，越高越省钱', 'Share of input tokens served from cache; higher means cheaper')}>
              <Statistic styles={{ content: statValue }} title={t('缓存命中率', 'Cache hit rate')} value={s ? cacheRate(s.cache_read_tokens, s.input_tokens) : '-'} />
            </Tooltip>
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Tooltip title={
              <>
                {t('这段时间里最忙的那一分钟：请求数 / tokens（输入含缓存 + 输出）。用来对照上游的限流额度；按整段时间平均没有意义。', 'The busiest minute in this range: requests / tokens (input incl. cache + output). Compare with upstream rate limits; a range average is meaningless.')}
                {s?.peak_rpm_at && <div>{t('请求峰值', 'Request peak')}：{dayjs(s.peak_rpm_at).format('MM-DD HH:mm')}</div>}
                {s?.peak_tpm_at && <div>{t('token 峰值', 'Token peak')}：{dayjs(s.peak_tpm_at).format('MM-DD HH:mm')}</div>}
              </>
            }>
              <Statistic styles={{ content: statValue }} title={t('峰值 RPM / TPM', 'Peak RPM / TPM')} value={s ? `${s.peak_rpm ?? 0} / ${tokens(s.peak_tpm ?? 0)}` : '-'} />
            </Tooltip>
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Tooltip title={t('成功请求的中位数：首字从发给上游算起（只算流式），总耗时是整个请求。用中位数是因为推理请求偶尔几分钟，平均值会被少数长请求拉高。', 'Medians of successful requests: first token from sending upstream (streaming only), and total time. Medians, because occasional minutes-long reasoning requests skew the mean.')}>
              <Statistic styles={{ content: statValue }} title={t('首字 · 总耗时（中位数）', 'First token · total (median)')} value={s ? `${s.median_first_token_ms != null ? seconds(s.median_first_token_ms) : '—'} · ${s.median_total_ms != null ? seconds(s.median_total_ms) : '—'}` : '-'} />
            </Tooltip>
          </Card>
        </Col>
      </Row>
      <Table<RequestActivity>
        rowKey="event_key"
        size="small"
        loading={loading}
        dataSource={data?.logs.filter((entry): entry is RequestActivity => entry.type === 'request')}
        onRow={(r) => ({ onClick: () => setDetail(r), style: { cursor: 'pointer' } })}
        pagination={pagination}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('这段时间没有调用记录', 'No calls in this period') }}
        columns={[
          time,
          ...(admin ? [{ title: t('用户', 'User'), dataIndex: 'username', render: (v: string, r: LogEntry) => <div className="nowrap">{v}{r.client_ip && <div className="muted mono">{r.client_ip}</div>}</div> }] : []),
          { title: 'Key', dataIndex: 'key_name', render: (v: string) => v || '—' },
          { title: t('服务分组', 'Service group'), dataIndex: 'group_name', render: (v: string) => v || '—' },
          {
            title: t('请求', 'Request'),
            render: (_, r) => (
              <div className="nowrap">
                <span className="mono">{r.model}</span>
                {r.service_tier === 'fast' && <Tag color="orange" style={{ marginLeft: 6 }}>Fast</Tag>}
                {r.operation === 'compaction' && <Tag color="purple" style={{ marginLeft: 6 }}>{t('压缩', 'Compaction')}</Tag>}
                <div className="muted">{[protocolText(r), r.stream ? t('流式', 'stream') : t('非流式', 'non-stream'), r.reasoning_effort, r.service_tier !== 'fast' && tierText(r.service_tier, t)].filter(Boolean).join(' · ')}</div>
              </div>
            ),
          },
          ...(admin ? [{ title: t('渠道', 'Channel'), dataIndex: 'channel_name', render: (v: string, r: LogEntry) => <>{v || '—'}{r.attempts > 1 && <Tag style={{ marginLeft: 4 }}>{t(`试了 ${r.attempts} 次`, `${r.attempts} tries`)}</Tag>}</> }] : []),
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
            title: t('输入 / 输出', 'In / out'),
            render: (_, r) => {
              const input = r.input_tokens + r.cache_read_tokens + r.cache_write_tokens
              return (
                <div className="nowrap">
                  {tokens(input)} / {tokens(r.output_tokens)}
                  {r.usage_estimated && (
                    <Tooltip title={t('上游没返回用量，按内容估算', 'Upstream returned no usage; estimated from content')}>
                      <Tag style={{ marginLeft: 4 }}>{t('估算', 'est.')}</Tag>
                    </Tooltip>
                  )}
                  {input > 0 && <div className="muted">{t('缓存', 'Cache')} {cacheRate(r.cache_read_tokens, input)}</div>}
                </div>
              )
            },
          },
          {
            title: t('首字 · 速度', 'First token · speed'),
            render: (_, r) => {
              const tps = outputTPS(r)
              const first = r.stream && r.first_byte_ms > 0 && r.status !== 'upstream_error' && r.status !== 'rejected' ? seconds(firstToken(r)) : '—'
              return (
                <div className="nowrap">
                  <div>{first}{tps !== null && ` · ${tps} tok/s`}</div>
                  {/* 读体超过 0.5 秒才提示：多半是用户上行慢、上下文大 */}
                  <div className="muted">{t('总耗时', 'Total')} {seconds(r.total_ms)}{(r.read_ms ?? 0) >= 500 && ` · ${t('读体', 'upload')} ${seconds(r.read_ms!)}`}</div>
                </div>
              )
            },
          },
          { title: t('花费', 'Cost'), dataIndex: 'cost', align: 'right', render: (v) => <span className="nowrap">{money(v, 6)}</span> },
        ]}
      />
    </>
  )

  const os = ops?.summary
  const operations = (
    <>
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}><Card size="small"><Statistic title={t('充值到账', 'Top-up credits')} value={os ? money(os.topup_total) : '-'} /></Card></Col>
        <Col xs={12} md={6}><Card size="small"><Statistic title={t('额度净调整', 'Net adjustments')} value={os ? money(os.adjustment_net) : '-'} /></Card></Col>
      </Row>
      <OperationLogTable logs={ops?.logs} loading={loading} pagination={pagination} />
    </>
  )

  return (
    <div className="page" style={{ maxWidth: 1400 }}>
      <Typography.Title level={3} className="page-title">
        {admin ? t('全站日志', 'All logs') : t('账户日志', 'Account logs')}
      </Typography.Title>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}

      <Tabs activeKey={tab} onChange={switchTab} items={[{ key: 'calls', label: t('调用日志', 'Call logs') }, { key: 'operations', label: t('操作日志', 'Operation logs') }]} />

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 16 }}>
        <DatePicker.RangePicker
          value={filter.range}
          allowClear={false}
          onChange={(v) => {
            if (!v || !v[0] || !v[1]) return
            const range: [Dayjs, Dayjs] = [v[0].startOf('day'), v[1].endOf('day')]
            setQuick(quickRanges().find((q) => q.range[0].isSame(range[0], 'minute') && q.range[1].isSame(range[1], 'minute'))?.key ?? '')
            update({ range })
          }}
        />
        {/* 快捷时间段放在日期旁边直接可见；选了别的日期时高亮自动取消。本周从周一算起（中文界面 dayjs 的周起始） */}
        <Segmented
          style={{ maxWidth: '100%', overflowX: 'auto' }}
          value={quick}
          onChange={(k) => {
            const q = quickRanges().find((x) => x.key === k)
            if (q) {
              setQuick(q.key)
              update({ range: q.range })
            }
          }}
          options={quickRanges().map((q) => ({ value: q.key, label: q.label(t) }))}
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
        {tab === 'calls' && <>
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
        {/* 刷新：保留当前筛选和页码，重新拉列表和汇总 */}
        <Button loading={loading} onClick={() => void load()}>{t('刷新', 'Refresh')}</Button>
        </>}
        {tab === 'operations' && (
          <Select
            allowClear
            placeholder={t('全部对象', 'All targets')}
            style={{ width: 160 }}
            value={filter.target}
            onChange={(v) => update({ target: v })}
            options={targetTypes.filter((type) => admin || ['user', 'api_key', 'balance', 'topup'].includes(type)).map((type) => ({ value: type, label: targetLabel(t, type) }))}
          />
        )}
      </div>

      {tab === 'calls' ? calls : operations}

      <Drawer open={!!detail} onClose={() => setDetail(null)} title={t('请求详情', 'Request details')} size={admin ? 720 : 520}>
        {detail && withConversation(
          <Descriptions column={1} size="small" bordered>
            <Descriptions.Item label={t('请求 ID', 'Request ID')}>
              <Typography.Text copyable className="mono">{detail.request_id}</Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label={t('时间', 'Time')}>{dayjs(detail.created_at).format('YYYY-MM-DD HH:mm:ss')}</Descriptions.Item>
            {admin && <Descriptions.Item label={t('用户', 'User')}>{detail.username}</Descriptions.Item>}
            <Descriptions.Item label={t('访问者 IP', 'Client IP')}><span className="mono">{detail.client_ip || '—'}</span></Descriptions.Item>
            <Descriptions.Item label="Key">{detail.key_name}</Descriptions.Item>
            <Descriptions.Item label={t('服务分组 / 倍率', 'Service group / ratio')}>{detail.group_name} ×{Number(detail.group_ratio)}</Descriptions.Item>
            <Descriptions.Item label={t('协议', 'Protocol')}>
              {protocolText(detail)}{detail.stream ? t('（流式）', ' (stream)') : ''}
              {detail.upstream_protocol && detail.upstream_protocol !== detail.protocol && (
                <Tooltip title={t('客户端走 Responses，上游只支持 Messages，网关自动转接。', 'The client spoke Responses and the upstream only speaks Messages; the gateway converted it.')}>
                  <Tag color="cyan" style={{ marginLeft: 6 }}>{t('转接', 'bridged')}</Tag>
                </Tooltip>
              )}
            </Descriptions.Item>
            <Descriptions.Item label={t('用途', 'Operation')}>{operationText(detail, t)}</Descriptions.Item>
            {(detail.conversion_diagnostics?.length ?? 0) > 0 && (
              <Descriptions.Item label={t('转接诊断', 'Conversion diagnostics')}>
                <div className="mono" style={{ fontSize: 12, wordBreak: 'break-all' }}>
                  {JSON.stringify(detail.conversion_diagnostics).slice(0, 500)}
                </div>
              </Descriptions.Item>
            )}
            <Descriptions.Item label={t('推理强度', 'Reasoning effort')}>{detail.reasoning_effort || '—'}</Descriptions.Item>
            {detail.service_tier && <Descriptions.Item label={t('速度档位', 'Speed tier')}>{tierText(detail.service_tier, t)}</Descriptions.Item>}
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
            {detail.upstream_start_ms ? <>
              <Descriptions.Item label={t('读体', 'Request upload')}>{secs2(detail.read_ms ?? 0)}<span className="muted">{t('（接收客户端请求体）', ' (receiving the request body)')}</span></Descriptions.Item>
              <Descriptions.Item label={t('网关处理', 'Gateway')}>{secs2(detail.upstream_start_ms - (detail.read_ms ?? 0))}<span className="muted">{t('（鉴权、选渠道、预扣、换渠道重试）', ' (auth, routing, hold, retries)')}</span></Descriptions.Item>
              {detail.first_byte_ms > 0 && <Descriptions.Item label={t('首字', 'First token')}>{secs2(firstToken(detail))}<span className="muted">{t('（发给上游后）', ' (after sending upstream)')}</span></Descriptions.Item>}
              <Descriptions.Item label={t('转发', 'Upstream')}>{secs2(detail.total_ms - detail.upstream_start_ms)}<span className="muted">{t('（发给上游到传完）', ' (send to finish)')}</span></Descriptions.Item>
              <Descriptions.Item label={t('总耗时', 'Total')}>{secs2(detail.total_ms)}</Descriptions.Item>
            </> : <Descriptions.Item label={t('首字 / 总耗时', 'First byte / total')}>{detail.first_byte_ms} ms / {detail.total_ms} ms</Descriptions.Item>}
            <Descriptions.Item label={t('计价', 'Pricing')}>
              {detail.price_rule ? <Tag color="purple">{detail.price_rule}</Tag> : t('基础价', 'Base price')}
              {detail.service_tier === 'fast' && <Tag color="orange">{t('Fast 价', 'Fast price')}</Tag>}
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
