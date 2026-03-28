import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import { Bot, ChevronDown, Paperclip, SendHorizonal, Square } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useChatStore } from '@/stores/useChatStore'
import { useAppStore } from '@/stores/useAppStore'
import { useModels, useProviders } from '@/hooks/useSettings'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'

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
  const queryClient = useQueryClient()

  const [modelOpen, setModelOpen] = useState(false)
  const [uploading, setUploading] = useState(false)
  const modelRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

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

  const handleFileUpload = useCallback(async (files: FileList | null) => {
    if (!files || files.length === 0 || !activeSessionId) return
    setUploading(true)
    try {
      for (const file of Array.from(files)) {
        await api.uploadArtifact(activeSessionId, file)
      }
      queryClient.invalidateQueries({ queryKey: ['artifacts', activeSessionId] })
    } catch (err) {
      console.error('Failed to upload artifact:', err)
    } finally {
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }, [activeSessionId, queryClient])

  return (
    <div className="flex items-center justify-between px-3 py-1.5 bg-zinc-800/50 rounded-b-xl border-t border-zinc-700/50">
      {/* Left: Attach + Model picker */}
      <div className="flex items-center gap-1">
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => void handleFileUpload(e.target.files)}
        />
        <Button
          variant="ghost"
          size="icon"
          className={`w-7 h-7 transition-colors ${
            uploading
              ? 'text-indigo-400 animate-pulse'
              : 'text-zinc-500 hover:text-zinc-300 hover:bg-zinc-700/50'
          }`}
          title={uploading ? 'Uploading...' : 'Attach file'}
          disabled={!activeSessionId || uploading}
          onClick={() => fileInputRef.current?.click()}
        >
          <Paperclip className="w-3.5 h-3.5" />
        </Button>
      <div className="relative" ref={modelRef}>
        <button
          onClick={() => setModelOpen((o) => !o)}
          className="flex items-center gap-1 text-xs text-zinc-400 hover:text-zinc-200 transition-colors py-0.5 px-1 rounded hover:bg-zinc-700/50"
        >
          <Bot className="w-3 h-3" />
          <span className="max-w-[200px] truncate">{currentModel?.label || activeModel || 'Select model'}</span>
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

      {/* Right: Send/Stop */}
      <div className="flex items-center gap-1">
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
