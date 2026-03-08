import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { ChevronDown, Paperclip, SendHorizonal, Square } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui/Button'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import { AVAILABLE_MODELS, AGENT_MODES, type AgentMode, type ModelOption, type Provider } from '@/lib/types'

const MODE_DOT_COLORS: Record<AgentMode, string> = {
  default: 'bg-blue-400',
  architect: 'bg-purple-400',
  planner: 'bg-green-400',
  writer: 'bg-amber-400',
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

  // Fetch providers from API, fall back to static AVAILABLE_MODELS grouped as "Anthropic"
  const { data: providers } = useQuery({
    queryKey: ['providers'],
    queryFn: api.listProviders,
    staleTime: 5 * 60 * 1000,
  })

  const PROVIDER_ICONS: Record<string, string> = {
    anthropic: 'A',
    openai: 'O',
    ollama: 'L',
  }

  const groupedModels: Provider[] = useMemo(() => {
    // Only use API providers if they have nested models arrays
    if (providers && providers.length > 0 && providers[0].models) return providers
    // Fallback: group static models by provider field
    const groups = new Map<string, ModelOption[]>()
    for (const m of AVAILABLE_MODELS) {
      const p = m.provider || 'anthropic'
      if (!groups.has(p)) groups.set(p, [])
      groups.get(p)!.push(m)
    }
    return Array.from(groups.entries()).map(([id, models]) => ({
      id,
      name: id.charAt(0).toUpperCase() + id.slice(1),
      models,
    }))
  }, [providers])

  const allModels = useMemo(() => groupedModels.flatMap((p) => p.models ?? []), [groupedModels])
  const currentModel = allModels.find((m) => m.id === activeModel) || AVAILABLE_MODELS.find((m) => m.id === activeModel)

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
  }, [modelOpen, modeOpen])

  const handleModelSelect = useCallback(async (modelId: string) => {
    setActiveModel(modelId)
    setModelOpen(false)
    if (activeSessionId) {
      try {
        await api.updateSession(activeSessionId, { model: modelId } as never)
      } catch (err) {
        console.error('Failed to update model:', err)
      }
    }
  }, [activeSessionId, setActiveModel])

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
          <span>{currentModel?.label || 'Select model'}</span>
          <ChevronDown className="w-3 h-3" />
        </button>

        {modelOpen && (
          <div className="absolute bottom-full left-0 mb-1 w-56 bg-zinc-900 border border-zinc-700 rounded-lg shadow-xl z-50 py-1 max-h-72 overflow-y-auto">
            {groupedModels.map((provider) => (
              <div key={provider.id}>
                <div className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-zinc-500 uppercase tracking-wider">
                  <span className="w-4 h-4 rounded bg-zinc-800 flex items-center justify-center text-[10px] font-bold text-zinc-400 shrink-0">
                    {PROVIDER_ICONS[provider.id?.toLowerCase()] || provider.name?.charAt(0) || '?'}
                  </span>
                  {provider.name}
                </div>
                {(provider.models ?? []).map((model) => (
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
            ))}
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
