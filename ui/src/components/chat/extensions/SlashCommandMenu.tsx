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
        <div className="bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-2 px-3 w-64">
          <span className="text-xs text-zinc-500">No matching commands</span>
        </div>
      )
    }

    let flatIndex = -1

    return (
      <div className="bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1 w-72 max-h-72 overflow-y-auto">
        {groups.map((group) => (
          <div key={group.category}>
            <div className="px-3 py-1.5 text-[10px] font-medium text-zinc-500 uppercase tracking-wider">
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
                      ? 'bg-zinc-800 text-zinc-100'
                      : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                  }`}
                >
                  <span className="text-sm font-mono text-indigo-400 shrink-0 w-20 truncate">/{item.name}</span>
                  <span className="text-xs text-zinc-500 truncate">{item.description}</span>
                  {item.source === 'plugin' && (
                    <span className="text-[9px] text-zinc-600 bg-zinc-800 rounded px-1 py-0.5 shrink-0 ml-auto">plugin</span>
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
