// Plugin config component override registry.
//
// Plugins can specify a `component` name on a ConfigField to replace the
// default input with a custom React component. Overrides only render when
// the user has `developer_mode` enabled; `recover_mode` bypasses all overrides.
//
// To register a custom config component:
//   1. Create the component in ui/src/components/settings/config-overrides/
//   2. Add a lazy import entry below keyed by the component name
//   3. The component receives: { field, value, onChange } (same as ConfigFieldInput)
import { lazy } from 'react'
import type { ComponentType } from 'react'
import type { ConfigField } from '@/lib/types'

export interface ConfigFieldOverrideProps {
  field: ConfigField
  value: unknown
  onChange: (key: string, value: unknown) => void
}

// biome-ignore lint/suspicious/noExplicitAny: plugin override components have varied implementations
type LazyConfigComponent = React.LazyExoticComponent<ComponentType<any>>

// --- PLUGIN CONFIG COMPONENT OVERRIDES ---
// Key = the `component` string from the ConfigField schema.
const PLUGIN_CONFIG_COMPONENT_REGISTRY: Record<string, LazyConfigComponent> = {
  // Example:
  // 'color-picker': lazy(() => import('@/components/settings/config-overrides/ColorPicker').then(m => ({ default: m.ColorPicker }))),
}

export function getConfigComponentOverride(name: string): LazyConfigComponent | undefined {
  return PLUGIN_CONFIG_COMPONENT_REGISTRY[name]
}
