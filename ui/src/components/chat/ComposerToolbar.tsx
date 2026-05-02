import { useRef, useEffect, useState, useCallback, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { ArrowBigUp, ChevronDown, Square } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Tooltip } from '@/components/ui/tooltip'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { useModels, useProviders } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'
import type { UISlotEntry } from '@/lib/types'
import { StatusPill } from './envelopes/primitives'
import { ComposerPlusMenu } from './ComposerPlusMenu'
import { LayoutMenu } from './LayoutMenu'

// F1 (CW-20260420-0014) — Effort levels for the per-turn budget + reasoning dial.
const EFFORT_LEVELS = [
  { value: 'low',    label: 'Low',    title: 'Effort: Low — 0.5× token budget, reasoning off' },
  { value: 'normal', label: 'Norm',   title: 'Effort: Normal — 1.0× token budget (default)' },
  { value: 'high',   label: 'High',   title: 'Effort: High — 2.0× token budget, reasoning on' },
  { value: 'max',    label: 'Max',    title: 'Effort: Max — 4.0× token budget, intensive reasoning' },
] as const

type EffortValue = typeof EFFORT_LEVELS[number]['value']

const PROVIDER_ICONS: Record<string, string> = {
  anthropic: 'A',
  openai: 'O',
  ollama: 'Ol',
  gemini: 'G',
  mistral: 'M',
  'azure-openai': 'Az',
  pty: 'C',
  'pty-claude': 'C',
  'pty-codex': 'Cx',
  'pty-gemini': 'G',
  'pty-copilot': 'Cp',
  'pty-aider': 'Ai',
}

interface ComposerToolbarProps {
  hasContent: boolean
  isStreaming: boolean
  onSend: () => void
  onStop?: () => void
  onAttach: () => void
  onSlash: () => void
  onMention: () => void
  shellMode: 'ask' | 'session' | 'yolo'
  onCycleShell: () => void
  uploading?: boolean
}

export function ComposerToolbar({
  hasContent,
  isStreaming,
  onSend,
  onStop,
  onAttach,
  onSlash,
  onMention,
  shellMode,
  onCycleShell,
  uploading = false,
}: ComposerToolbarProps) {
  // Layout menu state lives here (not inside ComposerPlusMenu) so the
  // global `⌘\` keyboard shortcut — which dispatches a
  // `toggle-layout-menu` window event — can still open the menu without
  // having to first open the `+` popover.
  const [layoutOpen, setLayoutOpen] = useState(false)
  const layoutAnchorRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function onToggle() { setLayoutOpen((o) => !o) }
    window.addEventListener('toggle-layout-menu', onToggle)
    return () => window.removeEventListener('toggle-layout-menu', onToggle)
  }, [])

  const activeModel = useChatStore((s) => s.activeModel)
  const setActiveModel = useChatStore((s) => s.setActiveModel)
  // F1 (CW-20260420-0014): effort dial state from global store.
  const activeEffort = useChatStore((s) => s.activeEffort) as EffortValue
  const setActiveEffort = useChatStore((s) => s.setActiveEffort)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  // B3 (CW-20260428-0011) + F2 (CW-20260429-0002): per-session auto-switch
  // override toggle. Cycle order: inherit → off → on → inherit. "inherit"
  // defers to the global mode_auto_switch_pref; "off" suppresses all
  // auto-switches for this session even when the global pref is "always" or
  // "ask"; "on" re-enables (does NOT bypass first-use). F2 persists this on
  // sessions.auto_switch_override so it survives session reload.
  const autoSwitchOverride = useChatStore((s) =>
    activeSessionId ? s.autoSwitchSessionOverrides[activeSessionId] : undefined,
  )
  const setAutoSwitchOverride = useChatStore((s) => s.setAutoSwitchOverride)
  // F2: load session row to seed the store on first render / session swap.
  // We use the same query key the rest of the app uses so the cache is shared.
  const { data: sessionForOverride } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
  })
  const persistedOverride = sessionForOverride?.auto_switch_override ?? null
  useEffect(() => {
    if (!activeSessionId) return
    // Map persisted boolean | null → store enum.
    if (persistedOverride === true) {
      setAutoSwitchOverride(activeSessionId, 'on')
    } else if (persistedOverride === false) {
      setAutoSwitchOverride(activeSessionId, 'off')
    } else {
      setAutoSwitchOverride(activeSessionId, 'inherit')
    }
    // Only re-run when the persisted value or the active session changes —
    // setAutoSwitchOverride is stable from zustand.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSessionId, persistedOverride])
  const cycleAutoSwitch = useCallback(() => {
    if (!activeSessionId) return
    // Cycle: inherit → off → on → inherit.
    let next: 'inherit' | 'off' | 'on'
    let payload: boolean | null
    if (autoSwitchOverride === undefined) { next = 'off'; payload = false }
    else if (autoSwitchOverride === 'off') { next = 'on'; payload = true }
    else { next = 'inherit'; payload = null }
    // Optimistic local update.
    setAutoSwitchOverride(activeSessionId, next)
    // Persist to the session row. Failure logs but doesn't roll back the
    // optimistic update — the next session reload will re-seed from the row.
    void api.setSessionAutoSwitch(activeSessionId, payload).catch((err) => {
      console.error('[ComposerToolbar] failed to persist auto-switch override:', err)
    })
  }, [activeSessionId, autoSwitchOverride, setAutoSwitchOverride])
  const autoSwitchTitle =
    autoSwitchOverride === 'off'
      ? 'Auto-switch: OFF (per-session override). Click to set ON.'
      : autoSwitchOverride === 'on'
        ? 'Auto-switch: ON (per-session override). Click to clear.'
        : 'Auto-switch: inherit global pref. Click to override OFF for this session.'
  const autoSwitchClass =
    autoSwitchOverride === 'on'
      ? 'text-primary'
      : autoSwitchOverride === 'off'
        ? 'text-fg-faint line-through decoration-fg-faint/50'
        : 'text-fg-faint/60'
  const [modelOpen, setModelOpen] = useState(false)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const modelRef = useRef<HTMLDivElement>(null)
  const [dropdownPos, setDropdownPos] = useState<{ left: number; bottom: number } | null>(null)

  const { data: models } = useModels()
  const { data: providers } = useProviders()
  const pluginButtons = usePluginSlots('composer-toolbar')

  const handlePluginAction = useCallback((entry: UISlotEntry) => {
    switch (entry.action) {
      case 'command':
        if (activeSessionId && entry.props?.command) {
          void api.executeCommand(String(entry.props.command), activeSessionId, '')
        }
        break
      case 'navigate':
        if (entry.props?.hash) window.location.hash = String(entry.props.hash)
        break
      case 'handler':
        window.dispatchEvent(new CustomEvent('plugin-action', { detail: { id: entry.id, entry } }))
        break
      case 'modal':
        window.dispatchEvent(new CustomEvent('plugin-modal', { detail: entry }))
        break
    }
  }, [activeSessionId])

  const groupedModels = useMemo(() => {
    if (!models || !providers) return []
    const providerMap = new Map(providers.map((p) => [p.id, p]))
    const groups = new Map<string, { id: string; name: string; icon: string; isPty: boolean; models: { id: string; label: string; provider: string; isPty: boolean }[] }>()
    for (const m of models) {
      if (!m.is_enabled) continue
      const providerInfo = providerMap.get(m.provider_id)
      if (providerInfo && !providerInfo.is_enabled) continue
      const providerType = m.provider_type || 'anthropic'
      const isPty = providerType.startsWith('pty')
      if (!groups.has(m.provider_id)) {
        groups.set(m.provider_id, {
          id: providerType,
          name: providerInfo?.name || providerType,
          icon: PROVIDER_ICONS[providerType] || providerType.charAt(0).toUpperCase(),
          isPty,
          models: [],
        })
      }
      groups.get(m.provider_id)!.models.push({ id: m.model_id, label: m.display_name, provider: providerType, isPty })
    }
    return Array.from(groups.values())
  }, [models, providers])

  const allModels = useMemo(() => groupedModels.flatMap((g) => g.models), [groupedModels])
  const currentModel = allModels.find((m) => m.id === activeModel)

  useEffect(() => {
    if (!modelOpen) return
    function onDown(e: MouseEvent) {
      const t = e.target as Node
      if (modelRef.current && !modelRef.current.contains(t) && (!dropdownRef.current || !dropdownRef.current.contains(t))) {
        setModelOpen(false)
      }
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [modelOpen])

  const handleModelSelect = useCallback(async (modelId: string) => {
    setActiveModel(modelId)
    setModelOpen(false)
    if (activeSessionId) {
      try {
        const selected = allModels.find((m) => m.id === modelId)
        await api.updateSession(activeSessionId, { model: modelId, provider: selected?.provider || 'anthropic' } as never)
      } catch (err) {
        console.error('Failed to update model:', err)
      }
    }
  }, [activeSessionId, setActiveModel, allModels])

  const shellTitle = shellMode === 'yolo'
    ? 'Shell: YOLO — no restrictions'
    : shellMode === 'session'
      ? 'Shell: Session — auto-approve, denylist active'
      : 'Shell: Ask — confirm each command'

  const shellClass = shellMode === 'yolo'
    ? 'text-warning'
    : shellMode === 'session'
      ? 'text-primary'
      : ''

  const cycleEffort = useCallback(() => {
    const idx = EFFORT_LEVELS.findIndex((l) => l.value === activeEffort)
    const next = EFFORT_LEVELS[(idx + 1) % EFFORT_LEVELS.length]
    setActiveEffort(next.value)
  }, [activeEffort, setActiveEffort])

  const activeEffortLevel = EFFORT_LEVELS.find((l) => l.value === activeEffort)

  return (
    <div className="flex items-center justify-between border-t border-divider pt-2 pr-2.5 pb-4 pl-3">
      {/* Layout menu — rendered at the toolbar level so the global ⌘\
          keyboard shortcut can drive it independently of the +
          popover's open state. The anchor div is invisible; LayoutMenu
          centers itself via portal regardless. */}
      <div ref={layoutAnchorRef} className="hidden" />
      <LayoutMenu
        open={layoutOpen}
        onClose={() => setLayoutOpen(false)}
        anchorRef={layoutAnchorRef}
      />
      {/* ── Left: + popover, effort cycle pill, model picker ── */}
      <div className="relative flex items-center gap-2">
        <ComposerPlusMenu
          onAttach={onAttach}
          onSlash={onSlash}
          onMention={onMention}
          shellMode={shellMode}
          onCycleShell={onCycleShell}
          shellTitle={shellTitle}
          shellClass={shellClass}
          autoSwitchOverride={autoSwitchOverride}
          onCycleAutoSwitch={cycleAutoSwitch}
          autoSwitchTitle={autoSwitchTitle}
          autoSwitchClass={autoSwitchClass}
          uploading={uploading}
          layoutOpen={layoutOpen}
          onToggleLayout={() => setLayoutOpen((o) => !o)}
          pluginButtons={pluginButtons.length > 0 ? (
            <>
              <span className="mx-1 h-4 w-px bg-divider" />
              {pluginButtons.map((entry) => {
                const PluginIcon = resolveIcon(entry.icon)
                return (
                  <button
                    key={entry.id}
                    type="button"
                    onClick={() => handlePluginAction(entry)}
                    className="flex items-center gap-1.5 rounded-[4px] px-2 py-1 text-xs text-fg-muted transition-colors hover:bg-surface hover:text-fg"
                    role="menuitem"
                  >
                    <PluginIcon size={14} />
                    <span>{entry.label}</span>
                  </button>
                )
              })}
            </>
          ) : null}
        />

        <span className="h-4 w-px bg-divider" />

        {/* Effort cycle pill — uppercase, no glyph */}
        <Tooltip
          content={activeEffortLevel?.title ?? 'Effort: per-turn token budget and reasoning dial'}
          side="top"
        >
          <button
            type="button"
            onClick={cycleEffort}
            className="rounded-[6px] border border-divider px-2.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wider text-fg-secondary transition-colors hover:bg-surface"
          >
            {(activeEffortLevel?.label ?? 'Norm').toUpperCase()}
          </button>
        </Tooltip>

        <span className="h-4 w-px bg-divider" />

        {/* Model picker */}
        <div ref={modelRef}>
          <button
            ref={buttonRef}
            type="button"
            onClick={() => {
              if (!modelOpen && buttonRef.current) {
                const rect = buttonRef.current.getBoundingClientRect()
                setDropdownPos({ left: rect.left, bottom: window.innerHeight - rect.top + 4 })
              }
              setModelOpen((o) => !o)
            }}
            className="flex items-center gap-1 rounded-[6px] px-1.5 py-0.5 font-mono text-[11px] text-fg-muted transition-colors hover:bg-surface hover:text-fg"
          >
            <span className="max-w-[180px] truncate">
              {currentModel?.label || activeModel || 'Model'}
            </span>
            <ChevronDown size={10} />
          </button>

          {modelOpen && dropdownPos && createPortal(
            <div
              ref={dropdownRef}
              className="provider-scroll fixed z-[9999] max-h-80 w-64 overflow-y-auto rounded-[10px] border border-border-subtle bg-bg-elevated py-1 shadow-2xl"
              style={{ left: dropdownPos.left, bottom: dropdownPos.bottom }}
            >
              {groupedModels.length === 0 ? (
                <div className="px-3 py-2 text-xs text-fg-muted">Loading models…</div>
              ) : (
                groupedModels.map((group) => (
                  <div key={group.id}>
                    <div className="flex items-center gap-1.5 px-3 py-1.5 font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-muted">
                      <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-[3px] bg-surface text-[10px] font-bold text-fg-secondary">
                        {group.icon}
                      </span>
                      <span className="truncate">{group.name}</span>
                      <StatusPill tone={group.isPty ? 'info' : 'primary'} className="ml-auto">
                        {group.isPty ? 'PTY' : 'API'}
                      </StatusPill>
                    </div>
                    {group.models.map((model) => (
                      <button
                        key={model.id}
                        type="button"
                        onClick={() => void handleModelSelect(model.id)}
                        className={`flex w-full items-center gap-1.5 py-1.5 pl-8 pr-3 text-left text-xs transition-colors ${
                          model.id === activeModel
                            ? 'bg-surface text-fg'
                            : 'text-fg-secondary hover:bg-surface hover:text-fg'
                        }`}
                      >
                        <span className="flex-1 truncate">{model.label}</span>
                      </button>
                    ))}
                  </div>
                ))
              )}
            </div>,
            document.body,
          )}
        </div>

      </div>

      {/* ── Right: send / stop ── */}
      <div className="flex items-center gap-2">
        {isStreaming ? (
          <Tooltip content="Stop generating" side="top">
            <button
              type="button"
              onClick={onStop}
              className="flex h-7 w-7 items-center justify-center rounded-[6px] text-danger transition-colors hover:bg-surface"
            >
              <Square size={14} />
            </button>
          </Tooltip>
        ) : (
          <Tooltip content="Send (Enter)" side="top">
            <button
              type="button"
              onClick={onSend}
              disabled={!hasContent}
              className={`flex h-7 w-7 items-center justify-center rounded-[6px] border transition-colors ${
                hasContent
                  ? 'bg-brand text-brand-fg border-brand hover:bg-brand-hover hover:border-brand-hover'
                  : 'cursor-default bg-brand-muted text-brand border-brand-muted'
              }`}
            >
              <ArrowBigUp size={16} strokeWidth={2.5} />
            </button>
          </Tooltip>
        )}
      </div>
    </div>
  )
}
