import { useRef, useEffect, useState, useCallback, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUp, AtSign, ChevronDown, Paperclip, Slash, Square, Terminal, Unlock, Zap } from 'lucide-react'
import { Tooltip } from '@/components/ui/tooltip'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { useModels, useProviders } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'
import type { UISlotEntry } from '@/lib/types'
import { StatusPill } from './envelopes/primitives'
import { LayoutMenu, LayoutMenuTrigger } from './LayoutMenu'

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

function ToolbarBtn({ onClick, title, disabled, children, className = '' }: {
  onClick?: () => void
  title: string
  disabled?: boolean
  children: React.ReactNode
  className?: string
}) {
  return (
    <Tooltip content={title} side="top">
      <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        className={`flex h-7 w-7 items-center justify-center rounded-[6px] text-fg-muted transition-colors hover:bg-surface hover:text-fg disabled:opacity-40 disabled:cursor-default ${className}`}
      >
        {children}
      </button>
    </Tooltip>
  )
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
  const [layoutOpen, setLayoutOpen] = useState(false)
  const layoutTriggerRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    function onToggle() { setLayoutOpen((o) => !o) }
    window.addEventListener('toggle-layout-menu', onToggle)
    return () => window.removeEventListener('toggle-layout-menu', onToggle)
  }, [])

  const activeModel = useChatStore((s) => s.activeModel)
  const setActiveModel = useChatStore((s) => s.setActiveModel)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
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

  return (
    <div className="flex items-center justify-between border-t border-divider pt-2 pr-2.5 pb-4 pl-3">
      {/* ── Left: action icons ── */}
      <div className="relative flex items-center gap-0.5">
        {/* Layout menu trigger */}
        <LayoutMenuTrigger ref={layoutTriggerRef} open={layoutOpen} onClick={() => setLayoutOpen((o) => !o)} />
        <LayoutMenu open={layoutOpen} onClose={() => setLayoutOpen(false)} anchorRef={layoutTriggerRef} />

        <div className="mx-1 h-4 w-px bg-divider" />

        <ToolbarBtn onClick={onAttach} title={uploading ? 'Uploading…' : 'Attach file'} disabled={uploading}>
          <Paperclip size={14} className={uploading ? 'animate-pulse text-primary' : ''} />
        </ToolbarBtn>

        <ToolbarBtn onClick={onSlash} title="Slash commands (/)">
          <Slash size={14} />
        </ToolbarBtn>

        <ToolbarBtn onClick={onMention} title="Mention file (@)">
          <AtSign size={14} />
        </ToolbarBtn>

        <div className="mx-1 h-4 w-px bg-divider" />

        <ToolbarBtn onClick={onCycleShell} title={shellTitle} className={shellClass}>
          {shellMode === 'yolo'
            ? <Zap size={14} className="fill-current" />
            : shellMode === 'session'
              ? <Unlock size={14} />
              : <Terminal size={14} />
          }
        </ToolbarBtn>

        {/* Model picker */}
        <div className="mx-1 h-4 w-px bg-divider" />
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

        {/* Plugin toolbar slots */}
        {pluginButtons.length > 0 && (
          <>
            <div className="mx-1 h-4 w-px bg-divider" />
            {pluginButtons.map((entry) => {
              const PluginIcon = resolveIcon(entry.icon)
              return (
                <ToolbarBtn key={entry.id} onClick={() => handlePluginAction(entry)} title={entry.label}>
                  <PluginIcon size={14} />
                </ToolbarBtn>
              )
            })}
          </>
        )}
      </div>

      {/* ── Right: send hint + send/stop ── */}
      <div className="flex items-center gap-2">
        {!isStreaming && (
          <span className="font-mono text-[11px] text-fg-muted">⌘↵ to send</span>
        )}
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
              className={`flex h-7 w-7 items-center justify-center rounded-[6px] transition-colors ${
                hasContent
                  ? 'bg-primary text-primary-foreground hover:bg-primary-hover'
                  : 'cursor-default text-fg-faint'
              }`}
            >
              <ArrowUp size={15} strokeWidth={2.2} />
            </button>
          </Tooltip>
        )}
      </div>
    </div>
  )
}
