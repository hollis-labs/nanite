import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useState,
  useCallback,
  useMemo,
} from 'react'
import type { SlashCommand } from './SlashCommandExtension'

interface SlashCommandMenuProps {
  items: SlashCommand[]
  command: (item: SlashCommand) => void
}

export interface SlashCommandMenuRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean
}

// Group commands by category for display
function groupByCategory(items: SlashCommand[]) {
  const groups: { category: string; commands: SlashCommand[] }[] = []
  const seen = new Map<string, SlashCommand[]>()

  for (const item of items) {
    const cat = item.category || 'other'
    if (!seen.has(cat)) {
      const cmds: SlashCommand[] = []
      seen.set(cat, cmds)
      groups.push({ category: cat, commands: cmds })
    }
    seen.get(cat)!.push(item)
  }
  return groups
}

export const SlashCommandMenu = forwardRef<SlashCommandMenuRef, SlashCommandMenuProps>(
  ({ items, command }, ref) => {
    const [selectedIndex, setSelectedIndex] = useState(0)
    const groups = useMemo(() => groupByCategory(items), [items])

    useEffect(() => {
      setSelectedIndex(0)
    }, [items])

    const selectItem = useCallback(
      (index: number) => {
        const item = items[index]
        if (item) {
          command(item)
        }
      },
      [items, command]
    )

    useImperativeHandle(ref, () => ({
      onKeyDown: ({ event }: { event: KeyboardEvent }) => {
        if (event.key === 'ArrowUp') {
          setSelectedIndex((prev) => (prev + items.length - 1) % items.length)
          return true
        }

        if (event.key === 'ArrowDown') {
          setSelectedIndex((prev) => (prev + 1) % items.length)
          return true
        }

        if (event.key === 'Enter') {
          selectItem(selectedIndex)
          return true
        }

        if (event.key === 'Escape') {
          return true
        }

        return false
      },
    }))

    if (items.length === 0) {
      return (
        <div className="bg-composer border border-composer-border-focus rounded-xl shadow-xl z-50 py-2 px-3 w-64">
          <span className="text-xs text-composer-fg-muted">No matching commands</span>
        </div>
      )
    }

    let flatIndex = -1

    return (
      <div className="bg-composer border border-composer-border-focus rounded-xl shadow-xl z-50 py-1 w-72 max-h-72 overflow-y-auto">
        {groups.map((group) => (
          <div key={group.category}>
            <div className="px-3 py-1.5 text-[10px] font-medium text-composer-fg-muted uppercase tracking-wider">
              {group.category}
            </div>
            {group.commands.map((item) => {
              flatIndex++
              const idx = flatIndex
              return (
                <button
                  key={item.name}
                  onClick={() => selectItem(idx)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                  className={`w-full text-left px-3 py-1.5 flex items-center gap-2.5 transition-colors ${
                    idx === selectedIndex
                      ? 'bg-composer-hover text-composer-fg'
                      : 'text-composer-fg-secondary hover:bg-composer-hover/60 hover:text-composer-fg'
                  }`}
                >
                  <span className="text-sm font-mono text-accent shrink-0 w-20 truncate">/{item.name}</span>
                  <span className="text-xs text-composer-fg-muted truncate">{item.description}</span>
                  {item.source === 'plugin' && (
                    <span className="text-[9px] text-composer-fg-muted bg-composer-hover rounded px-1 py-0.5 shrink-0 ml-auto">plugin</span>
                  )}
                </button>
              )
            })}
          </div>
        ))}
      </div>
    )
  }
)

SlashCommandMenu.displayName = 'SlashCommandMenu'
