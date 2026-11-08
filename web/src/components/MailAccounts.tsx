import { useState } from 'react'
import { App, Button, Card, Form, Input, Modal, Popconfirm, Space, Switch, Table, Tag, Typography } from 'antd'
import { ArrowDownOutlined, ArrowUpOutlined } from '@ant-design/icons'
import { del, get, post, put } from '../api'
import { useApp } from '../app'

export type MailAccount = { id: number; email: string; client_id: string; enabled: boolean; sort_order: number; refresh_token_configured: boolean; checked_at: string | null; last_status: string; last_error: string }
type Values = { email: string; client_id: string; refresh_token?: string; enabled: boolean }

export default function MailAccounts({ accounts, loading, onChange }: { accounts: MailAccount[]; loading: boolean; onChange: (accounts: MailAccount[]) => void }) {
  const { t } = useApp()
  const { message } = App.useApp()
  const [form] = Form.useForm<Values>()
  const [testForm] = Form.useForm<{ recipient: string }>()
  const [editing, setEditing] = useState<MailAccount | 'new' | null>(null)
  const [testing, setTesting] = useState<MailAccount | null>(null)
  const [saving, setSaving] = useState(false)
  const [busy, setBusy] = useState<number | null>(null)
  const refresh = async () => onChange(await get<MailAccount[]>('/api/admin/mail-accounts'))
  const edit = (account: MailAccount | 'new') => {
    form.resetFields()
    form.setFieldsValue(account === 'new' ? { enabled: true, client_id: accounts[0]?.client_id ?? '' } : { email: account.email, client_id: account.client_id, enabled: account.enabled })
    setEditing(account)
  }
  const save = async () => {
    let values: Values
    try { values = await form.validateFields() } catch { return }
    setSaving(true)
    try {
      const body = { ...values, refresh_token: values.refresh_token ?? '', sort_order: editing === 'new' ? accounts.length : editing?.sort_order }
      if (editing === 'new') await post('/api/admin/mail-accounts', body)
      else if (editing) await put(`/api/admin/mail-accounts/${editing.id}`, body)
      await refresh()
      setEditing(null)
      form.resetFields()
      message.success(t('已保存，请检查授权后使用', 'Saved. Check authorization before use.'))
    } catch (e) { message.error((e as Error).message) } finally { setSaving(false) }
  }
  const run = async (account: MailAccount, action: () => Promise<unknown>, success: string) => {
    setBusy(account.id)
    try { await action(); await refresh(); message.success(success) } catch (e) {
      message.error((e as Error).message)
      await refresh().catch(() => {})
    } finally { setBusy(null) }
  }
  const move = (index: number, direction: number) => {
    const ordered = [...accounts]
    ;[ordered[index], ordered[index + direction]] = [ordered[index + direction], ordered[index]]
    return run(accounts[index], () => put('/api/admin/mail-accounts/order', { ids: ordered.map((a) => a.id) }), t('顺序已保存', 'Order saved'))
  }
  const sendTest = async () => {
    let values: { recipient: string }
    try { values = await testForm.validateFields() } catch { return }
    if (!testing) return
    setSaving(true)
    try {
      await post(`/api/admin/mail-accounts/${testing.id}/test`, values)
      await refresh()
      setTesting(null)
      message.success(t('测试邮件已提交发送，请检查收件箱', 'Test email submitted. Check the recipient inbox.'))
    } catch (e) { message.error((e as Error).message); await refresh().catch(() => {}) } finally { setSaving(false) }
  }
  const primary = accounts.find((a) => a.enabled)?.id
  return <>
    <Card title={t('发信账号', 'Mail accounts')} extra={<Button onClick={() => edit('new')}>{t('添加账号', 'Add account')}</Button>}>
      <Typography.Paragraph type="secondary">{t('使用 Outlook 发信，按顺序主备切换。授权信息保存后不再显示。', 'Send with Outlook using ordered primary and standby accounts. Saved credentials are never displayed.')}</Typography.Paragraph>
      <Table<MailAccount> rowKey="id" size="small" loading={loading} dataSource={accounts} pagination={false} scroll={{ x: 760 }} columns={[
        { title: t('账号', 'Account'), dataIndex: 'email', render: (email, a) => <Space direction="vertical" size={0}><Typography.Text>{email}</Typography.Text><Space size={4}><Tag color={a.enabled ? 'blue' : undefined}>{a.id === primary ? t('主发', 'Primary') : t('备用', 'Standby')}</Tag>{a.refresh_token_configured && <Typography.Text type="secondary">{t('授权已配置', 'Credentials configured')}</Typography.Text>}</Space></Space> },
        { title: t('启用', 'Enabled'), render: (_, a) => <Switch size="small" checked={a.enabled} loading={busy === a.id} onChange={(enabled) => run(a, () => put(`/api/admin/mail-accounts/${a.id}`, { email: a.email, client_id: a.client_id, enabled, sort_order: a.sort_order, refresh_token: '' }), t('已保存', 'Saved'))} /> },
        { title: t('最近结果', 'Last result'), render: (_, a) => <Space direction="vertical" size={0}><Typography.Text>{a.last_error || (a.last_status === 'submitted' ? t('已提交发送', 'Submitted') : a.checked_at ? t('授权已通过', 'Authorized') : t('待检查授权', 'Authorization not checked'))}</Typography.Text>{a.checked_at && <Typography.Text type="secondary">{new Date(a.checked_at).toLocaleString()}</Typography.Text>}</Space> },
        { title: t('顺序', 'Order'), width: 85, render: (_, _a, i) => <Space size={0}><Button size="small" type="text" icon={<ArrowUpOutlined />} aria-label={t('上移', 'Move up')} disabled={i === 0 || busy !== null} onClick={() => move(i, -1)} /><Button size="small" type="text" icon={<ArrowDownOutlined />} aria-label={t('下移', 'Move down')} disabled={i === accounts.length - 1 || busy !== null} onClick={() => move(i, 1)} /></Space> },
        { title: t('操作', 'Actions'), render: (_, a) => <Space size={0} wrap><Button size="small" type="link" onClick={() => edit(a)}>{t('编辑', 'Edit')}</Button><Button size="small" type="link" loading={busy === a.id} onClick={() => run(a, () => post(`/api/admin/mail-accounts/${a.id}/check`), t('授权检查通过', 'Authorization verified'))}>{t('检查授权', 'Check authorization')}</Button><Button size="small" type="link" onClick={() => { testForm.setFieldsValue({ recipient: 'support@piqode.com' }); setTesting(a) }}>{t('测试邮件', 'Test email')}</Button><Popconfirm title={t('删除这个发信账号？', 'Delete this mail account?')} onConfirm={() => run(a, () => del(`/api/admin/mail-accounts/${a.id}`), t('已删除', 'Deleted'))}><Button size="small" type="link" danger>{t('删除', 'Delete')}</Button></Popconfirm></Space> },
      ]} />
    </Card>
    <Modal open={editing !== null} title={editing === 'new' ? t('添加发信账号', 'Add mail account') : t('编辑发信账号', 'Edit mail account')} onCancel={() => { setEditing(null); form.resetFields() }} onOk={save} confirmLoading={saving} okText={t('保存', 'Save')} cancelText={t('取消', 'Cancel')}>
      <Form form={form} layout="vertical" requiredMark={false}>
        <Form.Item name="email" label={t('Outlook 邮箱', 'Outlook email')} rules={[{ required: true, type: 'email', message: t('请输入有效邮箱', 'Enter a valid email') }]}><Input autoComplete="off" /></Form.Item>
        <Form.Item name="client_id" label="Client ID" rules={[{ required: true, whitespace: true }]}><Input autoComplete="off" /></Form.Item>
        <Form.Item name="refresh_token" label="Refresh Token" extra={editing !== 'new' ? t('留空保留已保存的授权；修改后需要重新检查授权。', 'Leave blank to keep saved credentials. Check authorization after changes.') : undefined} rules={[{ required: editing === 'new', whitespace: true }]}><Input.Password autoComplete="new-password" /></Form.Item>
        <Form.Item name="enabled" label={t('启用', 'Enabled')} valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
    <Modal open={testing !== null} title={t('发送测试邮件', 'Send test email')} onCancel={() => setTesting(null)} onOk={sendTest} confirmLoading={saving} okText={t('发送邮件', 'Send email')} cancelText={t('取消', 'Cancel')}>
      <Typography.Paragraph>{t('将使用此账号实际发送一封测试邮件。', 'This account will send a real test email.')}</Typography.Paragraph>
      <Form form={testForm} layout="vertical"><Form.Item name="recipient" label={t('收件邮箱', 'Recipient email')} rules={[{ required: true, type: 'email' }]}><Input type="email" /></Form.Item></Form>
    </Modal>
  </>
}
