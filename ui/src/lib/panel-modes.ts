/**
 * J8 v1 — mode/status preset map (CW-20260426-0006).
 *
 * Maps an agent-emitted mode/status name to the set of panels that should
 * open when the FE sees a `mode=<name>` signal (either via a panel_signal
 * stream event with action="mode" or via an envelope's `mode` field).
 *
 * **Vocabulary policy.** v1 ships exactly one mode (`planning`). The map is
 * intentionally extensible — new modes land here as additional entries; no
 * other code change is required. Unknown modes are silently no-ops on the
 * FE so a forward-compatible agent can emit a future mode against an older
 * client without a crash.
 *
 * **Dispatch order.** Each panel ID in the preset is opened in array order
 * via `useLayoutStore.setPanelOpen(id, 'agent')`. The store's 4-state
 * dismiss machine handles per-panel gating: dismissed panels stay dismissed,
 * user-opened panels keep their source attribution.
 *
 * **Out of scope (v2).** Mode-driven panel _close_, mode stacking (multi-
 * mode active simultaneously), and per-mode layout overrides (e.g. force
 * widget panel to a specific tab). v1 only opens panels in the preset.
 */
export const PANEL_MODE_PRESETS: Record<string, readonly string[]> = {
  /**
   * Planning: surface both the right-rail Work panel (todos / plans) and
   * the Workflows panel (guided-interaction templates) so the user can
   * triage and dispatch in parallel. Bottom drawer stays as-is — planning
   * doesn't displace whatever reference content is already visible there.
   */
  planning: ["work", "workflows"],
} as const;

/**
 * Resolve the panel set for a mode name, or null when the mode is unknown.
 * Centralized lookup so callers can branch on null rather than empty arrays.
 */
export function resolvePanelMode(mode: string): readonly string[] | null {
  if (!mode) return null;
  const preset = PANEL_MODE_PRESETS[mode];
  return preset ?? null;
}
