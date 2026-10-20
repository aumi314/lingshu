import { StrictMode, useEffect } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import { App as AntApp, ConfigProvider, Spin, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import dayjs from 'dayjs'
import 'dayjs/locale/zh-cn'
import { AppProvider, useApp } from './app'
import AppLayout from './components/AppLayout'
import Login from './pages/Login'
import Welcome from './pages/Welcome'
import Overview from './pages/Overview'
import Keys from './pages/Keys'
import Models from './pages/Models'
import Logs from './pages/Logs'
import Settings from './pages/Settings'
import Users from './pages/admin/Users'
import Pricing from './pages/admin/Pricing'
import Channels from './pages/admin/Channels'
import System from './pages/admin/System'
import './style.css'

// 页面背景跟随主题（Ant Design 只管组件，不管 body）
function ThemedBody({ dark }: { dark: boolean }) {
  const { token } = theme.useToken()
  useEffect(() => {
    document.body.style.background = token.colorBgLayout
    document.body.style.colorScheme = dark ? 'dark' : 'light'
  }, [token.colorBgLayout, dark])
  return null
}

// 控制台要求登录；没登录就去登录页，登录后回到原来要去的页面
function RequireUser() {
  const { user } = useApp()
  const location = useLocation()
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return <Outlet />
}

// 已登录（包括刚登录成功）时，回到原来要去的页面
function LoginRoute() {
  const { user } = useApp()
  const from = (useLocation().state as { from?: string } | null)?.from ?? '/console'
  return user ? <Navigate to={from} replace /> : <Login />
}

function Root() {
  const { user, loading, dark, lang } = useApp()
  dayjs.locale(lang === 'zh' ? 'zh-cn' : 'en')
  return (
    <ConfigProvider
      locale={lang === 'zh' ? zhCN : enUS}
      theme={{
        algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm,
        token: { colorPrimary: '#4d6bfe', borderRadius: 8 },
      }}
    >
      <AntApp style={{ height: '100%' }}>
        <ThemedBody dark={dark} />
        {loading ? (
          <div className="center-screen">
            <Spin size="large" />
          </div>
        ) : (
          <Routes>
            <Route index element={<Welcome />} />
            <Route path="login" element={<LoginRoute />} />
            <Route path="console" element={<RequireUser />}>
              <Route element={<AppLayout />}>
                <Route index element={<Overview />} />
                <Route path="keys" element={<Keys />} />
                <Route path="models" element={<Models />} />
                <Route path="logs" element={<Logs />} />
                <Route path="settings" element={<Settings />} />
                {user?.role === 'admin' && (
                  <Route path="admin">
                    <Route path="users" element={<Users />} />
                    <Route path="pricing" element={<Pricing />} />
                    <Route path="groups" element={<Navigate to="/console/admin/pricing?tab=groups" replace />} />
                    <Route path="models" element={<Navigate to="/console/admin/pricing" replace />} />
                    <Route path="channels" element={<Channels />} />
                    <Route path="logs" element={<Logs admin />} />
                    <Route path="system" element={<System />} />
                  </Route>
                )}
                <Route path="*" element={<Navigate to="/console" replace />} />
              </Route>
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        )}
      </AntApp>
    </ConfigProvider>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AppProvider>
        <Root />
      </AppProvider>
    </BrowserRouter>
  </StrictMode>,
)
