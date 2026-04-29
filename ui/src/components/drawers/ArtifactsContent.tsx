import { useEffect, useState } from 'react'
import { FileText, FileCode, FileImage, File, Download, Package, Eye, ArrowLeft, Upload } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { useArtifactUpload } from '@/hooks/useArtifactUpload'
import { useAppStore } from '@/stores/useAppStore'
import { api } from '@/lib/api'
import type { Artifact } from '@/lib/types'

function getMimeIcon(mimeType: string) {
  if (mimeType.startsWith('text/')) return FileText
  if (mimeType.includes('json') || mimeType.includes('javascript') || mimeType.includes('typescript'))
    return FileCode
  if (mimeType.startsWith('image/')) return FileImage
  return File
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function isPreviewable(mimeType: string): boolean {
  return (
    mimeType.startsWith('image/') ||
    mimeType.startsWith('text/') ||
    mimeType.includes('json') ||
    mimeType.includes('javascript') ||
    mimeType.includes('typescript') ||
    mimeType.includes('markdown') ||
    mimeType.includes('yaml') ||
    mimeType.includes('xml')
  )
}

function ArtifactPreview({ artifact }: { artifact: Artifact }) {
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const downloadUrl = `/api/artifacts/${artifact.id}/download`

  useEffect(() => {
    setLoading(true)
    setContent(null)

    if (artifact.mime_type.startsWith('image/')) {
      setLoading(false)
      return
    }

    fetch(downloadUrl)
      .then((res) => res.text())
      .then((text) => {
        setContent(text)
        setLoading(false)
      })
      .catch(() => {
        setContent(null)
        setLoading(false)
      })
  }, [artifact.id, artifact.mime_type, downloadUrl])

  if (loading) {
    return (
      <div className="p-4 space-y-3">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-3/4" />
        <Skeleton className="h-4 w-5/6" />
      </div>
    )
  }

  if (artifact.mime_type.startsWith('image/')) {
    return (
      <div className="flex items-center justify-center p-4">
        <img
          src={downloadUrl}
          alt={artifact.name}
          className="max-w-full max-h-[60vh] rounded-sm border border-border"
        />
      </div>
    )
  }

  if (content !== null) {
    return (
      <pre className="p-4 text-xs font-mono text-fg-secondary bg-bg rounded-sm border border-border overflow-auto max-h-[60vh] whitespace-pre-wrap break-words">
        {content}
      </pre>
    )
  }

  return <p className="text-xs text-fg-muted italic p-4">Unable to preview this file</p>
}

function ArtifactRow({
  artifact,
  onPreview,
}: {
  artifact: Artifact
  onPreview: (a: Artifact) => void
}) {
  const Icon = getMimeIcon(artifact.mime_type)
  const canPreview = isPreviewable(artifact.mime_type)

  return (
    <div className="flex items-center gap-3 p-3 rounded-sm bg-bg-elevated/50 border border-border hover:border-border-subtle transition-colors">
      <Icon className="w-5 h-5 text-fg-muted shrink-0" />
      <div className="flex-1 min-w-0">
        <p className="text-sm text-fg truncate">{artifact.name}</p>
        <div className="flex items-center gap-2 mt-0.5">
          <span className="text-xs text-fg-faint">{artifact.mime_type}</span>
          <span className="text-xs text-fg-faint">{formatSize(artifact.size_bytes)}</span>
        </div>
      </div>
      <div className="flex items-center gap-1 shrink-0">
        {canPreview && (
          <button
            type="button"
            onClick={() => onPreview(artifact)}
            className="p-1.5 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
            aria-label={`Preview ${artifact.name}`}
          >
            <Eye className="w-4 h-4" />
          </button>
        )}
        <a
          href={`/api/artifacts/${artifact.id}/download`}
          download
          className="p-1.5 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
          aria-label={`Download ${artifact.name}`}
        >
          <Download className="w-4 h-4" />
        </a>
      </div>
    </div>
  )
}

interface ArtifactsContentProps {
  onTitleChange?: (title: string) => void
}

/**
 * ArtifactsContent — right-rail Artifacts panel.
 *
 * F4 (CW-20260429-0004) added two pieces vs. the C2 baseline:
 *
 *   1. Project-inherited artifacts. The panel now renders two sections —
 *      "This Session" and "This Project" — so users opening a new session in
 *      a project still see prior artifacts. The "This Project" section is
 *      omitted entirely when the active session has no project.
 *
 *   2. Dropzone. Files can now be dropped directly on the panel; the upload
 *      flows through the same useArtifactUpload hook used by ChatComposer
 *      and lands in the *current* session (matching the chat-input dropzone
 *      semantics). This works alongside the existing ChatComposer dropzone,
 *      not as a replacement.
 *
 * The C2 mini-card path on the bottom drawer is unchanged. Auto-emitting
 * artifact-mini envelopes from BE artifact creation is explicitly out of
 * scope (decision: keep agent-emit symmetry — see Vanta
 * `decisions.nanite.collab_ui_v1.drawer_cards_implementation_choices`).
 */
export function ArtifactsContent({ onTitleChange }: ArtifactsContentProps) {
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const [previewing, setPreviewing] = useState<Artifact | null>(null)

  // Resolve the active session's project_id so we can fetch project-inherited
  // artifacts. Use the existing ['session', sessionId] cache key — already
  // populated by ChatHeader / ChatTranscript on session switch — so we
  // typically hit the cache instead of re-fetching here.
  const { data: session } = useQuery({
    queryKey: ['session', activeSessionId],
    queryFn: () => api.getSession(activeSessionId!),
    enabled: !!activeSessionId,
    staleTime: 30_000,
  })
  const sessionProjectId = session?.project_id || ''

  // C2 (CW-20260428-0013): refetch every 8s so newly-emitted agent artifacts
  // surface in the right-rail without a page reload. The bottom-drawer
  // mini-card is the immediate ephemeral surface; right-rail is durable but
  // needs to track new arrivals on a tolerable cadence.
  const { data: sessionArtifacts = [], isLoading: sessionLoading } = useQuery({
    queryKey: ['artifacts', activeSessionId],
    queryFn: () => api.listArtifacts(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: 8000,
  })

  // F4 (CW-20260429-0004): project-inherited artifacts (sibling sessions in
  // the same project, excluding the current one to avoid double-counting).
  const { data: projectArtifacts = [], isLoading: projectLoading } = useQuery({
    queryKey: ['artifacts', 'project', sessionProjectId, activeSessionId],
    queryFn: () => api.listArtifactsByProject(sessionProjectId, activeSessionId ?? undefined),
    enabled: !!sessionProjectId,
    refetchInterval: 8000,
  })

  // Dropzone — uploads land in the current session. Reuses the same hook
  // ChatComposer uses so semantics are identical (cache invalidation +
  // serial uploads + error logging).
  const { dragOver, setDragOver, handleDrop, uploading } =
    useArtifactUpload(activeSessionId)

  useEffect(() => {
    onTitleChange?.(previewing ? previewing.name : 'Artifacts')
  }, [previewing, onTitleChange])

  // Reset preview when session changes
  useEffect(() => {
    setPreviewing(null)
  }, [activeSessionId])

  const isLoading = sessionLoading || (!!sessionProjectId && projectLoading)
  const hasAnyArtifacts = sessionArtifacts.length > 0 || projectArtifacts.length > 0

  return (
    <div
      className={`relative flex flex-1 min-h-0 flex-col transition-colors ${
        dragOver ? 'bg-primary/5' : ''
      }`}
      onDragOver={(e) => {
        // Only treat the drag as a file drop hint when the dataTransfer
        // actually carries files. This prevents in-app drags (e.g. widget
        // reorder, todo reorder) from accidentally triggering the dropzone.
        if (!activeSessionId) return
        if (e.dataTransfer.types.includes('Files')) {
          e.preventDefault()
          setDragOver(true)
        }
      }}
      onDragLeave={(e) => {
        // Only clear when leaving the wrapper itself — child enter/leave
        // events would otherwise flicker the indicator.
        if (e.currentTarget.contains(e.relatedTarget as Node)) return
        setDragOver(false)
      }}
      onDrop={(e) => void handleDrop(e)}
    >
      {/* Drag-over overlay — matches ChatComposer's primary-tinted strip */}
      {dragOver && (
        <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center border-2 border-dashed border-primary bg-primary/10 backdrop-blur-[1px]">
          <div className="flex items-center gap-2 rounded-md bg-bg-elevated px-3 py-2 shadow-lg">
            <Upload className="h-3.5 w-3.5 text-primary" />
            <span className="font-mono text-[11px] font-semibold uppercase tracking-wide text-primary">
              Drop files to upload
            </span>
          </div>
        </div>
      )}

      {/* Upload-in-flight banner — sits above the list so the user sees
          progress feedback even when the dropzone overlay is gone. */}
      {uploading && !dragOver && (
        <div className="flex items-center gap-2 border-b border-border-subtle bg-surface px-3 py-1.5 text-xs text-fg-muted shrink-0">
          <Upload className="h-3 w-3 animate-pulse" />
          <span className="font-mono text-[10px] font-semibold uppercase tracking-wide">
            Uploading…
          </span>
        </div>
      )}

      {previewing && (
        <div className="px-3 pt-2 shrink-0">
          <button
            type="button"
            onClick={() => setPreviewing(null)}
            className="flex items-center gap-1.5 text-xs text-fg-muted hover:text-fg transition-colors"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
            Back to list
          </button>
        </div>
      )}
      <ScrollArea className="flex-1 min-h-0">
        <div className="p-3 space-y-2">
          {previewing ? (
            <ArtifactPreview artifact={previewing} />
          ) : (
            <>
              {isLoading && !hasAnyArtifacts && (
                <div className="space-y-2">
                  {Array.from({ length: 3 }).map((_, i) => (
                    <div key={i} className="flex items-center gap-3 p-3 rounded-sm">
                      <Skeleton className="w-5 h-5 rounded" />
                      <div className="flex-1 space-y-1.5">
                        <Skeleton className="h-3.5 w-32" />
                        <Skeleton className="h-2.5 w-20" />
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {!isLoading && !hasAnyArtifacts && (
                <div className="text-center py-12">
                  <Package className="w-10 h-10 text-fg-faint mx-auto mb-3" />
                  <p className="text-sm text-fg-muted">Artifacts will appear here</p>
                  <p className="text-xs text-fg-faint mt-1">
                    Drop files here or use the chat composer to upload
                  </p>
                </div>
              )}

              {/* This Session — always shown when there are session artifacts.
                  We intentionally do not show an empty "This Session" header
                  when there are no session-scoped artifacts but the project
                  has them; the "This Project" header alone is clearer. */}
              {sessionArtifacts.length > 0 && (
                <SectionHeader label="This Session" count={sessionArtifacts.length} />
              )}
              {sessionArtifacts.map((artifact) => (
                <ArtifactRow
                  key={artifact.id}
                  artifact={artifact}
                  onPreview={setPreviewing}
                />
              ))}

              {/* This Project — omitted entirely when session has no project,
                  per F4 spec (avoid empty header noise). */}
              {sessionProjectId && projectArtifacts.length > 0 && (
                <>
                  {sessionArtifacts.length > 0 && <div className="h-1" />}
                  <SectionHeader label="This Project" count={projectArtifacts.length} />
                  {projectArtifacts.map((artifact) => (
                    <ArtifactRow
                      key={artifact.id}
                      artifact={artifact}
                      onPreview={setPreviewing}
                    />
                  ))}
                </>
              )}
            </>
          )}
        </div>
      </ScrollArea>
    </div>
  )
}

function SectionHeader({ label, count }: { label: string; count: number }) {
  return (
    <div className="flex items-center justify-between px-1 pb-1 pt-1 first:pt-0">
      <span className="font-mono text-[10px] font-semibold uppercase tracking-wide text-fg-faint">
        {label}
      </span>
      <span className="font-mono text-[10px] text-fg-faint">{count}</span>
    </div>
  )
}
