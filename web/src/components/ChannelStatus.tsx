import { Badge, Space, Tag, Tooltip, Typography } from 'antd'
import dayjs from 'dayjs'
import type { Channel } from '../api'
import { useApp } from '../app'

// 渠道页「连接状态」列（2026-11-06）：最近 24 小时的成功率和次数，次行是首字、输出速度（中位数）和缓存命中率。
// 统计口径见 internal/store/channel_stats.go；冷却中（连续失败暂停）也放在这里。
export default function ChannelStatus({ channel: c }: { channel: Channel }) {
  const { t } = useApp()
  const cooling = c.cooling && (
    <Tooltip title={t('连续失败，暂停 5 分钟后自动恢复', 'Paused after repeated failures; resumes automatically in 5 minutes')}>
      <Tag color="orange">{t('冷却中', 'Cooling')}</Tag>
    </Tooltip>
  )
  if (!c.models.length) return <Space size={4}><Typography.Text type="warning">{t('未配置模型', 'No models')}</Typography.Text>{cooling}</Space>

  const s = c.stats
  const total = s ? s.success + s.failed : 0
  if (!s || total === 0) return <Space size={4}><span className="muted">{t('24 小时无调用', 'No calls in 24h')}</span>{cooling}</Space>

  // 向下取整：有失败时不显示成 100%
  const pct = Math.floor((s.success * 100) / total)
  const status = pct >= 95 ? 'success' : pct >= 80 ? 'warning' : 'error'
  const details = [
    s.first_token_ms !== null && `${t('首字', 'TTFT')} ${(s.first_token_ms / 1000).toFixed(1)}s`,
    s.tokens_per_sec !== null && `${s.tokens_per_sec} tok/s`,
    s.input_tokens > 0 && `${t('缓存', 'cache')} ${Math.floor((s.cache_read_tokens * 100) / s.input_tokens)}%`,
  ].filter(Boolean)
  const when = (v: string | null) => (v ? dayjs(v).format('MM-DD HH:mm') : '—')

  return (
    <Tooltip
      title={<>
        <div>{t(`最近 24 小时：成功 ${s.success} 次，失败 ${s.failed} 次`, `Last 24h: ${s.success} succeeded, ${s.failed} failed`)}</div>
        <div>{t('最近成功', 'Last success')} {when(s.last_success_at)} · {t('最近失败', 'last failure')} {when(s.last_failure_at)}</div>
      </>}
    >
      <div className="nowrap">
        <Space size={4}>
          <Badge status={status} text={t(`成功率 ${pct}% · ${total} 次`, `${pct}% ok · ${total}`)} />
          {cooling}
        </Space>
        {details.length > 0 && <div className="muted" style={{ fontSize: 12 }}>{details.join(' · ')}</div>}
      </div>
    </Tooltip>
  )
}
