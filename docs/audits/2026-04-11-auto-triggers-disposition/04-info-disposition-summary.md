# [Info] Auto-triggers disposition summary

**Scope:** internal/plugin/auto_triggers.go
**Topic:** Architecture
**Date:** 2026-04-11

## Problem

The plugin audit (2026-04-10) and its plan-eval audit both flagged `auto_triggers.go` as having unknown disposition. This finding records the disposition determination.

## Evidence

### What the file does

`AutoTriggerHandler` is a plugin event hook that:
1. Listens for `session.start`, `agent.switched`, `mode.changed` events (lines 19-23)
2. Looks up `custom_actions` rows whose `auto_triggers` JSON array contains the matching trigger name (line 54, via `Store.ListCustomActionsByTrigger`)
3. Emits an `action.triggered` event with the action's command and metadata (lines 75-80)

### Is it used?

Yes. `RegisterAutoTriggerHandler(pluginHost)` is called in `cmd/nanite/main.go:253`, so the handler is active in production.

### Is it correct?

The internal logic is correct:
- Event-to-trigger mapping is sound (line 19-23)
- Nil store guard (line 50-52) prevents nil pointer
- DB query via `ListCustomActionsByTrigger` uses parameterized SQL with `json_each` (no injection risk)
- Only enabled actions are returned by the query (store filters `ca.enabled = 1`)
- Invalid `auto_triggers` JSON is caught and logged (line 68-70)

### Is it safe?

No trust boundary issues. The handler reads from a local SQLite database (custom_actions table). The `Command` field is user-configured via an authenticated API endpoint. The emitted event stays within the in-process plugin event bus.

### Is it dead code?

Partially. The handler is wired and executes, but its output (`action.triggered` events) has no consumer. The mechanism fires into a void. See finding 02.

### Does it interact with the plugin trigger/event system?

Yes, directly. It uses `Host.RegisterEventHook` to subscribe and `Host.EmitActionTriggered` to publish. It sits in the event bus's dispatch chain alongside other hooks. It does not interact with `TriggerDispatcher` (the rule-based trigger system in `triggers.go`) — these are two separate trigger mechanisms that happen to share the word "trigger."

## Impact

No operational impact. This is a reference finding for future decisions.

## Recommendation

No action required on this finding. See findings 01 and 02 for actionable items.

## References

- `internal/plugin/auto_triggers.go` — full file (94 lines)
- `internal/plugin/auto_triggers_test.go` — 3 test functions, all passing
- `internal/store/custom_actions.go` — store layer with `ListCustomActionsByTrigger`
- `cmd/nanite/main.go:253` — registration call
- `docs/audits/2026-04-10-plugin-system-plan-eval/06-high-existing-catalog-signature-code-ignored.md:102-117` — prior audit flagging unknown disposition
- `docs/audits/2026-04-10-plugin-system-plan-eval/index.md:203` — prior audit index entry
