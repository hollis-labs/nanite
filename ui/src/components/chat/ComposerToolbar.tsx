import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { createPortal } from 'react-dom'
import { Bot, ChevronDown, SendHorizonal, Square } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip } from '@/components/ui/tooltip'
import { UserProfileMenu } from './UserProfileMenu'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { useModels, useProviders } from '@/hooks/useSettings'
import { usePluginSlots } from '@/hooks/usePluginSlots'
import { resolveIcon } from '@/lib/icons'
import { api } from '@/lib/api'
import type { UISlotEntry } from '@/lib/types'

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
}

export function ComposerToolbar({ hasContent, isStreaming, onSend, onStop }: ComposerToolbarProps) {
  const activeModel = useChatStore((s) => s.activeModel)
  const setActiveModel = useChatStore((s) => s.setActiveModel)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const [modelOpen, setModelOpen] = useState(false)
  const modelRef = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const [dropdownPos, setDropdownPos] = useState<{ left: number; bottom: number } | null>(null)

  const { data: models } = useModels()
  const { data: providers } = useProviders()

  const handlePluginAction = useCallback((entry: UISlotEntry) => {
    switch (entry.action) {
      case 'command':
        if (activeSessionId && entry.props?.command) {
          void api.executeCommand(String(entry.props.command), activeSessionId, '')
        }
        break
      case 'navigate':
        if (entry.props?.hash) {
          window.location.hash = String(entry.props.hash)
        }
        break
      case 'handler':
        window.dispatchEvent(new CustomEvent('plugin-action', { detail: { id: entry.id, entry } }))
        break
      case 'modal':
        window.dispatchEvent(new CustomEvent('plugin-modal', { detail: entry }))
        break
    }
  }, [activeSessionId])

  // Group models by provider for the dropdown.
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
      groups.get(m.provider_id)!.models.push({
        id: m.model_id,
        label: m.display_name,
        provider: providerType,
        isPty,
      })
    }

    return Array.from(groups.values())
  }, [models, providers])

  const allModels = useMemo(() => groupedModels.flatMap((g) => g.models), [groupedModels])
  const currentModel = allModels.find((m) => m.id === activeModel)

  const dropdownRef = useRef<HTMLDivElement>(null)

  // Close dropdown on outside click — checks both the button container and the portal
  useEffect(() => {
    if (!modelOpen) return
    function handleClickOutside(e: MouseEvent) {
      const target = e.target as Node
      if (
        modelRef.current && !modelRef.current.contains(target) &&
        (!dropdownRef.current || !dropdownRef.current.contains(target))
      ) {
        setModelOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [modelOpen])

  const handleModelSelect = useCallback(async (modelId: string) => {
    setActiveModel(modelId)
    setModelOpen(false)
    if (activeSessionId) {
      try {
        const selected = allModels.find((m) => m.id === modelId)
        const providerType = selected?.provider || 'anthropic'
        await api.updateSession(activeSessionId, { model: modelId, provider: providerType } as never)
      } catch (err) {
        console.error('Failed to update model:', err)
      }
    }
  }, [activeSessionId, setActiveModel, allModels])

  const pluginButtons = usePluginSlots('composer-toolbar')

  return (
    <div className="flex items-center justify-between px-3 py-1.5 bg-composer-bar border-t border-composer-border">
      {/* Left: User profile + Model picker */}
      <div className="flex items-center gap-1.5">
      <UserProfileMenu />
      <div ref={modelRef}>
        <button
          ref={buttonRef}
          onClick={() => {
            if (!modelOpen && buttonRef.current) {
              const rect = buttonRef.current.getBoundingClientRect()
              setDropdownPos({ left: rect.left, bottom: window.innerHeight - rect.top + 4 })
            }
            setModelOpen((o) => !o)
          }}
          className="flex items-center gap-1 text-xs text-composer-fg-secondary hover:text-composer-fg transition-colors py-0.5 px-1.5 rounded-md hover:bg-composer-hover"
        >
          <Bot className="w-3 h-3" />
          <span className="max-w-[200px] truncate">{currentModel?.label || activeModel || 'Select model'}</span>
          <ChevronDown className="w-3 h-3" />
        </button>

        {modelOpen && dropdownPos && createPortal(
          <div
            ref={dropdownRef}
            className="fixed w-64 bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl z-[9999] py-1 max-h-80 overflow-y-auto provider-scroll"
            style={{ left: dropdownPos.left, bottom: dropdownPos.bottom }}
          >
            {groupedModels.length === 0 ? (
              <div className="px-3 py-2 text-xs text-fg-muted">Loading models...</div>
            ) : (
              groupedModels.map((group) => (
                <div key={group.id}>
                  <div className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-fg-muted uppercase tracking-wider">
                    <span className="w-4 h-4 rounded bg-surface-hover flex items-center justify-center text-[10px] font-bold text-fg-secondary shrink-0">
                      {group.icon}
                    </span>
                    {group.name}
                    <span className={`ml-auto text-[9px] px-1 py-px rounded font-medium leading-none ${
                      group.isPty
                        ? 'bg-cyan-500/15 text-cyan-400 border border-cyan-500/20'
                        : 'bg-info/15 text-info border border-info/30'
                    }`}>
                      {group.isPty ? 'PTY' : 'API'}
                    </span>
                  </div>
                  {group.models.map((model) => (
                    <button
                      key={model.id}
                      onClick={() => void handleModelSelect(model.id)}
                      className={`w-full text-left px-3 pl-8 py-1.5 text-xs flex items-center gap-1.5 transition-colors ${
                        model.id === activeModel
                          ? 'bg-surface-hover text-fg'
                          : 'text-fg-secondary hover:bg-surface/60 hover:text-fg'
                      }`}
                    >
                      <span className="truncate flex-1">{model.label}</span>
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

      {/* Center: Plugin-registered toolbar items */}
      {pluginButtons.length > 0 && (
        <div className="flex items-center gap-0.5">
          {pluginButtons.map((entry) => {
            const PluginIcon = resolveIcon(entry.icon)
            return (
              <Tooltip key={entry.id} content={entry.label} side="top">
                <button
                  type="button"
                  className="p-1 rounded text-composer-fg-muted hover:text-composer-fg hover:bg-composer-hover transition-colors"
                  onClick={() => handlePluginAction(entry)}
                >
                  <PluginIcon className="w-3.5 h-3.5" />
                </button>
              </Tooltip>
            )
          })}
        </div>
      )}

      {/* Right: Send/Stop */}
      <div className="flex items-center gap-1">
        {isStreaming ? (
          <Tooltip content="Stop generating" side="top">
            <Button
              variant="ghost"
              size="icon"
              className="w-7 h-7 text-danger hover:text-danger hover:bg-danger/10 transition-colors"
              onClick={onStop}
            >
              <Square className="w-3.5 h-3.5" />
            </Button>
          </Tooltip>
        ) : (
          <Tooltip content="Send (Enter)" side="top">
            <Button
              variant="ghost"
              size="icon"
              className={`w-7 h-7 transition-colors ${
                hasContent
                  ? 'text-primary hover:text-primary-hover hover:bg-primary-hover/10'
                  : 'text-composer-fg-muted'
              }`}
              disabled={!hasContent}
              onClick={onSend}
            >
              <SendHorizonal className="w-3.5 h-3.5" />
            </Button>
          </Tooltip>
        )}
      </div>
    </div>
  )
}
