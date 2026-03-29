import { useState } from 'react'
import { ShieldCheck, ShieldAlert, ShieldX } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import type { ApprovalRequest } from '@/lib/types'

interface ApprovalCardProps {
  approval: ApprovalRequest
}

const RISK_STYLES = {
  low: { bg: 'bg-green-500/15', text: 'text-green-400', border: 'border-green-500/25', icon: ShieldCheck },
  medium: { bg: 'bg-amber-500/15', text: 'text-amber-400', border: 'border-amber-500/25', icon: ShieldAlert },
  high: { bg: 'bg-red-500/15', text: 'text-red-400', border: 'border-red-500/25', icon: ShieldX },
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
      <div className="rounded-lg border border-green-500/30 bg-green-500/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-green-400" />
          <span className="text-sm text-green-400">Approved: {approval.description}</span>
        </div>
      </div>
    )
  }

  if (decision === 'rejected') {
    return (
      <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldX className="w-4 h-4 text-red-400" />
          <span className="text-sm text-red-400">Rejected: {approval.description}</span>
        </div>
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-border-subtle bg-bg-elevated/50 p-4">
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
          className="bg-green-600 hover:bg-green-500 text-white text-xs px-3 py-1 h-7"
          onClick={handleApprove}
        >
          Approve
        </Button>
        <Button
          size="sm"
          className="bg-red-600 hover:bg-red-500 text-white text-xs px-3 py-1 h-7"
          onClick={handleReject}
        >
          Reject
        </Button>
      </div>
    </div>
  )
}
