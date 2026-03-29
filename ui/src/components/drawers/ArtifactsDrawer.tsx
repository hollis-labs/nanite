import { useEffect, useCallback, useState } from 'react'
import { X, FileText, FileCode, FileImage, File, Download, Package, Eye, ArrowLeft } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { ScrollArea } from '@/components/ui/ScrollArea'
import { useLayoutStore } from '@/stores/useLayoutStore'
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
      <div className="flex items-center justify-center py-12">
        <div className="w-5 h-5 border-2 border-border-subtle border-t-zinc-400 rounded-full animate-spin" />
      </div>
    )
  }

  if (artifact.mime_type.startsWith('image/')) {
    return (
      <div className="flex items-center justify-center p-4">
        <img
          src={downloadUrl}
          alt={artifact.name}
          className="max-w-full max-h-[60vh] rounded-lg border border-border"
        />
      </div>
    )
  }

  if (content !== null) {
    return (
      <pre className="p-4 text-xs font-mono text-fg-secondary bg-bg rounded-lg border border-border overflow-auto max-h-[60vh] whitespace-pre-wrap break-words">
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
    <div className="flex items-center gap-3 p-3 rounded-lg bg-bg-elevated/50 border border-border hover:border-border-subtle transition-colors">
      <Icon className="w-5 h-5 text-fg-muted shrink-0" />
      <div className="flex-1 min-w-0">
        <p className="text-sm text-fg truncate">{artifact.name}</p>
        <div className="flex items-center gap-2 mt-0.5">
          <span className="text-xs text-fg-faint">{artifact.mime_type}</span>
          <span className="text-xs text-fg-faint">{formatSize(artifact.size)}</span>
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

export function ArtifactsDrawer() {
  const open = useLayoutStore((s) => s.artifactsDrawerOpen)
  const setOpen = useLayoutStore((s) => s.setArtifactsDrawer)
  const activeSessionId = useAppStore((s) => s.activeSessionId)
  const [previewing, setPreviewing] = useState<Artifact | null>(null)

  const { data: artifacts = [], isLoading } = useQuery({
    queryKey: ['artifacts', activeSessionId],
    queryFn: () => api.listArtifacts(activeSessionId!),
    enabled: !!activeSessionId && open,
  })

  const handleClose = useCallback(() => {
    setOpen(false)
    setPreviewing(null)
  }, [setOpen])

  // Close on Escape
  useEffect(() => {
    if (!open) return
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        if (previewing) {
          setPreviewing(null)
        } else {
          handleClose()
        }
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [open, handleClose, previewing])

  // Reset preview when session changes
  useEffect(() => {
    setPreviewing(null)
  }, [activeSessionId])

  if (!open) return null

  return (
      <div className="fixed inset-y-0 right-0 w-96 bg-bg border-l border-border z-50 flex flex-col shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between px-4 h-12 border-b border-border shrink-0">
          <div className="flex items-center gap-2">
            {previewing ? (
              <button
                type="button"
                onClick={() => setPreviewing(null)}
                className="p-0.5 rounded text-fg-muted hover:text-fg transition-colors"
                aria-label="Back to list"
              >
                <ArrowLeft className="w-4 h-4" />
              </button>
            ) : (
              <Package className="w-4 h-4 text-fg-secondary" />
            )}
            <h2 className="text-sm font-semibold text-fg truncate">
              {previewing ? previewing.name : 'Artifacts'}
            </h2>
          </div>
          <button
            onClick={handleClose}
            className="p-1 rounded text-fg-muted hover:text-fg hover:bg-surface transition-colors"
            aria-label="Close artifacts drawer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Content */}
        <ScrollArea className="flex-1 min-h-0">
          <div className="p-3 space-y-2">
            {previewing ? (
              <ArtifactPreview artifact={previewing} />
            ) : (
              <>
                {isLoading && (
                  <div className="flex items-center justify-center py-12">
                    <div className="w-5 h-5 border-2 border-border-subtle border-t-zinc-400 rounded-full animate-spin" />
                  </div>
                )}

                {!isLoading && artifacts.length === 0 && (
                  <div className="text-center py-12">
                    <Package className="w-10 h-10 text-zinc-800 mx-auto mb-3" />
                    <p className="text-sm text-fg-muted">Artifacts will appear here</p>
                    <p className="text-xs text-fg-faint mt-1">
                      Files and outputs generated during your session
                    </p>
                  </div>
                )}

                {artifacts.map((artifact) => (
                  <ArtifactRow
                    key={artifact.id}
                    artifact={artifact}
                    onPreview={setPreviewing}
                  />
                ))}
              </>
            )}
          </div>
        </ScrollArea>
      </div>
  )
}
