import { useState, useMemo, useCallback } from 'react'
import { FileText, ExternalLink, Download, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { MessageContent } from '../MessageContent'

interface DocumentViewerData {
  title: string
  content: string
  format?: 'html' | 'markdown'
  sections?: string[]
  download_filename?: string
  download_enabled?: boolean
}

interface DocumentViewerCardProps {
  data: DocumentViewerData
  onSendMessage?: (content: string) => void
}

export function DocumentViewerCard({ data }: DocumentViewerCardProps) {
  const [expanded] = useState(true)
  const isHTML = (data.format || 'markdown') === 'html'

  const handleDownload = useCallback(() => {
    if (!data.download_enabled || !data.download_filename) return
    const blob = new Blob([data.content], {
      type: isHTML ? 'text/html' : 'text/markdown',
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = data.download_filename
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }, [data.content, data.download_filename, data.download_enabled, isHTML])

  const handleOpenNewTab = useCallback(() => {
    const blob = new Blob([data.content], {
      type: isHTML ? 'text/html' : 'text/plain',
    })
    const url = URL.createObjectURL(blob)
    window.open(url, '_blank')
  }, [data.content, isHTML])

  const sectionLinks = useMemo(() => {
    if (!data.sections || data.sections.length === 0) return null
    return data.sections
  }, [data.sections])

  return (
    <div className="animate-in fade-in duration-300 space-y-2">
      {/* Header */}
      <div className="rounded-lg border border-indigo-500/20 bg-zinc-900/50 overflow-hidden">
        <div className="flex items-center justify-between px-4 py-3 border-b border-zinc-800">
          <div className="flex items-center gap-2">
            <FileText className="h-4 w-4 text-indigo-400 shrink-0" />
            <span className="text-sm font-medium text-zinc-200">{data.title}</span>
          </div>
          <div className="flex items-center gap-1">
            {data.download_enabled && data.download_filename && (
              <Button
                size="sm"
                variant="ghost"
                onClick={handleDownload}
                className="h-7 px-2 text-zinc-400 hover:text-zinc-200"
              >
                <Download className="h-3.5 w-3.5" />
              </Button>
            )}
            <Button
              size="sm"
              variant="ghost"
              onClick={handleOpenNewTab}
              className="h-7 px-2 text-zinc-400 hover:text-zinc-200"
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>

        {/* Section nav */}
        {sectionLinks && (
          <div className="flex items-center gap-1 px-4 py-2 border-b border-zinc-800/50 bg-zinc-900/30 overflow-x-auto">
            {sectionLinks.map(section => (
              <button
                key={section}
                type="button"
                onClick={() => {
                  const el = document.getElementById(`doc-section-${section}`)
                  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
                }}
                className="flex items-center gap-1 rounded px-2 py-1 text-xs text-zinc-400 hover:text-zinc-200 hover:bg-zinc-800 whitespace-nowrap transition-colors"
              >
                <ChevronRight className="h-3 w-3" />
                {section}
              </button>
            ))}
          </div>
        )}

        {/* Content */}
        {expanded && (
          <div className="max-h-[500px] overflow-y-auto chat-scroll">
            <div className="px-4 py-3">
              {isHTML ? (
                <div
                  className="prose prose-invert prose-sm max-w-none"
                  dangerouslySetInnerHTML={{ __html: data.content }}
                />
              ) : (
                <MessageContent content={data.content} role="assistant" />
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
