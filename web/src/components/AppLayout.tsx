import { Avatar, Layout, Menu, Typography, theme } from 'antd'
import {
  ApiOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  ClusterOutlined,
  DashboardOutlined,
  KeyOutlined,
  ProfileOutlined,
  SettingOutlined,
  TagsOutlined,
  TeamOutlined,
  ToolOutlined,
} from '@ant-design/icons'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useApp } from '../app'

export default function AppLayout() {
  const { user, t } = useApp()
  const nav = useNavigate()
  const { pathname } = useLocation()
  const { token } = theme.useToken()

  const items = [
    { key: '/console', icon: <DashboardOutlined />, label: t('概览', 'Overview') },
    { key: '/console/keys', icon: <KeyOutlined />, label: 'API keys' },
    { key: '/console/models', icon: <TagsOutlined />, label: t('模型与价格', 'Models & pricing') },
    { key: '/console/logs', icon: <BarChartOutlined />, label: t('使用记录', 'Usage') },
    ...(user?.role === 'admin'
      ? [
          {
            type: 'group' as const,
            label: t('管理', 'Admin'),
            children: [
              { key: '/console/admin/users', icon: <TeamOutlined />, label: t('用户', 'Users') },
              { key: '/console/admin/groups', icon: <ClusterOutlined />, label: t('分组', 'Groups') },
              { key: '/console/admin/models', icon: <AppstoreOutlined />, label: t('模型', 'Models') },
              { key: '/console/admin/channels', icon: <ApiOutlined />, label: t('渠道', 'Channels') },
              { key: '/console/admin/logs', icon: <ProfileOutlined />, label: t('全站日志', 'All logs') },
              { key: '/console/admin/system', icon: <ToolOutlined />, label: t('系统设置', 'System') },
            ],
          },
        ]
      : []),
  ]

  return (
    <Layout style={{ minHeight: '100%' }}>
      <Layout.Sider
        width={232}
        theme="light"
        style={{
          borderRight: `1px solid ${token.colorBorderSecondary}`,
          position: 'sticky',
          top: 0,
          height: '100vh',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
          <div style={{ padding: '22px 24px 18px', cursor: 'pointer' }} onClick={() => nav('/console')}>
            <Typography.Title level={4} style={{ margin: 0 }}>
              灵枢 <span style={{ fontWeight: 400, fontSize: 15, opacity: 0.6 }}>Lingshu</span>
            </Typography.Title>
          </div>
          <Menu
            mode="inline"
            selectedKeys={[pathname]}
            items={items}
            onClick={(e) => nav(e.key)}
            style={{ borderInlineEnd: 'none', flex: 1, overflowY: 'auto' }}
          />
          <div
            onClick={() => nav('/console/settings')}
            style={{
              margin: 12,
              padding: '10px 12px',
              borderRadius: 10,
              cursor: 'pointer',
              display: 'flex',
              alignItems: 'center',
              gap: 10,
              background: pathname === '/console/settings' ? token.colorPrimaryBg : token.colorFillQuaternary,
            }}
          >
            <Avatar size={28} style={{ background: token.colorPrimary }}>
              {user?.username.slice(0, 1).toUpperCase()}
            </Avatar>
            <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis' }}>{user?.username}</span>
            <SettingOutlined style={{ opacity: 0.6 }} />
          </div>
        </div>
      </Layout.Sider>
      <Layout.Content>
        <Outlet />
      </Layout.Content>
    </Layout>
  )
}
