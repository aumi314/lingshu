// 表格行拖拽排序（@dnd-kit）。用法：Table 的 components.body.row 用 SortableRow，
// 把 DragHandle 放进某一列，外面包一层 SortableBody。
import { createContext, useContext, useMemo, type CSSProperties, type HTMLAttributes, type ReactNode } from 'react'
import { Button } from 'antd'
import { HolderOutlined } from '@ant-design/icons'
import { DndContext, PointerSensor, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { restrictToVerticalAxis } from '@dnd-kit/modifiers'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'

type HandleProps = { setActivatorNodeRef?: (el: HTMLElement | null) => void; listeners?: Record<string, unknown> }
const HandleContext = createContext<HandleProps>({})

export function DragHandle({ title }: { title?: string }) {
  const { setActivatorNodeRef, listeners } = useContext(HandleContext)
  return <Button type="text" size="small" icon={<HolderOutlined />} style={{ cursor: 'move' }} ref={setActivatorNodeRef} title={title} {...listeners} />
}

export function SortableRow(props: HTMLAttributes<HTMLTableRowElement> & { 'data-row-key': string }) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: String(props['data-row-key']),
    transition: { duration: 220, easing: 'cubic-bezier(0.2, 0, 0, 1)' },
  })
  const translate = CSS.Translate.toString(transform)
  const style: CSSProperties = {
    ...props.style,
    // 拿起来时稍微放大，像把卡片从桌面上拿起
    transform: isDragging ? `${translate ?? ''} scale(1.015)` : translate,
    transition: isDragging ? 'box-shadow 150ms ease' : transition,
    ...(isDragging ? { position: 'relative', zIndex: 9 } : {}),
  }
  const ctx = useMemo(() => ({ setActivatorNodeRef, listeners }), [setActivatorNodeRef, listeners])
  return (
    <HandleContext.Provider value={ctx}>
      <tr {...props} className={`${props.className ?? ''}${isDragging ? ' row-dragging' : ''}`} ref={setNodeRef} style={style} {...attributes} />
    </HandleContext.Provider>
  )
}

// settle 让松手的那一行从松手位置滑进空位（FLIP）：等新顺序渲染后量出实际位置，
// 先用 transform 放回松手处，下一帧再过渡到 0。
function settle(id: string, releasedTop: number) {
  requestAnimationFrame(() => {
    const row = document.querySelector<HTMLElement>(`tr[data-row-key="${window.CSS.escape(id)}"]`)
    if (!row) return
    const delta = releasedTop - row.getBoundingClientRect().top
    if (Math.abs(delta) < 1) return
    row.classList.add('row-settling')
    row.style.transition = 'none'
    row.style.transform = `translate3d(0, ${delta}px, 0) scale(1.015)`
    row.getBoundingClientRect() // 强制先应用起点
    row.style.transition = 'transform 220ms cubic-bezier(0.2, 0, 0, 1)'
    row.style.transform = ''
    row.addEventListener('transitionend', () => {
      row.classList.remove('row-settling')
      row.style.transition = ''
    }, { once: true })
  })
}

export function SortableBody({ ids, onMove, children }: { ids: (string | number)[]; onMove: (from: number, to: number) => void; children: ReactNode }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }))
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    const releasedTop = active.rect.current.translated?.top
    if (over && active.id !== over.id) {
      const from = ids.findIndex((id) => String(id) === String(active.id))
      const to = ids.findIndex((id) => String(id) === String(over.id))
      if (from >= 0 && to >= 0) onMove(from, to)
    }
    if (releasedTop != null) settle(String(active.id), releasedTop)
  }
  return (
    <DndContext sensors={sensors} modifiers={[restrictToVerticalAxis]} onDragEnd={onDragEnd}>
      <SortableContext items={ids.map(String)} strategy={verticalListSortingStrategy}>
        {children}
      </SortableContext>
    </DndContext>
  )
}
