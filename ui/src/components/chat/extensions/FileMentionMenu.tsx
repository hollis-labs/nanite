import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useState,
  useCallback,
} from 'react'
import { File, Folder } from 'lucide-react'
import type { FileResult } from './FileMentionExtension'

interface FileMentionMenuProps {
  items: FileResult[]
  command: (item: FileResult) => void
}

export interface FileMentionMenuRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes}B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)}K`
  return `${(bytes / (1024 * 1024)).toFixed(1)}M`
}

export const FileMentionMenu = forwardRef<FileMentionMenuRef, FileMentionMenuProps>(
  ({ items, command }, ref) => {
    const [selectedIndex, setSelectedIndex] = useState(0)

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

    if (items.length === 0) {
      return (
        <div className="bg-composer border border-composer-border-focus rounded-xl shadow-xl z-50 py-2 px-3 w-80">
          <span className="text-xs text-composer-fg-muted">No matching files</span>
        </div>
      )
    }

    return (
      <div className="bg-composer border border-composer-border-focus rounded-xl shadow-xl z-50 py-1 w-80 max-h-72 overflow-y-auto">
        <div className="px-3 py-1.5 text-[10px] font-medium text-composer-fg-muted uppercase tracking-wider">
          Files
        </div>
        {items.map((item, index) => (
          <button
            key={item.path}
            onClick={() => selectItem(index)}
            onMouseEnter={() => setSelectedIndex(index)}
            className={`w-full text-left px-3 py-1.5 flex items-center gap-2 transition-colors ${
              index === selectedIndex
                ? 'bg-composer-hover text-composer-fg'
                : 'text-composer-fg-secondary hover:bg-composer-hover/60 hover:text-composer-fg'
            }`}
          >
            {item.is_dir ? (
              <Folder className="w-3.5 h-3.5 text-amber-400 shrink-0" />
            ) : (
              <File className="w-3.5 h-3.5 text-composer-fg-muted shrink-0" />
            )}
            <span className="text-sm font-mono truncate flex-1">{item.path}</span>
            {!item.is_dir && item.size > 0 && (
              <span className="text-[10px] text-composer-fg-muted shrink-0">{formatSize(item.size)}</span>
            )}
          </button>
        ))}
      </div>
    )
  }
)

FileMentionMenu.displayName = 'FileMentionMenu'
