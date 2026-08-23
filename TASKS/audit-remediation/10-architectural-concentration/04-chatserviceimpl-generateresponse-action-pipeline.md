# `generateResponse` — implement the approved six-action pipeline

**Phase:** Audit remediation — Wave 5 reopened implementation beat
**Status:** in-progress
**Depends on:** reviewed evidence/characterization task `10/01`; AD-12 decided
**Gated on:** none — the operator expressly approved AD-12 on 2026-08-23
**Findings:** GO-SVCEXEC-001, GO-SVCEXEC-002
**requires_architect_decision:** false
**requires_security_review:** false
**requires_regression_test:** true
**Parallel-safe with:** `10/05`; this task takes an exclusive lock on `internal/service` while active
**Touches:** `internal/service/chat_generate.go`; new private action/state files under `internal/service/`; additive focused tests under `internal/service/`; `provider_stream_inactivity_test.go` only when the stream action moves; the reviewed responsibility map and this task file. Do not edit shared audit trackers; the orchestrator owns those.

## Operator decision and intent

The operator expressly approved AD-12 on 2026-08-23 and directed implementation
in Wave 5. The desired shape is the action/pipeline pattern used to transition
an order through an ecommerce system:

- `generateResponse` is the coordinator and owns sequencing and routing;
- each named action owns one step's logic and returns an explicit outcome;
- actions never invoke the next action or hide control flow in callbacks; and
- the provider/tool iteration remains a visible loop in `generateResponse`.

Implement the six reviewed boundaries from
`01-chatserviceimpl-responsibility-map.md`: prepare turn, initialize run,
request/govern provider iteration, consume provider iteration, settle tools,
and finalize/persist. Use private methods and explicit state/outcome structs
first. Do not create six service objects, rewrite the flow, or begin a wholesale
`chatServiceImpl` split.

At authorization time `generateResponse` spans
`internal/service/chat_generate.go:121–2036` with cognitive complexity 458,
cyclomatic complexity 225/228, and maintainability index 0. Recompute rather
than trusting shifted line numbers.

## Approved coordinator contract

After the work, a reader must still see this flow in `generateResponse`:

```text
trace/lifecycle setup
  -> prepare turn
  -> initialize run
  -> loop {
       request provider iteration
       consume provider iteration
       optionally settle tools and continue
     }
  -> finalize and persist
```

Only `generateResponse` interprets action outcomes and performs `continue`,
`break`, or terminal `return`. It retains root-span lifetime, channel close,
stream cleanup, active-presence cleanup/broadcast, context cancellation,
provider-attempt routing, final PTY disposition, and the existing defer order.

Use a small private directive/outcome vocabulary equivalent to:

```go
proceed | retryIteration | continueIteration | finishRun | terminate
```

Use explicit pointer-owned state shaped around the reviewed names
`generationLifecycle`, `turnSetup`, `runState`, `providerAttempt`, and
`providerTurn`/`turnResult`. Exact field placement may follow current source,
but do not copy a `strings.Builder` after first use and do not put durable
service dependencies into state values. Each action gets a typed result whose
directive is interpreted by the coordinator.

The target private action surface is:

- `prepareTurn(...)` — session/agent/model/provider/tool/context assembly;
- `initializeRun(...)` — stream start, pre-loop compaction, loop state;
- `requestProviderIteration(...)` — hard stops, budgets, hooks, telemetry,
  provider/CLI start, and pre-stream recovery;
- `consumeProviderIteration(...)` — inactivity timer, stream normalization,
  content/usage/tool events, mid-stream recovery;
- `settleToolTurn(...)` — compose the existing precheck/batch/postprocess tool
  pipeline and decide continuation; and
- `finalizeRun(...)` — envelopes/filters, structured persistence, usage and
  metrics, terminal events, title/tag scheduling.

## Required implementation order

Extract one phase per reviewed commit in this dependency-aware order:

1. **Prepare turn.** Move only the phase after root trace/defer setup. Establish
   the immutable `turnSetup` handoff.
2. **Initialize run.** Establish `runState`, including accumulators, without
   changing start-event or pre-loop compaction order.
3. **Settle tools.** First loop action; compose the already-factored
   `preCheckTools` → `executeToolBatch` → `postProcessToolResults` pipeline.
4. **Finalize run.** Move the straight-line terminal processing while keeping
   persistence-before-`stream_end` ordering.
5. **Consume provider iteration.** Move the channel/timer loop and its recovery
   outcomes. Update the inactivity AST test to inspect the new action and add a
   fast behavioral inactivity test.
6. **Request provider iteration.** Move the densest branching last, after the
   state and directive vocabulary is proven.

Before each move, add or identify focused coverage for its load-bearing branch
families. Test additions are allowed; changing characterized behavior is not.
Commit only after that phase's verification passes or its pre-existing
exception is reproduced and precisely recorded.

## Load-bearing invariants

- Existing deferred cleanup order stays in `generateResponse`; do not shorten
  the stream-start timeout context by deferring its cancel inside an action.
- A provider attempt's cancel and span end exactly once on success, plugin
  cancel, stream-start error, recovery/retry, inactivity, mid-stream error, and
  terminal return. Give the attempt an idempotent end/close operation.
- Coordinator handling of every retry preserves the current iteration
  decrement. Successful recovery resets both discovery counters and replaces
  messages, tools, and system prompt.
- A delta buffered before a general mid-stream error is persisted but not
  emitted as a normal delta. Phase buffering remains until stop reason is
  known.
- Thinking blocks precede text/tool-use blocks and reset only after the
  assistant tool-use message is appended.
- Tool calls precede tool results and original request order survives batching.
- Direct return resets full and final content before `replace_content` and
  completion.
- Pending envelopes are appended/emitted before parsing; envelope-data filters
  run before references and routed broadcasts.
- `context.WithoutCancel` remains on provider/envelope/usage/metrics outcome
  bookkeeping.
- Assistant persistence succeeds before `stream_end`; PTY success is set only
  after message, usage, and metrics persistence and before `stream_end`.
- No action calls another action. No callback graph replaces the state machine.

## Characterization and focused coverage

The `10/01` production-door suite is the behavioral floor and must remain
green throughout. Keep it unchanged except for additive coverage. It already
pins plain, serial tool, provider error, overflow recovery, pre-loop
compaction, plugin message cancellation, plugin tool cancellation, and forced
rate-budget behavior.

Add only the focused gaps needed to make each move safe. Priority cases are:

- preparation/init: representative early failure plus lifecycle/start ordering;
- provider request: general pre-stream error, refused/failed recovery, and
  cancellation or budget termination;
- provider consumption: behavioral inactivity, usage/phase aggregation, and a
  recovery/retry path;
- tool settlement: direct return or permission denial alongside the existing
  serial/plugin-cancel coverage;
- finalization: persistence failure suppresses `stream_end` and structured
  output ordering remains stable.

Do not turn this task into unrelated coverage work. If current behavior looks
wrong, characterize and log it; do not silently fix it.

## Acceptance criteria

- [ ] The six named actions exist as private methods with explicit state and
      typed outcomes.
- [ ] `generateResponse` remains the only coordinator and visibly owns all
      loop/terminal routing and lifecycle cleanup.
- [ ] Each extraction is a separate commit with its focused tests and recorded
      complexity delta.
- [ ] The existing production-door characterization suite remains green.
- [ ] Provider cancel/span ownership is exactly-once and retry iteration
      accounting is unchanged.
- [ ] Streaming, tool-event, envelope/filter, persistence, PTY, and terminal
      event ordering remain unchanged.
- [ ] Cognitive and cyclomatic complexity of `generateResponse` decreases
      after every extraction; no step merely moves the complete original
      complexity into one new action.
- [ ] The final coordinator is recognizable and materially smaller; extracted
      actions are cohesive even if a particular action remains above generic
      audit thresholds because of essential local branching.
- [ ] No production edit is needed outside `chat_generate.go` and the new
      action/state files. Any need to edit `chat.go`, `chat_loop_state.go`,
      `chat_tool_executor.go`, `context.go`, or `tool.go` must stop for
      orchestrator review as scope drift.
- [ ] No `chatServiceImpl` field cluster/type extraction or rewrite is included.

## Per-phase verification

Always run the immutable focused behavior and race suites:

```bash
go test ./internal/service/... \
  -run '^(TestGenerateResponseCharacterization_.*|TestRecoverFromContextOverflow_RateBudgetForcedCompactionSucceeds|TestGenerateResponse_(HaltSessionReflex_AbortsTurnBeforeLLMCall|NonHaltReflex_DoesNotAbortTurn))$' \
  -count=1 -v
go test -race ./internal/service/... \
  -run '^(TestGenerateResponseCharacterization_.*|TestRecoverFromContextOverflow_RateBudgetForcedCompactionSucceeds|TestGenerateResponse_(HaltSessionReflex_AbortsTurnBeforeLLMCall|NonHaltReflex_DoesNotAbortTurn))$' \
  -count=1 -v
```

Then run, after each individual extraction:

```bash
git diff --check
go build ./internal/service/...
go vet ./internal/service/...
go test ./internal/service/... -count=1
go test -race ./internal/service/... -timeout 20m -count=1
go build ./...
go vet ./...

golangci-lint run \
  --config docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 \
  ./internal/service/... 2>&1 | tee /tmp/ad12-phase-N-complexity.log
```

The audit complexity configuration reports historical findings and may exit
nonzero. Record the `generateResponse` and new-action diagnostics rather than
mislabeling the whole audit scan as a pass/fail gate. The required trend is a
decrease in coordinator complexity after every phase and no monolithic move.

At completion also run `go test ./... -count=1`. The prior full service race
timed out at Go's 10-minute limit in migration-heavy SQLite setup with no race
report; this task uses a 20-minute limit. Record exact outcomes and never call a
timeout a PASS.

## Non-goals

- Full rewrite, callback/state-machine framework, or six new service types.
- Runtime-session lifecycle type extraction or wholesale `chatServiceImpl`
  decomposition.
- Behavior fixes found during characterization.
- Unrelated cleanup, naming, context, storage, tool, or provider changes.
- Shared tracker edits, reviewer approval, or architect claims by the worker.

## Fresh review requirements

The reviewer must trace all coordinator directives and terminal paths,
independently verify the defer/cancel/span and persistence/event ordering,
prove actions never call one another, reproduce the focused normal/race suite,
and compare complexity per commit rather than only on the final tree. Review is
read-only and must not edit or claim operator approval.

## Work Log

- Phase 1 in progress: established the private directive vocabulary,
  `generationLifecycle`, and immutable `turnSetup` handoff; added a
  production-door disabled-agent characterization that pins termination before
  `stream_start` and provider invocation.
- Phase 1 complete: extracted `prepareTurn` without moving root-span or deferred
  cleanup ownership. Focused behavior passed; focused race passed in 102.333s;
  full non-race service passed in 87.641s; service/all build and vet and
  `git diff --check` passed. The required aggregate service race reproduced the
  pre-existing migration-heavy limitation at the doubled ceiling: exit 1 after
  1200.862s, `test timed out after 20m0s` while
  `TestDurableAgentWakeEndToEndPersistsPromptAsSessionMessage` was still in
  SQLite migration, with no `DATA RACE` report. This is recorded as a TIMEOUT,
  not a pass. Audit lint exited 1 on historical diagnostics; coordinator
  complexity decreased from cognitive 458 / cyclop 228 / gocyclo 225 to
  cognitive 397 / cyclop 185 / gocyclo 183. `prepareTurn` is cohesive at
  cognitive 62 / cyclop 45 / gocyclo 44 / maintainability 8 rather than a move
  of the original monolith.
- Phase 2 complete: extracted `initializeRun` and established pointer-owned
  `runState` for the loop, mutable request state, accumulators, usage, and
  reasoning configuration. `stream_start` still precedes the pre-loop
  compaction gate; a strengthened production-door test pins that order. The
  stream-start timeout cancel is returned to and deferred by the coordinator,
  preserving its lifetime and defer position. Focused behavior passed; focused
  race passed in 105.052s; service/all build and vet and diff check passed.
  The first full non-race service run hit the existing `driveBootSession`
  send-on-closed-channel panic after 10.916s; the identical retry passed in
  87.532s. Audit lint exited 1 on historical diagnostics; coordinator
  complexity decreased again to cognitive 389 / cyclop 177 / gocyclo 175.
  `initializeRun` did not trigger cognitive/cyclomatic/maintainability
  diagnostics (only the configured >100-line `funlen` diagnostic at 114).
- Phase 5 complete (third extraction in the required dependency order):
  extracted `settleToolTurn`, composing the existing discovery/precheck/batch/
  postprocess pipeline and returning explicit continue/finish outcomes to the
  coordinator. Existing serial, multi-tool ordering, and real plugin policy
  denial coverage protect the move. Focused behavior passed; focused race
  passed in 103.296s; full non-race service passed in 88.184s; service/all
  build and vet and diff check passed. Audit lint exited 1 on historical
  diagnostics; coordinator complexity decreased to cognitive 358 / cyclop 164
  / gocyclo 162. `settleToolTurn` is cognitive 21 / cyclop 17 / gocyclo 17,
  a bounded composition action rather than relocated coordinator complexity.
- Review correction after the Phase 2 midpoint review: the mechanical mutable-
  state qualification had leaked `run.tools` into observable telemetry keys,
  JSON detail keys, and continuation reason text. Restored all observable
  literals to their characterized `tools` spelling while retaining
  `run.tools` only as the Go state expression. Added a production-door
  pre-stream provider-error assertion pinning structured detail key `tools`
  and explicitly rejecting `run.tools`.
