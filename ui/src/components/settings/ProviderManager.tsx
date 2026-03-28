import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Eye, EyeOff, RefreshCw, Check, AlertTriangle,
  CircleCheck, Terminal, Globe, FolderSearch,
} from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import type { ProviderStatus, CLIDetectionResult } from '@/lib/types'

// --- Provider icon map ---

const PROVIDER_ICONS: Record<string, { icon: string; color: string }> = {
  anthropic:      { icon: 'A',  color: 'bg-orange-600' },
  openai:         { icon: 'O',  color: 'bg-emerald-600' },
  ollama:         { icon: 'Ol', color: 'bg-blue-600' },
  gemini:         { icon: 'G',  color: 'bg-blue-500' },
  mistral:        { icon: 'M',  color: 'bg-orange-500' },
  'azure-openai': { icon: 'Az', color: 'bg-sky-600' },
  pty:            { icon: 'C',  color: 'bg-violet-600' },
  'pty-claude':   { icon: 'C',  color: 'bg-violet-600' },
  'pty-codex':    { icon: 'Cx', color: 'bg-emerald-600' },
  'pty-gemini':   { icon: 'G',  color: 'bg-blue-500' },
  'pty-copilot':  { icon: 'Cp', color: 'bg-zinc-600' },
  'pty-aider':    { icon: 'Ai', color: 'bg-green-600' },
}

const DEFAULT_BASE_URLS: Record<string, string> = {
  anthropic: 'https://api.anthropic.com',
  openai: 'https://api.openai.com/v1',
  gemini: 'https://generativelanguage.googleapis.com',
  mistral: 'https://api.mistral.ai/v1',
  'azure-openai': '',
  ollama: 'http://localhost:11434',
}

function ProviderIcon({ providerType }: { providerType: string }) {
  const entry = PROVIDER_ICONS[providerType] ?? { icon: '?', color: 'bg-zinc-700' }
  return (
    <span className={`inline-flex items-center justify-center w-8 h-8 rounded-md text-xs font-bold text-white shrink-0 ${entry.color}`}>
      {entry.icon}
    </span>
  )
}

// --- API Key Input ---

function APIKeyField({ providerId, hasKey }: { providerId: string; hasKey: boolean }) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const [showKey, setShowKey] = useState(false)
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: (apiKey: string) => api.setProviderAPIKey(providerId, apiKey),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
      setEditing(false)
      setValue('')
    },
  })

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        {hasKey ? (
          <span className="text-xs text-emerald-400 flex items-center gap-1">
            <CircleCheck className="w-3 h-3" /> Key set
          </span>
        ) : (
          <span className="text-xs text-zinc-500">No key</span>
        )}
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-zinc-400 hover:text-zinc-200"
          onClick={() => setEditing(true)}
        >
          {hasKey ? 'Change' : 'Set key'}
        </Button>
      </div>
    )
  }

  return (
    <div className="flex items-center gap-2">
      <div className="relative flex-1">
        <input
          type={showKey ? 'text' : 'password'}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="sk-..."
          className="w-full bg-zinc-900 border border-zinc-700 rounded-md px-3 py-1.5 text-sm text-zinc-200 pr-8 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          autoFocus
        />
        <button
          type="button"
          onClick={() => setShowKey(!showKey)}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-zinc-500 hover:text-zinc-300"
        >
          {showKey ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
        </button>
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-emerald-400 hover:text-emerald-300"
        onClick={() => mutation.mutate(value)}
        disabled={mutation.isPending}
      >
        <Check className="w-3 h-3" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-zinc-500 hover:text-zinc-300"
        onClick={() => { setEditing(false); setValue('') }}
      >
        Cancel
      </Button>
    </div>
  )
}

// --- CLI Path Input ---

function CLIPathField({
  providerId,
  providerSettings,
  detection,
}: {
  providerId: string
  providerSettings: string
  detection?: CLIDetectionResult
}) {
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState('')
  const queryClient = useQueryClient()

  const parsed = (() => {
    try { return JSON.parse(providerSettings || '{}') }
    catch { return {} }
  })()
  const customPath = parsed.cli_path as string | undefined

  const displayPath = customPath || detection?.path || ''
  const isDetected = detection?.detected ?? false

  const mutation = useMutation({
    mutationFn: (path: string) => {
      const settings = JSON.stringify({ ...parsed, cli_path: path || undefined })
      return api.updateProvider(providerId, { settings })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
      queryClient.invalidateQueries({ queryKey: ['cli-detection'] })
      setEditing(false)
      setValue('')
    },
  })

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        {isDetected ? (
          <span className="text-xs text-emerald-400 flex items-center gap-1 min-w-0">
            <CircleCheck className="w-3 h-3 shrink-0" />
            <code className="text-zinc-300 truncate max-w-[160px] inline-block overflow-x-auto provider-scroll">{displayPath}</code>
          </span>
        ) : (
          <span className="text-xs text-amber-400 flex items-center gap-1">
            <AlertTriangle className="w-3 h-3" /> Not found
          </span>
        )}
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-zinc-400 hover:text-zinc-200 shrink-0"
          onClick={() => { setEditing(true); setValue(displayPath) }}
        >
          {displayPath ? 'Change' : 'Set path'}
        </Button>
      </div>
    )
  }

  return (
    <div className="flex items-center gap-2">
      <input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="/usr/local/bin/claude"
        className="flex-1 bg-zinc-900 border border-zinc-700 rounded-md px-3 py-1.5 text-sm text-zinc-200 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500"
        autoFocus
      />
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-emerald-400 hover:text-emerald-300"
        onClick={() => mutation.mutate(value)}
        disabled={mutation.isPending}
      >
        <Check className="w-3 h-3" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-zinc-500 hover:text-zinc-300"
        onClick={() => { setEditing(false); setValue('') }}
      >
        Cancel
      </Button>
    </div>
  )
}

// --- Base URL field ---

function BaseURLField({ providerId, providerType, currentURL }: { providerId: string; providerType: string; currentURL: string }) {
  const defaultURL = DEFAULT_BASE_URLS[providerType] ?? ''
  const displayURL = currentURL || defaultURL
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(displayURL)
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: (url: string) => api.updateProvider(providerId, { base_url: url }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
      setEditing(false)
    },
  })

  if (!editing) {
    return (
      <div className="flex items-center gap-2">
        <span className="text-xs text-zinc-400 font-mono truncate max-w-[180px] inline-block overflow-x-auto provider-scroll">{displayURL || 'Not set'}</span>
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-zinc-400 hover:text-zinc-200 shrink-0"
          onClick={() => { setEditing(true); setValue(displayURL) }}
        >
          Edit
        </Button>
      </div>
    )
  }

  return (
    <div className="flex items-center gap-2">
      <input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder={defaultURL || 'https://api.example.com'}
        className="flex-1 bg-zinc-900 border border-zinc-700 rounded-md px-2 py-1 text-xs text-zinc-200 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500"
        autoFocus
      />
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-emerald-400 hover:text-emerald-300"
        onClick={() => mutation.mutate(value)}
        disabled={mutation.isPending}
      >
        <Check className="w-3 h-3" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="text-xs text-zinc-500 hover:text-zinc-300"
        onClick={() => { setEditing(false); setValue(displayURL) }}
      >
        Cancel
      </Button>
    </div>
  )
}

// --- Provider Card ---

function ProviderCard({
  provider,
  detection,
  isCLI,
}: {
  provider: ProviderStatus
  detection?: CLIDetectionResult
  isCLI: boolean
}) {
  const queryClient = useQueryClient()

  // Can this provider be activated?
  // API providers need a key (or are Ollama which needs none).
  // CLI providers need a detected binary.
  const isOllama = provider.provider_type === 'ollama'
  const canActivate = isCLI
    ? (detection?.detected ?? false)
    : (provider.has_api_key || isOllama)

  const isActive = provider.is_enabled && canActivate

  const toggleMutation = useMutation({
    mutationFn: (enabled: boolean) => api.updateProvider(provider.id, { is_enabled: enabled }),
    onMutate: async (enabled) => {
      await queryClient.cancelQueries({ queryKey: ['provider-statuses'] })
      queryClient.setQueryData<ProviderStatus[]>(['provider-statuses'], (old) =>
        old?.map((p) => p.id === provider.id ? { ...p, is_enabled: enabled } : p)
      )
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
    },
  })

  return (
    <div className={`rounded-lg border p-3 transition-colors w-[280px] min-w-[280px] max-w-[280px] ${
      isActive
        ? 'border-zinc-700 bg-zinc-900/50'
        : 'border-zinc-800 bg-zinc-950/50'
    }`}>
      {/* Header: icon, name, toggle */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          <ProviderIcon providerType={provider.provider_type} />
          <div className="min-w-0">
            <div className={`text-sm font-medium truncate ${isActive ? 'text-zinc-200' : 'text-zinc-500'}`}>
              {provider.name}
            </div>
            {isActive && (
              <span className="text-[11px] text-emerald-400 flex items-center gap-1">
                <CircleCheck className="w-2.5 h-2.5" /> Active
              </span>
            )}
            {!canActivate && !isCLI && !isOllama && (
              <span className="text-[11px] text-zinc-500">Needs API key</span>
            )}
            {!canActivate && isCLI && (
              <span className="text-[11px] text-amber-400 flex items-center gap-1">
                <AlertTriangle className="w-2.5 h-2.5" /> Not found
              </span>
            )}
            {canActivate && !provider.is_enabled && (
              <span className="text-[11px] text-zinc-500">Disabled</span>
            )}
          </div>
        </div>

        <button
          onClick={() => canActivate && toggleMutation.mutate(!provider.is_enabled)}
          disabled={!canActivate}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors shrink-0 ${
            isActive
              ? 'bg-indigo-600'
              : canActivate
                ? 'bg-zinc-700 hover:bg-zinc-600'
                : 'bg-zinc-800 cursor-not-allowed opacity-40'
          }`}
        >
          <span className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform ${
            isActive ? 'translate-x-4.5' : 'translate-x-0.5'
          }`} />
        </button>
      </div>

      {/* Config fields */}
      <div className="mt-2 space-y-2">
        {!isCLI && !isOllama && (
          <div>
            <div className="text-[11px] text-zinc-500 mb-0.5">API Key</div>
            <APIKeyField providerId={provider.id} hasKey={provider.has_api_key} />
          </div>
        )}

        {isCLI && (
          <div>
            <div className="text-[11px] text-zinc-500 mb-0.5">CLI Path</div>
            <CLIPathField
              providerId={provider.id}
              providerSettings={provider.settings}
              detection={detection}
            />
          </div>
        )}

        {!isCLI && !isOllama && (
          <div>
            <div className="text-[11px] text-zinc-500 mb-0.5">Base URL</div>
            <BaseURLField providerId={provider.id} providerType={provider.provider_type} currentURL={provider.base_url} />
          </div>
        )}

        {isOllama && (
          <div>
            <div className="text-[11px] text-zinc-500 mb-0.5">Host</div>
            <BaseURLField
              providerId={provider.id}
              providerType={provider.provider_type}
              currentURL={provider.base_url}
            />
          </div>
        )}
      </div>
    </div>
  )
}

// --- Main Component ---

export function ProviderManager() {
  const { data: providers } = useQuery({
    queryKey: ['provider-statuses'],
    queryFn: api.listProviderStatuses,
    staleTime: 30_000,
  })

  const { data: cliDetection, refetch: refetchCLI, isFetching: detectingCLI } = useQuery({
    queryKey: ['cli-detection'],
    queryFn: api.detectCLI,
    staleTime: 60_000,
  })

  const isCLIProvider = useCallback(
    (providerType: string) => providerType.startsWith('pty-') || providerType === 'pty',
    [],
  )

  const getDetection = useCallback(
    (providerType: string) => cliDetection?.find((d) => d.provider_type === providerType),
    [cliDetection],
  )

  const apiProviders = providers?.filter((p) => !isCLIProvider(p.provider_type)) ?? []
  const cliProviders = providers?.filter((p) => isCLIProvider(p.provider_type)) ?? []

  return (
    <div className="space-y-8">
      {/* API Providers */}
      <div>
        <div className="border-b border-zinc-800 pb-2 mb-4">
          <div className="flex items-center gap-2">
            <Globe className="w-4 h-4 text-zinc-400" />
            <h3 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">
              API Providers
            </h3>
          </div>
          <p className="text-xs text-zinc-500 mt-1">
            Cloud-hosted LLM APIs. Set your API key to activate.
          </p>
        </div>
        <div className="flex flex-wrap gap-3">
          {apiProviders.map((p) => (
            <ProviderCard key={p.id} provider={p} isCLI={false} />
          ))}
        </div>
      </div>

      {/* CLI Providers */}
      <div>
        <div className="border-b border-zinc-800 pb-2 mb-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <Terminal className="w-4 h-4 text-zinc-400" />
              <h3 className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">
                CLI Providers
              </h3>
            </div>
            <Button
              variant="ghost"
              size="sm"
              className="text-xs text-zinc-400 hover:text-zinc-200"
              onClick={() => refetchCLI()}
              disabled={detectingCLI}
            >
              <FolderSearch className="w-3.5 h-3.5 mr-1.5" />
              {detectingCLI ? (
                <RefreshCw className="w-3 h-3 animate-spin" />
              ) : (
                'Re-detect'
              )}
            </Button>
          </div>
          <p className="text-xs text-zinc-500 mt-1">
            Local CLI tools spawned via PTY. Auto-detected from your PATH.
          </p>
        </div>
        <div className="flex flex-wrap gap-3">
          {cliProviders.map((p) => (
            <ProviderCard
              key={p.id}
              provider={p}
              detection={getDetection(p.provider_type)}
              isCLI
            />
          ))}
        </div>
      </div>
    </div>
  )
}
