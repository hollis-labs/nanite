# A2A conformance: real method names, real CancelTask, verify push delivery

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/api/a2a_jsonrpc.go` (method-name constants, `handleTaskCancel`), `internal/service/a2a_task_manager.go` (`TaskManager` — needs a new `CancelTask` method), `internal/service/a2a_push_notifier.go` (`A2APushNotifier.ProcessPendingDeliveries` — verification only, see part (c)), `internal/service/durable_agents.go` (`RequestStop`, likely reuse target), `internal/service/durable_agent_runtime_controller.go` (`CancelSession`/`StopSession`, likely reuse target), `internal/store/a2a_tasks.go` and `internal/store/migrations/088_a2a_tasks.sql` (`A2ATask` schema — read for routing fields, no schema change expected), `internal/a2a/*` (protocol types — read/possibly extend)

## Context

TASKS.md item 8 bundles three sub-parts. Architecture doc `docs/engineering/architecture/07-inter-agent-messaging.md` covers (a) and (b) directly; part (c) needs a **reality correction** against decision log §28 — see below, don't skip it.

### (a) Real method names — decision log §2

> A2A stays, distinct from MCP, and the goal is genuine spec conformance (not just internal reuse of its routing logic). Concrete task, not yet started: close the method-name conformance gap already self-flagged in the code (`internal/api/a2a_jsonrpc.go` uses Nanite-invented method names like `a2a.task.submit` instead of the real spec's `SendMessage`/`GetTask`/`CancelTask`)...

**Verified**: `internal/api/a2a_jsonrpc.go:26-31` currently defines:

```go
const (
	methodTaskSubmit       = "a2a.task.submit"
	methodTaskGet          = "a2a.task.get"
	methodTaskCancel       = "a2a.task.cancel"
	methodTaskProvideInput = "a2a.task.provideInput"
)
```

**Read the code comment directly above this block (lines 14-25) before touching it — it materially changes what "do the rename" means here.** It says, verbatim, that these method names were never spec-verified, and that the ticket which introduced them (CW-20260814-0016) explicitly required a real spec check first that never happened:

> Two independent spec lookups during review (2026-08-15) returned inconsistent method-name conventions ("SendMessage"/"GetTask"/"CancelTask" vs "a2a/SendMessage" etc.), neither matching what's used here — not authoritative enough to safely rename against.

In other words: **TASKS.md's own phrasing ("real method names `SendMessage`/`GetTask`/`CancelTask`") is repeating a lead from the code comment, not a confirmed spec citation** — the same comment says that exact name set was one of two *inconsistent* results from an earlier, non-authoritative lookup. Do not rename directly to `SendMessage`/`GetTask`/`CancelTask` on the strength of TASKS.md's phrasing alone. Do the real spec-verification pass first (the current A2A spec at a2a-protocol.org — JSON-RPC method names section), confirm the actual current method names, and use those. If they do turn out to be `SendMessage`/`GetTask`/`CancelTask`, great — but confirm it, and record the spec section/URL you checked in the Work log so this doesn't stay an unverified lead a third time.

There's also a fourth method in code with no TASKS.md-mentioned spec equivalent: `methodTaskProvideInput = "a2a.task.provideInput"` (added later, CW-20260814-0017, for resolving a paused workflow gate — see `handleTaskProvideInput`, `a2a_jsonrpc.go:181-221`). Check whether the real A2A spec has an equivalent concept (something like a "provide input" / resume-a-task method) or whether this is a legitimate Nanite-specific extension that should keep a clearly-non-spec name instead of being force-fit into spec vocabulary. Don't guess — if the spec doesn't cover this, document that finding rather than inventing a fake-spec-shaped name for it.

Also close the adjacent naming-cleanup note from decision log §2: "clean up any remaining naming ambiguity against the old, already-renamed-away internal messaging system that used to share the 'a2a' name (now `internal/messaging`/`agent_messages`)." Grep for any stray "a2a" references inside `internal/messaging` or its tests that should have been renamed already and weren't.

### (b) Real `TaskManager.CancelTask` — decision log §28

> **`a2a.task.cancel` needs a real implementation, not just a wire-level fix.** There is no `TaskManager.CancelTask` anywhere — the JSON-RPC method returns a hardcoded "not yet implemented" with no execution path at all, regardless of transport.

**Verified exactly as described**: `internal/api/a2a_jsonrpc.go:157-176`'s `handleTaskCancel` parses and validates the request, then unconditionally returns `respondJSONRPCError(w, a2a.JSONRPCInternalError, "Task cancellation not yet implemented", nil, req.ID)` (line 175) with a `TODO(CW-20260814-0016)` comment. `internal/service/a2a_task_manager.go` has `SubmitTask`, `GetTask`, `ProvideTaskInput`, and internal helpers (`classifyTarget`, `submitWorkflowTask`, `submitInstanceTask`, `deriveTaskState`, etc.) — no `CancelTask` method exists at all, confirmed via grep across the whole repo.

**Routing anchors to build from** (`internal/store/migrations/088_a2a_tasks.sql`): `a2a_tasks.target_kind` is `'workflow'` or `'instance'`, with `workflow_run_id` and `durable_agent_instance_id` FK columns respectively — the same two-substrate routing `SubmitTask`/`classifyTarget` already use (`internal/service/a2a_task_manager.go:160,185,233`). A real `CancelTask` should follow the same branch:
- **`target_kind = 'instance'`**: there's an existing stop primitive to reuse rather than building new cancellation machinery — `durableAgentService.RequestStop(ctx, id)` (`internal/service/durable_agents.go:515`) and/or `chatDurableAgentRuntimeController.CancelSession`/`StopSession` (`internal/service/durable_agent_runtime_controller.go:16,49`). Check which one is the right layer to call from `TaskManager` (they may not be equivalent — `RequestStop` looks like it operates on `durable_agent_instances`, the same table `a2a_tasks.durable_agent_instance_id` points at).
- **`target_kind = 'workflow'`**: no existing cancel/stop primitive was found in `internal/agentworkflow` or `internal/service/workflow*.go` (checked via grep for `Cancel`/`Stop`/`Halt`). This side may be a genuine from-scratch gap — check whether `workflow_runs` has *any* interrupt mechanism today (a status column, a context-cancellation path in `WorkflowLauncher`, anything). If nothing exists and building one is nontrivial (not just "flip a status column" but actually stopping in-flight execution), **escalate this specifically** rather than half-implementing a `CancelTask` that only works for one of the two target kinds without saying so.

After canceling, update the task's derived state via the existing `deriveTaskState`/`updateTaskStateFailed`-shaped pattern so `GetTask` reflects `'canceled'` (the `a2a_tasks.state` CHECK constraint already includes `'canceled'` as a valid value — `088_a2a_tasks.sql` line 28 — so no schema change needed there), and fire the existing push-notification hook (`enqueuePushNotification`, `a2a_task_manager.go:506`) on the transition, matching how every other state change in this file already does it.

### (c) `A2APushNotifier.ProcessPendingDeliveries` — REALITY CHECK, read before doing anything

Decision log §28's second bullet, and `docs/engineering/architecture/07-inter-agent-messaging.md`'s framing, both say:

> **`A2APushNotifier.ProcessPendingDeliveries` needs a real caller or an explicit scope decision.** Nothing drains queued push-notification deliveries today — its own doc comment claims a background ticker calls it, but none exists.

**This claim is stale as of today (2026-08-18) — verify before acting on it.** A real ticker already exists:

```go
// cmd/nanite/main.go:1074-1092
// Periodic A2A push notification delivery processor.
// CW-20260814-0018: Best-effort push delivery with bounded retries (max 3,
// exponential backoff). Deliveries are enqueued on TaskState transitions.
if container.TaskManager != nil {
	lc.Go("a2a-push-delivery", func(ctx context.Context) {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := container.TaskManager.PushNotifier().ProcessPendingDeliveries(ctx); err != nil {
					slog.Warn("a2a push delivery: processing failed", "error", err)
				}
			}
		}
	})
}
```

This was added by commit `80ded64` ("A2A: push notification config + best-effort delivery (CW-20260814-0018)"), dated **2026-08-15** — three days *before* the decision-log audit pass that wrote the "no such ticker exists anywhere in the repo" claim on 2026-08-17/18. `container.TaskManager` is constructed unconditionally at `cmd/nanite/main.go:530` in the real boot path (not behind any flag), so the `!= nil` guard always passes in production — the ticker is live, not dead code. Separately, the enqueue side is also fully wired: `TaskManager.enqueuePushNotification` (`a2a_task_manager.go:506`) is called from four real state-transition sites in that file (verified via grep), not just defined and orphaned.

**So this sub-part is not "build a ticker or scope it out" — it's verification of an already-built mechanism the docs haven't caught up to yet.** Concretely:

1. Confirm the ticker fires and drains correctly under real conditions — not just reading the code. Submit a real A2A task with a `PushNotificationConfig` pointing at a local test HTTP listener (or use the existing test coverage in `a2a_push_notifier.go`'s test file as a starting point and extend it if it doesn't already cover the ticker-driven path end to end), trigger a state transition, and confirm delivery happens within the 30s tick.
2. Check the doc comment on `ProcessPendingDeliveries` itself (`internal/service/a2a_push_notifier.go:73-75`, "This is called by the background worker ticker") — it's now *accurate*, unlike the decision-log's characterization of it. No change needed there unless your verification finds something actually wrong.
3. If verification turns up a real gap (e.g. the ticker never actually fires in some real deployment path, or enqueued deliveries silently never drain in practice despite the wiring looking correct), **that's when you escalate** — with the specific gap, not the original "ticker vs. scope-out" framing, which reality has moved past.
4. Update `docs/engineering/architecture/07-inter-agent-messaging.md` and this decision-log entry's characterization is out of scope to edit (the log is a historical record), but flag the correction in your Work log clearly enough that whoever reads it next doesn't have to re-discover this.

## What to do

1. Do the real A2A spec-verification pass for the JSON-RPC method names (part a). Rename `methodTaskSubmit`/`methodTaskGet`/`methodTaskCancel` (and decide on `methodTaskProvideInput`) to the verified real names. This is a hard rename per `docs/tool-naming-convention.md`'s "Hard rename policy (no compat shims)" precedent for this codebase generally, and there are no external A2A callers today (per the code comment) — no alias/compat layer needed.
2. Implement `TaskManager.CancelTask` (part b) in `internal/service/a2a_task_manager.go`, wiring `internal/api/a2a_jsonrpc.go`'s `handleTaskCancel` to call it instead of returning the hardcoded not-implemented error. Cover both `target_kind` branches, or escalate explicitly if the `'workflow'` branch has no real primitive to build on (see above).
3. For part (c): do the verification pass described above. Do not build a new ticker (one exists) and do not write a "v1 scope-out" decision (the mechanism appears complete) unless verification proves otherwise.
4. Update `internal/api/a2a_jsonrpc_test.go` and `internal/service/a2a_task_manager_test.go` for the renamed methods and the new `CancelTask` path.

## Done means

- JSON-RPC method-name constants in `a2a_jsonrpc.go` match a verified real A2A spec citation (recorded in the Work log — spec section/URL checked), not a guess.
- `TaskManager.CancelTask` exists with a real execution path for at least the `'instance'` target kind (reusing an existing stop/halt primitive rather than inventing new session-control machinery); the `'workflow'` kind is either implemented or explicitly escalated with the specific gap found.
- `handleTaskCancel` calls the real implementation and returns real state, not a hardcoded error.
- `A2ATask.state` transitions to `'canceled'` on a successful cancel, and the existing push-notification hook fires on that transition like every other state change.
- Part (c) verification is complete and documented in the Work log: either "ticker verified working end-to-end via a real test task + local delivery listener" or a specific, concrete gap escalated — not a restatement of the original "ticker or scope-out" choice.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/api/...` and `internal/service/...`.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
