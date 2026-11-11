import { useEffect, useState } from 'react'
import { Alert, App, Button, Card, Checkbox, Form, Input, InputNumber, Modal, Radio, Space, Switch, Table, Tabs, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import dayjs from 'dayjs'
import { del, get, post, put } from '../../api'
import { useApp } from '../../app'
import PaymentSettings from '../../components/PaymentSettings'
import RedeemCodes from '../../components/RedeemCodes'
import MailAccounts, { type MailAccount } from '../../components/MailAccounts'
import { normalizeRegistration } from '../../registration'

type RegistrationCode = { id: number; prefix: string; created_at: string; used_by: string; used_at: string | null; revoked_at: string | null }
type GeneratedCode = { id: number; prefix: string; code: string }

type FXStatus = { source: string; date?: string; rate?: string; checked_at?: string; error?: string }
type CallLimits = { user_concurrency: number; user_rpm: number; reserve_output_tokens: number; bridge_responses_to_messages?: boolean; bridge_messages_to_responses?: boolean }
type Settings = { call_limits?: CallLimits; cny_per_usd: string; payment_enabled: boolean; redeem_enabled: boolean; registration_enabled?: boolean; registration_code_required?: boolean; registration_email_required?: boolean; registration_mode?: string; api_base_url: string; fx_mode?: string; fx_status?: FXStatus; announcement?: string; user_agreement?: string; privacy_policy?: string }

// 协议正文最长 10 万字，和后端一致
const maxLegalLength = 100000

export default function System() {
  const { t, reload, user } = useApp()
  const { message } = App.useApp()
  const [params, setParams] = useSearchParams()
  const [fxMode, setFXMode] = useState<'manual' | 'live'>('manual')
  const [fxStatus, setFXStatus] = useState<FXStatus>({ source: 'Frankfurter' })
  const [savingFXMode, setSavingFXMode] = useState(false)
  // 兑换码 2026-11-07 并入支付设置：旧链接 ?tab=redeem 落到支付设置并滚到兑换码卡片
  const rawTab = params.get('tab') ?? ''
  const tab = rawTab === 'redeem' ? 'payments' : ['calls', 'payments', 'registration'].includes(rawTab) ? rawTab : 'operations'
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
  const [agreementForm] = Form.useForm<{ user_agreement: string }>()
  const [privacyForm] = Form.useForm<{ privacy_policy: string }>()
  const [savingAgreement, setSavingAgreement] = useState(false)
  const [savingPrivacy, setSavingPrivacy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [paymentEnabled, setPaymentEnabled] = useState(false)
  const [savingPayment, setSavingPayment] = useState(false)
  const [redeemEnabled, setRedeemEnabled] = useState(false)
  const [savingRedeem, setSavingRedeem] = useState(false)
  // 协议转接的应急开关（调用设置），默认两个方向都开
  const [bridge, setBridge] = useState({ bridge_responses_to_messages: true, bridge_messages_to_responses: true })
  const [savingBridge, setSavingBridge] = useState('')
  const [regFlags, setRegFlags] = useState({ enabled: false, code_required: false, email_required: false })
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
    if (s.call_limits) {
      const { bridge_responses_to_messages = true, bridge_messages_to_responses = true, ...limits } = s.call_limits
      limitsForm.setFieldsValue(limits)
      setBridge({ bridge_responses_to_messages, bridge_messages_to_responses })
    }
    setFXMode(s.fx_mode === 'live' ? 'live' : 'manual')
    setFXStatus(s.fx_status ?? { source: 'Frankfurter' })
    apiURLForm.setFieldsValue({ api_base_url: s.api_base_url })
    noticeForm.setFieldsValue({ announcement: s.announcement ?? '' })
    agreementForm.setFieldsValue({ user_agreement: s.user_agreement ?? '' })
    privacyForm.setFieldsValue({ privacy_policy: s.privacy_policy ?? '' })
    setPaymentEnabled(s.payment_enabled)
    setRedeemEnabled(s.redeem_enabled)
    setRegFlags(normalizeRegistration({ enabled: s.registration_enabled, code_required: s.registration_code_required, email_required: s.registration_email_required, mode: s.registration_mode }))
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

  const saveAgreement = async () => {
    const v = await agreementForm.validateFields()
    setSavingAgreement(true)
    try {
      await put('/api/admin/settings', { user_agreement: v.user_agreement ?? '' })
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingAgreement(false)
    }
  }

  const savePrivacy = async () => {
    const v = await privacyForm.validateFields()
    setSavingPrivacy(true)
    try {
      await put('/api/admin/settings', { privacy_policy: v.privacy_policy ?? '' })
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingPrivacy(false)
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

  const saveBridge = async (key: keyof typeof bridge, enabled: boolean) => {
    setSavingBridge(key)
    try {
      await put('/api/admin/settings', { [key]: enabled })
      setBridge((b) => ({ ...b, [key]: enabled }))
      message.success(t('已保存，10 秒内生效', 'Saved; takes effect within 10 seconds'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingBridge('')
    }
  }

  const saveRedeem = async (enabled: boolean) => {
    setSavingRedeem(true)
    try {
      await put('/api/admin/settings', { redeem_enabled: enabled })
      setRedeemEnabled(enabled)
      message.success(t('已保存', 'Saved'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSavingRedeem(false)
    }
  }

  const saveRegistration = async () => {
    setSavingRegistration(true)
    try {
      // 关闭总开关只停掉自助入口，两个验证条件保留为偏好，下次开启时仍然生效。
      await put('/api/admin/settings', { registration_enabled: regFlags.enabled, registration_code_required: regFlags.code_required, registration_email_required: regFlags.email_required })
      message.success(t('注册方式已保存', 'Registration settings saved'))
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
            <Card title={t('用户协议', 'Terms of Service')}>
              <Typography.Paragraph type="secondary">
                {t('用户注册时必须勾选同意这里的正文。留空表示还没有这份协议，注册页不会出现勾选框。纯文本即可，换行会原样保留。', 'Users must tick to agree to this text when registering. Leave it empty when there is no document yet; the registration page then shows no checkbox. Plain text is enough and line breaks are preserved.')}
              </Typography.Paragraph>
              <Form form={agreementForm} layout="vertical" onFinish={saveAgreement}>
                <Form.Item name="user_agreement" rules={[{ max: maxLegalLength, message: t('最多 100000 字', 'At most 100000 characters') }]}>
                  <Input.TextArea rows={10} showCount maxLength={maxLegalLength} placeholder={t('在这里填写用户协议正文', 'Write the terms of service here')} disabled={loading} />
                </Form.Item>
                <Button type="primary" htmlType="submit" loading={savingAgreement} disabled={loading}>
                  {t('保存', 'Save')}
                </Button>
              </Form>
            </Card>
            <Card title={t('隐私政策', 'Privacy Policy')}>
              <Typography.Paragraph type="secondary">
                {t('说明收集哪些信息、怎么用和保存多久。用户注册时必须勾选同意这里的正文；留空表示还没有这份协议。纯文本即可，换行会原样保留。', 'Describe what is collected, how it is used and how long it is kept. Users must tick to agree to this text when registering; leave it empty when there is no document yet. Plain text is enough and line breaks are preserved.')}
              </Typography.Paragraph>
              <Form form={privacyForm} layout="vertical" onFinish={savePrivacy}>
                <Form.Item name="privacy_policy" rules={[{ max: maxLegalLength, message: t('最多 100000 字', 'At most 100000 characters') }]}>
                  <Input.TextArea rows={10} showCount maxLength={maxLegalLength} placeholder={t('在这里填写隐私政策正文', 'Write the privacy policy here')} disabled={loading} />
                </Form.Item>
                <Button type="primary" htmlType="submit" loading={savingPrivacy} disabled={loading}>
                  {t('保存', 'Save')}
                </Button>
              </Form>
            </Card>
            </Space>
          ) },
          { key: 'calls', label: t('调用设置', 'Calls'), forceRender: true, children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
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
            <Card title={t('协议转接', 'Protocol bridging')}>
              <Typography.Paragraph type="secondary">
                {t(
                  '渠道没有客户端要用的协议地址时，网关自动转换协议，用户不用改任何配置。这里是应急开关：转接出问题时先关掉，对应的模型只走原生协议，不用等修复发版。',
                  'When a channel lacks the protocol a client uses, the gateway converts between protocols automatically. These are emergency switches: turn one off if bridging misbehaves, and those models fall back to native protocols only.',
                )}
              </Typography.Paragraph>
              {([
                ['bridge_responses_to_messages', t('Codex 等 Responses 客户端调用只有 Messages 地址的模型（如 Claude）', 'Responses clients (e.g. Codex) on Messages-only models (e.g. Claude)')],
                ['bridge_messages_to_responses', t('Claude Code 等 Messages 客户端调用只有 Responses 地址的模型（如 GPT）', 'Messages clients (e.g. Claude Code) on Responses-only models (e.g. GPT)')],
              ] as const).map(([key, label]) => (
                <Space key={key} style={{ width: '100%', justifyContent: 'space-between', marginTop: 8 }}>
                  <Typography.Text>{label}</Typography.Text>
                  <Switch aria-label={label} checked={bridge[key]} loading={savingBridge === key} disabled={loading} onChange={(v) => saveBridge(key, v)} />
                </Space>
              ))}
            </Card>
            <Card title={<Space>{t('渠道亲和', 'Channel affinity')}<Tag color="green">{t('自动生效', 'Always on')}</Tag></Space>}>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                {t(
                  '同一段对话固定使用同一个渠道（按 Claude Code、Codex 带的会话编号，没有就按 API key），同一优先级的几个渠道之间不再来回切换。上游的提示词缓存按账号存，固定下来缓存才命中得了，输入按缓存价计费。固定的渠道出故障时自动换下一个，恢复后再回来。不需要配置。',
                  'Each conversation sticks to one channel (by the session ID Claude Code and Codex send, or the API key otherwise) instead of hopping between channels of the same priority. Upstream prompt caches are per account, so sticking keeps cache hits and cheaper input. If that channel fails, the next one takes over and the conversation returns once it recovers. Nothing to configure.',
                )}
              </Typography.Paragraph>
            </Card>
            <Card title={<Space>{t('违禁词拦截', 'Prompt blocking')}<Tag>{t('规划中', 'Planned')}</Tag></Space>}>
              <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
                {t(
                  '按关键词拦截请求，用来保护上游账号或满足合规要求。只检查最新一条用户消息（客户端每轮都会重发完整历史，检查历史会让一段对话命中一次就一直被拦），命中时告诉用户原因并记日志。',
                  'Block requests by keyword to protect upstream accounts or meet compliance needs. Only the latest user message will be checked (clients resend the full history every turn, so checking history would block a conversation forever after one hit); blocked users see the reason and it is logged.',
                )}
              </Typography.Paragraph>
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
            <RedeemCodes enabled={redeemEnabled} saving={savingRedeem} disabled={loading} onChange={saveRedeem} />
            </Space>
          ) },
          { key: 'registration', label: t('注册管理', 'Registration'), children: (
            <Space direction="vertical" size={16} style={{ width: '100%' }}>
              <Card title={t('注册方式', 'Registration')}>
                <Typography.Text type="secondary">{t('管理员手动添加账号始终可用，在用户管理中创建。', 'Manual accounts are always available: admins create them in User Management.')}</Typography.Text>
                <Space style={{ width: '100%', justifyContent: 'space-between', marginTop: 16 }}>
                  <Typography.Text>{t('开放注册', 'Open registration')}</Typography.Text>
                  <Switch aria-label={t('开放注册', 'Open registration')} checked={regFlags.enabled} onChange={(v) => setRegFlags((s) => ({ ...s, enabled: v }))} />
                </Space>
                {regFlags.enabled && <>
                  <Typography.Paragraph type="secondary" style={{ marginTop: 8 }}>
                    {t('两项都不勾选时，用户填写用户名和密码即可注册；勾选后作为注册时必须满足的条件，可以同时要求。', 'With neither selected, a username and password are enough. Selected items become required conditions and can apply together.')}
                  </Typography.Paragraph>
                  <Space direction="vertical" size={12}>
                    <Checkbox checked={regFlags.code_required} onChange={(e) => setRegFlags((s) => ({ ...s, code_required: e.target.checked }))}>
                      {t('注册码验证', 'Require a registration code')} <Typography.Text type="secondary">{t('用户填写管理员发放的单次注册码。', 'Users enter a one-time code issued by an admin.')}</Typography.Text>
                    </Checkbox>
                    <Checkbox checked={regFlags.email_required} disabled={!mailReady && !regFlags.email_required} onChange={(e) => setRegFlags((s) => ({ ...s, email_required: e.target.checked }))}>
                      {t('邮箱认证', 'Require email verification')} <Typography.Text type="secondary">{t('用户验证邮箱验证码后注册。', 'Users verify an email code before registering.')}</Typography.Text>
                    </Checkbox>
                  </Space>
                  {!mailReady && <Typography.Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>{t('开启邮箱认证前，请由超级管理员添加发信账号并检查授权。', 'Ask a super admin to add a mail account and check authorization before requiring email verification.')}</Typography.Paragraph>}
                </>}
                <div style={{ marginTop: 20 }}>
                  <Button type="primary" loading={savingRegistration} disabled={loading || (regFlags.enabled && regFlags.email_required && !mailReady)} onClick={saveRegistration}>{t('保存', 'Save')}</Button>
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
