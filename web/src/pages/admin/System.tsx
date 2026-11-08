import { useEffect, useState } from 'react'
import { Alert, App, Button, Card, Form, Input, InputNumber, Modal, Radio, Space, Switch, Table, Tabs, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import dayjs from 'dayjs'
import { del, get, post, put } from '../../api'
import { useApp } from '../../app'
import PaymentSettings from '../../components/PaymentSettings'
import RedeemCodes from '../../components/RedeemCodes'
import MailAccounts, { type MailAccount } from '../../components/MailAccounts'

type RegistrationCode = { id: number; prefix: string; created_at: string; used_by: string; used_at: string | null; revoked_at: string | null }
type GeneratedCode = { id: number; prefix: string; code: string }

type FXStatus = { source: string; date?: string; rate?: string; checked_at?: string; error?: string }
type CallLimits = { user_concurrency: number; user_rpm: number; reserve_output_tokens: number }
type Settings = { call_limits?: CallLimits; cny_per_usd: string; payment_enabled: boolean; registration_mode?: string; api_base_url: string; deployment_mode: string; fx_mode?: string; fx_status?: FXStatus; announcement?: string }

export default function System() {
  const { t, reload, user } = useApp()
  const { message } = App.useApp()
  const [params, setParams] = useSearchParams()
  const [fxMode, setFXMode] = useState<'manual' | 'live'>('manual')
  const [fxStatus, setFXStatus] = useState<FXStatus>({ source: 'Frankfurter' })
  const [savingFXMode, setSavingFXMode] = useState(false)
  // 兑换码 2026-11-07 并入支付设置：旧链接 ?tab=redeem 落到支付设置并滚到兑换码卡片
  const rawTab = params.get('tab') ?? ''
  const tab = rawTab === 'redeem' ? 'payments' : ['payments', 'registration'].includes(rawTab) ? rawTab : 'operations'
  useEffect(() => {
    if (rawTab !== 'redeem') return
    const timer = setTimeout(() => document.getElementById('redeem-codes')?.scrollIntoView({ block: 'start' }), 300)
    return () => clearTimeout(timer)
  }, [rawTab])
  const [form] = Form.useForm<{ cny_per_usd: number }>()
  const [apiURLForm] = Form.useForm<{ api_base_url: string }>()
  const [limitsForm] = Form.useForm<CallLimits>()
  const [savingLimits, setSavingLimits] = useState(false)
  const [saving, setSaving] = useState(false)
  const [savingAPIURL, setSavingAPIURL] = useState(false)
  const [noticeForm] = Form.useForm<{ announcement: string }>()
  const [savingNotice, setSavingNotice] = useState(false)
  const [deploymentMode, setDeploymentMode] = useState<'commercial_relay'>('commercial_relay')
  const [savingDeploymentMode, setSavingDeploymentMode] = useState(false)
  const [loading, setLoading] = useState(true)
  const [paymentEnabled, setPaymentEnabled] = useState(false)
  const [savingPayment, setSavingPayment] = useState(false)
  const [registrationMode, setRegistrationMode] = useState<'manual' | 'code' | 'email'>('manual')
  const [savingRegistration, setSavingRegistration] = useState(false)
  const [mailAccounts, setMailAccounts] = useState<MailAccount[]>([])
  const [loadingMailAccounts, setLoadingMailAccounts] = useState(false)
  const mailReady = mailAccounts.some((account) => account.enabled && account.checked_at)
  const [codes, setCodes] = useState<RegistrationCode[]>([])
  const [loadingCodes, setLoadingCodes] = useState(false)
  const [codeCount, setCodeCount] = useState(1)
  const [generatingCodes, setGeneratingCodes] = useState(false)
  const [generatedCodes, setGeneratedCodes] = useState<GeneratedCode[] | null>(null)

  const fill = (s: Settings) => {
    form.setFieldsValue({ cny_per_usd: Number(s.cny_per_usd) })
    if (s.call_limits) limitsForm.setFieldsValue(s.call_limits)
    setFXMode(s.fx_mode === 'live' ? 'live' : 'manual')
    setFXStatus(s.fx_status ?? { source: 'Frankfurter' })
    apiURLForm.setFieldsValue({ api_base_url: s.api_base_url })
    noticeForm.setFieldsValue({ announcement: s.announcement ?? '' })
    setDeploymentMode(s.deployment_mode === 'commercial_relay' ? s.deployment_mode : 'commercial_relay')
    setPaymentEnabled(s.payment_enabled)
    setRegistrationMode(s.registration_mode === 'email' ? 'email' : s.registration_mode === 'code' ? 'code' : 'manual')
  }

  useEffect(() => {
    get<Settings>('/api/admin/settings')
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

  useEffect(() => {
    if (tab !== 'registration' || user?.role !== 'super_admin') return
    setLoadingMailAccounts(true)
    get<MailAccount[]>('/api/admin/mail-accounts').then(setMailAccounts).catch((e) => message.error(e.message)).finally(() => setLoadingMailAccounts(false))
  }, [tab, user?.role, message])

  // 切换汇率模式立即生效；切到实时会马上拉一次，再把最新汇率和状态取回来
  const saveFXMode = async (mode: 'manual' | 'live') => {
    setSavingFXMode(true)
    try {
      await put('/api/admin/settings', { fx_mode: mode })
      fill(await get<Settings>('/api/admin/settings'))
      message.success(mode === 'live' ? t('已切到实时汇率', 'Switched to live rate') : t('已切到手动汇率', 'Switched to manual rate'))
      reload()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingFXMode(false)
    }
  }

  const saveLimits = async () => {
    const v = await limitsForm.validateFields()
    setSavingLimits(true)
    try {
      await put('/api/admin/settings', v)
      message.success(t('已保存，10 秒内生效', 'Saved; takes effect within 10 seconds'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingLimits(false)
    }
  }

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

  const saveNotice = async () => {
    const v = await noticeForm.validateFields()
    setSavingNotice(true)
    try {
      await put('/api/admin/settings', { announcement: v.announcement ?? '' })
      noticeForm.setFieldValue('announcement', (v.announcement ?? '').trim())
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingNotice(false)
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
            <Card title={t('汇率', 'Exchange rate')}>
              <Typography.Paragraph type="secondary">
                {t(
                  '账户和价格按美元记账。这个汇率同时用于：用户看到的人民币金额、你用人民币录价格和调余额时的换算、人民币充值（如 EPay）的单价。已保存的价格和余额不会变。',
                  'Balances and prices are kept in USD. This one rate drives how CNY is shown to users, how your CNY input is converted, and the price of CNY top-ups (e.g. EPay). Saved prices and balances do not change.',
                )}
              </Typography.Paragraph>
              <div style={{ marginBottom: 16 }}>
              <Radio.Group
                optionType="button"
                value={fxMode}
                disabled={loading || savingFXMode}
                onChange={(e) => saveFXMode(e.target.value)}
                options={[{ value: 'manual', label: t('手动', 'Manual') }, { value: 'live', label: t('实时（跟随市场）', 'Live (market)') }]}
              />
              </div>
              {fxMode === 'manual' ? (
                <Form form={form} layout="inline" onFinish={save}>
                  <Form.Item name="cny_per_usd" label={t('1 美元 =', '1 USD =')} rules={[{ required: true }]}>
                    <InputNumber min={0.0001} step={0.01} precision={4} suffix={t('元', 'CNY')} style={{ width: 180 }} disabled={loading} />
                  </Form.Item>
                  <Button type="primary" htmlType="submit" loading={saving} disabled={loading}>
                    {t('保存', 'Save')}
                  </Button>
                </Form>
              ) : (
                <Space direction="vertical" size={4}>
                  <Typography.Text>{t('当前 1 美元 = ', 'Now 1 USD = ')}<b>{form.getFieldValue('cny_per_usd')}</b>{t(' 元', ' CNY')}</Typography.Text>
                  <Typography.Text type="secondary">
                    {t('来源 ', 'Source ')}<a href="https://frankfurter.dev" target="_blank" rel="noreferrer">{fxStatus.source}</a>
                    {fxStatus.date && t(` · 数据日期 ${fxStatus.date}`, ` · data date ${fxStatus.date}`)}
                    {fxStatus.checked_at && t(` · 上次检查 ${dayjs(fxStatus.checked_at).format('MM-DD HH:mm')}`, ` · checked ${dayjs(fxStatus.checked_at).format('MM-DD HH:mm')}`)}
                    {t(' · 每小时检查一次，市场数据每天更新', ' · checked hourly, market data updates daily')}
                  </Typography.Text>
                  {fxStatus.error && <Alert type="warning" showIcon title={fxStatus.error} style={{ marginTop: 8 }} />}
                </Space>
              )}
            </Card>
            <Card title={t('调用限制', 'Call limits')}>
              <Typography.Paragraph type="secondary">
                {t(
                  '按用户计算，填 0 表示不限。超过并发或频率上限时返回 429 并告诉客户端多久后重试；调用前会按最大可能费用冻结额度，可用余额不够就拒绝，结算后多退少补，避免并发请求把余额扣成负数。',
                  'Per user; 0 means unlimited. Over the concurrency or rate limit, requests get 429 with a retry hint. Before each call the maximum possible cost is held; if the available balance is short the call is refused, and the hold is settled against actual usage afterwards, so parallel calls cannot overdraw the balance.',
                )}
              </Typography.Paragraph>
              <Form form={limitsForm} layout="vertical" onFinish={saveLimits} disabled={loading}>
                <Space wrap size={24} align="start">
                  <Form.Item name="user_concurrency" label={t('每个用户同时最多', 'Concurrent requests per user')} rules={[{ required: true }]}>
                    <InputNumber min={0} max={1000000} precision={0} suffix={t('个请求', 'requests')} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="user_rpm" label={t('每个用户每分钟最多', 'Requests per minute per user')} rules={[{ required: true }]}>
                    <InputNumber min={0} max={1000000} precision={0} suffix={t('个请求', 'requests')} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="reserve_output_tokens" label={t('预扣时输出最多按', 'Hold at most this much output')} rules={[{ required: true }]} extra={t('请求写得更少按请求的；回复超过它时可能小额透支，0 表示完全按请求写的上限冻结', 'Smaller request limits are used as is; replies beyond it may overdraw slightly; 0 holds the full request limit')}>
                    <InputNumber min={0} max={1000000} precision={0} suffix="tokens" style={{ width: 180 }} />
                  </Form.Item>
                </Space>
                <div><Button type="primary" htmlType="submit" loading={savingLimits}>{t('保存', 'Save')}</Button></div>
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
            <Card title={t('公告', 'Announcement')}>
              <Typography.Paragraph type="secondary">
                {t('显示在所有用户概览页的顶部，适合写客服方式、维护通知。留空就不显示。', 'Shown at the top of every user\'s Overview page, e.g. support contact or maintenance notices. Leave empty to hide it.')}
              </Typography.Paragraph>
              <Form form={noticeForm} layout="vertical" onFinish={saveNotice}>
                <Form.Item name="announcement" rules={[{ max: 500, message: t('最多 500 字', 'At most 500 characters') }]}>
                  <Input.TextArea rows={2} showCount maxLength={500} placeholder={t('例如：客服邮箱 support@example.com', 'e.g. Support: support@example.com')} disabled={loading} />
                </Form.Item>
                <Button type="primary" htmlType="submit" loading={savingNotice} disabled={loading}>
                  {t('保存', 'Save')}
                </Button>
              </Form>
            </Card>
            </Space>
          ) },
          { key: 'payments', label: t('支付设置', 'Payments'), children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <Card title={t('在线支付', 'Online payments')}>
              <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
                <Typography.Text>{t('开放在线支付', 'Enable online payments')}</Typography.Text>
                <Switch checked={paymentEnabled} loading={savingPayment} disabled={loading} onChange={savePayment} />
              </Space>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                {t('开启后，用户可使用下方已配置且启用的生产支付服务充值；关闭后已有订单仍会正常到账。不影响兑换码。', 'When enabled, users can top up through configured production payment services below. Existing payments continue to settle when disabled. Does not affect redeem codes.')}
              </Typography.Paragraph>
              <PaymentSettings />
            </Card>
            <RedeemCodes />
            </Space>
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
                    <Radio value="email" disabled={!mailReady && registrationMode !== 'email'}>
                      {t('开放邮箱注册', 'Open email registration')} <Typography.Text type="secondary">{t('用户验证邮箱后创建账号，使用用户名和密码登录。', 'Users verify email to register and sign in with a username and password.')}</Typography.Text>
                    </Radio>
                  </Space>
                </Radio.Group>
                {!mailReady && <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>{t('开启邮箱注册前，请由超级管理员添加发信账号并检查授权。', 'Ask a super admin to add a mail account and check authorization before enabling email registration.')}</Typography.Paragraph>}
                <div style={{ marginTop: 20 }}>
                  <Button type="primary" loading={savingRegistration} disabled={loading || (registrationMode === 'email' && !mailReady)} onClick={saveRegistrationMode}>{t('保存', 'Save')}</Button>
                </div>
              </Card>

              {user?.role === 'super_admin' && <MailAccounts accounts={mailAccounts} loading={loadingMailAccounts} onChange={setMailAccounts} />}

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
