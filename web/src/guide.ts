// 概览页的上手教程与接入配置。现在写死在这里；以后做成后台可编辑时，把这些数据改成从设置读取即可，页面按数据渲染不用动。
// 各工具写法核对过官方文档（2026-11-05）：
// - Claude Code：ANTHROPIC_BASE_URL 填根地址；ANTHROPIC_AUTH_TOKEN 以 Authorization: Bearer 发送（code.claude.com/docs/en/env-vars）
// - Codex：~/.codex/config.toml 自定义 model_providers，wire_api 只支持 responses，openai 等 ID 保留（Codex config reference）
// - Magpie：一键导入链接 https://usemagpie.ai/import#…，参数在 # 后不经服务器（usemagpie.ai/docs/zh/import）

export type GuideClient = {
  key: string
  label: string
  recommended?: boolean
  intro: [string, string] // [中文, English]
  file?: string // 配置写在哪
  code?: (urls: GuideURLs) => string
  notes: [string, string][]
}

export type GuideURLs = { base: string; root: string } // base 带 /v1，root 是根地址

export const KEY_PLACEHOLDER = 'sk-ls-你的key'

// Magpie 导入链接：chat、responses 带 /v1，anthropic 用根地址。key 只在本人能看到的页面（刚创建 key 的弹窗）里带上。
export function magpieImportURL(urls: GuideURLs, key?: string): string {
  const params = new URLSearchParams({ name: 'Lingshu', id: 'lingshu', chat: urls.base, responses: urls.base, anthropic: urls.root })
  if (window.location.protocol === 'https:') params.set('website', window.location.origin)
  if (key) params.set('key', key)
  return `https://usemagpie.ai/import#${params.toString()}`
}

export const guideClients: GuideClient[] = [
  {
    key: 'magpie',
    label: 'Magpie',
    recommended: true,
    intro: [
      '统一管理 Claude Code、Codex 等 Agent 用哪家模型的开源工具。装好 Magpie 后点下面的按钮，地址和协议自动填好，只需粘贴你的 key。',
      'An open-source manager for which models your agents (Claude Code, Codex, …) use. With Magpie installed, click the button below: URLs and protocols are filled in, you only paste your key.',
    ],
    notes: [
      ['创建 API key 时的弹窗里也有这个按钮，会连同 key 一起导入。', 'The dialog shown right after you create an API key has this button too, and imports the key as well.'],
      ['导入前 Magpie 会弹窗让你确认，不会静默覆盖已有配置。', 'Magpie asks you to confirm before saving and never silently overwrites an existing provider.'],
    ],
  },
  {
    key: 'claude-code',
    label: 'Claude Code',
    intro: ['把下面内容合并进 Claude Code 的设置文件，重开会话生效。', 'Merge this into your Claude Code settings file and start a new session.'],
    file: '~/.claude/settings.json',
    code: ({ root }) => `{
  "env": {
    "ANTHROPIC_BASE_URL": "${root}",
    "ANTHROPIC_AUTH_TOKEN": "${KEY_PLACEHOLDER}"
  }
}`,
    notes: [
      ['地址填根地址，不要加 /v1，Claude Code 会自己拼。', 'Use the root URL without /v1; Claude Code appends the path itself.'],
      ['用 ANTHROPIC_AUTH_TOKEN，不用 ANTHROPIC_API_KEY：后者在登录了 Claude 订阅时会提示覆盖订阅。', 'Use ANTHROPIC_AUTH_TOKEN, not ANTHROPIC_API_KEY, which prompts to override a signed-in Claude subscription.'],
      ['只能用支持 Messages 协议的模型，在「模型与价格」页看协议标签。', 'Only models with the Messages protocol work; check the protocol tags on Models & pricing.'],
    ],
  },
  {
    key: 'codex',
    label: 'Codex',
    intro: ['在 Codex 的用户配置里加一个服务商，再把 key 放进环境变量。', 'Add a provider to your Codex user config and put the key in an environment variable.'],
    file: '~/.codex/config.toml',
    code: ({ base }) => `model = "换成支持 Responses 协议的模型"
model_provider = "lingshu"

[model_providers.lingshu]
name = "Lingshu"
base_url = "${base}"
env_key = "LINGSHU_API_KEY"

# 终端里：export LINGSHU_API_KEY=${KEY_PLACEHOLDER}`,
    notes: [
      ['Codex 只走 Responses 协议，模型必须在「模型与价格」页标有 Responses。', 'Codex only speaks the Responses protocol; pick a model tagged Responses on Models & pricing.'],
      ['写在用户级 ~/.codex/config.toml，项目里的 .codex/config.toml 不认服务商设置。', 'Use the user-level ~/.codex/config.toml; project-level files ignore provider settings.'],
    ],
  },
  {
    key: 'sdk',
    label: 'SDK',
    intro: ['在代码里调用：OpenAI SDK 填带 /v1 的地址，Anthropic SDK 填根地址。', 'In code: the OpenAI SDK takes the URL with /v1, the Anthropic SDK takes the root URL.'],
    code: ({ base, root }) => `# OpenAI SDK（Chat Completions / Responses）
from openai import OpenAI
client = OpenAI(base_url="${base}", api_key="${KEY_PLACEHOLDER}")

# Anthropic SDK（Messages）
import anthropic
client = anthropic.Anthropic(base_url="${root}", api_key="${KEY_PLACEHOLDER}")`,
    notes: [['模型名用「模型与价格」页里的名字。', 'Use model names exactly as listed on Models & pricing.']],
  },
]
