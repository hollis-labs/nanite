# ADR-002: Single Registry Pattern for Plugin Extension Points

**Date:** 2026-03-29
**Status:** Accepted

## Context

The plugin system has several dual-registry patterns where built-in items and plugin-contributed items live in separate data structures with different lookup/execution paths. This creates confusion, fragile fallback coupling, and inconsistent behavior between core and plugin contributions.

Affected systems today:
- **Slash commands** — `CommandRegistry` (built-in) + `host.slashCmds` (plugin), with a fallback linear scan in `handleExecuteCommand`.
- **Envelope registry** (frontend) — `CORE_ENVELOPE_REGISTRY` + `PLUGIN_ENVELOPE_REGISTRY` merged at import time with core-wins precedence.
- **Widget registry** (frontend) — `CORE_WIDGET_REGISTRY` + `PLUGIN_WIDGET_REGISTRY` with identical merge pattern.

## Problem

1. Plugin commands bypass `CommandRegistry.Execute()` entirely — they're only reachable through a separate fallback scan in the API handler. Two stores, two code paths.
2. Frontend envelope/widget registries use separate maps merged at module load. Recover mode swaps to a completely different registry object rather than filtering.
3. Adding new extension points (UI slots, settings tabs, nav items) would multiply the dual-registry pattern further if we don't standardize now.
4. Conflict detection, cleanup on unload, and source attribution are all harder when items live in different structures.

## Decision

**All extension point registries use a single map/store.** Built-in vs plugin is metadata on each entry, not a structural distinction.

### Pattern

Every registry entry includes a `source` field:
- `"core"` — shipped with Nanite, always present
- `"{plugin-id}"` — contributed by a specific plugin

This field replaces separate registries and enables:

| Concern | Implementation |
|---|---|
| **Recover mode** | Filter to entries where `source === "core"` |
| **UI badges** | Render "plugin" pill when `source !== "core"` |
| **Conflict detection** | Check for name collisions at registration time, log warning, first-wins or priority-wins |
| **Unload cleanup** | Remove all entries where `source === pluginId` |
| **Debugging** | Single place to inspect all registered items |

### Application

**Backend — Slash commands:**
- Remove `host.slashCmds` map. Plugins register commands via `CommandRegistry.Register()` (passed through `Host`).
- Built-in commands use `Source: "core"`, plugin commands use `Source: pluginID`.
- `CommandRegistry.Execute()` is the single execution path. No fallback scan.

**Frontend — Envelopes:**
- Single `ENVELOPE_REGISTRY` map. Core entries have `source: "core"`, plugin entries have `source: pluginId`.
- Recover mode: `Object.fromEntries(Object.entries(REGISTRY).filter(([_, v]) => v.source === "core"))`.
- Codegen script writes all entries into one map with appropriate source values.

**Frontend — Widgets:**
- Identical to envelopes.

**Future extension points (UI slots, settings tabs, nav items, keybindings, etc.):**
- Follow this same pattern from the start. No dual registries.

## Consequences

- All extension points have consistent registration, lookup, and cleanup semantics.
- Recover mode is a filter operation, not a registry swap.
- Plugin unload is a single sweep: remove all entries matching the plugin ID.
- Slightly more registration ceremony for core items (they must specify `source: "core"`) but this is trivially templated.
