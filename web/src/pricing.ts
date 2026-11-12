import type { PriceRule } from './api'

// 价格规则条件的简短描述，和服务端自动命名的含义一致
export function ruleCondition(r: PriceRule, t: (zh: string, en: string) => string, zone: string) {
  const parts: string[] = []
  if (r.date_from && r.date_to) parts.push(t(`${r.date_from} 至 ${r.date_to}`, `${r.date_from} – ${r.date_to}`))
  else if (r.date_from) parts.push(t(`${r.date_from} 起`, `from ${r.date_from}`))
  else if (r.date_to) parts.push(t(`截至 ${r.date_to}`, `until ${r.date_to}`))
  if (r.input_above != null) parts.push(t(`输入超过 ${r.input_above.toLocaleString()} tokens`, `input > ${r.input_above.toLocaleString()} tokens`))
  if (r.days === 'weekend') parts.push(t('周末', 'weekends'))
  if (r.time_start) parts.push(t(`${zone} ${r.time_start}–${r.time_end}`, `${r.time_start}–${r.time_end} ${zone}`))
  return parts.join(' · ')
}

// GPT Fast 档位结果的说明（调用日志用），标准请求返回空
export function tierText(tier: string, t: (zh: string, en: string) => string) {
  return ({
    fast: 'Fast',
    fast_downgraded: t('Fast 被上游降为标准，按标准价', 'Fast downgraded upstream; standard price'),
    unavailable: t('模型未开放 Fast，已按标准处理', 'Fast not offered; served as standard'),
  } as Record<string, string>)[tier] ?? ''
}
