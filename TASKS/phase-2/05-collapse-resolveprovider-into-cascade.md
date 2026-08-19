# Collapse `resolveProvider`'s fallback chain into the construction cascade; fold in `MessageWakePolicy`'s equivalent collapse

**Phase:** 2
**Status:** not-started
**Depends on:** Phase 1's `agents.model_id` (`TASKS/phase-1/02-add-agents-composition-columns.md`) resolved through a real cascade (`TASKS/phase-1/01-add-roles-table-and-cascade-resolution.md`); `01-wire-runtime-kind-routing.md` (this task's replacement logic needs `runtime_kind` already wired)
**Touches:** `internal/service/chat.go` (`resolveProvider`, `tryProviderCandidate`), `internal/chat/engine.go` (`InferProvider`), `internal/service/messaging_reactor.go` (`resolveMessageWakePolicy` — folded into this task, see Context)

## Context

Architecture doc `02-agent-launching.md`: *"`resolveProvider`'s bespoke fallback chain collapses into the construction-model cascade... Once an `agents` row has a real, cascade-resolved `model_id`, provider/model resolution is 'read the already-resolved value,' not a second independent walk."*

### `resolveProvider`'s exact current chain, verified

`internal/service/chat.go:1141-1195`: (1) `sessionProvider` (if set, checked against `s.providers`/CLI-provider check), (2) `agentProvider` via `tryProviderCandidate`, (3) `user_settings.default_provider`, (4) `user_settings.ProviderFallbackChain` (iterated), (5) `chat.InferProvider(model)` as the final floor. Five independent steps, none of which read a cascade-resolved value — each one is its own bespoke lookup.

### `MessageWakePolicy`'s resolution — same shape, folded into this task rather than a standalone Phase 5 task

Verified during Phase 5 planning research: `internal/service/messaging_reactor.go:87-121`'s `resolveMessageWakePolicy` is a three-step chain — `session.Metadata`'s `message_wake_policy` JSON key → `agent.Constraints` (`chat.ParseAgentConstraints`, resolved via `ResolveForSessionReadOnly`) → hardcoded `chat.SubagentPolicyAutoSummarize` fallback. Decision log §29 explicitly calls this "the same shape as `resolveProvider`'s already-flagged bespoke fallback chain... a fourth independent manual resolution walk in the codebase. Collapses into the role→agent→task cascade already locked for construction rather than staying its own separate mechanism." Since this mechanism depends on the same cascade-resolution infrastructure as `resolveProvider` (not on anything messaging-specific), and TASKS.md's own Phase 5 text has no explicit bullet covering it, this task absorbs it as a second, smaller instance of the identical pattern rather than leaving Phase 5 to invent a standalone "Messaging" task for one adjacent-shaped fix.

## What to do

1. Once an `agents` row has a real, cascade-resolved `model_id` (Phase 1) and `runtime_kind` (`01`), rewrite `resolveProvider` to read the already-resolved composition value as its primary path, falling back to `chat.InferProvider(model)` only for the genuine edge case of an agent with no resolvable composition (should become rare-to-nonexistent post-Phase-1, but keep the floor for safety rather than assuming it's dead).
2. Confirm `user_settings.default_provider`/`ProviderFallbackChain` either fold into the cascade as a genuine "role-level default" concept, or are confirmed superseded entirely by per-role/per-agent `model_id` assignment — a real design decision, document which was chosen and why in this file's Work Log.
3. Rewrite `resolveMessageWakePolicy` the same way: read the cascade-resolved value from the agent's composition (a `message_wake_policy` field belongs at whichever cascade tier makes sense — likely role-level default, agent-level override, task-level override for a specific dispatch) instead of its current three-step bespoke walk.
4. Preserve exact current behavior for every existing agent/session during the rewrite — this is a resolution-mechanism swap, not a policy change; a session that resolved to a particular provider/model or wake policy before this task must resolve to the same one after, unless the operator has explicitly reconfigured something via the new cascade.

## Done means

- `resolveProvider` reads the cascade-resolved value as its primary path; the bespoke five-step chain is gone or reduced to a documented, narrow fallback.
- `resolveMessageWakePolicy` is similarly collapsed into the cascade.
- No behavior change for any existing agent/session's resolved provider, model, or wake policy — verified by comparing resolution output before/after for a real sample of existing sessions.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
