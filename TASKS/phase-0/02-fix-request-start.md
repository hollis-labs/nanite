# Fix `RequestStart` to call straight through to `Start()`

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/service/durable_agents.go` (`RequestStart`, ~line 496-513; reference: `RequestResume`, ~line 589-602; `Start`, ~line 332-391), `internal/api/durable_agents.go` (`handleDurableAgentLifecycleRequest`, ~line 266-299; route registration in `internal/api/api.go`)

## Context

TASKS.md Phase 0 item 2, and decision log §9 ("`RequestStart` — fix to match reality, not document the gap"):

> Confirmed no known reason for the two-step (`RequestStart` flips `status` to `start_requested` and stops; nothing transitions it further). Align it with `RequestResume`'s behavior — call straight through to the real `Start()`.

This is confirmed directly against the current code, not just the decision log's paraphrase. `internal/service/durable_agents.go`:

```go
func (s *durableAgentService) RequestStart(_ context.Context, id string) (*store.DurableAgentInstance, error) {
	before, err := s.store.GetDurableAgentInstance(id)
	if err != nil {
		return nil, err
	}
	inst, err := s.store.SetDurableAgentInstanceStatus(id, store.DurableAgentStatusStartRequested)
	if err != nil {
		return nil, err
	}
	s.recordEvent(&store.DurableAgentEvent{
		InstanceID:   id,
		EventType:    store.DurableAgentEventStartRequested,
		StatusBefore: before.Status,
		StatusAfter:  inst.Status,
		SessionID:    inst.CurrentSessionID,
	})
	return inst, nil
}
```

That's the entire function. It fetches the instance, flips `status` to `store.DurableAgentStatusStartRequested`, records an event, and returns. Nothing downstream ever picks up `start_requested` and drives it forward — there is no reaper, ticker, or handler in this codebase that watches for that status and calls `Start()` on its behalf. An instance sent through this path sits in `start_requested` forever until something else (a different code path) moves it.

Compare `RequestResume`, immediately below it in the same file, which is the pattern to mirror:

```go
func (s *durableAgentService) RequestResume(ctx context.Context, id string) (*store.DurableAgentInstance, error) {
	result, err := s.Resume(ctx, id, DurableAgentStartRequest{})
	if err != nil {
		if result != nil && result.Instance != nil {
			return result.Instance, err
		}
		inst, getErr := s.store.GetDurableAgentInstance(id)
		if getErr == nil {
			return inst, err
		}
		return nil, err
	}
	return result.Instance, nil
}
```

`RequestResume` takes a real `ctx`, calls straight through to `s.Resume(ctx, id, DurableAgentStartRequest{})` (the real launch path — the same one `Resume`'s full-bodied HTTP handler uses), and has real fallback error handling: if `Resume` errors but still returns a partial result with an instance, return that instance alongside the error; otherwise re-fetch the instance from the store so the caller at least gets current state instead of `nil`.

The real `Start()` (same file, ~line 332) does the actual work `RequestStart` should trigger: sets status to `starting`, resolves the wake payload/launch policy, selects-or-creates a launch session, attaches it, sets status to `active`, records `StartSucceeded`/`StartFailed` events, and (on success) delivers any wake prompt via `deliverWakePrompt`. Signature: `func (s *durableAgentService) Start(ctx context.Context, id string, req DurableAgentStartRequest) (*DurableAgentLaunchResult, error)`.

**Why this gap exists and why it's safe to fix now**: there is a second, already-correct REST route for starting a durable agent that bypasses this bug entirely. `internal/api/api.go` registers both:
- `POST /api/durable-agents/{id}/start` → `handleDurableAgentStart` (`internal/api/durable_agents.go:226`) — decodes a full `DurableAgentStartRequest` body and calls `Start` directly. This one already works correctly.
- `POST /api/durable-agents/{id}/start-request` → `handleDurableAgentStartRequest` (`internal/api/durable_agents.go:203`) → `handleDurableAgentLifecycleRequest(w, r, "start")` (`internal/api/durable_agents.go:266`) → `RequestStart(r.Context(), id)` — the broken no-body lifecycle-style route this task fixes.

The `-request` suffix family (`start-request`, `stop-request`, `pause-request`, `resume-request`) is the uniform, no-request-body lifecycle-action surface — `resume-request` already works (via `RequestResume`), `stop-request`/`pause-request` (via `RequestStop`/`RequestPause`) already do real work too. `start-request` is the one broken sibling in that family. This task brings it in line with its siblings, not just with `RequestResume` specifically.

## What to do

In `internal/service/durable_agents.go`, replace `RequestStart` with an implementation that mirrors `RequestResume`'s shape exactly, calling `Start` instead of `Resume`:

```go
func (s *durableAgentService) RequestStart(ctx context.Context, id string) (*store.DurableAgentInstance, error) {
	result, err := s.Start(ctx, id, DurableAgentStartRequest{})
	if err != nil {
		if result != nil && result.Instance != nil {
			return result.Instance, err
		}
		inst, getErr := s.store.GetDurableAgentInstance(id)
		if getErr == nil {
			return inst, err
		}
		return nil, err
	}
	return result.Instance, nil
}
```

Key changes from the current code:
- Signature: `_ context.Context` → `ctx context.Context` (the parameter is currently discarded; it must now be threaded through to `Start`).
- Body: replace the manual `GetDurableAgentInstance` → `SetDurableAgentInstanceStatus(..., StartRequested)` → `recordEvent` sequence with a direct call to `s.Start(ctx, id, DurableAgentStartRequest{})`, with the same error/fallback handling `RequestResume` already uses.

Note `Start` itself already records a `StartRequested` event internally (as part of its own sequence, before transitioning to `starting`) — so removing the manual `SetDurableAgentInstanceStatus(..., StartRequested)` step from `RequestStart` does not lose that event; `Start` produces an equivalent (arguably more complete, since it flows into `starting`/`active` right after) trail.

Check whether `store.DurableAgentStatusStartRequested` becomes unused anywhere else after this change (`grep -rn DurableAgentStatusStartRequested`); if it's now dead, that's a candidate for a follow-up cut but is NOT in scope for this task — leave the constant/status value alone unless it's clearly still referenced elsewhere (e.g. in the CHECK constraint or FE status displays).

Check `handleDurableAgentLifecycleRequest`'s existing error-handling switch (`internal/api/durable_agents.go:266-299`) already maps `store.ErrDurableAgentInstanceNotFound`, `service.ErrDurableAgentNoResumableSession`, and `service.ErrDurableAgentUnsupportedLaunchPlan` to the right HTTP status codes — since `RequestStart` will now be able to return these same error types (via `Start`'s underlying `durableAgentLaunchPolicyFor`/`selectOrCreateLaunchSession` calls) that `RequestResume` already surfaces through this same handler, no handler-side changes should be needed, but verify this by reading through the error paths, not just assuming.

## Done means

- `RequestStart` calls through to `Start` (not a manual status flip), matching `RequestResume`'s call-through-to-`Resume` shape.
- `POST /api/durable-agents/{id}/start-request` on a `sleeping`/`stopped` instance actually launches a session and reaches `active` status (or a real terminal failure state), the same way `POST /api/durable-agents/{id}/start` already does — not stuck at `start_requested`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass, including `internal/api/durable_agents_test.go` and `internal/store/durable_agents_test.go`.
- Manually or via an existing integration test, verify a real durable-agent instance can be started via the `-request` route end-to-end (status progresses to `active`, a session is attached) — this is a real behavior change (previously a no-op-beyond-status-flip), so a green build alone is not sufficient; exercise it per `EXECUTION-PROCESS.md`'s validation-checkpoint guidance.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

Implemented per spec. `RequestStart` in `internal/service/durable_agents.go` now
calls straight through to `Start(ctx, id, DurableAgentStartRequest{})`, mirroring
`RequestResume`'s call-through-to-`Resume` shape exactly (same fallback error
handling: if `Start` errors but returns a partial result with an instance, return
that instance alongside the error; otherwise re-fetch current state from the
store). The context parameter is now threaded through instead of discarded.

Verified the two supporting claims in the task file directly against the code
before changing anything: `Start`'s own sequence already records a
`DurableAgentEventStartRequested` event (as `StatusBefore: sleeping/stopped,
StatusAfter: starting`) before it proceeds to `starting`/`active`, so removing
the manual `SetDurableAgentInstanceStatus(..., StartRequested)` step does not
drop that event from the trail — confirmed against
`internal/store/durable_agents_test.go:166` which already asserts exactly this
event shape. And `handleDurableAgentLifecycleRequest`
(`internal/api/durable_agents.go:266-299`) needed no changes: its existing
error-handling switch maps `ErrDurableAgentInstanceNotFound`,
`ErrDurableAgentNoResumableSession`, and `ErrDurableAgentUnsupportedLaunchPlan`
to specific status codes and falls through to a generic 400 for anything else
(including `ErrDurableAgentWorkspaceRequired`, which `RequestStart` can now
newly surface via `Start`) — so the fallback branch already covers it.

`grep -rn DurableAgentStatusStartRequested internal/` confirms the status
constant is still live elsewhere (frontend_readiness.go's status list/display,
a2a_task_manager.go, durable_wake.go's in-flight-status check, and the
sqlite CHECK constraint in store/durable_agents.go) — left untouched, per the
task file's own note.

Deviation from the task file, logged per EXECUTION-PROCESS.md worker step 7
(not an escalation — the decision-log/task-file rationale held up fine against
the code as written; this is a downstream test-fixture correction the fix
itself required): two existing tests encoded the *old*, broken behavior
(`RequestStart` flips status to `start_requested` and stops) as their
expected outcome, so they broke once `RequestStart` started doing real work:

- `internal/service/durable_agents_test.go` —
  `TestDurableAgentServiceLifecycleRequests` called `RequestStart` on a fresh
  instance with no attached session and no workspace_id. Since `RequestStart`
  now calls `Start` with an empty `DurableAgentStartRequest{}` (same as
  `RequestResume`/`Resume` — the `-request` routes never carry a body), a
  fresh instance with nothing to reuse and no workspace now legitimately fails
  with `ErrDurableAgentWorkspaceRequired`. Updated the test to pre-attach a
  session (the realistic "sleeping instance that already has a session gets
  woken back up" scenario, which is exactly what the advisor-class default
  `ReuseLatestOrCreate` session policy is for) and assert the real, intended
  outcome: status reaches `active` with that session attached.
- `internal/api/durable_agents_test.go` —
  `TestDurableAgentsAPI_LifecycleAndSessionAttachment` called
  `POST .../start-request` *before* attaching a session and asserted
  `status == start_requested`. Reordered to attach the session first (via the
  existing `POST .../sessions` route), then call `start-request` and assert
  `200 OK` with `status == active` and `current_session_id` equal to the
  attached session's ID — this is the task's Done-means end-to-end
  verification ("a real durable-agent instance can be started via the
  `-request` route end-to-end") exercised through the existing HTTP
  integration-test path per `EXECUTION-PROCESS.md`'s validation-checkpoint
  guidance, rather than a separate manual run.

No handler-side (`internal/api/durable_agents.go`) or route-registration
(`internal/api/api.go`) changes were needed — both already correct, verified
by reading, not assumed.

Checks: `go build ./cmd/nanite/` passes. `go vet ./...` has one pre-existing,
unrelated failure in `internal/service/container.go` (`stopReaper`/
`stopRuntimeReaper` possible context leak, lines ~1186-1257). `go test ./...`
has two pre-existing, unrelated failures — both about the `question-form`
envelope type having no registered/on-disk JSON schema
(`internal/envelope`'s `TestEnvelopeSchemas_AllTypesHaveSchemas` and
`internal/mcp`'s `TestNaniteToolDescribe_ShowCardExamplesValidateAgainstSchemas`).
Confirmed both the vet finding and the two test failures are pre-existing and
out of scope for this task: `git diff --stat`/`git status --porcelain` show
this change touches exactly three files
(`internal/service/durable_agents.go`, `internal/service/durable_agents_test.go`,
`internal/api/durable_agents_test.go`) — nothing in `internal/service/container.go`,
`internal/envelope`, or `internal/mcp`. `internal/api/durable_agents_test.go`
and `internal/store/durable_agents_test.go` — the two files this task's Done
means calls out by name — both pass (no changes were needed to the latter;
its `RequestStart`/`StartRequested` references are only in an
events-ordering test that already expects the `starting`-status event shape
`Start` produces). `internal/service` and `internal/api` (the two packages
this change actually touches) both pass in full.

No escalations. Two pre-existing-failure notes above are flagged for
visibility, not fixed — out of scope for this task.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
