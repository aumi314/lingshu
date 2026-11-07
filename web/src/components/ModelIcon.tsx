import VendorIcon from './VendorIcon'

// 顺序即模型列表里的厂商排列顺序：国际厂商在前，国内厂商在后，识别不了的排最后。第三列是筛选里显示的厂商名
const vendors: [RegExp, string, string][] = [
  [/^claude-/i, 'anthropic-platform', 'Anthropic'],
  [/^gpt-/i, 'openai-platform', 'OpenAI'],
  [/^gemini-/i, 'google-gemini', 'Google'],
  [/^grok-/i, 'xai', 'xAI'],
  [/^muse-/i, 'meta', 'Meta'],
  [/^deepseek-/i, 'deepseek', 'DeepSeek'],
  [/^kimi-/i, 'moonshot', 'Moonshot'],
  [/^glm-/i, 'zhipu', 'Zhipu'],
  [/^qwen/i, 'dashscope', 'Qwen'],
  [/^minimax-/i, 'minimax', 'MiniMax'],
  [/^doubao-/i, 'volcengine', 'Doubao'],
  [/^(hy\d|hunyuan)/i, 'hunyuan', 'Hunyuan'],
  [/^step-/i, 'stepfun', 'StepFun'],
  [/^mimo-/i, 'mimo', 'MiMo'],
  [/^jev-/i, 'typesafe', 'TypeSafe'],
  [/^drex-/i, 'nace', 'Nace'],
]

// vendorOf 返回模型所属厂商的显示名，识别不了返回空串（筛选里归到“其他”）
export function vendorOf(name: string): string {
  return vendors.find(([pattern]) => pattern.test(name))?.[2] ?? ''
}

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
