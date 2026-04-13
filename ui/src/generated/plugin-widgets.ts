// Widget registry — maps widget IDs to lazy-loaded React components.
// Follows ADR-002: single registry with `source` field. Core entries use source: "core";
// compiled-in widgets carried over from legacy plugins retain their original `source` so
// recover mode (which filters to source === "core") continues to exclude them.
// Runtime plugin widgets are resolved via getDynamicWidget from plugin-loader.ts.
//
// Widget IDs must match the IDs registered via RegisterUIComponent()
// on the backend. IDs are validated: alphanumeric + hyphens only, max 64 chars.
import { lazy } from "react";
import type { ComponentType } from "react";
import { getDynamicWidget } from "@/lib/plugin-loader";

// biome-ignore lint/suspicious/noExplicitAny: widget components have varied props
type LazyWidgetComponent = React.LazyExoticComponent<ComponentType<any>>;

export interface WidgetRegistryEntry {
  component: LazyWidgetComponent;
  source: string; // "core" | pluginId
}

// Valid widget ID pattern — enforced on both frontend lookup and backend registration.
const WIDGET_ID_PATTERN = /^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$/;

// --- CORE WIDGETS (compiled-in, hand-maintained) ---
// Entries with source !== "core" are compiled-in fallbacks that predated the
// dynamic plugin registry; their `source` is preserved so recover mode excludes
// them. When their owning plugin loads dynamically it wins via getDynamicWidget.
const CORE_ENTRIES: Record<string, WidgetRegistryEntry> = {
  "session-info": {
    component: lazy(() =>
      import("@/components/widgets/SessionInfoWidget").then((m) => ({
        default: m.SessionInfoWidget,
      })),
    ),
    source: "core",
  },
  "context-budget": {
    component: lazy(() =>
      import("@/components/widgets/ContextBudgetWidget").then((m) => ({
        default: m.ContextBudgetWidget,
      })),
    ),
    source: "core",
  },
  "token-usage": {
    component: lazy(() =>
      import("@/components/widgets/TokenUsageWidget").then((m) => ({
        default: m.TokenUsageWidget,
      })),
    ),
    source: "core",
  },
  observability: {
    component: lazy(() =>
      import("@/components/widgets/ObservabilityWidget").then((m) => ({
        default: m.ObservabilityWidget,
      })),
    ),
    source: "core",
  },
  tools: {
    component: lazy(() =>
      import("@/components/widgets/ToolsWidget").then((m) => ({
        default: m.ToolsWidget,
      })),
    ),
    source: "core",
  },
  "agent-status": {
    component: lazy(() =>
      import("@/components/widgets/AgentStatusWidget").then((m) => ({
        default: m.AgentStatusWidget,
      })),
    ),
    source: "core",
  },
  "worker-status": {
    component: lazy(() =>
      import("@/components/widgets/WorkerStatusWidget").then((m) => ({
        default: m.WorkerStatusWidget,
      })),
    ),
    source: "core",
  },
  bookmarks: {
    component: lazy(() =>
      import("@/components/plugins/bookmarks/BookmarksWidget").then((m) => ({
        default: m.BookmarksWidget,
      })),
    ),
    source: "bookmarks",
  },
  "broker-decisions": {
    component: lazy(() =>
      import("@/components/plugins/debug/BrokerDecisionsWidget").then((m) => ({
        default: m.BrokerDecisionsWidget,
      })),
    ),
    source: "debug",
  },
  "slot-inspector": {
    component: lazy(() =>
      import("@/components/plugins/debug/SlotInspectorWidget").then((m) => ({
        default: m.SlotInspectorWidget,
      })),
    ),
    source: "debug",
  },
  "turn-snapshots": {
    component: lazy(() =>
      import("@/components/plugins/debug/TurnSnapshotWidget").then((m) => ({
        default: m.TurnSnapshotWidget,
      })),
    ),
    source: "debug",
  },
};

// Compiled-in registry. Runtime plugin widgets resolve via getDynamicWidget.
export const WIDGET_REGISTRY: Record<string, WidgetRegistryEntry> = {
  ...CORE_ENTRIES,
};

// Default widget order — used when user has no saved preference. Core-only.
export const DEFAULT_WIDGET_ORDER: string[] = [
  "session-info",
  "bookmarks",
  "context-budget",
  "token-usage",
  "observability",
  "tools",
  "agent-status",
  "broker-decisions",
  "slot-inspector",
  "turn-snapshots",
];

/** Widget IDs that require developer_mode to be visible. */
export const DEVELOPER_ONLY_WIDGETS: Set<string> = new Set([
  "broker-decisions",
  "slot-inspector",
  "turn-snapshots",
]);

/** Validate a widget ID format. */
export function isValidWidgetId(id: string): boolean {
  return WIDGET_ID_PATTERN.test(id);
}

/** Look up a widget component by ID. Returns undefined if not in registry. */
export function getWidgetComponent(
  id: string,
  recoverMode = false,
): LazyWidgetComponent | undefined {
  if (!isValidWidgetId(id)) return undefined;
  const entry = WIDGET_REGISTRY[id];
  if (entry) {
    if (recoverMode && entry.source !== "core") return undefined;
    return entry.component;
  }

  // Fallback: check dynamically loaded plugins (skip in recover mode).
  if (recoverMode) return undefined;
  const dynamic = getDynamicWidget(id);
  return dynamic?.component;
}
