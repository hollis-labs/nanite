import { ShieldAlert, ShieldCheck, ShieldX } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ResponseStatus } from "@/lib/envelope-response";
import type { EnvelopeApprovalRequest } from "@/lib/types";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

interface ApprovalCardProps {
  approval: EnvelopeApprovalRequest;
  onRespond?: EnvelopeResponder;
}

const RISK_STYLES = {
  low: {
    bg: "bg-success/15",
    text: "text-success",
    border: "border-success/25",
    icon: ShieldCheck,
  },
  medium: {
    bg: "bg-warning/15",
    text: "text-warning",
    border: "border-warning/25",
    icon: ShieldAlert,
  },
  high: { bg: "bg-primary/15", text: "text-primary", border: "border-primary/25", icon: ShieldX },
};

export function ApprovalCard({ approval, onRespond }: ApprovalCardProps) {
  const [decision, setDecision] = useState<"pending" | "approved" | "rejected">("pending");
  const [submitError, setSubmitError] = useState<string | null>(null);

  const risk = approval.risk_level || "low";
  const riskStyle = RISK_STYLES[risk];
  const RiskIcon = riskStyle.icon;

  const respond = async (approved: boolean) => {
    setDecision(approved ? "approved" : "rejected");
    setSubmitError(null);
    if (!onRespond) return;
    try {
      await onRespond({
        status: ResponseStatus.Submitted,
        data: { approved, description: approval.description },
      });
    } catch (err) {
      setDecision("pending");
      setSubmitError(err instanceof Error ? err.message : "Failed to submit approval");
    }
  };

  if (decision === "approved") {
    return (
      <div className="rounded-sm border border-success/30 bg-success/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-success" />
          <span className="text-sm text-success">Approved: {approval.description}</span>
        </div>
      </div>
    );
  }

  if (decision === "rejected") {
    return (
      <div className="rounded-sm border border-primary/30 bg-primary/5 p-4">
        <div className="flex items-center gap-2">
          <ShieldX className="w-4 h-4 text-primary" />
          <span className="text-sm text-primary">Rejected: {approval.description}</span>
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4">
      {/* Header with risk badge */}
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-sm font-medium text-fg">Approval Required</h4>
        <div
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full text-xs ${riskStyle.bg} border ${riskStyle.border}`}
        >
          <RiskIcon className={`w-3 h-3 ${riskStyle.text}`} />
          <span className={riskStyle.text}>{risk} risk</span>
        </div>
      </div>

      {/* Description */}
      <p className="text-sm text-fg-secondary mb-2">{approval.description}</p>
      {approval.details && <p className="text-xs text-fg-muted mb-4">{approval.details}</p>}

      {submitError && (
        <p className="text-xs text-danger mb-2" role="alert">
          {submitError}
        </p>
      )}

      {/* Actions */}
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          className="bg-success hover:bg-success/80 text-white text-xs px-3 py-1 h-7"
          onClick={() => void respond(true)}
        >
          Approve
        </Button>
        <Button
          size="sm"
          className="bg-primary hover:bg-primary-hover text-white text-xs px-3 py-1 h-7"
          onClick={() => void respond(false)}
        >
          Reject
        </Button>
      </div>
    </div>
  );
}
