# Self-tool reaction telemetry — `event_log`, `category = "selftool_reaction"`

**Phase:** 2 — Telemetry, consumer cleanup, worked example (`TASKS/harness-reactive-self-tools`)
**Status:** not-started
**Depends on:** `03-reaction-engine-core.md` (needs `Fire`'s per-reaction `Result` shape to wrap).
**Touches:** new file `internal/selftools/reactions/telemetry.go` (a parallel, thin wrapper — not an extension of `internal/agent/reflexes/telemetry.go`), `internal/store/` (only if `LogEvent` needs a narrow interface excerpt — likely already usable as-is, see below).

## Context

`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "Telemetry" section is the design, already checked directly against the reflexes precedent by the design session rather than assumed:

- **Sink: `event_log`, reused as-is — no schema change.** Confirmed schema (`internal/store/migrations/001_schema.sql:473`): `session_id`, `event_type` TEXT, `category` TEXT, `detail` TEXT, `metadata` TEXT (JSON, default `'{}'`) — already fully polymorphic.
- **Full coverage required** — every fired reaction (both executed kinds; a defensively-skipped `implemented=false` kind should also produce a record, distinguishably marked, not silently vanish) gets a trace record, mirroring `internal/agent/reflexes/telemetry.go`'s `EmitFirings` (`internal/agent/reflexes/telemetry.go:1-60`, read in full before starting — same shape, different package, deliberately not sharing code with it: this design's own scope fence, from `TASKS/reflex-taxonomy/07-harness-reactive-self-tools-design-session.md` decision 7, is "a new, parallel thin wrapper in `internal/selftools/reactions`, not an extension of `internal/agent/reflexes/telemetry.go`" — the taxonomy doc's own mechanisms stay untouched by this batch).
- **Field mapping**:
  - `event_type` = the reaction kind slug (`render_card`, `internal_api_call`, …)
  - `category` = `"selftool_reaction"` — new value, distinct from reflexes' existing `category = "reflex"`, so the two telemetry streams stay separately queryable via the existing `ListEvents(category, limit)` path (confirm that method's exact name/signature by reading `internal/agent/reflexes/telemetry.go`'s own usage of it, or its `store` counterpart, before assuming the name below is exact).
  - `detail` = the tool name
  - `metadata` = a trace record: tool name, reaction kind, the config used, the triggering `tool_call_id` (for correlating back to the exact `tool_use` block that caused it — thread this through from wherever the self-tool handler already has it, the same way reflexes' own trace records thread caller-local context per `internal/agent/reflexes/telemetry.go`'s `FiringContext`), success/error.

## What to do

1. Design `internal/selftools/reactions.Result` (built by `03`) with enough per-reaction detail that this task's wrapper doesn't need anything beyond it plus a `tool_call_id` string passed in by the caller — mirror `internal/agent/reflexes/telemetry.go`'s `FiringContext` pattern (caller-local context that isn't part of the engine's own decision logic) rather than widening `Fire()`'s own signature.
2. Build `EmitReactionTrace(ctx, store TraceStore, toolCallID string, result reactions.Result) error` (or equivalent) in `internal/selftools/reactions/telemetry.go` — one `event_log` row per attempted reaction (executed *and* defensively-skipped-`implemented=false` cases both get a row; distinguish them via `metadata`, e.g. an `"outcome": "skipped_not_implemented"` field vs. `"outcome": "success"`/`"outcome": "error"`).
3. Define a narrow `TraceStore` interface (matching `internal/agent/reflexes/telemetry.go:56-60`'s pattern) — at minimum whatever `LogEvent`-shaped method `*store.Store` already exposes; confirm its exact signature by reading `internal/store/` rather than assuming it matches reflexes' `TraceStore` byte-for-byte (self-tool reactions don't need the `fired_count`/`last_fired_at` bump reflexes' `TraceStore` also carries — that's reflex-specific, agent_reflexes-scoped state with no self-tool-reaction equivalent; don't add an unused method to this interface).
4. Wire this into `07-worked-example-task-update-report.md`'s handler as the concrete proof this actually fires — this task itself doesn't need a full end-to-end self-tool to test against; a synthetic `reactions.Result` fed directly into `EmitReactionTrace` in a regression test is sufficient here.

## Done means

- `EmitReactionTrace` (or equivalent) exists, writes exactly one `event_log` row per attempted reaction (regardless of outcome), with the field mapping above.
- A regression test proves: firing two reactions together (one success, one `implemented=false`-skip) produces exactly two `event_log` rows, correctly distinguished by `metadata`'s outcome field, both at `category = "selftool_reaction"`.
- A regression test proves the two telemetry streams (`category = "reflex"` vs. `category = "selftool_reaction"`) are independently queryable via the existing category-filtered event-log read path, with no cross-contamination.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
