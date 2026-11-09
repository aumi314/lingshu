import { useEffect, useState } from 'react'
import { Alert, Button, Form, Input } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { Link } from 'react-router-dom'
import { ApiError, get, post } from '../api'
import { solveCaptcha } from '../captcha'
import { useApp } from '../app'
import AuthShell from '../components/AuthShell'
import { normalizeRegistration } from '../registration'

export default function Login() {
  const { reload, t } = useApp()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const [needCaptcha, setNeedCaptcha] = useState(false) // 失败几次后服务端会要求人机验证
  const [registrationEnabled, setRegistrationEnabled] = useState(false)

  useEffect(() => {
    get<{ enabled?: boolean; code_required?: boolean; email_required?: boolean; mode?: string }>('/api/auth/registration')
      .then((status) => setRegistrationEnabled(normalizeRegistration(status).enabled))
      .catch(() => {})
  }, [])

  const withCaptcha = async (v: { username: string; password: string }) => {
    setVerifying(true)
    try {
      return { ...v, ...(await solveCaptcha()) }
    } finally {
      setVerifying(false)
    }
  }

  const onFinish = async (v: { username: string; password: string }) => {
    setSubmitting(true)
    setError('')
    try {
      try {
        await post('/api/auth/login', needCaptcha ? await withCaptcha(v) : v)
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 428)) throw e
        setNeedCaptcha(true)
        await post('/api/auth/login', await withCaptcha(v)) // 服务端要求验证时自动算一次再提交
      }
      await reload() // 登录状态更新后，/login 路由会跳回原来要去的页面
    } catch (e) {
      if (e instanceof ApiError && e.data?.captcha) setNeedCaptcha(true)
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthShell
      variant="login"
      subtitle={t('登录灵枢控制台，管理 key、用量和余额。', 'Sign in to manage your keys, usage and balance.')}
      footer={
        registrationEnabled ? (
          <>
            {t('还没有账号？', 'No account yet?')}
            <Link to="/register">{t('创建账号', 'Create an account')}</Link>
          </>
        ) : (
          t('账号由管理员创建，需要请联系管理员。', 'Accounts are created by admins. Ask yours for one.')
        )
      }
    >
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      <Form layout="vertical" onFinish={onFinish} requiredMark={false}>
        <Form.Item name="username" rules={[{ required: true, message: t('请输入用户名', 'Enter your username') }]}>
          <Input size="large" prefix={<UserOutlined />} placeholder={t('用户名', 'Username')} autoFocus autoComplete="username" />
        </Form.Item>
        <Form.Item name="password" rules={[{ required: true, message: t('请输入密码', 'Enter your password') }]}>
          <Input.Password size="large" prefix={<LockOutlined />} placeholder={t('密码', 'Password')} autoComplete="current-password" />
        </Form.Item>
        <Button type="primary" htmlType="submit" size="large" block loading={submitting}>
          {verifying ? t('正在进行人机验证…', 'Verifying you are human…') : t('登录', 'Sign in')}
        </Button>
      </Form>
    </AuthShell>
  )
}
