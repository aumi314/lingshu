import { useCallback, useEffect, useState } from 'react'
import { App, Button, Card, DatePicker, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Space, Table, Tag, Typography } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { del, get, post } from '../api'
import { useApp } from '../app'

// 兑换码充值（2026-11-06 决策）：明文只在生成时显示一次，可复制或下载 CSV；库里只存哈希。
type RedeemCode = { id: number; prefix: string; amount: string; note: string; expires_at: string | null; created_at: string; used_by: string; used_at: string | null; revoked_at: string | null }
type Generated = { id: number; prefix: string; code: string }
type FormValues = { count: number; amount: number; currency: 'USD' | 'CNY'; note?: string; expires_at?: Dayjs }

export default function RedeemCodes() {
  const { money, t, currency } = useApp()
  const { message } = App.useApp()
  const [codes, setCodes] = useState<RedeemCode[]>([])
  const [loading, setLoading] = useState(true)
  const [generating, setGenerating] = useState(false)
  const [generated, setGenerated] = useState<{ codes: Generated[]; amount: string } | null>(null)
  const [form] = Form.useForm<FormValues>()

  const load = useCallback(async () => {
    try {
      setCodes(await get<RedeemCode[]>('/api/admin/redeem-codes'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])
  useEffect(() => { void load() }, [load])

  const generate = async () => {
    const v = await form.validateFields()
    setGenerating(true)
    try {
      const created = await post<Generated[]>('/api/admin/redeem-codes', { count: v.count, amount: String(v.amount), currency: v.currency, note: v.note ?? '', expires_at: v.expires_at ? v.expires_at.endOf('day').toISOString() : null })
      setGenerated({ codes: created, amount: `${v.currency === 'CNY' ? '¥' : '$'}${v.amount}` })
      await load()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setGenerating(false)
    }
  }

  const download = () => {
    if (!generated) return
    const csv = 'code,amount\n' + generated.codes.map((c) => `${c.code},${generated.amount}`).join('\n') + '\n'
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }))
    a.download = `lingshu-redeem-codes-${dayjs().format('YYYYMMDD-HHmm')}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const status = (c: RedeemCode) => c.used_at ? <Tag color="green">{t('已使用', 'Used')}</Tag>
    : c.revoked_at ? <Tag>{t('已撤销', 'Revoked')}</Tag>
      : c.expires_at && dayjs(c.expires_at).isBefore(dayjs()) ? <Tag>{t('已过期', 'Expired')}</Tag>
        : <Tag color="blue">{t('未使用', 'Unused')}</Tag>

  // 放在「系统设置 → 支付设置」的第二张卡片（2026-11-07 从单独的标签页合并过来），id 供旧链接 ?tab=redeem 定位
  return (
    <Card id="redeem-codes" title={t('兑换码', 'Redeem codes')}>
        <Typography.Paragraph type="secondary">
          {t('用户在「钱包充值」输入兑换码，面额立即到账；不受上面“开放在线支付”开关影响，没接支付通道也能用。', 'Users enter a code under Wallet and the amount is credited at once. Works regardless of the online payment switch above, even without any payment service.')}
          {t('可以把兑换码放到第三方发卡平台售卖（例如 ', 'You can sell codes on third-party card-selling platforms (e.g. ')}
          <a href="https://wzyp.cn" target="_blank" rel="noopener noreferrer">wzyp.cn</a>
          {t('），用户买到后自己兑换。', '); buyers redeem them themselves.')}
          {t('每个码只能用一次；完整的码只在生成后显示一次，请马上复制或下载保存。', 'Each code works once; full codes are shown only right after generation, so copy or download them now.')}
        </Typography.Paragraph>
        <Form form={form} layout="inline" initialValues={{ count: 1, currency: currency === 'CNY' ? 'CNY' : 'USD' }} onFinish={generate} style={{ rowGap: 12 }}>
          <Form.Item name="count" label={t('数量', 'Count')} rules={[{ required: true }]}>
            <InputNumber min={1} max={100} precision={0} style={{ width: 90 }} />
          </Form.Item>
          <Form.Item label={t('面额', 'Amount')} required>
            <Space.Compact>
              <Form.Item name="currency" noStyle><Segmented options={[{ value: 'USD', label: '$' }, { value: 'CNY', label: '¥' }]} /></Form.Item>
              <Form.Item name="amount" noStyle rules={[{ required: true, message: t('填写面额', 'Enter an amount') }]}><InputNumber min={0.01} step={1} style={{ width: 120 }} /></Form.Item>
            </Space.Compact>
          </Form.Item>
          <Form.Item name="expires_at" label={t('有效期至', 'Expires')}>
            <DatePicker disabledDate={(d) => d.isBefore(dayjs(), 'day')} placeholder={t('不过期', 'Never')} />
          </Form.Item>
          <Form.Item name="note" label={t('备注', 'Note')}>
            <Input maxLength={200} placeholder={t('例如：11 月活动', 'e.g. November promo')} style={{ width: 200 }} />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={generating}>{t('生成', 'Generate')}</Button>
        </Form>
      <Table<RedeemCode>
        style={{ marginTop: 16 }}
        rowKey="id"
        size="small"
        loading={loading}
        dataSource={codes}
        scroll={{ x: 'max-content' }}
        locale={{ emptyText: t('还没有兑换码', 'No codes yet') }}
        columns={[
          { title: t('前缀', 'Prefix'), dataIndex: 'prefix', render: (v: string) => <span className="mono">{v}…</span> },
          { title: t('面额', 'Amount'), dataIndex: 'amount', align: 'right', render: (v: string) => money(v) },
          { title: t('状态', 'Status'), render: (_, c) => status(c) },
          { title: t('使用者', 'Used by'), render: (_, c) => c.used_by ? <span>{c.used_by}<div className="muted">{dayjs(c.used_at).format('MM-DD HH:mm')}</div></span> : '—' },
          { title: t('有效期至', 'Expires'), dataIndex: 'expires_at', render: (v: string | null) => v ? dayjs(v).format('YYYY-MM-DD') : t('不过期', 'Never') },
          { title: t('备注', 'Note'), dataIndex: 'note', render: (v: string) => v || '—' },
          { title: t('创建时间', 'Created'), dataIndex: 'created_at', render: (v: string) => dayjs(v).format('YYYY-MM-DD HH:mm') },
          {
            title: '', align: 'right', render: (_, c) => !c.used_at && !c.revoked_at && (
              <Popconfirm title={t('撤销后这个码不能再兑换，确定？', 'Revoke this code? It can no longer be redeemed.')} onConfirm={async () => {
                try { await del(`/api/admin/redeem-codes/${c.id}`); await load() } catch (e) { message.error((e as Error).message) }
              }}>
                <Button size="small" type="link" danger>{t('撤销', 'Revoke')}</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Modal
        open={generated !== null}
        title={t(`已生成 ${generated?.codes.length ?? 0} 个兑换码（每个 ${generated?.amount}）`, `${generated?.codes.length ?? 0} codes generated (${generated?.amount} each)`)}
        okText={t('我已保存', "I've saved them")}
        cancelButtonProps={{ style: { display: 'none' } }}
        onOk={() => setGenerated(null)}
        onCancel={() => setGenerated(null)}
        destroyOnHidden // 关闭后明文从页面上移除
      >
        <Typography.Paragraph type="warning">{t('完整的码只显示这一次，关闭后无法再查看。', 'Full codes are shown only once and cannot be viewed after closing.')}</Typography.Paragraph>
        <Typography.Paragraph copyable={{ text: generated?.codes.map((c) => c.code).join('\n') }}>
          <pre className="mono" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{generated?.codes.map((c) => c.code).join('\n')}</pre>
        </Typography.Paragraph>
        <Button onClick={download}>{t('下载 CSV', 'Download CSV')}</Button>
      </Modal>
    </Card>
  )
}
