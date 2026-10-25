import VendorIcon from './VendorIcon'

const vendors: [RegExp, string][] = [
  [/^glm-/i, 'zhipu'],
  [/^minimax-/i, 'minimax'],
  [/^claude-/i, 'anthropic-platform'],
  [/^deepseek-/i, 'deepseek'],
  [/^doubao-/i, 'volcengine'],
  [/^drex-/i, 'nace'],
  [/^gemini-/i, 'google-gemini'],
  [/^gpt-/i, 'openai-platform'],
  [/^grok-/i, 'xai'],
  [/^(hy\d|hunyuan)/i, 'hunyuan'],
  [/^jev-/i, 'typesafe'],
  [/^kimi-/i, 'moonshot'],
  [/^mimo-/i, 'mimo'],
  [/^muse-/i, 'meta'],
  [/^qwen/i, 'dashscope'],
  [/^step-/i, 'stepfun'],
]

export default function ModelIcon({ name }: { name: string }) {
  const template = vendors.find(([pattern]) => pattern.test(name))?.[1]
  if (!template) return null
  return (
    <span aria-hidden style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: 18, height: 18, flexShrink: 0 }}>
      <VendorIcon template={template} size={18} />
    </span>
  )
}
