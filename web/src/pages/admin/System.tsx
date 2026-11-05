import { useEffect, useState } from 'react'
import { App, Button, Card, Form, Input, InputNumber, Modal, Radio, Space, Switch, Table, Tabs, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { del, get, post, put } from '../../api'
import { useApp } from '../../app'
import PaymentSettings from '../../components/PaymentSettings'

type RegistrationCode = { id: number; prefix: string; created_at: string; used_by: string; used_at: string | null; revoked_at: string | null }
type GeneratedCode = { id: number; prefix: string; code: string }

export default function System() {
  const { t, reload } = useApp()
  const { message } = App.useApp()
  const [params, setParams] = useSearchParams()
  const tab = ['payments', 'registration'].includes(params.get('tab') ?? '') ? params.get('tab')! : 'operations'
  const [form] = Form.useForm<{ cny_per_usd: number }>()
  const [apiURLForm] = Form.useForm<{ api_base_url: string }>()
  const [saving, setSaving] = useState(false)
  const [savingAPIURL, setSavingAPIURL] = useState(false)
  const [deploymentMode, setDeploymentMode] = useState<'commercial_relay'>('commercial_relay')
  const [savingDeploymentMode, setSavingDeploymentMode] = useState(false)
  const [loading, setLoading] = useState(true)
  const [paymentEnabled, setPaymentEnabled] = useState(false)
  const [savingPayment, setSavingPayment] = useState(false)
  const [registrationMode, setRegistrationMode] = useState<'manual' | 'code'>('manual')
  const [savingRegistration, setSavingRegistration] = useState(false)
  const [codes, setCodes] = useState<RegistrationCode[]>([])
  const [loadingCodes, setLoadingCodes] = useState(false)
  const [codeCount, setCodeCount] = useState(1)
  const [generatingCodes, setGeneratingCodes] = useState(false)
  const [generatedCodes, setGeneratedCodes] = useState<GeneratedCode[] | null>(null)

  const fill = (s: { cny_per_usd: string; payment_enabled: boolean; registration_mode?: string; api_base_url: string; deployment_mode: string }) => {
    form.setFieldsValue({ cny_per_usd: Number(s.cny_per_usd) })
    apiURLForm.setFieldsValue({ api_base_url: s.api_base_url })
    setDeploymentMode(s.deployment_mode === 'commercial_relay' ? s.deployment_mode : 'commercial_relay')
    setPaymentEnabled(s.payment_enabled)
    setRegistrationMode(s.registration_mode === 'code' ? 'code' : 'manual')
  }

  useEffect(() => {
    get<{ cny_per_usd: string; payment_enabled: boolean; registration_mode?: string; api_base_url: string; deployment_mode: string }>('/api/admin/settings')
      .then(fill)
      .catch((e) => message.error(e.message))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form, message])

  useEffect(() => {
    if (tab !== 'registration') return
    setLoadingCodes(true)
    get<RegistrationCode[]>('/api/admin/registration-codes')
      .then(setCodes)
      .catch((e) => message.error(e.message))
      .finally(() => setLoadingCodes(false))
  }, [tab, message])

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

  const saveAPIURL = async () => {
    const v = await apiURLForm.validateFields()
    setSavingAPIURL(true)
    try {
      await put('/api/admin/settings', { api_base_url: v.api_base_url })
      apiURLForm.setFieldValue('api_base_url', v.api_base_url.trim().replace(/\/+$/, ''))
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingAPIURL(false)
    }
  }

  const saveDeploymentMode = async () => {
    setSavingDeploymentMode(true)
    try {
      await put('/api/admin/settings', { deployment_mode: deploymentMode })
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingDeploymentMode(false)
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

  const saveRegistrationMode = async () => {
    setSavingRegistration(true)
    try {
      await put('/api/admin/settings', { registration_mode: registrationMode })
      message.success(t('注册方式已保存', 'Registration mode saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingRegistration(false)
    }
  }

  const generateCodes = async () => {
    setGeneratingCodes(true)
    try {
      const created = await post<GeneratedCode[]>('/api/admin/registration-codes', { count: codeCount })
      setGeneratedCodes(created)
      setCodes(await get<RegistrationCode[]>('/api/admin/registration-codes'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setGeneratingCodes(false)
    }
  }

  const copyCodes = async () => {
    if (!generatedCodes) return
    try {
      await navigator.clipboard.writeText(generatedCodes.map((code) => code.code).join('\n'))
      message.success(t('注册码已复制', 'Registration codes copied'))
    } catch {
      message.error(t('复制失败，请手动复制', 'Copy failed. Copy the codes manually.'))
    }
  }

  const revokeCode = async (id: number) => {
    try {
      await del(`/api/admin/registration-codes/${id}`)
      setCodes(await get<RegistrationCode[]>('/api/admin/registration-codes'))
      message.success(t('注册码已撤销', 'Registration code revoked'))
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('系统设置', 'System')}
      </Typography.Title>
      <Tabs
        activeKey={tab}
        onChange={(k) => setParams(k === 'operations' ? {} : { tab: k }, { replace: true })}
        items={[
          { key: 'operations', label: t('运营设置', 'Operations'), forceRender: true, children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Card title={t('使用模式', 'Usage mode')}>
              <Typography.Paragraph type="secondary">
                {t('灵枢当前以商业中转方式运行：用户预付余额、在线充值，按模型标价和服务分组倍率扣费。公司团队网关的专用功能正在开发。', 'Lingshu currently runs as a commercial relay: users prepay, top up online and are charged at list price times the service group ratio. Company team gateway features are in development.')}
              </Typography.Paragraph>
              <Radio.Group value={deploymentMode} onChange={(event) => setDeploymentMode(event.target.value)}>
                <Space direction="vertical" size={12}>
                  <Radio value="personal" disabled>{t('自用', 'Personal use')} <Typography.Text type="secondary">{t('个人账户自行使用模型服务', 'For individual use with personal accounts')}</Typography.Text> <Tag>{t('暂未开放', 'Unavailable')}</Tag></Radio>
                  <Radio value="company_team_gateway" disabled>{t('公司团队网关', 'Company team gateway')} <Typography.Text type="secondary">{t('团队成员统一接入，由管理员管理', 'Shared access managed by a team administrator')}</Typography.Text> <Tag>{t('开发中', 'In development')}</Tag></Radio>
                  <Radio value="commercial_relay">{t('商业中转', 'Commercial relay')} <Typography.Text type="secondary">{t('为外部客户提供模型中转服务', 'Model relay service for external customers')}</Typography.Text> <Tag color="blue">{t('当前模式', 'Current')}</Tag></Radio>
                </Space>
              </Radio.Group>
              <div style={{ marginTop: 20 }}>
                <Button type="primary" loading={savingDeploymentMode} disabled={loading} onClick={saveDeploymentMode}>{t('保存', 'Save')}</Button>
              </div>
            </Card>
            <Card title={t('显示汇率', 'Display exchange rate')}>
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
            <Card title={t('API 接入地址', 'API base URL')}>
              <Typography.Paragraph type="secondary">
                {t('用户在 API keys 和概览页面看到这个地址。每个部署填写自己的公开地址，格式为 https://域名/v1。', 'This address appears on the API keys and Overview pages. Set the public URL for this deployment in the format https://domain/v1.')}
              </Typography.Paragraph>
              <Form form={apiURLForm} layout="inline" onFinish={saveAPIURL}>
                <Form.Item name="api_base_url" rules={[{ required: true, message: t('请输入 API 接入地址', 'Enter an API base URL') }]}>
                  <Input placeholder="https://gateway.example.com/v1" style={{ width: 'min(360px, 70vw)' }} disabled={loading} />
                </Form.Item>
                <Button type="primary" htmlType="submit" loading={savingAPIURL} disabled={loading}>
                  {t('保存', 'Save')}
                </Button>
              </Form>
            </Card>
            </Space>
          ) },
          { key: 'payments', label: t('支付设置', 'Payments'), children: (
            <Card>
              <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
                <Typography.Text>{t('开放用户充值', 'Enable user top-ups')}</Typography.Text>
                <Switch checked={paymentEnabled} loading={savingPayment} disabled={loading} onChange={savePayment} />
              </Space>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                {t('开启后，用户可使用下方已配置且启用的生产支付服务充值；关闭后已有订单仍会正常到账。', 'When enabled, users can top up through configured production payment services below. Existing payments continue to settle when disabled.')}
              </Typography.Paragraph>
              <PaymentSettings />
            </Card>
          ) },
          { key: 'registration', label: t('注册管理', 'Registration'), children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
              <Card title={t('开放程度', 'Registration access')}>
                <Radio.Group value={registrationMode} onChange={(e) => setRegistrationMode(e.target.value)}>
                  <Space direction="vertical" size={12}>
                    <Radio value="manual">
                      {t('仅手动添加', 'Manual accounts only')} <Typography.Text type="secondary">{t('用户由管理员在用户管理中创建。', 'Admins create accounts in User Management.')}</Typography.Text>
                    </Radio>
                    <Radio value="code">
                      {t('注册码注册', 'Registration code')} <Typography.Text type="secondary">{t('用户凭管理员发放的单次注册码创建账号。', 'Users create an account with a one-time code from an admin.')}</Typography.Text>
                    </Radio>
                    <Radio value="email" disabled>
                      {t('开放邮箱注册', 'Open email registration')} <Tag>{t('暂未开放', 'Coming later')}</Tag>
                    </Radio>
                  </Space>
                </Radio.Group>
                <div style={{ marginTop: 20 }}>
                  <Button type="primary" loading={savingRegistration} disabled={loading} onClick={saveRegistrationMode}>{t('保存', 'Save')}</Button>
                </div>
              </Card>

              <Card title={t('注册码', 'Registration codes')}>
                <Typography.Paragraph type="secondary">
                  {t('每个注册码只能使用一次。明文只在生成时显示，请及时复制并发给成员；之后只能查看状态，不能再次查看注册码。', 'Each code can be used once. The full code is shown only when created; copy and share it then. Only its status is visible later.')}
                </Typography.Paragraph>
                <Space style={{ marginBottom: 16 }}>
                  <InputNumber min={1} max={100} value={codeCount} onChange={(v) => setCodeCount(v ?? 1)} addonAfter={t('个', 'codes')} />
                  <Button type="primary" loading={generatingCodes} onClick={generateCodes}>{t('生成注册码', 'Generate codes')}</Button>
                </Space>
                <Table
                  rowKey="id"
                  size="small"
                  loading={loadingCodes}
                  dataSource={codes}
                  pagination={{ pageSize: 10, hideOnSinglePage: true }}
                  columns={[
                    { title: t('前缀', 'Prefix'), dataIndex: 'prefix', render: (v) => <Typography.Text code>{v}…</Typography.Text> },
                    { title: t('创建时间', 'Created'), dataIndex: 'created_at', render: (v) => new Date(v).toLocaleString() },
                    { title: t('状态', 'Status'), render: (_, c) => c.revoked_at
                      ? <Tag>{t('已撤销', 'Revoked')}</Tag>
                      : c.used_at ? <Tag color="green">{t('已使用', 'Used')}</Tag> : <Tag color="blue">{t('可使用', 'Available')}</Tag> },
                    { title: t('使用者', 'Used by'), dataIndex: 'used_by', render: (v) => v || '—' },
                    { title: '', align: 'right', render: (_, c) => !c.used_at && !c.revoked_at
                      ? <Button size="small" type="link" danger onClick={() => revokeCode(c.id)}>{t('撤销', 'Revoke')}</Button>
                      : null },
                  ]}
                />
              </Card>
            </Space>
          ) },
        ]}
      />
      <Modal
        open={generatedCodes !== null}
        title={t('注册码已生成', 'Registration codes created')}
        okText={t('我已复制', 'Copied')}
        cancelButtonProps={{ style: { display: 'none' } }}
        onOk={() => setGeneratedCodes(null)}
        onCancel={() => setGeneratedCodes(null)}
      >
        <Typography.Paragraph type="warning">{t('请现在复制并保存。关闭后无法再次查看明文。', 'Copy and save these now. The full codes cannot be viewed again after closing.')}</Typography.Paragraph>
        <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', padding: 12, borderRadius: 8, background: 'rgba(128,128,128,.12)' }}>
          {generatedCodes?.map((code) => code.code).join('\n')}
        </pre>
        <Button onClick={copyCodes}>{t('复制全部', 'Copy all')}</Button>
      </Modal>
    </div>
  )
}
