// 用户协议和隐私政策：管理员在主设置里填写，公开接口 /api/auth/legal 返回，注册页按需强制勾选。
import { get } from './api'

export type LegalDocs = {
  user_agreement: string
  privacy_policy: string
  version: string
}

/** 任一文档有内容才需要用户勾选同意；只有空白也算没配置，这时不显示勾选框。正文原样使用，不做修改 */
export const legalRequired = (docs: LegalDocs | null): boolean =>
  Boolean((docs?.user_agreement ?? '').trim() || (docs?.privacy_policy ?? '').trim())

export async function fetchLegal(): Promise<LegalDocs> {
  const raw = await get<unknown>('/api/auth/legal')
  // 返回结构不对就当协议读不到：拦住注册，不让用户在没有条款的情况下注册。
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('用户协议和隐私政策读取失败，暂时不能注册')
  const body = raw as Record<string, unknown>
  const { user_agreement, privacy_policy, version } = body as { user_agreement?: unknown; privacy_policy?: unknown; version?: unknown }
  if (typeof user_agreement !== 'string' || typeof privacy_policy !== 'string' || typeof version !== 'string' || !version) {
    throw new Error('用户协议和隐私政策读取失败，暂时不能注册')
  }
  return { user_agreement, privacy_policy, version }
}
