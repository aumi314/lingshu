// 渠道模板对应的厂商图标（图标是打包进来的本地文件，可以直接内联）。
// MiMo 的官方图标是一行小字，这个尺寸看不清，改用小米的标志。
import deepseek from '../assets/logos/deepseek-color.svg?raw'
import zhipu from '../assets/logos/zhipu-color.svg?raw'
import qwen from '../assets/logos/qwen-color.svg?raw'
import stepfun from '../assets/logos/stepfun-color.svg?raw'
import minimax from '../assets/logos/minimax-color.svg?raw'
import doubao from '../assets/logos/doubao-color.svg?raw'
import kimi from '../assets/logos/kimi.svg?raw'
import xiaomi from '../assets/logos/xiaomi-color.svg?raw'
import hunyuan from '../assets/logos/hunyuan-color.svg?raw'
import openrouter from '../assets/logos/openrouter.svg?raw'
import nace from '../assets/logos/nace.svg?raw'
import typesafe from '../assets/logos/typesafe.png'

const icons: Record<string, string> = {
  deepseek,
  zhipu,
  dashscope: qwen,
  stepfun,
  minimax,
  volcengine: doubao,
  moonshot: kimi,
  mimo: xiaomi,
  hunyuan,
  openrouter,
  nace,
}

export default function VendorIcon({ template, size = 18 }: { template?: string; size?: number }) {
  if (template === 'typesafe') {
    return (
      <span aria-hidden style={{ display: 'inline-flex', width: size, height: size }}>
        <img src={typesafe} alt="" style={{ width: '100%', height: '100%', objectFit: 'contain' }} />
      </span>
    )
  }
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
