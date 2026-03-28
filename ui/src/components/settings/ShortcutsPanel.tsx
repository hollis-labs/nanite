import { useState, useEffect, useCallback, useRef } from 'react'
import { Pencil, RotateCcw, X, Check } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useSettings, useSettingsMutation } from '@/hooks/useSettings'
import type { UserSettings } from '@/lib/types'

const SHORTCUT_DEFS = [
  { key: 'toggle_left_sidebar', label: 'Toggle Left Sidebar', default: 'mod+b' },
  { key: 'toggle_right_rail', label: 'Toggle Right Rail', default: 'mod+/' },
  { key: 'focus_composer', label: 'Focus Composer', default: 'mod+l' },
  { key: 'new_session', label: 'New Session', default: 'mod+n' },
  { key: 'search', label: 'Search / Open Sidebar', default: 'mod+k' },
  { key: 'next_session', label: 'Next Session', default: 'mod+]' },
  { key: 'prev_session', label: 'Previous Session', default: 'mod+[' },
  { key: 'bookmark_last', label: 'Bookmark Last Message', default: 'mod+d' },
  { key: 'toggle_artifacts', label: 'Toggle Artifacts Drawer', default: 'mod+.' },
] as const

const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.userAgent)

function formatShortcut(binding: string): string {
  return binding
    .split('+')
    .map((part) => {
      if (part === 'mod') return isMac ? '\u2318' : 'Ctrl'
      if (part === 'shift') return isMac ? '\u21E7' : 'Shift'
      if (part === 'alt') return isMac ? '\u2325' : 'Alt'
      if (part === '[') return '['
      if (part === ']') return ']'
      return part.toUpperCase()
    })
    .join(isMac ? '' : '+')
}

function keyEventToBinding(e: KeyboardEvent): string | null {
  // Ignore modifier-only presses.
  if (['Meta', 'Control', 'Shift', 'Alt'].includes(e.key)) return null

  const parts: string[] = []
  if (e.metaKey || e.ctrlKey) parts.push('mod')
  if (e.shiftKey) parts.push('shift')
  if (e.altKey) parts.push('alt')

  let key = e.key.toLowerCase()
  // Normalize special keys.
  if (key === ' ') key = 'space'
  if (key === 'escape') return null // Cancel on Escape
  parts.push(key)

  return parts.join('+')
}

function ShortcutRow({
  label,
  binding,
  isEditing,
  onEdit,
  onSave,
  onCancel,
}: {
  label: string
  binding: string
  isEditing: boolean
  onEdit: () => void
  onSave: (newBinding: string) => void
  onCancel: () => void
}) {
  const [captured, setCaptured] = useState<string | null>(null)
  const cellRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!isEditing) {
      setCaptured(null)
      return
    }

    function handleKeyDown(e: KeyboardEvent) {
      e.preventDefault()
      e.stopPropagation()
      const result = keyEventToBinding(e)
      if (result === null) {
        // Escape pressed — cancel.
        onCancel()
        return
      }
      setCaptured(result)
    }

    window.addEventListener('keydown', handleKeyDown, true)
    return () => window.removeEventListener('keydown', handleKeyDown, true)
  }, [isEditing, onCancel])

  return (
    <div className="flex items-center justify-between py-2.5 group">
      <div className="text-sm text-zinc-300">{label}</div>
      <div className="flex items-center gap-2">
        {isEditing ? (
          <>
            <div
              ref={cellRef}
              className="min-w-[120px] px-3 py-1 bg-zinc-800 border border-indigo-500 rounded-md text-sm text-center"
            >
              {captured ? (
                <span className="text-zinc-100 font-mono">{formatShortcut(captured)}</span>
              ) : (
                <span className="text-zinc-500 italic">Press keys...</span>
              )}
            </div>
            {captured && (
              <Button
                variant="ghost"
                size="icon"
                className="w-6 h-6 text-green-400 hover:text-green-300 hover:bg-green-500/10"
                onClick={() => onSave(captured)}
                title="Confirm"
              >
                <Check className="w-3.5 h-3.5" />
              </Button>
            )}
            <Button
              variant="ghost"
              size="icon"
              className="w-6 h-6 text-zinc-500 hover:text-zinc-300 hover:bg-zinc-700/50"
              onClick={onCancel}
              title="Cancel"
            >
              <X className="w-3.5 h-3.5" />
            </Button>
          </>
        ) : (
          <>
            <div className="min-w-[120px] px-3 py-1 bg-zinc-900 border border-zinc-700 rounded-md text-sm text-center">
              <span className="text-zinc-200 font-mono">{formatShortcut(binding)}</span>
            </div>
            <Button
              variant="ghost"
              size="icon"
              className="w-6 h-6 text-zinc-600 opacity-0 group-hover:opacity-100 hover:text-zinc-300 hover:bg-zinc-700/50 transition-opacity"
              onClick={onEdit}
              title="Edit shortcut"
            >
              <Pencil className="w-3 h-3" />
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

export function ShortcutsPanel() {
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()
  const [editingKey, setEditingKey] = useState<string | null>(null)

  const shortcuts: Record<string, string> = (settings?.ext_settings?.shortcuts as Record<string, string>) ?? {}

  const getBinding = useCallback(
    (key: string, defaultValue: string) => shortcuts[key] ?? defaultValue,
    [shortcuts],
  )

  const handleSave = useCallback(
    (key: string, newBinding: string) => {
      const updated = { ...shortcuts, [key]: newBinding }
      mutation.mutate({
        ext_settings: {
          ...settings?.ext_settings,
          shortcuts: updated,
        },
      } as Partial<UserSettings>)
      setEditingKey(null)
    },
    [shortcuts, settings?.ext_settings, mutation],
  )

  const handleResetAll = useCallback(() => {
    const defaults: Record<string, string> = {}
    for (const def of SHORTCUT_DEFS) {
      defaults[def.key] = def.default
    }
    mutation.mutate({
      ext_settings: {
        ...settings?.ext_settings,
        shortcuts: defaults,
      },
    } as Partial<UserSettings>)
  }, [settings?.ext_settings, mutation])

  return (
    <div className="max-w-2xl">
      <div className="border-b border-zinc-800 pb-2 mb-1">
        <div className="flex items-center justify-between">
          <h3 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">
            Keyboard Shortcuts
          </h3>
          <Button
            variant="ghost"
            size="sm"
            className="text-xs text-zinc-500 hover:text-zinc-300"
            onClick={handleResetAll}
          >
            <RotateCcw className="w-3 h-3 mr-1.5" />
            Reset All
          </Button>
        </div>
      </div>

      <div className="divide-y divide-zinc-800/50">
        {SHORTCUT_DEFS.map((def) => (
          <ShortcutRow
            key={def.key}
            label={def.label}
            binding={getBinding(def.key, def.default)}
            isEditing={editingKey === def.key}
            onEdit={() => setEditingKey(def.key)}
            onSave={(newBinding) => handleSave(def.key, newBinding)}
            onCancel={() => setEditingKey(null)}
          />
        ))}
      </div>

      <p className="mt-4 text-xs text-zinc-600">
        {isMac ? '\u2318 = Command' : 'Mod = Ctrl'} &middot; Changes are saved automatically &middot; Custom shortcuts will be wired in a future update
      </p>
    </div>
  )
}
