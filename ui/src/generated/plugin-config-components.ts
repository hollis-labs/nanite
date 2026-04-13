// Plugin config component override registry.
// Follows ADR-002: single registry with `source` field.
//
// Plugins can specify a `component` name on a ConfigField to replace the
// default input with a custom React component. Overrides only render when
// the user has `developer_mode` enabled; `recover_mode` bypasses all overrides.
//
// To register a custom config component:
//   1. Create the component in ui/src/components/settings/config-overrides/
//   2. Add a lazy import entry below keyed by the component name
//   3. The component receives: { field, value, onChange } (same as ConfigFieldInput)
// @ts-expect-error lazy is used when entries are added to the registry below
// eslint-disable-next-line @typescript-eslint/no-unused-vars
import { lazy } from "react";
import type { ComponentType } from "react";
import type { ConfigField } from "@/lib/types";

export interface ConfigFieldOverrideProps {
  field: ConfigField;
  value: unknown;
  onChange: (key: string, value: unknown) => void;
}

// biome-ignore lint/suspicious/noExplicitAny: plugin override components have varied implementations
type LazyConfigComponent = React.LazyExoticComponent<ComponentType<any>>;

export interface ConfigComponentRegistryEntry {
  component: LazyConfigComponent;
  source: string; // "core" | pluginId
}

// --- CORE CONFIG COMPONENT OVERRIDES ---
const CORE_ENTRIES: Record<string, ConfigComponentRegistryEntry> = {
  // Example:
  // 'color-picker': {
  //   component: lazy(() => import('@/components/settings/config-overrides/ColorPicker').then(m => ({ default: m.ColorPicker }))),
  //   source: 'core',
  // },
};

// Compiled-in registry — core overrides only. Runtime plugin config overrides
// are handled by the dynamic plugin registry (see plugin-loader.ts).
export const CONFIG_COMPONENT_REGISTRY: Record<string, ConfigComponentRegistryEntry> = {
  ...CORE_ENTRIES,
};

/**
 * Look up a config component override by name.
 * In recover mode, pass `recoverMode: true` to restrict to core-only entries.
 */
export function getConfigComponentOverride(
  name: string,
  recoverMode = false,
): LazyConfigComponent | undefined {
  const entry = CONFIG_COMPONENT_REGISTRY[name];
  if (!entry) return undefined;
  if (recoverMode && entry.source !== "core") return undefined;
  return entry.component;
}
