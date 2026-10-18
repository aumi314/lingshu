import { Button, Card, Col, Row, Space, Typography, theme } from 'antd'
import { ApiOutlined, KeyOutlined, LineChartOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useApp } from '../app'

// 欢迎页：不需要登录，不请求任何数据
export default function Welcome() {
  const { user, t } = useApp()
  const nav = useNavigate()
  const { token } = theme.useToken()
  const base = window.location.origin

  const enter = () => nav(user ? '/console' : '/login')
  const features = [
    {
      icon: <KeyOutlined />,
      title: t('一个 key，所有模型', 'One key, every model'),
      text: t(
        'DeepSeek、GLM、Kimi、Qwen、MiMo、StepFun 等模型统一接入，不用再管各家的账号和 key。',
        'DeepSeek, GLM, Kimi, Qwen, MiMo, StepFun and more behind one key — no more juggling vendor accounts.',
      ),
    },
    {
      icon: <ApiOutlined />,
      title: t('官方协议，原样透传', 'Native protocols, passed through'),
      text: t(
        '支持 Chat Completions、Responses、Anthropic Messages。Claude Code、Codex、各类 SDK 改个地址就能用。',
        'Chat Completions, Responses and Anthropic Messages. Point Claude Code, Codex or any SDK at Lingshu and go.',
      ),
    },
    {
      icon: <LineChartOutlined />,
      title: t('按量计费，账目透明', 'Pay per use, fully transparent'),
      text: t(
        '每次请求的用量、耗时、花费都有记录，余额和限额一目了然。',
        'Every request is logged with tokens, latency and cost. Balances and limits at a glance.',
      ),
    },
  ]

  return (
    <div style={{ minHeight: '100%', display: 'flex', flexDirection: 'column' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '20px 40px' }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          灵枢 <span style={{ fontWeight: 400, fontSize: 15, opacity: 0.6 }}>Lingshu</span>
        </Typography.Title>
        <Button onClick={enter}>{user ? t('进入控制台', 'Open console') : t('登录', 'Sign in')}</Button>
      </div>

      <div style={{ flex: 1, maxWidth: 1080, width: '100%', margin: '0 auto', padding: '72px 40px 48px' }}>
        <Typography.Title style={{ fontSize: 44, marginBottom: 16 }}>{t('团队统一的 AI 模型网关', 'One AI gateway for the whole team')}</Typography.Title>
        <Typography.Paragraph type="secondary" style={{ fontSize: 18, maxWidth: 640 }}>
          {t(
            '用灵枢的地址和 key 调用各家模型，体验和直连官方一样。账号由管理员创建。',
            'Call every vendor\'s models with one Lingshu endpoint and key, just like calling them directly. Accounts are created by admins.',
          )}
        </Typography.Paragraph>
        <Space size={12} style={{ marginTop: 16 }}>
          <Button type="primary" size="large" onClick={enter}>
            {user ? t('进入控制台', 'Open console') : t('登录', 'Sign in')}
          </Button>
        </Space>

        <Row gutter={16} style={{ marginTop: 64 }}>
          {features.map((f) => (
            <Col key={f.title} xs={24} md={8} style={{ marginBottom: 16 }}>
              <Card style={{ height: '100%' }}>
                <div style={{ fontSize: 24, color: token.colorPrimary, marginBottom: 12 }}>{f.icon}</div>
                <Typography.Title level={5} style={{ marginTop: 0 }}>{f.title}</Typography.Title>
                <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>{f.text}</Typography.Paragraph>
              </Card>
            </Col>
          ))}
        </Row>

        <Card style={{ marginTop: 16 }}>
          <Typography.Title level={5} style={{ marginTop: 0 }}>{t('接入地址', 'Endpoints')}</Typography.Title>
          <Space orientation="vertical" size={6}>
            <span>OpenAI SDK：<Typography.Text copyable className="mono">{`${base}/v1`}</Typography.Text></span>
            <span>Anthropic SDK / Claude Code：<Typography.Text copyable className="mono">{base}</Typography.Text></span>
          </Space>
        </Card>
      </div>

      <Typography.Text type="secondary" style={{ textAlign: 'center', padding: 24, fontSize: 12 }}>
        灵枢 Lingshu
      </Typography.Text>
    </div>
  )
}
