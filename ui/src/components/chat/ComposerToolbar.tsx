import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
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
        window.dispatchEvent(new CustomEvent('plugin-action', { detail: entry }))
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
    const groups = new Map<string, { id: string; name: string; icon: string; models: { id: string; label: string; provider: string }[] }>()

    for (const m of models) {
      if (!m.is_enabled) continue
      const providerInfo = providerMap.get(m.provider_id)
      if (providerInfo && !providerInfo.is_enabled) continue
      const providerType = m.provider_type || 'anthropic'
      if (!groups.has(m.provider_id)) {
        groups.set(m.provider_id, {
          id: providerType,
          name: providerInfo?.name || providerType,
          icon: PROVIDER_ICONS[providerType] || providerType.charAt(0).toUpperCase(),
          models: [],
        })
      }
      groups.get(m.provider_id)!.models.push({
        id: m.model_id,
        label: m.display_name,
        provider: providerType,
      })
    }

    return Array.from(groups.values())
  }, [models, providers])

  const allModels = useMemo(() => groupedModels.flatMap((g) => g.models), [groupedModels])
  const currentModel = allModels.find((m) => m.id === activeModel)

  // Close dropdown on outside click
  useEffect(() => {
    if (!modelOpen) return
    function handleClickOutside(e: MouseEvent) {
      if (modelRef.current && !modelRef.current.contains(e.target as Node)) {
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
      <div className="relative" ref={modelRef}>
        <button
          onClick={() => setModelOpen((o) => !o)}
          className="flex items-center gap-1 text-xs text-composer-fg-secondary hover:text-composer-fg transition-colors py-0.5 px-1.5 rounded-md hover:bg-composer-hover"
        >
          <Bot className="w-3 h-3" />
          <span className="max-w-[200px] truncate">{currentModel?.label || activeModel || 'Select model'}</span>
          <ChevronDown className="w-3 h-3" />
        </button>

        {modelOpen && (
          <div className="absolute bottom-full left-0 mb-1 w-56 bg-composer border border-composer-border-focus rounded-xl shadow-xl z-50 py-1 max-h-72 overflow-y-auto">
            {groupedModels.length === 0 ? (
              <div className="px-3 py-2 text-xs text-composer-fg-muted">Loading models...</div>
            ) : (
              groupedModels.map((group) => (
                <div key={group.id}>
                  <div className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-composer-fg-muted uppercase tracking-wider">
                    <span className="w-4 h-4 rounded bg-composer-hover flex items-center justify-center text-[10px] font-bold text-composer-fg-secondary shrink-0">
                      {group.icon}
                    </span>
                    {group.name}
                  </div>
                  {group.models.map((model) => (
                    <button
                      key={model.id}
                      onClick={() => void handleModelSelect(model.id)}
                      className={`w-full text-left px-3 pl-8 py-1.5 text-sm transition-colors ${
                        model.id === activeModel
                          ? 'bg-composer-hover text-composer-fg'
                          : 'text-composer-fg-secondary hover:bg-composer-hover/60 hover:text-composer-fg'
                      }`}
                    >
                      {model.label}
                    </button>
                  ))}
                </div>
              ))
            )}
          </div>
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
              className="w-7 h-7 text-red-400 hover:text-red-300 hover:bg-red-500/10 transition-colors"
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
                  ? 'text-accent hover:text-accent-hover hover:bg-accent-hover/10'
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
