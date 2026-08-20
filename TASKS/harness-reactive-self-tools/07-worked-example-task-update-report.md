# Worked example — `task_update_report(id, msg)`

**Phase:** 2 — Telemetry, consumer cleanup, worked example (`TASKS/harness-reactive-self-tools`)
**Status:** not-started
**Depends on:** `01-move-self-tools-to-internal-selftools.md` (needs `internal/selftools` to add the new tool to), `03-reaction-engine-core.md` (`Fire`), `04-render-card-construction.md` (the render_card marker helper), `05-selftool-reaction-telemetry.md` (`EmitReactionTrace`, to prove full coverage). `06-collapse-envelope-marker-consumers.md` is **not** required — this task's render_card path works against the three pre-`06` consumer implementations unchanged; land `06` before or after this task, whichever is convenient.
**Touches:** a new `internal/selftools/self_tools_task_update_report.go` (tool definition + handler, registered in `SelfToolsTransport.ListTools`/`CallTool`), a seed for the two `selftool_reactions` rows (a Go-side seed function, mirroring `internal/agent/reflexes/seeds.go`'s pattern for reflex seeds — or a migration-time `INSERT`, your call, document it), a minimal example internal endpoint for the `internal_api_call` reaction to target (see step 4 — kept illustrative, not a real consumer).

## Context

`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "Worked example: `task_update_report(id, msg)`" section is the design, in full — read it before starting. Kept illustrative rather than bound to a real consumer, per the design session's own decision on immediate-vs-deferred implementation: *"the point is proving the shape is buildable, not shipping a specific integration."* Do not wire this to Nanite's actual todo/plan store or any other real system — that would be new, unscoped work this task file doesn't ask for.

**Naming, already decided:** `task_update_report`, not the design doc's own original draft name `nanite_report_task_updated` — renamed during the design session to match `docs/tool-naming-convention.md`'s `<concept>_<verb>` convention every other self-tool already follows. None of the ~70+ registered self-tools carry a `nanite_` prefix except `nanite_code_execute` (`docs/tool-naming-convention.md:117`) — the `nanite_*` reservation is about keeping the namespace clear of MCP-origin collisions, not a naming requirement for every self-tool. Do not re-litigate this naming call.

**Definition** (per the design doc):
- `id` (string, required) — opaque identifier; the harness never interprets it, only threads it through to reaction configs.
- `msg` (string, required) — human-readable update text.
- Description follows the existing When-to-use / When-NOT-to-use / Required-context / Output-shape convention every current self-tool description uses — read a few existing tool descriptions in `internal/selftools` (post-`01`'s move) for the exact house style before writing this one.

**Handler shape, deliberately thin** — the concrete thing distinguishing this from a CRUD tool like `todo_update`: no independent persisted state of its own. It validates `id`/`msg`, calls `reactions.Fire(ctx, "task_update_report", payload)`, returns a short confirmation to the LLM (plus `04`'s render_card marker, if that reaction fired), and calls `05`'s `EmitReactionTrace` for full telemetry coverage. Every side effect worth having happens through the reaction layer, not the handler — that's the actual point of this worked example: proving a self-tool can be *purely* declarative.

**Two reaction rows, exercising both execution shapes** identified by `03`'s Context:
1. `render_card` — `config: {envelope_type: "info-card", template: {title: "Task update", body: "{{msg}}"}}`. Exercises the "engine resolves, caller surfaces" path.
2. `internal_api_call` — `config: {endpoint: "/api/example/task-updates", method: "POST", body_template: {id: "{{id}}", msg: "{{msg}}"}}`. Exercises the "engine executes directly" path.

Both fire independently off one `Fire()` call — proving the "both" case the mechanism's own definition promises (a card, an internal API call, both, neither), not just a single kind.

## What to do

1. Define the `task_update_report` tool in `internal/selftools` (post-`01`, this is a normal addition to the same package/file-split pattern every other self-tool follows — do not create a special exception for it). Register it in `ListTools` and `CallTool` the same way every existing self-tool is registered.
2. Write the thin handler per the "Handler shape" section above: validate `id`/`msg` (both required, per the definition), call `reactions.Fire`, embed the render_card marker (via `04`'s helper) if that reaction resolved, call `EmitReactionTrace` (`05`) for every attempted reaction, return a short confirmation string to the LLM.
3. Seed the two `selftool_reactions` rows above (`tool_name = "task_update_report"`, one `render_card`, one `internal_api_call`, both `enabled = true`). Document your seed mechanism choice (Go-side seed function run at boot, matching `internal/agent/reflexes/seeds.go`'s pattern, vs. a migration-time `INSERT` — either is acceptable, pick one and document why, mirroring how `01-taxonomy-schema-foundation.md`'s own Work Log documented its `action_kind` string-vs-FK call).
4. Build a minimal example internal endpoint for the `internal_api_call` reaction's `config.endpoint` (`/api/example/task-updates`) to target — explicitly a test/demo fixture proving the mechanism executes a real HTTP round-trip, not a real feature. A trivial handler that accepts the POST and returns 200 is sufficient; do not persist anything real behind it. If you'd rather prove the `internal_api_call` path purely via `httptest.NewServer` inside a regression test (matching `03`'s own test approach) and skip standing up a real route in the running service, that's an acceptable alternative — document which you chose and why in this file's Work Log.
5. Confirm end-to-end, live (not just unit tests) — see Done means.

## Done means

- Calling `task_update_report(id, msg)` against a real running instance produces: (a) a render_card marker embedded in the tool's `ToolResult` text, resolving to an `info-card` envelope with `body` correctly substituted from `msg`; (b) a real HTTP call to the example internal endpoint (or the httptest-based equivalent, if you took that alternative) with `id`/`msg` correctly substituted into the body; (c) exactly two `event_log` rows at `category = "selftool_reaction"` (one per reaction), correctly distinguishing the two kinds.
- A regression test proves the full handler → `Fire` → both reactions → telemetry chain end-to-end, not just each piece in isolation (the pieces are already unit-tested by `03`/`04`/`05` individually — this task's own test is the integration proof).
- A live dogfeed (per `EXECUTION-PROCESS.md`'s validation-checkpoint convention): boot a scratch instance, call `task_update_report` for real through the actual MCP surface (not a direct Go function call), confirm the marker renders correctly and the `event_log` rows land, matching this project's own precedent of live-verifying registration/wiring changes rather than trusting unit tests alone (see `01`'s own Done means for why — package/registration wiring is exactly what unit tests are most likely to miss).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your seed-mechanism call (step 3) and your example-endpoint-vs-httptest call (step 4) are documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
