# SP1 — nanite_plan_step_add — implementer report

## Decision-rules pass

Walking the eight rules from `docs/architecture/agent-context-architecture.md`
§"Decision rules for new work" against this design:

1. **Where does this constraint actually need to be enforced?** — GREEN.
   The append-step capability is a tool-surface (Bucket 2) addition, not a
   prompt rule. Enforcement lives at the handler boundary in
   `store.AppendPlanSteps` (collision detection, ID validation,
   plan-not-found surfacing).
2. **Is this preemptive or reactive?** — GREEN. The tool is a capability the
   agent reaches for when it needs to append; it is not a gate that fires
   before any other tool. No "must call X before Y" anywhere.
3. **Is another layer already doing this?** — GREEN. Pre-SP1 the agent had
   no append path — `nanite_plan_update` mutates existing fields,
   `nanite_plan_create` builds from scratch. The c120 workaround was
   delete + recreate. No redundant enforcement layer is being added.
4. **Could this go in a tool description instead of the system prompt?** —
   GREEN. The full contract (when/when-not, params, golden example,
   cross-references) lives in the tool description. Zero `default.md`
   change.
5. **Could this be a runtime classifier injection?** — N/A. This is a
   capability surface, not per-task framing.
6. **Does this take handcuffs OFF the agent or PUT them ON?** — GREEN. OFF.
   The agent that wanted to add a step in c120 now has a direct path; it
   no longer has to delete-and-recreate (which destroys step history).
7. **c117 test — would this rule cause an agent to dodge a "let's add a
   step" request?** — GREEN. Opposite of dodge: it gives the agent the
   exact verb the user asked for.
8. **Measure prompt density.** — GREEN. `default.md` word/bullet/negative
   counts unchanged — verified via `git diff` (zero touches).

All eight green. No escalations.

## Design choice

**Option A — new `nanite_plan_step_add` tool.** Picked over Option B
(extending `nanite_plan_update` with `append_steps`) because:

- Per Bucket 2 / lens principle 2: tool descriptions should be elevator
  pitches with one job. Mixing "advance an existing step" with "append a
  new step" inside one tool forces a longer, more conditional description
  and makes the schema harder to validate. The c117 incident showed what
  happens when one tool's description carries too many shapes — the agent
  reads the prompt as a constraint document.
- Explicit verb is easier to discover via `nanite_tool_describe`
  (Levenshtein matches against `add` are clearer than spotting an
  optional `append_steps` array buried in `plan_update`'s schema).
- Does not regress callers of `plan_update` — they keep the exact
  schema they had.

`nanite_plan_update`'s description was minimally updated to add a
"When NOT to use" line pointing at `nanite_plan_step_add`. No other
behavioral change to the existing tool.

## Files changed

- `internal/store/plans.go` — added `AppendPlanSteps(planID, []PlanStep) ([]PlanStep, error)` plus a private `nextStepID` helper. Validates collisions, auto-assigns `s<n>` (or UUID fallback when no numeric scheme is in use), defaults missing status to `pending`, errors on missing title.
- `internal/mcp/self_tools_transport.go` — added `AppendPlanSteps` to the `TodoStoreInterface` (so `*store.Store` satisfies it via the new method), added the `nanite_plan_step_add` dispatch case, and added the `callPlanStepAdd` handler which accepts either a JSON-string `steps` arg (matching the `plan_create` convention) or a real array.
- `internal/mcp/self_tools.go` — registered `nanite_plan_step_add` definition with full Bucket-2 description (elevator pitch + when-to-use / when-not / required context / behavior / output shape / golden example / cross-references). Updated `nanite_plan_update`'s description with one line steering callers who want to append toward the new tool.
- `internal/mcp/self_tools_describe.go` — added `nanite_plan_step_add` to the curated `describeRelations` map and threaded back-references from `plan_create` / `plan_update` / `plan_get` so `nanite_tool_describe` can route the agent there.
- `internal/mcp/examples/nanite_plan_step_add.json` — single golden example per Phase 5/A1 convention (one realistic append call with id auto-assignment).
- `internal/dispatch/role_test.go` — locks the chat-surface guarantee that `nanite_plan_step_add` is reachable via the existing `nanite_plan_` prefix without widening `ChatToolSurface`.
- `internal/store/plans_test.go` — five store-level tests covering happy path append, ID collision rejection, plan-not-found, missing-title rejection, and UUID-fallback ID assignment for non-numeric schemes.
- `internal/mcp/self_tools_test.go` — three MCP-level tests: happy-path tool call (existing step preserved, two new steps appended with sequential IDs), plan_id-not-found surfaces a structured error, and a definition-presence + Bucket-2 description-section check.

## Tests added

- `TestAppendPlanSteps_AutoAssignsSequentialIDs` — happy path: append two steps to a plan with `s1..s3`, verify they land as `s4` and `s5` and that all five steps survive untouched.
- `TestAppendPlanSteps_RejectsCollidingID` — append a step with `id="s1"` against a plan that already has `s1`; assert the error and that the plan still has exactly one step (no partial write).
- `TestAppendPlanSteps_PlanNotFound` — `AppendPlanSteps("does-not-exist", ...)` returns an error.
- `TestAppendPlanSteps_RequiresTitle` — appending a step with empty title fails closed (no fabrication).
- `TestAppendPlanSteps_FallbackUUIDWhenNoNumericIDs` — when the existing scheme is non-numeric (e.g. `design-step`), the auto-assigned ID is a UUID-shaped fallback rather than a guessed numeric.
- `TestSelfToolsTransport_PlanStepAdd_HappyPath` — calls the MCP tool through `CallTool`, verifies the textual confirmation, the JSON `appended_count`, and the persisted plan state.
- `TestSelfToolsTransport_PlanStepAdd_PlanIDNotFound` — verifies the structured error path.
- `TestSelfToolDefinitions_PlanStepAddPresent` — locks Bucket-2 description sections (When to use / When NOT to use / Output shape / Cross-references).
- `TestIsChatSurfaceTool_PlanStepAddAllowed` — locks the chat-surface coverage so a future widening would be intentional.

## Smoke verification (worktree-local)

```
$ go build ./cmd/nanite/
✓ go build: ok

$ go vet ./...
✓ go vet: ok

$ go test ./...
ok  	github.com/hollis-labs/nanite/cmd/nanite	0.269s
ok  	github.com/hollis-labs/nanite/internal/agent	1.004s
... (all packages pass) ...
ok  	github.com/hollis-labs/nanite/internal/store	16.723s
ok  	github.com/hollis-labs/nanite/internal/mcp	23.707s
ok  	github.com/hollis-labs/nanite/internal/dispatch	0.424s
ok  	github.com/hollis-labs/nanite/pkg/provider	1.335s
```

All packages green. No skips. Both the targeted run
(`./internal/store/... ./internal/mcp/... ./internal/dispatch/...`) and
the full `./...` run pass.

## Commit SHAs

```
$ git log --oneline fix/c112-regression-cluster..HEAD
ca595ad docs(sp1): implementer report for nanite_plan_step_add (CW-20260430-0001)
f223aac feat(plans): nanite_plan_step_add — append steps without delete+recreate (CW-20260430-0001)
```

Two commits — the code change (`f223aac`) and this report (`ca595ad`).
Both are on top of the `fix/c112-regression-cluster` tip; the
orchestrator merges from this worktree.

## Deviations from ticket

- The ticket suggested the new method on `*store.Store` could be named
  `AppendPlanSteps` *or* `AddPlanStep`. I picked `AppendPlanSteps` (plural,
  batch-aware) so the agent can add multiple steps in one call without
  N round-trips, and so the deduplication / collision check has a single
  failure point. The MCP tool accepts an array regardless.
- The store method returns `([]PlanStep, error)` rather than just `error`,
  so the MCP handler can echo back the resolved step IDs (including
  auto-assigned ones) without a second `GetPlan`. The agent can then feed
  those IDs into `nanite_plan_update(step_id=...)` immediately.
- `nanite_plan_update`'s description gained a single "When NOT to use" line
  pointing at the new tool. No prior text was removed because the existing
  description did not contain a "cannot append steps" claim — it simply
  did not mention appending.

## Open questions for orchestrator

None — no escalations. `default.md` was not touched (verified via
`git diff --stat`). The new tool is a Bucket-2 capability addition only.

One advisory note for SP6 awareness: SP6 also touches
`internal/mcp/self_tools.go` and `self_tools_transport.go`. This patch's
hunks are localized to the `nanite_plan_*` block (around lines 387-450 in
self_tools.go and the `case "nanite_plan_update":` switch arm in
self_tools_transport.go) — should not collide with SP6's new-tool
territory unless SP6 also lands in the plans block, in which case a
trivial textual merge resolves it.
