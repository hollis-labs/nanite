import { useQuery } from "@tanstack/react-query";
import { Gauge, Info } from "lucide-react";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAppStore } from "@/stores/useAppStore";
import { useIsStreaming } from "@/stores/useChatStore";
import { ContextInspectorModal } from "./ContextInspectorModal";
import { Bar, pctTone, Widget, WidgetRow } from "./Widget";

const BREAKDOWN_STALE = 30_000;

const PCT_TEXT: Record<string, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
};

function formatTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`;
  return String(n);
}

function formatCost(usd: number): string {
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}

export function ContextBudgetWidget() {
  const activeSessionId = useAppStore((s) => s.activeSessionId);
  const isStreaming = useIsStreaming();
  const [inspectorOpen, setInspectorOpen] = useState(false);

  const { data: usage } = useQuery({
    queryKey: ["session-usage", activeSessionId],
    queryFn: () => api.getSessionUsage(activeSessionId!),
    enabled: !!activeSessionId,
    refetchInterval: isStreaming ? 5000 : 30000,
  });

  const { data: breakdown } = useQuery({
    queryKey: ["context-breakdown", activeSessionId],
    queryFn: () => api.getContextBreakdown(activeSessionId!),
    enabled: !!activeSessionId,
    staleTime: BREAKDOWN_STALE,
    refetchInterval: isStreaming ? 10000 : 60000,
  });

  const ctxTotal = breakdown?.total ?? 0;
  const ctxCeiling = breakdown?.ceiling ?? 1;
  const ctxPct = Math.min((ctxTotal / ctxCeiling) * 100, 100);
  const systemPromptTokens = breakdown?.system_prompt_tokens ?? 0;
  const toolCallCount = breakdown?.tools?.length ?? 0;
  const toolTokens = breakdown?.tool_tokens_total ?? 0;
  const toolsAvailable = breakdown?.tools_available ?? 0;

  const totalTokens = usage?.total_tokens ?? 0;
  const inputTokens = usage?.input_tokens ?? 0;
  const outputTokens = usage?.output_tokens ?? 0;
  const cost = usage?.estimated_cost_usd ?? 0;
  const messageCount = usage?.message_count ?? 0;
  const cacheCreation = usage?.cache_creation_tokens ?? 0;
  const cacheRead = usage?.cache_read_tokens ?? 0;
  const tokenPct = ctxCeiling > 1 ? (totalTokens / ctxCeiling) * 100 : 0;

  const ctxTone = pctTone(ctxPct);
  const tokenTone = pctTone(tokenPct);

  return (
    <>
      <Widget
        id="context"
        title="Context"
        icon={Gauge}
        accent="text-info"
        meta={`${Math.round(ctxPct)}%`}
      >
        <div className="flex flex-col gap-3">
          {/* Context window bar */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between text-[12px] text-fg-muted">
              <span className="inline-flex items-center gap-1">
                Window
                {activeSessionId && (
                  <button
                    onClick={() => setInspectorOpen(true)}
                    className="p-0.5 rounded hover:bg-surface transition-colors"
                    title="Inspect breakdown"
                  >
                    <Info className="w-[11px] h-[11px] text-fg-faint hover:text-fg-muted" />
                  </button>
                )}
              </span>
              <span className={`font-mono text-[11px] ${PCT_TEXT[ctxTone]}`}>
                {formatTokens(ctxTotal)} / {formatTokens(ctxCeiling)}
              </span>
            </div>
            <Bar pct={ctxPct} tone={ctxTone} />
          </div>

          {/* Token usage bar */}
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between text-[12px] text-fg-muted">
              <span>Tokens used</span>
              <span className={`font-mono text-[11px] ${PCT_TEXT[tokenTone]}`}>
                {formatTokens(totalTokens)} · {formatCost(cost)}
              </span>
            </div>
            <Bar pct={tokenPct} tone={tokenTone} />
          </div>

          {/* Breakdown */}
          {(totalTokens > 0 || ctxTotal > 0) && (
            <div className="flex flex-col gap-1.5 pt-2 border-t border-divider">
              <WidgetRow label="Input" mono>
                {formatTokens(inputTokens)}
              </WidgetRow>
              <WidgetRow label="Output" mono>
                {formatTokens(outputTokens)}
              </WidgetRow>
              <WidgetRow label="Messages" mono>
                {String(messageCount)}
              </WidgetRow>
              <WidgetRow label="System prompt" mono>
                {formatTokens(systemPromptTokens)}
              </WidgetRow>
              <WidgetRow label={`Tool calls (${toolCallCount})`} mono>
                {formatTokens(toolTokens)}
              </WidgetRow>
              <WidgetRow label="Tools available" mono>
                {String(toolsAvailable)}
              </WidgetRow>
              {(cacheCreation > 0 || cacheRead > 0) && (
                <>
                  <WidgetRow label="Cache write" mono>
                    {formatTokens(cacheCreation)}
                  </WidgetRow>
                  <WidgetRow label="Cache read">
                    <span className="font-mono text-[11px] text-success">
                      {formatTokens(cacheRead)}
                    </span>
                  </WidgetRow>
                </>
              )}
            </div>
          )}
        </div>
      </Widget>

      {activeSessionId && (
        <ContextInspectorModal
          sessionId={activeSessionId}
          open={inspectorOpen}
          onClose={() => setInspectorOpen(false)}
        />
      )}
    </>
  );
}
