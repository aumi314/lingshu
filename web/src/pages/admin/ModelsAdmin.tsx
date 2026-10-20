import { useCallback, useEffect, useState } from 'react'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Space, Switch, Table, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, get, post, put, type Model } from '../../api'
import { priceText, useApp } from '../../app'

const priceFields = ['input_price', 'output_price', 'cache_read_price', 'cache_write_price'] as const

// 放在「定价」页的标签里（pages/admin/Pricing.tsx），不单独成页
export default function ModelsAdmin() {
  const { money, t, rate } = useApp()
  const { message } = App.useApp()
  const [models, setModels] = useState<Model[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Model | 'new' | null>(null)
  const [inputCurrency, setInputCurrency] = useState<'USD' | 'CNY'>('USD')
  const [form] = Form.useForm()

  const load = useCallback(async () => {
    try {
      setModels(await get<Model[]>('/api/admin/models'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const open = (m: Model | 'new') => {
    setInputCurrency('USD')
    form.setFieldsValue(
      m === 'new'
        ? { name: '', enabled: true, input_price: 0, output_price: 0, cache_read_price: 0, cache_write_price: 0 }
        : { ...m, ...Object.fromEntries(priceFields.map((f) => [f, Number(m[f])])) },
    )
    setEditing(m)
  }

  // 切换录入货币时，把已填的数字换算过去
  const switchCurrency = (c: 'USD' | 'CNY') => {
    const factor = c === 'CNY' ? rate : 1 / rate
    const v = form.getFieldsValue(priceFields as unknown as string[])
    form.setFieldsValue(Object.fromEntries(priceFields.map((f) => [f, v[f] == null ? v[f] : Number((v[f] * factor).toFixed(6))])))
    setInputCurrency(c)
  }

  const save = async () => {
    const v = await form.validateFields()
    const toUSD = (n: number) => String(inputCurrency === 'CNY' ? Number((n / rate).toFixed(10)) : n)
    const body = { ...v, ...Object.fromEntries(priceFields.map((f) => [f, toUSD(v[f] ?? 0)])) }
    try {
      if (editing === 'new') await post('/api/admin/models', body)
      else if (editing) await put(`/api/admin/models/${editing.id}`, body)
      setEditing(null)
      message.success(t('已保存', 'Saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const remove = async (m: Model) => {
    try {
      await del(`/api/admin/models/${m.id}`)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const price = (v: string) => priceText(money, v)
  const symbol = inputCurrency === 'CNY' ? '¥' : '$'

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 16, marginBottom: 12 }}>
        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          {t(
            '这里是站内标准模型名和标价（每百万 tokens），用户看到和调用的都是这些名字。渠道里的上游模型名在「渠道」页映射到这里。价格建议填厂商国际站的美元价。',
            'Site-wide model names and list prices (per 1M tokens). Users only see and call these names; upstream names are mapped to them on the Channels page. Use the vendor\'s international USD price where possible.',
          )}
        </Typography.Paragraph>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => open('new')}>
          {t('添加模型', 'Add model')}
        </Button>
      </div>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={models}
        pagination={false}
        columns={[
          { title: t('模型名', 'Name'), dataIndex: 'name', render: (v) => <span className="mono">{v}</span> },
          { title: t('输入', 'Input'), dataIndex: 'input_price', render: price },
          { title: t('输出', 'Output'), dataIndex: 'output_price', render: price },
          { title: t('缓存命中', 'Cache hit'), dataIndex: 'cache_read_price', render: price },
          { title: t('缓存写入', 'Cache write'), dataIndex: 'cache_write_price', render: price },
          { title: t('启用', 'Enabled'), dataIndex: 'enabled', render: (v) => <Switch size="small" checked={v} disabled /> },
          {
            title: '',
            align: 'right',
            render: (_, m) => (
              <Space>
                <Button size="small" type="link" onClick={() => open(m)}>
                  {t('编辑', 'Edit')}
                </Button>
                <Popconfirm title={t('删除后各渠道里对这个模型的映射也会一起删除，确定？', 'Channel mappings to this model will be removed too. Delete?')} onConfirm={() => remove(m)}>
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal open={editing !== null} title={editing === 'new' ? t('添加模型', 'Add model') : t('编辑模型', 'Edit model')} onOk={save} onCancel={() => setEditing(null)} okText={t('保存', 'Save')} width={560} destroyOnHidden>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('站内模型名', 'Model name')} rules={[{ required: true }]} extra={t('用户调用时填的就是这个名字', 'This is the name users put in requests')}>
            <Input className="mono" />
          </Form.Item>
          <Space style={{ marginBottom: 12 }}>
            <Typography.Text type="secondary">{t('价格录入货币', 'Enter prices in')}</Typography.Text>
            <Segmented size="small" value={inputCurrency} onChange={(v) => switchCurrency(v as 'USD' | 'CNY')} options={['USD', 'CNY']} />
            {inputCurrency === 'CNY' && <Typography.Text type="secondary">{t(`保存时按 1 美元 = ${rate} 元换算成美元`, `Converted to USD at 1 USD = ${rate} CNY on save`)}</Typography.Text>}
          </Space>
          <Space wrap>
            {priceFields.map((f) => (
              <Form.Item
                key={f}
                name={f}
                label={{ input_price: t('输入', 'Input'), output_price: t('输出', 'Output'), cache_read_price: t('缓存命中', 'Cache hit'), cache_write_price: t('缓存写入', 'Cache write') }[f]}
                rules={[{ required: true }]}
              >
                <InputNumber min={0} step={0.1} prefix={symbol} style={{ width: 230 }} suffix={t('/百万', '/1M')} />
              </Form.Item>
            ))}
          </Space>
          <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
