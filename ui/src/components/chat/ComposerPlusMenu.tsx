import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  AtSign,
  Paperclip,
  Plus,
  Slash,
  Sparkles,
  Terminal,
  Unlock,
  Zap,
} from 'lucide-react'

import { Tooltip } from '@/components/ui/tooltip'
import { LayoutMenuTrigger } from './LayoutMenu'

/**
 * ComposerPlusMenu — Wave 4 / Task 11 of the 2026-05-01 chat-surface
 * redesign. Consolidates the previously-flat composer toolbar icon row
 * into a single `+` trigger that opens a popover containing every
 * left-side action.
 *
 * The Layout menu's open-state lives in the parent ComposerToolbar so
 * the global `⌘\` keyboard shortcut continues to work even when this
 * popover is closed. We render the LayoutMenuTrigger here and bubble
 * its click through `onToggleLayout` to the parent.
 */

export interface ComposerPlusMenuProps {
  onAttach: () => void
  onSlash: () => void
  onMention: () => void
  shellMode: 'ask' | 'session' | 'yolo'
  onCycleShell: () => void
  shellTitle: string
  shellClass: string
  autoSwitchOverride: 'inherit' | 'off' | 'on' | undefined
  onCycleAutoSwitch: () => void
  autoSwitchTitle: string
  autoSwitchClass: string
  uploading: boolean
  layoutOpen: boolean
  onToggleLayout: () => void
  pluginButtons: React.ReactNode
}

export function ComposerPlusMenu({
  onAttach,
  onSlash,
  onMention,
  shellMode,
  onCycleShell,
  shellTitle,
  shellClass,
  autoSwitchOverride,
  onCycleAutoSwitch,
  autoSwitchTitle,
  autoSwitchClass,
  uploading,
  layoutOpen,
  onToggleLayout,
  pluginButtons,
}: ComposerPlusMenuProps) {
  const [open, setOpen] = useState(false)
  const [popoverPos, setPopoverPos] = useState<{ left: number; bottom: number } | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const layoutTriggerRef = useRef<HTMLButtonElement>(null)

  // Compute fixed-position coordinates for the portaled popover so the
  // composer chrome's overflow-hidden doesn't clip it. Run on open and
  // recompute on viewport resize/scroll while open.
  useEffect(() => {
    if (!open) return
    function compute() {
      const r = triggerRef.current?.getBoundingClientRect()
      if (!r) return
      // Anchor: above the trigger (bottom = viewport height - trigger top).
      // 8px gap matches the previous `mb-2`.
      setPopoverPos({ left: r.left, bottom: window.innerHeight - r.top + 8 })
    }
    compute()
    window.addEventListener('resize', compute)
    window.addEventListener('scroll', compute, true)
    return () => {
      window.removeEventListener('resize', compute)
      window.removeEventListener('scroll', compute, true)
    }
  }, [open])

  // Click-outside dismiss for the popover itself. When the LayoutMenu
  // portal opens on top, its own click-outside handler closes itself;
  // we should NOT also close the + popover in that case.
  useEffect(() => {
    if (!open) return
    function onDown(e: MouseEvent) {
      if (
        triggerRef.current?.contains(e.target as Node) ||
        popoverRef.current?.contains(e.target as Node)
      ) {
        return
      }
      if (layoutOpen) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open, layoutOpen])

  // ESC closes the popover.
  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])

  const shellLabel =
    shellMode === 'yolo' ? 'YOLO' : shellMode === 'session' ? 'Session' : 'Ask'
  const ShellIcon =
    shellMode === 'yolo' ? Zap : shellMode === 'session' ? Unlock : Terminal

  const autoSwitchLabel =
    autoSwitchOverride === 'on'
      ? 'On'
      : autoSwitchOverride === 'off'
        ? 'Off'
        : 'Inherit'

  return (
    <div className="relative">
      <Tooltip content="More actions" side="top">
        <button
          ref={triggerRef}
          type="button"
          onClick={() => setOpen((o) => !o)}
          className={`flex h-7 w-7 items-center justify-center rounded-[6px] transition-colors ${
            open
              ? 'bg-primary-muted text-primary'
              : 'text-fg-muted hover:bg-surface hover:text-fg'
          }`}
          aria-label="More actions"
          aria-expanded={open}
        >
          <Plus size={14} strokeWidth={2.5} />
        </button>
      </Tooltip>

      {open && popoverPos && createPortal(
        <div
          ref={popoverRef}
          className="fixed z-[9999] flex flex-wrap items-center gap-1 rounded-[8px] border border-fg-secondary/30 bg-fg p-1.5 shadow-2xl min-w-[300px]"
          style={{ left: popoverPos.left, bottom: popoverPos.bottom }}
          role="menu"
        >
          {/* Layout trigger — opens the existing centered LayoutMenu modal.
              The LayoutMenu itself is rendered by the parent
              ComposerToolbar so `⌘\` can drive it independently. */}
          <LayoutMenuTrigger
            ref={layoutTriggerRef}
            open={layoutOpen}
            onClick={() => {
              onToggleLayout()
              setOpen(false)
            }}
          />

          <span className="mx-1 h-4 w-px bg-bg-elevated/20" />

          <button
            type="button"
            onClick={() => {
              onAttach()
              setOpen(false)
            }}
            disabled={uploading}
            className="flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs text-bg-elevated transition-colors hover:bg-fg-secondary hover:text-bg disabled:cursor-default disabled:opacity-40"
            role="menuitem"
          >
            <Paperclip size={14} className={uploading ? 'animate-pulse' : ''} />
            {uploading ? 'Uploading…' : 'Attach'}
          </button>

          <button
            type="button"
            onClick={() => {
              onSlash()
              setOpen(false)
            }}
            className="flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs text-bg-elevated transition-colors hover:bg-fg-secondary hover:text-bg"
            role="menuitem"
          >
            <Slash size={14} />
            Slash
          </button>

          <button
            type="button"
            onClick={() => {
              onMention()
              setOpen(false)
            }}
            className="flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs text-bg-elevated transition-colors hover:bg-fg-secondary hover:text-bg"
            role="menuitem"
          >
            <AtSign size={14} />
            Mention
          </button>

          <span className="mx-1 h-4 w-px bg-bg-elevated/20" />

          <button
            type="button"
            onClick={() => {
              onCycleShell()
              setOpen(false)
            }}
            title={shellTitle}
            className={`flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs transition-colors hover:bg-fg-secondary hover:text-bg ${shellClass || 'text-bg-elevated'}`}
            role="menuitem"
          >
            <ShellIcon size={14} className={shellMode === 'yolo' ? 'fill-current' : ''} />
            Shell: {shellLabel}
          </button>

          <button
            type="button"
            onClick={() => {
              onCycleAutoSwitch()
              setOpen(false)
            }}
            title={autoSwitchTitle}
            className={`flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs transition-colors hover:bg-fg-secondary hover:text-bg ${autoSwitchClass || 'text-bg-elevated'}`}
            role="menuitem"
          >
            <Sparkles size={14} />
            Auto-switch: {autoSwitchLabel}
          </button>

          {pluginButtons}
        </div>,
        document.body,
      )}
    </div>
  )
}
