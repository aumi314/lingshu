// 渠道模板对应的厂商图标（图标是打包进来的本地文件，可以直接内联）。
// MiMo 的官方图标是一行小字，这个尺寸看不清，改用小米橙底的 "Mi" 方块。
import deepseek from '../assets/logos/deepseek-color.svg?raw'
import zhipu from '../assets/logos/zhipu-color.svg?raw'
import qwen from '../assets/logos/qwen-color.svg?raw'
import stepfun from '../assets/logos/stepfun-color.svg?raw'
import minimax from '../assets/logos/minimax-color.svg?raw'
import doubao from '../assets/logos/doubao-color.svg?raw'
import kimi from '../assets/logos/kimi.svg?raw'

const icons: Record<string, string> = {
  deepseek,
  zhipu,
  dashscope: qwen,
  stepfun,
  minimax,
  volcengine: doubao,
  moonshot: kimi,
}

const mimo = (size: number) => (
  <span
    aria-hidden
    style={{
      display: 'inline-flex',
      alignItems: 'center',
      justifyContent: 'center',
      width: size,
      height: size,
      borderRadius: size / 4,
      background: '#FF6900',
      color: '#fff',
      fontSize: size * 0.5,
      fontWeight: 700,
      lineHeight: 1,
    }}
  >
    Mi
  </span>
)

export default function VendorIcon({ template, size = 18 }: { template?: string; size?: number }) {
  if (template === 'mimo') return mimo(size)
  const svg = template ? icons[template] : undefined
  if (!svg) return null
  return (
    <span
      aria-hidden
      style={{ display: 'inline-flex', fontSize: size }}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
