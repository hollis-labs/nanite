import { useState } from 'react'
import { ShieldCheck, ShieldAlert, ShieldX } from 'lucide-react'
import { Button } from '@/components/ui/button'
import type { EnvelopeApprovalRequest } from '@/lib/types'

interface ApprovalCardProps {
  approval: EnvelopeApprovalRequest
}

const RISK_STYLES = {
  low: { bg: 'bg-success/15', text: 'text-success', border: 'border-success/25', icon: ShieldCheck },
  medium: { bg: 'bg-amber-500/15', text: 'text-amber-400', border: 'border-amber-500/25', icon: ShieldAlert },
  high: { bg: 'bg-accent/15', text: 'text-accent', border: 'border-accent/25', icon: ShieldX },
}

export function ApprovalCard({ approval }: ApprovalCardProps) {
  const [decision, setDecision] = useState<'pending' | 'approved' | 'rejected'>('pending')

  const risk = approval.risk_level || 'low'
  const riskStyle = RISK_STYLES[risk]
  const RiskIcon = riskStyle.icon

  const handleApprove = () => {
    console.log('[ApprovalCard] Approved:', approval.description)
    setDecision('approved')
  }

  const handleReject = () => {
    console.log('[ApprovalCard] Rejected:', approval.description)
    setDecision('rejected')
  }

  if (decision === 'approved') {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-success" />
          <span className="text-sm text-success">Approved: {approval.description}</span>
        </div>
      </div>
    )
  }

  if (decision === 'rejected') {
    return (
      <div className="rounded-sm border border-accent/30 bg-accent/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldX className="w-4 h-4 text-accent" />
          <span className="text-sm text-accent">Rejected: {approval.description}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {/* Header with risk badge */}
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-sm font-medium text-fg">Approval Required</h4>
        <div className={`flex items-center gap-1 px-2 py-0.5 rounded-full text-xs ${riskStyle.bg} border ${riskStyle.border}`}>
          <RiskIcon className={`w-3 h-3 ${riskStyle.text}`} />
          <span className={riskStyle.text}>{risk} risk</span>
        </div>
      </div>

      {/* Description */}
      <p className="text-sm text-fg-secondary mb-2">{approval.description}</p>
      {approval.details && (
        <p className="text-xs text-fg-muted mb-4">{approval.details}</p>
      )}

      {/* Actions */}
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          className="bg-success hover:bg-success/80 text-white text-xs px-3 py-1 h-7"
          onClick={handleApprove}
        >
          Approve
        </Button>
        <Button
          size="sm"
          className="bg-accent hover:bg-accent-hover text-white text-xs px-3 py-1 h-7"
          onClick={handleReject}
        >
          Reject
        </Button>
      </div>
    </div>
  )
}
