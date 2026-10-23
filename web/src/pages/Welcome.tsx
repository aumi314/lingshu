// 首页：不需要登录，不请求任何数据。视觉取《灵枢》经络之意——一枢通百脉。
import { useEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { useApp } from '../app'
import Logo from '../components/Logo'
import Meridian from '../components/Meridian'
import '../welcome.css'

type Snippet = { id: string; label: string; code: string }

// 极简高亮：注释变暗，引号里的内容上色，行首的命令提示符着色
function highlight(code: string) {
  return code.split('\n').map((line, i) => {
    if (line.trimStart().startsWith('#')) return <div key={i} className="c-comment">{line}</div>
    const parts = line.split(/("[^"]*"|'[^']*')/g)
    return (
      <div key={i}>
        {parts.map((p, j) =>
          /^["']/.test(p) ? <span key={j} className="c-str">{p}</span> : j === 0 && p.startsWith('$ ') ? <span key={j}><span className="c-prompt">$ </span>{p.slice(2)}</span> : p,
        )}
      </div>
    )
  })
}

const reducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches

// 滚动到视野里时给 [data-reveal] 加上 is-in，触发 CSS 里的浮现动画
function useReveal() {
  useEffect(() => {
    const els = document.querySelectorAll('[data-reveal]')
    if (reducedMotion()) {
      els.forEach((el) => el.classList.add('is-in'))
      return
    }
    const io = new IntersectionObserver(
      (entries) => entries.forEach((e) => e.isIntersecting && (e.target.classList.add('is-in'), io.unobserve(e.target))),
      { threshold: 0.2 },
    )
    els.forEach((el) => io.observe(el))
    return () => io.disconnect()
  })
}

// 标题逐字晕开，像墨滴进宣纸
function InkTitle({ parts }: { parts: { text: string; accent?: boolean; br?: boolean }[] }) {
  let i = 0
  return (
    <>
      {parts.map((p, k) =>
        p.br ? (
          <br key={k} />
        ) : (
          <span key={k} className={p.accent ? 'ls-accent' : undefined}>
            {[...p.text].map((ch) => (
              <span key={i} className="ls-ink" style={{ animationDelay: `${0.2 + i++ * 0.11}s` }}>
                {ch === ' ' ? '\u00a0' : ch}
              </span>
            ))}
          </span>
        ),
      )}
    </>
  )
}

// 进入视野后从 0 滚到目标值
function CountUp({ to }: { to: number }) {
  const ref = useRef<HTMLSpanElement>(null)
  const [n, setN] = useState(reducedMotion() ? to : 0)
  useEffect(() => {
    if (reducedMotion()) return
    const el = ref.current!
    let raf = 0
    const io = new IntersectionObserver(([e]) => {
      if (!e.isIntersecting) return
      io.disconnect()
      const start = performance.now()
      const step = (now: number) => {
        const t = Math.min(1, (now - start) / 1400)
        setN(Math.round(to * (1 - Math.pow(1 - t, 3))))
        if (t < 1) raf = requestAnimationFrame(step)
      }
      raf = requestAnimationFrame(step)
    })
    io.observe(el)
    return () => (io.disconnect(), cancelAnimationFrame(raf))
  }, [to])
  return <span ref={ref}>{n}</span>
}

// 终端里逐字打出代码；换标签页时重新打
function Typewriter({ code, render }: { code: string; render: (s: string) => ReactNode }) {
  const ref = useRef<HTMLPreElement>(null)
  const [shown, setShown] = useState(reducedMotion() ? code.length : 0)
  const [visible, setVisible] = useState(false)
  useEffect(() => {
    const io = new IntersectionObserver(([e]) => e.isIntersecting && setVisible(true), { threshold: 0.3 })
    io.observe(ref.current!)
    return () => io.disconnect()
  }, [])
  useEffect(() => {
    if (reducedMotion()) return setShown(code.length)
    if (!visible) return
    setShown(0)
    let i = 0
    const id = setInterval(() => {
      i += Math.max(1, Math.round(code.length / 70))
      setShown(Math.min(i, code.length))
      if (i >= code.length) clearInterval(id)
    }, 16)
    return () => clearInterval(id)
  }, [code, visible])
  return (
    <pre ref={ref} className="ls-code">
      {render(code.slice(0, shown))}
      <span className={`ls-caret${shown >= code.length ? ' done' : ''}`} aria-hidden="true" />
    </pre>
  )
}

// 卡片边框的光跟着鼠标走
const trackGlow = (e: MouseEvent<HTMLElement>) => {
  const r = e.currentTarget.getBoundingClientRect()
  e.currentTarget.style.setProperty('--gx', `${e.clientX - r.left}px`)
  e.currentTarget.style.setProperty('--gy', `${e.clientY - r.top}px`)
}

export default function Welcome() {
  const { user, t, lang, setPref } = useApp()
  const nav = useNavigate()
  const base = window.location.origin
  const [tab, setTab] = useState('claude')
  const [copied, setCopied] = useState('')

  const enter = () => nav(user ? '/console' : '/login')
  useReveal()
  const copy = async (text: string, id: string) => {
    await navigator.clipboard.writeText(text).catch(() => {})
    setCopied(id)
    setTimeout(() => setCopied(''), 1500)
  }

  const snippets: Snippet[] = [
    {
      id: 'claude',
      label: 'Claude Code',
      code: `# ${t('换成灵枢的地址和 key，其余照旧', 'Point at Lingshu, everything else stays the same')}
$ export ANTHROPIC_BASE_URL="${base}"
$ export ANTHROPIC_AUTH_TOKEN="sk-ls-..."
$ claude --model qwen3.8-max`,
    },
    {
      id: 'codex',
      label: 'Codex',
      code: `$ export LINGSHU_KEY="sk-ls-..."
$ codex -m deepseek-v4.1-flash \\
    -c model_provider=lingshu \\
    -c 'model_providers.lingshu={name="Lingshu",base_url="${base}/v1",env_key="LINGSHU_KEY",wire_api="responses"}'`,
    },
    {
      id: 'curl',
      label: 'curl',
      code: `$ curl ${base}/v1/chat/completions \\
    -H "Authorization: Bearer $LINGSHU_KEY" \\
    -H "Content-Type: application/json" \\
    -d '{"model": "GLM-5.3", "stream": true, "messages": [{"role": "user", "content": "${t('你好', 'Hello')}"}]}'`,
    },
    {
      id: 'python',
      label: 'Python',
      code: `from openai import OpenAI

client = OpenAI(base_url="${base}/v1", api_key="sk-ls-...")
resp = client.chat.completions.create(
    model="kimi-k3",
    messages=[{"role": "user", "content": "${t('你好', 'Hello')}"}],
)`,
    },
  ]
  const current = snippets.find((s) => s.id === tab)!

  const veins = [
    {
      num: '壹',
      char: '通',
      title: t('一枢通百脉', 'One pivot, every channel'),
      text: t(
        'DeepSeek、GLM、Kimi、Qwen、MiMo、StepFun 汇于一枢。一个地址、一把 key，不用再管各家的账号和余额。',
        'DeepSeek, GLM, Kimi, Qwen, MiMo and StepFun meet at one pivot. One endpoint, one key — no more juggling vendor accounts.',
      ),
    },
    {
      num: '贰',
      char: '透',
      title: t('原样透传，分毫不改', 'Passed through, untouched'),
      text: t(
        'Chat Completions、Responses、Anthropic Messages、System One 四种协议原样转发。聊天和决策请求都用同一把 key。',
        'Chat Completions, Responses, Anthropic Messages and System One, forwarded as-is. One key for chat and decision requests.',
      ),
    },
    {
      num: '叁',
      char: '明',
      title: t('一笔一账，清清楚楚', 'Every call, accounted for'),
      text: t(
        '每次调用的用量、耗时、花费都记在账上，精确到十位小数。哪个渠道出错自动换下一个，用户无感。',
        'Tokens, latency and cost for every call, to ten decimal places. A failing channel hands off to the next without anyone noticing.',
      ),
    },
  ]

  return (
    <div className="ls-landing">
      <header className="ls-nav">
        <div className="ls-brand">
          <Logo size={34} />
          <span className="ls-brand-name">
            灵枢 <em>Lingshu</em>
          </span>
        </div>
        <div className="ls-nav-actions">
          <button className="ls-lang" onClick={() => setPref({ language: lang === 'zh' ? 'en' : 'zh' })}>
            {lang === 'zh' ? 'EN' : '中'}
          </button>
          <button className="ls-btn ls-btn-ghost" onClick={enter}>
            {user ? t('进入控制台', 'Open console') : t('登录', 'Sign in')}
          </button>
        </div>
      </header>

      <section className="ls-hero">
        <Meridian />
        <div className="ls-hero-text">
          <div className={`ls-eyebrow ls-rise${lang === 'en' ? ' ls-eyebrow-en' : ''}`} style={{ animationDelay: '0.05s' }}>
            {t('团队 AI 模型中枢', 'The team AI model gateway')}
          </div>
          <h1 key={lang} className={`ls-title${lang === 'en' ? ' ls-title-en' : ''}`} aria-label={t('一枢通百脉', 'One pivot, every model.')}>
            {lang === 'zh' ? (
              <InkTitle parts={[{ text: '一枢' }, { text: '通', accent: true }, { text: '百脉' }]} />
            ) : (
              <InkTitle parts={[{ text: 'One pivot,' }, { text: '', br: true }, { text: 'every ' }, { text: 'model', accent: true }, { text: '.' }]} />
            )}
          </h1>
          <p className="ls-lede ls-rise" style={{ animationDelay: '0.85s' }}>
            {t(
              '灵枢把各家模型汇到一处。团队只需要一个地址、一把 key，调用体验和直连官方一模一样，每一笔花费都看得见。',
              'Lingshu brings every vendor\'s models to one place. One endpoint and one key for the whole team — feels exactly like calling the vendor directly, with every cent on the record.',
            )}
          </p>
          <div className="ls-cta ls-rise" style={{ animationDelay: '1s' }}>
            <button className="ls-btn ls-btn-primary" onClick={enter}>
              {user ? t('进入控制台', 'Open console') : t('登录', 'Sign in')}
              <span aria-hidden="true">→</span>
            </button>
            <button className="ls-btn ls-btn-ghost" onClick={() => copy(`${base}/v1`, 'hero')}>
              <code>{base.replace(/^https?:\/\//, '')}/v1</code>
              <span className="ls-copy-hint">{copied === 'hero' ? t('已复制', 'Copied') : t('复制', 'Copy')}</span>
            </button>
          </div>
        </div>
        <div className="ls-scroll-hint" aria-hidden="true" />
      </section>

      <section className="ls-stats" data-reveal>
        {([
          [4, t('种协议', 'protocols')],
          [6, t('家厂商', 'vendors')],
          [1, t('把 key', 'key')],
          [10, t('位小数记账', 'decimal places')],
        ] as const).map(([n, label], i) => (
          <div key={label} className="ls-stat" style={{ transitionDelay: `${i * 0.08}s` }}>
            <span className="ls-stat-num">
              <CountUp to={n} />
            </span>
            <span className="ls-stat-label">{label}</span>
          </div>
        ))}
      </section>

      <section className="ls-veins" data-reveal>
        {veins.map((v, i) => (
          <article key={v.num} className="ls-vein" onMouseMove={trackGlow} style={{ transitionDelay: `${i * 0.12}s` }}>
            <div className="ls-vein-head">
              <span className="ls-vein-num">{v.num}</span>
              <span className="ls-vein-char">{v.char}</span>
            </div>
            <h3>{v.title}</h3>
            <p>{v.text}</p>
          </article>
        ))}
      </section>

      <section className="ls-connect" data-reveal>
        <div className="ls-connect-text">
          <h2>{t('只换一个地址', 'Change one line')}</h2>
          <p>
            {t(
              '在控制台创建 API key，把官方地址换成灵枢的地址。已有的代码、工具和工作流不用动。',
              'Create an API key in the console and swap the vendor\'s endpoint for Lingshu\'s. Your code, tools and workflows stay as they are.',
            )}
          </p>
          <button className="ls-btn ls-btn-primary" onClick={enter}>
            {user ? t('去创建 key', 'Create a key') : t('登录后创建 key', 'Sign in to create a key')}
            <span aria-hidden="true">→</span>
          </button>
        </div>
        <div className="ls-terminal">
          <div className="ls-terminal-bar">
            <span className="ls-dots" aria-hidden="true">
              <i />
              <i />
              <i />
            </span>
            <div className="ls-tabs" role="tablist">
              {snippets.map((s) => (
                <button key={s.id} role="tab" aria-selected={tab === s.id} className={tab === s.id ? 'active' : ''} onClick={() => setTab(s.id)}>
                  {s.label}
                </button>
              ))}
            </div>
            <button className="ls-copy" onClick={() => copy(current.code.replace(/^\$ /gm, ''), current.id)}>
              {copied === current.id ? t('已复制', 'Copied') : t('复制', 'Copy')}
            </button>
          </div>
          <Typewriter code={current.code} render={highlight} />
        </div>
      </section>

      <footer className="ls-footer">
        <blockquote data-reveal>
          <p>经脉者，所以能决死生，处百病，调虚实，不可不通。</p>
          <cite>——《灵枢·经脉》</cite>
        </blockquote>
        <div className="ls-footer-line">
          <span>灵枢 Lingshu</span>
          <span>{t('团队内部使用 · 账号由管理员创建', 'Internal use · accounts created by admins')}</span>
        </div>
      </footer>
    </div>
  )
}
