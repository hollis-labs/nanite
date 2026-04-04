import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useState,
  useCallback,
  useMemo,
  useRef,
} from 'react'
import { Terminal } from 'lucide-react'
import type { SlashCommand, SlashCommandArg } from './SlashCommandExtension'

interface SlashCommandMenuProps {
  items: SlashCommand[]
  command: (item: SlashCommand) => void
}

export interface SlashCommandMenuRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean
}

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

function ArgHints({ args }: { args: SlashCommandArg[] }) {
  return (
    <span className="flex items-center gap-1 ml-1">
      {args.map((arg) => (
        <span
          key={arg.name}
          className={`text-[10px] font-mono px-1 py-0.5 rounded leading-none ${
            arg.required
              ? 'text-info/80 bg-info/15 border border-info/30'
              : 'text-fg-faint bg-surface/50'
          }`}
          title={arg.description || `${arg.type ?? 'string'}${arg.options ? `: ${arg.options.join('|')}` : ''}`}
        >
          {arg.required ? arg.name : `[${arg.name}]`}
        </span>
      ))}
    </span>
  )
}

export const SlashCommandMenu = forwardRef<SlashCommandMenuRef, SlashCommandMenuProps>(
  ({ items, command }, ref) => {
    const [selectedIndex, setSelectedIndex] = useState(0)
    const groups = useMemo(() => groupByCategory(items), [items])
    const scrollRef = useRef<HTMLDivElement>(null)
    const itemRefs = useRef<Map<number, HTMLButtonElement>>(new Map())

    useEffect(() => {
      setSelectedIndex(0)
    }, [items])

    useEffect(() => {
      const el = itemRefs.current.get(selectedIndex)
      if (el) {
        el.scrollIntoView({ block: 'nearest' })
      }
    }, [selectedIndex])

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
        <div className="w-80 bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl z-[9999] py-2 px-3">
          <span className="text-xs text-fg-muted">No matching commands</span>
        </div>
      )
    }

    let flatIndex = -1

    return (
      <div className="w-80 bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl z-[9999]">
        <div
          ref={scrollRef}
          className="py-1 max-h-80 overflow-y-auto provider-scroll"
        >
          {groups.map((group) => (
            <div key={group.category}>
              <div className="flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-medium text-fg-muted uppercase tracking-wider">
                <span className="w-4 h-4 rounded bg-surface-hover flex items-center justify-center shrink-0">
                  <Terminal className="w-2.5 h-2.5 text-info" />
                </span>
                {group.category}
              </div>
              {group.commands.map((item) => {
                flatIndex++
                const idx = flatIndex
                return (
                  <button
                    key={item.name}
                    ref={(el) => { if (el) itemRefs.current.set(idx, el); else itemRefs.current.delete(idx) }}
                    onClick={() => selectItem(idx)}
                    onMouseEnter={() => setSelectedIndex(idx)}
                    className={`w-full text-left px-3 pl-8 py-1.5 transition-colors ${
                      idx === selectedIndex
                        ? 'bg-surface-hover text-fg'
                        : 'text-fg-secondary hover:bg-surface/60 hover:text-fg'
                    }`}
                  >
                    <div className="flex items-center gap-2">
                      <span className="text-[10px] font-mono font-semibold text-white bg-blue-500 rounded px-1 py-0.5 leading-none shrink-0">
                        /{item.name}
                      </span>
                      {item.args && item.args.length > 0 && <ArgHints args={item.args} />}
                      {item.source !== 'builtin' && (
                        <span className={`text-[9px] px-1 py-px rounded font-medium leading-none ml-auto shrink-0 ${
                          item.source.startsWith('custom-action:')
                            ? 'bg-warning/15 text-warning border border-warning/20'
                            : 'bg-info/15 text-info border border-info/30'
                        }`}>
                          {item.source.startsWith('custom-action:') ? 'action' : 'plugin'}
                        </span>
                      )}
                    </div>
                    <span className="text-xs font-mono text-fg-secondary mt-0.5 truncate block">{item.description}</span>
                  </button>
                )
              })}
            </div>
          ))}
        </div>
      </div>
    )
  }
)

SlashCommandMenu.displayName = 'SlashCommandMenu'
