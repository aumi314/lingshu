import { useState } from 'react'
import { Alert, App, Button, Input, Modal, Popconfirm, Radio, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import type { ChannelKey } from '../api'
import { useApp } from '../app'

// 渠道多 key 的编辑（docs/decisions.md「渠道多 key 与渠道备注」）：已保存的 key 只显示掩码，可启停、写备注、删除；
// 批量添加分追加和覆盖两种模式。粘贴里的重复当场去掉；和已保存 key 的重复由服务端判断（页面看不到完整 key），保存后提示。

export const MAX_CHANNEL_KEYS = 50

// KeyRow 是编辑中的一把 key：带 id 的是已保存的（api_key 为空，不改），不带 id 的是这次新加、还没保存的。
export type KeyRow = ChannelKey & { uid: number }

let uidSeq = 1
export const toRows = (keys: ChannelKey[] = []): KeyRow[] => keys.map((k) => ({ ...k, uid: uidSeq++ }))

// 保存时发给服务端的 key 列表：已保存的只带 id、启停和备注，新加的带完整 key
export const toPayload = (rows: KeyRow[]) =>
  rows.map((r) => (r.id ? { id: r.id, enabled: r.enabled, note: r.note } : { api_key: r.api_key, enabled: r.enabled, note: r.note }))

const tail = (k = '') => (k.length > 10 ? `${k.slice(0, 4)}…${k.slice(-4)}` : '*'.repeat(k.length))

export default function ChannelKeys({ value, onChange }: { value: KeyRow[]; onChange: (rows: KeyRow[]) => void }) {
  const { t } = useApp()
  const { message } = App.useApp()
  const [adding, setAdding] = useState(false)
  const [text, setText] = useState('')
  const [mode, setMode] = useState<'append' | 'replace'>('append')

  const update = (uid: number, patch: Partial<KeyRow>) => onChange(value.map((r) => (r.uid === uid ? { ...r, ...patch } : r)))

  const confirmAdd = () => {
    const lines = text.split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
    const unique = [...new Set(lines)]
    if (!unique.length) {
      message.warning(t('没有可添加的 key', 'No keys to add'))
      return
    }
    // 追加时去掉和本次已加、还没保存的 key 重复的；和已保存 key 的重复留给服务端去
    const pending = new Set(value.filter((r) => !r.id).map((r) => r.api_key))
    const fresh = mode === 'append' ? unique.filter((k) => !pending.has(k)) : unique
    const next = [...(mode === 'append' ? value : []), ...fresh.map((k) => ({ uid: uidSeq++, api_key: k, masked: tail(k), enabled: true, note: '' }))]
    if (next.length > MAX_CHANNEL_KEYS) {
      message.error(t(`一个渠道最多 ${MAX_CHANNEL_KEYS} 把 key，这样会有 ${next.length} 把`, `At most ${MAX_CHANNEL_KEYS} keys per channel (this would make ${next.length})`))
      return
    }
    onChange(next)
    const dropped = lines.length - fresh.length
    message.success(
      t(
        `${mode === 'replace' ? '将替换为' : '将追加'} ${fresh.length} 把 key${dropped ? `（粘贴里去掉 ${dropped} 把重复）` : ''}，保存后生效`,
        `${mode === 'replace' ? 'Will replace with' : 'Will append'} ${fresh.length} key(s)${dropped ? ` (${dropped} duplicate(s) dropped)` : ''}; save to apply`,
      ),
    )
    setAdding(false)
    setText('')
  }

  const statusTag = (r: KeyRow) => {
    if (!r.id) return <Tag color="blue">{t('待保存', 'Unsaved')}</Tag>
    const s = r.status
    if (!s || s.state === 'ok') return <Tag color="green">{t('正常', 'OK')}</Tag>
    const until = s.until ? dayjs(s.until).format('HH:mm:ss') : ''
    return (
      <Tooltip title={s.reason}>
        {s.state === 'invalid'
          ? <Tag color="red">{t(`失效（${until} 自动再试）`, `Invalid (retry at ${until})`)}</Tag>
          : <Tag color="orange">{t(`冷却中（到 ${until}）`, `Cooling (until ${until})`)}</Tag>}
      </Tooltip>
    )
  }

  return (
    <div>
      <Table<KeyRow>
        rowKey="uid"
        size="small"
        pagination={false}
        dataSource={value}
        locale={{ emptyText: t('还没有 key，点“添加 key”粘贴', 'No keys yet; click "Add keys" to paste') }}
        columns={[
          { title: 'Key', render: (_, r) => <span className="mono">{r.masked}</span> },
          { title: t('状态', 'Status'), render: (_, r) => statusTag(r) },
          {
            title: <Tooltip title={t('最近 24 小时最终由这把 key 完成的成功请求 / 在这把 key 上失败的尝试', 'Last 24h: successes finished on this key / failed attempts on it')}>{t('24 小时', '24h')}</Tooltip>,
            render: (_, r) => (r.stats ? <span className="nowrap">{r.stats.success} / <Typography.Text type={r.stats.failed ? 'danger' : undefined}>{r.stats.failed}</Typography.Text></span> : <span className="muted">—</span>),
          },
          { title: t('备注', 'Note'), render: (_, r) => <Input size="small" value={r.note} maxLength={100} placeholder={t('账号、来源', 'Account, source')} onChange={(e) => update(r.uid, { note: e.target.value })} /> },
          { title: t('启用', 'On'), width: 64, render: (_, r) => <Switch size="small" checked={r.enabled} onChange={(v) => update(r.uid, { enabled: v })} /> },
          {
            width: 40,
            render: (_, r) => (
              <Popconfirm title={t('删掉这把 key？保存后生效', 'Remove this key? Takes effect on save')} onConfirm={() => onChange(value.filter((x) => x.uid !== r.uid))}>
                <Button size="small" type="text" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            ),
          },
        ]}
      />
      <Space style={{ marginTop: 8 }}>
        <Button size="small" icon={<PlusOutlined />} onClick={() => setAdding(true)}>{t('添加 key', 'Add keys')}</Button>
        <Typography.Text type="secondary">{t(`${value.length} / ${MAX_CHANNEL_KEYS} 把；同一会话固定用一把，限流或失效时自动换下一把`, `${value.length} / ${MAX_CHANNEL_KEYS}; each session sticks to one key and fails over on rate limits or invalid keys`)}</Typography.Text>
      </Space>
      <Modal open={adding} title={t('添加 key', 'Add keys')} onCancel={() => setAdding(false)} onOk={confirmAdd} okText={t('确定', 'OK')} destroyOnHidden>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Radio.Group value={mode} onChange={(e) => setMode(e.target.value)} optionType="button" options={[
            { value: 'append', label: t('追加', 'Append') },
            { value: 'replace', label: t('覆盖', 'Replace all') },
          ]} />
          {mode === 'replace' && value.some((r) => r.id) && (
            <Alert type="warning" showIcon title={t('覆盖会替换现有的全部 key；和现有相同的 key 保留原来的记录和统计。保存后才生效。', 'Replaces all current keys; keys identical to existing ones keep their history. Takes effect on save.')} />
          )}
          <Input.TextArea rows={8} className="mono" value={text} onChange={(e) => setText(e.target.value)} placeholder={t('一行一把 key，自动去掉空行和重复', 'One key per line; blank lines and duplicates are removed')} />
        </Space>
      </Modal>
    </div>
  )
}
