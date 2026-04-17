# Inventory Audit: framework/libs/go-plugin

**Date:** 2026-04-11
**Scope:** `framework/libs/go-plugin/` (module `github.com/hollis-labs/go-plugin`)
**Reviewer:** nanite-reviewer-backend (deep-review, inventory variant)

## Scope

Audit of the standalone plugin SDK module at `~/Projects-apps/framework/libs/go-plugin/`. This module defines the plugin contract (interfaces, types, constants) that Nanite, vanta-conduit, fragments-engine, and framework external plugins depend on. The audit was triggered by Track I.1's plan to delete this module without a verified inventory.

**Files read in full:** `plugin.go`, `registry.go`, `uiregistry.go`, `logger.go`, `doc.go`, `example.go`, `plugin_test.go`, `go.mod`.

**Packages read in full for cross-reference:** `nanite/internal/plugin/types.go`, `nanite/internal/plugin/host.go`, `nanite/internal/plugin/registry.go`, `nanite/internal/plugin/loader.go`, `nanite/internal/plugin/logger.go`, `nanite/internal/plugin/subprocess/protocol.go`.

**Dependency consumers verified:** `nanite/go.mod`, `vanta-conduit/go.mod`, `fragments-engine/engine/go.mod`, `framework/.verify-sessionstats.buBu4U/go.mod`, `nanite-verify-agentrc/plugin-alias/go.mod`, three framework external plugins (`support-ticket`, `session-stats`, `observability-widgets`), plus `.archived/cortex/go.mod`.

## Methodology

This is an inventory audit, not a standard deep-review pass. The goal is to answer five questions for the release prep team:

1. What is in this module? (types, interfaces, functions)
2. Who imports it?
3. Does it overlap with `nanite/internal/plugin/`?
4. What would break if deleted?
5. Are types/interfaces shared between this module and nanite's internal plugin package?

Categories applied: **Module inventory**, **Consumer analysis**, **Overlap analysis**, **Deletion impact**. Security, concurrency, and tooling categories deferred (not in scope for an inventory audit).

## Findings

### By severity

**Critical (1)**
- [01 -- Shutdown deadlock in shared Registry](01-critical-shutdown-deadlock.md)

**High (1)**
- [02 -- Deletion breaks 4+ consumers and all external plugins](02-high-deletion-impact.md)

**Medium (1)**
- [03 -- Stale "Conduit Host" references in Registry stubs](03-medium-stale-conduit-references.md)

**Low (0)**
- _none_

**Info (3)**
- [04 -- Module inventory: complete type and function catalog](04-info-module-inventory.md)
- [05 -- Overlap analysis: go-plugin vs nanite internal/plugin](05-info-overlap-analysis.md)
- [06 -- Shared types: SDK is the source of truth](06-info-shared-types.md)

### By topic

**Registry correctness**
- [01 -- Shutdown deadlock in shared Registry](01-critical-shutdown-deadlock.md)

**Deletion impact**
- [02 -- Deletion breaks 4+ consumers and all external plugins](02-high-deletion-impact.md)

**Code hygiene**
- [03 -- Stale "Conduit Host" references in Registry stubs](03-medium-stale-conduit-references.md)

**Inventory**
- [04 -- Module inventory: complete type and function catalog](04-info-module-inventory.md)
- [05 -- Overlap analysis: go-plugin vs nanite internal/plugin](05-info-overlap-analysis.md)
- [06 -- Shared types: SDK is the source of truth](06-info-shared-types.md)

## Recommended next steps

1. **Do NOT delete `framework/libs/go-plugin/` without migrating all consumers.** Finding 02 enumerates the full blast radius. If the plan is to inline the SDK into nanite, the external plugins in `framework/plugins/nanite/` and `vanta-conduit` and `fragments-engine` must be updated first.
2. **Fix the Shutdown deadlock** (finding 01) before any release that exercises `Registry.Shutdown()` in production. The shared Registry is used by tests and lightweight hosts; the Nanite `Host` has its own shutdown path, but any test or tool using `Registry.Shutdown()` will deadlock.
3. **Rename "Conduit Host" references** to "Nanite Host" (finding 03) as part of the rename cleanup.
4. **Decide on SDK location.** The module is a zero-dependency interface package. Options: (a) keep as-is in `framework/libs/go-plugin/`, (b) move to `nanite/pkg/plugin/` as a public API package, (c) inline into `nanite/internal/plugin/` and break external plugin compilation. Option (b) is recommended if external plugins should remain supported.

## Known issues skipped

- Plugin isolation gap (design choice, tracked in `plugin-dev.md`)
- `Host.Shutdown()` mutex-across-Unload deadlock in nanite's `Host` (tracked in reviewer-backend.md, separate from the SDK `Registry.Shutdown()` deadlock filed here)
- Scaffold template broken imports (P0-1, tracked)
- All 25+ items in `plugin-dev.md` known limitations

## Noticed but out of scope

- `framework/.verify-sessionstats.buBu4U/` contains what appears to be a stale verification worktree with a full copy of nanite code. Its `go.mod` references `go-plugin` via a broken relative path (`../framework/libs/go-plugin` from inside framework). Suggest scope: `stale-worktree-cleanup`.
- `framework/plugins/nanite/` external plugins (`support-ticket`, `session-stats`, `observability-widgets`) import the SDK directly but their `AUDIT_RESULTS.md` files suggest they were audited by a different process. Their compatibility with the current SDK surface should be verified. Suggest scope: `framework-external-plugin-compat`.
- `fragments-engine/engine/` still imports go-plugin and has its own `internal/plugin/` host implementation. If fragments-engine is deprecated, its go-plugin dependency is dead. Suggest scope: `fragments-engine-deprecation-status`.
- `.archived/cortex/go.mod` references go-plugin. Archived, but confirms the module had broader historical reach.
