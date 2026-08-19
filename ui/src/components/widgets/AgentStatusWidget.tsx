import { Bot } from "lucide-react";
import { useModels } from "@/hooks/useSettings";
import { useActiveModel, useIsStreaming, useToolCalls } from "@/stores/useChatStore";
import { StatusDot, Widget, WidgetRow } from "./Widget";

export function AgentStatusWidget() {
  const activeModel = useActiveModel();
  const isStreaming = useIsStreaming();
  const toolCalls = useToolCalls();

  const { data: models } = useModels();
  const modelLabel = models?.find((m) => m.model_id === activeModel)?.display_name || activeModel;
  const hasToolCalls = toolCalls.length > 0;

  const statusDot = hasToolCalls ? (
    <StatusDot tone="warning" pulse>
      Tool pending
    </StatusDot>
  ) : isStreaming ? (
    <StatusDot tone="success" pulse>
      Streaming
    </StatusDot>
  ) : (
    <StatusDot tone="neutral">Idle</StatusDot>
  );

  return (
    <Widget id="agent-status" title="Agent" icon={Bot} accent="text-brand">
      <div className="flex flex-col gap-1.5">
        <WidgetRow label="Status">{statusDot}</WidgetRow>
        <WidgetRow label="Model" mono>
          {modelLabel || "—"}
        </WidgetRow>
      </div>
    </Widget>
  );
}
