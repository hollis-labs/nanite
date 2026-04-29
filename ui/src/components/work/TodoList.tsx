import { useCallback } from 'react'
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import type { Todo } from '@/lib/types'
import { TodoItem } from './TodoItem'
import { useWorkStore } from '@/stores/useWorkStore'

interface TodoListProps {
  todos: Todo[]
  onCheck: (id: string) => void
  onUncheck: (id: string, reason?: string) => void
  onReorder: (activeId: string, overId: string) => void
  scopeActions?: {
    activeProjectId: string | null
    activeSessionId: string | null
    onPromote: (id: string, projectId: string) => void
    onDemote: (id: string, sessionId: string) => void
  }
  /** When true, renders the scope chip on each todo row (used in the All filter). */
  showScope?: boolean
}

export function TodoList({ todos, onCheck, onUncheck, onReorder, scopeActions, showScope }: TodoListProps) {
  const recordChange = useWorkStore((s) => s.recordChange)
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event
      if (over && active.id !== over.id) {
        onReorder(String(active.id), String(over.id))
        recordChange({ type: 'todo_reordered', id: String(active.id) })
      }
    },
    [onReorder, recordChange],
  )

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
      <SortableContext items={todos.map((t) => t.id)} strategy={verticalListSortingStrategy}>
        <div className="space-y-1 group">
          {todos.map((todo) => (
            <TodoItem
              key={todo.id}
              todo={todo}
              onCheck={onCheck}
              onUncheck={onUncheck}
              scopeActions={scopeActions}
              showScope={showScope}
            />
          ))}
        </div>
      </SortableContext>
    </DndContext>
  )
}
