import { AlertTriangle, Gauge } from "lucide-react";
import { Envelope, EnvelopeBody, EnvelopeHeader } from "./primitives/Envelope";
import { StatusPill } from "./primitives/StatusPill";

interface ChatLoopBudgetSoftWarningData {
  max_turns: number;
  iteration: number;
  reason: string;
  timestamp: string;
}

interface ChatLoopBudgetSoftWarningCardProps {
  data: ChatLoopBudgetSoftWarningData;
}

export function ChatLoopBudgetSoftWarningCard({ data }: ChatLoopBudgetSoftWarningCardProps) {
  const formattedTime = (() => {
    try {
      return new Date(data.timestamp).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      });
    } catch {
      return data.timestamp;
    }
  })();

  return (
    <Envelope accent="warning">
      <EnvelopeHeader
        icon={AlertTriangle}
        label="Loop budget warning"
        tone="warning"
        meta={formattedTime}
        action={<StatusPill tone="warning">turn {data.iteration}</StatusPill>}
      />
      <EnvelopeBody title="Agent is running past the soft turn budget" description={data.reason}>
        <div className="flex items-center gap-2 font-mono text-[12px] text-fg-muted">
          <Gauge className="h-3.5 w-3.5" />
          <span>
            iter {data.iteration} / soft max {data.max_turns}
          </span>
        </div>
      </EnvelopeBody>
    </Envelope>
  );
}
