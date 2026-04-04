import { useMemo, useEffect, useState, useCallback, useRef } from 'react'
import { GripVertical, X, ChevronDown } from 'lucide-react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { usePermissionMode } from '@/hooks/usePermissionMode'
import { useSettings, useSettingsMutation, useModels, useProviders } from '@/hooks/useSettings'
import { api } from '@/lib/api'
import type { PermissionMode, ToolCallDisplayMode, ToolStreamBehavior } from '@/lib/types'

const PERMISSION_MODE_OPTIONS: { value: PermissionMode; label: string; description: string }[] = [
  { value: 'default', label: 'Default', description: 'Prompt for destructive/write operations' },
  { value: 'accept-edits', label: 'Accept Edits', description: 'Auto-accept file edits, prompt for shell' },
  { value: 'plan', label: 'Plan (Read-Only)', description: 'No modifications allowed' },
  { value: 'yolo', label: 'Yolo', description: 'Skip all permission prompts' },
]

const TOOL_DISPLAY_OPTIONS: { value: ToolCallDisplayMode; label: string }[] = [
  { value: 'indicator', label: 'Indicator' },
  { value: 'minimal', label: 'Minimal' },
  { value: 'compact', label: 'Compact' },
  { value: 'full', label: 'Full' },
]

const TOOL_STREAM_OPTIONS: { value: ToolStreamBehavior; label: string }[] = [
  { value: 'streaming', label: 'While Running' },
  { value: 'persist', label: 'Always' },
  { value: 'hidden', label: 'Hidden' },
]

const DRAWER_RETENTION_OPTIONS: { value: string; label: string }[] = [
  { value: '5', label: '5 minutes' },
  { value: '15', label: '15 minutes' },
  { value: '30', label: '30 minutes' },
  { value: '60', label: '1 hour' },
  { value: '-1', label: 'Until refresh' },
]

// --- Shared components ---

function SettingsCard({
  title,
  description,
  children,
}: {
  title: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      <div className="px-4 py-3 border-b border-border/50">
        <h3 className="text-sm font-semibold text-fg">{title}</h3>
        {description && (
          <p className="text-[11px] text-fg-muted mt-0.5">{description}</p>
        )}
      </div>
      <div className="px-4 py-2">{children}</div>
    </div>
  )
}

function SettingsRow({
  label,
  description,
  children,
}: {
  label: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-2.5">
      <div className="min-w-0">
        <div className="text-sm text-fg">{label}</div>
        {description && (
          <div className="text-[11px] text-fg-muted mt-0.5">{description}</div>
        )}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function SettingsSelect({
  value,
  options,
  onChange,
  disabled,
  allowNone = true,
}: {
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  disabled?: boolean
  allowNone?: boolean
}) {
  return (
    <div className="relative">
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="appearance-none w-48 bg-bg-elevated border border-border-subtle rounded-lg pl-3 pr-8 py-1.5 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
      >
        {allowNone && <option value="">None</option>}
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
    </div>
  )
}

function Toggle({
  checked,
  onChange,
  variant = 'default',
}: {
  checked: boolean
  onChange: (checked: boolean) => void
  variant?: 'default' | 'warning'
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={`relative shrink-0 inline-flex h-5 w-9 items-center rounded-full transition-colors ${
        checked
          ? variant === 'warning'
            ? 'bg-amber-600'
            : 'bg-toggle-on'
          : 'bg-zinc-700'
      }`}
    >
      <span
        className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform ${
          checked ? 'translate-x-[18px]' : 'translate-x-[3px]'
        }`}
      />
    </button>
  )
}

// --- Fallback Chain ---

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
    <div className="space-y-1.5">
      {items.length === 0 && (
        <div className="text-[11px] text-fg-faint py-3">No providers in fallback chain. Add one below.</div>
      )}
      {items.map((id, idx) => (
        <div
          key={id}
          draggable
          onDragStart={() => handleDragStart(idx)}
          onDragEnter={() => handleDragEnter(idx)}
          onDragEnd={handleDragEnd}
          onDragOver={(e) => e.preventDefault()}
          className="flex items-center gap-2 px-3 py-2 bg-bg-elevated border border-border-subtle rounded-lg text-sm text-fg cursor-grab active:cursor-grabbing hover:shadow-sm transition-all"
        >
          <GripVertical className="w-3.5 h-3.5 text-fg-faint shrink-0" />
          <span className="text-[11px] text-fg-muted tabular-nums w-5">{idx + 1}.</span>
          <span className="flex-1">{providerLabel(id)}</span>
          <button
            onClick={() => handleRemove(idx)}
            className="p-0.5 rounded text-fg-faint hover:text-fg-secondary hover:bg-surface transition-colors"
          >
            <X className="w-3 h-3" />
          </button>
        </div>
      ))}
      {available.length > 0 && (
        <div className="relative">
          <select
            onChange={(e) => {
              handleAdd(e.target.value)
              e.target.value = ''
            }}
            defaultValue=""
            className="appearance-none w-full bg-bg-elevated border border-border-subtle rounded-lg pl-3 pr-8 py-1.5 text-sm text-fg-muted focus:outline-none focus:ring-1 focus:ring-accent focus:border-accent cursor-pointer"
          >
            <option value="" disabled>Add provider...</option>
            {available.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
          <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
        </div>
      )}
    </div>
  )
}

// --- Main panel ---

export function PreferencesPanel() {
  const { data: settings } = useSettings()
  const mutation = useSettingsMutation()
  const { mode: permissionMode, setMode: setPermissionMode } = usePermissionMode()
  const { data: providers } = useProviders()
  const { data: models } = useModels()
  const { data: agents } = useQuery({
    queryKey: ['agents'],
    queryFn: api.listAgents,
    staleTime: 5 * 60 * 1000,
    placeholderData: keepPreviousData,
  })

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
    return providers
      .filter((p) => p.is_enabled)
      .map((p) => ({ value: p.provider_type, label: p.name }))
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
    return agents
      .filter((a) => a.status !== 'disabled')
      .map((a) => ({ value: a.id, label: a.source ? `${a.name} \u00b7 ${a.source}` : a.name }))
  }, [agents])

  const handleChange = (key: string, value: string) => {
    mutation.mutate({ [key]: value })
  }

  return (
    <div className="space-y-3">
      <SettingsCard title="Session Defaults" description="Defaults applied when creating new chat sessions">
        <SettingsRow label="Provider" description="Provider for new sessions">
          <SettingsSelect
            value={settings?.default_provider ?? ''}
            options={providerOptions}
            onChange={(v) => {
              mutation.mutate(settings?.default_model
                ? { default_provider: v, default_model: '' }
                : { default_provider: v })
            }}
          />
        </SettingsRow>
        <SettingsRow label="Model" description="Model for new sessions">
          <SettingsSelect
            value={settings?.default_model ?? ''}
            options={modelOptionsForProvider}
            onChange={(v) => handleChange('default_model', v)}
          />
        </SettingsRow>
        <SettingsRow label="Agent" description="Agent profile for new sessions">
          <SettingsSelect
            value={settings?.default_agent ?? ''}
            options={agentOptions}
            onChange={(v) => handleChange('default_agent', v)}
          />
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="Permission Mode" description="Controls how tool execution permissions are handled">
        <SettingsRow label="Mode" description="Determines which tool calls require approval">
          <SettingsSelect
            value={permissionMode}
            options={PERMISSION_MODE_OPTIONS}
            onChange={(v) => setPermissionMode(v as PermissionMode)}
          />
        </SettingsRow>
        {permissionMode !== 'default' && (
          <div className="pb-2">
            <p className="text-[10px] text-fg-muted">
              {PERMISSION_MODE_OPTIONS.find((o) => o.value === permissionMode)?.description}
            </p>
          </div>
        )}
      </SettingsCard>

      <SettingsCard title="Utility Model" description="Used for auto-title, auto-tags, and summarization">
        <SettingsRow label="Provider">
          <SettingsSelect
            value={settings?.utility_provider ?? ''}
            options={providerOptions}
            onChange={(v) => {
              mutation.mutate(settings?.utility_model
                ? { utility_provider: v, utility_model: '' }
                : { utility_provider: v })
            }}
          />
        </SettingsRow>
        <SettingsRow label="Model">
          <SettingsSelect
            value={settings?.utility_model ?? ''}
            options={utilityModelOptions}
            onChange={(v) => handleChange('utility_model', v)}
          />
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="Display">
        <SettingsRow label="Tool Call Style" description="How tool calls appear in chat">
          <SettingsSelect
            value={settings?.tool_call_display_mode ?? 'minimal'}
            options={TOOL_DISPLAY_OPTIONS}
            onChange={(v) => handleChange('tool_call_display_mode', v)}
          />
        </SettingsRow>
        <SettingsRow label="Tool Call Visibility" description="When tool calls are visible in the chat stream">
          <SettingsSelect
            value={settings?.tool_stream_behavior ?? 'streaming'}
            options={TOOL_STREAM_OPTIONS}
            onChange={(v) => handleChange('tool_stream_behavior', v)}
            allowNone={false}
          />
        </SettingsRow>
        <SettingsRow label="Drawer Retention" description="How long tool call history stays in the drawer">
          <SettingsSelect
            value={String(settings?.tool_drawer_retention ?? 15)}
            options={DRAWER_RETENTION_OPTIONS}
            onChange={(v) => mutation.mutate({ tool_drawer_retention: Number(v) })}
            allowNone={false}
          />
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="Provider Fallback Chain" description="When a provider is unavailable, Conduit tries the next one. Drag to reorder.">
        <FallbackChain
          chain={settings?.provider_fallback_chain ?? []}
          providers={providerOptions}
          onChange={(chain) => mutation.mutate({ provider_fallback_chain: chain })}
        />
      </SettingsCard>

      <SettingsCard title="Advanced">
        <SettingsRow label="Developer Mode" description="Allow plugins to register custom React components">
          <Toggle
            checked={settings?.developer_mode ?? false}
            onChange={(v) => mutation.mutate({ developer_mode: v })}
          />
        </SettingsRow>
        <SettingsRow label="Recover Mode" description="Disable all plugin UI overrides">
          <Toggle
            checked={settings?.recover_mode ?? false}
            onChange={(v) => mutation.mutate({ recover_mode: v })}
            variant="warning"
          />
        </SettingsRow>
      </SettingsCard>
    </div>
  )
}
