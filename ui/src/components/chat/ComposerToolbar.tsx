import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { ChevronDown, Paperclip, SendHorizonal, Square } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { useModels, useProviders } from '@/hooks/useSettings'
import { api } from '@/lib/api'
import { AGENT_MODES, type AgentMode } from '@/lib/types'

const MODE_DOT_COLORS: Record<AgentMode, string> = {
  default: 'bg-blue-400',
  architect: 'bg-purple-400',
  planner: 'bg-green-400',
  writer: 'bg-amber-400',
}

const PROVIDER_ICONS: Record<string, string> = {
  anthropic: 'A',
  openai: 'O',
  ollama: 'L',
  pty: 'C',
  'pty-claude': 'C',
  'pty-codex': 'O',
  'pty-gemini': 'G',
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
  const activeMode = useChatStore((s) => s.activeMode)
  const setActiveMode = useChatStore((s) => s.setActiveMode)
  const activeSessionId = useAppStore((s) => s.activeSessionId)

  const [modelOpen, setModelOpen] = useState(false)
  const [modeOpen, setModeOpen] = useState(false)
  const modelRef = useRef<HTMLDivElement>(null)
  const modeRef = useRef<HTMLDivElement>(null)

  const { data: models } = useModels()
  const { data: providers } = useProviders()

  // Group models by provider for the dropdown.
  const groupedModels = useMemo(() => {
    if (!models || !providers) return []

    const providerMap = new Map(providers.map((p) => [p.id, p]))
    const groups = new Map<string, { id: string; name: string; icon: string; models: { id: string; label: string; provider: string }[] }>()

    for (const m of models) {
      if (!m.is_enabled) continue
      const providerInfo = providerMap.get(m.provider_id)
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

  // Close dropdowns on outside click
  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (modelRef.current && !modelRef.current.contains(e.target as Node)) {
        setModelOpen(false)
      }
      if (modeRef.current && !modeRef.current.contains(e.target as Node)) {
        setModeOpen(false)
      }
    }
    if (modelOpen || modeOpen) {
      document.addEventListener('mousedown', handleClickOutside)
      return () => document.removeEventListener('mousedown', handleClickOutside)
    }
    return undefined
  }, [modelOpen, modeOpen])

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

  const handleModeSelect = useCallback(async (mode: AgentMode) => {
    setActiveMode(mode)
    setModeOpen(false)
    if (activeSessionId) {
      try {
        await api.switchMode(activeSessionId, mode)
      } catch (err) {
        console.error('Failed to switch mode:', err)
      }
    }
  }, [activeSessionId, setActiveMode])

  return (
    <div className="flex items-center justify-between px-3 py-1.5 bg-zinc-800/50 rounded-b-xl border-t border-zinc-700/50">
      {/* Left: Attach + Model picker */}
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          className="w-7 h-7 text-zinc-500 hover:text-zinc-300 hover:bg-zinc-700/50 transition-colors"
          title="Attach file"
        >
          <Paperclip className="w-3.5 h-3.5" />
        </Button>
      <div className="relative" ref={modelRef}>
        <button
          onClick={() => setModelOpen((o) => !o)}
          className="flex items-center gap-1 text-xs text-zinc-400 hover:text-zinc-200 transition-colors py-0.5 px-1 rounded hover:bg-zinc-700/50"
        >
          <span>{currentModel?.label || activeModel || 'Select model'}</span>
          <ChevronDown className="w-3 h-3" />
        </button>

        {modelOpen && (
          <div className="absolute bottom-full left-0 mb-1 w-56 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1 max-h-72 overflow-y-auto">
            {groupedModels.length === 0 ? (
              <div className="px-3 py-2 text-xs text-zinc-500">Loading models...</div>
            ) : (
              groupedModels.map((group) => (
                <div key={group.id}>
                  <div className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                    <span className="w-4 h-4 rounded bg-zinc-800 flex items-center justify-center text-[10px] font-bold text-zinc-400 shrink-0">
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
                          ? 'bg-zinc-800 text-zinc-100'
                          : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
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

      {/* Right: Mode quick-switch + Send */}
      <div className="flex items-center gap-1">
        <div className="relative" ref={modeRef}>
          <button
            onClick={() => setModeOpen((o) => !o)}
            className="flex items-center gap-1.5 text-xs text-zinc-400 hover:text-zinc-200 transition-colors py-0.5 px-1 rounded hover:bg-zinc-700/50"
          >
            <span className={`w-2 h-2 rounded-full ${MODE_DOT_COLORS[activeMode]}`} />
            <span className="capitalize">{activeMode}</span>
            <ChevronDown className="w-3 h-3" />
          </button>

          {modeOpen && (
            <div className="absolute bottom-full right-0 mb-1 w-36 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1">
              <div className="px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                Mode
              </div>
              {AGENT_MODES.map((mode) => (
                <button
                  key={mode}
                  onClick={() => void handleModeSelect(mode)}
                  className={`w-full text-left px-3 py-1.5 text-sm flex items-center gap-2 transition-colors ${
                    mode === activeMode
                      ? 'bg-zinc-800 text-zinc-100'
                      : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200'
                  }`}
                >
                  <span className={`w-2 h-2 rounded-full ${MODE_DOT_COLORS[mode]}`} />
                  <span className="capitalize">{mode}</span>
                </button>
              ))}
            </div>
          )}
        </div>

        {isStreaming ? (
          <Button
            variant="ghost"
            size="icon"
            className="w-7 h-7 text-red-400 hover:text-red-300 hover:bg-red-500/10 transition-colors"
            onClick={onStop}
            title="Stop generating"
          >
            <Square className="w-3.5 h-3.5" />
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon"
            className={`w-7 h-7 transition-colors ${
              hasContent
                ? 'text-indigo-400 hover:text-indigo-300 hover:bg-indigo-500/10'
                : 'text-zinc-600'
            }`}
            disabled={!hasContent}
            onClick={onSend}
            title="Send (Enter)"
          >
            <SendHorizonal className="w-3.5 h-3.5" />
          </Button>
        )}
      </div>
    </div>
  )
}
