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
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id: props['data-row-key'] })
  const style: CSSProperties = {
    ...props.style,
    transform: CSS.Translate.toString(transform),
    transition,
    ...(isDragging ? { position: 'relative', zIndex: 9 } : {}),
  }
  const ctx = useMemo(() => ({ setActivatorNodeRef, listeners }), [setActivatorNodeRef, listeners])
  return (
    <HandleContext.Provider value={ctx}>
      <tr {...props} ref={setNodeRef} style={style} {...attributes} />
    </HandleContext.Provider>
  )
}

export function SortableBody({ ids, onMove, children }: { ids: (string | number)[]; onMove: (from: number, to: number) => void; children: ReactNode }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }))
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (!over || active.id === over.id) return
    const from = ids.findIndex((id) => String(id) === String(active.id))
    const to = ids.findIndex((id) => String(id) === String(over.id))
    if (from >= 0 && to >= 0) onMove(from, to)
  }
  return (
    <DndContext sensors={sensors} modifiers={[restrictToVerticalAxis]} onDragEnd={onDragEnd}>
      <SortableContext items={ids.map(String)} strategy={verticalListSortingStrategy}>
        {children}
      </SortableContext>
    </DndContext>
  )
}
