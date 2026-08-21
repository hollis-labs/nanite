import { Check, ChevronRight, List } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useToggleTodo, useTodos } from '@/hooks/useTodos'
import { Envelope, EnvelopeBody, EnvelopeHeader } from './Envelope'

interface ListItem {
  label: string
  description?: string
  icon?: string
  /** Stable item identifier (e.g. a todo id) — required for `status` items to be toggleable. */
  id?: string
  /** Checkbox/done-state. Renders a checkbox instead of a bullet/number when present. */
  status?: 'pending' | 'done'
  action?: { label: string; type: string }
}

interface ListCardDataSource {
  kind: 'todos'
  scope: string
  scope_id: string
}

interface ListCardData {
  title?: string
  items: ListItem[]
  ordered?: boolean
  /**
   * Optional live-data-source pointer. When present, this card re-fetches
   * items at render time instead of trusting the static `items` snapshot
   * above — the composition target for the retired standalone `todo-list`
   * envelope type. See TASKS/phase-6/01-rebuild-todo-list-as-composition.md.
   */
  data_source?: ListCardDataSource
}

interface ListCardProps {
  data: ListCardData
  onSendMessage?: (content: string) => void
}

/**
 * ListCard — the shared `list-card` primitive.
 *
 * Renders a static list from `data.items` by default. When
 * `data.data_source.kind === "todos"` it instead defers to `LiveTodoList`,
 * which re-fetches its own data (`useTodos`) and wires per-item toggling
 * (`useToggleTodo`) — the same live-refetch behavior the old standalone
 * `todo-list` envelope's `TodoListCard` component had, now reached through
 * `list-card` + a `data_source` pointer instead of its own manifest entry.
 */
export function ListCard({ data, onSendMessage }: ListCardProps) {
  if (data.data_source?.kind === 'todos') {
    return <LiveTodoList source={data.data_source} title={data.title} />
  }

  // `items` is required by the type, but a partially-loaded envelope can
  // arrive without it — normalize so the card degrades gracefully.
  const items = data.items ?? []
  return (
    <Envelope>
      <EnvelopeHeader
        icon={List}
        label={data.ordered ? 'Ordered list' : 'List'}
        meta={`${items.length} item${items.length === 1 ? '' : 's'}`}
      />
      <EnvelopeBody title={data.title}>
        <div className="space-y-2">
          {items.map((item, i) => (
            <ListRow
              key={item.id ?? `item-${i}`}
              item={item}
              index={i}
              ordered={data.ordered}
              onSendMessage={onSendMessage}
            />
          ))}
        </div>
      </EnvelopeBody>
    </Envelope>
  )
}

/**
 * ListRow — one item. Renders a checkbox (and strikes through the label
 * when done) for items carrying `status`; falls back to a bullet/number for
 * plain items. `onToggleStatus` is only wired by `LiveTodoList` below — a
 * static list with a `status` field but no live source renders the
 * checkbox read-only.
 */
function ListRow({
  item,
  index,
  ordered,
  onSendMessage,
  onToggleStatus,
}: {
  item: ListItem
  index: number
  ordered?: boolean
  onSendMessage?: (content: string) => void
  onToggleStatus?: (item: ListItem) => void
}) {
  const isDone = item.status === 'done'
  return (
    <div className="flex items-start gap-3">
      {item.status ? (
        <button
          type="button"
          disabled={!onToggleStatus}
          onClick={() => onToggleStatus?.(item)}
          className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-[4px] transition-colors ${
            isDone ? 'bg-success text-success-fg' : 'border border-border hover:border-primary'
          } ${onToggleStatus ? '' : 'cursor-default'}`}
          aria-label={isDone ? 'Mark incomplete' : 'Mark complete'}
        >
          {isDone && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
        </button>
      ) : (
        <span className="mt-0.5 w-5 shrink-0 text-right font-mono text-[11px] text-fg-muted">
          {ordered ? `${index + 1}.` : '•'}
        </span>
      )}
      <div className="min-w-0 flex-1">
        <span className={`text-[13px] ${isDone ? 'text-fg-muted line-through' : 'text-fg'}`}>
          {item.label}
        </span>
        {item.description && (
          <p className="mt-0.5 text-[12px] text-fg-muted">{item.description}</p>
        )}
      </div>
      {item.action && onSendMessage && (
        <Button
          size="sm"
          variant="ghost"
          className="h-6 shrink-0 px-2 text-xs"
          onClick={() => onSendMessage(item.action!.type)}
        >
          {item.action.label}
          <ChevronRight className="ml-1 h-3 w-3" />
        </Button>
      )}
    </div>
  )
}

/**
 * LiveTodoList — the live-refetching todo-list variant of `list-card`.
 *
 * Not a separate envelope type: `ListCard` renders this internally when
 * `data.data_source.kind === "todos"`. Preserves the exact live-fetch and
 * toggle behavior the old standalone `todo-list` envelope's `TodoListCard`
 * component had (`useTodos` / `useToggleTodo`) — the envelope itself is
 * still just a pointer (scope/scope_id/title), not a data payload.
 */
function LiveTodoList({ source, title }: { source: ListCardDataSource; title?: string }) {
  const { data: todos = [] } = useTodos({ scope: source.scope, scope_id: source.scope_id })
  const toggleTodo = useToggleTodo()
  const completed = todos.filter((t) => t.status === 'done').length

  return (
    <Envelope>
      <EnvelopeHeader
        label={title || 'Todos'}
        meta={
          todos.length > 0 ? (
            <span className="font-mono">
              {completed}/{todos.length}
            </span>
          ) : undefined
        }
      />
      <EnvelopeBody>
        {todos.length === 0 ? (
          <span className="text-[13px] text-fg-faint">No todos</span>
        ) : (
          <div className="space-y-0.5">
            {todos.map((todo) => (
              <ListRow
                key={todo.id}
                item={{
                  id: todo.id,
                  label: todo.title,
                  status: todo.status === 'done' ? 'done' : 'pending',
                }}
                index={0}
                onToggleStatus={(toggled) => {
                  if (toggled.status === 'done') toggleTodo.uncheck(toggled.id!)
                  else toggleTodo.check(toggled.id!)
                }}
              />
            ))}
          </div>
        )}
      </EnvelopeBody>
    </Envelope>
  )
}
