// Envelope registry — maps envelope type strings to lazy-loaded React components.
// Follows ADR-002: single registry with `source` field. Core entries use source: "core",
// plugin entries use source: pluginId. Recover mode filters to source === "core".
//
// IMPORTANT: Core envelope registrations (marked CORE below) are NOT generated
// by the script — they are hardcoded here and MUST be preserved across
// regeneration. The generation script appends plugin entries after the
// PLUGIN_ENTRIES marker. See scripts/generate-plugin-imports.mjs.
import { lazy } from "react";
import type { ComponentType } from "react";
import { getDynamicEnvelope } from "@/lib/plugin-loader";

// biome-ignore lint/suspicious/noExplicitAny: plugin envelope components have varied props
type LazyEnvelopeComponent = React.LazyExoticComponent<ComponentType<any>>;

export interface EnvelopeRegistryEntry {
  component: LazyEnvelopeComponent;
  source: string; // "core" | pluginId
}

// --- CORE ENVELOPES (primitives + fundamental system cards) ---
const CORE_ENTRIES: Record<string, EnvelopeRegistryEntry> = {
  // Task system (core)
  "session-task": {
    component: lazy(() =>
      import("@/components/chat/envelopes/SessionTaskCard").then((m) => ({
        default: m.SessionTaskCard,
      })),
    ),
    source: "core",
  },
  // Generic primitives — reusable by any plugin
  "document-viewer": {
    component: lazy(() =>
      import("@/components/chat/envelopes/DocumentViewerCard").then((m) => ({
        default: m.DocumentViewerCard,
      })),
    ),
    source: "core",
  },
  "report-card": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ReportCard").then((m) => ({
        default: m.ReportCard,
      })),
    ),
    source: "core",
  },
  "error-report": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ErrorCard").then((m) => ({
        default: m.ErrorCard,
      })),
    ),
    source: "core",
  },
  "approval-card": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ApprovalCard").then((m) => ({
        default: m.ApprovalCard,
      })),
    ),
    source: "core",
  },
  "proposal-card": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ProposalCard").then((m) => ({
        default: m.ProposalCard,
      })),
    ),
    source: "core",
  },
  "question-form": {
    component: lazy(() =>
      import("@/components/chat/envelopes/QuestionForm").then((m) => ({
        default: m.QuestionForm,
      })),
    ),
    source: "core",
  },
};

// --- PLUGIN ENVELOPES (owned by their respective plugins) ---
const PLUGIN_ENTRIES: Record<string, EnvelopeRegistryEntry> = {
  // giphy plugin
  "giphy-modal": {
    component: lazy(() =>
      import("@/components/chat/envelopes/GiphyModalCard").then((m) => ({
        default: m.GiphyModalCard,
      })),
    ),
    source: "giphy",
  },
  // oembed plugin
  "oembed-card": {
    component: lazy(() =>
      import("@/components/chat/envelopes/OEmbedCard").then((m) => ({
        default: m.OEmbedCard,
      })),
    ),
    source: "oembed",
  },
  // support-ticket plugin
  "kb-result": {
    component: lazy(() =>
      import("@/components/chat/envelopes/KBResultCard").then((m) => ({
        default: m.KBResultCard,
      })),
    ),
    source: "support-ticket",
  },
  "ticket-form": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TicketFormCard").then((m) => ({
        default: m.TicketFormCard,
      })),
    ),
    source: "support-ticket",
  },
  "ticket-confirmation": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TicketConfirmationCard").then((m) => ({
        default: m.TicketConfirmationCard,
      })),
    ),
    source: "support-ticket",
  },
  "resolution-capture": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ResolutionCaptureCard").then((m) => ({
        default: m.ResolutionCaptureCard,
      })),
    ),
    source: "support-ticket",
  },
  // Fragments Engine — task cards
  "task-disposition": {
    component: lazy(() =>
      import("@/components/plugins/fragments-engine/TaskDispositionCard").then((m) => ({
        default: m.TaskDispositionCard,
      })),
    ),
    source: "fragments-engine",
  },
  "task-complete-notification": {
    component: lazy(() =>
      import("@/components/plugins/fragments-engine/TaskCompleteNotificationCard").then((m) => ({
        default: m.TaskCompleteNotificationCard,
      })),
    ),
    source: "fragments-engine",
  },
  // Fragments Engine — sprint planning
  "sprint-planning-review": {
    component: lazy(() =>
      import("@/components/plugins/fragments-engine/SprintPlanningReviewCard").then((m) => ({
        default: m.SprintPlanningReviewCard,
      })),
    ),
    source: "fragments-engine",
  },
};

// --- AUTO-GENERATED PLUGIN ENTRIES (safe to overwrite below this line) ---
// @PLUGIN_ENTRIES_START
const PLUGIN_ENVELOPE_ENTRIES: Record<string, LazyEnvelopeComponent> = {
  'sprint-planning-review': lazy(() => import('@/components/plugins/fragments-engine/SprintPlanningReviewCard').then(m => ({ default: m.SprintPlanningReviewCard }))),
  'task-disposition': lazy(() => import('@/components/plugins/fragments-engine/TaskDispositionCard').then(m => ({ default: m.TaskDispositionCard }))),
  'task-complete-notification': lazy(() => import('@/components/plugins/fragments-engine/TaskCompleteNotificationCard').then(m => ({ default: m.TaskCompleteNotificationCard }))),
  'giphy-modal': lazy(() => import('@/components/chat/envelopes/GiphyModalCard').then(m => ({ default: m.GiphyModalCard }))),
  'oembed-card': lazy(() => import('@/components/chat/envelopes/OEmbedCard').then(m => ({ default: m.OEmbedCard }))),
  'kb-result': lazy(() => import('@/components/chat/envelopes/KBResultCard').then(m => ({ default: m.KBResultCard }))),
  'ticket-form': lazy(() => import('@/components/chat/envelopes/TicketFormCard').then(m => ({ default: m.TicketFormCard }))),
  'ticket-confirmation': lazy(() => import('@/components/chat/envelopes/TicketConfirmationCard').then(m => ({ default: m.TicketConfirmationCard }))),
  'resolution-capture': lazy(() => import('@/components/chat/envelopes/ResolutionCaptureCard').then(m => ({ default: m.ResolutionCaptureCard }))),
};
// @PLUGIN_ENTRIES_END

// Single merged registry — core takes precedence on name collision.
export const ENVELOPE_REGISTRY: Record<string, EnvelopeRegistryEntry> = {
  // Auto-generated entries (lowest priority)
  ...Object.fromEntries(
    Object.entries(PLUGIN_ENVELOPE_ENTRIES).map(([k, v]) => [
      k,
      { component: v, source: "plugin" } as EnvelopeRegistryEntry,
    ]),
  ),
  // Plugin entries (middle priority)
  ...PLUGIN_ENTRIES,
  // Core entries (highest priority)
  ...CORE_ENTRIES,
};

/**
 * Get the envelope component for a given type.
 * In recover mode, pass `recoverMode: true` to restrict to core-only entries.
 */
export function getEnvelopeComponent(
  type: string,
  recoverMode = false,
): LazyEnvelopeComponent | undefined {
  const entry = ENVELOPE_REGISTRY[type];
  if (entry) {
    if (recoverMode && entry.source !== "core") return undefined;
    return entry.component;
  }

  // Fallback: check dynamically loaded plugins (skip in recover mode).
  if (recoverMode) return undefined;
  const dynamic = getDynamicEnvelope(type);
  return dynamic?.component;
}

// Legacy exports — these are used by EnvelopeRenderer. Kept for backward compat.
// biome-ignore lint/suspicious/noExplicitAny: legacy export shape
export const PLUGIN_ENVELOPE_REGISTRY: Record<string, React.LazyExoticComponent<ComponentType<any>>> =
  Object.fromEntries(
    Object.entries(ENVELOPE_REGISTRY).map(([k, v]) => [k, v.component]),
  );

// biome-ignore lint/suspicious/noExplicitAny: legacy export shape
export const CORE_ONLY_ENVELOPE_REGISTRY: Record<string, React.LazyExoticComponent<ComponentType<any>>> =
  Object.fromEntries(
    Object.entries(ENVELOPE_REGISTRY)
      .filter(([, v]) => v.source === "core")
      .map(([k, v]) => [k, v.component]),
  );
