import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useState,
  useCallback,
  useRef,
} from 'react'
import { File, Folder } from 'lucide-react'
import type { FileResult } from './FileMentionExtension'

interface FileMentionMenuProps {
  items: FileResult[]
  command: (item: FileResult) => void
  query?: string
}

export interface FileMentionMenuRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)}K`
  return `${(bytes / (1024 * 1024)).toFixed(1)}M`
}

// Group files: directories first, then files. Precompute original indices to avoid O(n^2) indexOf.
function groupFiles(items: FileResult[]) {
  const dirs: { item: FileResult; idx: number }[] = []
  const files: { item: FileResult; idx: number }[] = []
  for (let i = 0; i < items.length; i++) {
    if (items[i].is_dir) dirs.push({ item: items[i], idx: i })
    else files.push({ item: items[i], idx: i })
  }
  return { dirs, files }
}

export const FileMentionMenu = forwardRef<FileMentionMenuRef, FileMentionMenuProps>(
  ({ items, command, query = '' }, ref) => {
    const [selectedIndex, setSelectedIndex] = useState(0)
    const scrollRef = useRef<HTMLDivElement>(null)
    const itemRefs = useRef<Map<number, HTMLButtonElement>>(new Map())
    const { dirs, files } = groupFiles(items)

    useEffect(() => {
      setSelectedIndex(0)
    }, [items])

    // Scroll selected item into view
    useEffect(() => {
      const el = itemRefs.current.get(selectedIndex)
      if (el && scrollRef.current) {
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

        if (event.key === 'Enter' || event.key === 'Tab') {
          selectItem(selectedIndex)
          return true
        }

        if (event.key === 'Escape') {
          return true
        }

        return false
      },
    }))

    const searchBar = (
      <div className="px-2.5 pt-2 pb-1.5">
        <div className="flex items-center gap-1.5 px-2.5 py-1.5 bg-surface/50 border border-border-subtle rounded-lg">
          <File className="w-3 h-3 text-fg-faint shrink-0" />
          <span className="text-xs text-fg-secondary font-mono flex-1 truncate">
            {query ? `@${query}` : <span className="text-fg-faint">@type to search files…</span>}
          </span>
          {items.length > 0 && (
            <span className="text-[10px] text-fg-faint shrink-0">{items.length}</span>
          )}
        </div>
      </div>
    )

    if (items.length === 0) {
      return (
        <div className="w-80 bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl z-[9999]">
          {searchBar}
          <div className="px-3 pb-2.5">
            <span className="text-xs text-fg-muted">No matching files</span>
          </div>
        </div>
      )
    }

    // We render items in original order to match the flat index used by arrow keys
    return (
      <div className="w-80 bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl z-[9999]">
        {searchBar}
        <div
          ref={scrollRef}
          className="py-1 max-h-64 overflow-y-auto provider-scroll"
        >
        {dirs.length > 0 && (
          <>
            <div className="flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-medium text-fg-muted uppercase tracking-wider">
              <span className="w-4 h-4 rounded bg-surface-hover flex items-center justify-center shrink-0">
                <Folder className="w-2.5 h-2.5 text-amber-400" />
              </span>
              Directories
            </div>
            {dirs.map(({ item, idx }) => (
                <button
                  key={item.path}
                  ref={(el) => { if (el) itemRefs.current.set(idx, el); else itemRefs.current.delete(idx) }}
                  onClick={() => selectItem(idx)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                  className={`w-full text-left px-3 pl-8 py-1.5 flex items-center gap-2 transition-colors ${
                    idx === selectedIndex
                      ? 'bg-surface-hover text-fg'
                      : 'text-fg-secondary hover:bg-surface/60 hover:text-fg'
                  }`}
                >
                  <Folder className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                  <span className="text-xs font-mono truncate flex-1">{item.path}</span>
                </button>
            ))}
          </>
        )}
        {files.length > 0 && (
          <>
            <div className="flex items-center gap-1.5 px-3 py-1.5 text-[10px] font-medium text-fg-muted uppercase tracking-wider">
              <span className="w-4 h-4 rounded bg-surface-hover flex items-center justify-center shrink-0">
                <File className="w-2.5 h-2.5 text-fg-muted" />
              </span>
              Files
            </div>
            {files.map(({ item, idx }) => (
                <button
                  key={item.path}
                  ref={(el) => { if (el) itemRefs.current.set(idx, el); else itemRefs.current.delete(idx) }}
                  onClick={() => selectItem(idx)}
                  onMouseEnter={() => setSelectedIndex(idx)}
                  className={`w-full text-left px-3 pl-8 py-1.5 flex items-center gap-2 transition-colors ${
                    idx === selectedIndex
                      ? 'bg-surface-hover text-fg'
                      : 'text-fg-secondary hover:bg-surface/60 hover:text-fg'
                  }`}
                >
                  <File className="w-3.5 h-3.5 text-fg-muted shrink-0" />
                  <span className="text-xs font-mono truncate flex-1">{item.path}</span>
                  {item.size > 0 && (
                    <span className="text-[10px] text-fg-faint shrink-0">{formatSize(item.size)}</span>
                  )}
                </button>
            ))}
          </>
        )}
        </div>
      </div>
    )
  }
)

FileMentionMenu.displayName = 'FileMentionMenu'
