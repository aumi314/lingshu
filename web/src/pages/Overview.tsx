import { useEffect, useState } from 'react'
import { Alert, Card, Col, Row, Statistic, Table, Typography } from 'antd'
import dayjs from 'dayjs'
import { get, type LogsResponse } from '../api'
import { tokens, useApp } from '../app'

export default function Overview() {
  const { user, money, t } = useApp()
  const [today, setToday] = useState<LogsResponse | null>(null)
  const [month, setMonth] = useState<LogsResponse | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const q = (from: dayjs.Dayjs) => get<LogsResponse>(`/api/me/logs?page_size=1&from=${encodeURIComponent(from.toISOString())}`)
    Promise.all([q(dayjs().startOf('day')), q(dayjs().startOf('month'))])
      .then(([d, m]) => {
        setToday(d)
        setMonth(m)
      })
      .catch((e) => setError(e.message))
  }, [])

  const balance = Number(user?.balance ?? 0)
  const base = window.location.origin
  const endpoints = [
    { protocol: 'OpenAI Chat Completions', url: `${base}/v1/chat/completions`, sdk: `${base}/v1` },
    { protocol: 'OpenAI Responses', url: `${base}/v1/responses`, sdk: `${base}/v1` },
    { protocol: 'Anthropic Messages', url: `${base}/v1/messages`, sdk: base },
  ]

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('概览', 'Overview')}
      </Typography.Title>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      {balance <= 0 && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          title={t('余额不足，调用会被拒绝。请联系管理员充值。', 'Your balance is used up, so requests will be rejected. Contact an admin to top up.')}
        />
      )}
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={8}>
          <Card>
            <Statistic title={t('余额', 'Balance')} value={money(balance)} styles={{ content: balance <= 0 ? { color: '#ff4d4f' } : undefined }} />
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card>
            <Statistic title={t('今日花费', 'Spent today')} value={today ? money(today.summary.cost) : '-'} />
            <Typography.Text type="secondary">
              {today ? `${today.summary.requests} ${t('次请求', 'requests')} · ${tokens(today.summary.input_tokens + today.summary.output_tokens)} tokens` : ' '}
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card>
            <Statistic title={t('本月花费', 'Spent this month')} value={month ? money(month.summary.cost) : '-'} />
            <Typography.Text type="secondary">
              {month ? `${month.summary.requests} ${t('次请求', 'requests')} · ${tokens(month.summary.input_tokens + month.summary.output_tokens)} tokens` : ' '}
            </Typography.Text>
          </Card>
        </Col>
      </Row>

      <Card title={t('接入地址', 'Endpoints')} style={{ marginTop: 16 }}>
        <Typography.Paragraph type="secondary">
          {t(
            '用灵枢的 API key 代替官方 key，把地址换成下面的地址即可。SDK 里填写"SDK 地址"那一列。',
            'Use your Lingshu API key in place of the vendor key, and point your client at these endpoints. SDKs take the "SDK base URL" column.',
          )}
        </Typography.Paragraph>
        <Table
          rowKey="protocol"
          pagination={false}
          size="small"
          dataSource={endpoints}
          columns={[
            { title: t('协议', 'Protocol'), dataIndex: 'protocol' },
            { title: t('接口地址', 'Endpoint'), dataIndex: 'url', render: (v) => <Typography.Text copyable className="mono">{v}</Typography.Text> },
            { title: t('SDK 地址', 'SDK base URL'), dataIndex: 'sdk', render: (v) => <Typography.Text copyable className="mono">{v}</Typography.Text> },
          ]}
        />
      </Card>
    </div>
  )
}
