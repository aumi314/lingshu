// 首页主视觉"经络图"：请求像气血一样从画面左侧流入中枢，再沿经脉分到各家模型。
// 各家模型的 logo 由粒子组成，分两层轨道：外圈中国模型，内圈海外模型，光点两圈都会去。
// 轨道倾斜，转到中枢后面的会变小变暗。
// 纯 canvas 绘制；系统开启"减少动态效果"时只画静止的一帧。
import { useEffect, useRef } from 'react'
import deepseek from '../assets/logos/deepseek.svg?raw'
import zhipu from '../assets/logos/zhipu.svg?raw'
import kimi from '../assets/logos/kimi.svg?raw'
import qwen from '../assets/logos/qwen.svg?raw'
import mimo from '../assets/logos/xiaomimimo.svg?raw'
import stepfun from '../assets/logos/stepfun.svg?raw'
import openai from '../assets/logos/openai.svg?raw'
import claude from '../assets/logos/claude.svg?raw'
import grok from '../assets/logos/grok.svg?raw'
import mistral from '../assets/logos/mistral.svg?raw'
import minimax from '../assets/logos/minimax.svg?raw'
import meta from '../assets/logos/meta.svg?raw'

type Vendor = { name: string; svg: string }

// 外圈：中国模型
const OUTER: Vendor[] = [
  { name: 'DeepSeek', svg: deepseek },
  { name: 'GLM', svg: zhipu },
  { name: 'Kimi', svg: kimi },
  { name: 'Qwen', svg: qwen },
  { name: 'MiMo', svg: mimo },
  { name: 'StepFun', svg: stepfun },
]
// 内圈：海外模型
const INNER: Vendor[] = [
  { name: 'GPT', svg: openai },
  { name: 'Claude', svg: claude },
  { name: 'Grok', svg: grok },
  { name: 'Mistral', svg: mistral },
  { name: 'MiniMax', svg: minimax },
  { name: 'Muse', svg: meta }, // Meta 的模型
]

const JADE = [110, 231, 200]
const CINNABAR = [226, 72, 61]
const GOLD = [212, 169, 90]
const PAPER = [233, 228, 216]
const INK = '7, 8, 11'
const CLIENTS = 4 // 调用方在画面左边缘外，只画流进来的光点

type Pt = { x: number; y: number }
type Dot = { hx: number; hy: number; x: number; y: number; phase: number } // hx/hy：在 logo 里的位置（-0.5..0.5）
type Node = { v: Vendor; dots: Dot[]; glow: number }
type Pulse = { leg: 0 | 1; t: number; speed: number; client: number; target: number; color: number[] }

const rgba = (c: number[], a: number) => `rgba(${c[0]},${c[1]},${c[2]},${a})`

function bezier(a: Pt, c: Pt, b: Pt, t: number): Pt {
  const u = 1 - t
  return { x: u * u * a.x + 2 * u * t * c.x + t * t * b.x, y: u * u * a.y + 2 * u * t * c.y + t * t * b.y }
}

// 把 logo 栅格化后按网格采样，得到组成 logo 的粒子位置
async function sampleLogo(v: Vendor): Promise<{ hx: number; hy: number }[]> {
  const S = 72
  const c = document.createElement('canvas')
  c.width = c.height = S
  const g = c.getContext('2d', { willReadFrequently: true })!
  const svg = v.svg.replace('fill="currentColor"', 'fill="#fff"').replace(/width="1em"/, `width="${S}"`).replace(/height="1em"/, `height="${S}"`)
  const img = new Image()
  img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
  await img.decode()
  g.drawImage(img, 0, 0, S, S)
  const data = g.getImageData(0, 0, S, S).data
  const pts: { hx: number; hy: number }[] = []
  const step = 2
  for (let y = 0; y < S; y += step) {
    for (let x = (y / step) % 2 ? 1 : 0; x < S; x += step) {
      if (data[(y * S + x) * 4 + 3] > 120) pts.push({ hx: x / S - 0.5, hy: y / S - 0.5 })
    }
  }
  return pts
}

export default function Meridian() {
  const ref = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = ref.current!
    const ctx = canvas.getContext('2d')!
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    let w = 0, h = 0, raf = 0, last = performance.now(), time = 0
    let nodes: Node[] = []
    const pulses: Pulse[] = []
    let cancelled = false

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

    // 布局。宽屏中枢靠右给文字留位置；窄屏中枢在上方。
    // 两层轨道都是倾斜的椭圆，depth = sin(角度)：>0 在中枢前面，<0 在后面。
    const geometry = () => {
      const narrow = w < 860
      const pivot = narrow ? { x: w * 0.5, y: h * 0.27 } : { x: w * 0.72, y: h * 0.5 }
      const R = narrow ? Math.min(w * 0.36, h * 0.22) : Math.min(w * 0.235, h * 0.36)
      const logo = narrow ? 30 : 54
      return { narrow, pivot, R, logo }
    }

    const place = (i: number, outer: boolean, R: number, pivot: Pt) => {
      const n = outer ? OUTER.length : INNER.length
      const a = outer ? time * 0.05 + (i / n) * Math.PI * 2 : -time * 0.09 + (i / n) * Math.PI * 2 + Math.PI / n
      const rx = outer ? R * 1.16 : R * 0.82
      const ry = outer ? R * 0.86 : R * 0.6
      const depth = Math.sin(a)
      return { x: pivot.x + Math.cos(a) * rx, y: pivot.y + depth * ry, depth }
    }

    const ctrlIn = (a: Pt, p: Pt): Pt => ({ x: (a.x + p.x) / 2, y: a.y + (p.y - a.y) * 0.15 })
    const ctrlOut = (p: Pt, m: Pt): Pt => ({ x: (p.x + m.x) / 2 + (m.y - p.y) * 0.22, y: (p.y + m.y) / 2 - (m.x - p.x) * 0.22 })
    const clientPt = (i: number, narrow: boolean): Pt => ({
      x: -40,
      y: narrow ? h * (0.02 + (i * 0.48) / (CLIENTS - 1)) : h * (0.12 + (i * 0.76) / (CLIENTS - 1)),
    })

    const spawn = () => {
      const r = Math.random()
      pulses.push({
        leg: 0,
        t: 0,
        speed: 0.3 + Math.random() * 0.25,
        client: Math.floor(Math.random() * CLIENTS),
        target: Math.floor(Math.random() * (OUTER.length + INNER.length)),
        color: r < 0.72 ? JADE : r < 0.9 ? GOLD : CINNABAR,
      })
    }

    const drawPivot = (pivot: Pt, R: number) => {
      ctx.save()
      ctx.translate(pivot.x, pivot.y)
      const halo = ctx.createRadialGradient(0, 0, 0, 0, 0, R * 0.5)
      halo.addColorStop(0, rgba(JADE, 0.17))
      halo.addColorStop(1, rgba(JADE, 0))
      ctx.fillStyle = halo
      ctx.beginPath()
      ctx.arc(0, 0, R * 0.5, 0, Math.PI * 2)
      ctx.fill()
      ctx.lineWidth = 1
      for (const [r, a] of [[R * 0.4, 0.2], [R * 0.29, 0.3], [R * 0.17, 0.5]] as const) {
        ctx.strokeStyle = rgba(JADE, a)
        ctx.beginPath()
        ctx.arc(0, 0, r, 0, Math.PI * 2)
        ctx.stroke()
      }
      // 二十四道刻度，对应二十四节气
      ctx.rotate(time * 0.15)
      for (let i = 0; i < 24; i++) {
        const major = i % 6 === 0
        ctx.strokeStyle = major ? rgba(CINNABAR, 0.85) : rgba(JADE, 0.35)
        ctx.lineWidth = major ? 2 : 1
        ctx.beginPath()
        ctx.moveTo(0, -R * 0.4)
        ctx.lineTo(0, -R * (major ? 0.34 : 0.37))
        ctx.stroke()
        ctx.rotate((Math.PI * 2) / 24)
      }
      ctx.rotate(-time * 0.55)
      ctx.setLineDash([2, 6])
      ctx.strokeStyle = rgba(GOLD, 0.45)
      ctx.lineWidth = 1
      ctx.beginPath()
      ctx.arc(0, 0, R * 0.23, 0, Math.PI * 2)
      ctx.stroke()
      ctx.setLineDash([])
      ctx.restore()
      ctx.fillStyle = rgba(PAPER, 0.95)
      ctx.font = `600 ${Math.round(R * 0.2)}px LingshuSerif, serif`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.fillText('枢', pivot.x, pivot.y + 1)
    }

    const drawOrbits = (pivot: Pt, R: number) => {
      ctx.lineWidth = 1
      ctx.strokeStyle = rgba(JADE, 0.07)
      for (const [rx, ry] of [[1.16, 0.86], [0.82, 0.6]]) {
        ctx.beginPath()
        ctx.ellipse(pivot.x, pivot.y, R * rx, R * ry, 0, 0, Math.PI * 2)
        ctx.stroke()
      }
    }

    // 画一个 logo：粒子跟随目标位置（入场时从中枢飞出聚拢），收到请求时向外一涨并变亮
    const drawNode = (n: Node, at: { x: number; y: number; depth: number }, size: number, dt: number) => {
      const scale = 0.78 + 0.22 * (at.depth + 1) / 2
      const s = size * scale
      const swell = 1 + n.glow * 0.28
      const base = 0.82 * (0.55 + 0.45 * (at.depth + 1) / 2)
      const k = reduced ? 1 : Math.min(1, dt * 4.5)
      const color = n.glow > 0.05 ? JADE : PAPER
      for (const d of n.dots) {
        const tx = at.x + d.hx * s * swell + Math.sin(time * 1.3 + d.phase) * 0.35
        const ty = at.y + d.hy * s * swell + Math.cos(time * 1.1 + d.phase) * 0.35
        d.x += (tx - d.x) * k
        d.y += (ty - d.y) * k
        const tw = 0.75 + 0.25 * Math.sin(time * 2 + d.phase * 3)
        ctx.fillStyle = rgba(color, Math.min(1, (base + n.glow * 0.6) * tw))
        ctx.fillRect(d.x - 0.7, d.y - 0.7, 1.4, 1.4)
      }
      if (n.glow > 0.02) {
        const g = ctx.createRadialGradient(at.x, at.y, 0, at.x, at.y, s)
        g.addColorStop(0, rgba(JADE, 0.22 * n.glow))
        g.addColorStop(1, rgba(JADE, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(at.x, at.y, s, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.font = `500 ${Math.round(11 * scale)}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'top'
      ctx.fillStyle = rgba(PAPER, 0.6 * (0.6 + 0.4 * (at.depth + 1) / 2) + n.glow * 0.4)
      ctx.fillText(n.v.name, at.x, at.y + s * 0.5 + 6)
    }

    const draw = (dt: number) => {
      const { narrow, pivot, R, logo } = geometry()

      ctx.globalCompositeOperation = 'source-over'
      ctx.fillStyle = `rgba(${INK}, ${reduced ? 1 : 0.3})`
      ctx.fillRect(0, 0, w, h)

      const outer = OUTER.map((_, i) => place(i, true, R, pivot))
      const inner = INNER.map((_, i) => place(i, false, R, pivot))
      const spots = [...outer, ...inner] // 和 nodes 的顺序一致

      // 经脉：调用方 → 中枢 → 已接入的各家
      ctx.lineWidth = 1
      for (let i = 0; i < CLIENTS; i++) {
        const c = clientPt(i, narrow)
        const k = ctrlIn(c, pivot)
        ctx.strokeStyle = rgba(JADE, 0.08)
        ctx.beginPath()
        ctx.moveTo(c.x, c.y)
        ctx.quadraticCurveTo(k.x, k.y, pivot.x, pivot.y)
        ctx.stroke()
      }
      for (const m of spots) {
        const k = ctrlOut(pivot, m)
        ctx.strokeStyle = rgba(JADE, 0.04 + 0.05 * (m.depth + 1) / 2)
        ctx.beginPath()
        ctx.moveTo(pivot.x, pivot.y)
        ctx.quadraticCurveTo(k.x, k.y, m.x, m.y)
        ctx.stroke()
      }
      drawOrbits(pivot, R)

      // 先画在中枢后面的，再画中枢，最后画在前面的
      const all = [
        ...nodes.slice(0, OUTER.length).map((n, i) => ({ n, at: outer[i] })),
        ...nodes.slice(OUTER.length).map((n, i) => ({ n, at: inner[i] })),
      ].sort((a, b) => a.at.depth - b.at.depth)
      for (const n of nodes) n.glow = Math.max(0, n.glow - dt * 1.4)
      for (const { n, at } of all) if (at.depth < 0) drawNode(n, at, logo, dt)
      drawPivot(pivot, R)
      for (const { n, at } of all) if (at.depth >= 0) drawNode(n, at, logo, dt)

      // 请求光点
      ctx.globalCompositeOperation = 'lighter'
      for (let i = pulses.length - 1; i >= 0; i--) {
        const p = pulses[i]
        p.t += p.speed * dt
        if (p.t >= 1) {
          if (p.leg === 0) {
            p.leg = 1
            p.t = 0
          } else {
            nodes[p.target].glow = 1
            pulses.splice(i, 1)
            continue
          }
        }
        const c = clientPt(p.client, narrow)
        const m = spots[p.target]
        const pos = p.leg === 0 ? bezier(c, ctrlIn(c, pivot), pivot, p.t) : bezier(pivot, ctrlOut(pivot, m), m, p.t)
        const g = ctx.createRadialGradient(pos.x, pos.y, 0, pos.x, pos.y, 7)
        g.addColorStop(0, rgba(p.color, 0.95))
        g.addColorStop(1, rgba(p.color, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(pos.x, pos.y, 7, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.globalCompositeOperation = 'source-over'

    }

    const tick = (now: number) => {
      const dt = Math.min((now - last) / 1000, 0.05)
      last = now
      time += dt
      if (Math.random() < dt * 9) spawn()
      draw(dt)
      raf = requestAnimationFrame(tick)
    }

    // 等宋体（中枢的"枢"）和各家 logo 都准备好再开始画
    Promise.all([document.fonts.load('900 40px LingshuSerif'), document.fonts.load('600 40px LingshuSerif')])
      .catch(() => {})
      .then(() => Promise.all([...OUTER, ...INNER].map(sampleLogo)))
      .then((sampled) => {
        if (cancelled) return
        const { pivot } = geometry()
        nodes = [...OUTER, ...INNER].map((v, i) => ({
          v,
          glow: 0,
          // 入场：粒子从中枢附近散开出发
          dots: sampled[i].map((p) => ({ ...p, x: pivot.x + (Math.random() - 0.5) * 60, y: pivot.y + (Math.random() - 0.5) * 60, phase: Math.random() * Math.PI * 2 })),
        }))
        if (reduced) {
          for (let i = 0; i < 12; i++) spawn()
          pulses.forEach((p, i) => ((p.t = (i % 6) / 6), (p.leg = (i % 2) as 0 | 1)))
          draw(1)
        } else {
          last = performance.now()
          raf = requestAnimationFrame(tick)
        }
      })

    return () => {
      cancelled = true
      cancelAnimationFrame(raf)
      ro.disconnect()
    }
  }, [])

  return <canvas ref={ref} className="ls-meridian" aria-hidden="true" />
}
