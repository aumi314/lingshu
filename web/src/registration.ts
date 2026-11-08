// 注册开关兼容层：优先读独立布尔，缺失时回退到旧的单一 mode 字段（浏览器 mock 和未升级的接口仍可用）。
export type RegistrationFlags = { enabled: boolean; code_required: boolean; email_required: boolean }

export function normalizeRegistration(input: {
  enabled?: boolean
  code_required?: boolean
  email_required?: boolean
  mode?: string
}): RegistrationFlags {
  const mode = input.mode ?? 'manual'
  const code = mode === 'code' || mode === 'both'
  const email = mode === 'email' || mode === 'both'
  return {
    enabled: input.enabled ?? (code || email),
    code_required: input.code_required ?? code,
    email_required: input.email_required ?? email,
  }
}
