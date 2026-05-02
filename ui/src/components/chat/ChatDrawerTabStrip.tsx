import { ChevronLeft, ChevronRight, X, Pin, PinOff } from 'lucide-react'
import { useEffect, useLayoutEffect, useRef, useState } from 'react'

export interface ChatDrawerTab {
  id: string
  label: string
  /** True for the currently selected tab. */
  active: boolean
  /** Optional pulsing-pip indicator (e.g. Tools tab during a stream). */
  runningPip?: boolean
  /** Closeable tabs render an `×` on hover. Used for dynamic card-tabs. */
  closeable?: boolean
  /** Pinnable tabs render a Pin / PinOff toggle on hover. */
  pinnable?: boolean
  pinned?: boolean
}

interface Props {
  tabs: ChatDrawerTab[]
  /** Where the strip docks: 'top' (default) or 'bottom' (used by
   *  ChatPrimaryDrawer where the strip is the file-folder handle). */
  dock?: 'top' | 'bottom'
  /** Visual variant.
   *  - 'card' (default) renders the file-folder-tab look — used when the
   *    strip lives outside a drawer body and acts as the drawer handle
   *    (ChatPrimaryDrawer).
   *  - 'inline' renders simpler horizontal pill tabs — used when the strip
   *    lives INSIDE a drawer body alongside the active tab's content
   *    (ChatWorkingDrawer). */
  variant?: 'card' | 'inline'
  onSelect: (id: string) => void
  onClose?: (id: string) => void
  onTogglePin?: (id: string) => void
}

const TAB_LABEL_MAX = 14

function truncate(s: string): string {
  return s.length > TAB_LABEL_MAX ? s.slice(0, TAB_LABEL_MAX - 1) + '…' : s
}

export function ChatDrawerTabStrip({
  tabs,
  dock = 'top',
  variant = 'card',
  onSelect,
  onClose,
  onTogglePin,
}: Props) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [overflow, setOverflow] = useState<{ left: boolean; right: boolean }>({
    left: false,
    right: false,
  })

  const measure = () => {
    const el = scrollRef.current
    if (!el) return
    const left = el.scrollLeft > 0
    const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setOverflow({ left, right })
  }

  useLayoutEffect(measure, [tabs.length])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const handler = () => measure()
    el.addEventListener('scroll', handler, { passive: true })
    window.addEventListener('resize', handler)
    return () => {
      el.removeEventListener('scroll', handler)
      window.removeEventListener('resize', handler)
    }
  }, [])

  const paginate = (dir: -1 | 1) => {
    const el = scrollRef.current
    if (!el) return
    el.scrollBy({ left: dir * (el.clientWidth * 0.8), behavior: 'smooth' })
  }

  return (
    <div className={`flex items-center gap-1 px-1 ${
      variant === 'inline'
        ? 'border-b border-border-subtle bg-surface/40'
        : dock === 'bottom' ? 'border-t-2 border-t-fg' : 'border-b-2 border-b-fg'
    }`}>
      {overflow.left && (
        <button
          type="button"
          onClick={() => paginate(-1)}
          className="flex h-7 w-6 items-center justify-center rounded-[4px] text-fg-muted hover:bg-surface hover:text-fg"
          aria-label="Scroll tabs left"
        >
          <ChevronLeft size={14} />
        </button>
      )}
      <div
        ref={scrollRef}
        className="flex flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {tabs.map((t) => (
          <div
            key={t.id}
            className={`group relative shrink-0 flex items-center gap-1 px-2.5 py-1 font-mono text-[11px] tracking-wide transition-colors ${
              variant === 'inline'
                ? t.active
                  ? 'bg-bg-elevated text-fg rounded-[4px] shadow-sm'
                  : 'text-fg-muted hover:bg-bg-elevated/60 hover:text-fg-secondary rounded-[4px]'
                : t.active
                  ? 'bg-bg-elevated text-fg rounded-t-[6px] border-x border-border-subtle shadow-[inset_0_2px_0_0_var(--color-fg)] -mb-[2px] z-10'
                  : 'text-fg-muted hover:bg-surface hover:text-fg-secondary rounded-[6px]'
            }`}
          >
            <button
              type="button"
              onClick={() => onSelect(t.id)}
              title={t.label}
              className="flex items-center gap-1.5 outline-none"
            >
              <span>{truncate(t.label)}</span>
              {t.runningPip && (
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
              )}
            </button>
            {t.pinnable && onTogglePin && (
              <button
                type="button"
                onClick={() => onTogglePin(t.id)}
                className="opacity-0 group-hover:opacity-100 transition-opacity"
                aria-label={t.pinned ? 'Unpin tab' : 'Pin tab'}
              >
                {t.pinned ? <PinOff size={10} /> : <Pin size={10} />}
              </button>
            )}
            {t.closeable && onClose && (
              <button
                type="button"
                onClick={() => onClose(t.id)}
                className="opacity-0 group-hover:opacity-100 transition-opacity"
                aria-label="Close tab"
              >
                <X size={10} />
              </button>
            )}
          </div>
        ))}
      </div>
      {overflow.right && (
        <button
          type="button"
          onClick={() => paginate(1)}
          className="flex h-7 w-6 items-center justify-center rounded-[4px] text-fg-muted hover:bg-surface hover:text-fg"
          aria-label="Scroll tabs right"
        >
          <ChevronRight size={14} />
        </button>
      )}
    </div>
  )
}
