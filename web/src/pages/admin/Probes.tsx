import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Alert, App, Button, Card, Col, Collapse, Empty, Form, Modal, Progress, Radio, Row, Select, Space, Statistic, Table, Tag, Tooltip, Typography } from 'antd'
import { CheckCircleFilled, CloseCircleFilled, ExclamationCircleFilled, InfoCircleFilled, LoadingOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { get, post, type Channel } from '../../api'
import { tokens, useApp } from '../../app'

// 模型检测（docs/decisions.md「模型检测」）：选渠道和模型发起检测，和官方样本对比，逐项给出结论。
type Status = 'pass' | 'warn' | 'fail' | 'info'
type Item = { key: string; title: string; status: Status; summary: string; details?: Record<string, unknown> }
type Call = {
  item: string; system?: string; prompt: string; effort?: string; text: string; tool_calls?: { name: string; arguments: string }[]
  error?: string; input_tokens: number; cached_tokens: number; output_tokens: number; reasoning_tokens: number
  id?: string; service_tier?: string; first_token_ms: number; total_ms: number
}
type Report = { baseline: string; mode?: 'protocol'; score: number; verdict: string; items: Item[]; observations: Item[]; metrics: Record<string, number>; calls: Call[] }
type Run = {
  id: number; created_at: string; finished_at: string | null; created_by: string; channel_id: number | null; channel_name: string
  model: string; upstream_model: string; baseline: string; preset: 'quick' | 'full'; status: 'running' | 'done' | 'failed'
  score: number | null; verdict: string; cost: string; report?: Report; progress?: [number, number]
}

const statusIcon: Record<Status, ReactNode> = {
  pass: <CheckCircleFilled style={{ color: '#52c41a' }} />,
  warn: <ExclamationCircleFilled style={{ color: '#faad14' }} />,
  fail: <CloseCircleFilled style={{ color: '#ff4d4f' }} />,
  info: <InfoCircleFilled style={{ color: '#1677ff' }} />,
}

export default function Probes() {
  const { t, money } = useApp()
  const { message } = App.useApp()
  const [channels, setChannels] = useState<Channel[]>([])
  const [baselines, setBaselines] = useState<string[]>([])
  const [protocolModels, setProtocolModels] = useState<string[]>([]) // 没有官方样本、只做协议层检测的型号（Claude，走 Messages）
  const [runs, setRuns] = useState<Run[]>([])
  const [selected, setSelected] = useState<Run | null>(null)
  const [starting, setStarting] = useState(false)
  const [form] = Form.useForm<{ channel_id?: number; model?: string; preset: 'quick' | 'full' }>()
  const channelID = Form.useWatch('channel_id', form)

  const statusText: Record<Status, string> = { pass: t('通过', 'Pass'), warn: t('需关注', 'Check'), fail: t('不通过', 'Fail'), info: t('参考', 'Info') }

  const loadRuns = useCallback(async () => {
    try { setRuns(await get<Run[]>('/api/admin/probes')) } catch (e) { message.error((e as Error).message) }
  }, [message])
  const open = useCallback(async (id: number) => {
    try { setSelected(await get<Run>(`/api/admin/probes/${id}`)) } catch (e) { message.error((e as Error).message) }
  }, [message])

  useEffect(() => {
    get<Channel[]>('/api/admin/channels').then(setChannels).catch((e) => message.error(e.message))
    get<{ models: string[]; protocol_models?: string[] }>('/api/admin/probe-baselines').then((r) => { setBaselines(r.models); setProtocolModels(r.protocol_models ?? []) }).catch(() => {})
    void loadRuns()
  }, [loadRuns, message])

  // 进行中的检测每 2 秒刷新一次进度
  const running = selected?.status === 'running' || runs.some((r) => r.status === 'running')
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => {
      void loadRuns()
      if (selected?.status === 'running') void open(selected.id)
    }, 2000)
    return () => clearInterval(timer)
  }, [running, selected, loadRuns, open])

  // GPT 走 Chat、和官方样本比；Claude 走 Messages、只做协议层检测
  const probeChannels = channels.filter((c) => (c.base_url_chat || c.base_url_messages) && c.models.length > 0)
  const channel = probeChannels.find((c) => c.id === channelID)
  const models = channel?.models ?? []
  const modelNote = (name: string) => {
    if (protocolModels.includes(name)) return channel?.base_url_messages ? { ok: true, note: t('（协议检测）', ' (protocol check)') } : { ok: false, note: t('（渠道没有 Messages 地址）', ' (no Messages URL)') }
    if (baselines.includes(name)) return channel?.base_url_chat ? { ok: true, note: '' } : { ok: false, note: t('（渠道没有 Chat 地址）', ' (no Chat URL)') }
    return { ok: false, note: t('（暂无官方样本）', ' (no baseline)') }
  }

  const start = async () => {
    const v = await form.validateFields()
    setStarting(true)
    try {
      // 对照型号就是站内模型名（没有官方样本的模型选不了）
      const r = await post<{ id: number }>('/api/admin/probes', { channel_id: v.channel_id, model: v.model, preset: v.preset })
      message.success(t('检测已开始，后台运行，关掉页面也会跑完', 'Started; it keeps running in the background'))
      await loadRuns()
      await open(r.id)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setStarting(false)
    }
  }

  return (
    <div className="page" style={{ maxWidth: 1400 }}>
      <Typography.Title level={3} className="page-title">{t('模型检测', 'Model check')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t(
          '选渠道和模型，向渠道直接发一组检测题，和同名官方型号的样本对比，看上游有没有换模型、降推理强度、注入内容。Claude 暂无官方样本，先做协议层检测（格式、推理是否发生、工具、文档）。检测请求伪装成常见客户端、每次随机出题，不走路由、不进调用日志、不扣用户余额；花费按站内价估算，由渠道承担。',
          'Sends a set of probe questions straight to the channel and compares the answers with official baselines. Probe requests bypass routing, are not logged as calls and are not charged to users.',
        )}
      </Typography.Paragraph>

      <Card style={{ marginBottom: 16 }}>
        <Form form={form} layout="inline" initialValues={{ preset: 'quick' }} style={{ rowGap: 12 }}>
          <Form.Item name="channel_id" label={t('渠道', 'Channel')} rules={[{ required: true, message: t('选择渠道', 'Pick a channel') }]}>
            <Select style={{ width: 220 }} showSearch optionFilterProp="label" onChange={() => form.setFieldValue('model', undefined)}
              options={probeChannels.map((c) => ({ value: c.id, label: c.name + (c.enabled ? '' : t('（已停用）', ' (disabled)')) }))} />
          </Form.Item>
          <Form.Item name="model" label={t('模型', 'Model')} rules={[{ required: true, message: t('选择模型', 'Pick a model') }]}>
            <Select style={{ width: 280 }} showSearch disabled={!channelID}
              options={models.map((m) => {
                const n = modelNote(m.model_name)
                return {
                  value: m.model_name,
                  disabled: !n.ok, // 没有官方样本、或者渠道缺这个检测要走的协议地址
                  label: (m.upstream_name && m.upstream_name !== m.model_name ? `${m.model_name} ← ${m.upstream_name}` : m.model_name) + n.note,
                }
              })} />
          </Form.Item>
          <Form.Item name="preset" label={t('档位', 'Preset')}>
            <Radio.Group optionType="button" options={[
              { value: 'quick', label: <Tooltip title={t('单词指纹抽 40 题、每题问 4 次，几分钟跑完，花费低', '40 fingerprint prompts × 4')}>{t('快速', 'Quick')}</Tooltip> },
              { value: 'full', label: <Tooltip title={t('区分度高的指纹题全上、每题问 6 次，另加一次约 10 万 token 的大海捞针（查上下文截断、压缩）；时间和花费明显更高', 'All separating prompts × 6, plus a ~100K-token needle test')}>{t('完整', 'Full')}</Tooltip> },
            ]} />
          </Form.Item>
          <Button type="primary" loading={starting} onClick={start}>{t('开始检测', 'Start')}</Button>
        </Form>
        {baselines.length === 0 && <Alert style={{ marginTop: 12 }} type="warning" showIcon title={t('没有官方样本，无法检测', 'No baselines available')} />}
      </Card>

      <Modal
        open={!!selected}
        width={1180}
        title={selected ? t(`检测结果 · ${selected.channel_name} · ${selected.model}`, `Result · ${selected.channel_name} · ${selected.model}`) : ''}
        onCancel={() => setSelected(null)}
        footer={<Button onClick={() => setSelected(null)}>{t('关闭', 'Close')}</Button>}
        destroyOnHidden
      >
        {selected && <Result run={selected} protocol={protocolModels.includes(selected.baseline)} statusIcon={statusIcon} statusText={statusText} />}
      </Modal>

      <Card title={t('检测记录', 'History')} size="small">
        <Table<Run>
          rowKey="id"
          size="small"
          dataSource={runs}
          pagination={false}
          scroll={{ x: 'max-content' }}
          onRow={(r) => ({ onClick: () => open(r.id), style: { cursor: 'pointer' } })}
          rowClassName={(r) => (r.id === selected?.id ? 'ant-table-row-selected' : '')}
          locale={{ emptyText: <Empty description={t('还没有检测记录', 'No checks yet')} /> }}
          columns={[
            { title: t('时间', 'Time'), dataIndex: 'created_at', render: (v: string) => <span className="nowrap">{dayjs(v).format('MM-DD HH:mm')}</span> },
            { title: t('渠道', 'Channel'), dataIndex: 'channel_name' },
            { title: t('模型', 'Model'), render: (_, r) => <span className="mono">{r.model}</span> },
            { title: t('档位', 'Preset'), dataIndex: 'preset', render: (v: string) => (v === 'full' ? t('完整', 'Full') : t('快速', 'Quick')) },
            { title: t('得分', 'Score'), render: (_, r) => r.status === 'running' ? <RunningTag run={r} /> : r.status === 'failed' ? <Tag color="red">{t('失败', 'Failed')}</Tag> : <b>{r.score}%</b> },
            { title: t('结论', 'Verdict'), dataIndex: 'verdict', render: (v: string) => <span style={{ whiteSpace: 'normal' }}>{v || '—'}</span> },
            { title: t('花费', 'Cost'), dataIndex: 'cost', align: 'right', render: (v: string) => money(v, 4) },
          ]}
        />
      </Card>
    </div>
  )
}

function RunningTag({ run }: { run: Run }) {
  const { t } = useApp()
  const [done, total] = run.progress ?? [0, 0]
  return <Tag icon={<LoadingOutlined />} color="processing">{total ? `${Math.floor((done * 100) / total)}%` : t('检测中', 'Running')}</Tag>
}

function Result({ run, protocol, statusIcon, statusText }: { run: Run; protocol: boolean; statusIcon: Record<Status, ReactNode>; statusText: Record<Status, string> }) {
  const { t, money } = useApp()
  const [item, setItem] = useState<string | undefined>()
  const r = run.report
  const calls = useMemo(() => (r?.calls ?? []).filter((c) => !item || c.item === item), [r, item])
  if (run.status === 'running') {
    const [done, total] = run.progress ?? [0, 0]
    return (
      <div>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Text>{t(`正在检测 ${run.channel_name} 的 ${run.model}（${protocol ? '协议检测' : `对照 ${run.baseline}`}）…`, `Checking ${run.model} on ${run.channel_name}…`)}</Typography.Text>
          <Progress percent={total ? Math.floor((done * 100) / total) : 0} status="active" format={() => `${done} / ${total || '?'}`} />
          <Typography.Text type="secondary">{t('检测在后台运行，关掉这个窗口也会继续，跑完在检测记录里点开查看。', 'Runs in the background; you can close this window.')}</Typography.Text>
        </Space>
      </div>
    )
  }
  if (run.status === 'failed' || !r) {
    return <Alert type="error" showIcon title={t('检测失败', 'Check failed')} description={run.verdict} />
  }
  const color = r.score >= 85 ? '#52c41a' : r.score >= 60 ? '#faad14' : '#ff4d4f'
  const m = r.metrics
  const list = (items: Item[]) => (
    <div>
      {items.map((it) => (
        <Collapse
          key={it.key}
          ghost
          size="small"
          items={[{
            key: it.key,
            label: (
              <div style={{ display: 'flex', gap: 10, alignItems: 'baseline' }}>
                <span>{statusIcon[it.status]}</span>
                <span style={{ minWidth: 110, fontWeight: 500 }}>{it.title}</span>
                <Typography.Text type="secondary" style={{ flex: 1 }}>{it.summary}</Typography.Text>
                <Tag color={{ pass: 'green', warn: 'orange', fail: 'red', info: 'blue' }[it.status]}>{statusText[it.status]}</Tag>
              </div>
            ),
            children: <Details item={it} />,
            showArrow: !!it.details,
            collapsible: it.details ? undefined : 'disabled',
          }]}
        />
      ))}
    </div>
  )
  return (
    <div>
      <Row gutter={[24, 16]}>
        <Col xs={24} md={6} style={{ textAlign: 'center' }}>
          <Progress type="circle" percent={r.score} strokeColor={color} size={150} format={(p) => <span style={{ fontSize: 34, fontWeight: 600 }}>{p}%</span>} />
          <Typography.Title level={5} style={{ marginTop: 12 }}>{r.verdict}</Typography.Title>
          <div className="muted" style={{ fontSize: 13, lineHeight: 1.9 }}>
            <div>{dayjs(run.created_at).format('YYYY-MM-DD HH:mm')}</div>
            <div>{run.channel_name} · <span className="mono">{run.model}</span>{run.upstream_model !== run.model && <> ← <span className="mono">{run.upstream_model}</span></>}</div>
            <div>{r.mode === 'protocol' ? t('协议层检测，暂无官方样本', 'Protocol check, no baseline') : <>{t('对照', 'Baseline')} <span className="mono">{run.baseline}</span></>} · {run.preset === 'full' ? t('完整', 'Full') : t('快速', 'Quick')} · {t('花费约', 'Cost ~')} {money(run.cost, 4)}</div>
          </div>
        </Col>
        <Col xs={24} md={18}>
          <Typography.Text strong>{t('检测项', 'Checks')}</Typography.Text>
          {list(r.items)}
          <Typography.Text strong style={{ display: 'block', marginTop: 12 }}>{t('请求质量（不计分）', 'Request quality (not scored)')}</Typography.Text>
          {list(r.observations)}
        </Col>
      </Row>
      <Row gutter={[12, 12]} style={{ marginTop: 16 }}>
        {[
          [t('首字延迟', 'First token'), m.first_token_ms !== undefined ? `${(m.first_token_ms / 1000).toFixed(1)}s` : '—'],
          [t('输出速度', 'Speed'), m.tokens_per_sec !== undefined ? `${m.tokens_per_sec} tok/s` : '—'],
          [t('请求 / 失败', 'Requests / failed'), `${m.requests} / ${m.errors}`],
          [t('输入 tokens', 'Input tokens'), tokens(m.input_tokens ?? 0)],
          [t('输出 tokens', 'Output tokens'), tokens(m.output_tokens ?? 0)],
          [t('缓存读取', 'Cache read'), tokens(m.cached_tokens ?? 0)],
        ].map(([title, value]) => (
          <Col xs={12} md={4} key={title}><Card size="small"><Statistic title={title} value={value} /></Card></Col>
        ))}
      </Row>
      <Collapse
        style={{ marginTop: 16 }}
        items={[{
          key: 'calls',
          label: t(`请求明细（${r.calls.length} 个，可复核每道题的原文和回答）`, `Requests (${r.calls.length})`),
          children: (
            <>
              <Select allowClear placeholder={t('全部检测项', 'All checks')} style={{ width: 200, marginBottom: 8 }} value={item} onChange={setItem}
                options={[...new Set(r.calls.map((c) => c.item))].map((k) => ({ value: k, label: k }))} />
              <Table<Call>
                rowKey={(_, i) => String(i)}
                size="small"
                dataSource={calls}
                pagination={{ pageSize: 20, showSizeChanger: false }}
                scroll={{ x: 'max-content' }}
                columns={[
                  { title: t('检测项', 'Check'), dataIndex: 'item' },
                  { title: t('提示词', 'Prompt'), render: (_, c) => <Tooltip title={<pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{(c.system ? `[system] ${c.system}\n` : '') + c.prompt}</pre>}><span>{short(c.prompt, 60)}</span></Tooltip> },
                  { title: t('回答', 'Answer'), render: (_, c) => c.error ? <Typography.Text type="danger">{short(c.error, 60)}</Typography.Text> : <Tooltip title={<pre style={{ whiteSpace: 'pre-wrap', margin: 0 }}>{c.text || JSON.stringify(c.tool_calls)}</pre>}><span>{short(showInvisible(c.text) || (c.tool_calls ? `→ ${c.tool_calls.map((x) => `${x.name}(${x.arguments})`).join(', ')}` : ''), 60)}</span></Tooltip> },
                  { title: t('输入 / 输出（推理）', 'In / out (reasoning)'), render: (_, c) => <span className="nowrap">{c.input_tokens} / {c.output_tokens}{c.reasoning_tokens ? `（${c.reasoning_tokens}）` : ''}</span> },
                  { title: t('响应编号', 'Response ID'), render: (_, c) => <span className="mono muted">{(c.id ?? '').slice(0, 14)}{c.service_tier ? ` · ${c.service_tier}` : ''}</span> },
                  { title: t('耗时', 'Time'), render: (_, c) => <span className="nowrap">{(c.total_ms / 1000).toFixed(1)}s</span> },
                ]}
              />
            </>
          ),
        }]}
      />
    </div>
  )
}

function Details({ item }: { item: Item }) {
  const { t } = useApp()
  const d = item.details ?? {}
  if (Array.isArray(d.ranking)) {
    const sessions = (d.sessions ?? []) as { session: number; nearest: string; odd: boolean; baseline_distance: number; ranking: { model: string; distance: number }[] }[]
    return (
      <Space direction="vertical" size={2}>
        <Typography.Text type="secondary">{t('和各官方型号的距离（越小越像）：', 'Distance to each official model (smaller is closer):')}</Typography.Text>
        {(d.ranking as { model: string; distance: number; prompts: number }[]).map((x) => (
          <span key={x.model} className="mono">{x.model}：{x.distance.toFixed(3)}（{x.prompts} {t('题', 'prompts')}）</span>
        ))}
        {sessions.length > 0 && <>
          <Typography.Text type="secondary" style={{ marginTop: 6 }}>{t('各会话单独看（号池按会话路由，每个会话可能打到不同上游）：', 'Per session (pools route by session):')}</Typography.Text>
          {sessions.map((x) => (
            <span key={x.session} style={{ color: x.odd ? '#ff4d4f' : undefined }}>
              {t(`会话 ${x.session}：最像`, `Session ${x.session}: closest to`)} <span className="mono">{x.nearest}</span>（{x.ranking[0].distance.toFixed(3)}）{x.odd && t('，明显不像对照型号', ', clearly not the baseline')}
            </span>
          ))}
        </>}
      </Space>
    )
  }
  if (Array.isArray(d.problems)) {
    return (
      <Space direction="vertical" size={2}>
        {(d.problems as { kind: string; answer: number; got: string; correct: boolean; reasoning_tokens: number; baseline_reasoning?: number; baseline_correct?: string }[]).map((p, i) => (
          <span key={i}>{p.correct ? '✓' : '✗'} {p.kind}：{t('标准答案', 'answer')} {p.answer}，{t('回答', 'got')} {short(p.got, 30) || '—'}；{t('推理', 'reasoning')} {p.reasoning_tokens}{p.baseline_correct !== undefined && <>（{t('官方中位数', 'official median')} {p.baseline_reasoning}，{t('官方答对', 'official correct')} {p.baseline_correct}）</>}</span>
        ))}
        {d.effort_low !== undefined && <span>{t('推理强度 low / high 的推理 token：', 'Reasoning tokens at low / high: ')}{String(d.effort_low)} / {String(d.effort_high)}</span>}
      </Space>
    )
  }
  return <pre style={{ margin: 0, whiteSpace: 'pre-wrap', fontSize: 12 }}>{JSON.stringify(d, null, 2)}</pre>
}

function short(s: string, n: number) {
  return s.length > n ? s.slice(0, n) + '…' : s
}

// 回答里的零宽字符显示出来，方便核对
function showInvisible(s: string) {
  return s.replace(/[\u200b\u200c\u200d\u2060\ufeff]/g, '⟨零宽⟩')
}
