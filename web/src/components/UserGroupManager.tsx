import { useState } from 'react'
import { App, Button, Form, Input, Modal, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, post, put, type UserGroup } from '../api'
import { useApp } from '../app'

// 用户分组只是“限定范围的服务分组”的名单，所以放在服务分组旁边管理，不在用户页单独占位。
export default function UserGroupManager({ open, userGroups, onClose, onChanged }: { open: boolean; userGroups: UserGroup[]; onClose: () => void; onChanged: () => void }) {
  const { t } = useApp()
  const { message } = App.useApp()
  const [editing, setEditing] = useState<UserGroup | 'new' | null>(null)
  const [form] = Form.useForm<{ name: string }>()

  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    try {
      await fn()
      if (ok) message.success(ok)
      onChanged()
      return true
    } catch (e) {
      message.error((e as Error).message)
      return false
    }
  }

  return (
    <>
      <Modal open={open} title={t('用户分组', 'User groups')} footer={null} onCancel={onClose} destroyOnHidden>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Typography.Text type="secondary">
            {t('用户分组是服务分组的使用名单：服务分组设为“指定用户分组”时，只有名单里的用户能用。成员在「用户」页的编辑里调整；新用户自动加入 default。', 'User groups are access lists for service groups: a service group limited to selected user groups is only usable by their members. Change membership when editing a user; new users join default automatically.')}
          </Typography.Text>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => { form.resetFields(); setEditing('new') }}>
            {t('新建用户分组', 'Create user group')}
          </Button>
          <Table
            rowKey="id"
            size="small"
            dataSource={userGroups}
            pagination={false}
            columns={[
              { title: t('名称', 'Name'), dataIndex: 'name' },
              { title: t('用途', 'Type'), render: (_, g) => g.is_default ? <Tag color="blue">{t('默认', 'Default')}</Tag> : null },
              {
                title: '', align: 'right', render: (_, g) => (
                  <Space>
                    <Button size="small" type="link" onClick={() => { form.setFieldsValue({ name: g.name }); setEditing(g) }}>{t('编辑', 'Edit')}</Button>
                    {!g.is_default && (
                      <Popconfirm title={t(`删除「${g.name}」？`, `Delete “${g.name}”?`)} description={t('成员会失去通过此分组获得的服务分组权限。', 'Members will lose service-group access granted through this group.')} onConfirm={() => run(() => del(`/api/admin/user-groups/${g.id}`))}>
                        <Button size="small" type="link" danger>{t('删除', 'Delete')}</Button>
                      </Popconfirm>
                    )}
                  </Space>
                ),
              },
            ]}
          />
        </Space>
      </Modal>
      <Modal
        open={editing !== null}
        title={editing === 'new' ? t('新建用户分组', 'Create user group') : t('编辑用户分组', 'Edit user group')}
        okText={t('保存', 'Save')}
        onCancel={() => setEditing(null)}
        onOk={async () => {
          const v = await form.validateFields()
          const done = await run(() => editing === 'new'
            ? post('/api/admin/user-groups', v)
            : put(`/api/admin/user-groups/${(editing as UserGroup).id}`, v), t('用户分组已保存', 'User group saved'))
          if (done) setEditing(null)
        }}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]}>
            <Input autoFocus />
          </Form.Item>
        </Form>
      </Modal>
    </>
  )
}
