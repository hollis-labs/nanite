import { useMemo, useEffect, useState, useCallback, useRef } from 'react'
import { Loader2, GripVertical, X } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useSettings, useSettingsMutation, useModels, useProviders } from '@/hooks/useSettings'
import { api } from '@/lib/api'
import type { ToolCallDisplayMode } from '@/lib/types'

const ADAPTER_OPTIONS = [
  { value: 'http', label: 'HTTP (API)' },
  { value: 'pty', label: 'PTY (CLI)' },
  { value: 'subprocess', label: 'Subprocess (Pipe)' },
]

const TOOL_DISPLAY_OPTIONS: { value: ToolCallDisplayMode; label: string }[] = [
  { value: 'indicator', label: 'Indicator' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'compact', label: 'Compact' },
  { value: 'full', label: 'Full' },
]

function SettingsSelect({
  label,
  description,
  value,
  options,
  onChange,
  disabled,
}: {
  label: string
  description?: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  disabled?: boolean
}) {
  return (
    <div className="flex items-start justify-between gap-8 py-3">
      <div className="min-w-0">
        <div className="text-sm font-medium text-zinc-200">{label}</div>
        {description && (
          <div className="text-xs text-zinc-500 mt-0.5">{description}</div>
        )}
      </div>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="shrink-0 w-56 bg-zinc-900 border border-zinc-700 rounded-md px-3 py-1.5 text-sm text-zinc-200 focus:outline-none focus:ring-1 focus:ring-indigo-500 focus:border-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <option value="">None</option>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  )
}

function SectionHeader({ title }: { title: string }) {
  return (
    <div className="border-b border-zinc-800 pb-2 mb-1">
      <h3 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">{title}</h3>
    </div>
  )
}

function FallbackChain({
  chain,
  providers,
  onChange,
}: {
  chain: string[]
  providers: { value: string; label: string }[]
  onChange: (chain: string[]) => void
}) {
  const [items, setItems] = useState(chain)
  const dragItem = useRef<number | null>(null)
  const dragOverItem = useRef<number | null>(null)

  // Sync when external chain changes.
  useEffect(() => {
    setItems(chain)
  }, [chain])

  const providerLabel = useCallback(
    (id: string) => providers.find((p) => p.value === id)?.label || id,
    [providers],
  )

  const handleDragStart = useCallback((idx: number) => {
    dragItem.current = idx
  }, [])

  const handleDragEnter = useCallback((idx: number) => {
    dragOverItem.current = idx
  }, [])

  const handleDragEnd = useCallback(() => {
    if (dragItem.current === null || dragOverItem.current === null) return
    const updated = [...items]
    const [removed] = updated.splice(dragItem.current, 1)
    updated.splice(dragOverItem.current, 0, removed)
    dragItem.current = null
    dragOverItem.current = null
    setItems(updated)
    onChange(updated)
  }, [items, onChange])

  const handleRemove = useCallback(
    (idx: number) => {
      const updated = items.filter((_, i) => i !== idx)
      setItems(updated)
      onChange(updated)
    },
    [items, onChange],
  )

  const handleAdd = useCallback(
    (providerId: string) => {
      if (!providerId || items.includes(providerId)) return
      const updated = [...items, providerId]
      setItems(updated)
      onChange(updated)
    },
    [items, onChange],
  )

  const available = providers.filter((p) => !items.includes(p.value))

  return (
    <div className="space-y-2">
      {items.length === 0 && (
        <div className="text-xs text-zinc-600 py-2">No providers in fallback chain. Add one below.</div>
      )}
      {items.map((id, idx) => (
        <div
          key={id}
          draggable
          onDragStart={() => handleDragStart(idx)}
          onDragEnter={() => handleDragEnter(idx)}
          onDragEnd={handleDragEnd}
          onDragOver={(e) => e.preventDefault()}
          className="flex items-center gap-2 px-3 py-2 bg-zinc-900 border border-zinc-700 rounded-md text-sm text-zinc-200 cursor-grab active:cursor-grabbing hover:border-zinc-600 transition-colors"
        >
          <GripVertical className="w-3.5 h-3.5 text-zinc-600 shrink-0" />
          <span className="text-xs text-zinc-500 tabular-nums w-5">{idx + 1}.</span>
          <span className="flex-1">{providerLabel(id)}</span>
          <button
            onClick={() => handleRemove(idx)}
            className="p-0.5 rounded text-zinc-600 hover:text-zinc-300 hover:bg-zinc-800 transition-colors"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      ))}
      {available.length > 0 && (
        <div className="flex items-center gap-2 pt-1">
          <select
            onChange={(e) => {
              handleAdd(e.target.value)
              e.target.value = ''
            }}
            defaultValue=""
            className="flex-1 bg-zinc-900 border border-zinc-700 rounded-md px-3 py-1.5 text-sm text-zinc-400 focus:outline-none focus:ring-1 focus:ring-indigo-500 focus:border-indigo-500"
          >
            <option value="" disabled>Add provider...</option>
            {available.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </div>
      )}
    </div>
  )
}

export function PreferencesPanel() {
  const { data: settings, isLoading: settingsLoading } = useSettings()
  const mutation = useSettingsMutation()
  const { data: providers } = useProviders()
  const { data: models } = useModels()
  const { data: agents } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
    staleTime: 5 * 60 * 1000,
  })

  // Migrate toolCallDisplayMode from localStorage on first load.
  useEffect(() => {
    if (!settings) return
    const stored = localStorage.getItem('conduit:toolCallDisplayMode')
    if (stored && !settings.tool_call_display_mode) {
      mutation.mutate({ tool_call_display_mode: stored as ToolCallDisplayMode })
      localStorage.removeItem('conduit:toolCallDisplayMode')
    }
  }, [settings]) // eslint-disable-line react-hooks/exhaustive-deps

  const providerOptions = useMemo(() => {
    if (!providers) return []
    return providers.map((p) => ({ value: p.provider_type, label: p.name }))
  }, [providers])

  const modelOptionsForProvider = useMemo(() => {
    if (!models) return []
    const provider = settings?.default_provider
    const filtered = provider ? models.filter((m) => m.provider_type === provider) : models
    return filtered.map((m) => ({ value: m.model_id, label: m.display_name }))
  }, [models, settings?.default_provider])

  const utilityModelOptions = useMemo(() => {
    if (!models) return []
    const provider = settings?.utility_provider
    const filtered = provider ? models.filter((m) => m.provider_type === provider) : models
    return filtered.map((m) => ({ value: m.model_id, label: m.display_name }))
  }, [models, settings?.utility_provider])

  const agentOptions = useMemo(() => {
    if (!agents) return []
    return agents.map((a) => ({ value: a.id, label: a.name }))
  }, [agents])

  const handleChange = (key: string, value: string) => {
    mutation.mutate({ [key]: value })
  }

  if (settingsLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-zinc-500">
        <Loader2 className="w-5 h-5 animate-spin mr-2" />
        Loading preferences...
      </div>
    )
  }

  return (
    <div className="max-w-2xl space-y-8">
      {/* Defaults */}
      <div>
        <SectionHeader title="Session Defaults" />
        <div className="divide-y divide-zinc-800/50">
          <SettingsSelect
            label="Default Adapter"
            description="How Conduit connects to AI providers"
            value={settings?.default_adapter ?? ''}
            options={ADAPTER_OPTIONS}
            onChange={(v) => handleChange('default_adapter', v)}
          />
          <SettingsSelect
            label="Default Provider"
            description="Provider for new sessions"
            value={settings?.default_provider ?? ''}
            options={providerOptions}
            onChange={(v) => {
              handleChange('default_provider', v)
              // Clear model when provider changes (it may not be valid).
              if (settings?.default_model) {
                handleChange('default_model', '')
              }
            }}
          />
          <SettingsSelect
            label="Default Model"
            description="Model for new sessions"
            value={settings?.default_model ?? ''}
            options={modelOptionsForProvider}
            onChange={(v) => handleChange('default_model', v)}
          />
          <SettingsSelect
            label="Default Agent"
            description="Agent profile assigned to new sessions"
            value={settings?.default_agent ?? ''}
            options={agentOptions}
            onChange={(v) => handleChange('default_agent', v)}
          />
        </div>
      </div>

      {/* Utility Model */}
      <div>
        <SectionHeader title="Utility Model" />
        <p className="text-xs text-zinc-500 mb-2">
          Used for background tasks like auto-title, auto-tags, and summarization.
        </p>
        <div className="divide-y divide-zinc-800/50">
          <SettingsSelect
            label="Utility Provider"
            description="Provider for utility calls"
            value={settings?.utility_provider ?? ''}
            options={providerOptions}
            onChange={(v) => {
              handleChange('utility_provider', v)
              if (settings?.utility_model) {
                handleChange('utility_model', '')
              }
            }}
          />
          <SettingsSelect
            label="Utility Model"
            description="Model for utility calls"
            value={settings?.utility_model ?? ''}
            options={utilityModelOptions}
            onChange={(v) => handleChange('utility_model', v)}
          />
        </div>
      </div>

      {/* Display */}
      <div>
        <SectionHeader title="Display" />
        <div className="divide-y divide-zinc-800/50">
          <SettingsSelect
            label="Tool Call Display"
            description="How tool calls appear in chat"
            value={settings?.tool_call_display_mode ?? 'minimal'}
            options={TOOL_DISPLAY_OPTIONS}
            onChange={(v) => handleChange('tool_call_display_mode', v)}
          />
        </div>
      </div>

      {/* Fallback Chain */}
      <div>
        <SectionHeader title="Provider Fallback Chain" />
        <p className="text-xs text-zinc-500 mb-3">
          When a provider is unavailable, Conduit tries the next one in order. Drag to reorder.
        </p>
        <FallbackChain
          chain={settings?.provider_fallback_chain ?? []}
          providers={providerOptions}
          onChange={(chain) => mutation.mutate({ provider_fallback_chain: chain })}
        />
      </div>
    </div>
  )
}
