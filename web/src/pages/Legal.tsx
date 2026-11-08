// 公开的法律文档页：登录前可访问，原样显示纯文本（保留换行，不渲染 HTML）。
import { useEffect, useState } from 'react'
import { Alert, Card, Spin, Typography } from 'antd'
import { Link } from 'react-router-dom'
import { useApp } from '../app'
import Logo from '../components/Logo'
import { fetchLegal, type LegalDocs } from '../legal'

export default function Legal({ kind }: { kind: 'terms' | 'privacy' }) {
  const { t } = useApp()
  const [docs, setDocs] = useState<LegalDocs | null>(null)
  const [error, setError] = useState('')
  const title = kind === 'terms' ? t('用户协议', 'Terms of Service') : t('隐私政策', 'Privacy Policy')
  const content = kind === 'terms' ? docs?.user_agreement ?? '' : docs?.privacy_policy ?? ''

  useEffect(() => {
    fetchLegal()
      .then(setDocs)
      .catch((e) => setError((e as Error).message))
  }, [])

  return (
    <div className="center-screen" style={{ alignItems: 'flex-start', padding: '24px 16px' }}>
      <Card style={{ width: 760, maxWidth: 'calc(100vw - 32px)' }}>
        <Typography.Title level={3} style={{ marginTop: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
          <Logo size={32} />
          {title}
        </Typography.Title>
        {error ? (
          <Alert type="error" showIcon title={error} />
        ) : !docs ? (
          <Spin />
        ) : !content.trim() ? (
          <Alert type="info" showIcon message={t('管理员还没有发布这份文档。', 'This document has not been published yet.')} />
        ) : (
          <div style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.8 }}>
            {content}
          </div>
        )}
        <div style={{ marginTop: 16 }}>
          <Link to="/register">{t('返回注册', 'Back to registration')}</Link>
        </div>
      </Card>
    </div>
  )
}
