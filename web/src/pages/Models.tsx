import { useEffect, useState } from 'react'
import { Alert, Segmented, Table, Tag, Typography } from 'antd'
import { get, type Group, type PricedModel } from '../api'
import { priceText, useApp } from '../app'
import { ruleCondition } from '../pricing'

const entry: Record<string, string> = { chat: '/v1/chat/completions', messages: '/v1/messages', responses: '/v1/responses', systemone: '/v1/systemone' }

export default function Models() {
  const { money, t, currency } = useApp()
  const [groups, setGroups] = useState<Group[]>([])
  const [models, setModels] = useState<PricedModel[]>([])
  const [groupID, setGroupID] = useState<number>()
  const [error, setError] = useState('')

  useEffect(() => {
    get<{ groups: Group[]; models: PricedModel[] }>('/api/me/models')
      .then((r) => {
        setGroups(r.groups)
        setModels(r.models)
        setGroupID(r.groups[0]?.id)
      })
      .catch((e) => setError(e.message))
  }, [])

  const group = groups.find((g) => g.id === groupID)
  const unit = `${currency === 'CNY' ? '¥' : '$'} / ${t('百万 tokens', '1M tokens')}`
  const price = (v: string) => (Number(v) === 0 ? <span className="muted">{t('免费', 'Free')}</span> : priceText(money, v))

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('模型与价格', 'Models & pricing')}
      </Typography.Title>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      {groups.length > 1 && (
        <Segmented
          style={{ marginBottom: 16 }}
          value={groupID}
          onChange={(v) => setGroupID(v as number)}
          options={groups.map((g) => ({ value: g.id, label: `${g.name} ×${Number(g.ratio)}` }))}
        />
      )}
      {group && (
        <Typography.Paragraph type="secondary">
          {t('分组', 'Group')} <b>{group.name}</b>
          {t('，倍率 ', ', ratio ')}
          {Number(group.ratio)}
          {group.description && ` · ${group.description}`}
          {t('。', '. ')}
          {t('下面的价格已经乘过倍率，就是实际扣费的单价。', 'Prices below already include the ratio — they are what you are actually charged.')}
        </Typography.Paragraph>
      )}
      <Table
        rowKey="name"
        pagination={false}
        dataSource={models.filter((m) => m.group_id === groupID)}
        locale={{ emptyText: t('这个分组还没有可用的模型', 'No models available in this group yet') }}
        expandable={{
          rowExpandable: (m) => m.price_rules?.length > 0,
          expandedRowRender: (m) => (
            <div style={{ paddingLeft: 8 }}>
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                {t('满足下面的条件时改按这些价格计费（从上往下，第一条满足的生效）：', 'These prices apply when the condition matches (first match wins):')}
              </Typography.Text>
              {m.price_rules.map((r, i) => (
                <div key={i} style={{ marginTop: 6 }}>
                  <Tag>{ruleCondition(r, t)}</Tag>
                  {t('输入', 'Input')} {price(r.input_price)} · {t('输出', 'Output')} {price(r.output_price)} · {t('缓存命中', 'Cache hit')} {price(r.cache_read_price)} · {t('缓存写入', 'Cache write')} {price(r.cache_write_price)}
                </div>
              ))}
            </div>
          ),
        }}
        columns={[
          {
            title: t('模型', 'Model'),
            dataIndex: 'name',
            render: (v, m) => (
              <>
                <Typography.Text copyable className="mono">{v}</Typography.Text>
                {m.price_rules?.length > 0 && <Tag color="purple" style={{ marginLeft: 8 }}>{t('阶梯价', 'Tiered')}</Tag>}
              </>
            ),
          },
          {
            title: t('调用入口', 'Endpoint'),
            dataIndex: 'protocols',
            render: (ps: string[]) => ps.map((p) => <Tag key={p} className="mono">{entry[p]}</Tag>),
          },
          { title: `${t('输入', 'Input')}（${unit}）`, dataIndex: 'input_price', render: price },
          { title: t('输出', 'Output'), dataIndex: 'output_price', render: price },
          { title: t('缓存命中', 'Cache hit'), dataIndex: 'cache_read_price', render: price },
          { title: t('缓存写入', 'Cache write'), dataIndex: 'cache_write_price', render: price },
        ]}
      />
    </div>
  )
}
