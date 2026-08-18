import { useState, useCallback, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Eye, EyeOff, AlertTriangle, X, Search,
  CircleCheck, Globe, Loader2,
  ArrowUpDown,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { ProviderStatus, CLIDetectionResult } from '@/lib/types'

// --- Provider icon map ---

const PROVIDER_INITIALS: Record<string, string> = {
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

function ProviderIcon({ providerType, active }: { providerType: string; active: boolean }) {
  const initial = PROVIDER_INITIALS[providerType] ?? '?'
  return (
    <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg text-xs font-bold shrink-0 ${
      active
        ? 'bg-surface-hover text-fg-secondary'
        : 'bg-surface text-fg-muted'
    }`}>
      {initial}
    </span>
  )
}

// --- Tiny inline icons for the detail footer ---

function KeyIcon() {
  return (
    <svg className="w-3 h-3 text-fg-faint shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
    </svg>
  )
}

function FolderIcon() {
  return (
    <svg className="w-3 h-3 text-fg-faint shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
    </svg>
  )
}

// --- Modal shell ---

function Modal({
  title,
  open,
  onClose,
  children,
}: {
  title: string
  open: boolean
  onClose: () => void
  children: React.ReactNode
}) {
  if (!open) return null

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-md bg-bg-elevated border border-border-subtle rounded-xl shadow-2xl">
        <div className="flex items-center justify-between px-5 h-12 border-b border-border">
          <h3 className="text-sm font-semibold text-fg">{title}</h3>
          <button onClick={onClose} className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors">
            <X className="w-4 h-4" />
          </button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  )
}

// --- API Key clickable value + modal ---

function APIKeyField({ provider }: { provider: ProviderStatus }) {
  const [modalOpen, setModalOpen] = useState(false)
  const [value, setValue] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [testResult, setTestResult] = useState<'idle' | 'testing' | 'success' | 'error'>('idle')
  const [testError, setTestError] = useState('')
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: (apiKey: string) => api.setProviderAPIKey(provider.id, apiKey),
    onSuccess: () => {
      setTestResult('testing')
      setTestError('')
      api.testProviderConnection(provider.id)
        .then(() => {
          setTestResult('success')
          queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
        })
        .catch((err: Error) => {
          setTestResult('error')
          setTestError(err.message || 'Connection failed')
          queryClient.invalidateQueries({ queryKey: ['provider-statuses'] })
        })
    },
  })

  const handleOpen = () => {
    setValue('')
    setShowKey(false)
    setTestResult('idle')
    setTestError('')
    setModalOpen(true)
  }

  const handleSave = () => {
    if (!value.trim()) return
    mutation.mutate(value.trim())
  }

  return (
    <>
      <button onClick={handleOpen} className="text-left group">
        {provider.has_api_key ? (
          <span className="text-[11px] text-fg-secondary font-medium flex items-center gap-1 group-hover:text-fg transition-colors cursor-pointer">
            Key set
          </span>
        ) : (
          <span className="text-[11px] text-fg-muted group-hover:text-fg-secondary transition-colors underline underline-offset-2 decoration-border-subtle cursor-pointer">
            Set API key
          </span>
        )}
      </button>

      <Modal
        title={`${provider.name} — API Key`}
        open={modalOpen}
        onClose={() => setModalOpen(false)}
      >
        <div className="space-y-4">
          <div className="relative">
            <input
              type={showKey ? 'text' : 'password'}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') handleSave() }}
              placeholder="sk-..."
              className="w-full bg-surface border border-border-subtle rounded-md px-3 py-2 text-sm text-fg pr-10 focus:outline-none focus:ring-1 focus:ring-primary font-mono"
              autoFocus
            />
            <button
              type="button"
              onClick={() => setShowKey(!showKey)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-fg-muted hover:text-fg-secondary"
            >
              {showKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
            </button>
          </div>

          {testResult === 'testing' && (
            <div className="flex items-center gap-2 text-sm text-fg-secondary">
              <Loader2 className="w-4 h-4 animate-spin" />
              Testing connection...
            </div>
          )}
          {testResult === 'success' && (
            <div className="flex items-center gap-2 text-sm text-success bg-success-muted px-3 py-2 rounded-md">
              <CircleCheck className="w-4 h-4" />
              Connected successfully
            </div>
          )}
          {testResult === 'error' && (
            <div className="flex items-center gap-2 text-sm text-danger bg-danger/10 px-3 py-2 rounded-md">
              <AlertTriangle className="w-4 h-4" />
              {testError || 'Connection failed'}
            </div>
          )}

          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setModalOpen(false)}>
              {testResult === 'success' ? 'Done' : 'Cancel'}
            </Button>
            {testResult !== 'success' && (
              <Button
                size="sm"
                className="bg-primary hover:bg-primary-hover text-white"
                onClick={handleSave}
                disabled={!value.trim() || mutation.isPending || testResult === 'testing'}
              >
                {mutation.isPending ? 'Saving...' : 'Save & Test'}
              </Button>
            )}
          </div>
        </div>
      </Modal>
    </>
  )
}

// --- CLI Path — click to edit ---

function CLIPathField({
  providerId,
  providerSettings,
  detection,
}: {
  providerId: string
  providerSettings: string
  detection?: CLIDetectionResult
}) {
  const [modalOpen, setModalOpen] = useState(false)
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
      setModalOpen(false)
    },
  })

  const handleOpen = () => {
    setValue(displayPath)
    setModalOpen(true)
  }

  return (
    <>
      <button onClick={handleOpen} className="text-left group">
        {isDetected ? (
          <code className="text-[11px] text-fg-secondary font-mono truncate max-w-[200px] inline-block provider-scroll group-hover:text-fg transition-colors cursor-pointer">{displayPath}</code>
        ) : (
          <span className="text-[11px] text-fg-muted group-hover:text-fg-secondary transition-colors underline underline-offset-2 decoration-border-subtle cursor-pointer">
            Set path
          </span>
        )}
      </button>

      <Modal title="CLI Path" open={modalOpen} onClose={() => setModalOpen(false)}>
        <div className="space-y-4">
          <input
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') mutation.mutate(value) }}
            placeholder="/usr/local/bin/claude"
            className="w-full bg-surface border border-border-subtle rounded-md px-3 py-2 text-sm text-fg font-mono focus:outline-none focus:ring-1 focus:ring-primary"
            autoFocus
          />
          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setModalOpen(false)}>Cancel</Button>
            <Button
              size="sm"
              className="bg-primary hover:bg-primary-hover text-white"
              onClick={() => mutation.mutate(value)}
              disabled={mutation.isPending}
            >
              Save
            </Button>
          </div>
        </div>
      </Modal>
    </>
  )
}

// --- Unified Provider Card ---

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
    <div className={`rounded-xl border overflow-hidden transition-all border-l-2 ${
      isActive
        ? 'border-border-subtle bg-bg-elevated hover:border-border border-l-status-ok'
        : 'border-border bg-bg/30 opacity-45 border-l-fg-faint'
    }`}>
      {/* Header: Icon · Name · Status dot · Toggle */}
      <div className="flex items-center gap-2.5 px-3.5 py-3">
        <ProviderIcon providerType={provider.provider_type} active={isActive} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className={`text-sm font-semibold truncate ${isActive ? 'text-fg' : 'text-fg-muted'}`}>
              {provider.name}
            </span>
            {isActive && <span className="w-1.5 h-1.5 rounded-full bg-status-ok shrink-0" />}
            {!canActivate && <AlertTriangle className="w-3 h-3 text-warning shrink-0" />}
          </div>
        </div>
        <button
          onClick={() => canActivate && toggleMutation.mutate(!provider.is_enabled)}
          disabled={!canActivate}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors shrink-0 ${
            isActive
              ? 'bg-toggle-on'
              : canActivate
                ? 'bg-surface-hover hover:bg-surface-hover'
                : 'bg-surface cursor-not-allowed'
          }`}
        >
          <span className={`inline-block h-3.5 w-3.5 rounded-full bg-white shadow-sm transition-transform ${
            isActive ? 'translate-x-4.5' : 'translate-x-0.5'
          }`} />
        </button>
      </div>

      {/* Detail footer */}
      <div className="border-t border-border-subtle px-3.5 py-2 bg-bg/40 flex items-center gap-3">
        {/* Field 1: API key (Ollama needs no key/config — nothing to show) */}
        {!isCLI && !isOllama && (
          <div className="flex items-center gap-1.5 min-w-0">
            <KeyIcon />
            <APIKeyField provider={provider} />
          </div>
        )}
        {isCLI && (
          <div className="flex items-center gap-1.5 min-w-0">
            <FolderIcon />
            <CLIPathField
              providerId={provider.id}
              providerSettings={provider.settings}
              detection={detection}
            />
          </div>
        )}
        {/* Separator + Field 2 */}
        {isCLI && (
          <>
            <div className="w-px h-3.5 bg-border shrink-0" />
            <div className="flex items-center gap-1.5 shrink-0">
              <span className="text-[11px] text-fg-muted">
                {detection?.detected ? 'Auto-detected' : 'Manual'}
              </span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

// --- Skeleton loader ---

function ProviderCardSkeleton() {
  return (
    <div className="rounded-xl border border-border overflow-hidden animate-pulse">
      <div className="flex items-center gap-2.5 px-3.5 py-3">
        <div className="w-9 h-9 rounded-lg bg-surface" />
        <div className="h-4 bg-surface rounded w-28" />
        <div className="flex-1" />
        <div className="w-9 h-5 bg-surface rounded-full" />
      </div>
      <div className="border-t border-border-subtle px-3.5 py-2 bg-bg/40 flex items-center gap-3">
        <div className="h-3 bg-surface rounded w-16" />
        <div className="h-3 bg-surface rounded w-40" />
      </div>
    </div>
  )
}

// --- Sort dropdown ---

type SortOption = 'alpha' | 'status' | 'updated'
const SORT_OPTIONS: { value: SortOption; label: string }[] = [
  { value: 'status', label: 'Status' },
  { value: 'alpha', label: 'Alphabetical' },
  { value: 'updated', label: 'Recently Updated' },
]

// --- Filter types ---
type StatusFilter = 'all' | 'active' | 'inactive'

// --- Main Component ---

export function ProviderManager() {
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [sortBy, setSortBy] = useState<SortOption>('status')
  const [sortMenuOpen, setSortMenuOpen] = useState(false)

  const { data: providers, isLoading } = useQuery({
    queryKey: ['provider-statuses'],
    queryFn: api.listProviderStatuses,
    staleTime: 30_000,
  })

  const isCLIProvider = useCallback(
    (providerType: string) => providerType.startsWith('pty-') || providerType === 'pty',
    [],
  )

  const apiProviders = providers?.filter((p) => !isCLIProvider(p.provider_type)) ?? []

  const isConfigured = useCallback((p: ProviderStatus) => {
    if (isCLIProvider(p.provider_type)) {
      return false
    }
    return p.has_api_key || p.provider_type === 'ollama'
  }, [isCLIProvider])

  const filteredProviders = useMemo(() => {
    let list = apiProviders

    if (search) {
      const q = search.toLowerCase()
      list = list.filter((p) => p.name.toLowerCase().includes(q))
    }

    if (statusFilter === 'active') {
      list = list.filter((p) => p.is_enabled && isConfigured(p))
    } else if (statusFilter === 'inactive') {
      list = list.filter((p) => !p.is_enabled || !isConfigured(p))
    }

    if (sortBy === 'alpha') {
      list = [...list].sort((a, b) => a.name.localeCompare(b.name))
    } else if (sortBy === 'status') {
      list = [...list].sort((a, b) => {
        const aActive = a.is_enabled && isConfigured(a) ? 1 : 0
        const bActive = b.is_enabled && isConfigured(b) ? 1 : 0
        return bActive - aActive || a.name.localeCompare(b.name)
      })
    } else if (sortBy === 'updated') {
      list = [...list].sort((a, b) => b.updated_at.localeCompare(a.updated_at))
    }

    return list
  }, [apiProviders, search, statusFilter, sortBy, isConfigured])

  const filterButtons: { value: StatusFilter; label: string }[] = [
    { value: 'all', label: 'All' },
    { value: 'active', label: 'Active' },
    { value: 'inactive', label: 'Inactive' },
  ]

  return (
    <div className="space-y-4">
      {/* Toolbar: Tabs + Controls */}
      <div className="flex items-center gap-3 flex-wrap">
        {/* Tabs */}
        <div className="flex items-center gap-1 bg-surface/50 rounded-lg p-0.5">
          <button
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              'bg-bg-elevated text-fg shadow-sm'
            }`}
          >
            <Globe className="w-3.5 h-3.5" />
            HTTP
            <span className="text-[11px] text-fg-faint tabular-nums">{apiProviders.length}</span>
          </button>
        </div>

        <div className="flex-1" />

        {/* Search */}
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-fg-faint pointer-events-none" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter..."
            className="w-40 bg-surface/50 border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-primary focus:border-primary"
          />
        </div>

        {/* Status filter */}
        <div className="flex items-center bg-surface/50 rounded-md p-0.5">
          {filterButtons.map((f) => (
            <button
              key={f.value}
              onClick={() => setStatusFilter(f.value)}
              className={`px-2.5 py-1 text-xs rounded transition-colors ${
                statusFilter === f.value
                  ? 'bg-bg-elevated text-fg shadow-sm'
                  : 'text-fg-muted hover:text-fg-secondary'
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>

        {/* Sort */}
        <div className="relative">
          <Button
            variant="ghost"
            size="sm"
            className="text-xs text-fg-secondary hover:text-fg gap-1"
            onClick={() => setSortMenuOpen(!sortMenuOpen)}
          >
            <ArrowUpDown className="w-3.5 h-3.5" />
            {SORT_OPTIONS.find((o) => o.value === sortBy)?.label}
          </Button>
          {sortMenuOpen && (
            <>
              <div className="fixed inset-0 z-40" onClick={() => setSortMenuOpen(false)} />
              <div className="absolute right-0 top-full mt-1 z-50 w-44 bg-bg-elevated border border-border-subtle rounded-lg shadow-xl py-1">
                {SORT_OPTIONS.map((opt) => (
                  <button
                    key={opt.value}
                    onClick={() => { setSortBy(opt.value); setSortMenuOpen(false) }}
                    className={`w-full text-left px-3 py-1.5 text-sm transition-colors ${
                      sortBy === opt.value
                        ? 'text-fg bg-surface'
                        : 'text-fg-secondary hover:bg-surface/50 hover:text-fg'
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      </div>

      {/* Provider list */}
      <div className="grid grid-cols-2 gap-3">
        {isLoading && (
          <>
            <ProviderCardSkeleton />
            <ProviderCardSkeleton />
            <ProviderCardSkeleton />
            <ProviderCardSkeleton />
          </>
        )}

        {!isLoading && filteredProviders.length === 0 && (
          <div className="col-span-2 flex flex-col items-center justify-center py-12 text-center">
            <Search className="w-8 h-8 text-fg-faint mb-3" />
            <p className="text-sm text-fg-muted">No providers match your filter</p>
            {(search || statusFilter !== 'all') && (
              <button
                onClick={() => { setSearch(''); setStatusFilter('all') }}
                className="text-xs text-primary hover:text-primary-hover mt-2 transition-colors"
              >
                Clear filters
              </button>
            )}
          </div>
        )}

        {filteredProviders.map((p) => (
          <ProviderCard
            key={p.id}
            provider={p}
            detection={undefined}
            isCLI={isCLIProvider(p.provider_type)}
          />
        ))}
      </div>
    </div>
  )
}
