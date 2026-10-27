// 渠道的上游余额：列表里的"额度"列 + 点开的查询弹窗。
// 只有 DeepSeek、Kimi、OpenRouter 有余额接口；查到的数字按厂商原始币种显示，不做汇率换算。
import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Modal, Space, Typography } from 'antd'
import { SyncOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'
import { post, type Channel } from '../api'
import { useApp } from '../app'

dayjs.extend(relativeTime)

const QUERYABLE = ['deepseek', 'moonshot', 'openrouter']

export const canQueryBalance = (template: string) => QUERYABLE.includes(template)

const symbol = (currency: string) => (currency === 'CNY' ? '¥' : currency === 'USD' ? '$' : `${currency} `)

export function formatBalance(items: Channel['balance'] | undefined) {
  if (!items?.length) return ''
  return items.map((b) => `${symbol(b.currency)}${Number(b.amount).toFixed(2)}`).join(' + ')
}

// BalanceCell 是渠道列表里的"额度"列。
export function BalanceCell({ channel, onOpen }: { channel: Channel; onOpen: () => void }) {
  const { t, lang } = useApp()
  dayjs.locale(lang === 'zh' ? 'zh-cn' : 'en')
  if (!canQueryBalance(channel.template)) {
    return (
      <Typography.Text type="secondary" style={{ cursor: 'help' }} title={t('这个渠道没有余额接口', 'No balance API for this channel')}>
        —
      </Typography.Text>
    )
  }
  const value = formatBalance(channel.balance)
  return (
    <div style={{ cursor: 'pointer', lineHeight: 1.3, whiteSpace: 'nowrap' }} onClick={onOpen}>
      <Typography.Link style={{ fontWeight: 500, whiteSpace: 'nowrap' }}>{value || t('查询', 'Check')}</Typography.Link>
      <div className="muted" style={{ fontSize: 12 }}>
        {channel.balance_error ? (
          <Typography.Text type="warning" style={{ fontSize: 12 }}>
            {t('查询失败', 'Failed')}
          </Typography.Text>
        ) : channel.balance_checked_at ? (
          dayjs(channel.balance_checked_at).fromNow()
        ) : (
          t('未查询', 'Never')
        )}
      </div>
    </div>
  )
}

// BalanceModal 打开时自动查一次，失败保留上一次的数字并显示原因。
export function BalanceModal({
  channel,
  onClose,
  onChanged,
}: {
  channel: Channel | null
  onClose: () => void
  onChanged: () => void
}) {
  const { t } = useApp()
  const { message } = App.useApp()
  const [querying, setQuerying] = useState(false)
  const [balance, setBalance] = useState<Channel['balance']>([])
  const [checkedAt, setCheckedAt] = useState<string | null>(null)
  const [error, setError] = useState('')

  const query = useCallback(
    async (id: number) => {
      setQuerying(true)
      try {
        const res = await post<{ balance: Channel['balance']; checked_at: string }>(`/api/admin/channels/${id}/balance`)
        setBalance(res.balance)
        setCheckedAt(res.checked_at)
        setError('')
      } catch (e) {
        setError((e as Error).message)
        message.error((e as Error).message)
      } finally {
        setQuerying(false)
        onChanged()
      }
    },
    [message, onChanged],
  )

  useEffect(() => {
    if (!channel) return
    setBalance(channel.balance)
    setCheckedAt(channel.balance_checked_at)
    setError(channel.balance_error)
    if (canQueryBalance(channel.template)) query(channel.id)
    // 只在换渠道时重置并查询，查询结果回来触发的列表刷新不重新查
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channel?.id])

  const value = formatBalance(balance)

  return (
    <Modal
      open={channel !== null}
      onCancel={onClose}
      title={`${t('查询额度', 'Vendor balance')} · ${channel?.name ?? ''}`}
      footer={null}
      width={460}
      destroyOnHidden
    >
      {channel && !canQueryBalance(channel.template) ? (
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {t(
            '这个渠道没有余额接口。目前只有 DeepSeek、Kimi、OpenRouter 支持查询。',
            'This channel has no balance API. Only DeepSeek, Kimi and OpenRouter support it.',
          )}
        </Typography.Paragraph>
      ) : (
        <>
          <div style={{ fontSize: 30, fontWeight: 600, minHeight: 42 }}>
            {querying && !value ? t('查询中…', 'Checking…') : value || t('没查到', 'No data')}
          </div>
          <div className="muted" style={{ fontSize: 12, marginBottom: 12 }}>
            {checkedAt ? `${t('查询于', 'Checked')} ${dayjs(checkedAt).format('MM-DD HH:mm')}` : t('还没查过', 'Never checked')}
          </div>
          {error && <Alert type="warning" showIcon title={error} style={{ marginBottom: 12 }} />}
          <Space align="center" wrap>
            <Button icon={<SyncOutlined spin={querying} />} disabled={querying || !channel} onClick={() => channel && query(channel.id)}>
              {t('重新查询', 'Check again')}
            </Button>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('用这个渠道的 key 向厂商查一次，只显示，不参与计费。', 'Uses this channel key; display only.')}
            </Typography.Text>
          </Space>
        </>
      )}
    </Modal>
  )
}
