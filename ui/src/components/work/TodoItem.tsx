import { useCallback } from 'react'
import { GripVertical, Check } from 'lucide-react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Todo, TodoPriority } from '@/lib/types'

const PRIORITY_STYLE: Record<TodoPriority, string> = {
  critical: 'text-danger bg-danger/10',
  high: 'text-warning bg-warning/10',
  medium: 'text-fg-muted bg-surface',
  low: 'text-fg-faint bg-surface/50',
}

interface TodoItemProps {
  todo: Todo
  onCheck: (id: string) => void
  onUncheck: (id: string, reason?: string) => void
}

export function TodoItem({ todo, onCheck, onUncheck }: TodoItemProps) {
  const isDone = todo.status === 'done'

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: todo.id })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : undefined,
  }

  const handleToggle = useCallback(() => {
    if (isDone) {
      onUncheck(todo.id)
    } else {
      onCheck(todo.id)
    }
  }, [isDone, todo.id, onCheck, onUncheck])

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`rounded-md border transition-colors ${
        isDone ? 'border-border-subtle/50 bg-bg/80' : 'border-border-subtle bg-bg-elevated'
      }`}
    >
      <div className="flex items-center gap-1.5 px-1.5 py-1.5">
        <button
          type="button"
          className="p-0.5 text-fg-faint/50 hover:text-fg-muted cursor-grab active:cursor-grabbing touch-none"
          {...attributes}
          {...listeners}
        >
          <GripVertical className="w-3 h-3" />
        </button>

        <button
          type="button"
          onClick={handleToggle}
          className={`w-3.5 h-3.5 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
            isDone ? 'bg-primary border-primary' : 'border-primary hover:border-primary/80'
          }`}
        >
          {isDone && <Check className="w-2.5 h-2.5 text-white" />}
        </button>

        <span className={`flex-1 text-xs truncate ${isDone ? 'text-fg-muted line-through' : 'text-fg'}`}>
          {todo.title}
        </span>

        {todo.priority !== 'medium' && (
          <span className={`text-[9px] px-1.5 py-0.5 rounded ${PRIORITY_STYLE[todo.priority]}`}>
            {todo.priority}
          </span>
        )}
      </div>
    </div>
  )
}
