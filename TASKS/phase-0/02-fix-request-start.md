# Fix `RequestStart` to call straight through to `Start()`

**Phase:** 0
**Status:** not-started
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

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
