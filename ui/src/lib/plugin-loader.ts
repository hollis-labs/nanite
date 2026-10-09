/** Nanite taxonomy and host policy over the released browser registry v2. */

import type {
  AdoptedContribution,
  KindDescriptor,
  PluginRegistryOptions,
  PluginRegistryResponse,
  RegionDescriptor,
} from "@hollis-labs/plugin-registry";
import { parseRegistryResponse, qualifiedKey } from "@hollis-labs/plugin-registry";
import { createReactPluginRegistry } from "@hollis-labs/plugin-registry/react";
import { type ComponentType, type LazyExoticComponent, version as reactVersion } from "react";
import { satisfies, validRange } from "semver";
import { ENVELOPE_REGISTRY } from "@/generated/plugin-envelopes";
import { WIDGET_REGISTRY } from "@/lib/builtin-widgets";

export type { PluginRegistryResponse } from "@hollis-labs/plugin-registry";

export interface DynamicRegistryEntry {
  // Plugin components own their prop shapes; callers narrow at render sites.
  // biome-ignore lint/suspicious/noExplicitAny: host component boundary.
  component: LazyExoticComponent<ComponentType<any>>;
  source: string;
}

const CORE_PANEL_IDS = new Set(["widgets", "work", "workflows", "inbox", "artifacts"]);
const kinds: Record<string, KindDescriptor> = {};
const regions: Record<string, RegionDescriptor> = {};
for (const kind of ["envelope", "widget", "slot", "panel"]) {
  kinds[kind] = {
    schema_version: 1,
    metadata_schema: {},
    representations: ["component"],
    regions: [kind],
    required_capabilities: [],
  };
  regions[kind] = {
    kinds: [kind],
    representations: ["component"],
    context_schema: {},
    ordering: "manifest",
  };
}

function coreBinding(kind: string, binding: string): boolean {
  return (
    (kind === "envelope" && ENVELOPE_REGISTRY[binding]?.source === "core") ||
    (kind === "widget" && WIDGET_REGISTRY[binding]?.source === "core") ||
    (kind === "panel" && CORE_PANEL_IDS.has(binding)) ||
    (kind === "slot" &&
      binding.startsWith("right-rail-tab/") &&
      CORE_PANEL_IDS.has(binding.slice("right-rail-tab/".length)))
  );
}

// Inclusive SDK runtime bounds cannot express npm caret ranges. Every Nanite
// activation also enforces the unchanged app range against the running React
// version. An omitted SDK bound therefore never bypasses this host check.
function compatibleReactMetadata(metadata: unknown): boolean {
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) return false;
  if (!("react_range" in metadata)) return true;
  const range = metadata.react_range;
  return typeof range === "string" && !!validRange(range) && satisfies(reactVersion, range);
}

export function createNanitePluginRegistry(options: Partial<PluginRegistryOptions> = {}) {
  let bindings = new Map<string, string>();
  const registry = createReactPluginRegistry({
    ...options,
    kinds: options.kinds ?? kinds,
    regions: options.regions ?? regions,
    runtimes: { ...options.runtimes, react: reactVersion },
    // Use the SDK's default importer, which executes the digest-verified bytes.
    validateMetadata: (schema, metadata) =>
      compatibleReactMetadata(metadata) && (options.validateMetadata?.(schema, metadata) ?? true),
    reserved: (kind, key) =>
      coreBinding(kind, bindings.get(`${kind}:${key}`) ?? key) ||
      (options.reserved?.(kind, key) ?? false),
  });
  return {
    ...registry,
    sync: (response: PluginRegistryResponse | string) => {
      let parsed: PluginRegistryResponse;
      try {
        parsed = typeof response === "string" ? parseRegistryResponse(response) : response;
      } catch {
        return registry.sync(response);
      }
      bindings = new Map(
        Object.entries(parsed.contributions).flatMap(([kind, entries]) =>
          Object.values(entries).map(
            (entry) =>
              [
                `${kind}:${qualifiedKey(entry.owner_id, entry.local_key)}`,
                entry.public_binding ?? entry.local_key,
              ] as const,
          ),
        ),
      );
      return registry.sync(parsed);
    },
  };
}

export let browserPluginRegistry = createNanitePluginRegistry();
const listeners = new Set<() => void>();
let registryVersion = 0;
const changed = () => {
  registryVersion++;
  for (const listener of listeners) listener();
};
let detach = browserPluginRegistry.subscribe(changed);
export const subscribeRegistry = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
export const getRegistryVersion = () => registryVersion;
export async function clearDynamicRegistry() {
  const retired = browserPluginRegistry;
  detach();
  browserPluginRegistry = createNanitePluginRegistry();
  detach = browserPluginRegistry.subscribe(changed);
  changed();
  await retired.clear();
}

export async function syncPluginRegistry(response: PluginRegistryResponse | string): Promise<void> {
  const result = await browserPluginRegistry.sync(response);
  if (!result.accepted) throw new Error("Plugin registry protocol or structure was refused");
}

function bindingEntry(kind: string, binding: string): AdoptedContribution | undefined {
  return browserPluginRegistry
    .list(kind)
    .find((entry) => entry.public_binding === binding && entry.isActive());
}
function componentEntry(contribution?: AdoptedContribution): DynamicRegistryEntry | undefined {
  if (!contribution || !contribution.isActive()) return undefined;
  return {
    component: contribution.value as DynamicRegistryEntry["component"],
    source: contribution.pluginId,
  };
}

export function getDynamicEnvelope(type: string) {
  return componentEntry(bindingEntry("envelope", type));
}
export function getDynamicWidget(id: string) {
  return componentEntry(bindingEntry("widget", id));
}
export function getDynamicSlotComponent(name: string, owner?: string, id?: string) {
  const matches = browserPluginRegistry.list("slot").filter((entry) => {
    if (!entry.isActive() || entry.exportName !== name || (owner && entry.pluginId !== owner))
      return false;
    if (!id) return true;
    const meta = entry.metadata;
    return !!meta && typeof meta === "object" && "id" in meta && meta.id === id;
  });
  if (!owner && new Set(matches.map((entry) => entry.pluginId)).size > 1) return undefined;
  return componentEntry(matches[0]);
}
export function getEnvelopePluginId(type: string) {
  return bindingEntry("envelope", type)?.pluginId;
}
export function getPluginLoadError(id: string) {
  return (
    browserPluginRegistry.errors().find((entry) => entry.pluginId === id)?.reason ??
    browserPluginRegistry.refusals().find((entry) => entry.owner_id === id)?.reason
  );
}
export function getPluginLoadErrors() {
  return [...browserPluginRegistry.errors()]
    .map(({ pluginId, reason }) => ({ pluginId, reason }))
    .sort((a, b) => a.pluginId.localeCompare(b.pluginId));
}

async function reloadPluginFromBrowser(id: string): Promise<unknown> {
  const response = await fetch("/api/plugins/reload", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: id }),
  });
  const body = await response.json();
  if (!response.ok) throw new Error(`Reload failed: ${response.status}`);
  await browserPluginRegistry.unload(id);
  return body;
}

export function installPluginDevHelpers() {
  if (!import.meta.env.DEV || typeof window === "undefined") return;
  Object.assign(window, {
    __nanite_reloadPlugin: reloadPluginFromBrowser,
    __nanite_pluginRegistry: () => browserPluginRegistry.snapshot(),
  });
}
