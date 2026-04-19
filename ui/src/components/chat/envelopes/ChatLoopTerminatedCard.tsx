import { AlertOctagon, Clock, RotateCcw, Shield, Zap } from "lucide-react"

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
  const toneColor =
    info.tone === "danger"
      ? "border-danger/30 bg-danger/5"
      : "border-warning/30 bg-warning/5"
  const accentColor = info.tone === "danger" ? "text-danger" : "text-warning"

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
      <div className={`rounded-sm border ${toneColor} overflow-hidden max-w-lg`}>
        <div className="p-4 flex flex-col gap-3">
          <div className="flex items-start gap-3">
            <div className={`shrink-0 mt-0.5 ${accentColor}`}>
              <Icon className="h-5 w-5" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 mb-1">
                <span className={`text-xs font-semibold ${accentColor}`}>
                  {info.label}
                </span>
                <span className="text-[10px] text-fg-faint ml-auto font-mono">
                  {formattedTime}
                </span>
              </div>
              <p className="text-sm text-fg-secondary leading-snug">
                {data.reason}
              </p>
              <p className="text-xs text-fg-muted mt-2 leading-snug">
                {info.hint}
              </p>
            </div>
          </div>

          <div className="flex items-center gap-3 text-[11px] text-fg-muted font-mono border-t border-border-subtle/50 pt-2">
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
            <details className="text-xs">
              <summary className="cursor-pointer text-fg-muted hover:text-fg-secondary">
                Last error
              </summary>
              <pre className="mt-1 p-2 rounded bg-bg/50 text-[11px] font-mono overflow-x-auto whitespace-pre-wrap break-words">
                {data.last_error}
              </pre>
            </details>
          )}

          {onSendMessage && (
            <div className="flex gap-2 pt-1">
              {data.code === "max_turns" && (
                <button
                  type="button"
                  onClick={handleContinue}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-surface hover:bg-surface-hover border border-border-subtle transition-colors"
                >
                  <RotateCcw className="w-3.5 h-3.5" />
                  Continue
                </button>
              )}
              {(data.code === "max_turns" || data.code === "hard_ceiling") && (
                <button
                  type="button"
                  onClick={handleRetryScoped}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-surface hover:bg-surface-hover border border-border-subtle transition-colors"
                >
                  Retry with narrower scope
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
