export type StandardBases = {
  base_url_chat: string
  base_url_messages: string
  base_url_responses: string
}

export type StandardRootResult = { root: string; error?: never } | { root?: never; error: 'required' | 'invalid' | 'path' }

export function parseStandardRoot(input: string): StandardRootResult {
  const root = input.trim().replace(/\/+$/, '')
  if (!root) return { error: 'required' }

  let parsed: URL
  try {
    parsed = new URL(root)
  } catch {
    return { error: 'invalid' }
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return { error: 'invalid' }
  if (parsed.username || parsed.password) return { error: 'invalid' }
  if (parsed.pathname !== '/' || root.includes('?') || root.includes('#')) return { error: 'path' }
  return { root }
}

export function standardBases(root: string): StandardBases {
  const parsed = parseStandardRoot(root)
  if (!parsed.root) throw new Error(parsed.error)
  return {
    base_url_chat: `${parsed.root}/v1`,
    base_url_messages: parsed.root,
    base_url_responses: `${parsed.root}/v1`,
  }
}

export function recoverStandardRoot(bases: StandardBases): string | undefined {
  const parsed = parseStandardRoot(bases.base_url_messages)
  if (!parsed.root) return undefined
  const chat = bases.base_url_chat.trim().replace(/\/+$/, '')
  const responses = bases.base_url_responses.trim().replace(/\/+$/, '')
  return chat === `${parsed.root}/v1` && responses === `${parsed.root}/v1` ? parsed.root : undefined
}

// 只填域名根地址、自动生成三种协议地址的模板：标准模板和中转面板（sub2api、new-api，另外能查余额）
export const usesRootURL = (template?: string) => ['standard', 'sub2api', 'new-api'].includes(template ?? '')
