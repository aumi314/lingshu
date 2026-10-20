import { useState } from 'react'
import { Alert, Button, Card, Form, Input, Typography } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { ApiError, post } from '../api'
import { solveCaptcha } from '../captcha'
import { useApp } from '../app'
import Logo from '../components/Logo'

export default function Login() {
  const { reload, t } = useApp()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const [needCaptcha, setNeedCaptcha] = useState(false) // 失败几次后服务端会要求人机验证
  const nav = useNavigate()

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
    <div className="center-screen">
      <Card style={{ width: 380, maxWidth: 'calc(100vw - 32px)' }}>
        <Typography.Title level={3} style={{ marginTop: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
          <Logo size={32} />
          灵枢 <span style={{ fontWeight: 400, fontSize: 16, opacity: 0.6 }}>Lingshu</span>
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          {t('团队统一 AI 模型网关。账号由管理员创建。', 'Team AI model gateway. Accounts are created by admins.')}
        </Typography.Paragraph>
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
        <Button type="link" style={{ padding: 0, marginTop: 12 }} onClick={() => nav('/')}>
          ← {t('返回首页', 'Back to home')}
        </Button>
      </Card>
    </div>
  )
}
