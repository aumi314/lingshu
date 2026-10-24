import { post } from './api'

export const paymentNames: Record<string, string> = { epay: 'EPay', stripe: 'Stripe', creem: 'Creem', waffo: 'Waffo', waffo_pancake: 'Waffo Pancake' }
export type PaymentMethod = { id: string; name: string }
export type PaymentConfig = {
  version_id: number; provider: string; enabled: boolean; environment: string; currency: string
  unit_price: string; min_usd: string; max_usd: string; values: Record<string, string>
  configured_secrets: Record<string, boolean>; methods: PaymentMethod[]
}
export type PaymentService = { config: PaymentConfig; ready: boolean; notify_url: string }
export type Topup = {
  id: string; user_id: number; provider: string; environment: string; credit_usd: string
  amount: string; currency: string; status: string; tax: string; total: string
  admin_test: boolean; created_at: string; completed_at: string | null
}
export type OrderList = { items: Topup[]; total: number }

export async function payOrder(id: string) {
  const action = await post<{ type: string; url: string; fields?: Record<string, string> }>(`/api/me/topups/${id}/checkout`)
  // 返回钱包时能够直接找到这一笔订单，不依赖支付平台透传参数。
  window.history.replaceState(null, '', `/console/wallet?order=${encodeURIComponent(id)}`)
  if (action.type === 'form') {
    const form = document.createElement('form')
    form.method = 'POST'
    form.action = action.url
    for (const [name, value] of Object.entries(action.fields ?? {})) {
      const input = document.createElement('input')
      input.type = 'hidden'; input.name = name; input.value = value
      form.appendChild(input)
    }
    document.body.appendChild(form)
    form.submit()
  } else {
    window.location.assign(action.url)
  }
}
