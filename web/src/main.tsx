import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { App as AntApp, ConfigProvider, Spin, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import dayjs from 'dayjs'
import 'dayjs/locale/zh-cn'
import { AppProvider, useApp } from './app'
import AppLayout from './components/AppLayout'
import Login from './pages/Login'
import Overview from './pages/Overview'
import Keys from './pages/Keys'
import Models from './pages/Models'
import Logs from './pages/Logs'
import Settings from './pages/Settings'
import Users from './pages/admin/Users'
import Groups from './pages/admin/Groups'
import ModelsAdmin from './pages/admin/ModelsAdmin'
import Channels from './pages/admin/Channels'
import System from './pages/admin/System'
import './style.css'

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
      <AntApp>
        {loading ? (
          <div className="center-screen">
            <Spin size="large" />
          </div>
        ) : !user ? (
          <Login />
        ) : (
          <Routes>
            <Route element={<AppLayout />}>
              <Route index element={<Overview />} />
              <Route path="keys" element={<Keys />} />
              <Route path="models" element={<Models />} />
              <Route path="logs" element={<Logs />} />
              <Route path="settings" element={<Settings />} />
              {user.role === 'admin' && (
                <Route path="admin">
                  <Route path="users" element={<Users />} />
                  <Route path="groups" element={<Groups />} />
                  <Route path="models" element={<ModelsAdmin />} />
                  <Route path="channels" element={<Channels />} />
                  <Route path="logs" element={<Logs admin />} />
                  <Route path="system" element={<System />} />
                </Route>
              )}
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
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
