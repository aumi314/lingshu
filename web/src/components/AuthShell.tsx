// 登录、注册共用的外壳：延续首页的墨色、北斗、远山，但不放首页的动态星图，表单是唯一的焦点。
// 背景纯 SVG + CSS（无 canvas），"减少动态效果"时静止。宋体字形来自首页的字体子集，
// 这里的中文改动后要运行 scripts/subset-font.py 重新生成。
import { useMemo, type ReactNode } from 'react'
import { ConfigProvider, theme } from 'antd'
import { Link } from 'react-router-dom'
import { useApp } from '../app'
import Logo from './Logo'
import '../welcome.css'
import '../auth.css'

// 表单固定用墨色主题，不跟随控制台的明暗设置（和首页一致）
const authTheme = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: '#4fd1b0',
    colorLink: '#6ee7c8',
    colorLinkHover: '#a8f5df',
    colorText: '#e9e4d8',
    colorTextPlaceholder: 'rgba(233, 228, 216, 0.32)',
    colorBgContainer: 'rgba(255, 255, 255, 0.035)',
    colorBorder: 'rgba(233, 228, 216, 0.13)',
    borderRadius: 12,
    controlHeightLG: 48,
    fontSizeLG: 15,
  },
}

// 北斗七星（天枢、天璇、天玑、天权、玉衡、开阳、摇光），坐标和首页 Meridian 一致
const DIPPER: [number, number][] = [[0, 0], [0.02, 0.33], [0.33, 0.43], [0.38, 0.19], [0.58, 0.17], [0.76, 0.21], [1, 0.4]]
const DIPPER_LINES = [[0, 1], [1, 2], [2, 3], [3, 0], [3, 4], [4, 5], [5, 6]]

// 固定种子的伪随机：每次渲染星空和山形都一样
function seeded(seed: number) {
  return () => ((seed = (seed * 16807) % 2147483647) - 1) / 2147483646
}

// 山脊：多层"脊状"起伏叠加（1 - |sin| 让峰顶变尖），频率逐层加倍、幅度减半，像水墨远山的棱线
function ridge(base: number, amp: number, seed: number) {
  const rnd = seeded(seed)
  const octaves = Array.from({ length: 6 }, (_, k) => ({ f: 0.0032 * Math.pow(2.1, k) * (0.8 + rnd() * 0.4), p: rnd() * Math.PI * 2, a: amp * Math.pow(0.48, k) }))
  let d = 'M0 900'
  for (let x = 0; x <= 1440; x += 6) d += `L${x} ${(base - octaves.reduce((s, o) => s + o.a * (1 - Math.abs(Math.sin(o.f * x + o.p))), 0)).toFixed(1)}`
  return d + 'L1440 900Z'
}

function Scene() {
  const { stars, mountains } = useMemo(() => {
    const rnd = seeded(7)
    const stars = Array.from({ length: 150 }, (_, i) => ({ x: rnd() * 1440, y: rnd() * 620, r: 0.35 + rnd() * 1.05, o: 0.18 + rnd() * 0.6, tw: i % 3 === 0, delay: rnd() * 6 }))
    const mountains = [ridge(790, 110, 3), ridge(845, 80, 11), ridge(895, 56, 23)]
    return { stars, mountains }
  }, [])
  const dipper = DIPPER.map(([x, y]) => [140 + x * 300, 132 + y * 150])
  return (
    <svg className="ls-auth-scene" viewBox="0 0 1440 900" preserveAspectRatio="xMidYMid slice" aria-hidden>
      <defs>
        <radialGradient id="auth-sky" cx="0.68" cy="0.45" r="0.7">
          <stop offset="0" stopColor="#0f1a19" />
          <stop offset="1" stopColor="#07080b" />
        </radialGradient>
        <linearGradient id="auth-m0" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#1d302d" stopOpacity="0.75" />
          <stop offset="1" stopColor="#07080b" stopOpacity="0.2" />
        </linearGradient>
        <linearGradient id="auth-m1" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#14211f" />
          <stop offset="1" stopColor="#07080b" />
        </linearGradient>
        <linearGradient id="auth-m2" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#0c1312" />
          <stop offset="1" stopColor="#07080b" />
        </linearGradient>
        <filter id="auth-blur" x="-20%" y="-50%" width="140%" height="200%">
          <feGaussianBlur stdDeviation="28" />
        </filter>
        <filter id="auth-glow" x="-200%" y="-200%" width="500%" height="500%">
          <feGaussianBlur stdDeviation="4" />
        </filter>
      </defs>
      <rect width="1440" height="900" fill="url(#auth-sky)" />
      {stars.map((s, i) => (
        <circle key={i} cx={s.x} cy={s.y} r={s.r} fill="#e9e4d8" opacity={s.o} className={s.tw ? 'ls-auth-twinkle' : undefined} style={s.tw ? { animationDelay: `${s.delay}s` } : undefined} />
      ))}
      <g className="ls-auth-dipper">
        {DIPPER_LINES.map(([a, b], i) => (
          <line key={i} x1={dipper[a][0]} y1={dipper[a][1]} x2={dipper[b][0]} y2={dipper[b][1]} pathLength={1} style={{ animationDelay: `${0.3 + i * 0.12}s` }} />
        ))}
        {dipper.map(([x, y], i) => (i === 0 ? null : <circle key={i} cx={x} cy={y} r={2} fill="#d4a95a" />))}
        <circle className="ls-auth-tianshu-halo" cx={dipper[0][0]} cy={dipper[0][1]} r={7} fill="#6ee7c8" filter="url(#auth-glow)" />
        <circle cx={dipper[0][0]} cy={dipper[0][1]} r={2.6} fill="#c9fbec" />
        <text x={dipper[0][0] + 12} y={dipper[0][1] - 8} className="ls-auth-star-label">天枢</text>
      </g>
      <path d={mountains[0]} fill="url(#auth-m0)" />
      <path d={mountains[0]} fill="none" stroke="rgba(110, 231, 200, 0.16)" strokeWidth="1" />
      <ellipse className="ls-auth-mist" cx="520" cy="760" rx="560" ry="46" fill="rgba(170, 220, 205, 0.07)" filter="url(#auth-blur)" />
      <path d={mountains[1]} fill="url(#auth-m1)" />
      <ellipse className="ls-auth-mist ls-auth-mist-2" cx="1050" cy="830" rx="620" ry="40" fill="rgba(170, 220, 205, 0.06)" filter="url(#auth-blur)" />
      <path d={mountains[2]} fill="url(#auth-m2)" />
    </svg>
  )
}

export default function AuthShell({ variant, subtitle, children, footer }: { variant: 'login' | 'register'; subtitle?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  const { t, lang, setPref } = useApp()
  const title = variant === 'login' ? t('欢迎回来', 'Welcome back') : t('创建账号', 'Create account')
  const headline: { text: string; accent?: boolean; br?: boolean }[] =
    lang === 'zh' ? [{ text: '一枢' }, { text: '通', accent: true }, { text: '百脉' }] : [{ text: 'One pivot,' }, { text: '', br: true }, { text: 'every ' }, { text: 'model', accent: true }, { text: '.' }]
  let n = 0
  return (
    <ConfigProvider theme={authTheme} button={{ autoInsertSpace: false }}>
      <div className="ls-auth">
        <Scene />
        <header className="ls-auth-nav">
          <Link to="/" className="ls-auth-brand" aria-label={t('返回首页', 'Back to home')}>
            <Logo size={30} />
            <span className="ls-auth-brand-name">
              灵枢<em>Lingshu</em>
            </span>
          </Link>
          <button type="button" className="ls-lang" onClick={() => setPref({ language: lang === 'zh' ? 'en' : 'zh' })}>
            {lang === 'zh' ? 'EN' : '中文'}
          </button>
        </header>
        <main className="ls-auth-main">
          <section className="ls-auth-intro" aria-hidden>
            <div className={`ls-eyebrow ls-rise${lang === 'en' ? ' ls-eyebrow-en' : ''}`}>{t('团队 AI 模型中枢', 'Team AI model gateway')}</div>
            <div className={`ls-auth-headline${lang === 'en' ? ' ls-auth-headline-en' : ''}`}>
              {headline.map((p, k) => p.br ? <br key={k} /> : (
                <span key={k} className={p.accent ? 'ls-accent' : undefined}>
                  {[...p.text].map((ch) => (
                    <span key={n} className="ls-ink" style={{ animationDelay: `${0.15 + n++ * 0.09}s` }}>
                      {ch === ' ' ? '\u00a0' : ch}
                    </span>
                  ))}
                </span>
              ))}
            </div>
            <p className="ls-auth-lede ls-rise" style={{ animationDelay: '0.7s' }}>
              {t('一个地址、一把 key，调用各家模型；每一笔花费都看得见。', 'One endpoint and one key for every model, with every cost in plain sight.')}
            </p>
          </section>
          <section className="ls-auth-panel ls-rise" style={{ animationDelay: '0.1s' }}>
            <h1 className="ls-auth-title">{title}</h1>
            {subtitle && <p className="ls-auth-subtitle">{subtitle}</p>}
            {children}
            {footer && <div className="ls-auth-foot">{footer}</div>}
          </section>
        </main>
      </div>
    </ConfigProvider>
  )
}
