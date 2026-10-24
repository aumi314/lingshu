import { useEffect, useState } from 'react'
import { App, Button, Card, Form, InputNumber, Space, Switch, Typography } from 'antd'
import { get, put } from '../../api'
import { useApp } from '../../app'
import PaymentSettings from '../../components/PaymentSettings'

export default function System() {
  const { t, reload } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm<{ cny_per_usd: number }>()
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)
  const [paymentEnabled, setPaymentEnabled] = useState(false)
  const [savingPayment, setSavingPayment] = useState(false)

  const fill = (s: { cny_per_usd: string; payment_enabled: boolean }) => {
    form.setFieldsValue({ cny_per_usd: Number(s.cny_per_usd) })
    setPaymentEnabled(s.payment_enabled)
  }

  useEffect(() => {
    get<{ cny_per_usd: string; payment_enabled: boolean }>('/api/admin/settings')
      .then(fill)
      .catch((e) => message.error(e.message))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form, message])

  const save = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      await put('/api/admin/settings', { cny_per_usd: String(v.cny_per_usd) })
      message.success(t('已保存', 'Saved'))
      reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const savePayment = async (enabled: boolean) => {
    setSavingPayment(true)
    try {
      await put('/api/admin/settings', { payment_enabled: enabled })
      setPaymentEnabled(enabled)
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingPayment(false)
    }
  }

  return (
    <div className="page" style={{ maxWidth: 760 }}>
      <Typography.Title level={3} className="page-title">
        {t('系统设置', 'System')}
      </Typography.Title>
      <Card title={t('运营设置', 'Operations')} style={{ marginBottom: 20 }}>
        <Typography.Title level={5}>{t('显示汇率', 'Display exchange rate')}</Typography.Title>
        <Typography.Paragraph type="secondary">
          {t(
            '账户和价格都按美元记账。汇率只影响人民币的显示，以及管理员用人民币录入价格、调整余额时的换算；已经保存的价格和余额不会变。',
            'Balances and prices are kept in USD. The rate only affects how CNY is displayed and how CNY input is converted; saved prices and balances do not change.',
          )}
        </Typography.Paragraph>
        <Form form={form} layout="inline" onFinish={save}>
          <Form.Item name="cny_per_usd" label={t('1 美元 =', '1 USD =')} rules={[{ required: true }]}>
            <InputNumber min={0.01} step={0.01} suffix={t('元', 'CNY')} style={{ width: 180 }} disabled={loading} />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={saving} disabled={loading}>
            {t('保存', 'Save')}
          </Button>
        </Form>
      </Card>
      <Card title={t('支付设置', 'Payments')}>
        <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
          <Typography.Text>{t('开放用户充值', 'Enable user top-ups')}</Typography.Text>
          <Switch checked={paymentEnabled} loading={savingPayment} disabled={loading} onChange={savePayment} />
        </Space>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
          {t('开启后，用户可使用下方已配置且启用的生产支付服务充值；关闭后已有订单仍会正常到账。', 'When enabled, users can top up through configured production payment services below. Existing payments continue to settle when disabled.')}
        </Typography.Paragraph>
        <PaymentSettings />
      </Card>
    </div>
  )
}
