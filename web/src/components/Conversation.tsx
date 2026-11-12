// 对话留存的展示（docs/decisions.md「对话留存」）：把三种协议的请求统一成“角色 + 内容”的列表，加上这次的回复。
// 只为阅读：解析不了的部分原样显示 JSON；要拿原文分析用“下载原文”。
import { Button, Space, Tag, Typography } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { useApp } from '../app'

export type ConversationData = {
  captured: boolean
  session?: string
  request?: unknown
  response?: string
  response_truncated?: boolean
  reply_text?: string
}

type Turn = { role: string; text: string; kind?: 'tool' | 'thinking' | 'data' }

type Obj = Record<string, unknown>
const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v, null, 2))

// 一段内容（字符串或内容块数组）拆成若干条：文字合在一起，工具调用、工具结果、思考、图片各成一条
function parts(role: string, content: unknown): Turn[] {
  if (content == null) return []
  if (typeof content === 'string') return content ? [{ role, text: content }] : []
  if (!Array.isArray(content)) return [{ role, text: str(content), kind: 'data' }]
  const out: Turn[] = []
  let text = ''
  const flush = () => {
    if (text) out.push({ role, text })
    text = ''
  }
  for (const p of content) {
    if (!isObj(p)) continue
    const type = String(p.type ?? '')
    if (['text', 'input_text', 'output_text', 'summary_text'].includes(type)) {
      text += (text ? '\n' : '') + String(p.text ?? '')
      continue
    }
    flush()
    if (type === 'tool_use') out.push({ role, text: `${p.name}(${str(p.input)})`, kind: 'tool' })
    else if (type === 'tool_result') out.push(...parts('tool', p.content).map((t) => ({ ...t, kind: 'tool' as const })))
    else if (type === 'thinking' || type === 'redacted_thinking') out.push({ role, text: String(p.thinking ?? '…'), kind: 'thinking' })
    else if (type === 'image' || type === 'image_url' || type === 'input_image') out.push({ role, text: '[图片]', kind: 'data' })
    else out.push({ role, text: str(p), kind: 'data' })
  }
  flush()
  return out
}

function turnsOf(protocol: string, req: unknown): Turn[] {
  if (!isObj(req)) return [{ role: 'request', text: str(req), kind: 'data' }]
  const out: Turn[] = []
  if (protocol === 'messages') {
    out.push(...parts('system', req.system))
    for (const m of (req.messages as unknown[]) ?? []) if (isObj(m)) out.push(...parts(String(m.role), m.content))
  } else if (protocol === 'responses') {
    if (typeof req.instructions === 'string' && req.instructions) out.push({ role: 'system', text: req.instructions })
    const input = req.input
    if (typeof input === 'string') out.push({ role: 'user', text: input })
    for (const it of Array.isArray(input) ? input : []) {
      if (!isObj(it)) continue
      const type = String(it.type ?? (it.role ? 'message' : ''))
      if (type === 'message') out.push(...parts(String(it.role), it.content))
      else if (type === 'function_call' || type === 'custom_tool_call') out.push({ role: 'assistant', text: `${it.name}(${String(it.arguments ?? it.input ?? '')})`, kind: 'tool' })
      else if (type === 'function_call_output' || type === 'custom_tool_call_output') out.push({ role: 'tool', text: str(it.output), kind: 'tool' })
      else if (type === 'reasoning') out.push({ role: 'assistant', text: '[推理]', kind: 'thinking' })
      else out.push({ role: type || 'item', text: str(it), kind: 'data' })
    }
  } else {
    for (const m of (req.messages as unknown[]) ?? []) {
      if (!isObj(m)) continue
      out.push(...parts(String(m.role), m.content))
      for (const c of (m.tool_calls as unknown[]) ?? []) {
        const f = isObj(c) && isObj(c.function) ? c.function : null
        if (f) out.push({ role: String(m.role), text: `${f.name}(${String(f.arguments ?? '')})`, kind: 'tool' })
      }
    }
  }
  return out
}

const roleColor: Record<string, string> = { system: 'purple', developer: 'purple', user: 'blue', assistant: 'green', tool: 'gold' }

export default function Conversation({ protocol, requestID, data }: { protocol: string; requestID: string; data: ConversationData }) {
  const { t } = useApp()
  if (!data.captured) {
    return (
      <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
        {t('这次调用没有留存对话。留存开关在「系统设置 → 调用设置」，开启后对之后的调用生效。', 'This call has no saved conversation. Turn saving on under System → Calls; it applies to later calls.')}
      </Typography.Paragraph>
    )
  }
  const turns = turnsOf(protocol, data.request)
  const download = () => {
    const blob = new Blob([JSON.stringify({ request_id: requestID, session: data.session, protocol, request: data.request, response: data.response, response_truncated: data.response_truncated ?? false }, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${requestID}.json`
    a.click()
    URL.revokeObjectURL(a.href)
  }
  return (
    <Space direction="vertical" size={10} style={{ width: '100%' }}>
      <Space wrap>
        {data.session && <Typography.Text type="secondary">{t('会话', 'Session')} <span className="mono">{data.session}</span></Typography.Text>}
        <Button size="small" icon={<DownloadOutlined />} onClick={download}>{t('下载原文', 'Download raw')}</Button>
      </Space>
      {turns.map((turn, i) => (
        <div key={i} className="conv-turn">
          <Tag color={roleColor[turn.role]}>{turn.role}</Tag>
          {turn.kind === 'tool' && <Tag>{t('工具', 'tool')}</Tag>}
          {turn.kind === 'thinking' && <Tag>{t('思考', 'thinking')}</Tag>}
          <Typography.Paragraph className={turn.kind && turn.kind !== 'thinking' ? 'mono' : undefined} style={{ whiteSpace: 'pre-wrap', margin: '4px 0 0', fontSize: turn.kind ? 12 : undefined }} ellipsis={{ rows: 6, expandable: 'collapsible' }}>
            {turn.text}
          </Typography.Paragraph>
        </div>
      ))}
      <div className="conv-turn conv-reply">
        <Tag color="green">{t('本次回复', 'This reply')}</Tag>
        {data.response_truncated && <Tag color="orange">{t('超过 8 MiB，已截断', 'Over 8 MiB, truncated')}</Tag>}
        <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', margin: '4px 0 0' }} ellipsis={{ rows: 12, expandable: 'collapsible' }}>
          {data.reply_text || t('（回复里没有正文，可能只有工具调用；看原文请下载）', '(No text in the reply, maybe only tool calls; download for the raw reply)')}
        </Typography.Paragraph>
      </div>
    </Space>
  )
}
