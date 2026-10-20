import { useState } from 'react'
import { Avatar, Button, Drawer, Grid, Layout, Menu, Typography, theme } from 'antd'
import {
  ApiOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  ClusterOutlined,
  DashboardOutlined,
  KeyOutlined,
  MenuOutlined,
  ProfileOutlined,
  SettingOutlined,
  TagsOutlined,
  TeamOutlined,
  ToolOutlined,
  WalletOutlined,
} from '@ant-design/icons'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useApp } from '../app'

export default function AppLayout() {
  const { user, t } = useApp()
  const nav = useNavigate()
  const { pathname } = useLocation()
  const { token } = theme.useToken()
  const mobile = !Grid.useBreakpoint().md // 窄屏（手机）把侧边菜单收进抽屉
  const [menuOpen, setMenuOpen] = useState(false)
  const go = (path: string) => {
    setMenuOpen(false)
    nav(path)
  }

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
              { key: '/console/admin/accounts', icon: <WalletOutlined />, label: t('上游账户', 'Upstream accounts') },
              { key: '/console/admin/logs', icon: <ProfileOutlined />, label: t('全站日志', 'All logs') },
              { key: '/console/admin/system', icon: <ToolOutlined />, label: t('系统设置', 'System') },
            ],
          },
        ]
      : []),
  ]

  const sidebar = (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '22px 24px 18px', cursor: 'pointer' }} onClick={() => go('/console')}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          灵枢 <span style={{ fontWeight: 400, fontSize: 15, opacity: 0.6 }}>Lingshu</span>
        </Typography.Title>
      </div>
      <Menu
        mode="inline"
        selectedKeys={[pathname]}
        items={items}
        onClick={(e) => go(e.key)}
        style={{ borderInlineEnd: 'none', flex: 1, overflowY: 'auto' }}
      />
      <div
        onClick={() => go('/console/settings')}
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
  )

  if (mobile) {
    return (
      <Layout style={{ minHeight: '100%' }}>
        <div
          style={{
            position: 'sticky',
            top: 0,
            zIndex: 10,
            display: 'flex',
            alignItems: 'center',
            gap: 4,
            height: 52,
            padding: '0 8px',
            background: token.colorBgContainer,
            borderBottom: `1px solid ${token.colorBorderSecondary}`,
          }}
        >
          <Button type="text" icon={<MenuOutlined />} aria-label={t('菜单', 'Menu')} onClick={() => setMenuOpen(true)} />
          <Typography.Text strong style={{ fontSize: 16, cursor: 'pointer' }} onClick={() => go('/console')}>
            灵枢 <span style={{ fontWeight: 400, fontSize: 14, opacity: 0.6 }}>Lingshu</span>
          </Typography.Text>
        </div>
        <Drawer
          open={menuOpen}
          onClose={() => setMenuOpen(false)}
          placement="left"
          size={260}
          closable={false}
          styles={{ body: { padding: 0 } }}
        >
          {sidebar}
        </Drawer>
        <Layout.Content>
          <Outlet />
        </Layout.Content>
      </Layout>
    )
  }

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
        {sidebar}
      </Layout.Sider>
      <Layout.Content>
        <Outlet />
      </Layout.Content>
    </Layout>
  )
}
