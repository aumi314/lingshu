import { useEffect, useState } from 'react'
import { App, Button, Card, Form, InputNumber, Typography } from 'antd'
import { get, put } from '../../api'
import { useApp } from '../../app'

export default function System() {
  const { t, reload } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm<{ cny_per_usd: number }>()
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    get<{ cny_per_usd: string }>('/api/admin/settings')
      .then((s) => form.setFieldsValue({ cny_per_usd: Number(s.cny_per_usd) }))
      .catch((e) => message.error(e.message))
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

  return (
    <div className="page" style={{ maxWidth: 760 }}>
      <Typography.Title level={3} className="page-title">
        {t('系统设置', 'System')}
      </Typography.Title>
      <Card title={t('汇率', 'Exchange rate')}>
        <Typography.Paragraph type="secondary">
          {t(
            '账户和价格都按美元记账。汇率只影响人民币的显示，以及管理员用人民币录入价格、调整余额时的换算；已经保存的价格和余额不会变。',
            'Balances and prices are kept in USD. The rate only affects how CNY is displayed and how CNY input is converted; saved prices and balances do not change.',
          )}
        </Typography.Paragraph>
        <Form form={form} layout="inline" onFinish={save}>
          <Form.Item name="cny_per_usd" label={t('1 美元 =', '1 USD =')} rules={[{ required: true }]}>
            <InputNumber min={0.01} step={0.01} suffix={t('元', 'CNY')} style={{ width: 180 }} />
          </Form.Item>
          <Button type="primary" htmlType="submit" loading={saving}>
            {t('保存', 'Save')}
          </Button>
        </Form>
      </Card>
    </div>
  )
}
