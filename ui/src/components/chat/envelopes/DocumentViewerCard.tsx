import { useState, useMemo, useCallback } from 'react'
import { FileText, ExternalLink, Download, ChevronRight } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { MessageContent } from '../MessageContent'
import { Envelope, EnvelopeHeader } from './primitives/Envelope'

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
    <div className="animate-in fade-in duration-300">
      <Envelope>
        <EnvelopeHeader
          icon={FileText}
          label="Document"
          tone="info"
          meta={<span className="normal-case font-sans text-fg">{data.title}</span>}
          action={
            <div className="flex items-center gap-1">
              {data.download_enabled && data.download_filename && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={handleDownload}
                  className="h-7 w-7 p-0 text-fg-secondary hover:text-fg"
                  aria-label="Download"
                >
                  <Download className="h-3.5 w-3.5" />
                </Button>
              )}
              <Button
                size="sm"
                variant="ghost"
                onClick={handleOpenNewTab}
                className="h-7 w-7 p-0 text-fg-secondary hover:text-fg"
                aria-label="Open in new tab"
              >
                <ExternalLink className="h-3.5 w-3.5" />
              </Button>
            </div>
          }
        />

        {sectionLinks && (
          <div className="flex items-center gap-1 overflow-x-auto border-b border-border-subtle bg-surface px-3 py-1.5">
            {sectionLinks.map((section) => (
              <button
                key={section}
                type="button"
                onClick={() => {
                  const el = document.getElementById(`doc-section-${section}`)
                  el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
                }}
                className="flex shrink-0 items-center gap-1 rounded-[4px] px-2 py-1 text-[12px] text-fg-secondary transition-colors hover:bg-surface-hover hover:text-fg"
              >
                <ChevronRight className="h-3 w-3" />
                {section}
              </button>
            ))}
          </div>
        )}

        {expanded && (
          <div className="chat-scroll max-h-[500px] overflow-y-auto">
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
      </Envelope>
    </div>
  )
}
