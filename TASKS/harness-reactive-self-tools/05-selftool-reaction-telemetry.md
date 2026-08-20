# Self-tool reaction telemetry — `event_log`, `category = "selftool_reaction"`

**Phase:** 2 — Telemetry, consumer cleanup, worked example (`TASKS/harness-reactive-self-tools`)
**Status:** implemented
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

Implemented by a worker session, dispatched in parallel with
`06-collapse-envelope-marker-consumers.md` (different worker/worktree,
zero file overlap, confirmed by the dispatching Orchestrator).

**Doc availability note (not a deviation, just a pointer for future
readers of this worktree):** `docs/engineering/architecture/
11-harness-reactive-self-tools.md` did not exist in this task's worktree
branch at start (it's still an untracked file in the main `nanite`
checkout, not yet committed/merged into this branch's history). Read it
directly from the main worktree's filesystem path instead — its
"Telemetry" section content matches this task's own Context section
verbatim, so no discrepancy, just a location note. Also: this file's own
`Status:` read `not-started`, not `in-progress` as the dispatch message
described — noted, not blocking, proceeded as instructed.

**What was built** — `internal/selftools/reactions/telemetry.go`:

- `CategorySelftoolReaction = "selftool_reaction"` — the new
  `event_log.category` value, exported as a constant (reflexes itself
  uses a bare `"reflex"` string literal inline in `EmitFirings`, not a
  constant — exported this one anyway since Done-means explicitly
  requires the two streams stay independently queryable, and a shared
  constant is safer for future callers/tests than repeating the string).
- `TraceStore` interface — exactly one method,
  `LogEvent(sessionID, eventType, category, detail, metadata string)`,
  matching `*store.Store`'s real method (`internal/store/events.go:17`)
  byte-for-byte (confirmed by reading `internal/store/events.go`
  directly, not assumed — it does **not** return an error, matching
  reflexes' own `TraceStore.LogEvent` signature exactly). Deliberately
  omits reflexes' `BumpAgentReflexFired` — no self-tool-reaction
  equivalent exists, per the task's own instruction not to add an unused
  method.
- `traceRecord` (unexported) — `tool_name`, `reaction_id`, `kind`,
  `category` (from `FiredReaction.Category` — "render"/"execute"),
  `config` (embedded as a nested `json.RawMessage`, not a
  double-escaped string, since `FiredReaction.Config` is already raw
  JSON text "as stored" per `result.go`'s own doc comment — guarded so
  an empty `Config` string doesn't produce an invalid empty
  `RawMessage`), `tool_call_id` (the caller-supplied context), `outcome`
  (`FiredReaction.Outcome` copied verbatim — `"success"`/`"error"`/
  `"skipped_not_implemented"`), `error` (omitempty).
- `EmitReactionTrace(ctx context.Context, ts TraceStore, toolCallID string, result Result) error`
  — the exact signature the task file proposed. Writes one `event_log`
  row per entry in `result.Reactions` unconditionally (no filtering by
  outcome) — `event_type = fr.Kind`, `category =
  CategorySelftoolReaction`, `detail = result.ToolName`, `metadata =`
  the marshaled `traceRecord`. No-op (nil error, nothing written) when
  `result.Reactions` is empty (the normal case for the ~70 non-reactive
  self-tools). Returns a non-nil error only when `ts` is nil (a
  caller-programming error) — an individual reaction's own outcome is
  always recorded in its own row rather than aborting the loop; a
  per-reaction JSON-marshal failure (defensive-only path — every
  `traceRecord` field is a plain string plus a raw-JSON passthrough)
  logs a warning and falls back to an empty `"{}"` metadata blob for
  that one row rather than dropping it or aborting siblings, mirroring
  `EmitFirings`' own fallback behavior.

**Deviation from the task's literal `EmitReactionTrace` signature: none
in the signature itself** — built exactly
`EmitReactionTrace(ctx, ts TraceStore, toolCallID string, result Result) error`
as proposed. One design call made explicit here since the task file
didn't spell it out: `session_id` is **not** threaded into the emitted
`event_log` row (passed as `""` to `LogEvent`). Checked directly against
the architecture doc's own "Telemetry" field-mapping list before making
this call — that list enumerates `event_type`/`category`/`detail`/
`metadata` (tool name, reaction kind, config, tool_call_id, success/
error) and does **not** include `session_id`, unlike reflexes'
`EmitFirings` (which explicitly threads `state.SessionID` both into the
`event_log` column and into its own trace record). This isn't an
oversight carried forward blindly — `reactions.Fire` itself is already
fully session-agnostic by design (`Fire(ctx, toolName, payload)` takes
no session parameter at all; `Result` carries none either), so a
telemetry wrapper built "with nothing beyond `Result` plus a
`tool_call_id` string" (the task's own item 1 wording) has no session id
available to thread through without widening either `Fire`'s signature
(explicitly out of scope) or `EmitReactionTrace`'s own signature beyond
what the task specified. `tool_call_id` is the documented correlation
key for this stream; `session_id` staying blank does not affect either
Done-means regression test (both query by `category`, not `session_id`).
Flagging this for the Orchestrator/Reviewer in case `07`'s worked-example
wiring surfaces a real need for session correlation later — at that
point the caller (the self-tool handler, which already has
`mcp.SessionIDFromContext(ctx)` available) can pass it through the
`metadata`/`config` path without a signature change here, or this file
can grow a small caller-local context struct then.

**Import-cycle / dependency-surface check performed, not assumed:**
verified via `go list -deps` that `internal/mcp` does not depend on
`internal/selftools` (either the top package or `reactions`), so
importing `internal/mcp` here for `SessionIDFromContext` would not have
cycled — but deliberately did **not** add that import anyway, to keep
`internal/selftools/reactions`' dependency surface exactly what it was
before this task (`internal/store` plus stdlib only), matching
`engine.go`'s own "narrow the dependency surface" precedent and
consistent with the `session_id`-omission call above.

**Regression tests** (`internal/selftools/reactions/telemetry_test.go`),
both feeding a synthetic `reactions.Result` directly into
`EmitReactionTrace` per the task's own item 4 allowance:

- `TestEmitReactionTrace_SuccessAndSkip_TwoRowsDistinguishedByOutcome` —
  a `Result` with two `FiredReaction` entries (one
  `Outcome: OutcomeSuccess` / `internal_api_call`, one
  `Outcome: OutcomeSkippedNotImplemented` / `external_api_call`)
  produces exactly two `event_log` rows via
  `store.ListEvents(CategorySelftoolReaction, 50)`, each correctly
  keyed by `reaction_id` and distinguished by `metadata.outcome`, both
  carrying `category = "selftool_reaction"`, `detail = "task_update_report"`,
  and `metadata.tool_call_id = "tool-call-abc"`.
- `TestEmitReactionTrace_ReflexAndSelftoolReaction_IndependentlyQueryable`
  — synthesizes one `"reflex"`-category row directly via
  `(*store.Store).LogEvent` (not by importing
  `internal/agent/reflexes`, which would widen this package's
  dependency surface for a test-only need and reopen exactly the
  cross-package coupling this batch's own scope fence exists to avoid)
  alongside one real `EmitReactionTrace`-written `"selftool_reaction"`
  row, then confirms `ListEvents("reflex", 50)` returns only the reflex
  row, `ListEvents(CategorySelftoolReaction, 50)` returns only the
  reaction row, neither list contains a row from the other's category,
  and an unfiltered `ListEvents("", 50)` sees both (2 total) —
  confirming `ListEvents`' existing category filter, not new code, is
  what keeps the streams apart.

**Build/vet/test results** (run from this worktree):
- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — passes except the pre-existing, unrelated
  `internal/service/container.go` `stopReaper`/`stopRuntimeReaper`
  possible-context-leak findings named in this task's own dispatch
  message as expected/ignorable; confirmed no new vet findings anywhere
  under `internal/selftools/...`.
- `go test ./...` — all packages pass, including
  `internal/selftools/reactions` (both new tests, verified individually
  with `-run TestEmitReactionTrace -v`: both `PASS`) and the full
  existing `internal/selftools`/`internal/selftools/reactions` suites
  (`03`'s and `04`'s regression tests unaffected).

**Independently re-verified by the Orchestrator before merge**: `go build ./...`
and `go vet ./...` re-run directly in this worktree (same pre-existing,
unrelated `container.go` finding, nothing new), full `go test ./...`
re-run clean, and the two new test functions confirmed present with
the exact names claimed above.

No changes were made to `internal/store/` — `LogEvent`'s existing
signature was directly usable as-is for the narrow `TraceStore`
interface, exactly as the task file's own "Touches" line anticipated as
the likely outcome.

Nothing wired into `07-worked-example-task-update-report.md` — that
task depends on this one (`05`) per `TASKS/harness-reactive-self-tools/
README.md`'s own task table, not the reverse; wiring `EmitReactionTrace`
into the `task_update_report` handler is `07`'s own scope, not this
task's. This task's item 4 ("wire this into 07's handler") is read as
forward-looking context for `07`'s own future worker, not a
this-task-must-touch-07 instruction — reinforced by the task's own very
next sentence ("this task itself doesn't need a full end-to-end
self-tool to test against").

## Review notes

<!-- Reviewer fills in. -->
