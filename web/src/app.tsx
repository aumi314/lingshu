// 全局状态：当前用户、偏好（语言 / 货币 / 主题）、汇率，以及据此生成的翻译和金额格式化函数。
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { get, put, type Preferences, type User } from './api'

type Lang = 'zh' | 'en'
type Currency = 'USD' | 'CNY'
type ThemeMode = 'system' | 'light' | 'dark'

export type AppState = {
  user: User | null
  loading: boolean
  rate: number // 1 USD = rate CNY
  lang: Lang
  currency: Currency
  theme: ThemeMode
  dark: boolean // 实际生效的主题
  reload: () => Promise<void>
  setPref: (p: Preferences) => Promise<void>
  /** 中英文写在调用处：t('余额', 'Balance') */
  t: (zh: string, en: string) => string
  /** 把 USD 金额按当前货币显示，digits 是小数位 */
  money: (usd: string | number, digits?: number) => string
}

const Ctx = createContext<AppState | null>(null)

export function useApp(): AppState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useApp 必须在 AppProvider 里使用')
  return v
}

// 没登录时（登录页）沿用上次的偏好
const LOCAL_KEY = 'lingshu.prefs'
function localPrefs(): Preferences {
  try {
    return JSON.parse(localStorage.getItem(LOCAL_KEY) ?? '{}')
  } catch {
    return {}
  }
}

function useSystemDark(): boolean {
  const query = '(prefers-color-scheme: dark)'
  const [dark, setDark] = useState(() => window.matchMedia(query).matches)
  useEffect(() => {
    const mq = window.matchMedia(query)
    const on = (e: MediaQueryListEvent) => setDark(e.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return dark
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const [rate, setRate] = useState(6.7)
  const [prefs, setPrefs] = useState<Preferences>(localPrefs)
  const systemDark = useSystemDark()

  const reload = useCallback(async () => {
    try {
      const me = await get<{ user: User; cny_per_usd: string }>('/api/me')
      setUser(me.user)
      setRate(Number(me.cny_per_usd))
      const p = { ...localPrefs(), ...me.user.preferences }
      setPrefs(p)
      localStorage.setItem(LOCAL_KEY, JSON.stringify(p))
    } catch {
      setUser(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    reload()
    const onUnauthorized = () => setUser(null)
    window.addEventListener('lingshu:unauthorized', onUnauthorized)
    return () => window.removeEventListener('lingshu:unauthorized', onUnauthorized)
  }, [reload])

  const setPref = useCallback(
    async (p: Preferences) => {
      const next = { ...prefs, ...p }
      setPrefs(next) // 先生效，再保存
      localStorage.setItem(LOCAL_KEY, JSON.stringify(next))
      if (user) await put('/api/me/preferences', p)
    },
    [prefs, user],
  )

  const lang: Lang = prefs.language ?? 'zh'
  const currency: Currency = prefs.currency ?? 'USD'
  const theme: ThemeMode = prefs.theme ?? 'system'
  const dark = theme === 'dark' || (theme === 'system' && systemDark)

  useEffect(() => {
    document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
  }, [lang])

  const value = useMemo<AppState>(() => {
    const t = (zh: string, en: string) => (lang === 'zh' ? zh : en)
    const money = (usd: string | number, digits = 4) => {
      const n = Number(usd) * (currency === 'CNY' ? rate : 1)
      const sign = n < 0 ? '-' : ''
      return `${sign}${currency === 'CNY' ? '¥' : '$'}${Math.abs(n).toFixed(digits)}`
    }
    return { user, loading, rate, lang, currency, theme, dark, reload, setPref, t, money }
  }, [user, loading, rate, lang, currency, theme, dark, reload, setPref])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

/** 每百万 token 的单价：去掉多余的 0，最多 6 位小数 */
export function priceText(money: AppState['money'], usd: string): string {
  const s = money(usd, 6)
  return s.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '')
}

export function tokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M'
  if (n >= 10_000) return (n / 1000).toFixed(1) + 'K'
  return n.toLocaleString()
}
