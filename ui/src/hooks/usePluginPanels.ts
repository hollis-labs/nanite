import type { ComponentType, LazyExoticComponent } from "react";
import { useEffect, useSyncExternalStore } from "react";
import { resolveIcon } from "@/lib/icons";
import { browserPluginRegistry, getRegistryVersion, subscribeRegistry } from "@/lib/plugin-loader";
import type { UISlotEntry } from "@/lib/types";
import { type PanelDef, usePanelRegistryStore } from "./usePanelRegistry";

interface PanelMeta {
  title: string;
  icon?: string;
  default_visible?: boolean;
  order?: number;
}

export interface PluginPanelView {
  definition: PanelDef;
  // Plugin render props belong to the declaration, with host scope added at render.
  // biome-ignore lint/suspicious/noExplicitAny: runtime plugin component boundary.
  View: LazyExoticComponent<ComponentType<any>>;
  props?: Record<string, unknown>;
}

let snapshotVersion: number | undefined;
let snapshot: PluginPanelView[] = [];

// React requires an unchanged external-store snapshot to keep its identity.
function getPanelSnapshot(): PluginPanelView[] {
  const version = getRegistryVersion();
  if (snapshotVersion === version) return snapshot;

  const result = new Map<string, PluginPanelView>();
  for (const entry of browserPluginRegistry.list("panel")) {
    if (!entry.isActive()) continue;
    const meta = entry.metadata as PanelMeta;
    const id = entry.public_binding ?? entry.local_key;
    result.set(id, {
      definition: {
        id,
        label: meta.title || id,
        icon: resolveIcon(meta.icon),
        source: "plugin",
        pluginId: entry.pluginId,
        order: meta.order ?? 100,
        defaultVisible: meta.default_visible ?? false,
      },
      View: entry.value as PluginPanelView["View"],
    });
  }
  for (const entry of browserPluginRegistry.list("slot")) {
    if (!entry.isActive() || !entry.public_binding?.startsWith("right-rail-tab/")) continue;
    const meta = entry.metadata as UISlotEntry;
    // A panel declaration is authoritative when its owner also exposes a slot.
    if (result.has(meta.id)) continue;
    result.set(meta.id, {
      definition: {
        id: meta.id,
        label: meta.label || meta.id,
        icon: resolveIcon(meta.icon),
        source: "plugin",
        pluginId: entry.pluginId,
        order: 100 + (meta.priority ?? 0),
        defaultVisible: false,
      },
      View: entry.value as PluginPanelView["View"],
      props: meta.props,
    });
  }
  snapshot = [...result.values()];
  snapshotVersion = version;
  return snapshot;
}

/** Reconcile loaded panel and rail-slot declarations without a second loader. */
export function usePluginPanels(): PluginPanelView[] {
  const views = useSyncExternalStore(subscribeRegistry, getPanelSnapshot);

  useEffect(() => {
    const store = usePanelRegistryStore.getState();
    const desired = new Map(views.map((view) => [view.definition.id, view.definition]));
    for (const existing of Object.values(store.panels)) {
      if (existing.source !== "plugin") continue;
      const next = desired.get(existing.id);
      if (!next || next.pluginId !== existing.pluginId) store.unregister(existing.id);
    }
    for (const view of views) store.register(view.definition);
  }, [views]);
  return views;
}
