/**
 * Plugin slot component lookup.
 *
 * Resolves a slot component name (from `UISlotEntry.component`) to a React
 * component. Today this only delegates to the runtime dynamic registry — the
 * build-time registry scaffolding in `generated/plugin-slot-components.ts`
 * has been removed because it was never populated by any code path.
 *
 * Callers must handle `undefined` gracefully (AppShell, RightRail, and
 * SettingsPage all do — they render nothing when the component isn't found).
 *
 * See `.nanite/agents/frontend.md` §Known Gaps and
 * `docs/architecture/plugin-system.md` §13 for the open design question of
 * how plugins should ship React components to the frontend.
 */
import { getDynamicSlotComponent } from '@/lib/plugin-loader'

export function getSlotComponent(name: string) {
  return getDynamicSlotComponent(name)?.component
}
