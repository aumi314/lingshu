// 定价：模型标价 + 服务分组（倍率和可用范围）。"渠道"负责拿到算力，"定价"负责怎么分发。
// 以后的阶梯计价、分时段计价也放在这里加标签。
import { Tabs, Typography } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { useApp } from '../../app'
import ModelsAdmin from './ModelsAdmin'
import Groups from './Groups'

export default function Pricing() {
  const { t } = useApp()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') === 'groups' ? 'groups' : 'models'
  return (
    <div className="page">
      <Typography.Title level={3} style={{ margin: '0 0 4px' }}>
        {t('定价', 'Pricing')}
      </Typography.Title>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
        {t(
          '成员实际付的钱 = 模型标价 × 服务分组倍率。「渠道」负责拿到算力，「定价」负责怎么分发。',
          'Members pay model price × service-group ratio. Channels supply the capacity; pricing decides how it is handed out.',
        )}
      </Typography.Paragraph>
      <Tabs
        activeKey={tab}
        onChange={(k) => setParams(k === 'models' ? {} : { tab: k }, { replace: true })}
        destroyOnHidden
        items={[
          { key: 'models', label: t('模型定价', 'Model prices'), children: <ModelsAdmin /> },
          { key: 'groups', label: t('服务分组', 'Service groups'), children: <Groups /> },
        ]}
      />
    </div>
  )
}
