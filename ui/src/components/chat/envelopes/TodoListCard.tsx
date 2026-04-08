import { Check } from 'lucide-react'
import { useToggleTodo, useTodos } from '@/hooks/useTodos'

interface TodoListCardData {
  scope: string
  scope_id: string
  title?: string
}

interface TodoListCardProps {
  data: TodoListCardData
}

export function TodoListCard({ data }: TodoListCardProps) {
  const { data: todos = [] } = useTodos({
    scope: data.scope,
    scope_id: data.scope_id,
  })
  const toggleTodo = useToggleTodo()

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/60 overflow-hidden my-2">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border/50">
        <span className="text-sm font-semibold text-fg">{data.title || 'Todos'}</span>
        <span className="text-[10px] text-fg-muted">{todos.length} items</span>
      </div>
      <div className="px-3 py-1.5 space-y-1">
        {todos.map((todo) => {
          const isDone = todo.status === 'done'
          return (
            <div key={todo.id} className="flex items-center gap-2 py-0.5">
              <button
                type="button"
                onClick={() => {
                  if (isDone) {
                    toggleTodo.uncheck(todo.id)
                  } else {
                    toggleTodo.check(todo.id)
                  }
                }}
                className={`w-3 h-3 rounded-sm border-2 flex items-center justify-center shrink-0 transition-colors ${
                  isDone ? 'bg-primary border-primary' : 'border-primary'
                }`}
              >
                {isDone && <Check className="w-2 h-2 text-white" />}
              </button>
              <span className={`text-xs ${isDone ? 'text-fg-muted line-through' : 'text-fg'}`}>
                {todo.title}
              </span>
            </div>
          )
        })}
        {todos.length === 0 && (
          <span className="text-xs text-fg-faint">No todos</span>
        )}
      </div>
    </div>
  )
}
