// 首页：不需要登录，不请求任何数据。视觉取《灵枢》经络之意——一枢通百脉。
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useApp } from '../app'
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

export default function Welcome() {
  const { user, t, lang, setPref } = useApp()
  const nav = useNavigate()
  const base = window.location.origin
  const [tab, setTab] = useState('claude')
  const [copied, setCopied] = useState('')

  const enter = () => nav(user ? '/console' : '/login')
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
        'Chat Completions、Responses、Anthropic Messages 三种协议原样转发。思考、工具调用、提示缓存都和直连官方一样。',
        'Chat Completions, Responses and Anthropic Messages, forwarded as-is. Reasoning, tool calls and prompt caching behave exactly like going direct.',
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
          <span className="ls-seal" aria-hidden="true">
            <span>灵</span>
            <span>枢</span>
          </span>
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
        <Meridian legend={{ live: t('已接入', 'Live'), soon: t('即将接入', 'Coming soon') }} />
        <div className="ls-hero-text">
          <div className={`ls-eyebrow ls-rise${lang === 'en' ? ' ls-eyebrow-en' : ''}`} style={{ animationDelay: '0.05s' }}>
            {t('团队 AI 模型中枢', 'The team AI model gateway')}
          </div>
          <h1 className={`ls-title ls-rise${lang === 'en' ? ' ls-title-en' : ''}`} style={{ animationDelay: '0.15s' }}>
            {lang === 'zh' ? (
              <>
                一枢<span className="ls-accent">通</span>百脉
              </>
            ) : (
              <>
                One pivot,
                <br />
                every <span className="ls-accent">model</span>.
              </>
            )}
          </h1>
          <p className="ls-lede ls-rise" style={{ animationDelay: '0.3s' }}>
            {t(
              '灵枢把各家模型汇到一处。团队只需要一个地址、一把 key，调用体验和直连官方一模一样，每一笔花费都看得见。',
              'Lingshu brings every vendor\'s models to one place. One endpoint and one key for the whole team — feels exactly like calling the vendor directly, with every cent on the record.',
            )}
          </p>
          <div className="ls-cta ls-rise" style={{ animationDelay: '0.45s' }}>
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

      <section className="ls-stats">
        {[
          ['3', t('种协议', 'protocols')],
          ['6', t('家厂商', 'vendors')],
          ['1', t('把 key', 'key')],
          ['10', t('位小数记账', 'decimal places')],
        ].map(([n, label]) => (
          <div key={label} className="ls-stat">
            <span className="ls-stat-num">{n}</span>
            <span className="ls-stat-label">{label}</span>
          </div>
        ))}
      </section>

      <section className="ls-veins">
        {veins.map((v) => (
          <article key={v.num} className="ls-vein">
            <div className="ls-vein-head">
              <span className="ls-vein-num">{v.num}</span>
              <span className="ls-vein-char">{v.char}</span>
            </div>
            <h3>{v.title}</h3>
            <p>{v.text}</p>
          </article>
        ))}
      </section>

      <section className="ls-connect">
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
          <pre className="ls-code">{highlight(current.code)}</pre>
        </div>
      </section>

      <footer className="ls-footer">
        <blockquote>
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
