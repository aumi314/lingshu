import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Card, Col, Form, Input, InputNumber, Modal, Row, Select, Space, Statistic, Table, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import dayjs from 'dayjs'
import { get, post, type LogsResponse } from '../api'
import { useApp } from '../app'
import { paymentNames, payOrder, type OrderList, type PaymentMethod, type Topup } from '../payment'

type Available = { provider: string; currency: string; min_usd: string; max_usd: string; methods: PaymentMethod[] }

export default function Wallet() {
  const { user, money, t, reload } = useApp()
  const { message } = App.useApp()
  const [params] = useSearchParams()
  const [form] = Form.useForm()
  const [methods, setMethods] = useState<Available[]>([])
  const [totalSpent, setTotalSpent] = useState<string | null>(null)
  const [orders, setOrders] = useState<OrderList>({ items: [], total: 0 })
  const [page, setPage] = useState(1)
  const [quote, setQuote] = useState<Topup | null>(null)
  const [focused, setFocused] = useState<Topup | null>(null)
  const [busy, setBusy] = useState(false)
  const [requestKey, setRequestKey] = useState(() => crypto.randomUUID())
  const provider = Form.useWatch('provider', { form, preserve: true })
  const selected = methods.find((m) => m.provider === provider)
  const loadOrders = useCallback(() => get<OrderList>(`/api/me/topups?page=${page}`).then(setOrders), [page])

  useEffect(() => {
    reload()
    get<LogsResponse>('/api/me/logs?page_size=1').then((v) => setTotalSpent(v.summary.cost)).catch((e) => message.error(e.message))
    get<{ enabled: boolean; methods: Available[] }>('/api/me/payment-methods').then((v) => {
      setMethods(v.methods)
      if (v.methods[0]) form.setFieldsValue({ provider: v.methods[0].provider, method: v.methods[0].methods[0]?.id, credit_usd: v.methods[0].min_usd })
    }).catch((e) => message.error(e.message))
  }, [reload, form, message])

  useEffect(() => { loadOrders().catch((e) => message.error(e.message)) }, [loadOrders, message])
  const orderID = params.get('order')
  useEffect(() => {
    if (!orderID) return
    let active = true
    let timer: number | undefined
    const poll = () => get<Topup>(`/api/me/topups/${encodeURIComponent(orderID)}`).then((o) => {
      if (!active) return
      setFocused(o)
      if (o.status === 'success') { window.clearInterval(timer); reload(); loadOrders() }
    }).catch(() => {})
    poll()
    post<{ order: Topup }>(`/api/me/topups/${encodeURIComponent(orderID)}/refresh`).then((v) => { if (active) { setFocused(v.order); reload(); loadOrders() } }).catch(() => {})
    timer = window.setInterval(poll, 5000)
    return () => { active = false; window.clearInterval(timer) }
  }, [orderID, reload, loadOrders])

  const run = async (fn: () => Promise<void>) => {
    setBusy(true)
    try { await fn() } catch (e) { message.error((e as Error).message) } finally { setBusy(false) }
  }
  const refresh = (id: string) => run(async () => {
    const v = await post<{ order: Topup; message?: string }>(`/api/me/topups/${id}/refresh`)
    setFocused(v.order)
    if (v.message) message.info(v.message)
    await reload(); await loadOrders()
  })

  // 兑换码充值：成功后立即刷新余额
  const [redeemCode, setRedeemCode] = useState('')
  const [redeeming, setRedeeming] = useState(false)
  const redeem = async () => {
    if (!redeemCode.trim() || redeeming) return
    setRedeeming(true)
    try {
      const r = await post<{ amount: string; balance: string }>('/api/me/redeem', { code: redeemCode })
      message.success(t(`兑换成功，到账 ${money(r.amount)}`, `Redeemed: ${money(r.amount)} added`))
      setRedeemCode('')
      reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setRedeeming(false)
    }
  }

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('钱包充值', 'Wallet & top-up')}
      </Typography.Title>
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12}>
          <Card>
            <Statistic title={t('当前余额', 'Current balance')} value={money(user?.balance ?? 0)} />
          </Card>
        </Col>
        <Col xs={24} sm={12}>
          <Card>
            <Statistic title={t('累计消费金额', 'Total spent')} value={totalSpent === null ? '—' : money(totalSpent)} />
          </Card>
        </Col>
      </Row>
      {focused && <Alert showIcon type={focused.status === 'success' ? 'success' : 'info'} style={{ marginTop: 16 }} title={focused.status === 'success' ? t('充值已完成', 'Top-up completed') : t('正在核对付款状态，请勿重复支付', 'Checking payment; please do not pay again')} description={<Space wrap><Typography.Text copyable>{focused.id}</Typography.Text><span>{focused.status}</span>{focused.environment === 'test' && <Tag>{t('测试环境，不增加真实余额', 'Test: no wallet credit')}</Tag>}<Button size="small" loading={busy} onClick={() => refresh(focused.id)}>{t('刷新状态', 'Refresh status')}</Button></Space>} />}
      <Card title={t('兑换码', 'Redeem a code')} style={{ marginTop: 16 }}>
        <Space.Compact style={{ width: '100%', maxWidth: 520 }}>
          <Input value={redeemCode} onChange={(e) => setRedeemCode(e.target.value)} onPressEnter={redeem} placeholder={t('输入兑换码，例如 ABCDE-FGHIJ-…', 'Enter a code, e.g. ABCDE-FGHIJ-…')} allowClear className="mono" />
          <Button type="primary" loading={redeeming} onClick={redeem} disabled={!redeemCode.trim()}>{t('兑换', 'Redeem')}</Button>
        </Space.Compact>
      </Card>
      <Card title={t('在线充值', 'Online top-up')} style={{ marginTop: 16 }}>
        {methods.length === 0 ? <Typography.Text type="secondary">{t('在线充值尚未开放，如需充值请联系管理员。', 'Online top-ups are not available. Contact an admin to top up.')}</Typography.Text> : <Form form={form} layout="vertical" onValuesChange={() => setRequestKey(crypto.randomUUID())} onFinish={(v) => run(async () => {
          const order = await post<Topup>('/api/me/topups', { ...v, method: v.provider === 'epay' ? v.method : '', request_key: requestKey })
          setQuote(order); loadOrders()
        })}>
          <Space wrap align="start">
            <Form.Item name="provider" label={t('支付服务', 'Payment service')} rules={[{ required: true }]}><Select style={{ width: 210 }} options={methods.map((m) => ({ value: m.provider, label: paymentNames[m.provider] }))} onChange={(p) => form.setFieldValue('method', methods.find((m) => m.provider === p)?.methods[0]?.id)} /></Form.Item>
            {provider === 'epay' && <Form.Item name="method" label={t('支付方式', 'Payment method')} rules={[{ required: true }]}><Select style={{ width: 160 }} options={selected?.methods.map((m) => ({ value: m.id, label: m.name }))} /></Form.Item>}
            <Form.Item name="credit_usd" label={t('美元到账额度（USD）', 'Wallet credit (USD)')} rules={[{ required: true }]}><InputNumber stringMode precision={2} min={selected?.min_usd} max={selected?.max_usd} prefix="$" style={{ width: 190 }} /></Form.Item>
          </Space>
          <Typography.Paragraph type="secondary">{t('此处输入到账的美元额度。点击继续后核对实际付款金额，支付服务如收取税费，会在收银台显示。', 'Enter the USD credit you want. Continue to review the payment quote; any provider taxes are shown at checkout.')}</Typography.Paragraph>
          <Button type="primary" htmlType="submit" loading={busy}>{t('继续', 'Continue')}</Button>
        </Form>}
      </Card>
      <Card title={t('充值记录', 'Top-up history')} style={{ marginTop: 16 }}>
        <Table rowKey="id" size="small" scroll={{ x: 800 }} dataSource={orders.items} pagination={{ current: page, total: orders.total, pageSize: 20, showSizeChanger: false, onChange: setPage }} columns={[
          { title: t('订单号', 'Order'), dataIndex: 'id', render: (v) => <Typography.Text copyable>{v}</Typography.Text> },
          { title: t('服务', 'Provider'), dataIndex: 'provider', render: (v) => paymentNames[v] },
          { title: t('美元到账额度', 'USD credit'), dataIndex: 'credit_usd', render: (v) => `$${v}` },
          { title: t('付款报价', 'Payment quote'), render: (_, o) => `${o.amount} ${o.currency}` },
          { title: t('税费', 'Tax'), render: (_, o) => `${o.tax} ${o.currency}` },
          { title: t('实际支付合计', 'Paid total'), render: (_, o) => o.status === 'success' ? `${o.total} ${o.currency}` : '—' },
          { title: t('状态', 'Status'), render: (_, o) => <Space><Tag color={o.status === 'success' ? 'green' : 'default'}>{o.status === 'success' ? t('成功', 'Success') : o.status === 'failed' ? t('失败', 'Failed') : o.status === 'expired' ? t('已过期', 'Expired') : t('待付款确认', 'Pending')}</Tag>{o.environment === 'test' && <Tag>test</Tag>}</Space> },
          { title: t('创建时间', 'Created'), dataIndex: 'created_at', render: (v) => dayjs(v).format('YYYY-MM-DD HH:mm') },
          { title: '', render: (_, o) => o.status !== 'success' && <Space>{o.status === 'pending' && <Button size="small" onClick={() => setQuote(o)}>{t('继续付款', 'Continue payment')}</Button>}<Button size="small" loading={busy} onClick={() => refresh(o.id)}>{t('核对付款', 'Check payment')}</Button></Space> },
        ]} />
      </Card>
      <Modal open={!!quote} title={t('确认充值', 'Confirm top-up')} onCancel={() => { setQuote(null); setRequestKey(crypto.randomUUID()) }} okText={t('前往付款', 'Continue to payment')} confirmLoading={busy} onOk={() => quote && run(() => payOrder(quote.id))}>
        {quote && <><p>{t('到账额度', 'Wallet credit')}：<b>${quote.credit_usd} USD</b></p><p>{t('应付金额（不含可能的税费）', 'Payment before any applicable taxes')}：<b>{quote.amount} {quote.currency}</b></p><p>{t('订单号', 'Order')}：{quote.id}</p><p>{t('付款成功并确认后自动到账。', 'Your wallet is credited after payment is confirmed.')}</p></>}
      </Modal>
    </div>
  )
}
