import { useState } from 'react'
import { Alert, Button, Card, Form, Input, Typography } from 'antd'
import { LockOutlined, UserOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { post } from '../api'
import { useApp } from '../app'

export default function Login() {
  const { reload, t } = useApp()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const nav = useNavigate()

  const onFinish = async (v: { username: string; password: string }) => {
    setSubmitting(true)
    setError('')
    try {
      await post('/api/auth/login', v)
      await reload() // 登录状态更新后，/login 路由会跳回原来要去的页面
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="center-screen">
      <Card style={{ width: 380 }}>
        <Typography.Title level={3} style={{ marginTop: 0 }}>
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
            {t('登录', 'Sign in')}
          </Button>
        </Form>
        <Button type="link" style={{ padding: 0, marginTop: 12 }} onClick={() => nav('/')}>
          ← {t('返回首页', 'Back to home')}
        </Button>
      </Card>
    </div>
  )
}
