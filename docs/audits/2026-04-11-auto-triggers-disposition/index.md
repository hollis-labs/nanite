# Audit: auto-triggers-disposition

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `auto-triggers-disposition`

**Interpretation:** Single-file archaeology of `internal/plugin/auto_triggers.go` plus its immediate dependency graph. The file was flagged by the 2026-04-10 plugin system plan-eval audit (INDEX.md item 12) as having unknown disposition — never opened during the plugin audit, in the plugin package, potentially load-bearing for the rearchitecture plan.

**Files read in full:**
- `internal/plugin/auto_triggers.go` (94 lines)
- `internal/plugin/auto_triggers_test.go` (136 lines)
- `internal/store/custom_actions.go` (161 lines)
- `internal/plugin/events.go:L350-370` (EmitActionTriggered)
- `cmd/nanite/main.go:L245-265` (registration call site)

**Files searched (grep, not full read):**
- `internal/chat/` — no consumers of `action.triggered`
- `internal/api/` — no consumers of `action.triggered`
- `ui/src/` — no consumers of `action.triggered`
- `internal/plugin/host.go` — `RegisterEventHook` signature and return type

## Methodology

**Categories applied:**
- Error Handling — checked all error return sites in the file
- Security — checked trust boundaries (DB query parameterization, event bus scope)
- Antipatterns — checked for dead code, incomplete feature paths
- Test Quality — reviewed test synchronization patterns

**Categories skipped:**
- Concurrency — no goroutines spawned directly by this file; event dispatch concurrency is owned by the host
- Standards/Tooling — deferred to the whole-repo tooling sweep (already completed)
- Memory & Resources — no allocations of concern

**Tools deferred:** `go vet`, `go test -race`, `golangci-lint` — already run by the `whole-repo-tooling-and-tests-sweep` audit on the same date. The golangci-lint finding for this file (error ignored on line 39) is cross-referenced in finding 01.

## Findings

### By severity

**Critical (0)**
- _none_

**High (0)**
- _none_

**Medium (2)**
- [01 -- RegisterEventHook error ignored](01-medium-error-ignored-on-register.md)
- [02 -- action.triggered event has no consumer](02-medium-no-consumer-for-emitted-event.md)

**Low (1)**
- [03 -- Tests rely on time.Sleep for synchronization](03-low-test-relies-on-sleep.md)

**Info (1)**
- [04 -- Auto-triggers disposition summary](04-info-disposition-summary.md)

### By topic

**Error Handling**
- [01 -- RegisterEventHook error ignored](01-medium-error-ignored-on-register.md)

**Antipatterns / Dead Code**
- [02 -- action.triggered event has no consumer](02-medium-no-consumer-for-emitted-event.md)

**Test Quality**
- [03 -- Tests rely on time.Sleep for synchronization](03-low-test-relies-on-sleep.md)

**Architecture**
- [04 -- Auto-triggers disposition summary](04-info-disposition-summary.md)

## Recommended next steps

1. **Decide the feature's fate.** Finding 02 is the key decision point. The auto-trigger mechanism is wired and executes but produces no effect because `action.triggered` events have no consumer. The hardening plan (`docs/hardening-phase-plan.md:38`) already flags this event for deletion. Either wire a consumer (complete the feature) or remove the dead path.
2. **Fix the ignored error** (finding 01) regardless of the feature decision. If the file survives, the error should be checked. If it's deleted, the finding is moot.
3. **Update the plugin audit plan** to reflect the disposition. The plan-eval audit's finding 06 (`06-high-existing-catalog-signature-code-ignored.md`) should be updated to note that `auto_triggers.go` has been reviewed and its disposition is "active but incomplete."

## Known issues skipped

- **Envelope emission system-wide breakage** — tracked in `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md`. Not re-flagged.
- **`Host.Shutdown()` mutex-across-Unload deadlock** — tracked in `reviewer-backend.md` pre-existing issues. Not re-flagged.

## Noticed but out of scope

- **`internal/store/custom_actions.go` has no input validation on `AutoTriggers` field.** The `CreateCustomAction` and `UpdateCustomAction` methods accept any string for `auto_triggers` — no validation that it's valid JSON or that the trigger names are from the known set (`on_new_session`, `on_agent_switch`, `on_mode_change`). A malformed value silently breaks the `json_each` query in `ListCustomActionsByTrigger`. Suggested follow-up scope: `custom-actions-validation` or include in a broader `store-input-validation` pass.
- **`ui/src/components/settings/ActionsPanel.tsx` lets users configure auto-triggers that have no backend effect.** The UI presents checkboxes for `on_new_session`, `on_agent_switch`, `on_mode_change` triggers. These are stored correctly but never acted upon (see finding 02). This is a UX honesty concern. Suggested follow-up scope: `frontend-dead-features` pass.
- **Two separate "trigger" systems in `internal/plugin/` with no cross-reference.** `triggers.go` (TriggerDispatcher, rule-based) and `auto_triggers.go` (AutoTriggerHandler, event-based custom actions) are unrelated mechanisms that share terminology. A developer reading the plugin package for the first time would reasonably confuse them. Suggested follow-up scope: naming/documentation pass for the plugin package.
