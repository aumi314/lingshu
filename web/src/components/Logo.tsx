// 灵枢的标志：玉琮俯视（外方内圆，"天圆地方"）。和 assets/brand/logo.svg 是同一个图形；
// 页面里内联绘制，渐变 id 用 useId 防止同一页面多个标志互相冲突。
import { useId } from 'react'

export default function Logo({ size = 28 }: { size?: number }) {
  const id = useId()
  const light = `${id}-l`
  const dark = `${id}-d`
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden style={{ display: 'block', flex: 'none' }}>
      <defs>
        <linearGradient id={light} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#a8f5df" />
          <stop offset=".55" stopColor="#4fcfac" />
          <stop offset="1" stopColor="#1f8c70" />
        </linearGradient>
        <linearGradient id={dark} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#3fbf9c" />
          <stop offset="1" stopColor="#0f5c49" />
        </linearGradient>
      </defs>
      <rect width="64" height="64" rx="14" fill="#0b0d12" />
      <path d="M14 10H50L54 14V50L50 54H14L10 50V14Z" fill={`url(#${dark})`} />
      <path d="M10 24.7H18M46 24.7H54M10 39.3H18M46 39.3H54" stroke="#0b0d12" strokeWidth="2.2" opacity=".35" />
      <circle cx="32" cy="32" r="15" fill={`url(#${light})`} />
      <circle cx="32" cy="32" r="15" stroke="#0b0d12" strokeWidth="1.6" fill="none" opacity=".4" />
      <circle cx="32" cy="32" r="8.5" fill="#0b0d12" />
    </svg>
  )
}
