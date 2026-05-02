/**
 * Shared UI constants.
 *
 * Lift constants here when they're consumed by more than one feature surface
 * (e.g. a component AND its tests, or two unrelated components). Don't dump
 * config values that belong in a hook or a store.
 */

/** Maximum number of dynamic pinned-card tabs per session in either chat drawer. */
export const CHAT_DRAWER_PIN_CAP = 10
