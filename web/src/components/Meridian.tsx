// 首页主视觉：一幅"天象 + 山水"。天上是星空和北斗（斗口第一星即"天枢"），地上是三层水墨远山和流动的雾；
// 以浑天仪为"枢"，各家模型的粒子 logo 在两条交叉的空间轨道上运行，
// 请求像气血一样从左侧流入中枢、再分往各家；背景是汇向中枢的"气"流。
// 整个场景是三维投影，镜头随鼠标轻微偏转；鼠标划过 logo 会把粒子拨开，随后自己聚回。
// 纯 canvas 2D，无依赖；"减少动态效果"时只画一帧静止画面。
import { useEffect, useRef } from 'react'
import deepseek from '../assets/logos/deepseek.svg?raw'
import zhipu from '../assets/logos/zhipu.svg?raw'
import kimi from '../assets/logos/kimi.svg?raw'
import qwen from '../assets/logos/qwen.svg?raw'
import mimo from '../assets/logos/xiaomimimo.svg?raw'
import stepfun from '../assets/logos/stepfun.svg?raw'
import minimax from '../assets/logos/minimax.svg?raw'
import doubao from '../assets/logos/doubao.svg?raw'
import hunyuan from '../assets/logos/hunyuan.svg?raw'
import openai from '../assets/logos/openai.svg?raw'
import claude from '../assets/logos/claude.svg?raw'
import grok from '../assets/logos/grok.svg?raw'
import mistral from '../assets/logos/mistral.svg?raw'
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
  { name: 'MiniMax', svg: minimax },
  { name: 'Doubao Seed', svg: doubao },
  { name: 'HY', svg: hunyuan }, // 腾讯混元
]
// 内圈：海外模型
const INNER: Vendor[] = [
  { name: 'GPT', svg: openai },
  { name: 'Claude', svg: claude },
  { name: 'Grok', svg: grok },
  { name: 'Mistral', svg: mistral },
  { name: 'Muse', svg: meta }, // Meta 的模型
]
const VENDORS = [...OUTER, ...INNER]

const JADE = [110, 231, 200]
const CINNABAR = [226, 72, 61]
const GOLD = [212, 169, 90]
const PAPER = [233, 228, 216]
const INK = '7, 8, 11'
const CLIENTS = 5 // 调用方在画面左边缘外，只画流进来的光点

type V3 = [number, number, number]
type Pt = { x: number; y: number }
type Proj = { x: number; y: number; z: number; k: number } // k：透视缩放；z 越大越靠近镜头
type Dot = { hx: number; hy: number; x: number; y: number; vx: number; vy: number; phase: number }
type Node = { v: Vendor; dots: Dot[]; glow: number; hover: number }
type Pulse = { leg: 0 | 1; t: number; speed: number; client: number; target: number; color: number[] }
type Mote = { x: number; y: number; life: number; max: number } // 背景的"气"
type Star = { x: number; y: number; r: number; phase: number }
type Ridge = { img: HTMLCanvasElement; top: number; depth: number } // 预先画好的一层远山

// 北斗七星的相对位置（天枢、天璇、天玑、天权、玉衡、开阳、摇光），x 向右、y 向下
const DIPPER: [number, number][] = [[0, 0], [0.02, 0.33], [0.33, 0.43], [0.38, 0.19], [0.58, 0.17], [0.76, 0.21], [1, 0.4]]
const DIPPER_LINES = [[0, 1], [1, 2], [2, 3], [3, 0], [3, 4], [4, 5], [5, 6]]

// 可复现的伪随机，保证每次打开山形一致
function seeded(seed: number) {
  return () => {
    seed |= 0
    seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// 一维平滑噪声，几层叠加得到起伏的山脊
function ridgeNoise(rand: () => number) {
  const table = Array.from({ length: 512 }, rand)
  const smooth = (x: number) => {
    const i = Math.floor(x), f = x - i, u = f * f * (3 - 2 * f)
    return table[i & 511] * (1 - u) + table[(i + 1) & 511] * u
  }
  return (x: number) => {
    let v = 0, amp = 0.55, freq = 1
    for (let o = 0; o < 5; o++) {
      v += smooth(x * freq) * amp
      amp *= 0.5
      freq *= 2.1
    }
    return v
  }
}

const rgba = (c: number[], a: number) => `rgba(${c[0]},${c[1]},${c[2]},${a < 0 ? 0 : a > 1 ? 1 : a})`

// ---- 三维 ----
const rotX = ([x, y, z]: V3, a: number): V3 => [x, y * Math.cos(a) - z * Math.sin(a), y * Math.sin(a) + z * Math.cos(a)]
const rotY = ([x, y, z]: V3, a: number): V3 => [x * Math.cos(a) + z * Math.sin(a), y, -x * Math.sin(a) + z * Math.cos(a)]
const rotZ = ([x, y, z]: V3, a: number): V3 => [x * Math.cos(a) - y * Math.sin(a), x * Math.sin(a) + y * Math.cos(a), z]

function bezier(a: Pt, c: Pt, b: Pt, t: number): Pt {
  const u = 1 - t
  return { x: u * u * a.x + 2 * u * t * c.x + t * t * b.x, y: u * u * a.y + 2 * u * t * c.y + t * t * b.y }
}

// 把 logo 栅格化后按网格采样，得到组成 logo 的粒子位置（-0.5..0.5）
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
  for (let y = 0; y < S; y += 2) {
    for (let x = (y / 2) % 2 ? 1 : 0; x < S; x += 2) {
      if (data[(y * S + x) * 4 + 3] > 120) pts.push({ hx: x / S - 0.5, hy: y / S - 0.5 })
    }
  }
  return pts
}

export default function Meridian() {
  const ref = useRef<HTMLCanvasElement>(null)
  const bgRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    // 两层画布：底层画天、山、雾、灯光，每帧整幅重画；上层画气流、光点、轨道和 logo，带拖尾。
    // 分层是为了让静止的山和星不被拖尾反复叠加而变亮。
    const canvas = ref.current!
    const bgCanvas = bgRef.current!
    const fgCtx = canvas.getContext('2d')!
    const bgCtx = bgCanvas.getContext('2d')!
    let ctx = fgCtx
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    let w = 0, h = 0, raf = 0, last = performance.now(), time = 0, cancelled = false
    let nodes: Node[] = []
    const pulses: Pulse[] = []
    let motes: Mote[] = []
    let stars: Star[] = []
    let ridges: Ridge[] = []
    let dpr = 1
    let base: HTMLCanvasElement | null = null // 墨色底和地平线月光，尺寸变化时重画
    // 镜头：随鼠标轻微偏转（平滑跟随）
    const cam = { yaw: 0, pitch: 0, tyaw: 0, tpitch: 0 }
    const mouse = { x: -9999, y: -9999, active: false }

    function newMote(anywhere: boolean): Mote {
      const max = 4 + Math.random() * 6
      // 新的气从左边和上下边缘生出，向中枢汇聚
      const edge = Math.random()
      const x = anywhere ? Math.random() * w : edge < 0.6 ? -10 : Math.random() * w * 0.7
      const y = anywhere ? Math.random() * h : edge < 0.6 ? Math.random() * h : edge < 0.8 ? -10 : h + 10
      return { x, y, life: anywhere ? Math.random() * max : 0, max }
    }

    // 三层远山：远山淡、近山浓，山脊一线微光。画到离屏画布上，每帧只做平移（视差）
    const buildRidges = () => {
      const rand = seeded(20261018)
      const pad = 160
      ridges = [0, 1, 2].map((depth) => {
        const noise = ridgeNoise(rand)
        const base = h * (0.74 + depth * 0.075)
        const amp = h * (0.2 - depth * 0.045)
        const W = w + pad * 2
        const ys: number[] = []
        for (let x = 0; x <= W; x += 3) {
          const n = noise(x / (w * (0.16 + depth * 0.05)) + depth * 37)
          ys.push(base - amp * Math.pow(n, 1.7) * 1.6)
        }
        const top = Math.min(...ys) - 4
        const c = document.createElement('canvas')
        c.width = W * dpr
        c.height = (h - top) * dpr
        const g = c.getContext('2d')!
        g.setTransform(dpr, 0, 0, dpr, 0, -top * dpr)
        g.beginPath()
        g.moveTo(0, h)
        ys.forEach((y, i) => g.lineTo(i * 3, y))
        g.lineTo(W, h)
        g.closePath()
        const grad = g.createLinearGradient(0, top, 0, h)
        if (depth === 0) {
          grad.addColorStop(0, rgba([70, 96, 94], 0.55))
          grad.addColorStop(0.5, rgba([22, 30, 32], 0.7))
          grad.addColorStop(1, rgba([10, 12, 15], 0.9))
        } else if (depth === 1) {
          grad.addColorStop(0, rgba([34, 46, 47], 0.95))
          grad.addColorStop(0.4, rgba([13, 16, 19], 1))
          grad.addColorStop(1, rgba([8, 9, 12], 1))
        } else {
          grad.addColorStop(0, rgba([16, 20, 22], 1))
          grad.addColorStop(0.3, rgba([7, 8, 11], 1))
          grad.addColorStop(1, rgba([7, 8, 11], 1))
        }
        g.fillStyle = grad
        g.fill()
        g.beginPath()
        ys.forEach((y, i) => (i ? g.lineTo(i * 3, y) : g.moveTo(0, y)))
        g.strokeStyle = depth === 2 ? rgba(JADE, 0.3) : rgba(depth === 0 ? JADE : PAPER, depth === 0 ? 0.12 : 0.1)
        g.lineWidth = 1
        g.stroke()
        return { img: c, top, depth }
      })
    }

    const buildBase = () => {
      const { pivot } = geometry()
      const c = document.createElement('canvas')
      c.width = w * dpr
      c.height = h * dpr
      const g = c.getContext('2d')!
      g.setTransform(dpr, 0, 0, dpr, 0, 0)
      g.fillStyle = `rgb(${INK})`
      g.fillRect(0, 0, w, h)
      const glow = g.createRadialGradient(pivot.x, h * 0.8, 0, pivot.x, h * 0.8, w * 0.55)
      glow.addColorStop(0, rgba(JADE, 0.1))
      glow.addColorStop(0.4, rgba(GOLD, 0.025))
      glow.addColorStop(1, rgba(JADE, 0))
      g.fillStyle = glow
      g.fillRect(0, 0, w, h)
      base = c
    }

    const resize = () => {
      dpr = Math.min(window.devicePixelRatio || 1, 2)
      const r = canvas.getBoundingClientRect()
      w = r.width
      h = r.height
      for (const [c, g] of [[canvas, fgCtx], [bgCanvas, bgCtx]] as const) {
        c.width = w * dpr
        c.height = h * dpr
        g.setTransform(dpr, 0, 0, dpr, 0, 0)
      }
      motes = Array.from({ length: w < 860 ? 380 : 1100 }, () => newMote(true))
      const rand = seeded(7)
      stars = Array.from({ length: w < 860 ? 70 : 170 }, () => ({
        x: rand() * w,
        y: Math.pow(rand(), 1.4) * h * 0.72,
        r: 0.35 + rand() * 0.9,
        phase: rand() * Math.PI * 2,
      }))
      buildRidges()
      buildBase()
    }

    const onMove = (e: PointerEvent) => {
      const r = canvas.getBoundingClientRect()
      mouse.x = e.clientX - r.left
      mouse.y = e.clientY - r.top
      mouse.active = mouse.y >= 0 && mouse.y <= h
      cam.tyaw = (e.clientX / window.innerWidth - 0.5) * 0.5
      cam.tpitch = (e.clientY / window.innerHeight - 0.5) * 0.28
    }
    const onLeave = () => {
      mouse.active = false
      mouse.x = mouse.y = -9999
    }

    // 布局：宽屏中枢靠右给文字留位置；窄屏中枢在上方
    const geometry = () => {
      const narrow = w < 860
      const pivot = narrow ? { x: w * 0.5, y: h * 0.27 } : { x: w * 0.72, y: h * 0.5 }
      const R = narrow ? Math.min(w * 0.36, h * 0.22) : Math.min(w * 0.23, h * 0.35)
      return { narrow, pivot, R, logo: narrow ? 30 : 56 }
    }

    // 世界坐标 → 屏幕
    const project = (p: V3, pivot: Pt, R: number): Proj => {
      let q = rotY(p, cam.yaw)
      q = rotX(q, cam.pitch)
      const f = R * 4.2
      const k = f / (f - q[2])
      return { x: pivot.x + q[0] * k, y: pivot.y + q[1] * k, z: q[2], k }
    }

    // 两条交叉倾斜的轨道：外圈平缓、内圈陡一些，像原子轨道
    const orbitPoint = (outer: boolean, a: number, R: number): V3 => {
      const r = outer ? R * 1.12 : R * 0.78
      let p: V3 = [Math.cos(a) * r, 0, Math.sin(a) * r]
      p = rotX(p, outer ? 1.22 : 1.02)
      return rotZ(p, outer ? -0.18 : 0.42)
    }
    const vendorAngle = (i: number) => {
      if (i < OUTER.length) return time * 0.06 + (i / OUTER.length) * Math.PI * 2
      const j = i - OUTER.length
      return -time * 0.1 + (j / INNER.length) * Math.PI * 2 + 0.4
    }

    const clientPt = (i: number, narrow: boolean): Pt => ({
      x: -40,
      y: narrow ? h * (0.02 + (i * 0.46) / (CLIENTS - 1)) : h * (0.1 + (i * 0.8) / (CLIENTS - 1)),
    })
    const ctrlIn = (a: Pt, p: Pt): Pt => ({ x: (a.x + p.x) / 2, y: a.y + (p.y - a.y) * 0.15 })
    const ctrlOut = (p: Pt, m: Pt): Pt => ({ x: (p.x + m.x) / 2 + (m.y - p.y) * 0.2, y: (p.y + m.y) / 2 - (m.x - p.x) * 0.2 })

    const spawnPulse = () => {
      const r = Math.random()
      pulses.push({
        leg: 0,
        t: 0,
        speed: 0.32 + Math.random() * 0.25,
        client: Math.floor(Math.random() * CLIENTS),
        target: Math.floor(Math.random() * VENDORS.length),
        color: r < 0.7 ? JADE : r < 0.88 ? GOLD : CINNABAR,
      })
    }

    // ---- 天：星空和北斗。鼠标附近的星会亮起来 ----
    const near = (x: number, y: number, r: number) => (mouse.active ? Math.max(0, 1 - Math.hypot(x - mouse.x, y - mouse.y) / r) : 0)
    const drawSky = (narrow: boolean) => {
      for (const st of stars) {
        const tw = 0.5 + 0.5 * Math.sin(time * 1.7 + st.phase)
        const a = 0.22 + 0.55 * tw + near(st.x, st.y, 160) * 0.6
        ctx.fillStyle = rgba(PAPER, a)
        ctx.fillRect(st.x - st.r, st.y - st.r, st.r * 2, st.r * 2)
      }
      // 北斗：宽屏放在左上（标题上方），窄屏放在中枢下方左侧
      const sx = narrow ? w * 0.42 : Math.min(w * 0.2, 300)
      const ox = narrow ? w * 0.08 : w * 0.06
      const oy = narrow ? h * 0.5 : h * 0.12
      const pts = DIPPER.map(([x, y]) => ({ x: ox + x * sx - cam.yaw * 18, y: oy + y * sx * 0.5 - cam.pitch * 12 }))
      ctx.lineWidth = 1
      ctx.strokeStyle = rgba(GOLD, 0.42)
      ctx.beginPath()
      for (const [a, b] of DIPPER_LINES) {
        ctx.moveTo(pts[a].x, pts[a].y)
        ctx.lineTo(pts[b].x, pts[b].y)
      }
      ctx.stroke()
      pts.forEach((p, i) => {
        const tianshu = i === 0 // 天枢：斗口第一星，"枢"的出处之一
        const pulse = 0.75 + 0.25 * Math.sin(time * 2 + i)
        const r = (tianshu ? 14 : 9) * pulse + near(p.x, p.y, 120) * 8
        const g = ctx.createRadialGradient(p.x, p.y, 0, p.x, p.y, r)
        g.addColorStop(0, rgba(tianshu ? JADE : GOLD, 1))
        g.addColorStop(0.2, rgba(tianshu ? JADE : GOLD, 0.45))
        g.addColorStop(1, rgba(GOLD, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(p.x, p.y, r, 0, Math.PI * 2)
        ctx.fill()
        ctx.fillStyle = rgba(PAPER, 0.95)
        ctx.fillRect(p.x - 1, p.y - 1, 2, 2)
      })
      ctx.font = '500 13px LingshuSerif, serif'
      ctx.textAlign = 'left'
      ctx.textBaseline = 'middle'
      ctx.fillStyle = rgba(JADE, 0.85)
      ctx.fillText('天枢', pts[0].x + 12, pts[0].y - 2)
      ctx.fillStyle = rgba(PAPER, 0.55)
      ctx.fillText('北', pts[6].x + 12, pts[6].y - 8)
      ctx.fillText('斗', pts[6].x + 12, pts[6].y + 8)
    }

    // ---- 地：三层远山（视差）和缓缓流过的雾 ----
    const drawLand = () => {
      for (const rg of ridges) {
        const dx = -160 - cam.yaw * (24 + rg.depth * 46)
        const dy = -cam.pitch * (10 + rg.depth * 22)
        ctx.drawImage(rg.img, dx, rg.top + dy, rg.img.width / dpr, rg.img.height / dpr)
        if (rg.depth < 2) {
          // 山间的雾：两团扁的光在层与层之间横向漂移
          for (let k = 0; k < 2; k++) {
            const span = w + 800
            const x = ((time * (10 + rg.depth * 6) + k * span * 0.5 + rg.depth * 300) % span) - 400
            const y = h * (0.8 + rg.depth * 0.07)
            ctx.save()
            ctx.translate(x, y)
            ctx.scale(1, 0.16)
            const g = ctx.createRadialGradient(0, 0, 0, 0, 0, 420)
            g.addColorStop(0, rgba(PAPER, 0.05 + near(x, y, 500) * 0.05))
            g.addColorStop(1, rgba(PAPER, 0))
            ctx.fillStyle = g
            ctx.beginPath()
            ctx.arc(0, 0, 420, 0, Math.PI * 2)
            ctx.fill()
            ctx.restore()
          }
        }
      }
    }

    // ---- 鼠标是一盏灯，照亮附近的星、雾和山脊 ----
    const drawLantern = () => {
      if (!mouse.active) return
      const g = ctx.createRadialGradient(mouse.x, mouse.y, 0, mouse.x, mouse.y, 280)
      g.addColorStop(0, rgba(PAPER, 0.05))
      g.addColorStop(0.5, rgba(JADE, 0.018))
      g.addColorStop(1, rgba(JADE, 0))
      ctx.fillStyle = g
      ctx.beginPath()
      ctx.arc(mouse.x, mouse.y, 280, 0, Math.PI * 2)
      ctx.fill()
    }

    // ---- 背景的"气"：沿流场漂动，整体汇向中枢，越近越绕着转 ----
    const drawMotes = (pivot: Pt, R: number, dt: number) => {
      ctx.fillStyle = rgba(JADE, 1)
      for (let i = 0; i < motes.length; i++) {
        const m = motes[i]
        const dx = pivot.x - m.x
        const dy = pivot.y - m.y
        const dist = Math.hypot(dx, dy) || 1
        const swirl = Math.sin(m.x * 0.0042 + time * 0.25) * 1.6 + Math.cos(m.y * 0.0051 - time * 0.2) * 1.6
        const ang = Math.atan2(dy, dx) + swirl * 0.55 + 0.9 * Math.min(1, R / dist)
        const sp = 18 + 50 * Math.min(1, (R * 1.5) / dist)
        m.x += Math.cos(ang) * sp * dt
        m.y += Math.sin(ang) * sp * dt
        if (mouse.active) {
          const mx = m.x - mouse.x, my = m.y - mouse.y, md = mx * mx + my * my
          if (md < 6400) {
            const f = (1 - md / 6400) * 60 * dt
            const inv = 1 / Math.sqrt(md + 1)
            m.x += mx * inv * f
            m.y += my * inv * f
          }
        }
        m.life += dt
        if (m.life > m.max || dist < R * 0.18 || m.x > w + 20 || m.y < -20 || m.y > h + 20) {
          motes[i] = newMote(false)
          continue
        }
        ctx.globalAlpha = Math.sin((m.life / m.max) * Math.PI) * 0.18
        ctx.fillRect(m.x, m.y, 1.2, 1.2)
      }
      ctx.globalAlpha = 1
    }

    // ---- 浑天仪：赤道环（带二十四节气刻度）、子午环、黄道环，三环各自转动 ----
    // front=false 只画背向镜头的半边，front=true 只画朝向镜头的半边，这样"枢"字被夹在中间
    const drawArmillary = (pivot: Pt, R: number, front: boolean) => {
      const Rp = R * 0.42
      const rings: { at: (a: number) => V3; color: number[]; alpha: number; width: number }[] = [
        { at: (a) => rotX([Math.cos(a) * Rp, 0, Math.sin(a) * Rp], 1.25), color: JADE, alpha: 0.8, width: 1.4 },
        { at: (a) => rotY([0, Math.cos(a) * Rp * 0.94, Math.sin(a) * Rp * 0.94], time * 0.35), color: JADE, alpha: 0.5, width: 1 },
        { at: (a) => rotY(rotZ(rotX([Math.cos(a) * Rp * 0.86, 0, Math.sin(a) * Rp * 0.86], 1.25), 0.41), -time * 0.22), color: GOLD, alpha: 0.6, width: 1 },
      ]
      const N = 72
      for (const ring of rings) {
        ctx.lineWidth = ring.width
        for (let i = 0; i < N; i++) {
          const p0 = project(ring.at((i / N) * Math.PI * 2), pivot, R)
          const p1 = project(ring.at(((i + 1) / N) * Math.PI * 2), pivot, R)
          if ((p0.z + p1.z >= 0) !== front) continue
          const depth = (p0.z + p1.z) / 2 / Rp
          ctx.strokeStyle = rgba(ring.color, ring.alpha * (0.3 + 0.7 * (depth + 1) / 2))
          ctx.beginPath()
          ctx.moveTo(p0.x, p0.y)
          ctx.lineTo(p1.x, p1.y)
          ctx.stroke()
        }
      }
      // 赤道环上的二十四道刻度，随时间转动；四正位用朱砂
      for (let i = 0; i < 24; i++) {
        const a = (i / 24) * Math.PI * 2 + time * 0.12
        const p0 = project(rotX([Math.cos(a) * Rp * 0.9, 0, Math.sin(a) * Rp * 0.9], 1.25), pivot, R)
        const p1 = project(rotX([Math.cos(a) * Rp * 1.07, 0, Math.sin(a) * Rp * 1.07], 1.25), pivot, R)
        if ((p0.z >= 0) !== front) continue
        const major = i % 6 === 0
        ctx.strokeStyle = rgba(major ? CINNABAR : JADE, (major ? 1 : 0.5) * (0.3 + 0.7 * (p0.z / Rp + 1) / 2))
        ctx.lineWidth = major ? 2.4 : 1
        ctx.beginPath()
        ctx.moveTo(p0.x, p0.y)
        ctx.lineTo(p1.x, p1.y)
        ctx.stroke()
      }
    }

    const drawCore = (pivot: Pt, R: number) => {
      const r = R * 0.62 * (1 + Math.sin(time * 1.4) * 0.06)
      const halo = ctx.createRadialGradient(pivot.x, pivot.y, 0, pivot.x, pivot.y, r)
      halo.addColorStop(0, rgba(JADE, 0.24))
      halo.addColorStop(0.45, rgba(JADE, 0.07))
      halo.addColorStop(1, rgba(JADE, 0))
      ctx.fillStyle = halo
      ctx.beginPath()
      ctx.arc(pivot.x, pivot.y, r, 0, Math.PI * 2)
      ctx.fill()
      ctx.font = `900 ${Math.round(R * 0.24)}px LingshuSerif, serif`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'middle'
      ctx.shadowColor = rgba(JADE, 0.9)
      ctx.shadowBlur = 24
      ctx.fillStyle = rgba(PAPER, 0.96)
      ctx.fillText('枢', pivot.x, pivot.y + 1)
      ctx.shadowBlur = 0
    }

    // 画一个 logo：粒子用弹簧跟随目标；鼠标靠近时被拨开；悬停放大发光；收到请求时向外一涨、变亮
    const drawNode = (n: Node, at: Proj, size: number, dt: number, R: number) => {
      const near = (Math.max(-1, Math.min(1, at.z / (R * 1.12))) + 1) / 2
      const hoverD = mouse.active ? Math.hypot(mouse.x - at.x, mouse.y - at.y) : 1e9
      n.hover += ((hoverD < size * 0.75 ? 1 : 0) - n.hover) * Math.min(1, dt * 6)
      const s = size * at.k * (1 + n.hover * 0.25)
      const swell = 1 + n.glow * 0.3
      const color = n.glow > 0.35 || n.hover > 0.3 ? JADE : PAPER
      ctx.fillStyle = rgba(color, 0.4 + 0.52 * near + n.glow * 0.4 + n.hover * 0.3)
      const stiff = reduced ? 1 : Math.min(1, dt * 5)
      const r = 0.65 + 0.35 * near
      for (const d of n.dots) {
        const tx = at.x + d.hx * s * swell + Math.sin(time * 1.3 + d.phase) * 0.4
        const ty = at.y + d.hy * s * swell + Math.cos(time * 1.1 + d.phase) * 0.4
        if (mouse.active) {
          const mx = d.x - mouse.x, my = d.y - mouse.y, md = mx * mx + my * my
          if (md < 2500) {
            const f = (1 - md / 2500) * 900 * dt
            const inv = 1 / Math.sqrt(md + 1)
            d.vx += mx * inv * f
            d.vy += my * inv * f
          }
        }
        d.vx *= 0.86
        d.vy *= 0.86
        d.x += (tx - d.x) * stiff + d.vx * dt
        d.y += (ty - d.y) * stiff + d.vy * dt
        ctx.fillRect(d.x - r, d.y - r, r * 2, r * 2)
      }
      const gl = Math.max(n.glow, n.hover * 0.8)
      if (gl > 0.02) {
        const g = ctx.createRadialGradient(at.x, at.y, 0, at.x, at.y, s * 1.1)
        g.addColorStop(0, rgba(JADE, 0.25 * gl))
        g.addColorStop(1, rgba(JADE, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(at.x, at.y, s * 1.1, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.font = `500 ${Math.round(10 + 2 * near + n.hover * 3)}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
      ctx.textAlign = 'center'
      ctx.textBaseline = 'top'
      ctx.fillStyle = rgba(PAPER, 0.3 + 0.35 * near + n.glow * 0.4 + n.hover * 0.4)
      ctx.fillText(n.v.name, at.x, at.y + s * 0.52 + 6)
    }

    const draw = (dt: number) => {
      const { narrow, pivot, R, logo } = geometry()
      cam.yaw += (cam.tyaw - cam.yaw) * Math.min(1, dt * 2.2)
      cam.pitch += (cam.tpitch - cam.pitch) * Math.min(1, dt * 2.2)

      // 底层：整幅重画
      ctx = bgCtx
      ctx.globalCompositeOperation = 'source-over'
      if (base) ctx.drawImage(base, 0, 0, w, h)
      ctx.globalCompositeOperation = 'lighter'
      drawSky(narrow)
      ctx.globalCompositeOperation = 'source-over'
      drawLand()
      ctx.globalCompositeOperation = 'lighter'
      drawLantern()
      ctx.globalCompositeOperation = 'source-over'

      // 上层：拖尾——每帧把已有的内容擦淡一些，而不是盖墨，底层才能透上来
      ctx = fgCtx
      if (reduced) {
        ctx.clearRect(0, 0, w, h)
      } else {
        ctx.globalCompositeOperation = 'destination-out'
        ctx.fillStyle = 'rgba(0,0,0,0.3)'
        ctx.fillRect(0, 0, w, h)
      }
      ctx.globalCompositeOperation = 'lighter'
      drawMotes(pivot, R, dt)
      ctx.globalCompositeOperation = 'source-over'

      const spots = VENDORS.map((_, i) => project(orbitPoint(i < OUTER.length, vendorAngle(i), R), pivot, R))

      // 轨道：靠近镜头的半边更亮
      ctx.lineWidth = 1
      for (const outer of [true, false]) {
        const N = 96
        for (let i = 0; i < N; i++) {
          const p0 = project(orbitPoint(outer, (i / N) * Math.PI * 2, R), pivot, R)
          const p1 = project(orbitPoint(outer, ((i + 1) / N) * Math.PI * 2, R), pivot, R)
          ctx.strokeStyle = rgba(JADE, 0.04 + 0.1 * ((p0.z + p1.z) / 2 / (R * 1.12) + 1) / 2)
          ctx.beginPath()
          ctx.moveTo(p0.x, p0.y)
          ctx.lineTo(p1.x, p1.y)
          ctx.stroke()
        }
      }

      // 经脉：调用方 → 中枢 → 各家
      for (let i = 0; i < CLIENTS; i++) {
        const c = clientPt(i, narrow)
        const k = ctrlIn(c, pivot)
        ctx.strokeStyle = rgba(JADE, 0.07)
        ctx.beginPath()
        ctx.moveTo(c.x, c.y)
        ctx.quadraticCurveTo(k.x, k.y, pivot.x, pivot.y)
        ctx.stroke()
      }
      for (const m of spots) {
        const k = ctrlOut(pivot, m)
        ctx.strokeStyle = rgba(JADE, 0.03 + 0.05 * (Math.max(-1, Math.min(1, m.z / R)) + 1) / 2)
        ctx.beginPath()
        ctx.moveTo(pivot.x, pivot.y)
        ctx.quadraticCurveTo(k.x, k.y, m.x, m.y)
        ctx.stroke()
      }

      // 远处的 logo → 浑天仪后半 → 光晕和"枢" → 浑天仪前半 → 近处的 logo
      const order = spots.map((p, i) => ({ p, i })).sort((a, b) => a.p.z - b.p.z)
      for (const n of nodes) n.glow = Math.max(0, n.glow - dt * 2.4)
      for (const { p, i } of order) if (p.z < 0 && nodes[i]) drawNode(nodes[i], p, logo, dt, R)
      drawArmillary(pivot, R, false)
      drawCore(pivot, R)
      drawArmillary(pivot, R, true)
      for (const { p, i } of order) if (p.z >= 0 && nodes[i]) drawNode(nodes[i], p, logo, dt, R)

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
            if (nodes[p.target]) nodes[p.target].glow = 1
            pulses.splice(i, 1)
            continue
          }
        }
        const c = clientPt(p.client, narrow)
        const m = spots[p.target]
        const pos = p.leg === 0 ? bezier(c, ctrlIn(c, pivot), pivot, p.t) : bezier(pivot, ctrlOut(pivot, m), m, p.t)
        const rad = 6 + (p.leg === 1 ? 2 * m.k : 0)
        const g = ctx.createRadialGradient(pos.x, pos.y, 0, pos.x, pos.y, rad)
        g.addColorStop(0, rgba(p.color, 0.95))
        g.addColorStop(0.35, rgba(p.color, 0.45))
        g.addColorStop(1, rgba(p.color, 0))
        ctx.fillStyle = g
        ctx.beginPath()
        ctx.arc(pos.x, pos.y, rad, 0, Math.PI * 2)
        ctx.fill()
      }
      ctx.globalCompositeOperation = 'source-over'
    }

    const tick = (now: number) => {
      const dt = Math.min((now - last) / 1000, 0.05)
      last = now
      time += dt
      if (Math.random() < dt * 6) spawnPulse()
      draw(dt)
      raf = requestAnimationFrame(tick)
    }

    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(canvas)
    window.addEventListener('pointermove', onMove)
    document.addEventListener('pointerleave', onLeave)

    // 等宋体（中枢的"枢"）和各家 logo 都准备好再开始画
    document.fonts
      .load('900 40px LingshuSerif')
      .catch(() => {})
      .then(() => Promise.all(VENDORS.map(sampleLogo)))
      .then((sampled) => {
        if (cancelled) return
        const { pivot } = geometry()
        nodes = VENDORS.map((v, i) => ({
          v,
          glow: 0,
          hover: 0,
          // 入场：粒子从中枢迸发出去，各自聚成 logo
          dots: sampled[i].map((p) => {
            const a = Math.random() * Math.PI * 2
            const sp = reduced ? 0 : 200 + Math.random() * 500
            return { ...p, x: pivot.x, y: pivot.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp, phase: Math.random() * Math.PI * 2 }
          }),
        }))
        if (reduced) {
          for (let i = 0; i < 14; i++) spawnPulse()
          pulses.forEach((p, i) => ((p.t = (i % 7) / 7), (p.leg = (i % 2) as 0 | 1)))
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
      window.removeEventListener('pointermove', onMove)
      document.removeEventListener('pointerleave', onLeave)
    }
  }, [])

  return (
    <>
      <canvas ref={bgRef} className="ls-meridian" aria-hidden="true" />
      <canvas ref={ref} className="ls-meridian" aria-hidden="true" />
    </>
  )
}
