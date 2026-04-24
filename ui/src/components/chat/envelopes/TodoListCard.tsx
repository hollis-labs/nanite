import { Check } from 'lucide-react'
import { useToggleTodo, useTodos } from '@/hooks/useTodos'
import { Envelope, EnvelopeHeader } from './primitives/Envelope'

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

  const completed = todos.filter((t) => t.status === 'done').length

  return (
    <Envelope>
      <EnvelopeHeader
        label={data.title || 'Todos'}
        meta={
          todos.length > 0 ? (
            <span className="font-mono">
              {completed}/{todos.length}
            </span>
          ) : undefined
        }
      />

      <div className="px-4 py-2.5">
        {todos.length === 0 ? (
          <span className="text-[13px] text-fg-faint">No todos</span>
        ) : (
          <ul className="space-y-0.5">
            {todos.map((todo) => {
              const isDone = todo.status === 'done'
              return (
                <li key={todo.id} className="flex items-center gap-2.5 rounded-[6px] px-2 py-1.5">
                  <button
                    type="button"
                    onClick={() => {
                      if (isDone) toggleTodo.uncheck(todo.id)
                      else toggleTodo.check(todo.id)
                    }}
                    className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-[4px] transition-colors ${
                      isDone
                        ? 'bg-success text-success-fg'
                        : 'border border-border hover:border-primary'
                    }`}
                    aria-label={isDone ? 'Mark incomplete' : 'Mark complete'}
                  >
                    {isDone && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
                  </button>
                  <span
                    className={`flex-1 text-[13px] leading-snug ${
                      isDone ? 'text-fg-muted line-through' : 'text-fg'
                    }`}
                  >
                    {todo.title}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </Envelope>
  )
}
