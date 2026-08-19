# Unify the three run-another-agent surfaces into one shared request/result type

**Phase:** 4
**Status:** not-started
**Depends on:** `TASKS/phase-0/03-fix-callertype-mistagging.md` (fixes durable-agent wake's `CallerChat`→`CallerBackground` mistagging — see Context for why this matters to this task specifically, not just as background cleanup)
**Touches:** `internal/dispatcher/dispatcher.go` (`Dispatcher.Run`, `CallerType` constants), `internal/chat/delegate.go` (`DelegationRequest`/`DelegationResult`), `internal/service/delegation.go` (`DelegateTask`/`DelegateAndAggregate`), `internal/subagent/types.go` (`SpawnRequest`/`Result`), `internal/service/subagent_runner.go` (`ChatRunner.Run`, `drainCapture`, fabrication/zero-output detection), `internal/service/durable_wake.go` (`DurableAgentWakeRequest`/`Result`), `internal/service/durable_agents.go` (`DurableAgentStartRequest`/`LaunchResult`), `internal/service/durable_agent_runtime_controller.go`

## Context

TASKS.md Phase 4 item 2: *"Unify the three run-another-agent surfaces into one shared request/result type."* Architecture doc `04-harness.md`: *"REST delegation, LLM-triggered subagent dispatch, and durable-agent wake all converge on the same `generateResponse` execution, but each currently has its own request/result type, draining logic, and completion-signaling mechanism. Collapsing to one shared shape is real, scoped work — keeping the genuinely different entry semantics... while unifying what's underneath them."*

### Confirmed: all three do converge on one execution path

Verified directly: all three ultimately call `Dispatcher.Run` (`internal/dispatcher/dispatcher.go:169-201`), which validates `CallerType`, stamps it on `ctx` via `WithCallerType`, and calls `d.runner.Invoke(...)` → `chatServiceImpl.generateResponse`. This is the real convergence point, not just a doc claim.

### The three surfaces, as they exist today

**A. REST delegation** — `internal/api/messages.go:75-137` (`POST /api/sessions/{id}/delegate`, `/delegate-aggregate`) → `chatServiceImpl.DelegateTask` (`internal/service/delegation.go:24-238`). Type: `chat.DelegationRequest`/`chat.DelegationResult` (`internal/chat/delegate.go:8-26`). **Synchronous drain**: a `for { select { case evt := <-ch: ...; case <-time.After(5*time.Minute): ...; case <-ctx.Done(): ...} }` loop (`delegation.go:169-237`) accumulating `delta` content directly in the calling goroutine, blocking the HTTP handler until completion, timeout, or cancellation. Completion signal: channel close.

**B. LLM-triggered subagent dispatch** — an MCP self-tool call → `subagent.Service.Spawn(ctx, subagent.SpawnRequest{...})` (`internal/subagent/service.go:597`, request type `internal/subagent/types.go:175-223`) → a persisted `subagent.Run` row → async `svc.execute`/`executeWithSlot` → `ChatRunner.Run(ctx, run *subagent.Run) (*subagent.Result, error)` (`internal/service/subagent_runner.go:530`, result type `subagent.Result{Summary, ResultJSON}`, `types.go:286-289`). Draining: async goroutine + capture channel (`drainCapture`, `subagent_runner.go:632`), gated by two detectors **unique to this surface and safety-critical, not incidental plumbing**:
- **Fabrication-suspected detection** (`errSubagentFabricationSuspected`, `detectFabrication`, `subagent_runner.go:63-74,664-691`) — tool called, no successful `tool_result`, yet non-empty assistant text.
- **Zero-output detection** (`errZeroOutput`, ~line 693+) — clean drain but nothing produced, routed to `StatusStalled`.

Completion signal: capture-channel close, classified through `classifyRunOutcome` into `subagent_runs.status`.

**C. Durable-agent wake** — `durableWakeService.Wake(ctx, instanceID, DurableAgentWakeRequest)` (`internal/service/durable_wake.go:195-250`) → `DurableAgentService.Start`/`Resume` (`internal/service/durable_agents.go:332-467`, types at lines 54-66) → `deliverWakePrompt` (`durable_agents.go:476-494`) → `DurableAgentRuntimeController.SendMessage` → `chatDurableAgentRuntimeController.SendMessage` (`internal/service/durable_agent_runtime_controller.go:57-63`) → **`ChatService.HandleMessage`** — the same entry point real user chat messages use, and the odd one out: `HandleMessage` fires `launchGeneration` in a goroutine and returns immediately (`chat.go:775`); the wake call **completes as soon as the message is queued, not when the agent finishes**. Completion tracking is instead a **lifecycle state machine**: `durable_agent_instances.status` transitions plus `durable_agent_events` rows (`recordWakeEvent`, `durable_wake.go:229-230,383-391`).

**A real, documented gap in this surface, confirmed in the code's own comments** (`durable_wake.go:307-324`): nothing ever transitions an instance's status back out of `Active` once a turn completes — for a process-class instance this is by design (Fresh-Per-Wake session policy makes "active" inert), but it means this surface's "lifecycle state machine" doesn't actually observe turn completion the way the other two surfaces' drains do. This is a real asymmetry to be honest about in the unified design, not something to paper over.

### `CallerType` doesn't map 1:1 onto the three surfaces today — this is why the dependency on Phase 0 #3 matters

The three `CallerType` values (`CallerChat`/`CallerSubagent`/`CallerBackground`, `dispatcher.go:49-68`) do **not** correspond 1:1 to surfaces A/B/C. REST delegation (A) and LLM-triggered subagent dispatch (B) both stamp `CallerSubagent`. Durable-agent wake (C) currently mis-stamps `CallerChat` instead of the intended `CallerBackground` — this exact bug is `TASKS/phase-0/03-fix-callertype-mistagging.md` (status not-started as of this planning pass). **If this task's unified request/result type keys any behavior off `CallerType` (a natural design choice, since it's already the one real discriminator all three pass through `Dispatcher.Run`), it must be built against the *fixed* three-way mapping, not the current two-value-in-practice one** — hence the hard dependency on Phase 0 #3 landing first, not just a nice-to-have ordering.

## What to do

1. Confirm Phase 0 #3 has landed (durable-agent wake correctly stamps `CallerBackground`) before starting. If it hasn't, this task cannot safely assume `CallerType` is a reliable three-way discriminator for the unified shape — escalate rather than build against the mistagged state.
2. Design one shared request/result type (naming and exact field shape are this task's real design work — not prescribed by any doc read so far) that each of the three call sites constructs before invoking the shared path, and that `Dispatcher.Run`/`generateResponse` returns uniformly. The shared shape must be a strict superset that lets each surface's distinct semantics still be expressed, not a lowest-common-denominator that drops capability:
   - Delegation's hard 5-minute synchronous timeout and content-accumulation contract.
   - Subagent dispatch's fabrication-suspected and zero-output detection — these change *outcome classification*, not just plumbing; do not weaken or bypass them in the unified path.
   - Durable wake's fire-and-forget-plus-status-polling shape — it fundamentally cannot synchronously drain without becoming a different mechanism (wakes are scheduled/background by nature). The unified result type should represent "queued, poll status separately" as a legitimate first-class result shape, not force durable wake into a synchronous-drain mold it can't fulfil.
3. Refactor each of the three call sites (`delegation.go`, `subagent_runner.go`/`service.go`, `durable_wake.go`/`durable_agents.go`) to construct and consume the shared type, preserving every existing behavior (timeout values, detection logic, event/status writes) exactly — this is a request/result-shape unification, not a behavior change.
4. Address the durable-wake completion-tracking gap (`durable_wake.go:307-324`'s "nothing transitions status back out of `Active`") only to the extent needed to give the unified result type an honest, non-misleading status to report for this surface — do not attempt a full redesign of durable-agent lifecycle tracking here if that turns out to be bigger than a shape-unification task should own; escalate if it is.
5. Do not touch `chat.AgentConstraints`/`RunawayFailCap`/`HardCeiling`/`IdleTimeoutSeconds` (Phase 0 #12 already settled these) or the subagent reaper (Phase 4 task 01) — this task is request/result-shape unification only, not termination-bounds work.

## Done means

- One shared request/result type exists and is used by all three call sites.
- Delegation's 5-minute timeout, subagent dispatch's fabrication/zero-output detection, and durable wake's fire-and-forget-plus-poll shape all still behave identically to today, verified by exercising each surface in a real session (a real delegate call, a real LLM-triggered subagent dispatch that deliberately produces zero output, a real durable-agent wake) — not just unit tests.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- No change to `CallerType` stamping beyond what Phase 0 #3 already fixed.
- The durable-wake completion-tracking asymmetry is either resolved within this task's scope or explicitly escalated as its own follow-up if it turns out to require a lifecycle redesign beyond shape unification.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
