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
  role: 'super_admin' | 'admin' | 'user'
  balance: string
  enabled: boolean
  preferences: Preferences
  user_group_ids: number[]
  created_at: string
  /** 管理员写的内部备注，只有管理端用户列表返回 */
  note?: string  /** 该用户所有 key 最近一次调用的时间，只有管理端用户列表返回 */
  last_used_at?: string
}

// 超级管理员和管理员都能进后台，权限相同；超级管理员只多一项任免管理员
export const isAdmin = (role?: string) => role === 'admin' || role === 'super_admin'

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
  fast_multiplier: string | null // GPT Fast 计费倍数（乘在规则选出的单价上），null 表示不开放
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
  fast_multiplier: string | null
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
  client_ip: string // 访问者 IP，旧日志为空
  reasoning_effort: string // 客户端要求的推理强度，原样记录，旧日志为空
  service_tier: '' | 'fast' | 'fast_downgraded' | 'unavailable' // GPT Fast 档位结果，'' 为标准
  read_ms?: number // 读请求体用时；旧日志为 0
  upstream_start_ms?: number // 发出最终上游请求的时刻（从进入网关算）；旧日志为 0，此时首字按旧口径（从进入网关算）
}

export type LogSummary = {
  requests: number; failed: number; input_tokens: number; cache_read_tokens: number; output_tokens: number; cost: string // input_tokens 含缓存读写
  // 负载与响应：峰值取最忙的那一分钟；响应时间是成功请求的中位数（首字从发给上游算起）
  peak_rpm?: number; peak_rpm_at?: string | null; peak_tpm?: number; peak_tpm_at?: string | null
  median_first_token_ms?: number | null; median_total_ms?: number | null
}
export type LogsResponse = { logs: LogEntry[]; summary: LogSummary; models: string[] }

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

export type OperationChange = { field: string; from?: unknown; to?: unknown; secret?: boolean }
export type OperationEntry = {
  id: number
  created_at: string
  actor_id: number | null // null 表示系统
  actor: string
  user_id: number | null
  action: string
  target_type: string
  target_id: string
  target_name: string
  changes: OperationChange[]
  note: string
  amount?: string
  order?: { id: string; provider: string; method: string; currency: string; amount: string; tax: string; total: string; completed_at: string | null; status: 'pending' | 'success' | 'failed' | 'expired' } // status 为订单当前状态
}
export type OperationLogsResponse = { logs: OperationEntry[]; total: number; summary: { topup_total: string; adjustment_net: string } }

export type ChannelModel = { model_id: number; model_name: string; upstream_name: string }

// 渠道里的一把 key：管理端只看掩码；status、stats 来自网关内存和最近 24 小时日志
export type ChannelKey = {
  id?: number
  api_key?: string
  masked: string
  enabled: boolean
  note: string
  status?: { state: 'ok' | 'cooling' | 'invalid'; until?: string; reason?: string }
  stats?: { success: number; failed: number }
}

export type Channel = {
  id: number
  name: string
  template: string
  note: string
  api_key?: string
  api_key_masked: string
  keys: ChannelKey[]
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
  // 最近 24 小时的连接状态，没有任何尝试时为 null；口径见 internal/store/channel_stats.go
  stats: ChannelStat | null
  // 上次查到的厂商余额，原样保存；只有 DeepSeek、Kimi、OpenRouter 能查
  balance: { currency: string; amount: string }[]
  balance_checked_at: string | null
  balance_error: string
}

export type ChannelStat = {
  success: number
  failed: number
  first_token_ms: number | null // 成功流式请求的首字中位数（从发给上游算）
  tokens_per_sec: number | null // 成功流式请求的输出速度中位数
  cache_read_tokens: number
  input_tokens: number // 含缓存读写
  last_success_at: string | null
  last_failure_at: string | null
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
