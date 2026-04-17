import { ChevronDown, ChevronRight, ShieldAlert, ShieldCheck, ShieldX } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ResponseStatus } from "@/lib/envelope-response";
import type { EnvelopeResponder } from "./EnvelopeRenderer";

export type SubagentSpawnApprovalData = {
  run_id: string;
  role: string;
  prompt: string;
  mode: "sync" | "async" | "api" | "interactive";
  parent_agent_id?: string;
  timeout_seconds?: number;
  inputs_json?: string;
  risk_level?: "low" | "medium" | "high";
};

interface SubagentSpawnApprovalCardProps {
  data: SubagentSpawnApprovalData;
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
  high: {
    bg: "bg-primary/15",
    text: "text-primary",
    border: "border-primary/25",
    icon: ShieldX,
  },
};

const PROMPT_TRUNCATE_LEN = 200;

export function SubagentSpawnApprovalCard({ data, onRespond }: SubagentSpawnApprovalCardProps) {
  const [decided, setDecided] = useState<"approved" | "rejected" | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [reason, setReason] = useState("");
  const [showFullPrompt, setShowFullPrompt] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const risk = data.risk_level ?? "medium";
  const riskStyle = RISK_STYLES[risk];
  const RiskIcon = riskStyle.icon;

  const submit = async (status: "submitted" | "cancelled") => {
    if (!onRespond) return;
    setSubmitting(true);
    setError(null);
    try {
      if (status === ResponseStatus.Submitted) {
        await onRespond({ status: ResponseStatus.Submitted });
        setDecided("approved");
      } else {
        await onRespond({ status: ResponseStatus.Cancelled, data: { reason } });
        setDecided("rejected");
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to submit");
    } finally {
      setSubmitting(false);
    }
  };

  if (decided) {
    return (
      <div className="rounded-sm border border-border-subtle bg-bg-elevated/30 p-3">
        <div className="text-sm">
          Subagent run <code className="font-mono">{data.run_id}</code> — {decided}.
        </div>
      </div>
    );
  }

  const isLongPrompt = data.prompt.length > PROMPT_TRUNCATE_LEN;
  const displayedPrompt =
    isLongPrompt && !showFullPrompt ? `${data.prompt.slice(0, PROMPT_TRUNCATE_LEN)}…` : data.prompt;
  const hasAdvanced = !!(data.parent_agent_id || data.inputs_json);

  return (
    <div className="rounded-sm border border-border-subtle bg-bg-elevated/50 p-4 space-y-3">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium text-fg">Subagent Spawn Approval</h4>
        <div
          className={`flex items-center gap-1 px-2 py-0.5 rounded-full text-xs ${riskStyle.bg} border ${riskStyle.border}`}
        >
          <RiskIcon className={`w-3 h-3 ${riskStyle.text}`} />
          <span className={riskStyle.text}>{risk} risk</span>
        </div>
      </div>

      {/* Role / Mode / Timeout grid */}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-fg-secondary">
        <span>
          <span className="text-fg-muted">Role:</span>{" "}
          <span className="font-mono text-fg">{data.role}</span>
        </span>
        <span>
          <span className="text-fg-muted">Mode:</span>{" "}
          <span className="font-mono text-fg">{data.mode}</span>
        </span>
        {data.timeout_seconds != null && (
          <span>
            <span className="text-fg-muted">Timeout:</span>{" "}
            <span className="font-mono text-fg">{data.timeout_seconds}s</span>
          </span>
        )}
      </div>

      {/* Prompt */}
      <div>
        <p className="text-xs text-fg-muted mb-1">Prompt</p>
        <pre className="text-xs text-fg bg-surface border border-border-subtle rounded-sm px-2.5 py-2 whitespace-pre-wrap break-words">
          {displayedPrompt}
        </pre>
        {isLongPrompt && (
          <button
            type="button"
            onClick={() => setShowFullPrompt((v) => !v)}
            className="mt-1 text-xs text-primary hover:underline"
          >
            {showFullPrompt ? "collapse" : "show full"}
          </button>
        )}
      </div>

      {/* Advanced collapsible */}
      {hasAdvanced && (
        <div>
          <button
            type="button"
            onClick={() => setShowAdvanced((v) => !v)}
            className="flex items-center gap-1 text-xs text-fg-secondary hover:text-fg"
          >
            {showAdvanced ? (
              <ChevronDown className="w-3 h-3" />
            ) : (
              <ChevronRight className="w-3 h-3" />
            )}
            Advanced
          </button>
          {showAdvanced && (
            <div className="mt-2 space-y-2 text-xs text-fg-secondary">
              {data.parent_agent_id && (
                <div>
                  <span className="text-fg-muted">Parent agent ID:</span>{" "}
                  <code className="font-mono text-fg">{data.parent_agent_id}</code>
                </div>
              )}
              {data.inputs_json && (
                <div>
                  <span className="text-fg-muted block mb-1">Inputs JSON:</span>
                  <pre className="bg-surface border border-border-subtle rounded-sm px-2 py-1.5 whitespace-pre-wrap break-words text-fg">
                    {data.inputs_json}
                  </pre>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      {/* Reason textarea */}
      <textarea
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        rows={2}
        placeholder="Reason (optional, shown to the agent if you reject)"
        className="w-full bg-surface border border-border-subtle rounded-md px-2.5 py-1.5 text-sm text-fg outline-none focus:border-primary resize-none"
      />

      {error && (
        <p className="text-xs text-danger" role="alert">
          {error}
        </p>
      )}

      {/* Actions */}
      <div className="flex items-center justify-end gap-2">
        <Button
          size="sm"
          variant="outline"
          className="text-xs px-3 py-1 h-7"
          disabled={submitting}
          onClick={() => void submit(ResponseStatus.Cancelled)}
        >
          Reject
        </Button>
        <Button
          size="sm"
          className="bg-success hover:bg-success/80 text-white text-xs px-3 py-1 h-7"
          disabled={submitting}
          onClick={() => void submit(ResponseStatus.Submitted)}
        >
          Approve
        </Button>
      </div>
    </div>
  );
}
