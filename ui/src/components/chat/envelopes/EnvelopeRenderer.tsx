import { useQueryClient } from "@tanstack/react-query";
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
import { Envelope as EnvelopeShell, EnvelopeBody, EnvelopeHeader } from "./primitives/Envelope";
import { PluginLoadErrorCard } from "./PluginLoadErrorCard";
import { ProposalCard } from "./ProposalCard";
import { InterviewCard } from "./InterviewCard";

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
        <PluginLoadErrorCard
          title={`Envelope failed: ${this.props.type}`}
          reason={this.state.error.message}
        />
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
  userMessageCount?: number;
}

export function EnvelopeRenderer({
  envelope,
  onSendMessage,
  onEnvelopeResponse,
  userMessageCount,
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

  const renderLegacyFallback = () => {
    const hasLegacyContent =
      (envelope.proposals?.length ?? 0) > 0 ||
      (envelope.questions?.length ?? 0) > 0 ||
      envelope.approval != null;

    if (!hasLegacyContent) {
      return null;
    }

    return (
      <div className="space-y-3">
        {envelope.proposals?.map((proposal, i) => (
          <ProposalCard key={`proposal-${i}`} proposal={proposal} />
        ))}

        {envelope.questions && envelope.questions.length > 0 && (
          <InterviewCard
            envelope={envelope}
            {...(envelope.id ? { onRespond } : {})}
            userMessageCount={userMessageCount}
          />
        )}

        {envelope.approval && (
          <ApprovalCard approval={envelope.approval} {...(envelope.id ? { onRespond } : {})} />
        )}
      </div>
    );
  };

  const renderUnreachableFallback = () => (
    <EnvelopeShell muted>
      <EnvelopeHeader label="Unsupported envelope" meta={<span>{envelope.type}</span>} />
      <EnvelopeBody
        title="No render path is registered for this envelope."
        description="The envelope payload arrived, but no matching component or legacy fallback handled it."
      />
    </EnvelopeShell>
  );

  // Single registry lookup — checks build-time first, then dynamic fallback.
  // Recover mode restricts to core-only entries.
  const registryEntry = getEnvelopeComponent(envelope.type, recoverMode);
  if (registryEntry) {
    const PluginComponent = registryEntry.component;
    const componentProps =
      registryEntry.props === "approval"
        ? envelope.data || envelope.approval
          ? { approval: envelope.data ?? envelope.approval }
          : null
        : registryEntry.props === "proposal"
          ? envelope.data || envelope.proposals?.[0]
            ? { proposal: envelope.data ?? envelope.proposals?.[0] }
            : null
          : registryEntry.props === "envelope"
            ? { envelope }
            : envelope.data
              ? { data: envelope.data }
              : null;

    if (componentProps) {
      return (
        <EnvelopeErrorBoundary type={envelope.type}>
          <Suspense
            fallback={<div className="animate-pulse p-4 text-sm text-fg-secondary">Loading...</div>}
          >
            <PluginComponent
              {...componentProps}
              {...(onSendMessage ? { onSendMessage } : {})}
              {...(envelope.id ? { onRespond } : {})}
              {...(registryEntry.props === "envelope" ? { userMessageCount } : {})}
            />
          </Suspense>
        </EnvelopeErrorBoundary>
      );
    }
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

  return renderLegacyFallback() ?? renderUnreachableFallback();
}
