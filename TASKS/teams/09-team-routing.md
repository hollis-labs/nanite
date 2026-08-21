# Team routing — explicit addressing, semantic/fallback routing, and the resolution-failure stress test

**Phase:** 3 — Routing & messaging (`TASKS/teams`)
**Status:** not-started
**Depends on:** `05` (run-scoped `agent_reflexes`), `08` (`team_run_members` must be populated to resolve `@slot` addresses against)
**Touches:** `internal/service/team_routing.go` (new — explicit addressing resolver, semantic-rule seeding).

## Context

**Corrected self-tool name, real, easy to get wrong from the design doc's own prose:** the actual agent-facing self-tool for sending a message is **`message_send`**, not `send_message` (`internal/selftools/self_tools_transport.go:481`, `case "message_send":`, builds `messaging.SendInput`, calls `st.Messaging.SendMessage(ctx, msg)`). `send_message` is the name of the *reflex `action_kind`* (a different, related but distinct vocabulary item) — do not conflate the two when wiring explicit addressing to a real tool call.

**Corrected tuple-addressing model.** The design doc's "Slot resolution" section describes "the same tuple `internal/messaging` already addresses `agent_messages` by," implying one `(agent_id, session_id)` tuple. Confirmed directly: `agent_messages` (`internal/store/migrations/090_agent_messages_subagent_result_kind.sql`) actually has **two independent tuples** — `(from_session_id, from_agent_id)` and `(to_session_id, to_agent_id)` — each validated independently by `SendMessage` ("validates both ends of the address tuple," `internal/messaging/service.go:122`). This doesn't change what this task builds, but get the mental model right: explicit `@slot` addressing resolves the **`to_*`** tuple only; the sender's own `from_*` tuple is already known from the calling session.

**Real seed example to model a Team-scoped semantic routing rule against** (already cited in task `05`'s Context, repeated here since this is the task that actually writes these rows): `internal/agent/reflexes/seeds.go:334-352`'s `dispatch_to_agent_open_subagent` — `ActionKind: "dispatch_to_agent"`, `ActionSpec: {"agent_slug":"planner","confidence":0.75,"reason":"..."}` (shape documented `internal/store/agent_reflexes.go:45-47`). A Team's `architecture_question -> architect` routing rule (the design doc's own SME example) becomes one such row, with `workflow_run_id` set (task `05`) and `agent_slug` resolved to the concrete Team Slot's currently-resolved member.

**Multi-member addressing default — the design doc explicitly leaves this open, on purpose:** *"`@engineer` when the slot resolved to three concrete members — route-to-one, broadcast, or address-a-specific-member are all plausible; no syntax or default is chosen here, deliberately, since real usage should inform it."* This task must still make **a** concrete, working default (a Team can't ship with `@engineer` simply undefined) — treat it as provisional and flag it as such, not as a final answer to the design doc's open question.

**Provenance-tier requirement** — task `05` already established that run-scoped `dispatch_to_agent` rows must carry a `provenance_tier` that passes `ActionKindAllowsProvenanceTier` (migration `125`'s allow-list; `plugin` tier is confirmed denied for at least one kind). This task is the one that actually inserts these rows — apply task `05`'s documented choice, or make and document your own if `05` left it open.

**The second of the design doc's two explicitly-unresolved stress tests** (task `06` owns the first, phase-closure race): *"a message routes to a slot whose durable member is unavailable or whose fresh member fails to instantiate (routing-target resolution failure)... deserve concrete answers before implementation starts, not just a passing mention."*

**Routing provenance** — the design doc's "why did this message go to Architect" trace: *"the delivered message (`agent_messages`) → the reflex firing that produced it → the resolved slot → the concrete `(agent_id, session_id)` in `team_run_members`."* `EmitFirings`'s `alternatives_considered` telemetry is already general (not `dispatch_to_agent`-specific) per the reflex-taxonomy work already on `main` — this task should confirm it's queryable filtered by `workflow_run_id` (task `05`'s column), not build new persistence for it.

**`AuthorizedForVerb`'s `to_slot='self'` sharp edge, flagged by Phase 1's fresh reviewer, real for you specifically — this task is `AuthorizedForVerb`'s first real caller for `may_message`/`may_not_review`.** `internal/store/team_authority.go`'s `AuthorizedForVerb(ctx, teamID, fromSlot, verb, toSlot)` matches a stored `to_slot='self'` grant row whenever the literal string `"self"` is passed as the `toSlot` *argument* — the function's actual self-targeting check is `grantToSlot == toSlot`, which is trivially true if you ever call it with `toSlot == "self"` literally, regardless of what `fromSlot` really is. The function's real self-check (`grantToSlot == TeamAuthoritySelfSlot && fromSlot == toSlot`) only correctly detects genuine self-targeting when you pass `toSlot` as the *real resolved slot name*, not the sentinel string. **When this task calls `AuthorizedForVerb` to enforce `may_not_review`, always resolve `toSlot` to the real Team Slot name being addressed before calling — never pass the literal string `"self"` as `toSlot`.**

## What to do

1. **Explicit `@slot` addressing**: resolve a Team Slot name to its `team_run_members` row(s) (task `02`), then call the real `message_send` self-tool (or its underlying `messaging.SendMessage`, whichever integration point is cleaner given this code's actual caller) with the resolved `to_session_id`/`to_agent_id`. **Multi-member default: broadcast to every `active`-status member of the slot.** Document this explicitly as a provisional v1 default per the design doc's own "no syntax or default is chosen here, deliberately" framing — not a closed decision.

2. **Semantic/fallback routing**: for each Team routing rule (task `01`'s `routing_json`), insert a run-scoped `agent_reflexes` row (`workflow_run_id` set per task `05`, `action_kind='dispatch_to_agent'`, `ActionSpec.agent_slug` resolved against the target slot's currently-resolved member) at TeamRun launch time (called from task `08`, or from this task's own function that `08` calls — your call on the exact wiring boundary, document it). Add one additional lowest-priority row per Team for the coordinator fallback (`* -> orchestrator`, matching the design doc's three-tier framing) — `first_applicable`'s existing priority/`created_at` tie-break (already the live combining algorithm for this kind, no new logic needed) naturally makes this the fallback once all higher-priority run-scoped rules are checked and none matched.

3. **Routing-target-resolution-failure — concrete answer required, not deferred.** When a resolved target's `team_run_members.status != 'active'` (a durable member's underlying instance isn't in a resumable state, or a fresh member's session construction previously failed), pick one, implement it, and cover it with a test:
   - (a) the send fails loudly — surfaced back to the sender as a real tool-result error, matching `message_send`'s existing per-call validation style (recommended: matches this codebase's "hints not control, but real gates stay real gates" posture, and avoids silently masking a real failure behind an automatic reroute);
   - (b) automatically fall through to the coordinator-fallback routing rule instead of failing.
   Document your choice and reasoning in the Work Log.

4. **Provenance trace test**: write a test proving the full walk — a delivered `agent_messages` row → the `event_log` firing (via `EmitFirings`) that produced it → the resolved slot → the `team_run_members` tuple — is reconstructable end to end, filtered by `workflow_run_id`, with no new persistence layer required.

## Done means

- Explicit addressing (including the broadcast-to-multi-member default), semantic routing, and coordinator-fallback routing are each covered by a real test against a launched TeamRun fixture (from task `08`).
- The routing-target-resolution-failure stress test's chosen behavior is implemented and tested, not just documented.
- Provenance-tier choice for inserted `agent_reflexes` rows is documented and confirmed valid via a direct test.
- The provenance trace test passes.
- `go build`/`vet`/`test` clean.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
