import { Suspense, useState, useCallback, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Save, Loader2, Eye, EyeOff } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import { useSettings } from '@/hooks/useSettings'
import { getConfigComponentOverride } from '@/generated/plugin-config-components'
import type { ConfigField } from '@/lib/types'

interface PluginConfigPanelProps {
  pluginId: string
  pluginName: string
  onBack: () => void
}

function ConfigFieldInput({
  field,
  value,
  onChange,
}: {
  field: ConfigField
  value: unknown
  onChange: (key: string, value: unknown) => void
}) {
  const [showSecret, setShowSecret] = useState(false)

  switch (field.type) {
    case 'bool':
      return (
        <label className="flex items-center gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={Boolean(value ?? field.default ?? false)}
            onChange={(e) => onChange(field.key, e.target.checked)}
            className="w-4 h-4 rounded bg-zinc-800 border-zinc-600 text-indigo-500 focus:ring-indigo-500 focus:ring-offset-0 cursor-pointer"
          />
          <span className="text-sm text-zinc-300">{field.label}</span>
        </label>
      )

    case 'int':
      return (
        <input
          type="number"
          value={value !== undefined && value !== null ? String(value) : (field.default !== undefined ? String(field.default) : '')}
          onChange={(e) => onChange(field.key, e.target.value === '' ? undefined : Number(e.target.value))}
          className="w-full px-3 py-1.5 rounded-md bg-zinc-800 border border-zinc-700 text-sm text-zinc-200 focus:outline-none focus:border-indigo-500 transition-colors"
        />
      )

    case 'select':
      return (
        <select
          value={String(value ?? field.default ?? '')}
          onChange={(e) => onChange(field.key, e.target.value)}
          className="w-full px-3 py-1.5 rounded-md bg-zinc-800 border border-zinc-700 text-sm text-zinc-200 focus:outline-none focus:border-indigo-500 transition-colors"
        >
          <option value="">Select...</option>
          {(field.options ?? []).map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </select>
      )

    case 'secret':
      return (
        <div className="relative">
          <input
            type={showSecret ? 'text' : 'password'}
            value={String(value ?? '')}
            onChange={(e) => onChange(field.key, e.target.value)}
            placeholder={field.default ? '••••••••' : 'Enter value'}
            className="w-full px-3 py-1.5 pr-9 rounded-md bg-zinc-800 border border-zinc-700 text-sm text-zinc-200 font-mono focus:outline-none focus:border-indigo-500 transition-colors"
          />
          <button
            type="button"
            onClick={() => setShowSecret(!showSecret)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300 transition-colors"
          >
            {showSecret ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
          </button>
        </div>
      )

    default: // string
      return (
        <input
          type="text"
          value={String(value ?? field.default ?? '')}
          onChange={(e) => onChange(field.key, e.target.value)}
          className="w-full px-3 py-1.5 rounded-md bg-zinc-800 border border-zinc-700 text-sm text-zinc-200 focus:outline-none focus:border-indigo-500 transition-colors"
        />
      )
  }
}

function OverrideFieldRenderer({
  field,
  value,
  onChange,
}: {
  field: ConfigField
  value: unknown
  onChange: (key: string, value: unknown) => void
}) {
  const Override = field.component ? getConfigComponentOverride(field.component) : undefined
  if (!Override) {
    return <ConfigFieldInput field={field} value={value} onChange={onChange} />
  }
  return (
    <Suspense fallback={<div className="h-9 animate-pulse rounded-md bg-zinc-800" />}>
      <Override field={field} value={value} onChange={onChange} />
    </Suspense>
  )
}

export function PluginConfigPanel({ pluginId, pluginName, onBack }: PluginConfigPanelProps) {
  const queryClient = useQueryClient()
  const { data: userSettings } = useSettings()
  const developerMode = userSettings?.developer_mode ?? false
  const recoverMode = userSettings?.recover_mode ?? false
  const [localSettings, setLocalSettings] = useState<Record<string, unknown>>({})
  const [dirty, setDirty] = useState(false)

  const { data: config, isLoading } = useQuery({
    queryKey: ['plugin-config', pluginId],
    queryFn: () => api.getPluginConfig(pluginId),
  })

  // Sync remote settings to local state when data arrives
  useEffect(() => {
    if (config?.settings) {
      setLocalSettings(config.settings)
      setDirty(false)
    }
  }, [config])

  const saveMutation = useMutation({
    mutationFn: (settings: Record<string, unknown>) =>
      api.updatePluginConfig(pluginId, settings),
    onSuccess: () => {
      setDirty(false)
      queryClient.invalidateQueries({ queryKey: ['plugin-config', pluginId] })
    },
  })

  const handleChange = useCallback((key: string, value: unknown) => {
    setLocalSettings((prev) => ({ ...prev, [key]: value }))
    setDirty(true)
  }, [])

  const handleSave = useCallback(() => {
    saveMutation.mutate(localSettings)
  }, [saveMutation, localSettings])

  const schema = config?.schema ?? []

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onBack}
          className="p-1 rounded text-zinc-500 hover:text-zinc-200 hover:bg-zinc-800 transition-colors"
        >
          <ArrowLeft className="w-5 h-5" />
        </button>
        <div>
          <h2 className="text-lg font-semibold text-zinc-100">{pluginName} Configuration</h2>
          <p className="text-xs text-zinc-500">Plugin settings for {pluginId}</p>
        </div>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="w-6 h-6 animate-spin text-zinc-500" />
        </div>
      )}

      {!isLoading && schema.length === 0 && (
        <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-6 text-center">
          <p className="text-sm text-zinc-400">This plugin has no configurable settings.</p>
        </div>
      )}

      {!isLoading && schema.length > 0 && (
        <div className="space-y-4">
          {schema.map((field) => (
            <div
              key={field.key}
              className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-4"
            >
              {field.type !== 'bool' && (
                <div className="flex items-baseline gap-2 mb-2">
                  <label className="text-sm font-medium text-zinc-200">
                    {field.label}
                    {field.required && <span className="text-red-400 ml-0.5">*</span>}
                  </label>
                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-500 font-mono">
                    {field.type}
                  </span>
                </div>
              )}
              {field.description && field.type !== 'bool' && (
                <p className="text-xs text-zinc-500 mb-2">{field.description}</p>
              )}
              {developerMode && !recoverMode && field.component ? (
                <OverrideFieldRenderer
                  field={field}
                  value={localSettings[field.key]}
                  onChange={handleChange}
                />
              ) : (
                <ConfigFieldInput
                  field={field}
                  value={localSettings[field.key]}
                  onChange={handleChange}
                />
              )}
              {field.type === 'bool' && field.description && (
                <p className="text-xs text-zinc-500 mt-1 ml-7">{field.description}</p>
              )}
            </div>
          ))}

          {/* Save button */}
          <div className="flex items-center gap-3 pt-2">
            <Button
              onClick={handleSave}
              disabled={!dirty || saveMutation.isPending}
              className="bg-indigo-600 hover:bg-indigo-700 text-white disabled:opacity-50"
            >
              {saveMutation.isPending ? (
                <Loader2 className="w-4 h-4 animate-spin mr-2" />
              ) : (
                <Save className="w-4 h-4 mr-2" />
              )}
              Save Settings
            </Button>
            {saveMutation.isSuccess && !dirty && (
              <span className="text-xs text-emerald-400">Saved</span>
            )}
            {saveMutation.isError && (
              <span className="text-xs text-red-400">
                {(saveMutation.error as Error)?.message || 'Failed to save'}
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
