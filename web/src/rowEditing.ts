import type { HTMLAttributes } from 'react'

// 行内操作和文本选择保留自身行为，其余区域打开编辑。
export function rowEditing(open: () => void, label: string): HTMLAttributes<HTMLTableRowElement> {
  return {
    tabIndex: 0,
    'aria-label': label,
    style: { cursor: 'pointer' },
    onClick: (event) => {
      const target = event.target as Element
      if (!event.currentTarget.contains(target)) return
      const control = target.closest('button, a, input, textarea, select, [role="button"], [role="switch"], [data-row-action]')
      if (control && control !== event.currentTarget) return
      const selection = window.getSelection()
      if (selection?.toString() && selection.anchorNode && event.currentTarget.contains(selection.anchorNode)) return
      open()
    },
    onKeyDown: (event) => {
      if (event.target !== event.currentTarget || (event.key !== 'Enter' && event.key !== ' ')) return
      event.preventDefault()
      open()
    },
  }
}
