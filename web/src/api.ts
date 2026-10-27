// 调用 /api 接口。出错时抛出带服务端提示的 Error，页面直接展示 message。

export class ApiError extends Error {
  constructor(public status: number, message: string, public data?: Record<string, unknown>) {
    super(message)
  }
}

export async function api<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
  let resp: Response
  try {
    resp = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, '连不上服务器，请检查网络后重试')
  }
  const text = await resp.text()
  let data: unknown = undefined
  try {
    data = text ? JSON.parse(text) : undefined
  } catch {
    /* 非 JSON 响应 */
  }
  if (!resp.ok) {
    const msg = (data as { error?: string } | undefined)?.error ?? `请求失败（HTTP ${resp.status}）`
    if (resp.status === 401 && !path.startsWith('/api/auth/')) {
      window.dispatchEvent(new Event('lingshu:unauthorized'))
    }
    throw new ApiError(resp.status, msg, data as Record<string, unknown> | undefined)
  }
  return data as T
}

export const get = <T,>(path: string) => api<T>('GET', path)
export const post = <T,>(path: string, body?: unknown) => api<T>('POST', path, body ?? {})
export const put = <T,>(path: string, body: unknown) => api<T>('PUT', path, body)
export const patch = <T,>(path: string, body: unknown) => api<T>('PATCH', path, body)
export const del = <T,>(path: string) => api<T>('DELETE', path)

// ---- 接口返回的数据结构（金额都是字符串，单位 USD） ----

export type Preferences = { language?: 'zh' | 'en'; currency?: 'USD' | 'CNY'; theme?: 'system' | 'light' | 'dark' }

export type User = {
  id: number
  username: string
  role: 'admin' | 'user'
  balance: string
  enabled: boolean
  preferences: Preferences
  user_group_ids: number[]
  created_at: string
}

export type UserGroup = { id: number; name: string; is_default: boolean }
export type Group = { id: number; name: string; ratio: string; enabled: boolean; is_public: boolean; user_group_ids: number[]; description: string }

// 价格规则（阶梯计价、分时段计价），按顺序匹配，第一条满足的生效
export type PriceRule = {
  name: string
  input_above?: number | null // 总输入（含缓存）超过这么多 token
  time_start?: string // 北京时间 HH:MM，含开始不含结束，可跨零点
  time_end?: string
  days?: '' | 'weekend' // 周末 = 北京时间周六、周日
  date_from?: string // 生效日期（北京时间 YYYY-MM-DD，含当天）
  date_to?: string
  input_price: string
  output_price: string
  cache_read_price: string
  cache_write_price: string
}

export type Model = {
  id: number
  name: string
  input_price: string
  output_price: string
  cache_read_price: string
  cache_write_price: string
  price_rules: PriceRule[]
}

export type PricedModel = {
  group_id: number
  name: string
  protocols: string[]
  input_price: string
  output_price: string
  cache_read_price: string
  cache_write_price: string
  price_rules: PriceRule[]
}

export type APIKey = {
  id: number
  name: string
  prefix: string
  group_id: number
  group_name: string
  quota_limit: string | null
  quota_used: string
  enabled: boolean
  last_used_at: string | null
  created_at: string
}

export type LogEntry = {
  id: number
  request_id: string
  username: string
  key_name: string
  group_name: string
  protocol: string
  model: string
  upstream_model?: string
  channel_name?: string
  stream: boolean
  status: 'success' | 'client_cancelled' | 'upstream_error' | 'rejected'
  http_status: number
  error: string
  input_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  output_tokens: number
  reasoning_tokens: number
  usage_estimated: boolean
  group_ratio: string
  price_rule: string // 命中的价格规则，空表示基础价
  input_price: string
  output_price: string
  cost: string
  attempts: number
  first_byte_ms: number
  total_ms: number
  created_at: string
}

export type LogSummary = { requests: number; failed: number; input_tokens: number; output_tokens: number; cost: string }
export type LogsResponse = { logs: LogEntry[]; summary: LogSummary; models: string[] }

export type LogType = 'all' | 'request' | 'topup' | 'adjustment'
type ActivityFields = { id: number; event_key: string; user_id: number; username: string; created_at: string; balance_delta: string; operator: string; note: string }
export type RequestActivity = LogEntry & ActivityFields & { type: 'request' }
export type BalanceActivity = ActivityFields & {
  type: 'topup' | 'adjustment'
  status: 'success'
  balance_after: string
  order?: { id: string; provider: string; method: string; currency: string; amount: string; tax: string; total: string; completed_at: string | null }
}
export type ActivityEntry = RequestActivity | BalanceActivity
export type ActivityLogsResponse = { logs: ActivityEntry[]; total: number; summary: LogSummary & { topup_total: string; adjustment_net: string }; models: string[] }

export type ChannelModel = { model_id: number; model_name: string; upstream_name: string }

export type Channel = {
  id: number
  name: string
  template: string
  api_key?: string
  api_key_masked: string
  priority: number
  enabled: boolean
  base_url_chat: string
  base_url_messages: string
  base_url_responses: string
  base_url_systemone: string
  proxy_url: string
  group_ids: number[]
  models: ChannelModel[]
  cooling?: boolean
  // 上次查到的厂商余额，原样保存；只有 DeepSeek、Kimi、OpenRouter 能查
  balance: { currency: string; amount: string }[]
  balance_checked_at: string | null
  balance_error: string
}

export type ChannelTemplate = {
  id: string
  name: string
  base_url_chat: string
  base_url_messages: string
  base_url_responses: string
  base_url_systemone: string
  has_model_list: boolean
  preset_models: string[] | null
  proxy_url: string
  note: string
}

export type ProbeResult = {
  protocol: string
  model: string
  ok: boolean
  http_status: number
  error?: string
  first_byte_ms: number
  total_ms: number
  usage_reported: boolean
  input_tokens: number
  output_tokens: number
}

export type BalanceAdjustment = {
  id: number
  amount: string
  balance_after: string
  operator: string
  note: string
  created_at: string
}
