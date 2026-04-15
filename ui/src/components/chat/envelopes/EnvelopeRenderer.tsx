import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { Component, type ReactNode, Suspense, useCallback, useSyncExternalStore } from "react";
import { getEnvelopeComponent } from "@/generated/plugin-envelopes";
import { useSettings } from "@/hooks/useSettings";
import {
  RESPONSE_V1_VERSION,
  type ResponseV1,
  submitEnvelopeResponse,
} from "@/lib/envelope-response";
import {
  getEnvelopePluginId,
  getPluginLoadError,
  getRegistryVersion,
  subscribeRegistry,
} from "@/lib/plugin-loader";
import type { Envelope } from "@/lib/types";
import { ApprovalCard } from "./ApprovalCard";
import { PluginLoadErrorCard } from "./PluginLoadErrorCard";
import { ProposalCard } from "./ProposalCard";
import { QuestionForm } from "./QuestionForm";

/**
 * The shape cards produce — EnvelopeRenderer injects `v`, `kind`, `id`
 * so cards only own status + payload.
 */
export type EnvelopeResponsePartial = Omit<ResponseV1, "v" | "kind" | "id">;

export type EnvelopeResponder = (partial: EnvelopeResponsePartial) => Promise<void>;

/** Error boundary scoped to a single envelope — prevents a broken plugin from crashing the chat. */
class EnvelopeErrorBoundary extends Component<
  { type: string; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    if (this.state.error) {
      return (
        <div className="rounded-sm border border-danger/50 bg-danger/10 p-3">
          <div className="flex items-center gap-2 mb-1">
            <AlertTriangle className="w-3.5 h-3.5 text-danger shrink-0" />
            <span className="text-xs font-medium text-danger">
              Envelope failed: {this.props.type}
            </span>
          </div>
          <p className="text-[11px] text-danger/70 leading-relaxed">{this.state.error.message}</p>
        </div>
      );
    }
    return this.props.children;
  }
}

interface EnvelopeRendererProps {
  envelope: Envelope;
  /**
   * @deprecated use `onEnvelopeResponse` for typed payloads. Still wired for
   * cards not yet migrated to the Phase-3 S5 typed-response path.
   */
  onSendMessage?: (content: string) => void;
  /**
   * Handle a typed envelope response. If omitted, a default implementation
   * POSTs to `/api/envelopes/{id}/respond` via `submitEnvelopeResponse`.
   */
  onEnvelopeResponse?: (response: ResponseV1) => Promise<void>;
}

export function EnvelopeRenderer({
  envelope,
  onSendMessage,
  onEnvelopeResponse,
}: EnvelopeRendererProps) {
  const { data: settings } = useSettings();
  const recoverMode = settings?.recover_mode ?? false;
  const queryClient = useQueryClient();

  // Re-render when dynamic plugins register new envelope components.
  useSyncExternalStore(subscribeRegistry, getRegistryVersion);

  // Card-level responder: cards hand us a partial (status + payload); we
  // attach v/kind/id and delegate to the caller-supplied handler or the
  // default POST. If the envelope has no id (pre-Phase-3 grandfathered
  // emits) we no-op and let the legacy onSendMessage path own the response.
  const onRespond = useCallback<EnvelopeResponder>(
    async (partial) => {
      const id = envelope.id;
      if (!id) {
        if (import.meta.env?.DEV) {
          console.warn(
            "[EnvelopeRenderer] envelope missing id — typed response skipped",
            envelope.type,
          );
        }
        return;
      }
      const response: ResponseV1 = {
        v: RESPONSE_V1_VERSION,
        kind: envelope.type,
        id,
        ...partial,
      };
      if (onEnvelopeResponse) {
        await onEnvelopeResponse(response);
      } else {
        await submitEnvelopeResponse(id, response);
      }
    },
    [envelope.id, envelope.type, onEnvelopeResponse],
  );

  // Single registry lookup — checks build-time first, then dynamic fallback.
  // Recover mode restricts to core-only entries.
  const PluginComponent = getEnvelopeComponent(envelope.type, recoverMode);
  if (PluginComponent && envelope.data) {
    return (
      <EnvelopeErrorBoundary type={envelope.type}>
        <Suspense
          fallback={<div className="animate-pulse p-4 text-sm text-fg-secondary">Loading...</div>}
        >
          <PluginComponent data={envelope.data} {...(onSendMessage ? { onSendMessage } : {})} />
        </Suspense>
      </EnvelopeErrorBoundary>
    );
  }

  // No component resolved. If the type belongs to a plugin whose bundle
  // failed to load, surface the failure with a retry button instead of
  // falling through to the default renderer.
  if (!recoverMode) {
    const pluginId = getEnvelopePluginId(envelope.type);
    if (pluginId) {
      const reason = getPluginLoadError(pluginId);
      if (reason) {
        return (
          <PluginLoadErrorCard
            pluginId={pluginId}
            reason={reason}
            onRetry={() => {
              void queryClient.invalidateQueries({ queryKey: ["plugins", "registry"] });
            }}
          />
        );
      }
    }
  }

  // Default envelope rendering — proposals, questions, approval
  return (
    <div className="space-y-3">
      {envelope.proposals?.map((proposal, i) => (
        <ProposalCard key={`proposal-${i}`} proposal={proposal} />
      ))}

      {envelope.questions && envelope.questions.length > 0 && (
        <QuestionForm
          questions={envelope.questions}
          {...(envelope.id ? { onRespond } : {})}
          {...(onSendMessage ? { onSubmit: onSendMessage } : {})}
        />
      )}

      {envelope.approval && (
        <ApprovalCard approval={envelope.approval} {...(envelope.id ? { onRespond } : {})} />
      )}
    </div>
  );
}
