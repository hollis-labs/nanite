/**
 * ArtifactMiniCard — C2 (CW-20260428-0013)
 *
 * Compact downloadable-artifact card. Sits in the bottom chat drawer's Cards
 * tab (via render_target=bottom_chat_drawer routing) when the agent emits a
 * fresh artifact. Lifetime is transient: Download or Dismiss closes the card
 * and the drawer reverts to the user's default tab.
 *
 * The durable per-session list lives in the right-rail Artifacts panel; this
 * surface is intentionally ephemeral — "your file is ready, here's a button".
 *
 * Schema: internal/envelope/schemas/artifact-mini.schema.json
 *   data: { artifact_id, name, mime_type, size_bytes?, origin? }
 */

import { useCallback } from 'react'
import { Download, Package, X } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'

interface ArtifactMiniData {
  artifact_id: string
  name: string
  mime_type: string
  size_bytes?: number
  origin?: string
}

interface ArtifactMiniCardProps {
  data: ArtifactMiniData
}

function formatSize(bytes?: number): string {
  if (typeof bytes !== 'number' || !isFinite(bytes) || bytes < 0) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function ArtifactMiniCard({ data }: ArtifactMiniCardProps) {
  const clearPanelEnvelopes = useLayoutStore((s) => s.clearPanelEnvelopes)
  const setBottomDrawerOpen = useLayoutStore((s) => s.setBottomDrawerOpen)

  const downloadUrl = data?.artifact_id ? `/api/artifacts/${data.artifact_id}/download` : ''

  // Dismiss closes the transient card by clearing the bottom-drawer panel
  // envelope list. The right-rail Artifacts panel still shows the artifact —
  // mini is ephemeral, right-rail is durable (per spec).
  const handleDismiss = useCallback(() => {
    clearPanelEnvelopes('bottom_chat_drawer')
  }, [clearPanelEnvelopes])

  // After Download fires the browser-native flow, close the transient card and
  // close the bottom drawer to revert to the chat surface. We use 'agent'
  // source for the close so a user-opened drawer is preserved (J8 rule).
  const handleDownloadClick = useCallback(() => {
    // Defer the clear so the <a download> click completes first.
    setTimeout(() => {
      clearPanelEnvelopes('bottom_chat_drawer')
      setBottomDrawerOpen(false, 'agent')
    }, 0)
  }, [clearPanelEnvelopes, setBottomDrawerOpen])

  if (!data) {
    return (
      <div className="flex items-center gap-2 p-3 border border-border rounded-md bg-bg-elevated/50 text-xs text-fg-muted">
        <Package className="w-4 h-4 text-fg-faint" />
        <span>Artifact data missing</span>
      </div>
    )
  }

  const sizeLabel = formatSize(data.size_bytes)

  return (
    <div className="flex items-start gap-3 p-3 border border-border rounded-md bg-bg-elevated/50">
      <Package className="w-5 h-5 text-fg-muted shrink-0 mt-0.5" />
      <div className="flex-1 min-w-0">
        <p className="text-sm text-fg truncate font-medium">{data.name}</p>
        <div className="flex items-center gap-2 mt-0.5 text-xs text-fg-faint">
          {data.mime_type && <span>{data.mime_type}</span>}
          {sizeLabel && <span>{sizeLabel}</span>}
          {data.origin && <span className="text-fg-muted">· {data.origin}</span>}
        </div>
      </div>
      <div className="flex items-center gap-1 shrink-0">
        {downloadUrl && (
          <a
            href={downloadUrl}
            download={data.name}
            onClick={handleDownloadClick}
            className="flex items-center gap-1 px-2 py-1 rounded text-xs text-primary hover:text-primary-hover hover:bg-surface transition-colors"
            aria-label={`Download ${data.name}`}
          >
            <Download className="w-3.5 h-3.5" />
            Download
          </a>
        )}
        <button
          type="button"
          onClick={handleDismiss}
          className="flex items-center gap-1 px-2 py-1 rounded text-xs text-fg-muted hover:text-fg hover:bg-surface transition-colors"
          aria-label="Dismiss artifact"
        >
          <X className="w-3.5 h-3.5" />
          Dismiss
        </button>
      </div>
    </div>
  )
}
