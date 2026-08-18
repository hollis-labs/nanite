# Inter-Agent Messaging

Two real messaging surfaces going forward, not three — see `GLOSSARY.md` for why a third (`gomsg`) was cut.

## `internal/messaging` — the real, active internal primitive

`agent_messages`, tuple-addressed `(session_id, agent_id) → (session_id, agent_id)`. This is the system with real traffic: subagent completions, notifications, human-agent communication, over MCP self-tools, HTTP, and CLI. Three independent delivery paths exist for the same message (SSE push to an open stream, a live-wake reactor that can spin up a new turn, and durable storage + poll/turn-start injection) — only the third is guaranteed to eventually surface a message.

## A2A — the external, spec-conformant door

Distinct from `internal/messaging` and not a duplicate of MCP — see `GLOSSARY.md`. Conformance work closed 2026-08-18 (`TASKS/phase-0/08-a2a-conformance.md`):

- **Method names are spec-verified.** `a2a_jsonrpc.go`'s JSON-RPC method constants are `SendMessage`/`GetTask`/`CancelTask`, confirmed against the official A2A spec (github.com/a2aproject/A2A, release v1.0.1) §5.3/§9.4 — not the earlier Nanite-invented `a2a.task.*` names, and not a guess off unverified draft-spec leads. `a2a.task.provideInput` (gate-resolution, CW-20260814-0017) has no spec equivalent and correctly stays a non-spec-shaped Nanite extension — the spec resumes a paused task via a new `SendMessage` on the same `taskId`, not a dedicated method.
- **`TaskManager.CancelTask` has a real implementation for the `'instance'` target kind**, reusing `DurableAgentService.RequestStop` (the same primitive `WorkflowLauncher` itself calls to tear down a finished workflow-run instance). The `'workflow'` target kind is **not** implemented: `WorkflowLauncher.Launch` runs the engine synchronously in-process with a locally-deferred `context.CancelFunc` never exposed to any registry a later, separate `CancelTask` call could reach — there is no real interrupt primitive to call yet. `CancelTask` returns a typed `ErrWorkflowCancelUnsupported` for that branch rather than faking a `'canceled'` state. See `TASKS/ESCALATIONS.md` for the full finding.
- **`A2APushNotifier.ProcessPendingDeliveries` already has a real caller — this doc's earlier "nothing drains queued deliveries" claim was stale.** A background ticker (`cmd/nanite/main.go`, `lc.Go("a2a-push-delivery", ...)`, added by commit `80ded64`, 2026-08-15) calls it every 30s, and `container.TaskManager` is constructed unconditionally in the real boot path — the ticker is live, not dead code. Verified end-to-end (not just by reading the code): `internal/service/a2a_push_notifier_test.go`'s `TestA2APushNotifier_TickerDrivenPath_EndToEnd` drives a real `TaskManager.CancelTask` state transition through the actual `enqueuePushNotification` call site, then calls `ProcessPendingDeliveries` (exactly what the ticker calls) and confirms a real local HTTP listener receives the notification.

`TaskManager`'s routing logic (classify-target → route to `WorkflowLauncher.Launch` or `DurableWake.Wake`) stays shared infrastructure behind both A2A and the MCP control-plane tools once those are built — that reuse is correct independent of the A2A-vs-MCP question.

## Cut

`internal/messaging/gomsg` (`messaging_envelopes` table) — fully built, contract-tested, never constructed anywhere the process boots. `agent_mailbox_view` — zero call sites anywhere, references a file (`internal/composer/source_mail.go`) that doesn't exist in the tree.

## One resolution collapse

`MessageWakePolicy`'s resolution chain (`sessions.metadata` → `agent_profiles.constraints` → hardcoded global default) is the same shape as `resolveProvider`'s already-flagged bespoke fallback chain (see [Agent Launching](02-agent-launching.md)) — a fourth independent manual resolution walk. Collapses into the role→agent→task cascade rather than staying its own separate mechanism.
