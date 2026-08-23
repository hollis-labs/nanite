# Steering

Steering is the layer that decides what an agent does moment to moment. It went from five independently-evolved decision layers (agent broker, strategy planner, tool broker, skill broker, plus reflexes) down to one real primitive.

## Reflexes are the single steering primitive

`internal/agent/reflexes` — a DB-backed predicate/event/interval rule engine — absorbs the real, valuable jobs of everything else being retired:

- **The agent broker's real intent** ("know which agent to use based on context"; nudge on mail/detected intent) folds in as a new reflex action kind, `dispatch_to_agent`, alongside the existing five (`inject_reminder`/`force_tool_choice`/`send_message`/`halt_session`/`add_schedule`). **Done** — `TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md`.
- **`promptrouter`'s phrase-matching** — the reflex predicate engine already supports regex-match triggers, a superset of promptrouter's flat phrase-list matching. Promptrouter's job became a reflex trigger shape, not a separate system. **Done** — `internal/promptrouter` is deleted in full; its catalog lives on as `dispatch_to_agent` reflex rows (`internal/agent/reflexes/seeds.go`) — `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`.
- **Skill-usage and scratchpad-usage nudging** — reflexes point an agent toward using an assigned skill or its own scratchpad on a specific, testable condition, the same pattern reflexes already use to point agents at named procedures.
- **`session_handoffs` triggering** — a handoff request becomes something a reflex condition or a workflow step can initiate, not a separate steering mechanism.

The `dispatch_to_agent` action-kind design and the promptrouter catalog migration above are both finalized (see the two linked task files' Work Logs for the concrete shapes and decisions). **Resolved 2026-08-19** — the "carrying six jobs" watch item is closed by a dedicated precedence/taxonomy design: see [Reflex Action Taxonomy & Precedence](10-reflex-action-taxonomy.md) (`TASKS/phase-4/10-reflex-architecture-review.md`). Summary: action kinds get a declared `category` (advisory `system_message` vs deterministic `execute_action`), a per-kind combining algorithm (`deny_overrides`/`first_applicable`/`all_applicable`, closest precedent XACML's policy-combining algorithms) replacing today's overloaded `priority` field, a real provenance/authority-tier facet, and a three-level recurrence cascade (system default → kind override → reflex override). It also fixes concrete bugs found during the review: `force_tool_choice` reflexes can currently co-fire with contradictory directives; `halt_session` neither preempts other actions in the same evaluation pass nor stops the current turn synchronously (it only stamps a DB flag checked on a later request); and reflex telemetry is currently split across two different sinks (`event_log` vs `playbook_match_log`) depending on which of three call sites handled the firing, with `fired_count` and plugin observability hooks blind to one of the three paths entirely — the design requires one consistent trace record regardless of entry point. Design is operator-signed-off; implementation is deferred to follow-up work, not yet filed.

## What's cut

- **Modes, in full**: Session Mode (+ `modes` table + `/mode`/`/chat`/`/plan`/`/work` slash commands), Legacy Agent Mode, the mode↔agent junction table, the per-turn `classify.ClassifyMode` signal. Real usage was near-zero; permission/procedure effects modes provided are better done via reflexes. The context/slot system's `INV4` invariant ("mode-aware content swap") becomes vacuous once this lands — needs a deliberate small update, not silent rot.
- **The strategy planner, in full.** Both outputs were already effectively dead: `Approach` was decorative (logged, never gated behavior); `MaxTurns` is superseded by harness-level termination bounds. Hard-gating experiments here produced dead-end conversations — hints over control, not just a preference.
- **Grounding's pre-dispatch memory-recall subsystem, in full.** The implementation, persistence adapter, and self-tools integration seam were never wired in production and were retired by `TASKS/audit-remediation/09-production-islands/01-grounding-memory-recall.md` (AD-06). The broader `internal/memory.Service` and its live context-broker/extraction consumers remain.
- **The skill broker and tool broker, as formal abstractions.** What's cut: the rule-matching wrapper (`go-toolbroker`/`NaniteDefaultRules`, `internal/skillbroker`). What stays: the underlying catalogs and the selection/filter logic that does real work (permissions, allowlist, chat-surface exclusion, progressive discovery).
- **`agent_known_tools.BumpActivation`'s usage-based "learning."** Broke prompt-cache stability and produced unreliable results. Not being revived — preference, if wanted, comes from explicit assignment through the construction model.

## Kept, actively being evaluated

- **`pending_reflexes`** (agent proposes its own reflex, operator approves) — complete backend, missing only the self-tool that would let an agent call it. Build the missing piece and test before deciding.

## One correctness gap carried into implementation

- Truncation, broadly (not just one specific case), has a real documented history of causing bugs here — needs deliberate care wherever it's touched next, not "add a limit and move on."

(Tool concurrency-safety classification was the other gap listed here; it's closed — `known_tools.concurrency_safe` declared metadata, fail-closed default, replaced the name-heuristic in full. `TASKS/phase-4/07-tool-concurrency-safety-classification.md`.)
