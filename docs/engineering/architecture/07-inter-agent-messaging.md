# Inter-Agent Messaging

Two real messaging surfaces going forward, not three — see `GLOSSARY.md` for why a third (`gomsg`) was cut.

## `internal/messaging` — the real, active internal primitive

`agent_messages`, tuple-addressed `(session_id, agent_id) → (session_id, agent_id)`. This is the system with real traffic: subagent completions, notifications, human-agent communication, over MCP self-tools, HTTP, and CLI. Three independent delivery paths exist for the same message (SSE push to an open stream, a live-wake reactor that can spin up a new turn, and durable storage + poll/turn-start injection) — only the third is guaranteed to eventually surface a message.

## A2A — the external, spec-conformant door

Distinct from `internal/messaging` and not a duplicate of MCP — see `GLOSSARY.md`. Two concrete gaps in closing the conformance work already planned:
- **`a2a.task.cancel` needs a real implementation, not just a wire-level fix.** There is no `TaskManager.CancelTask` anywhere — the JSON-RPC method returns a hardcoded "not yet implemented" with no execution path at all.
- **`A2APushNotifier.ProcessPendingDeliveries` needs a real caller or an explicit scope decision.** Nothing drains queued push-notification deliveries today; its own doc comment claims a background ticker calls it, but none exists.

`TaskManager`'s routing logic (classify-target → route to `WorkflowLauncher.Launch` or `DurableWake.Wake`) stays shared infrastructure behind both A2A and the MCP control-plane tools once those are built — that reuse is correct independent of the A2A-vs-MCP question.

## Cut

`internal/messaging/gomsg` (`messaging_envelopes` table) — fully built, contract-tested, never constructed anywhere the process boots. `agent_mailbox_view` — zero call sites anywhere, references a file (`internal/composer/source_mail.go`) that doesn't exist in the tree.

## One resolution collapse

`MessageWakePolicy`'s resolution chain (`sessions.metadata` → `agent_profiles.constraints` → hardcoded global default) is the same shape as `resolveProvider`'s already-flagged bespoke fallback chain (see [Agent Launching](02-agent-launching.md)) — a fourth independent manual resolution walk. Collapses into the role→agent→task cascade rather than staying its own separate mechanism.
