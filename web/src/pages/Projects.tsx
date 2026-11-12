// 我的项目（docs/decisions.md「项目额度」）：成员在这里加入项目、在项目下建 key；创建人管理额度池、规则和成员。
// 上面一排是项目卡片，选中的项目直接在下面展开（选择记在网址 ?project=），不放进弹窗。
// 项目 key 调用时花项目池的钱，不碰个人余额。
import { useCallback, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Alert, App, Button, Card, Col, Empty, Form, Input, InputNumber, Modal, Popconfirm, Progress, Row, Segmented, Select, Space, Table, Tabs, Tag, Typography, theme } from 'antd'
import { PlusOutlined, TeamOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { del, get, patch, post, type APIKey, type Group, type LimitPeriod, type MyProjects, type Project, type ProjectMember } from '../api'
import { useApp } from '../app'

// 额度周期的说法：本周期叫什么、什么时候重置
const periodText = (t: (zh: string, en: string) => string, p: LimitPeriod, zone: string) =>
  ({
    day: { now: t('今天', 'today'), reset: t(`${zone}每天 0 点重置`, `resets daily at 00:00 ${zone}`), pick: t('按天', 'Daily') },
    week: { now: t('本周', 'this week'), reset: t(`${zone}每周一 0 点重置`, `resets Monday 00:00 ${zone}`), pick: t('按周', 'Weekly') },
    month: { now: t('本月', 'this month'), reset: t(`${zone}每月 1 号 0 点重置`, `resets on the 1st at 00:00 ${zone}`), pick: t('按月', 'Monthly') },
  })[p] ?? { now: t('本周', 'this week'), reset: '', pick: t('按周', 'Weekly') }

const statusTag = (t: (zh: string, en: string) => string, s: Project['my_status']) =>
  ({ owner: <Tag color="gold">{t('创建人', 'Owner')}</Tag>, active: <Tag color="green">{t('成员', 'Member')}</Tag>, pending: <Tag>{t('待审批', 'Pending')}</Tag>, '': null })[s]

export default function Projects() {
  const { t, money, user, zone } = useApp()
  const { message } = App.useApp()
  const [data, setData] = useState<MyProjects | null>(null)
  const [params, setParams] = useSearchParams()
  const { token } = theme.useToken()
  const [creating, setCreating] = useState(false)
  const [joinCode, setJoinCode] = useState('')
  const [form] = Form.useForm<{ name: string }>()

  const load = useCallback(async () => {
    try {
      const r = await get<MyProjects>('/api/me/projects')
      setData(r)
    } catch (e) {
      message.error((e as Error).message)
    }
  }, [message])
  useEffect(() => {
    load()
  }, [load])

  const join = async () => {
    if (!joinCode.trim()) return
    try {
      const r = await post<{ name: string }>('/api/me/projects/join', { code: joinCode.trim() })
      message.success(t(`已申请加入「${r.name}」，等待项目负责人审批`, `Requested to join "${r.name}"; waiting for the owner to approve`))
      setJoinCode('')
      load()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const create = async () => {
    const v = await form.validateFields()
    try {
      const p = await post<Project>('/api/me/projects', v)
      setCreating(false)
      form.resetFields()
      await load()
      select(p.id)
      message.success(t('项目已创建：先转入额度、选开放的分组，再把邀请码发给成员', 'Project created: add funds and pick groups, then share the invite code'))
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const select = (id?: number) => setParams(id ? { project: String(id) } : {}, { replace: true })
  // 选中的项目：网址里的那个，没有就默认第一个已加入的（待审批的不能展开）
  const usable = data?.projects.filter((p) => p.my_status !== 'pending') ?? []
  const selected = usable.find((p) => String(p.id) === params.get('project')) ?? usable[0]

  if (!data) return <div className="page" />
  if (!data.enabled) {
    return (
      <div className="page">
        <Typography.Title level={3} className="page-title">{t('我的项目', 'My projects')}</Typography.Title>
        <Alert type="info" showIcon title={t('项目功能没有开启，请联系管理员', 'Projects are not enabled; ask an admin')} />
      </div>
    )
  }
  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">{t('我的项目', 'My projects')}</Typography.Title>
      <Typography.Paragraph type="secondary">
        {t(
          '项目有一个共享额度池，由项目负责人从自己的余额转入。在项目里建的 API key，调用花的是项目池的钱，不扣你的个人余额。拿到邀请码就能申请加入，负责人审批后生效。',
          'A project has a shared pool funded by its owner. API keys you create in a project spend the pool, not your own balance. Join with an invite code; it takes effect once the owner approves.',
        )}
      </Typography.Paragraph>
      <Space wrap style={{ marginBottom: 20 }}>
        <Space.Compact>
          <Input placeholder={t('输入邀请码', 'Invite code')} value={joinCode} onChange={(e) => setJoinCode(e.target.value)} onPressEnter={join} style={{ width: 180 }} className="mono" />
          <Button onClick={join}>{t('申请加入', 'Request to join')}</Button>
        </Space.Compact>
        {data.can_create && (
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
            {t('创建项目', 'Create project')}
          </Button>
        )}
      </Space>
      {data.projects.length === 0 ? (
        <Empty description={data.can_create ? t('还没有项目：创建一个，或者用邀请码加入', 'No projects yet: create one or join with an invite code') : t('还没有项目：向项目负责人要邀请码加入', 'No projects yet: ask a project owner for an invite code')} />
      ) : (
        <div className="project-strip">
          {data.projects.map((p) => (
            <ProjectCard key={p.id} p={p} active={p.id === selected?.id} onOpen={() => p.my_status !== 'pending' && select(p.id)} />
          ))}
        </div>
      )}
      {selected && <ProjectDetail key={selected.id} project={selected} meID={user?.id ?? 0} onGone={() => { select(); load() }} onChanged={load} money={money} />}

      <Modal open={creating} title={t('创建项目', 'Create project')} onOk={create} onCancel={() => setCreating(false)} okText={t('创建', 'Create')} destroyOnHidden>
        <Form form={form} layout="vertical" requiredMark={false}>
          <Form.Item name="name" label={t('项目名称', 'Project name')} rules={[{ required: true, whitespace: true, max: 50 }]}>
            <Input placeholder={t('例如：研发组', 'e.g. R&D team')} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )

  function ProjectCard({ p, active, onOpen }: { p: Project; active: boolean; onOpen: () => void }) {
    const avail = Number(p.balance) - Number(p.reserved)
    const limit = p.period_limit == null ? null : Number(p.period_limit)
    const now = periodText(t, p.limit_period, zone).now
    return (
      <Card
        size="small"
        hoverable={p.my_status !== 'pending'}
        onClick={onOpen}
        aria-current={active || undefined}
        className="project-card"
        style={active ? { borderColor: token.colorPrimary, boxShadow: `0 0 0 1px ${token.colorPrimary}` } : undefined}
        title={<Space size={6}>{p.name}{statusTag(t, p.my_status)}{p.frozen && <Tag color="red">{t('已冻结', 'Frozen')}</Tag>}</Space>}
      >
        {p.my_status === 'pending' ? (
          <Typography.Text type="secondary">{t(`已申请，等待 ${p.owner_name} 审批`, `Waiting for ${p.owner_name} to approve`)}</Typography.Text>
        ) : (
          <Space direction="vertical" style={{ width: '100%' }} size={6}>
            <Typography.Text type="secondary">{t('池子可用', 'Pool available')}</Typography.Text>
            <Typography.Text style={{ fontSize: 20 }}>{money(avail)}</Typography.Text>
            <Typography.Text type="secondary">
              {limit == null ? t(`${now}已花 ${money(p.period_spent)}，不限上限`, `${money(p.period_spent)} spent ${now}, no cap`) : t(`${now}已花 ${money(p.period_spent)} / ${money(limit)}`, `${money(p.period_spent)} / ${money(limit)} ${now}`)}
            </Typography.Text>
            {limit != null && <Progress percent={Math.min(100, Math.round((Number(p.period_spent) / limit) * 100))} size="small" showInfo={false} />}
            <Typography.Text type="secondary">
              <TeamOutlined /> {p.members} · {t('负责人', 'Owner')} {p.owner_name}
            </Typography.Text>
          </Space>
        )}
      </Card>
    )
  }
}

function ProjectDetail({ project, meID, onGone, onChanged, money }: { project: Project; meID: number; onGone: () => void; onChanged: () => void; money: (v: string | number, d?: number) => string }) {
  const { t, zone } = useApp()
  const { message, modal } = App.useApp()
  const period = periodText(t, project.limit_period, zone)
  const owner = project.my_status === 'owner'
  const [members, setMembers] = useState<ProjectMember[]>([])
  const [keys, setKeys] = useState<APIKey[]>([])
  const [groups, setGroups] = useState<Group[]>([]) // 创建人自己能用的分组，开放时从这里选
  const [created, setCreated] = useState('')
  const [keyForm] = Form.useForm<{ name: string; group_id: number }>()
  const [rules] = Form.useForm<{ name: string; limit_period: LimitPeriod; period_limit: number | null; group_ids: number[] }>()
  const [amount, setAmount] = useState<number | null>(null)
  const [direction, setDirection] = useState<'in' | 'out'>('in')

  const loadDetail = useCallback(async () => {
    try {
      const [d, k] = await Promise.all([get<{ members?: ProjectMember[] }>(`/api/me/projects/${project.id}`), get<APIKey[]>('/api/me/keys')])
      setMembers(d.members ?? [])
      setKeys(k.filter((x) => x.project_id === project.id))
    } catch (e) {
      message.error((e as Error).message)
    }
  }, [project.id, message])
  useEffect(() => {
    loadDetail()
    if (owner) get<{ groups: Group[] }>('/api/me/models').then((r) => setGroups(r.groups)).catch(() => {})
  }, [loadDetail, owner])
  useEffect(() => {
    rules.setFieldsValue({ name: project.name, limit_period: project.limit_period ?? 'week', period_limit: project.period_limit == null ? null : Number(project.period_limit), group_ids: project.group_ids })
  }, [project, rules])

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      message.success(ok)
      onChanged()
      loadDetail()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const base = `/api/me/projects/${project.id}`
  const createKey = async () => {
    const v = await keyForm.validateFields()
    try {
      const r = await post<{ key: string }>('/api/me/keys', { ...v, project_id: project.id })
      keyForm.resetFields()
      setCreated(r.key)
      loadDetail()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const saveRules = async () => {
    const v = await rules.validateFields()
    run(() => patch(base, { name: v.name, group_ids: v.group_ids ?? [], limit_period: v.limit_period, ...(v.period_limit ? { period_limit: String(v.period_limit) } : { no_period_limit: true }) }), t('规则已保存', 'Rules saved'))
  }
  const avail = Number(project.balance) - Number(project.reserved)

  const keysTab = (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      {project.groups.length === 0 ? (
        <Alert type="warning" showIcon title={owner ? t('还没有开放分组，先在「规则」里选开放的分组', 'No groups open yet; pick them under Rules') : t('项目还没有开放分组，请联系负责人', 'The project has no open groups yet; ask the owner')} />
      ) : (
        <Form form={keyForm} layout="inline" requiredMark={false}>
          <Form.Item name="name" rules={[{ required: true, whitespace: true, message: t('起个名字', 'Name it') }]}>
            <Input placeholder={t('key 名称，例如：笔记本', 'Key name, e.g. laptop')} style={{ width: 200 }} />
          </Form.Item>
          <Form.Item name="group_id" rules={[{ required: true, message: t('选分组', 'Pick a group') }]}>
            <Select placeholder={t('服务分组', 'Service group')} style={{ width: 220 }} options={project.groups.map((g) => ({ value: g.id, label: g.is_collection ? g.name : `${g.name} · ×${Number(g.ratio)}` }))} />
          </Form.Item>
          <Button type="primary" onClick={createKey} disabled={project.frozen}>{t('创建 key', 'Create key')}</Button>
        </Form>
      )}
      <Table
        rowKey="id"
        size="small"
        pagination={false}
        dataSource={keys}
        locale={{ emptyText: t('你在这个项目下还没有 key', 'You have no keys in this project yet') }}
        columns={[
          { title: t('名称', 'Name'), dataIndex: 'name' },
          { title: 'Key', dataIndex: 'prefix', render: (v) => <span className="mono">{v}…</span> },
          { title: t('分组', 'Group'), dataIndex: 'group_name' },
          { title: t('已用', 'Used'), dataIndex: 'quota_used', render: (v) => money(v) },
          { title: t('最近调用', 'Last used'), dataIndex: 'last_used_at', render: (v) => (v ? dayjs(v).format('MM-DD HH:mm') : '—') },
        ]}
      />
      <Typography.Text type="secondary">{t('改名、停用、删除项目 key 在「API keys」页。', 'Rename, disable or delete project keys on the API keys page.')}</Typography.Text>
    </Space>
  )

  const membersTab = (
    <Table
      rowKey="user_id"
      size="small"
      pagination={false}
      dataSource={members}
      columns={[
        { title: t('成员', 'Member'), dataIndex: 'username' },
        { title: t('身份', 'Role'), dataIndex: 'status', render: (s) => statusTag(t, s) },
        { title: t(`${period.now}花费`, `Spent ${period.now}`), dataIndex: 'period_spent', render: (v, m) => (m.status === 'pending' ? '—' : money(v)) },
        { title: t('累计花费', 'Total'), dataIndex: 'total_spent', render: (v, m) => (m.status === 'pending' ? '—' : money(v)) },
        {
          title: '',
          align: 'right',
          render: (_, m) =>
            m.status === 'owner' ? null : (
              <Space>
                {m.status === 'pending' && (
                  <Button size="small" type="primary" onClick={() => run(() => post(`${base}/members/${m.user_id}/approve`), t('已通过', 'Approved'))}>
                    {t('通过', 'Approve')}
                  </Button>
                )}
                <Popconfirm
                  title={m.status === 'pending' ? t('拒绝这个申请？', 'Reject this request?') : t('移出后他在项目下的 key 立即不能用，确定？', 'Their project keys stop working right away. Remove?')}
                  onConfirm={() => run(() => del(`${base}/members/${m.user_id}`), m.status === 'pending' ? t('已拒绝', 'Rejected') : t('已移出', 'Removed'))}
                >
                  <Button size="small" danger type="link">{m.status === 'pending' ? t('拒绝', 'Reject') : t('移出', 'Remove')}</Button>
                </Popconfirm>
              </Space>
            ),
        },
      ]}
    />
  )

  const poolTab = (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      <Row gutter={16}>
        <Col span={12}><Card size="small"><Typography.Text type="secondary">{t('池子可用', 'Available')}</Typography.Text><div style={{ fontSize: 22 }}>{money(avail)}</div>{Number(project.reserved) > 0 && <Typography.Text type="secondary">{t(`进行中冻结 ${money(project.reserved)}`, `${money(project.reserved)} held by calls in progress`)}</Typography.Text>}</Card></Col>
        <Col span={12}><Card size="small"><Typography.Text type="secondary">{t(`${period.now}已花`, `Spent ${period.now}`)}</Typography.Text><div style={{ fontSize: 22 }}>{money(project.period_spent)}</div><Typography.Text type="secondary">{project.period_limit == null ? t('不限上限', 'No cap') : t(`上限 ${money(project.period_limit)}，${period.reset}`, `Cap ${money(project.period_limit)}, ${period.reset}`)}</Typography.Text></Card></Col>
      </Row>
      <Space wrap>
        <Segmented value={direction} onChange={(v) => setDirection(v as 'in' | 'out')} options={[{ value: 'in', label: t('从我的余额转入', 'Add from my balance') }, { value: 'out', label: t('取回到我的余额', 'Withdraw to my balance') }]} />
        <InputNumber prefix="$" min={0.01} precision={2} value={amount} onChange={setAmount} style={{ width: 140 }} placeholder={t('金额', 'Amount')} />
        <Button type="primary" disabled={!amount} onClick={() => run(() => post(`${base}/transfer`, { direction, amount: String(amount) }), direction === 'in' ? t('已转入', 'Added') : t('已取回', 'Withdrawn')).then(() => setAmount(null))}>
          {t('确定', 'Confirm')}
        </Button>
      </Space>
      <Typography.Text type="secondary">{t('只能动可用部分：你的余额扣掉冻结的，池子扣掉进行中请求冻结的。每笔都记在你的账户日志里。', 'Only available funds move: your balance minus holds, the pool minus holds by calls in progress. Each move shows in your account log.')}</Typography.Text>
    </Space>
  )

  const rulesTab = (
    <Form form={rules} layout="vertical" requiredMark={false} style={{ maxWidth: 520 }}>
      <Form.Item name="name" label={t('项目名称', 'Project name')} rules={[{ required: true, whitespace: true, max: 50 }]}>
        <Input />
      </Form.Item>
      <Form.Item label={t('额度上限', 'Spending cap')} extra={t(`整个项目合计，周期按${zone}重置（按天每天 0 点、按周每周一 0 点、按月每月 1 号 0 点）；金额留空不限`, `Whole project; periods reset in ${zone} (daily at 00:00, weekly on Monday, monthly on the 1st); leave the amount empty for no cap`)}>
        <Space>
          <Form.Item name="limit_period" noStyle>
            <Segmented options={(['day', 'week', 'month'] as const).map((p) => ({ value: p, label: periodText(t, p, zone).pick }))} />
          </Form.Item>
          <Form.Item name="period_limit" noStyle>
            <InputNumber prefix="$" min={0.01} precision={2} style={{ width: 160 }} placeholder={t('不限', 'No cap')} />
          </Form.Item>
        </Space>
      </Form.Item>
      <Form.Item name="group_ids" label={t('开放的服务分组', 'Open service groups')} extra={t('成员在项目下建 key 时能选这些分组；只能从你自己能用的分组里选', 'Members pick from these when creating project keys; limited to groups you can use')}>
        <Select mode="multiple" options={groups.map((g) => ({ value: g.id, label: g.is_collection ? g.name : `${g.name} · ×${Number(g.ratio)}` }))} />
      </Form.Item>
      <Button type="primary" onClick={saveRules}>{t('保存', 'Save')}</Button>
      <div style={{ marginTop: 28 }}>
        <Typography.Title level={5}>{t('邀请码', 'Invite code')}</Typography.Title>
        <Space>
          <Typography.Text code copyable className="mono" style={{ fontSize: 16 }}>{project.invite_code}</Typography.Text>
          <Popconfirm title={t('换新码后旧码立即失效，确定？', 'The old code stops working. Reset?')} onConfirm={() => run(() => post(`${base}/invite-code`), t('邀请码已重置', 'Invite code reset'))}>
            <Button size="small">{t('重置', 'Reset')}</Button>
          </Popconfirm>
        </Space>
      </div>
      <div style={{ marginTop: 28 }}>
        <Button
          danger
          onClick={() =>
            modal.confirm({
              title: t('解散项目？', 'Close project?'),
              content: t(`池子里剩下的 ${money(project.balance)} 会退回你的余额，所有项目 key 不能再用。不能撤销。`, `The remaining ${money(project.balance)} returns to your balance and every project key stops working. This cannot be undone.`),
              okButtonProps: { danger: true },
              okText: t('解散', 'Close'),
              onOk: () => run(() => del(base), t('项目已解散，余额已退回', 'Project closed; funds returned')).then(onGone),
            })
          }
        >
          {t('解散项目', 'Close project')}
        </Button>
      </div>
    </Form>
  )

  return (
    <Card style={{ marginTop: 20 }} title={<Space>{project.name}{statusTag(t, project.my_status)}{project.frozen && <Tag color="red">{t('已被管理员冻结', 'Frozen by admin')}</Tag>}<Typography.Text type="secondary" style={{ fontWeight: 400 }}>{t('负责人', 'Owner')} {project.owner_name}</Typography.Text></Space>}>
      <Tabs
        items={[
          { key: 'keys', label: t('我的 key', 'My keys'), children: keysTab },
          ...(owner
            ? [
                { key: 'pool', label: t('额度池', 'Pool'), children: poolTab },
                { key: 'members', label: t('成员', 'Members') + (members.some((m) => m.status === 'pending') ? ` · ${members.filter((m) => m.status === 'pending').length}` : ''), children: membersTab },
                { key: 'rules', label: t('规则', 'Rules'), children: rulesTab },
              ]
            : []),
        ]}
      />
      {!owner && (
        <Popconfirm title={t('退出后你在项目下的 key 立即不能用，确定？', 'Your project keys stop working right away. Leave?')} onConfirm={() => run(() => del(`${base}/members/${meID}`), t('已退出项目', 'Left the project')).then(onGone)}>
          <Button danger type="link" style={{ padding: 0 }}>{t('退出项目', 'Leave project')}</Button>
        </Popconfirm>
      )}
      <Modal open={!!created} title={t('API key 已创建', 'API key created')} onOk={() => setCreated('')} onCancel={() => setCreated('')} cancelButtonProps={{ style: { display: 'none' } }}>
        <Alert type="warning" showIcon title={t('只显示这一次，请现在复制保存', 'Shown only once; copy it now')} style={{ marginBottom: 12 }} />
        <Typography.Paragraph copyable={{ text: created }} className="mono" style={{ wordBreak: 'break-all' }}>{created}</Typography.Paragraph>
        <Typography.Text type="secondary">{t('用它调用时，花的是项目池的钱。', 'Calls with it spend the project pool.')}</Typography.Text>
      </Modal>
    </Card>
  )
}
