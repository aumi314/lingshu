import VendorIcon from './VendorIcon'

// 顺序即模型列表里的厂商排列顺序：国际厂商在前，国内厂商在后，识别不了的排最后
const vendors: [RegExp, string][] = [
  [/^claude-/i, 'anthropic-platform'],
  [/^gpt-/i, 'openai-platform'],
  [/^gemini-/i, 'google-gemini'],
  [/^grok-/i, 'xai'],
  [/^muse-/i, 'meta'],
  [/^deepseek-/i, 'deepseek'],
  [/^kimi-/i, 'moonshot'],
  [/^glm-/i, 'zhipu'],
  [/^qwen/i, 'dashscope'],
  [/^minimax-/i, 'minimax'],
  [/^doubao-/i, 'volcengine'],
  [/^(hy\d|hunyuan)/i, 'hunyuan'],
  [/^step-/i, 'stepfun'],
  [/^mimo-/i, 'mimo'],
  [/^jev-/i, 'typesafe'],
  [/^drex-/i, 'nace'],
]

const vendorRank = (name: string) => {
  const i = vendors.findIndex(([pattern]) => pattern.test(name))
  return i < 0 ? vendors.length : i
}
const collator = new Intl.Collator('en', { numeric: true, sensitivity: 'base' })

// byVendor 按厂商分组排列，同一厂商内按名字自然排序（5.3 在 5.10 前）
export function byVendor<T extends { name: string }>(a: T, b: T): number {
  return vendorRank(a.name) - vendorRank(b.name) || collator.compare(a.name, b.name)
}

export default function ModelIcon({ name }: { name: string }) {
  const template = vendors.find(([pattern]) => pattern.test(name))?.[1]
  if (!template) return null
  return (
    <span aria-hidden style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: 18, height: 18, flexShrink: 0 }}>
      <VendorIcon template={template} size={18} />
    </span>
  )
}
