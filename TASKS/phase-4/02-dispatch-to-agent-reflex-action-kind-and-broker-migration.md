# Design and add the `dispatch_to_agent` reflex action kind; migrate the agent broker's remaining rules

**Phase:** 4
**Status:** not-started
**Depends on:** Phase 1's reflex opt-out field (the "required, cannot opt out" vs. "default-on, may opt out" distinction on `agent_reflexes`); Phase 0 #21 (`21-cut-modes.md`) must already be landed — it neuters rules 2-4 by deleting the `classify.ClassifyMode` call site that fed `in.Mode`/`in.ModeConfidence`.
**Touches:** `internal/agent/reflexes/*` (new action-kind type + executor branch), `internal/service/chat_broker_dispatch.go` (`attemptBrokerDispatch`, `buildBrokerInput` — full retirement of this 571-line file's broker call site), `internal/mcp/self_tools_dispatch.go` (`callExecuteTask`'s separate `st.ReflexSet`-driven dispatch hint mechanism — a second, deliberately-independent consumer of reflex/promptrouter matching, see Context), `cmd/nanite/main.go:354` (`agentbroker.New()` wiring — likely removed once the call site is gone), `internal/store/migrations/075_agent_reflexes.sql`-adjacent new migration (action-kind enum/config shape), `internal/api/reflexes.go` (CRUD/validation for the new action kind).

**Does NOT touch:** `libs/agentkit` (the external module at `/Users/chrispian/dev/hollis-labs/libs/agentkit`, a separate git repo also consumed by sibling apps) — this task neuters/retires Nanite's *call site* into that module, it does not edit the module itself.

## Context

TASKS.md Phase 3: *"Design and add the `dispatch_to_agent` reflex action kind; migrate the agent broker's remaining rules and promptrouter's phrase catalog into reflex predicates..."* This task covers the action-kind design and the agent-broker half; `02-migrate-promptrouter-to-reflexes.md` covers the promptrouter half (deliberately separate task, but depends on this one — the action kind needs to exist before promptrouter's catalog can migrate onto it).

Architecture doc `03-steering.md`: *"The agent broker's real intent ('know which agent to use based on context'; nudge on mail/detected intent) folds in as a new reflex action kind (e.g. `dispatch_to_agent`) alongside the existing five (`inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`)."* Decision log §10: *"Agreed in principle; concrete design still to be worked out."* This is real, not-yet-specified design work — the task is bigged appropriately below, not treated as mechanical translation.

### The agent broker today — six rules, three already dead

`internal/service/chat_broker_dispatch.go`'s `buildBrokerInput` (lines ~361-416) populates an `agentbroker.Input` struct passed to the external `agentkit/broker` module's `DeterministicBroker.Decide` (`libs/agentkit/broker/deterministic.go:40-110`). Six priority-ordered rules:

1. **Reflex override** (`deterministic.go:45-51`) — `in.ReflexMatchID != "" && in.ReflexAgentSlug != ""` → dispatch to that agent, reason `"reflex:"+id`, confidence = `in.ReflexConfidence`. **Reachable, needs migration.**
2. Mode=work, confidence≥0.85 → worker (`:56-62`). **Dead** — Phase 0 #21 deletes the `classify.ClassifyMode` call that populates `in.Mode`, so this permanently can't fire without touching `agentkit`.
3. Mode=work, confidence≥0.70 → worker (`:64-74`). **Dead**, same mechanism.
4. Mode=plan, confidence≥0.85, tier=open → planner (`:76-86`). **Dead**, same mechanism.
5. **`ScopeTier==TierOpen && ExecutionPattern==PatternSubagent` → planner**, mode-independent, fixed confidence 0.75 (`:88-98`, `:112-125`). **Reachable, needs migration.**
6. **Default → chat handles the turn, confidence 0** (`:100-109`). **Reachable — this is "no dispatch," may not need an explicit reflex predicate at all, just the absence of a match.**

Rules 1, 5, 6 are "the agent broker's remaining rules" this task migrates. Rules 2-4 need no migration — they're already permanently unreachable once Phase 0 #21 lands; do not spend effort porting them.

### Rule 1's current feed is `promptrouter`, not `internal/agent/reflexes` — a real coupling this task must resolve

`buildBrokerInput` (`chat_broker_dispatch.go:400`) calls `promptrouter.Match(userContent, tier, pattern, promptrouter.BuiltinReflexes())` and projects the result into `in.ReflexMatchID`/`in.ReflexAgentSlug`/`in.ReflexConfidence` (confidence = `match.Reflex.Priority / 100.0`). **Today's Rule 1 *is* the promptrouter integration** — there is no existing `internal/agent/reflexes`-table-backed feed into the broker at all. This task must explicitly decide (and document the decision in this file's Work Log): does the new reflex-table-backed matcher become Rule 1's replacement feed directly, or does the `dispatch_to_agent` action kind's own predicate/trigger evaluation subsume Rule 1 entirely (i.e., a `dispatch_to_agent` reflex fires on its own trigger, no separate "broker" concept survives at all)? The latter is more consistent with "reflexes are the single steering primitive" (architecture doc 03) — prefer it unless a concrete blocker surfaces; if choosing the former, document why.

### A second, deliberately-independent consumer exists — do not collapse it

`internal/mcp/self_tools_dispatch.go`'s `callExecuteTask` (~lines 116-154) runs its *own* separate reflex match (`st.ReflexSet` — a different reflex set than `promptrouter.BuiltinReflexes()`, may include user overrides loaded via `promptrouter`'s `LoadUserReflexes()`) to populate `dispatch.ReflexHints` for the `task_execute` self-tool's own dispatch call — independent of the upstream agent-broker's decision. `chat_broker_dispatch.go`'s own header comment (lines 7-17) documents this as two deliberately separate layers: *"Both layers run. Don't collapse them."* This task's broker-migration work only touches the upstream `attemptBrokerDispatch` path; `02-migrate-promptrouter-to-reflexes.md` covers whether/how this second consumer's `st.ReflexSet` mechanism also needs to move onto the new action kind (it consumes the same `promptrouter` catalog, so it can't be left half-migrated once `internal/promptrouter` is retired).

## What to do

1. Design the `dispatch_to_agent` reflex action kind: what config shape does it carry (target agent slug/role, a confidence/priority value, a reason string for `event_log` capture per decision log §14)? Add it to whatever enum/type currently lists the five action kinds (`inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`) in `internal/agent/reflexes`, plus executor support (the code path that actually performs a `dispatch_to_agent` action once a reflex fires — this is new: none of the five existing action kinds change control flow to a different agent).
2. Decide and document the Rule-1-feed question above (reflex-table match directly, or subsumed into the new action kind's own trigger evaluation).
3. Migrate rule 5 (`ScopeTier==TierOpen && ExecutionPattern==PatternSubagent` → planner, confidence 0.75) into a `dispatch_to_agent` reflex predicate — a class-bound (`agent_id IS NULL`) reflex keyed on the same tier/pattern signal.
4. Rule 6 (default, no dispatch) likely needs no explicit reflex — confirm the new dispatch path's absence-of-match behavior already falls through to normal chat handling; don't build a reflex for "do nothing" unless the executor design requires one.
5. Retire `internal/service/chat_broker_dispatch.go`'s `attemptBrokerDispatch` call site (per `docs/architecture-agents-tasks-2026-08-18.md`'s explicit instruction: *"Retire `internal/service/chat_broker_dispatch.go`'s `attemptBrokerDispatch` call site once migrated"*) and the `buildBrokerInput` function along with it. Remove the `agentbroker.New()` wiring in `cmd/nanite/main.go:354` if nothing else in Nanite calls into `agentkit/broker` after this (grep to confirm before removing — if something else uses it, scope that separately rather than guessing).
6. Wire `event_log` capture for `dispatch_to_agent` firings per decision log §14's write-site-discipline requirement: *"whatever logs a steering decision... needs to populate `metadata` with real reasoning (confidence, alternatives considered, why this and not that), not just a bare event name."*
7. Update `internal/api/reflexes.go`'s validation (`validateReflexDefinition`) to recognize the new action kind.

## Done means

- `agent_reflexes` supports a real `dispatch_to_agent` action kind end to end: definable via the existing CRUD/validation path, evaluable by the reflex executor, and capable of actually routing a turn to a different agent.
- The former Rule 5 behavior (open-tier subagent-pattern turns route to planner) is reproduced via a reflex, verified in a real session (a manually-triggered turn matching that tier/pattern combination routes to the planner agent, same as before).
- `attemptBrokerDispatch`/`buildBrokerInput` are deleted from `chat_broker_dispatch.go`; `libs/agentkit` itself is untouched (confirm via `git status` in that separate repo — this task should produce zero diff there).
- A `dispatch_to_agent` firing writes a real, populated `event_log` row (not a bare event name) — confirmed by triggering one and inspecting the row.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- The Rule-1-feed design decision (reflex-table-direct vs. subsumed-into-new-action-kind) is documented in this file's Work Log with the reasoning, since `02-migrate-promptrouter-to-reflexes.md` depends on knowing which was chosen.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
