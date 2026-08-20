# Worked example — `task_update_report(id, msg)`

**Phase:** 2 — Telemetry, consumer cleanup, worked example (`TASKS/harness-reactive-self-tools`)
**Status:** implemented
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

Implemented by a worker session, in the `07`-dispatch worktree. Ground
truth confirmed at dispatch time: tasks `01`-`05` on `main`, Phase 1
independently reviewed clean; `docs/engineering/architecture/
11-harness-reactive-self-tools.md` was present on the operator's main
checkout but not yet committed to git at all (confirmed via `git log
--all` — zero history for that path) — read directly off disk via the
Read tool rather than through git, since git operations in this
worktree can't reach an uncommitted file living in a sibling checkout.
Noted here as a documentation-pipeline gap for the Orchestrator's
attention (the doc should get committed so future worktrees/clones can
see it via git, not just this one operator checkout), not something
this task needed to fix itself.

**1. Tool definition + registration.** Added `internal/selftools/
self_tools_task_update_report.go`: `taskUpdateReportToolDefinition()`
(id/msg both required strings, description in the existing When-to-use
/ When-NOT-to-use / Required-context / Output-shape house style read
off several existing tool descriptions in `self_tools.go` first).
Registered in `selfToolDefinitions()` (`self_tools.go`) and in
`CallTool`'s switch (`self_tools_transport.go`), the same way every
other self-tool is. Added a golden example (`internal/selftools/
examples/task_update_report.json`) after `go test ./...` caught
`TestNaniteToolDescribe_AllSelfToolsHaveExamples` failing for the new
tool — not called out in the task's own "What to do," but required by
an existing, already-passing repo invariant every self-tool must
satisfy; fixed as part of the baseline-check discipline rather than
treated as a scope question.

**2. Handler.** `callTaskUpdateReport` validates `id`/`msg` (both
required), calls `st.Reactions.Fire(ctx, "task_update_report", payload)`
(nil-safe — a bare `SelfToolsTransport` with no `Reactions` wired still
returns a clean confirmation, matching every other optional field on the
struct), embeds the render_card marker via `EmbedRenderCardMarker` when
`Result.RenderCardPayload()` resolves, calls `reactions.
EmitReactionTrace` for every attempted reaction, and returns a short
confirmation string. Added a new `Reactions *reactions.Engine` field to
`SelfToolsTransport` (nil-safe, doc comment explains) since nothing in
the codebase wired a `*reactions.Engine` anywhere outside tests before
this task — `03`/`04`/`05` built the engine and telemetry as
library code with no live consumer yet; `07` is genuinely the first.
Wired `selfTools.Reactions = reactions.NewEngine(s, nil, slog.Default())`
in `cmd/nanite/main.go`, alongside the other post-construction
`SelfToolsTransport` field wiring.

`EmitReactionTrace`'s `toolCallID` parameter is passed `""` — no
per-call `tool_use_id` is threaded into `ctx` at the self-tool-handler
layer today (`internal/mcp/tool_ctx.go` only stamps the turn-level
aggregate set via `WithTurnToolUseIDs`, not a single current-call ID);
wiring a real per-call ID through would mean adding a new `ctx` key and
touching the same three call sites the architecture doc's own "three
independent consumers" ENVELOPE_DATA discussion names
(`chat_tool_executor.go`, `tools_call.go`, `mcpserver/handlers.go`) —
real, non-trivial scope this task doesn't ask for. Left as a documented,
deferred gap, the same posture `05`'s own Work Log already took for
`EmitReactionTrace`'s hardcoded `session_id=""`. Did not modify `05`'s
`EmitReactionTrace` signature (already implemented/reviewed/merged) to
add a `sessionID` parameter either, for the same reason — out of this
task's scope, and `mcp.SessionIDFromContext(ctx)` is available to a
future task that wants to close that gap without a handler-shape change.

**3. Seed mechanism: Go-side seed function, not a migration-time
INSERT.** `SeedTaskUpdateReportReactions(ctx, st, apiBaseURL, logger)`
in the same new file, mirroring `internal/agent/reflexes/seeds.go`'s
`SeedBaseReflexes` pattern: idempotent via a new `store.
CountSelftoolReactionsByToolAndKind(ctx, toolName, reactionKindID)`
helper (counts by tool_name+reaction_kind_id regardless of enabled
state, so an operator's own disable survives re-boot instead of being
silently re-inserted — proven by
`TestSeedTaskUpdateReportReactions_RespectsOperatorDisable`). The
reasoning for Go-side over migration-time, discovered while implementing
step 4: `internal/selftools/reactions/internal_api_call.go`'s own doc
comment (already landed by `03`) states the `internal_api_call`
reaction's `config.endpoint` must be a fully-qualified, same-process URL
(`http://127.0.0.1:<port>/...`) and explicitly assigns resolving it into
a full URL to "the seeding caller's responsibility" — not the executor's.
A migration file runs at DB-open time, before this process's own listen
port (`cmd/nanite/main.go`'s `apiBaseURL`, built from the `-port` flag)
is known, so a migration-time INSERT structurally cannot produce a
correct endpoint. A Go-side seed called from `main()` right after
`apiBaseURL` is resolved is the only point where both the seed shape and
the real listen address are simultaneously available — called via
`selftools.SeedTaskUpdateReportReactions(context.Background(), s,
apiBaseURL, slog.Default())`, right after `selfTools.Reactions` is
wired, before the rest of the `selfTools.*` post-construction block.
This also settles the design doc's own illustrative `config.endpoint`
value (`/api/example/task-updates`, a bare path) into what the code
actually needs (a fully-qualified URL) — a correction to the design
doc's illustrative precision level, not a re-litigation of its decision.

**4. Example endpoint: built a real minimal route, not
httptest-only.** Added `POST /api/example/task-updates`
(`internal/api/example_task_updates.go`, registered in `api.go`'s
`RegisterRoutes`): decodes `{id, msg}`, logs at debug level (so a live
dogfeed can confirm the substituted values arrived), returns `200
{"received": true}`. Persists nothing. Also exempted the path from
`basicAuthMiddleware` (`internal/server/auth.go`) the same way
`/api/tools/call` already is — the reaction engine's HTTP client sends
no credentials, so an environment with `NANITE_AUTH_USER`/`PASSWORD`
configured would otherwise 401 the seeded reaction every time; a real
route without this exemption would flake in exactly the deployment
shape the codebase already has a precedent for handling. Chose a real
route over the task's offered httptest-only alternative because the
Done-means item 5 live dogfeed needs an actual reachable endpoint in a
*running* boot instance — an `httptest.NewServer` only exists inside a
`go test` process, so it can't be what a live server's seeded
`config.endpoint` points at. The regression test (item below) still
uses `httptest.NewServer` for its own internal_api_call proof, per the
task's own explicit allowance and matching `03`'s own test approach
(`internal_api_call_test.go`) — the real route and the live dogfeed
exercise the actual deployed shape; the httptest server keeps the unit
test isolated and fast. Both together, not either alone.

**5. Regression test — full chain, not pieces in isolation.** `internal/
selftools/self_tools_task_update_report_test.go`:
`TestCallTaskUpdateReport_FullChain_RenderCardAndInternalAPICall` drives
`st.CallTool(ctx, "task_update_report", args)` end to end against a real
`*reactions.Engine` and a real `*store.Store`, with the internal_api_call
reaction's `config.endpoint` pointed at an `httptest.NewServer` — asserts
the embedded marker decodes to the correct `info-card` envelope with
`body` substituted from `msg`, the httptest server received the real
HTTP POST with `id`/`msg` substituted into the body, and exactly two
`event_log` rows land at `category="selftool_reaction"` distinguishing
the two kinds by `event_type`. Four supporting tests round out the
handler/seed-function edges: required-field validation, the nil-`Reactions`
fallback, the zero-configured-reactions normal case (every other
self-tool today), and the seed function's own idempotency + operator-disable
respect.

**6. Live dogfeed.** Built the binary to an absolute scratch path
(`/private/tmp/.../scratchpad/task07-dogfeed/nanite`, never `./nanite`
in the repo root — the CWD-relative footgun `EXECUTION-PROCESS.md`
flags), booted it with `-db <absolute-scratch-path>/scratch.db -port
8099 -dev` (an explicit absolute `-db` override, not a relative path
resolved against CWD), confirmed via boot log `"seeded task_update_report
reactions" count=2`, then called `task_update_report` for real through
the actual MCP surface — `POST /api/tools/call` (the same loopback
self-tools proxy a CLI-launched agent's `nanite mcp` subprocess uses,
not a direct Go function call) — with `id="live-dogfeed-1"`,
`msg="Live dogfeed check: harness-reactive self-tools worked example"`.
Result: the returned tool text carried the confirmation string with
`id`/`msg` correctly substituted, plus a correctly-resolved
`<!--ENVELOPE_DATA:...-->` marker (`type=info-card`,
`data.title="Task update"`, `data.body` = the exact `msg` text). Server
log showed a real `POST /api/example/task-updates` request landing.
Queried the scratch DB directly (`sqlite3`): exactly two `event_log`
rows at `category='selftool_reaction'`, one `event_type=render_card` and
one `event_type=internal_api_call`, both `outcome=success`, both
`detail=task_update_report`. Restarted the same scratch instance a
second time against the same DB to confirm the seed's idempotency live
(not just in the unit test): boot log showed no
`"seeded task_update_report reactions"` line at all on the second boot
(the `n > 0` guard suppresses it, same as `SeedBaseReflexes`'s own
pattern) and `selftool_reactions` still held exactly 2 rows for
`task_update_report`. Ran `git status --short` immediately after the
dogfeed per `EXECUTION-PROCESS.md`'s own instruction — clean, only the
intended source-file changes; no stray write to any tracked file. Tore
down the scratch server and deleted the entire scratch directory
afterward.

**7. Baseline checks.** `go build ./cmd/nanite/`, `go build ./...`, `go
vet ./...` (same single pre-existing, unrelated `internal/service/
container.go` `stopReaper`/`stopRuntimeReaper` finding noted in this
task's own brief — confirmed unchanged by this task, ignored per that
note), and `go test ./...` all pass. Full suite run twice: once before
adding the golden-example fixture (one real failure,
`TestNaniteToolDescribe_AllSelfToolsHaveExamples`, fixed per item 1
above) and once clean after.

**Deviations from the plan, summarized:** (a) added a golden-example
JSON fixture the task file didn't explicitly call out, required by an
existing repo-wide test invariant; (b) added a `Reactions` field to
`SelfToolsTransport` and its `main.go` wiring, since no prior task wired
`reactions.Engine` into any live consumer — implied by "write the thin
handler" but not spelled out as its own line item; (c) exempted the new
example route from `basicAuthMiddleware`, beyond the task's literal
"accepts the POST and returns 200" ask, to keep the seeded reaction
correct in an authed deployment, not just local dev. None of these
change what the task asked for; all three are load-bearing for the
handler to actually work end to end, not scope expansion into unrelated
territory.

**Flagged for the Orchestrator:** `docs/engineering/architecture/
11-harness-reactive-self-tools.md` exists on disk (the operator's main
checkout) but has never been committed to git in any branch this
worktree can see (`git log --all` returns nothing for that path) — the
same is true of `docs/engineering/architecture/12-scheduling.md` and a
few sibling files visible in the original session's `git status`
context. Every task file in this batch (and `TASKS/INDEX.md`) cites
`11-harness-reactive-self-tools.md` by path as load-bearing context: a
future worker dispatched into a fresh worktree that hasn't inherited the
operator's uncommitted main-checkout state won't be able to read it via
git at all, only by knowing to fall back to a direct filesystem Read
against the sibling checkout path — which is what this task's own
Context-gathering step had to do. Recommend committing the doc (and its
siblings) to `main` so it's reachable the normal way for the next
worktree/task.

**Addendum (2026-08-20) — post-review fix: loopback gate added to the handler.**
Orchestrator review flagged that the `basicAuthMiddleware` exemption comment
in `internal/server/auth.go` for `/api/example/task-updates` claimed parity
with `/api/tools/call`'s exemption immediately above it ("exactly the
reasoning /api/tools/call above already documents"), but that claim was
false as landed: `/api/tools/call`'s actual safety comes from a real
`isLoopbackRequest(r)` check inside `handleSelfToolCall`
(`internal/api/tools_call.go`), and `handleExampleTaskUpdate`
(`internal/api/example_task_updates.go`) had no equivalent check — it was
auth-exempt with genuinely zero gating, not "the loopback gate instead of
basic auth" like the comment implied. Low impact as landed (the handler
only logs and returns `{"received": true}`, no store write, no other side
effect), but a real gap between what the code claimed and what it did.

Fixed by adding the same `isLoopbackRequest(r)` check `handleSelfToolCall`
already has to the top of `handleExampleTaskUpdate`, returning 403 with a
short message for a non-loopback caller — making the `auth.go` comment's
parity claim actually true rather than aspirational. Added
`internal/api/example_task_updates_test.go` (new file, modeled on
`tools_call_test.go`'s `TestHandleSelfToolCall_RejectsNonLoopback` and
reusing its `newToolCallTestAPI` helper) with two tests:
`TestHandleExampleTaskUpdate_RejectsNonLoopback` (non-loopback caller gets
403) and `TestHandleExampleTaskUpdate_LoopbackSucceeds` (loopback caller
still gets 200 + `{"received": true}`). `auth.go`'s own comment was left
untouched — it already describes the intended trust model, which the fix
now makes true; no other files were touched. `go build ./cmd/nanite/`,
`go vet ./...` (same single pre-existing, unrelated `container.go`
finding), and `go test ./...` all pass, including this task's own existing
suite (`internal/selftools/self_tools_task_update_report_test.go`) and the
two new regression tests.

**Independently re-verified by the Orchestrator before merge:** re-read
the auth.go/example_task_updates.go/main.go/self_tools.go/
self_tools_transport.go/selftool_reactions.go diffs directly, confirmed
the loopback-gate fix matches `handleSelfToolCall`'s exact pattern, and
re-ran `go build ./...`, `go vet ./...` (same pre-existing finding only),
and the full `go test ./...` suite plus the two new
`TestHandleExampleTaskUpdate_*` tests individually — all clean.

## Review notes

<!-- Reviewer fills in. -->
