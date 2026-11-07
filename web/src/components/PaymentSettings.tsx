import { useEffect, useState } from 'react'
import { Alert, App, Button, Collapse, Form, Input, InputNumber, Modal, Select, Space, Switch, Tag, Typography } from 'antd'
import { ApiError, get, post, put } from '../api'
import { useApp } from '../app'
import { paymentNames, payOrder, type PaymentService, type Topup } from '../payment'

const valueFields: Record<string, [string, string][]> = {
  epay: [['gateway', '支付网关 / Gateway URL'], ['merchant_id', '商户 ID / Merchant ID']],
  stripe: [],
  creem: [['product_id', '一次性商品 ID / One-time product ID']],
  waffo: [['merchant_id', '商户 ID / Merchant ID'], ['public_key', '平台公钥 / Platform public key']],
  waffo_pancake: [['merchant_id', '商户 ID / Merchant ID'], ['store_id', '店铺 ID / Store ID'], ['product_id', '一次性商品 ID / One-time product ID']],
}
const secretFields: Record<string, [string, string][]> = {
  epay: [['key', '商户密钥 / Merchant key']],
  stripe: [['api_key', 'API Key'], ['webhook_secret', 'Webhook Secret']],
  creem: [['api_key', 'API Key'], ['webhook_secret', 'Webhook Secret']],
  waffo: [['api_key', 'API Key'], ['private_key', '签名私钥 / Private key']],
  waffo_pancake: [['private_key', '签名私钥 / Private key']],
}

function ServiceForm({ service, onSave }: { service: PaymentService; onSave: () => void }) {
  const { t } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm()
  const [busy, setBusy] = useState('')
  const [clearSecrets, setClearSecrets] = useState<string[]>([])
  const [catalog, setCatalog] = useState<{ id: string; name: string; onetimeProducts: { id: string; name: string }[] }[]>([])
  const [testOrder, setTestOrder] = useState<Topup | null>(null)
  const c = service.config
  const provider = c.provider
  useEffect(() => {
    form.resetFields()
    form.setFieldsValue({ ...c, secrets: {} })
    setClearSecrets([])
  }, [c, form])

  const run = async (name: string, fn: () => Promise<void>) => {
    setBusy(name)
    try { await fn() } catch (e) { message.error((e as Error).message) } finally { setBusy('') }
  }
  const payload = async (enabled?: boolean) => {
    const v = await form.validateFields()
    return { ...v, ...(provider === 'epay' ? { environment: 'prod' } : {}), ...(enabled === undefined ? {} : { enabled }), clear_secrets: clearSecrets }
  }
  const save = () => run('save', async () => {
    await put(`/api/admin/payments/${provider}`, await payload())
    message.success(t('已保存', 'Saved')); onSave()
  })
  const probe = () => run('probe', async () => {
    await post(`/api/admin/payments/${provider}/probe`, await payload(false))
    message.success(t('支付服务验证通过', 'Payment service verified'))
  })
  const fetchCatalog = () => run('catalog', async () => {
    const result = await post<{ stores: typeof catalog }>('/api/admin/payments/waffo_pancake/catalog', await payload(false))
    setCatalog(result.stores ?? [])
    message.success(t('目录已加载', 'Catalog loaded'))
  })
  const createProduct = () => run('create', async () => {
    try {
      const result = await post<{ store_id: string; product_id: string }>('/api/admin/payments/waffo_pancake/create-product', await payload(false))
      form.setFieldValue(['values', 'store_id'], result.store_id)
      form.setFieldValue(['values', 'product_id'], result.product_id)
      message.success(t('店铺和商品已创建，请保存绑定', 'Store and product created; save to bind them'))
    } catch (e) {
      if (e instanceof ApiError && e.data) {
        if (e.data.store_id) form.setFieldValue(['values', 'store_id'], e.data.store_id)
        if (e.data.product_id) form.setFieldValue(['values', 'product_id'], e.data.product_id)
      }
      throw e
    }
  })
  const storeID = Form.useWatch(['values', 'store_id'], form)
  return (
    <>
      <Form form={form} layout="vertical" requiredMark={false}>
        <Space wrap align="start">
          <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked"><Switch /></Form.Item>
          {provider === 'epay' ? <Form.Item label={t('支付环境', 'Environment')}><Typography.Text>{t('生产', 'Production')}</Typography.Text></Form.Item> : <Form.Item name="environment" label={t('支付环境', 'Environment')}><Select style={{ width: 150 }} options={[{ value: 'prod', label: t('生产', 'Production') }, { value: 'test', label: t('测试（不增加真实余额）', 'Test (no wallet credit)') }]} /></Form.Item>}
          <Form.Item name="currency" label={t('收款币种', 'Currency')}><Input readOnly style={{ width: 90 }} /></Form.Item>
          {/* 收人民币的服务单价跟随系统汇率（运营设置里的「汇率」），不单独填 */}
          <Form.Item name="unit_price" label={t('每 $1 到账额度的实付金额', 'Payment per $1 wallet credit')} rules={[{ required: true }]}
            extra={form.getFieldValue('currency') === 'CNY' ? t('跟随系统汇率，在「运营设置 → 汇率」里改', 'Follows the system exchange rate; change it under Operations → Exchange rate') : undefined}>
            <InputNumber stringMode min="0.0000000001" style={{ width: 170 }} disabled={form.getFieldValue('currency') === 'CNY'} />
          </Form.Item>
          <Form.Item name="min_usd" label={t('最低到账额度（USD）', 'Minimum credit (USD)')} rules={[{ required: true }]}><InputNumber stringMode min="0.01" precision={2} /></Form.Item>
          <Form.Item name="max_usd" label={t('最高到账额度（USD）', 'Maximum credit (USD)')} rules={[{ required: true }]}><InputNumber stringMode min="0.01" precision={2} /></Form.Item>
        </Space>
        {valueFields[provider].map(([key, label]) => (
          <Form.Item key={key} name={['values', key]} label={label}>
            <Input autoComplete="off" />
          </Form.Item>
        ))}
        {secretFields[provider].map(([key, label]) => (
          <Form.Item key={key} label={<Space wrap>{label}{c.configured_secrets[key] && <Tag color="green">{t('已配置', 'Configured')}</Tag>}
            {c.configured_secrets[key] && <Button size="small" type="link" danger onClick={() => setClearSecrets((v) => v.includes(key) ? v.filter((x) => x !== key) : [...v, key])}>{clearSecrets.includes(key) ? t('取消清除', 'Keep secret') : t('清除', 'Clear')}</Button>}
          </Space>}>
            <Form.Item name={['secrets', key]} noStyle><Input.Password autoComplete="new-password" placeholder={clearSecrets.includes(key) ? t('保存后清除', 'Will be cleared on save') : t('留空保留已保存的值', 'Leave blank to keep saved value')} /></Form.Item>
          </Form.Item>
        ))}
        {provider === 'epay' && <Form.Item name="methods" label={t('支付方式', 'Payment methods')} getValueProps={(v) => ({ value: (v ?? []).map((m: { id: string }) => m.id) })} getValueFromEvent={(v: string[]) => v.map((id) => ({ id, name: id === 'alipay' ? '支付宝' : id === 'wxpay' ? '微信支付' : id }))}>
          <Select mode="tags" options={[{ value: 'alipay', label: t('支付宝', 'Alipay') }, { value: 'wxpay', label: t('微信支付', 'WeChat Pay') }]} />
        </Form.Item>}
        {provider === 'creem' && <Typography.Paragraph type="secondary">{t('需绑定 USD 一次性商品，付款金额由订单动态设置；最低实付 $1。', 'Bind a USD one-time product. The order sets the price dynamically; minimum payment is $1.')}</Typography.Paragraph>}
        {provider === 'waffo_pancake' && <>
          <Space wrap style={{ marginBottom: 16 }}>
            <Button onClick={fetchCatalog} loading={busy === 'catalog'} disabled={!!busy}>{t('拉取店铺和商品', 'Load stores & products')}</Button>
            <Button onClick={() => Modal.confirm({ title: t('创建 Lingshu 专用商品？', 'Create a Lingshu product?'), content: t('将在当前店铺创建并发布一次性商品；店铺 ID 留空时会先创建新店铺。已有资源会保留。', 'Creates and publishes a one-time product in the selected store, or creates a store first when no store is selected. Existing resources are kept.'), onOk: createProduct })} disabled={!!busy}>{t('创建专用商品', 'Create dedicated product')}</Button>
          </Space>
          {catalog.length > 0 && <Space wrap style={{ marginBottom: 16 }}>
            <Select style={{ minWidth: 210 }} placeholder={t('选择店铺', 'Select store')} value={storeID || undefined} options={catalog.map((s) => ({ value: s.id, label: s.name }))} onChange={(v) => { form.setFieldValue(['values', 'store_id'], v); form.setFieldValue(['values', 'product_id'], '') }} />
            <Select style={{ minWidth: 210 }} placeholder={t('选择一次性商品', 'Select one-time product')} options={(catalog.find((s) => s.id === storeID)?.onetimeProducts ?? []).map((p) => ({ value: p.id, label: p.name }))} onChange={(v) => form.setFieldValue(['values', 'product_id'], v)} />
          </Space>}
        </>}
        {c.version_id > 0 && <Typography.Paragraph>{t('通知地址', 'Notification URL')}：<Typography.Text copyable style={{ wordBreak: 'break-all' }}>{service.notify_url}</Typography.Text></Typography.Paragraph>}
        <Typography.Paragraph type="secondary">{t('更换商户凭据或支付环境后，请在服务商后台更新通知地址；旧地址保留处理已有订单。', 'After changing credentials or environment, update the notification URL in your provider dashboard. Old URLs remain available for existing orders.')}</Typography.Paragraph>
        <Space wrap>
          <Button type="primary" onClick={save} loading={busy === 'save'} disabled={!!busy}>{t('保存', 'Save')}</Button>
          <Button onClick={probe} loading={busy === 'probe'} disabled={!!busy}>{t('验证连接', 'Verify connection')}</Button>
          <Button disabled={!service.ready || !c.version_id || !!busy} onClick={() => run('test', async () => {
            const o = await post<Topup>(`/api/admin/payments/${provider}/test-order`, { credit_usd: c.min_usd, method: c.methods[0]?.id ?? '', request_key: crypto.randomUUID() })
            setTestOrder(o)
          })} loading={busy === 'test'}>{t('测试支付', 'Test payment')}</Button>
        </Space>
      </Form>
      <Modal open={!!testOrder} title={t('确认验收订单', 'Confirm verification order')} onCancel={() => setTestOrder(null)} okText={t('前往付款', 'Continue to payment')} confirmLoading={busy === 'checkout'} onOk={() => testOrder && run('checkout', () => payOrder(testOrder.id))}>
        {testOrder && <><p>{t('到账额度', 'Wallet credit')}：${testOrder.credit_usd} USD</p><p>{t('应付金额（税费以收银台为准）', 'Payment (taxes shown at checkout)')}：{testOrder.amount} {testOrder.currency}</p><p>{testOrder.environment === 'prod' ? t('这是实际收款订单，付款成功后会增加当前账户余额。', 'This is a real payment and will credit your account after payment.') : t('这是测试环境订单，不会增加真实余额。', 'This is a test order and will not credit your wallet.')}</p></>}
      </Modal>
    </>
  )
}

export default function PaymentSettings() {
  const { t } = useApp()
  const { message } = App.useApp()
  const [services, setServices] = useState<PaymentService[]>([])
  const [publicURL, setPublicURL] = useState('')
  const load = () => get<{ services: PaymentService[]; public_url: string }>('/api/admin/payments').then((v) => { setServices(v.services); setPublicURL(v.public_url) }).catch((e) => message.error(e.message))
  useEffect(() => { load() }, []) // eslint-disable-line react-hooks/exhaustive-deps
  return <div style={{ marginTop: 20 }}>
    {!publicURL && <Alert showIcon type="warning" title={t('请设置 LINGSHU_PUBLIC_URL 后启用在线支付', 'Set LINGSHU_PUBLIC_URL before enabling online payments')} style={{ marginBottom: 16 }} />}
    <Collapse items={[
      ...services.map((s) => ({ key: s.config.provider, label: <Space>{paymentNames[s.config.provider]}<Tag color={s.config.enabled && s.ready ? 'green' : 'default'}>{s.config.enabled && s.ready ? t('已启用', 'Enabled') : t('未开放', 'Not available')}</Tag></Space>, children: <ServiceForm service={s} onSave={load} /> })),
      // 占位：计划接入自托管的 BEpusdt 收 USDT，接入前不能配置，用户也看不到这个支付方式
      { key: 'usdt', label: <Space>USDT（BEpusdt）<Tag>{t('规划中', 'Planned')}</Tag></Space>, children: <Typography.Text type="secondary">{t('计划接入 BEpusdt（自托管的 USDT 收款网关）。接入前不能配置，用户的充值页也不会出现这个支付方式。', 'BEpusdt (self-hosted USDT gateway) is planned. Not configurable yet, and users will not see it as a payment method.')}</Typography.Text> },
    ]} />
    <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>{t('充值记录在「全站日志 → 操作日志」里查看，对象选“充值订单”可只看充值。', 'Top-ups are under All logs → Operation logs; filter by "Top-up order" to see only top-ups.')}</Typography.Paragraph>
  </div>
}
