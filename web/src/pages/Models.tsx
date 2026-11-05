import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Alert, Segmented, Table, Tag, Typography } from 'antd'
import { get, type Group, type PricedModel } from '../api'
import { priceText, useApp } from '../app'
import { ruleCondition } from '../pricing'
import ModelIcon, { byVendor } from '../components/ModelIcon'

const entry: Record<string, string> = { chat: '/v1/chat/completions', messages: '/v1/messages', responses: '/v1/responses', systemone: '/v1/systemone' }

export default function Models() {
  const { money, t, currency } = useApp()
  const [groups, setGroups] = useState<Group[]>([])
  const [models, setModels] = useState<PricedModel[]>([])
  const [params, setParams] = useSearchParams()
  const [error, setError] = useState('')

  useEffect(() => {
    get<{ groups: Group[]; models: PricedModel[] }>('/api/me/models')
      .then((r) => {
        setGroups(r.groups)
        setModels(r.models)
      })
      .catch((e) => setError(e.message))
  }, [])

  // 选中的分组记在网址上，刷新不丢；没选或已不可用时用第一个
  const group = groups.find((g) => String(g.id) === params.get('group')) ?? groups[0]
  const groupID = group?.id
  const count = (id: number) => models.filter((m) => m.group_id === id).length
  const unit = `${currency === 'CNY' ? '¥' : '$'} / ${t('百万 tokens', '1M tokens')}`
  const price = (v: string) => (Number(v) === 0 ? <span className="muted">{t('免费', 'Free')}</span> : priceText(money, v))

  return (
    <div className="page">
      <Typography.Title level={3} className="page-title">
        {t('模型与价格', 'Models & pricing')}
      </Typography.Title>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      {groups.length > 1 && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
          <Typography.Text type="secondary">{t('服务分组', 'Service group')}</Typography.Text>
          <Segmented
            value={groupID}
            onChange={(v) => setParams({ group: String(v) }, { replace: true })}
            options={groups.map((g) => ({
              value: g.id,
              label: (
                <span>
                  {g.name}
                  {Number(g.ratio) !== 1 && <Tag color="gold" style={{ marginInlineStart: 6, marginInlineEnd: 0 }}>×{Number(g.ratio)}</Tag>}
                  <span className="muted" style={{ marginInlineStart: 6 }}>{count(g.id)}</span>
                </span>
              ),
            }))}
          />
        </div>
      )}
      {group && (
        <Typography.Paragraph type="secondary">
          {group.description && <>{group.description}{t('。', '. ')}</>}
          {Number(group.ratio) !== 1
            ? t(`价格已按倍率 ×${Number(group.ratio)} 算好，就是实际扣费单价。`, `Prices already include the ×${Number(group.ratio)} ratio; this is what you are charged.`)
            : t('下面就是实际扣费单价。', 'These are the prices you are charged.')}
          {t('创建 API key 时选这个分组，就能调用下面的模型。', 'Create an API key in this group to use the models below.')}
        </Typography.Paragraph>
      )}
      <Table
        rowKey="name"
        pagination={false}
        dataSource={models.filter((m) => m.group_id === groupID).sort(byVendor)}
        locale={{ emptyText: t('这个服务分组还没有可用的模型', 'No models available in this service group yet') }}
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
                <span className="model-name">
                  <ModelIcon name={v} />
                  <Typography.Text copyable className="mono">{v}</Typography.Text>
                </span>
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
