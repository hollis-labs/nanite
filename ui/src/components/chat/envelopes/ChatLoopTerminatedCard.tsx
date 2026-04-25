import { AlertOctagon, Clock, RotateCcw, Shield, Zap } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Envelope, EnvelopeBody, EnvelopeFooter, EnvelopeHeader, EnvelopeSection } from "./primitives/Envelope"
import { StatusPill } from "./primitives/StatusPill"

interface ChatLoopTerminatedData {
  reason: string
  code:
    | "runaway_tool_failures"
    | "max_turns"
    | "hard_ceiling"
    | "idle_timeout"
    | "retry_budget_exhausted"
  iteration: number
  consecutive_failures: number
  last_error?: string
  last_tool?: string
  timestamp: string
}

interface ChatLoopTerminatedCardProps {
  data: ChatLoopTerminatedData
  onSendMessage?: (content: string) => void
}

const CODE_INFO: Record<
  ChatLoopTerminatedData["code"],
  { label: string; icon: typeof Clock; hint: string; tone: "warning" | "danger" }
> = {
  max_turns: {
    label: "Turn limit reached",
    icon: Clock,
    hint: "The loop hit its per-generation turn budget before finishing. If the task legitimately needed more steps, ask the user to continue or split the request.",
    tone: "warning",
  },
  hard_ceiling: {
    label: "Hard ceiling reached",
    icon: Shield,
    hint: "The loop hit the absolute safety cap. This is a runaway-prevention backstop — the task is too large for a single generation.",
    tone: "danger",
  },
  runaway_tool_failures: {
    label: "Runaway tool failures",
    icon: AlertOctagon,
    hint: "The loop tripped the tool-failure circuit-breaker. Usually means a tool is mis-configured or an input shape is wrong.",
    tone: "danger",
  },
  idle_timeout: {
    label: "Idle timeout",
    icon: Clock,
    hint: "The loop produced nothing for an extended period and was closed.",
    tone: "warning",
  },
  retry_budget_exhausted: {
    label: "Retry budget exhausted",
    icon: Zap,
    hint: "The loop used all retry attempts recovering from provider errors.",
    tone: "danger",
  },
}

export function ChatLoopTerminatedCard({ data, onSendMessage }: ChatLoopTerminatedCardProps) {
  const info = CODE_INFO[data.code] ?? {
    label: data.code,
    icon: AlertOctagon,
    hint: "The chat loop exited without a natural end_turn from the model. No final reply was composed.",
    tone: "warning" as const,
  }
  const Icon = info.icon

  const formattedTime = (() => {
    try {
      return new Date(data.timestamp).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
      })
    } catch {
      return data.timestamp
    }
  })()

  const handleContinue = () => {
    if (!onSendMessage) return
    onSendMessage("Please continue from where you left off.")
  }

  const handleRetryScoped = () => {
    if (!onSendMessage) return
    onSendMessage(
      "That generation ran out of turns. Please retry with a narrower scope — focus on one specific aspect instead of the full exploration.",
    )
  }

  return (
    <div className="animate-in fade-in slide-in-from-bottom-2 duration-300">
      <Envelope accent={info.tone} className="max-w-lg">
        <EnvelopeHeader
          icon={Icon}
          label="Loop terminated"
          tone={info.tone}
          meta={formattedTime}
          action={<StatusPill tone={info.tone}>{info.label}</StatusPill>}
        />

        <EnvelopeBody title={data.reason} description={info.hint}>
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2 font-mono text-[11px] text-fg-muted">
              <span>iter {data.iteration}</span>
              {data.consecutive_failures > 0 && (
                <span>{data.consecutive_failures} consecutive failures</span>
              )}
              {data.last_tool && (
                <span className="truncate" title={data.last_tool}>
                  last tool: {data.last_tool}
                </span>
              )}
            </div>

            {data.last_error && (
              <EnvelopeSection label="Last error">
                <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded-[6px] border border-border-subtle bg-surface px-3 py-2 font-mono text-[11px] text-fg">
                  {data.last_error}
                </pre>
              </EnvelopeSection>
            )}
          </div>
        </EnvelopeBody>

        {onSendMessage && (
          <EnvelopeFooter>
            {data.code === "max_turns" && (
              <Button size="sm" variant="outline" onClick={handleContinue}>
                <RotateCcw className="h-3.5 w-3.5" />
                Continue
              </Button>
            )}
            {(data.code === "max_turns" || data.code === "hard_ceiling") && (
              <Button size="sm" variant="outline" onClick={handleRetryScoped}>
                Retry with narrower scope
              </Button>
            )}
          </EnvelopeFooter>
        )}
      </Envelope>
    </div>
  )
}
