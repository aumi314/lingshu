import { useEffect, useState } from 'react'
import { Alert, App, Button, Card, Form, Input, Spin, Typography } from 'antd'
import { Link, useNavigate } from 'react-router-dom'
import { get, post } from '../api'
import { useApp } from '../app'
import Logo from '../components/Logo'

export default function Register() {
  const { reload, t } = useApp()
  const { message } = App.useApp()
  const [enabled, setEnabled] = useState(false)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const nav = useNavigate()

  useEffect(() => {
    get<{ enabled: boolean }>('/api/auth/registration')
      .then((status) => setEnabled(status.enabled))
      .catch((e) => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  const register = async (values: { username: string; password: string; code: string }) => {
    setSubmitting(true)
    setError('')
    try {
      await post('/api/auth/register', values)
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
        {loading ? <Spin /> : enabled ? (
          <>
            <Typography.Paragraph type="secondary">{t('使用管理员提供的注册码创建团队账号。', 'Use a registration code from your administrator to join the team.')}</Typography.Paragraph>
            {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
            <Form layout="vertical" onFinish={register} requiredMark={false}>
              <Form.Item name="code" label={t('注册码', 'Registration code')} rules={[{ required: true, message: t('请输入注册码', 'Enter your registration code') }]}>
                <Input size="large" autoComplete="off" />
              </Form.Item>
              <Form.Item name="username" label={t('用户名', 'Username')} rules={[{ required: true, whitespace: true, message: t('请输入用户名', 'Enter a username') }, { max: 64, message: t('最多 64 个字符', 'Maximum 64 characters') }]}>
                <Input size="large" autoComplete="username" />
              </Form.Item>
              <Form.Item name="password" label={t('密码', 'Password')} rules={[{ required: true, min: 12, message: t('至少 12 位', 'At least 12 characters') }]}>
                <Input.Password size="large" autoComplete="new-password" />
              </Form.Item>
              <Button type="primary" htmlType="submit" size="large" block loading={submitting}>{t('注册并进入', 'Register and continue')}</Button>
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
