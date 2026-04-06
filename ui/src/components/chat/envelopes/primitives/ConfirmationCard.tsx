import { useState } from 'react'
import { AlertTriangle, CheckCircle, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface ConfirmationCardData {
  title: string
  message: string
  confirm_label?: string
  cancel_label?: string
  risk?: 'low' | 'medium' | 'high'
}

interface ConfirmationCardProps {
  data: ConfirmationCardData
  onSendMessage?: (content: string) => void
}

const RISK_BUTTON_STYLES: Record<string, string> = {
  low:    'bg-success hover:bg-success/80 text-white',
  medium: 'bg-warning hover:bg-warning/80 text-white',
  high:   'bg-danger hover:bg-danger/80 text-white',
}

export function ConfirmationCard({ data, onSendMessage }: ConfirmationCardProps) {
  const [decision, setDecision] = useState<'pending' | 'confirmed' | 'cancelled'>('pending')

  const risk = data.risk || 'low'
  const confirmLabel = data.confirm_label || 'Confirm'
  const cancelLabel = data.cancel_label || 'Cancel'

  const handleConfirm = () => {
    setDecision('confirmed')
    onSendMessage?.(`confirm:${data.title}`)
  }

  const handleCancel = () => {
    setDecision('cancelled')
    onSendMessage?.(`cancel:${data.title}`)
  }

  if (decision === 'confirmed') {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <div className="flex items-center gap-2">
          <CheckCircle className="w-4 h-4 text-success" />
          <span className="text-sm text-success">Confirmed: {data.title}</span>
        </div>
      </div>
    )
  }

  if (decision === 'cancelled') {
    return (
      <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
        <div className="flex items-center gap-2">
          <XCircle className="w-4 h-4 text-fg-muted" />
          <span className="text-sm text-fg-muted">Cancelled: {data.title}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      <div className="flex items-center gap-2 mb-2">
        {risk === 'high' && <AlertTriangle className="w-4 h-4 text-danger shrink-0" />}
        <h4 className="text-sm font-medium text-fg">{data.title}</h4>
      </div>
      <p className="text-sm text-fg-secondary mb-4">{data.message}</p>
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          className={`text-xs px-3 py-1 h-7 ${RISK_BUTTON_STYLES[risk]}`}
          onClick={handleConfirm}
        >
          {confirmLabel}
        </Button>
        <Button
          size="sm"
          variant="outline"
          className="text-xs px-3 py-1 h-7"
          onClick={handleCancel}
        >
          {cancelLabel}
        </Button>
      </div>
    </div>
  )
}
