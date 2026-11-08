import { useCallback, useEffect, useState } from 'react'
import { Alert, App, Button, Card, Checkbox, Form, Input, Spin, Typography } from 'antd'
import { Link, useNavigate } from 'react-router-dom'
import { ApiError, get, post } from '../api'
import { useApp } from '../app'
import Logo from '../components/Logo'
import { solveCaptcha } from '../captcha'
import { fetchLegal, legalRequired, type LegalDocs } from '../legal'
import { normalizeRegistration, type RegistrationFlags } from '../registration'

export default function Register() {
  const { reload, t } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm()
  const [flags, setFlags] = useState<RegistrationFlags>({ enabled: false, code_required: false, email_required: false })
  const [sending, setSending] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const [retryAt, setRetryAt] = useState(0)
  const [seconds, setSeconds] = useState(0)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [legal, setLegal] = useState<LegalDocs | null>(null)
  const [legalError, setLegalError] = useState('')
  const [legalLoading, setLegalLoading] = useState(true)
  const nav = useNavigate()

  useEffect(() => {
    get<{ enabled?: boolean; code_required?: boolean; email_required?: boolean; mode?: string }>('/api/auth/registration')
      .then((status) => setFlags(normalizeRegistration(status)))
      .catch((e) => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  // 协议拉不到就当没法确认条款：拦住注册，不让用户在没看到条款的情况下注册。
  const loadLegal = useCallback(async () => {
    setLegalLoading(true)
    setLegalError('')
    try {
      setLegal(await fetchLegal())
    } catch (e) {
      setLegalError((e as Error).message)
    } finally {
      setLegalLoading(false)
    }
  }, [])

  useEffect(() => {
    loadLegal()
  }, [loadLegal])

  useEffect(() => {
    const update = () => setSeconds(Math.max(0, Math.ceil((retryAt - Date.now()) / 1000)))
    update()
    const timer = setInterval(update, 1000)
    return () => clearInterval(timer)
  }, [retryAt])

  const sendCode = async () => {
    try { await form.validateFields(['email']) } catch { return }
    setSending(true)
    setVerifying(true)
    setError('')
    try {
      const proof = await solveCaptcha()
      setVerifying(false)
      const result = await post<{ retry_after: number }>('/api/auth/email-code', { email: form.getFieldValue('email'), ...proof })
      setRetryAt(Date.now() + (result.retry_after ?? 60) * 1000)
      message.success(t('验证码已提交发送，请查收邮件', 'Verification email submitted. Check your inbox.'))
    } catch (e) {
      if (e instanceof ApiError && Number(e.data?.retry_after) > 0) setRetryAt(Date.now() + Number(e.data?.retry_after) * 1000)
      setError((e as Error).message)
    } finally {
      setSending(false)
      setVerifying(false)
    }
  }

  // 只提交当前要求的方式对应字段，未勾选的限制即使表单里有残值也不发送。
  const register = async (values: { username: string; password: string; code?: string; email?: string; email_code?: string; legal_accepted?: boolean }) => {
    // 协议没读到就不提交，用户重试后条款到手才能注册。
    if (legalError) {
      setError(t('请先重新加载用户协议和隐私政策', 'Please reload the terms of service and privacy policy first'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const body: Record<string, string | boolean> = {}
      if (flags.email_required) { body.email = values.email ?? ''; body.email_code = values.email_code ?? '' }
      if (flags.code_required) body.code = values.code ?? ''
      body.username = values.username
      body.password = values.password
      // 配置了协议才提交同意信息；没配置时后端不校验这一项。
      if (legalRequired(legal)) {
        body.legal_accepted = true
        body.legal_version = legal?.version ?? ''
      }
      await post('/api/auth/register', body)
      await reload()
      message.success(t('账号已创建', 'Account created'))
      nav('/console', { replace: true })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="center-screen">
      <Card style={{ width: 400, maxWidth: 'calc(100vw - 32px)' }}>
        <Typography.Title level={3} style={{ marginTop: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
          <Logo size={32} />
          {t('创建账号', 'Create account')}
        </Typography.Title>
        {loading || legalLoading ? <Spin /> : flags.enabled ? (
          <>
            <Typography.Paragraph type="secondary">{t('创建账号后使用用户名和密码登录。', 'Create an account, then sign in with your username and password.')}</Typography.Paragraph>
            {legalError && (
              <Alert
                type="error"
                showIcon
                style={{ marginBottom: 16 }}
                title={t('无法读取用户协议和隐私政策，暂时不能注册', 'Cannot load the terms of service and privacy policy, so registration is unavailable')}
                description={legalError}
                action={<Button size="small" onClick={loadLegal}>{t('重新加载', 'Retry')}</Button>}
              />
            )}
            {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
            <Form form={form} layout="vertical" onFinish={register} requiredMark={false}>
              {flags.email_required && <>
                <Form.Item name="email" label={t('邮箱', 'Email')} rules={[{ required: true, message: t('请输入邮箱', 'Enter your email') }, { type: 'email', message: t('请输入有效邮箱', 'Enter a valid email') }]}>
                  <Input size="large" type="email" autoComplete="email" />
                </Form.Item>
                <div style={{ display: 'flex', gap: 8, alignItems: 'start' }}>
                  <Form.Item name="email_code" label={t('邮件验证码', 'Email code')} style={{ flex: 1, minWidth: 0 }} rules={[{ required: true, pattern: /^\d{6}$/, message: t('请输入 6 位验证码', 'Enter the 6-digit code') }]}>
                    <Input size="large" inputMode="numeric" autoComplete="one-time-code" maxLength={6} />
                  </Form.Item>
                  <Button size="large" style={{ marginTop: 30 }} loading={sending} disabled={seconds > 0} onClick={sendCode}>
                    {seconds > 0 ? `${seconds}s` : verifying ? t('验证中…', 'Verifying…') : t('发送验证码', 'Send code')}
                  </Button>
                </div>
              </>}
              {flags.code_required && <Form.Item name="code" label={t('注册码', 'Registration code')} rules={[{ required: true, message: t('请输入注册码', 'Enter your registration code') }]}>
                <Input size="large" autoComplete="off" />
              </Form.Item>}
              <Form.Item name="username" label={t('用户名', 'Username')} rules={[{ required: true, whitespace: true, message: t('请输入用户名', 'Enter a username') }, { max: 64, message: t('最多 64 个字符', 'Maximum 64 characters') }]}>
                <Input size="large" autoComplete="username" />
              </Form.Item>
              <Form.Item name="password" label={t('密码', 'Password')} rules={[{ required: true, min: 12, message: t('至少 12 位', 'At least 12 characters') }]}>
                <Input.Password size="large" autoComplete="new-password" />
              </Form.Item>
              {legalRequired(legal) && (
                <Form.Item
                  name="legal_accepted"
                  valuePropName="checked"
                  rules={[{
                    validator: (_, value) => value === true
                      ? Promise.resolve()
                      : Promise.reject(new Error(t('请先阅读并同意用户协议和隐私政策', 'Please read and agree to the terms of service and privacy policy'))),
                  }]}
                >
                  <Checkbox>
                    {t('我已阅读并同意', 'I have read and agree to the')}{' '}
                    {legal?.user_agreement?.trim() && <Link to="/terms" target="_blank" rel="noopener noreferrer">{t('《用户协议》', 'Terms of Service')}</Link>}
                    {legal?.user_agreement?.trim() && legal?.privacy_policy?.trim() && t(' 和 ', ' and ')}
                    {legal?.privacy_policy?.trim() && <Link to="/privacy" target="_blank" rel="noopener noreferrer">{t('《隐私政策》', 'Privacy Policy')}</Link>}
                  </Checkbox>
                </Form.Item>
              )}
              <Button type="primary" htmlType="submit" size="large" block loading={submitting} disabled={Boolean(legalError)}>{t('注册并进入', 'Register and continue')}</Button>
            </Form>
          </>
        ) : (
          <Alert type="info" showIcon message={t('当前未开放自助注册', 'Self-service registration is currently closed')} description={t('请联系管理员创建账号。', 'Ask your administrator to create an account.')} />
        )}
        <div style={{ marginTop: 16 }}><Link to="/login">{t('返回登录', 'Back to sign in')}</Link></div>
      </Card>
    </div>
  )
}
