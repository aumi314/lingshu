import type { PriceRule } from './api'

// 价格规则条件的简短描述，和服务端自动命名的含义一致
export function ruleCondition(r: PriceRule, t: (zh: string, en: string) => string) {
  const parts: string[] = []
  if (r.input_above != null) parts.push(t(`输入超过 ${r.input_above.toLocaleString()} tokens`, `input > ${r.input_above.toLocaleString()} tokens`))
  if (r.time_start) parts.push(t(`北京时间 ${r.time_start}–${r.time_end}`, `${r.time_start}–${r.time_end} Beijing time`))
  return parts.join(' · ')
}
