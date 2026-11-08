import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Alert, Segmented, Table, Tag, Tooltip, Typography } from 'antd'
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
  const prices = (p: Pick<PricedModel, 'input_price' | 'output_price' | 'cache_read_price' | 'cache_write_price'>, k = 1) => {
    const x = (v: string) => price(String(Number(v) * k))
    return <>{t('输入', 'Input')} {x(p.input_price)} · {t('输出', 'Output')} {x(p.output_price)} · {t('缓存命中', 'Cache hit')} {x(p.cache_read_price)} · {t('缓存写入', 'Cache write')} {x(p.cache_write_price)}</>
  }

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
          rowExpandable: (m) => m.price_rules?.length > 0 || m.fast_multiplier != null,
          expandedRowRender: (m) => {
            const fast = m.fast_multiplier == null ? 0 : Number(m.fast_multiplier)
            return (
              <div style={{ paddingLeft: 8 }}>
                {m.price_rules.length > 0 && <>
                  <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                    {t('满足下面的条件时改按这些价格计费（从上往下，第一条满足的生效）：', 'These prices apply when the condition matches (first match wins):')}
                  </Typography.Text>
                  {m.price_rules.map((r, i) => (
                    <div key={i} style={{ marginTop: 6 }}>
                      <Tag>{ruleCondition(r, t)}</Tag>
                      {prices(r)}
                    </div>
                  ))}
                </>}
                {fast > 0 && <div style={{ marginTop: m.price_rules.length ? 12 : 0 }}>
                  <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                    {t(`Fast：请求里写 service_tier 为 fast（或旧名 priority）时，按对应价格 ×${fast} 计费；上游降回标准速度时按标准价。`, `Fast: with service_tier set to fast (or priority), the matching price ×${fast} applies; standard price if the upstream downgrades it.`)}
                  </Typography.Text>
                  <div style={{ marginTop: 6 }}><Tag color="orange">Fast</Tag>{prices(m, fast)}</div>
                  {m.price_rules.map((r, i) => (
                    <div key={i} style={{ marginTop: 6 }}>
                      <Tag color="orange">Fast · {ruleCondition(r, t)}</Tag>
                      {prices(r, fast)}
                    </div>
                  ))}
                </div>}
              </div>
            )
          },
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
                {m.fast_multiplier != null && <Tag color="orange" style={{ marginLeft: m.price_rules?.length ? 0 : 8 }}>Fast ×{Number(m.fast_multiplier)}</Tag>}
              </>
            ),
          },
          {
            title: t('调用入口', 'Endpoint'),
            dataIndex: 'protocols',
            render: (ps: string[], m: PricedModel) => (
              <>
                {ps.map((p) => <Tag key={p} className="mono">{entry[p]}</Tag>)}
                {m.bridged_protocols?.includes('responses') && (
                  <Tooltip title={t('上游只支持 Messages，Responses 请求由网关自动转接。', 'The upstream only speaks Messages; the gateway converts Responses requests for you.')}>
                    <Tag color="cyan">{t('Responses（转接）', 'Responses (bridged)')}</Tag>
                  </Tooltip>
                )}
              </>
            ),
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
