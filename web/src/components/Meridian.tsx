// 首页主视觉"经络图"：调用方的请求像气血一样沿经脉流入中枢，再分流到各家模型。
// 纯 canvas 绘制，无依赖；系统开启"减少动态效果"时只画静止的一帧。
import { useEffect, useRef } from 'react'

// 调用方（Claude Code、Codex、各类 SDK）在画面左边缘外，只画流进来的光点
const CLIENTS = [0, 1, 2, 3]
const MODELS = ['DeepSeek', 'GLM', 'Kimi', 'Qwen', 'MiMo', 'StepFun']

const JADE = [110, 231, 200]
const CINNABAR = [226, 72, 61]
const GOLD = [212, 169, 90]
const INK = '7, 8, 11'

type Pt = { x: number; y: number }
type Particle = { leg: 0 | 1; t: number; speed: number; client: number; model: number; color: number[] }

function bezier(a: Pt, c: Pt, b: Pt, t: number): Pt {
  const u = 1 - t
  return { x: u * u * a.x + 2 * u * t * c.x + t * t * b.x, y: u * u * a.y + 2 * u * t * c.y + t * t * b.y }
}

const rgba = (c: number[], a: number) => `rgba(${c[0]},${c[1]},${c[2]},${a})`

export default function Meridian() {
  const ref = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = ref.current!
    const ctx = canvas.getContext('2d')!
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    let w = 0, h = 0, raf = 0, last = performance.now()
    let spin = 0
    const glow = MODELS.map(() => 0)
    const particles: Particle[] = []

    const resize = () => {
      const dpr = Math.min(window.devicePixelRatio || 1, 2)
      const r = canvas.getBoundingClientRect()
      w = r.width
      h = r.height
      canvas.width = w * dpr
      canvas.height = h * dpr
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      ctx.fillStyle = `rgb(${INK})`
      ctx.fillRect(0, 0, w, h)
    }
    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(canvas)

    // 布局：请求从画面左边缘外流入中枢，模型绕中枢缓慢公转。
    // 宽屏时中枢靠右，给左侧文字留出空间；窄屏时中枢在上方，文字在下方。
    const layout = () => {
      const narrow = w < 860
      const pivot = narrow ? { x: w * 0.5, y: h * 0.3 } : { x: w * 0.7, y: h * 0.5 }
      const R = narrow ? Math.min(w * 0.34, h * 0.2) : Math.min(w * 0.24, h * 0.36)
      const clients = CLIENTS.map((_, i) => ({
        x: -40,
        y: narrow ? h * (0.04 + (i * 0.5) / (CLIENTS.length - 1)) : h * (0.12 + (i * 0.76) / (CLIENTS.length - 1)),
      }))
      const models = MODELS.map((_, i) => {
        const a = spin * 0.08 + (i / MODELS.length) * Math.PI * 2 - Math.PI / 2
        return { x: pivot.x + Math.cos(a) * R * 0.95, y: pivot.y + Math.sin(a) * R * 0.95 }
      })
      return { pivot, R, clients, models }
    }

    const ctrlIn = (a: Pt, p: Pt): Pt => ({ x: (a.x + p.x) / 2, y: a.y + (p.y - a.y) * 0.15 })
    const ctrlOut = (p: Pt, m: Pt): Pt => ({ x: (p.x + m.x) / 2 + (m.y - p.y) * 0.25, y: (p.y + m.y) / 2 - (m.x - p.x) * 0.25 })

    const spawn = () => {
      const r = Math.random()
      particles.push({
        leg: 0,
        t: 0,
        speed: 0.28 + Math.random() * 0.25,
        client: Math.floor(Math.random() * CLIENTS.length),
        model: Math.floor(Math.random() * MODELS.length),
        color: r < 0.72 ? JADE : r < 0.9 ? GOLD : CINNABAR,
      })
    }

    const draw = (dt: number) => {
      const { pivot, R, clients, models } = layout()

      // 拖尾：每帧盖一层半透明的墨色
      ctx.globalCompositeOperation = 'source-over'
      ctx.fillStyle = `rgba(${INK}, ${reduced ? 1 : 0.22})`
      ctx.fillRect(0, 0, w, h)

      // 经脉
      ctx.lineWidth = 1
      for (const c of clients) {
        ctx.strokeStyle = rgba(JADE, 0.09)
        ctx.beginPath()
        const k = ctrlIn(c, pivot)
        ctx.moveTo(c.x, c.y)
        ctx.quadraticCurveTo(k.x, k.y, pivot.x, pivot.y)
        ctx.stroke()
      }
      models.forEach((m) => {
        ctx.strokeStyle = rgba(JADE, 0.08)
        ctx.beginPath()
        const k = ctrlOut(pivot, m)
        ctx.moveTo(pivot.x, pivot.y)
        ctx.quadraticCurveTo(k.x, k.y, m.x, m.y)
        ctx.stroke()
      })

      // 中枢：罗盘式的同心圆和二十四道刻度
      ctx.save()
      ctx.translate(pivot.x, pivot.y)
      const halo = ctx.createRadialGradient(0, 0, 0, 0, 0, R * 0.55)
      halo.addColorStop(0, rgba(JADE, 0.16))
      halo.addColorStop(1, rgba(JADE, 0))
      ctx.fillStyle = halo
      ctx.beginPath()
      ctx.arc(0, 0, R * 0.55, 0, Math.PI * 2)
      ctx.fill()
      for (const [r, a] of [[R * 0.42, 0.18], [R * 0.3, 0.28], [R * 0.17, 0.5]] as const) {
        ctx.strokeStyle = rgba(JADE, a)
        ctx.beginPath()
        ctx.arc(0, 0, r, 0, Math.PI * 2)
        ctx.stroke()
      }
      ctx.rotate(spin * 0.15)
      for (let i = 0; i < 24; i++) {
        const major = i % 6 === 0
        ctx.strokeStyle = major ? rgba(CINNABAR, 0.85) : rgba(JADE, 0.35)
        ctx.lineWidth = major ? 2 : 1
        ctx.beginPath()
        ctx.moveTo(0, -R * 0.42)
        ctx.lineTo(0, -R * (major ? 0.36 : 0.39))
        ctx.stroke()
        ctx.rotate((Math.PI * 2) / 24)
      }
      ctx.rotate(-spin * 0.15)
      ctx.rotate(-spin * 0.4)
      ctx.setLineDash([2, 6])
      ctx.strokeStyle = rgba(GOLD, 0.45)
      ctx.beginPath()
      ctx.arc(0, 0, R * 0.235, 0, Math.PI * 2)
      ctx.stroke()
      ctx.setLineDash([])
      ctx.restore()
      ctx.fillStyle = 'rgba(233,228,216,0.95)'
      ctx.font = `600 ${Math.round(R * 0.2)}px LingshuSerif, "Noto Serif SC", serif`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText('枢', pivot.x, pivot.y + 1)

      // 光点
      ctx.globalCompositeOperation = 'lighter'
      for (let i = particles.length - 1; i >= 0; i--) {
        const p = particles[i]
        p.t += p.speed * dt
        if (p.t >= 1) {
          if (p.leg === 0) {
            p.leg = 1
            p.t = 0
          } else {
            glow[p.model] = 1
            particles.splice(i, 1)
            continue
          }
        }
        const pos =
          p.leg === 0
            ? bezier(clients[p.client], ctrlIn(clients[p.client], pivot), pivot, p.t)
            : bezier(pivot, ctrlOut(pivot, models[p.model]), models[p.model], p.t)
        const g = ctx.createRadialGradient(pos.x, pos.y, 0, pos.x, pos.y, 7)
        g.addColorStop(0, rgba(p.color, 0.95))
        g.addColorStop(1, rgba(p.color, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(pos.x, pos.y, 7, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.globalCompositeOperation = 'source-over'

      // 模型节点和名字
      ctx.font = '500 12px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'
      models.forEach((m, i) => {
        glow[i] = Math.max(0, glow[i] - dt * 1.6)
        const r = 4 + glow[i] * 5
        const g = ctx.createRadialGradient(m.x, m.y, 0, m.x, m.y, r * 3)
        g.addColorStop(0, rgba(JADE, 0.25 + glow[i] * 0.5))
        g.addColorStop(1, rgba(JADE, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(m.x, m.y, r * 3, 0, Math.PI * 2)
        ctx.fill()
        ctx.fillStyle = 'rgba(233,228,216,0.95)'
        ctx.beginPath()
        ctx.arc(m.x, m.y, 3, 0, Math.PI * 2)
        ctx.fill()
        ctx.fillStyle = `rgba(233,228,216,${0.55 + glow[i] * 0.45})`
        ctx.textAlign = m.x >= pivot.x ? 'left' : 'right'
        ctx.fillText(MODELS[i], m.x + (m.x >= pivot.x ? 12 : -12), m.y)
      })
    }

    const tick = (now: number) => {
      const dt = Math.min((now - last) / 1000, 0.05)
      last = now
      spin += dt
      if (Math.random() < dt * 9) spawn()
      draw(dt)
      raf = requestAnimationFrame(tick)
    }

    // 中枢里的"枢"字用嵌入的宋体，等字体加载好再开始画
    document.fonts.load('600 40px LingshuSerif').finally(() => {
      if (reduced) {
        for (let i = 0; i < 40; i++) spawn()
        particles.forEach((p, i) => ((p.t = (i % 10) / 10), (p.leg = (i % 2) as 0 | 1)))
        draw(0)
      } else {
        raf = requestAnimationFrame(tick)
      }
    })
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
    }
  }, [])

  return <canvas ref={ref} className="ls-meridian" aria-hidden="true" />
}
