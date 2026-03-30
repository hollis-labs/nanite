import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  Globe,
  Loader2,
  Plus,
  Trash2,
  AlertCircle,
  Shield,
  ChevronDown,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Empty, EmptyHeader, EmptyMedia, EmptyTitle, EmptyDescription } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'

interface CatalogSourceManagerProps {
  onBack: () => void
}

export function CatalogSourceManager({ onBack }: CatalogSourceManagerProps) {
  const [adding, setAdding] = useState(false)
  const [editingKey, setEditingKey] = useState<string | null>(null) // source ID being key-edited
  const [keyInput, setKeyInput] = useState('')
  const queryClient = useQueryClient()

  const {
    data: sources = [],
    isLoading,
    isError,
    error,
  } = useQuery({
    queryKey: ['catalog-sources'],
    queryFn: api.listCatalogSources,
    staleTime: 30_000,
  })

  const invalidate = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ['catalog-sources'] })
    void queryClient.invalidateQueries({ queryKey: ['catalog-browse'] })
  }, [queryClient])

  const deleteMutation = useMutation({
    mutationFn: api.deleteCatalogSource,
    onSuccess: invalidate,
  })

  const toggleMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api.updateCatalogSource(id, { enabled }),
    onSuccess: invalidate,
  })

  const setKeyMutation = useMutation({
    mutationFn: ({ id, publicKey }: { id: string; publicKey: string }) =>
      api.setCatalogSourceKey(id, publicKey),
    onSuccess: () => {
      setEditingKey(null)
      setKeyInput('')
      invalidate()
    },
  })

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <button
          onClick={onBack}
          className="p-1.5 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
        >
          <ArrowLeft className="w-4 h-4" />
        </button>
        <h3 className="text-sm font-semibold text-fg">Catalog Sources</h3>
        <div className="flex-1" />
        <Button
          variant="ghost"
          size="sm"
          className="text-xs text-fg-secondary hover:text-fg"
          onClick={() => setAdding(true)}
        >
          <Plus className="w-3.5 h-3.5 mr-1" />
          Add Source
        </Button>
      </div>

      <p className="text-[11px] text-fg-faint leading-relaxed">
        Catalog sources serve plugin.yaml indexes. Higher priority sources win when the same plugin appears in multiple catalogs.
      </p>

      {/* Error */}
      {isError && (
        <div className="rounded-xl border border-red-500/30 bg-red-500/5 p-4 flex items-start gap-3">
          <AlertCircle className="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
          <p className="text-xs text-fg-muted">{(error as Error)?.message}</p>
        </div>
      )}

      {/* Loading */}
      {isLoading && (
        <div className="space-y-3">
          {Array.from({ length: 2 }).map((_, i) => (
            <div key={i} className="rounded-xl border border-border-subtle bg-bg-elevated/60 shadow-sm p-3.5">
              <Skeleton className="h-3.5 w-1/3 mb-2" />
              <Skeleton className="h-2.5 w-2/3" />
            </div>
          ))}
        </div>
      )}

      {/* Empty */}
      {!isLoading && !isError && sources.length === 0 && !adding && (
        <Empty className="py-10">
          <EmptyHeader>
            <EmptyMedia variant="icon"><Globe /></EmptyMedia>
            <EmptyTitle className="text-sm">No catalog sources</EmptyTitle>
            <EmptyDescription className="text-xs">
              Add a source URL to browse available plugins.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}

      {/* Add source form */}
      {adding && (
        <AddSourceForm
          onDone={() => {
            setAdding(false)
            invalidate()
          }}
          onCancel={() => setAdding(false)}
        />
      )}

      {/* Source list */}
      {!isLoading && sources.length > 0 && (
        <div className="space-y-3">
          {sources.map((source) => (
            <div
              key={source.id}
              className={`rounded-xl border shadow-sm overflow-hidden transition-all ${
                source.enabled
                  ? 'border-border-subtle bg-white dark:bg-bg-elevated/60'
                  : 'border-border-subtle bg-white dark:bg-bg-elevated/60 opacity-55'
              }`}
            >
              {/* Header */}
              <div className="flex items-center gap-2.5 px-3.5 py-3">
                <span className={`inline-flex items-center justify-center w-9 h-9 rounded-lg shrink-0 ${
                  source.enabled ? 'bg-zinc-700 text-zinc-300' : 'bg-zinc-300 text-zinc-500'
                }`}>
                  <Globe className="w-4 h-4" />
                </span>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-semibold text-fg truncate">{source.name}</span>
                    {source.enabled && <span className="w-1.5 h-1.5 rounded-full bg-success shrink-0" />}
                    <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-bg-elevated border border-border-subtle text-fg-muted leading-none">
                      {source.type}
                    </span>
                  </div>
                  <p className="text-[11px] text-fg-muted truncate mt-0.5">{source.url}</p>
                </div>

                {/* Actions */}
                <div className="flex items-center gap-1 shrink-0">
                  <span className="text-[10px] text-fg-faint mr-1">pri: {source.priority}</span>
                  <button
                    onClick={() => toggleMutation.mutate({ id: source.id, enabled: !source.enabled })}
                    disabled={toggleMutation.isPending}
                    className={`relative w-8 h-4.5 rounded-full transition-colors ${
                      source.enabled ? 'bg-toggle-on' : 'bg-zinc-700 hover:bg-zinc-600'
                    }`}
                  >
                    <span className={`absolute top-0.5 w-3.5 h-3.5 rounded-full bg-white shadow transition-transform ${
                      source.enabled ? 'left-[calc(100%-1rem)]' : 'left-0.5'
                    }`} />
                  </button>
                  {source.type === 'custom' && (
                    <button
                      onClick={() => deleteMutation.mutate(source.id)}
                      disabled={deleteMutation.isPending}
                      className="p-1.5 rounded text-fg-faint hover:text-red-400 hover:bg-red-500/10 transition-colors"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
              </div>

              {/* Detail footer */}
              <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5">
                    <Shield className="w-3 h-3 text-fg-faint" />
                    <span className="text-[11px] text-fg-muted">
                      {source.public_key ? 'Signed (key set)' : 'No verification key'}
                    </span>
                  </div>
                  <button
                    onClick={() => {
                      if (editingKey === source.id) {
                        setEditingKey(null)
                      } else {
                        setEditingKey(source.id)
                        setKeyInput(source.public_key || '')
                      }
                    }}
                    className="text-[10px] text-fg-faint hover:text-fg-muted transition-colors"
                  >
                    <ChevronDown className={`w-3 h-3 transition-transform ${editingKey === source.id ? 'rotate-180' : ''}`} />
                  </button>
                </div>

                {/* Inline key editor */}
                {editingKey === source.id && (
                  <div className="mt-2 flex items-center gap-2">
                    <input
                      type="text"
                      placeholder="Ed25519 public key (64 hex chars)"
                      value={keyInput}
                      onChange={(e) => setKeyInput(e.target.value)}
                      className="flex-1 bg-surface/50 border border-border rounded-md px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-faint font-mono focus:outline-none focus:ring-1 focus:ring-border-subtle"
                    />
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-xs"
                      disabled={setKeyMutation.isPending}
                      onClick={() => setKeyMutation.mutate({ id: source.id, publicKey: keyInput })}
                    >
                      {setKeyMutation.isPending ? <Loader2 className="w-3 h-3 animate-spin" /> : 'Save'}
                    </Button>
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// --- Add Source Form ---

function AddSourceForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [priority, setPriority] = useState(10)

  const addMutation = useMutation({
    mutationFn: () => api.addCatalogSource(name, url, priority),
    onSuccess: onDone,
  })

  return (
    <div className="rounded-xl border border-border-subtle bg-white dark:bg-bg-elevated/60 shadow-sm overflow-hidden">
      <div className="px-3.5 py-3 space-y-3">
        <div>
          <label className="text-[11px] font-medium text-fg-secondary block mb-1">Name</label>
          <input
            type="text"
            placeholder="My Plugin Catalog"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full bg-surface/50 border border-border rounded-md px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-faint focus:outline-none focus:ring-1 focus:ring-border-subtle"
          />
        </div>
        <div>
          <label className="text-[11px] font-medium text-fg-secondary block mb-1">URL</label>
          <input
            type="url"
            placeholder="https://example.com/catalog.yaml"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            className="w-full bg-surface/50 border border-border rounded-md px-2.5 py-1.5 text-xs text-fg placeholder:text-fg-faint font-mono focus:outline-none focus:ring-1 focus:ring-border-subtle"
          />
        </div>
        <div>
          <label className="text-[11px] font-medium text-fg-secondary block mb-1">Priority</label>
          <input
            type="number"
            min={0}
            max={100}
            value={priority}
            onChange={(e) => setPriority(Number(e.target.value))}
            className="w-20 bg-surface/50 border border-border rounded-md px-2.5 py-1.5 text-xs text-fg focus:outline-none focus:ring-1 focus:ring-border-subtle"
          />
          <span className="text-[10px] text-fg-faint ml-2">Higher wins on conflict</span>
        </div>
      </div>
      <div className="border-t border-border/50 px-3.5 py-2 bg-bg-elevated/40 flex items-center justify-end gap-2">
        {addMutation.isError && (
          <p className="text-[11px] text-red-400 mr-auto">
            {(addMutation.error as Error)?.message}
          </p>
        )}
        <Button variant="ghost" size="sm" className="text-xs" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          size="sm"
          className="text-xs bg-accent hover:bg-accent-hover text-white"
          disabled={!name || !url || addMutation.isPending}
          onClick={() => addMutation.mutate()}
        >
          {addMutation.isPending ? <Loader2 className="w-3 h-3 animate-spin mr-1" /> : null}
          Add Source
        </Button>
      </div>
    </div>
  )
}
