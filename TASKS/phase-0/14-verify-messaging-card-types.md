# Verify and close: the four backend-only messaging card types

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** none expected — see Context. This task is a verification/close-out, not a code change, unless the worker's own re-verification finds something this planning pass missed.

## Context

TASKS.md Phase 0 item 14: "The four backend-only messaging card types (`message-request`/`reply`/`notification`/`handoff`) — no frontend component ever existed for them." Decision log §35: "`message-request`/`message-reply`/`message-notification`/`message-handoff` — backend-only, 'no frontend component yet' (unfinished scaffolding, not a deliberate omission like `chat-loop-budget-soft-warning`'s), and redundant with `agent_messages.kind`'s own CHECK constraint. Cut."

**Verified against real code (2026-08-18) — real doc/reality mismatch, report prominently rather than silently closing this.** A repo-wide search (Go, TypeScript, YAML manifests, generated envelope-type files) for `message-request`, `message-reply`, `message-notification`, `message-handoff`, and their un-hyphenated/underscored variants found **zero hits anywhere** — no manifest entry (backend `go-envelopes` manifest or otherwise), no Go registration, no generated TypeScript type, no frontend component.

The only real, related thing found is `agent_messages.kind`'s own CHECK constraint (migration `089_agent_messages_subagent_result_kind.sql`), which uses **unhyphenated**, lowercase-with-underscore-style values: `request`/`reply`/`notification`/`handoff`/`subagent_result` — these are plain DB enum values used to classify rows in the real, live `internal/messaging` system (`agent_messages` table), never implemented as hyphenated Card/envelope types the way TASKS.md item 14 describes.

**Most likely explanation**: TASKS.md's item 14 and decision log §35 are describing something that either (a) existed at an earlier point in this codebase's history and was already removed before this planning pass, (b) was planned/scaffolded in a design doc but never actually implemented in code, or (c) conflates the real `agent_messages.kind` enum values with a hypothetical Card-type naming convention that was never built. Whichever it is, **there is nothing to cut in the current tree** — this item's real effect on the codebase is zero.

## What to do

1. Re-run the verification grep the Orchestrator ran during planning, to confirm this finding still holds at execution time (the tree may have changed between planning and execution):
   ```
   grep -rn "message-request\|message-reply\|message-notification\|message-handoff" --include="*.go" --include="*.ts" --include="*.tsx" --include="*.yaml" .
   ```
   Also check the `go-envelopes` manifest directly (`libs/go-envelopes/manifest/envelopes.yaml`) for any entry matching these names.
2. If the grep still returns nothing: no code change is needed. Do not touch `agent_messages.kind`'s CHECK constraint or its `request`/`reply`/`notification`/`handoff`/`subagent_result` values — those are a real, live, unrelated mechanism (`internal/messaging`, see architecture doc `07-inter-agent-messaging.md`) and are explicitly out of scope for this item regardless of what it finds.
3. If the grep finds something this planning pass missed (i.e. one or more of these types DOES exist somewhere in the current tree), stop and escalate to the Orchestrator with exactly what was found — don't silently decide whether to cut it, since this task file was scoped assuming there's nothing there.
4. Record the outcome in the Work Log either way — this is a real "doc describes something that isn't in the code" finding worth preserving for the record, per `EXECUTION-PROCESS.md`'s escalation/documentation discipline, even though the resolution (no action) is not itself an escalation.

## Done means

- The verification grep has been re-run at execution time and its result (still-empty, or something found) is recorded in the Work Log.
- If still empty: this task is marked done with no code changes, and a note is added to `TASKS/ESCALATIONS.md` (if not already present from planning) recording that TASKS.md item 14 / decision log §35 describes card types that do not exist anywhere in the current codebase.
- If something is found: escalated to the Orchestrator rather than acted on unilaterally, and this task's status reflects "blocked pending escalation resolution" rather than "done."
- `agent_messages.kind`'s CHECK constraint and its real enum values are confirmed untouched either way.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
