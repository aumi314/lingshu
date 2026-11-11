import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Row, Space, Statistic, Tabs, Tag, Typography } from 'antd'
import { CheckCircleFilled, ExportOutlined, NotificationOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { Link } from 'react-router-dom'
import { get, type APIKey, type LogsResponse } from '../api'
import { tokens, useApp } from '../app'
import { guideClients, magpieImportURL, type GuideURLs } from '../guide'

const LOW_BALANCE_USD = 1 // 余额低于这个数提前提醒，免得调用突然被拒

export default function Overview() {
  const { user, money, t } = useApp()
  const [today, setToday] = useState<LogsResponse | null>(null)
  const [month, setMonth] = useState<LogsResponse | null>(null)
  const [keys, setKeys] = useState<APIKey[] | null>(null)
  const [topupOpen, setTopupOpen] = useState(false)
  const [apiBaseURL, setAPIBaseURL] = useState('')
  const [announcement, setAnnouncement] = useState('') // 管理员在主设置里填的公告，空就不显示
  const [error, setError] = useState('')
  const [showSteps, setShowSteps] = useState(false)

  useEffect(() => {
    const q = (from: dayjs.Dayjs) => get<LogsResponse>(`/api/me/logs?page_size=1&from=${encodeURIComponent(from.toISOString())}`)
    Promise.all([q(dayjs().startOf('day')), q(dayjs().startOf('month'))])
      .then(([d, m]) => {
        setToday(d)
        setMonth(m)
      })
      .catch((e) => setError(e.message))
    get<APIKey[]>('/api/me/keys').then(setKeys).catch(() => setKeys([]))
    get<{ enabled: boolean }>('/api/me/payment-methods').then((v) => setTopupOpen(v.enabled)).catch(() => {})
    get<{ api_base_url: string; announcement?: string }>('/api/me/endpoints').then((v) => { setAPIBaseURL(v.api_base_url); setAnnouncement(v.announcement ?? '') }).catch(() => {})
  }, [])

  const balance = Number(user?.balance ?? 0)
  const base = apiBaseURL || `${window.location.origin}/v1`
  const urls: GuideURLs = { base, root: base.replace(/\/v1\/?$/, '') }
  const topupAction = topupOpen
    ? <Link to="/console/wallet"><Button size="small" type="primary">{t('充值', 'Top up')}</Button></Link>
    : <Typography.Text type="secondary">{t('联系管理员充值', 'Ask an admin to top up')}</Typography.Text>

  // 每一步是否完成由系统判断，用户不用自己对照
  const hasKey = (keys?.length ?? 0) > 0
  const steps = [
    {
      done: balance > 0,
      title: t('确保有余额', 'Make sure you have balance'),
      detail: balance > 0 ? t(`当前余额 ${money(balance)}`, `Balance ${money(balance)}`) : topupOpen ? t('先充值，余额为零时调用会被拒绝', 'Top up first; calls are rejected at zero balance') : t('联系管理员充值，余额为零时调用会被拒绝', 'Ask an admin to top up; calls are rejected at zero balance'),
      action: balance > 0 ? null : topupAction,
    },
    {
      done: hasKey,
      title: t('选好模型和服务分组', 'Pick a model and service group'),
      detail: t('不同分组倍率不同，模型标签里能看到支持的协议', 'Groups differ in price ratio; tags show which protocols each model supports'),
      action: <Link to="/console/models"><Button size="small">{t('模型与价格', 'Models & pricing')}</Button></Link>,
    },
    {
      done: hasKey,
      title: t('创建并保存 API key', 'Create and save an API key'),
      detail: hasKey ? t(`已有 ${keys!.length} 个 key`, `${keys!.length} key(s)`) : t('完整的 key 只显示一次，创建后马上保存好', 'The full key is shown only once; save it right away'),
      action: <Link to="/console/keys"><Button size="small">API keys</Button></Link>,
    },
    {
      done: !!keys?.some((k) => k.last_used_at),
      title: t('接入你的工具', 'Connect your tool'),
      detail: t('在下方「接入」里，把提示词发给你的 Agent 自动配置，或用 Magpie 一键导入', 'Under Connect below, send the prompt to your agent to set itself up, or import into Magpie in one click'),
      action: null,
    },
  ]
  const doneCount = steps.filter((s) => s.done).length
  const allDone = keys !== null && doneCount === steps.length
  const codeBlock = (code: string) => (
    <div className="guide-code">
      <Typography.Paragraph copyable={{ text: code }} style={{ margin: 0 }}>
        <pre className="mono" style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{code}</pre>
      </Typography.Paragraph>
    </div>
  )

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('概览', 'Overview')}
      </Typography.Title>
      {announcement && <Alert type="info" showIcon icon={<NotificationOutlined />} title={<span style={{ whiteSpace: 'pre-wrap' }}>{linkEmails(announcement)}</span>} style={{ marginBottom: 16 }} />}
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}

      <Row gutter={[16, 16]}>
        <Col xs={24} sm={8}>
          <Card style={{ height: '100%' }}>
            <Statistic title={t('余额', 'Balance')} value={money(balance)} styles={{ content: balance < LOW_BALANCE_USD ? { color: '#ff4d4f' } : undefined }} />
            <div style={{ marginTop: 8, minHeight: 24 }}>
              {balance <= 0
                ? <Space wrap><Typography.Text type="danger">{t('余额已用完，调用会被拒绝', 'Balance used up; calls are rejected')}</Typography.Text>{topupAction}</Space>
                : balance < LOW_BALANCE_USD
                  ? <Space wrap><Typography.Text type="warning">{t('余额不多了', 'Running low')}</Typography.Text>{topupAction}</Space>
                  : topupOpen ? topupAction : null}
            </div>
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card style={{ height: '100%' }}>
            <Statistic title={t('今日花费', 'Spent today')} value={today ? money(today.summary.cost) : '-'} />
            <Typography.Text type="secondary">
              {today ? `${today.summary.requests} ${t('次请求', 'requests')} · ${tokens(today.summary.input_tokens + today.summary.output_tokens)} tokens` : ' '}
            </Typography.Text>
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card style={{ height: '100%' }}>
            <Statistic title={t('本月花费', 'Spent this month')} value={month ? money(month.summary.cost) : '-'} />
            <Typography.Text type="secondary">
              {month ? `${month.summary.requests} ${t('次请求', 'requests')} · ${tokens(month.summary.input_tokens + month.summary.output_tokens)} tokens` : ' '}
            </Typography.Text>
          </Card>
        </Col>
      </Row>

      {keys !== null && (
        <Card
          style={{ marginTop: 16 }}
          title={allDone ? <Space><CheckCircleFilled style={{ color: '#52c41a' }} />{t('已完成上手', 'You are all set')}</Space> : t('开始使用', 'Get started')}
          extra={allDone
            ? <Button type="link" size="small" onClick={() => setShowSteps((v) => !v)}>{showSteps ? t('收起', 'Hide') : t('查看步骤', 'Show steps')}</Button>
            : <Typography.Text type="secondary">{t(`已完成 ${doneCount}/${steps.length}`, `${doneCount}/${steps.length} done`)}</Typography.Text>}
          styles={allDone && !showSteps ? { body: { display: 'none' } } : undefined}
        >
          <Space direction="vertical" size={14} style={{ width: '100%' }}>
            {steps.map((s, i) => (
              <div key={i} className="guide-step">
                <span className={`guide-step-mark${s.done ? ' done' : ''}`}>{s.done ? <CheckCircleFilled /> : i + 1}</span>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <Typography.Text strong={!s.done} type={s.done ? 'secondary' : undefined}>{s.title}</Typography.Text>
                  <div className="muted">{s.detail}</div>
                </div>
                {!s.done && s.action}
              </div>
            ))}
          </Space>
        </Card>
      )}

      <Card title={t('接入', 'Connect')} style={{ marginTop: 16 }}>
        <Space direction="vertical" size={4} style={{ marginBottom: 12 }}>
          <Typography.Text type="secondary">{t('API 地址', 'API base URL')}</Typography.Text>
          <Typography.Text copyable className="mono" style={{ fontSize: 15 }}>{urls.base}</Typography.Text>
        </Space>
        <Tabs
          items={guideClients.map((c) => ({
            key: c.key,
            label: <Space size={4}>{c.key === 'agent' ? t('Agent 配置', 'Agent setup') : c.label}{c.recommended && <Tag color="blue" style={{ marginInlineEnd: 0 }}>{t('推荐', 'Recommended')}</Tag>}</Space>,
            children: (
              <Space direction="vertical" size={12} style={{ width: '100%' }}>
                <Typography.Text>{t(...c.intro)}</Typography.Text>
                {c.key === 'magpie' && (
                  <Space wrap>
                    <Button type="primary" href={magpieImportURL(urls)} target="_blank" rel="noreferrer" icon={<ExportOutlined />}>{t('添加到 Magpie', 'Add to Magpie')}</Button>
                    <Button type="link" href="https://usemagpie.ai" target="_blank" rel="noreferrer">{t('还没装？去 Magpie 官网', 'Get Magpie')}</Button>
                  </Space>
                )}
                {c.file && <Typography.Text type="secondary">{t('写在', 'File')}：<span className="mono">{c.file}</span></Typography.Text>}
                {c.code && codeBlock(c.code(urls))}
                <ul className="guide-notes">
                  {c.notes.map((n, i) => <li key={i}>{t(...n)}</li>)}
                </ul>
              </Space>
            ),
          }))}
        />
      </Card>
    </div>
  )
}

// 公告里的邮箱地址做成可点的 mailto 链接，其余原样显示
function linkEmails(text: string) {
  return text.split(/([\w.+-]+@[\w-]+(?:\.[\w-]+)+)/).map((part, i) => (i % 2 ? <a key={i} href={`mailto:${part}`}>{part}</a> : part))
}
