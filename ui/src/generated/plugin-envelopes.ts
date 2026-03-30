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

// --- CORE ENVELOPES (do not remove — these are NOT plugins) ---
const CORE_ENTRIES: Record<string, EnvelopeRegistryEntry> = {
  "task-disposition": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TaskDispositionCard").then((m) => ({
        default: m.TaskDispositionCard,
      })),
    ),
    source: "core",
  },
  "giphy-modal": {
    component: lazy(() =>
      import("@/components/chat/envelopes/GiphyModalCard").then((m) => ({
        default: m.GiphyModalCard,
      })),
    ),
    source: "core",
  },
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
  "task-complete-notification": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TaskCompleteNotificationCard").then((m) => ({
        default: m.TaskCompleteNotificationCard,
      })),
    ),
    source: "core",
  },
  "kb-result": {
    component: lazy(() =>
      import("@/components/chat/envelopes/KBResultCard").then((m) => ({
        default: m.KBResultCard,
      })),
    ),
    source: "core",
  },
  "ticket-confirmation": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TicketConfirmationCard").then((m) => ({
        default: m.TicketConfirmationCard,
      })),
    ),
    source: "core",
  },
  "ticket-form": {
    component: lazy(() =>
      import("@/components/chat/envelopes/TicketFormCard").then((m) => ({
        default: m.TicketFormCard,
      })),
    ),
    source: "core",
  },
  "resolution-capture": {
    component: lazy(() =>
      import("@/components/chat/envelopes/ResolutionCaptureCard").then((m) => ({
        default: m.ResolutionCaptureCard,
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
  // Sprint planning envelope — registered as plugin so recover mode hides it
  "sprint-planning-review": {
    component: lazy(() =>
      import("@/components/plugins/sprint/SprintPlanningReviewCard").then((m) => ({
        default: m.SprintPlanningReviewCard,
      })),
    ),
    source: "sprint-planning",
  },
};

// --- PLUGIN ENTRIES (auto-generated, safe to overwrite below this line) ---
// @PLUGIN_ENTRIES_START
const PLUGIN_ENVELOPE_ENTRIES: Record<string, LazyEnvelopeComponent> = {

};
// @PLUGIN_ENTRIES_END

// Single merged registry — core takes precedence on name collision.
// Note: PLUGIN_ENVELOPE_ENTRIES is named by the codegen script — do not rename.
export const ENVELOPE_REGISTRY: Record<string, EnvelopeRegistryEntry> = {
  ...Object.fromEntries(
    Object.entries(PLUGIN_ENVELOPE_ENTRIES).map(([k, v]) => [
      k,
      { component: v, source: "plugin" } as EnvelopeRegistryEntry,
    ]),
  ),
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
