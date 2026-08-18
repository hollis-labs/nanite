# Session Lifecycle & Recovery

A `sessions` row and the OS-process-level runtime (`agent_runtime`) are separate lifetimes — a session can survive many process deaths/relaunches without its own status changing. This split is why recovery is a multi-mechanism subsystem rather than a single state machine.

## Four recovery mechanisms, each answering a different question

- **Recovery Broker** — a booted process exits with an error while the daemon is still running; classifies the failure and may dispatch a replacement.
- **Orphan/Runtime Reaper** — reconciles stale `agent_runtime` rows against actually-dead PIDs, most commonly after a daemon restart.
- **Recovery Pack** — replays trailing context into a freshly cold-booted session so it isn't blind on its next turn.
- **Interrupted-turn detection** — a cheap, read-only heuristic surfaced to the frontend.

These are genuinely distinct, not redundant — no consolidation of the mechanisms themselves is needed. What *is* needed: they're scattered across unrelated packages today with nothing in the code layout reflecting they're one four-part system. **Restructure under a shared `internal/recovery/*` namespace**, matching the `agent/*`/`prompt/*` convention. Also: only the Recovery Broker currently writes a queryable postmortem trail — extend `event_log` logging to all four.

**HTTP-provider recovery**: real evidence (92% of all permanent-outcome recovery breadcrumbs in one dataset) that the Recovery Broker's retry path structurally can't succeed for HTTP-streamed sessions, escalating correctly-classified-transient failures to user-visible permanent errors. Decision: build a real HTTP-provider retry path (direct API retry, no bootdir involved), with explicit flagging/telemetry and a backoff strategy.

## Compaction

An escalating, budget-driven four-stage pipeline (drop enrichment → dedupe tool results → summarize oldest span → strip tool blocks). `compaction_events` (the structured postmortem of what a compaction pass did) is fully built on both read and write sides but the writer is never assigned at either production call site — a one-line wiring gap that also silently kills the "CompactionContract disclosure" feature (telling the model what was preserved after compaction). **Decision: wire it up** — low cost, real value.

## Handoffs, scratchpad, and Glass-4 — three distinct things, don't conflate

- **Glass-4** — the agent-self-authored continuity handoff. **Being made universal** (every session, not gated by a "long-running" classification) — motivated by general low-cost value, not an observed context-size problem. The classifier that used to gate it (`ClassifySessionIntent`) is cut entirely alongside this, since gating Glass-4 was its only real consumer.
- **The scratchpad** — a working-notes tool. Its value is the *act* of using it, not the content persisting. Kept, with TTL auto-pruning added (in-memory storage worth considering, given it's genuinely ephemeral).
- **P7** (the mechanism that used to snapshot the scratchpad into `handoff_stashes` at compaction time) — **cut**. Write-only, never read back for the vast majority of sessions, and the scratchpad was never meant to reliably hold the structured fields P7 extracted anyway.
- **`session_handoffs`** (the primary-agent handoff request/approval workflow) — real and fully wired (a prior audit incorrectly called this dead; verified otherwise), but never exercised in practice. Kept, evaluated like grounding — with a real design direction: bake handoff requests into reflexes/procedures/workflows as a triggerable action rather than building a fourth standalone steering mechanism.

## The context-replay gap (see also Cards)

Envelope/Card data emitted mid-turn is already excluded from the *current* turn's tool-result content the agent sees — but the full data still lands in the assistant's own persisted message text, which *is* replayed into every subsequent turn's context. None of the four compaction stages target this specifically. Real, scoped task: exclude Card data from replayed context, so a rich card shown once doesn't cost context on every later turn of a long session.

## Cleanup

`sessions.status`'s unused CHECK values (`sleeping`/`halted`/`terminated`, leaked vocabulary from `durable_agent_instances.status`) get dropped from the constraint — the actively-used `halted_at`/`halted_reason` column pair (a distinct, real mechanism sharing the word "halted") stays. `sessions.compaction_summary`/`compacted_at` are cut once `compaction_events` is wired. `session_agent_overrides` (zero rows, no clear consumer) is cut. `session_objects` (also zero rows, but real, exercised eviction logic) stays — it's just currently empty because nothing active needs it.
