import { useCallback, useEffect, useState } from 'react'
import { App, Button, Card, Divider, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Table, Tag, Tooltip, Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined, DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { del, get, post, put, type Model, type PriceRule } from '../../api'
import { priceText, useApp } from '../../app'

const priceFields = ['input_price', 'output_price', 'cache_read_price', 'cache_write_price'] as const
const clock = /^([01]\d|2[0-3]):[0-5]\d$/
const ymd = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])$/

type RuleForm = { name?: string; input_above?: number | null; days?: string; date_from?: string; date_to?: string; time_start?: string; time_end?: string } & Partial<Record<(typeof priceFields)[number], number>>


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
        ? { name: '', input_price: 0, output_price: 0, cache_read_price: 0, cache_write_price: 0, price_rules: [] }
        : {
            ...m,
            ...Object.fromEntries(priceFields.map((f) => [f, Number(m[f])])),
            price_rules: (m.price_rules ?? []).map((r) => ({ ...r, days: r.days ?? '', ...Object.fromEntries(priceFields.map((f) => [f, Number(r[f])])) })),
          },
    )
    setEditing(m)
  }

  // 切换录入货币时，把已填的数字换算过去
  const switchCurrency = (c: 'USD' | 'CNY') => {
    const factor = c === 'CNY' ? rate : 1 / rate
    const v = form.getFieldsValue(priceFields as unknown as string[])
    const conv = (n: number | undefined) => (n == null ? n : Number((n * factor).toFixed(6)))
    form.setFieldsValue(Object.fromEntries(priceFields.map((f) => [f, conv(v[f])])))
    const rules: RuleForm[] = form.getFieldValue('price_rules') ?? []
    form.setFieldValue('price_rules', rules.map((r) => ({ ...r, ...Object.fromEntries(priceFields.map((f) => [f, conv(r[f])])) })))
    setInputCurrency(c)
  }

  const save = async () => {
    const v = await form.validateFields()
    const toUSD = (n: number) => String(inputCurrency === 'CNY' ? Number((n / rate).toFixed(10)) : n)
    const body = {
      ...v,
      ...Object.fromEntries(priceFields.map((f) => [f, toUSD(v[f] ?? 0)])),
      price_rules: ((v.price_rules ?? []) as RuleForm[]).map((r) => ({
        name: r.name ?? '',
        input_above: r.input_above ?? null,
        days: r.days || undefined,
        date_from: r.date_from || undefined,
        date_to: r.date_to || undefined,
        time_start: r.time_start || undefined,
        time_end: r.time_end || undefined,
        ...Object.fromEntries(priceFields.map((f) => [f, toUSD(r[f] ?? 0)])),
      })),
    }
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
          {
            title: t('价格规则', 'Price rules'),
            dataIndex: 'price_rules',
            render: (rules: PriceRule[]) =>
              rules?.length ? (
                <Tooltip
                  title={rules.map((r, i) => (
                    <div key={i}>
                      {i + 1}. {r.name}：{price(r.input_price)} / {price(r.output_price)} · {t('缓存', 'cache')} {price(r.cache_read_price)} / {price(r.cache_write_price)}
                    </div>
                  ))}
                >
                  <Tag color="purple">{t(`${rules.length} 条规则`, `${rules.length} rule${rules.length > 1 ? 's' : ''}`)}</Tag>
                </Tooltip>
              ) : (
                <span className="muted">—</span>
              ),
          },
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
      <Modal open={editing !== null} title={editing === 'new' ? t('添加模型', 'Add model') : t('编辑模型', 'Edit model')} onOk={save} onCancel={() => setEditing(null)} okText={t('保存', 'Save')} width={780} destroyOnHidden>
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

          <Divider titlePlacement="start" style={{ marginTop: 0 }}>
            {t('价格规则（阶梯 / 分时段）', 'Price rules (tiers / time of day)')}
          </Divider>
          <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
            {t(
              '上面是基础价。规则从上往下匹配，第一条条件全部满足的生效，都不满足用基础价。输入长度 = 输入 + 缓存命中 + 缓存写入；日期和时间都按北京时间、以请求开始时为准，时间段可以跨零点；生效日期用来提前配好涨价、降价或限时价；周末按北京时间的周六、周日判断。规则价同样乘服务分组倍率。',
              'The prices above are the base. Rules are checked top-down; the first whose conditions all match applies, otherwise the base price. Input length = input + cache hit + cache write; time is Beijing time at request start and may wrap past midnight. Service-group ratios apply to rule prices too.',
            )}
          </Typography.Paragraph>
          <Form.List name="price_rules">
            {(fields, { add, remove, move }) => (
              <>
                {fields.map((field, i) => (
                  <Card
                    key={field.key}
                    size="small"
                    style={{ marginBottom: 12 }}
                    title={t(`规则 ${i + 1}`, `Rule ${i + 1}`)}
                    extra={
                      <Space size={0}>
                        <Button size="small" type="text" icon={<ArrowUpOutlined />} disabled={i === 0} onClick={() => move(i, i - 1)} aria-label={t('上移', 'Move up')} />
                        <Button size="small" type="text" icon={<ArrowDownOutlined />} disabled={i === fields.length - 1} onClick={() => move(i, i + 1)} aria-label={t('下移', 'Move down')} />
                        <Button size="small" type="text" danger icon={<DeleteOutlined />} onClick={() => remove(field.name)} aria-label={t('删除', 'Delete')} />
                      </Space>
                    }
                  >
                    <Space wrap align="start">
                      <Form.Item name={[field.name, 'name']} label={t('名称', 'Name')} extra={t('可不填，自动按条件命名', 'Optional; named from the condition')}>
                        <Input style={{ width: 170 }} placeholder={t('例如：长上下文', 'e.g. Long context')} />
                      </Form.Item>
                      <Form.Item name={[field.name, 'input_above']} label={t('输入超过（tokens）', 'Input above (tokens)')} extra={t('例如 272000；空表示不限', 'e.g. 272000; empty for any')}>
                        <InputNumber min={0} step={1000} style={{ width: 140 }} />
                      </Form.Item>
                      <Form.Item
                        name={[field.name, 'date_from']}
                        label={t('生效日期', 'From date')}
                        rules={[{ pattern: ymd, message: 'YYYY-MM-DD' }]}
                        extra={t('含当天；空表示不限', 'Inclusive; empty for any')}
                      >
                        <Input style={{ width: 120 }} placeholder="2026-12-01" />
                      </Form.Item>
                      <Form.Item name={[field.name, 'date_to']} label={t('截止日期', 'To date')} rules={[{ pattern: ymd, message: 'YYYY-MM-DD' }]}>
                        <Input style={{ width: 120 }} placeholder={t('不限', 'Any')} />
                      </Form.Item>
                      <Form.Item name={[field.name, 'days']} label={t('日期条件', 'Days')}>
                        <Select
                          style={{ width: 130 }}
                          options={[
                            { value: '', label: t('不限', 'Any day') },
                            { value: 'weekend', label: t('仅周末', 'Weekends only') },
                          ]}
                        />
                      </Form.Item>
                      <Form.Item
                        name={[field.name, 'time_start']}
                        label={t('开始（北京时间）', 'From (Beijing)')}
                        rules={[{ pattern: clock, message: 'HH:MM' }]}
                        extra={t('空表示全天', 'Empty for all day')}
                      >
                        <Input style={{ width: 90 }} placeholder="00:30" />
                      </Form.Item>
                      <Form.Item
                        name={[field.name, 'time_end']}
                        label={t('结束', 'To')}
                        dependencies={[['price_rules', field.name, 'time_start']]}
                        rules={[
                          { pattern: clock, message: 'HH:MM' },
                          ({ getFieldValue }) => ({
                            validator: (_, v) =>
                              !!getFieldValue(['price_rules', field.name, 'time_start']) === !!v
                                ? Promise.resolve()
                                : Promise.reject(new Error(t('开始和结束要一起填', 'Fill both'))),
                          }),
                        ]}
                      >
                        <Input style={{ width: 90 }} placeholder="08:30" />
                      </Form.Item>
                    </Space>
                    <Space wrap>
                      {priceFields.map((f) => (
                        <Form.Item
                          key={f}
                          name={[field.name, f]}
                          label={{ input_price: t('输入', 'Input'), output_price: t('输出', 'Output'), cache_read_price: t('缓存命中', 'Cache hit'), cache_write_price: t('缓存写入', 'Cache write') }[f]}
                          rules={[{ required: true }]}
                          style={{ marginBottom: 0 }}
                        >
                          <InputNumber min={0} step={0.1} prefix={symbol} style={{ width: 170 }} />
                        </Form.Item>
                      ))}
                    </Space>
                  </Card>
                ))}
                <Button
                  type="dashed"
                  block
                  icon={<PlusOutlined />}
                  onClick={() => add({ days: '', ...Object.fromEntries(priceFields.map((f) => [f, form.getFieldValue(f) ?? 0])) })}
                >
                  {t('添加规则（价格先按基础价填好，再改）', 'Add rule (prefilled with the base prices)')}
                </Button>
              </>
            )}
          </Form.List>
        </Form>
      </Modal>
    </div>
  )
}
