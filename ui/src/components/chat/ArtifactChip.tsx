import { FileText, FileCode, FileImage, File } from 'lucide-react'
import { useLayoutStore } from '@/stores/useLayoutStore'

function getMimeIcon(mimeType: string) {
  if (mimeType.startsWith('text/')) return FileText
  if (mimeType.includes('json') || mimeType.includes('javascript') || mimeType.includes('typescript'))
    return FileCode
  if (mimeType.startsWith('image/')) return FileImage
  return File
}

interface ArtifactChipProps {
  name: string
  mimeType?: string
}

export function ArtifactChip({ name, mimeType = 'application/octet-stream' }: ArtifactChipProps) {
  const setOpen = useLayoutStore((s) => s.setArtifactsDrawer)
  const Icon = getMimeIcon(mimeType)

  return (
    <button
      type="button"
      onClick={() => setOpen(true)}
      className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-surface border border-border-subtle hover:border-border-subtle hover:bg-surface-hover transition-colors text-xs text-fg-secondary hover:text-fg cursor-pointer"
    >
      <Icon className="w-3 h-3 text-fg-muted" />
      <span className="font-mono truncate max-w-[200px]">{name}</span>
    </button>
  )
}
