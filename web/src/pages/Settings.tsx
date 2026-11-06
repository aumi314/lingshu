import { App, Button, Card, Descriptions, Form, Input, Modal, Segmented, Space, Tag, Typography } from 'antd'
import { useState } from 'react'
import { post, put } from '../api'
import { useApp } from '../app'

export default function Settings() {
  const { user, lang, currency, theme, setPref, reload, money, rate, t } = useApp()
  const { message } = App.useApp()
  const [pwdOpen, setPwdOpen] = useState(false)
  const [form] = Form.useForm<{ old_password: string; new_password: string; confirm: string }>()

  const change = async (p: Parameters<typeof setPref>[0]) => {
    try {
      await setPref(p)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const changePassword = async () => {
    const v = await form.validateFields()
    try {
      await put('/api/me/password', { old_password: v.old_password, new_password: v.new_password })
      setPwdOpen(false)
      message.success(t('密码已修改，请用新密码重新登录', 'Password changed. Please sign in again.'))
      await reload() // 所有会话已失效，回到登录页
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const logout = async () => {
    await post('/api/auth/logout').catch(() => {})
    await reload()
  }

  return (
    <div className="page" style={{ maxWidth: 760 }}>
      <Typography.Title level={3} className="page-title">
        {t('设置', 'Settings')}
      </Typography.Title>

      <Card title={t('账号', 'Account')} style={{ marginBottom: 16 }}>
        <Descriptions column={1}>
          <Descriptions.Item label={t('用户名', 'Username')}>{user?.username}</Descriptions.Item>
          <Descriptions.Item label={t('角色', 'Role')}>
            <Tag color={user?.role === 'super_admin' ? 'purple' : user?.role === 'admin' ? 'blue' : undefined}>{user?.role === 'super_admin' ? t('超级管理员', 'Super admin') : user?.role === 'admin' ? t('管理员', 'Admin') : t('成员', 'Member')}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label={t('余额', 'Balance')}>{money(user?.balance ?? 0)}</Descriptions.Item>
        </Descriptions>
      </Card>

      <Card title={t('偏好', 'Preferences')} style={{ marginBottom: 16 }}>
        <Space orientation="vertical" size={20} style={{ width: '100%' }}>
          <div>
            <Typography.Text type="secondary">{t('语言', 'Language')}</Typography.Text>
            <div style={{ marginTop: 8 }}>
              <Segmented value={lang} onChange={(v) => change({ language: v as 'zh' | 'en' })} options={[{ value: 'zh', label: '中文' }, { value: 'en', label: 'English' }]} />
            </div>
          </div>
          <div>
            <Typography.Text type="secondary">{t('货币', 'Currency')}</Typography.Text>
            <div style={{ marginTop: 8 }}>
              <Segmented
                value={currency}
                onChange={(v) => change({ currency: v as 'USD' | 'CNY' })}
                options={[{ value: 'USD', label: t('美元 $', 'USD $') }, { value: 'CNY', label: t('人民币 ¥', 'CNY ¥') }]}
              />
            </div>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t(`账户按美元记账，人民币按 1 美元 = ${rate} 元换算显示`, `Accounts are kept in USD; CNY is shown at 1 USD = ${rate} CNY`)}
            </Typography.Text>
          </div>
          <div>
            <Typography.Text type="secondary">{t('色彩主题', 'Theme')}</Typography.Text>
            <div style={{ marginTop: 8 }}>
              <Segmented
                value={theme}
                onChange={(v) => change({ theme: v as 'system' | 'light' | 'dark' })}
                options={[
                  { value: 'system', label: t('跟随系统', 'System') },
                  { value: 'light', label: t('浅色', 'Light') },
                  { value: 'dark', label: t('深色', 'Dark') },
                ]}
              />
            </div>
          </div>
        </Space>
      </Card>

      <Space>
        <Button onClick={() => { form.resetFields(); setPwdOpen(true) }}>{t('修改密码', 'Change password')}</Button>
        <Button onClick={logout}>{t('退出登录', 'Sign out')}</Button>
      </Space>

      <Modal open={pwdOpen} title={t('修改密码', 'Change password')} onOk={changePassword} onCancel={() => setPwdOpen(false)} okText={t('保存', 'Save')} destroyOnHidden>
        <Typography.Paragraph type="secondary">{t('修改后所有设备都需要重新登录。', 'All devices will be signed out after the change.')}</Typography.Paragraph>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="old_password" label={t('当前密码', 'Current password')} rules={[{ required: true }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item name="new_password" label={t('新密码', 'New password')} rules={[{ required: true, min: 12, message: t('至少 12 位', 'At least 12 characters') }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirm"
            label={t('再输一次', 'Confirm')}
            dependencies={['new_password']}
            rules={[
              { required: true },
              ({ getFieldValue }) => ({
                validator: (_, v) => (v === getFieldValue('new_password') ? Promise.resolve() : Promise.reject(new Error(t('两次输入不一致', 'Passwords do not match')))),
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
