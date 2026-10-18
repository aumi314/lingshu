import { useCallback, useEffect, useState } from 'react'
import { App, Button, Form, Input, InputNumber, Modal, Popconfirm, Space, Switch, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, get, post, put, type Group } from '../../api'
import { useApp } from '../../app'

export default function Groups() {
  const { t } = useApp()
  const { message } = App.useApp()
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<Group | 'new' | null>(null)
  const [form] = Form.useForm()

  const load = useCallback(async () => {
    try {
      setGroups(await get<Group[]>('/api/admin/groups'))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [message])

  useEffect(() => {
    load()
  }, [load])

  const save = async () => {
    const v = await form.validateFields()
    const body = { ...v, ratio: String(v.ratio) }
    try {
      if (editing === 'new') await post('/api/admin/groups', body)
      else if (editing) await put(`/api/admin/groups/${editing.id}`, body)
      setEditing(null)
      message.success(t('已保存', 'Saved'))
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const remove = async (g: Group) => {
    try {
      await del(`/api/admin/groups/${g.id}`)
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <div className="page">
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 16 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>
          {t('分组', 'Groups')}
        </Typography.Title>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => {
            form.setFieldsValue({ name: '', ratio: 1, is_public: true, description: '' })
            setEditing('new')
          }}
        >
          {t('创建分组', 'Create group')}
        </Button>
      </Space>
      <Typography.Paragraph type="secondary">
        {t(
          '用户按"模型标价 × 分组倍率"付费。公开分组所有人都能用；特殊分组要在「用户」页单独授权。渠道在「渠道」页挂到分组上。',
          'Users pay model price × group ratio. Public groups are open to everyone; special groups must be granted per user on the Users page. Channels are attached to groups on the Channels page.',
        )}
      </Typography.Paragraph>
      <Table
        rowKey="id"
        loading={loading}
        dataSource={groups}
        pagination={false}
        columns={[
          { title: t('名称', 'Name'), dataIndex: 'name' },
          { title: t('倍率', 'Ratio'), dataIndex: 'ratio', render: (v) => `×${Number(v)}` },
          { title: t('类型', 'Type'), dataIndex: 'is_public', render: (v) => (v ? <Tag color="green">{t('公开', 'Public')}</Tag> : <Tag color="gold">{t('特殊', 'Special')}</Tag>) },
          { title: t('说明', 'Description'), dataIndex: 'description' },
          {
            title: '',
            align: 'right',
            render: (_, g) => (
              <Space>
                <Button size="small" type="link" onClick={() => { form.setFieldsValue({ ...g, ratio: Number(g.ratio) }); setEditing(g) }}>
                  {t('编辑', 'Edit')}
                </Button>
                <Popconfirm title={t('确定删除这个分组？', 'Delete this group?')} onConfirm={() => remove(g)}>
                  <Button size="small" type="link" danger>
                    {t('删除', 'Delete')}
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal open={editing !== null} title={editing === 'new' ? t('创建分组', 'Create group') : t('编辑分组', 'Edit group')} onOk={save} onCancel={() => setEditing(null)} okText={t('保存', 'Save')} destroyOnHidden>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('名称', 'Name')} rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="ratio" label={t('倍率', 'Ratio')} rules={[{ required: true }]} extra={t('1 表示按标价收费，0.5 表示半价', '1 = list price, 0.5 = half price')}>
            <InputNumber min={0} step={0.1} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="is_public" label={t('公开分组', 'Public')} valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="description" label={t('说明', 'Description')}>
            <Input placeholder={t('例如：走官方渠道，稳定', 'e.g. official channels, stable')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}
