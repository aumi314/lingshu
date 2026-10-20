import { useEffect, useState } from 'react'
import { App, Button, Card, Form, Input, InputNumber, Tag, Typography } from 'antd'
import { get, put } from '../../api'
import { useApp } from '../../app'

export default function System() {
  const { t, reload } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm<{ cny_per_usd: number }>()
  const [saving, setSaving] = useState(false)
  const [holidays, setHolidays] = useState<string[]>([])
  const [holidayText, setHolidayText] = useState('')
  const [rate, setRate] = useState('')

  const fill = (s: { cny_per_usd: string; cn_holidays: string[] }) => {
    form.setFieldsValue({ cny_per_usd: Number(s.cny_per_usd) })
    setRate(s.cny_per_usd)
    setHolidays(s.cn_holidays)
    setHolidayText(s.cn_holidays.join('\n'))
  }

  useEffect(() => {
    get<{ cny_per_usd: string; cn_holidays: string[] }>('/api/admin/settings')
      .then(fill)
      .catch((e) => message.error(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form, message])

  const saveHolidays = async () => {
    setSaving(true)
    try {
      // 支持换行、逗号、空格分隔
      const dates = holidayText.split(/[\s,，、]+/).filter(Boolean)
      await put('/api/admin/settings', { cny_per_usd: rate, cn_holidays: dates })
      fill(await get('/api/admin/settings'))
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const save = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      await put('/api/admin/settings', { cny_per_usd: String(v.cny_per_usd) })
      setRate(String(v.cny_per_usd))
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

      <Card title={t('中国法定节假日', 'Chinese public holidays')} style={{ marginTop: 16 }}>
        <Typography.Paragraph type="secondary">
          {t(
            '价格规则里的"工作日 / 非工作日"按这里判断：工作日 = 周一到周五且不在下面的列表里；周末一律算非工作日（调休上班的周末也算，和 DeepSeek 一致），所以只需要填放假的日期，调休上班日不用填。每年国务院公布下一年安排后更新。',
            'Workday / non-workday price rules use this list: workdays are Mon–Fri not listed here; weekends are always non-workdays (including make-up working weekends, as DeepSeek does), so list only the days off. Update yearly when the State Council publishes the schedule.',
          )}
        </Typography.Paragraph>
        <Input.TextArea
          rows={6}
          value={holidayText}
          onChange={(e) => setHolidayText(e.target.value)}
          placeholder={'2027-01-01\n2027-02-06\n...'}
          className="mono"
        />
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 12 }}>
          <Button type="primary" loading={saving} onClick={saveHolidays}>
            {t('保存节假日', 'Save holidays')}
          </Button>
          <Typography.Text type="secondary">
            {holidays.length ? t(`已设置 ${holidays.length} 天，最晚 ${holidays[holidays.length - 1]}`, `${holidays.length} days set, latest ${holidays[holidays.length - 1]}`) : <Tag color="orange">{t('还没有设置', 'None set')}</Tag>}
          </Typography.Text>
        </div>
      </Card>
    </div>
  )
}
