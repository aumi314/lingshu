import { useEffect } from 'react'
import { Card, Statistic, Typography } from 'antd'
import { useApp } from '../app'

export default function Wallet() {
  const { user, money, t, reload } = useApp()

  useEffect(() => {
    reload()
  }, [reload])

  return (
    <div className="page" style={{ maxWidth: 760 }}>
      <Typography.Title level={3} className="page-title">
        {t('钱包充值', 'Wallet & top-up')}
      </Typography.Title>
      <Card>
        <Statistic title={t('当前余额', 'Current balance')} value={money(user?.balance ?? 0)} />
        <Typography.Paragraph type="secondary" style={{ marginTop: 16, marginBottom: 0 }}>
          {t('如需充值，请联系管理员。', 'Contact an admin to top up.')}
        </Typography.Paragraph>
      </Card>
    </div>
  )
}
