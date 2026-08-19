# Gate agent_update/agent_create self-tools behind ManageClass.Editable()

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/mcp/self_tools_transport.go` (`callUpdateAgent` lines 626-657, `callCreateAgent` lines 579-601), `internal/agent/source_class.go` (`ManageClass.Editable()`, `Classify`)

## Context

Found during Phase 1-6 planning verification (2026-08-18), not from the original two-day design review — a live security gap, not a decision-log mismatch, so there is no "decision" to re-litigate here, only a fix to scope correctly.

`internal/mcp/self_tools_transport.go`'s `callUpdateAgent` (write at line 653: `st.Store.UpdateAgent(a)`) and `callCreateAgent` (write at line 595: `st.Store.CreateAgent(a)`) both write directly to the agent store with zero `ManageClass`/`Editable()`/`Classify()` check anywhere in either function (confirmed by full read of both function bodies). Any chat session with the `agent_update` or `agent_create` self-tool (registered at `internal/mcp/self_tools.go:155`, category `CategoryAgent` per `internal/tool/register.go:37,39`) can silently overwrite or fabricate an agent profile — including a builtin/seed agent profile, which `ManageClass.Editable()` exists specifically to prevent.

The gate: `internal/agent/source_class.go:39` — `func (c ManageClass) Editable() bool { return c == ManageClassManaged }`. Four `ManageClass` values at lines 19 (`Managed`), 25 (`Internal`), 30 (`Plugin`), 35 (`External`) — only `Managed` is editable. `Classify` method at `source_class.go:79`.

The correct pattern already exists at the REST layer — `internal/api/agents.go`'s `handleUpdateAgent`/`handleDeleteAgent` and `internal/api/agent_capabilities.go`'s `requireMutableAgent` both gate on `class.Editable()` — but no self-tool in the codebase follows it (`grep -rn "Editable()\|ManageClass\|\.Classify(" internal/mcp/*.go` returns zero matching hits in any self-tool file; the one unrelated hit in `self_tools_dispatch.go:121` is a different `classify.Classify` — dispatch intent-tier classification, not `ManageClass`). This isn't a regression — the self-tool surface was simply never wired to this gate at all since these tools were added.

## What to do

1. In `callUpdateAgent` (`self_tools_transport.go:626-657`), before the `st.Store.UpdateAgent(a)` write at line 653: load the target agent's current `ManageClass` (via `Classify`, `source_class.go:79`), check `.Editable()`, and reject the update with a clear tool-result error if false — match the rejection behavior/message shape already used by `requireMutableAgent` in `internal/api/agent_capabilities.go`.
2. Do the same in `callCreateAgent` (`self_tools_transport.go:579-601`) for the write at line 595 — creating/overwriting an agent that collides with a non-`Managed`-class profile should be rejected the same way.
3. Add a test covering both: attempting to update/create against each non-editable class (`Internal`/`Plugin`/`External`) is rejected; a `Managed`-class profile still updates/creates successfully.

## Done means

- Both self-tools reject writes against non-editable agent profiles with a clear error, verified by a real test exercising all four `ManageClass` values.
- Editable (`Managed`-class) agent writes are unaffected — verified by the same test.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Added an `AgentClassifier` interface + `classifyAgent` helper to `internal/mcp/self_tools_transport.go`, wired via `selfTools.AgentClassifier = container.AgentConfig` in `cmd/nanite/main.go` — the same `AgentConfigService` instance the REST layer (`handleUpdateAgent`/`requireMutableAgent`) already uses, so both surfaces now share one source of truth for `ManageClass.Editable()`. Both `callUpdateAgent` and `callCreateAgent` now reject writes against non-editable (`Internal`/`Plugin`/`External`) agent profiles with a clear tool-result error before reaching the store write; `Managed`-class writes are unaffected. Fails closed to a non-editable zero-value classification if `AgentClassifier` is ever left unwired. Added `internal/mcp/self_tools_agent_editable_test.go` covering all four `ManageClass` values for both tools. `go build`/`go vet`/`go test ./...` pass. No escalations.

Committed as `67a1e1bf` ("Phase 0 #34: gate agent_update/agent_create self-tools behind ManageClass.Editable()").

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
