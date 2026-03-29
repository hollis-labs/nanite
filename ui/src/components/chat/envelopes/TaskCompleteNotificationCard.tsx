import { useState } from 'react'
import { CheckCircle2, XCircle, FileText, X } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface TaskCompleteNotificationData {
  title: string
  run_id: string
  blueprint_id?: string
  status: 'done' | 'error'
  completed_at?: string
  has_output?: boolean
  output_preview?: string
  prompt_text?: string
  report_content?: string
}

interface TaskCompleteNotificationCardProps {
  data: TaskCompleteNotificationData
  onSendMessage?: (content: string) => void
}

function formatTimestamp(iso: string): string {
  try {
    const d = new Date(iso)
    return d.toLocaleString('en-US', {
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    })
  } catch {
    return iso
  }
}

export function TaskCompleteNotificationCard({ data, onSendMessage }: TaskCompleteNotificationCardProps) {
  const [dismissed, setDismissed] = useState(false)
  const isError = data.status === 'error'

  if (dismissed) {
    return null
  }

  return (
    <div className="animate-in fade-in slide-in-from-bottom-2 duration-500">
      <div className={`rounded-sm border overflow-hidden ${
        isError
          ? 'border-red-500/30 bg-red-500/5'
          : 'border-success/30 bg-success/5'
      }`}>
        <div className="px-4 py-3">
          {/* Header row */}
          <div className="flex items-start justify-between gap-3">
            <div className="flex items-center gap-2">
              {isError ? (
                <XCircle className="h-5 w-5 text-red-400 shrink-0" />
              ) : (
                <CheckCircle2 className="h-5 w-5 text-success shrink-0" />
              )}
              <div>
                <p className={`text-sm font-medium ${isError ? 'text-red-300' : 'text-success'}`}>
                  {data.title}
                </p>
                {data.completed_at && (
                  <p className="text-xs text-fg-muted mt-0.5">
                    Completed at {formatTimestamp(data.completed_at)}
                  </p>
                )}
              </div>
            </div>
            <button
              type="button"
              onClick={() => setDismissed(true)}
              className="text-fg-faint hover:text-fg-secondary transition-colors"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Preview */}
          {data.output_preview && (
            <p className="mt-2 text-xs text-fg-secondary line-clamp-3 ml-7">
              {data.output_preview}
            </p>
          )}

          {/* Prompt + actions */}
          {data.has_output && !isError && (
            <div className="mt-3 ml-7 flex items-center gap-2">
              {data.prompt_text && (
                <span className="text-xs text-fg-secondary">{data.prompt_text}</span>
              )}
              <Button
                size="sm"
                onClick={() => {
                  if (onSendMessage) {
                    onSendMessage(`SHOW_REPORT:${data.run_id}`)
                  }
                }}
                className="bg-success hover:bg-success/80 text-white text-xs px-3 py-1 h-7"
              >
                <FileText className="mr-1.5 h-3 w-3" />
                Show Report
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
