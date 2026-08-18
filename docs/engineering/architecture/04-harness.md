# Harness

The shared turn-loop mechanics (`generateResponse`) every launched agent runs through, regardless of entry point — interactive chat, LLM-triggered subagent, REST-triggered delegation, or a durable agent's scheduled wake all converge here via a `CallerType`-tagged dispatcher.

## Termination bounds, simplified

Two redundant "soft" turn-budget mechanisms are cut: the strategy planner's `MaxTurns` and, separately, `AgentConstraints.MaxTurns` (a second, independent soft budget that also only fired a warning and never stopped anything). The real, hard stoppers stay as-is: `RunawayFailCap` (consecutive tool failures), `HardCeiling` (an absolute iteration backstop — already the generous, non-strategic ceiling a redesign would otherwise need to invent), `IdleTimeoutSeconds`, and natural `end_turn`.

**Idle-timeout/reaper aggressiveness is a real, still-open reliability question**, not resolved by the current activity-reset fix. Real operator experience: more false reaps historically than genuine idle/runaway catches. The shipped fix (activity-reset idle timer + non-resetting hard ceiling) should help but needs verification against real behavior before being trusted as fully resolved.

## Run-another-agent surfaces, unified

REST delegation, LLM-triggered subagent dispatch, and durable-agent wake all converge on the same `generateResponse` execution, but each currently has its own request/result type, draining logic, and completion-signaling mechanism. Collapsing to one shared shape is real, scoped work — keeping the genuinely different entry semantics (synchronous-drain for delegation, fabrication/zero-output detection for subagents, lifecycle state machine for durable) while unifying what's underneath them.

## Tool lazy-loading

`NANITE_TOOLS_LAZY_LOAD` (essential/lazy tool partitioning — loads full tool schemas on demand instead of always) is fully built and tested but off by default. Decision: turn it on, tune based on real behavior.

## CLI-vs-API routing

See [Agent Launching](02-agent-launching.md) — `agents.runtime_kind` is the one typed field this now resolves to, replacing four scattered string-matching call sites.
