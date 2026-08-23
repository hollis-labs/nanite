# `chatServiceImpl` / `generateResponse` — responsibility map, phase boundaries, and characterization tests

**Phase:** Audit remediation — Wave 5 (architectural concentration)
**Status:** implemented
**Depends on:** none (self-contained planning/characterization task). Sequencing note: any future extraction-execution tasks this task's own output proposes should not be scoped or dispatched until an architect has reviewed and approved this task's responsibility map and phase-boundary proposal — see `requires_architect_decision` below.
**Touches:** `internal/service/chat_generate.go` (`generateResponse` and its private helpers), `internal/service/chat.go` (`chatServiceImpl` struct definition and its 84 methods, spread across this file and others in the package), new characterization/regression test files under `internal/service/` (exact filenames TBD by the worker — likely `chat_generate_characterization_test.go` plus targeted additions to existing `chat_generate_*_test.go` files for the coverage-gap branches). Read-only reference: `internal/service/stream.go` (`StreamManager` — the in-repo precedent for this exact kind of extraction).
**requires_architect_decision:** true — per the remediation guide's §9 decision queue item 5 ("`chatServiceImpl` decomposition boundaries"). This task's own deliverable (the responsibility map + phase-boundary proposal) is explicitly the input to that decision, not a substitute for it. No extraction may begin — in this task or any follow-on task — until an architect has reviewed and signed off on the proposed boundaries.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 5 — architectural concentration · **Dispatch unit:** `W5`
> - **Depends on:** Wave 4 complete
> - **Blocks:** `11/01`, `11/04`, `11/11`
> - **Parallel-safe with:** **none — this task takes an exclusive lock on `internal/service`.** It is a multi-phase extraction of an 84-method type, done one phase at a time with behavior re-verified after each; any concurrent edit to the package invalidates its characterization tests.
> - **Gated on:** AD-12 — and note the decision has a **prerequisite deliverable**: the responsibility map. Do not decide the extraction boundaries before it exists.
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

This is the single largest architecture task in the whole remediation batch. It addresses the two highest-severity, highest-evidence architectural findings the audit produced anywhere in the codebase.

### Findings addressed

- **GO-SVCEXEC-001** (**high**, complexity, confidence high) — `(*chatServiceImpl).generateResponse` (`internal/service/chat_generate.go:122-2028`) is a **~1,900-line, cognitive-complexity-458, cyclomatic-225-228, maintainability-index-0** function. It is **the single highest-complexity function found anywhere in this audit**, by roughly **5x** the next-sharpest outlier (`workflow/executor.go`'s `Run`, cognitive 87). It was **missed entirely by the audit's own mechanical triage pass** — the base report's automated complexity table never surfaced it; it was found only because a cluster reviewer read the actual file during the deep-dive phase. Coverage is **36.5%**.
- **GO-SVCEXEC-002** (**high**, god-object, confidence high) — `chatServiceImpl` (`internal/service/chat.go:214-392`) has **52 fields** and **84 methods** — more than double the remediation guide's own god-object thresholds on both axes. It fails the guide's "wiring vs. embedded behavior" test decisively: `generateResponse` and dozens of other multi-hundred-line methods on this type implement real domain behavior directly, not thin delegation.

Both findings are evidenced in `docs/audits/2026-08-21-go-quality/REPORT.md` §8.4 and `docs/audits/2026-08-21-go-quality/findings.json` (ids `GO-SVCEXEC-001`, `GO-SVCEXEC-002`). Both are named explicitly in the remediation guide's (`~/dev/chrispian/inbox/nanite-audit-triage-remediation-planning-guide.md`) §4 Wave 5 as the lead item of the "architectural concentration" wave.

### Root cause

Two related but distinct accumulation patterns, per the audit's own read:

1. **`generateResponse`'s complexity is largely accidental, not essential.** Real domain complexity does exist (an LLM tool-use turn genuinely has many phases and failure modes), but the audit's verdict is explicit: the gap between this function and the *same file's* own `workflow_engine.go`-style DAG runner — comparable domain complexity, cleanly factored into named steps — is exactly the essential-vs-accidental line the guide asks reviewers to judge, and `generateResponse` lands on the accidental side. The function has an established in-file pattern of extracting helpers for other logic (dozens of small private methods exist elsewhere in the package); the ~970-line tool-use loop inside `generateResponse` simply never received the same treatment — budget enforcement, plugin hooks, telemetry construction, provider streaming, error/compaction recovery, and tool dispatch are all inlined into one unbroken block instead.
2. **`chatServiceImpl` accreted responsibility clusters over time by adding fields/methods directly onto the type** rather than behind narrower collaborators, each individually reasonable in isolation but never later revisited as a group. The clearest example of an already-cohesive, not-yet-extracted cluster is the **PTY/agent-runtime session-lifecycle field group** — 9 distinct `sync.Map`s plus 2 companion fields, all named and commented consistently, all owned by the same conceptual subsystem (per-session runtime-agent bookkeeping), never pulled into their own type.

### Current behavior

**`generateResponse` (`internal/service/chat_generate.go:122-2028`).** The audit's own read of the file identified these structural phase boundaries — cite them as a **starting point for the phase-boundary proposal this task must produce, not a final decomposition**:

| Approx. line | Marker in source | Phase |
|---|---|---|
| 122 | `func (s *chatServiceImpl) generateResponse(...)` | function start |
| 193 | `// --- Load session ---` | session load |
| 205 | `// --- Resolve agent ---` | agent resolve |
| 235 | `// --- Resolve model ---` | model resolve |
| 268 | `// --- Resolve provider ---` | provider resolve |
| 333 | `// --- Tool selection via ToolService ...` | tool selection |
| 475 | `// --- Assemble context (slot-based) ---` | slot assembly |
| 609 | `// --- Stream start ---` | stream start |
| 652 | `// --- Pre-loop budget / compaction gate (pt3 T4) ---` | pre-loop budget/compaction gate |
| 738 | `// --- Tool-use loop ---` | **start of the ~970-line unbroken loop** |
| 867 | `// --- Provider call ---` | (inside loop) provider call |
| 909 | `// --- Pre-hook: message.sending ---` | (inside loop) plugin pre-hook |
| 1262 | `// --- Consume provider stream ---` | (inside loop) provider streaming/consumption |
| 1601 | `// --- Build assistant message with tool_use blocks ---` | (inside loop) assistant-message assembly |
| 1631 | `// --- Execute tools (pre-check → parallel/serial → post-process) ---` | (inside loop) tool dispatch |
| 1728 | `// --- Post-processing ---` | post-processing, through line 2028 |

The tool-use loop (lines ~738-1728, ~970 lines) is the single largest undivided block and contains budget enforcement, plugin hooks, telemetry construction, provider streaming, error/compaction recovery, and tool dispatch all inlined together — this is where the bulk of the cognitive-458/cyclomatic-225 score concentrates.

**Production entry point / callers.** `generateResponse` is unexported and reached through exactly one production door, by design: `chatRunnerAdapter` (`internal/service/chat_dispatcher_runner.go:43`) binds `svc.generateResponse` as the `Dispatcher`'s runner closure — the file's own comments describe this as deliberately preserving a "one door" invariant (`chat.go:459`: "The Dispatcher delegates to `chatServiceImpl.generateResponse`... door."). Internally, `chatServiceImpl.launchGeneration`/`runGeneration` (`chat.go:548`, and call sites at `chat.go:738,781,811,881,940` — `handleMessage`, `retryLastMessage`, `sendAgentMessage`, `triggerHarnessTurn`, `triggerMessageWake`) all launch `generateResponse` as a cancellable goroutine. The subagent execution path (`internal/service/subagent_runner.go:368,420`) reaches the same method through a narrow `invoker` interface with a `generateResponse` method — same underlying implementation, different call-site framing. Any characterization test suite must exercise generateResponse through (or equivalently to) these real entry points, not just as a bare function call with hand-built arguments, to actually lock observable behavior.

**`chatServiceImpl` (`internal/service/chat.go:214-392`).** 52 fields, 84 methods. Field clusters visible directly in the struct: session/stream/context plumbing, plugin/command/process/task wiring, permission/path-grant state, embedding-warning dedup, tool-schema/cache/model-catalog state, route-dispatch wiring, and the PTY/agent-runtime session-lifecycle cluster:

- `activeSessions sync.Map` (`chat.go:302`)
- `freshBootSessions sync.Map` (`chat.go:310`)
- `activeSessionSlots sync.Map` (`chat.go:332`)
- `toolPartitionStates sync.Map` (`chat.go:339`)
- `rebootingSessions sync.Map` (`chat.go:347`)
- `displacedSessions sync.Map` (`chat.go:363`)
- `activeSessionContextBlocks sync.Map` (`chat.go:381`)
- `agentEventBridge *agentEventBridge` (`chat.go:317`)
- `agentBootDirAdapter *agentBootDirAdapter` (`chat.go:326`)

— 9 fields total (7 `sync.Map`s the audit's recommendation calls out directly, plus the 2 non-`sync.Map` companions `agentEventBridge`/`agentBootDirAdapter` that participate in the same per-session lifecycle bookkeeping, e.g. both are cleaned up alongside the `sync.Map`s in the session-teardown path at `chat.go:1010-1021`). All 9 already share consistent naming, adjacent doc comments, and a single conceptual purpose (per-session runtime-agent state), and are already used together as a group at their one teardown call site (`chat.go:1010-1021`) — exactly the shape of a cluster that's ready to extract.

**Direct in-repo precedent.** `StreamManager` (`internal/service/stream.go:15-31`) already exists as a fully realized instance of this exact move: its own doc comment reads *"`StreamManager` owns the concurrent state for message streams, SSE connections, and presence. Extracted from Engine's 6 `sync.Map` fields."* (`stream.go:15-16`). It is now a clean, independently-testable, 9-field/many-method type (`streams`, `msgToSession`, `sessionToMsgs`, `sessionSSE`, `presenceClient`, `activePresence`, `cliThrottle` — 6 `sync.Map`s plus 2 non-map fields — `stream.go:17-31`), owned by `chatServiceImpl` today as a single `streams *StreamManager` field rather than 6 raw fields. This is the "healthy, narrow, focused" comparison point the audit's own god-object table cites directly (§8.4: *"`StreamManager` 9/— ... all healthy, narrow, focused (`StreamManager` notably a good precedent for decomposing `chatServiceImpl`)"*). This task should study exactly how that extraction was structured (constructor shape, method surface, how `chatServiceImpl` now delegates to it) and propose following the same pattern for the PTY/agent-runtime cluster.

### Desired invariant

After this task, generateResponse's and chatServiceImpl's *externally observable behavior* must be fully protected by a test suite thorough enough that a future incremental extraction can be verified not to have changed behavior — independent of whether any extraction has actually happened yet. Concretely: any future PR that moves code out of `generateResponse` or off of `chatServiceImpl` should be checkable against this task's characterization suite and fail loudly if streaming output, tool-dispatch sequencing, error/compaction-recovery behavior, or plugin-cancel handling silently changes.

## What to do

Follow the remediation guide's exact 7-step preferred approach (§4 Wave 5) — in order, not in parallel, and not skipped:

1. **Lock behavior with characterization/regression tests before touching anything.** Write tests against `generateResponse` (through its real entry points — `launchGeneration`/the Dispatcher path, per Current behavior above) that capture today's actual behavior across representative turn shapes: a plain no-tool-call turn, a single-tool-call turn, a multi-tool-call turn, a provider error mid-stream, a context-overflow/compaction trigger, and a plugin-initiated cancel. These are *characterization* tests (assert what the code currently does, faithfully, even if a given behavior looks surprising) — do not "fix" anything discovered while writing them; if a genuine bug is found, log it as a discovery per this project's `surface-discovery` convention rather than silently patching it inside this task.
2. **Improve coverage of the specific under-covered branches the 36.5% figure hides**, not coverage generally:
   - **provider-error branches** — the audit's phase table's "Provider call" (~`chat_generate.go:867`) and "Consume provider stream" (~`chat_generate.go:1262`) sections;
   - **compaction-recovery branches** — the "Pre-loop budget/compaction gate" section (~`chat_generate.go:652`) and `recoverFromContextOverflow` (`chat_generate.go:2257`, called from within the loop);
   - **plugin-cancel branches** — the "Pre-hook: message.sending" section (~`chat_generate.go:909`) and any cancellation propagation through the tool-dispatch phase (~`chat_generate.go:1631`).
   Confirm the actual current coverage of each of these three branch families against real source before writing tests — the 36.5% figure is a whole-function average and may understate or overstate any one branch family; measure it directly (`go test -coverprofile` + `go tool cover -func` scoped to `chat_generate.go`) rather than assuming.
3. **Identify 3-6 coherent phases/capabilities within `generateResponse`.** The table under Current behavior above is the audit's own read and a legitimate starting point — but it is explicitly **not** a final decomposition; this task's job is to turn it into a real proposal with named phase boundaries (line ranges, phase name, what state each phase reads/mutates, what it hands to the next phase). Pay particular attention to the ~970-line tool-use loop (lines ~738-1728) — it is the largest undivided block and almost certainly needs its own internal phase breakdown (provider call / stream consumption / message assembly / tool dispatch, per the audit's sub-markers) rather than being treated as one "phase."
4. **For each identified phase, write down the candidate extraction boundary** — using the guide's own responsibility-map format, applied here at the *phase* level rather than the whole-type level: capability, state read, state mutated, dependencies (what other `chatServiceImpl` fields/methods the phase touches), and what would need to become a method parameter or return value if the phase were pulled into a standalone method or type. Do **not** actually extract in this task — this is the proposal, not the execution. (See Non-goals.)
5. **Produce a full responsibility map for `chatServiceImpl` itself**, in the guide's exact format:

   ```
   capability
   fields owned
   methods owned
   shared mutable state
   dependencies
   callers
   candidate extraction boundary
   ```

   Enumerate this for every responsibility cluster visible on the 52-field/84-method struct (session/stream/context plumbing, plugin/command/process/task wiring, permission/path-grant state, embedding-warning dedup, tool-schema/cache/model-catalog state, route-dispatch wiring, and the PTY/agent-runtime cluster). For the PTY/agent-runtime cluster specifically, the map is largely pre-populated by Current behavior above — confirm those 9 fields and their real methods/callers against current source (line numbers may have shifted since this task was authored — re-grep before trusting them), and write the `StreamManager` comparison up explicitly as the model to follow for this cluster's own extraction (constructor shape, what becomes a method on the new type vs. stays a thin `chatServiceImpl` accessor, how the one teardown call site at `chat.go:1010-1021` would change).
6. **State explicitly, for both `generateResponse` and `chatServiceImpl`, that the outer state-machine flow must remain recognizable after any future extraction** — i.e. a reader who knows today's `generateResponse` should be able to read a post-extraction version and still see "session load → agent resolve → ... → tool-use loop → post-processing" as a legible top-level shape, even if each named phase is now a call to an extracted method rather than inline code. This is a constraint the phase-boundary proposal must respect, not an afterthought for whoever executes the extraction later.
7. **Verification-rerun instruction for follow-on work:** state explicitly in this task's proposal that whoever executes an extraction (in a follow-on task, one phase at a time, per the guide's step 4-5) must rerun the audit's behavior/race/complexity reports (`go test ./internal/service/...`, `go test -race ./internal/service/...`, and the complexity tooling referenced in `docs/audits/2026-08-21-go-quality/REPORT.md` §25/§10) **after each individual extraction**, not just once at the end of the whole decomposition — this is the guide's explicit instruction and this task's proposal should carry it forward as a stated requirement on any follow-on task, not leave it implicit.
8. **State explicitly that a full rewrite is a last resort, not the default.** Per the guide's step 7: "consider a rewrite only if it is clearly safer/cleaner than incremental extraction." This task's proposal should not recommend a rewrite unless the responsibility-map/phase-boundary work itself surfaces a concrete reason incremental extraction is unsafe (e.g. the mutate-in-place state-machine shape genuinely cannot be preserved through incremental extraction for some specific phase) — and if it does, that reasoning must be written down explicitly for the architect to evaluate, not asserted.

## Non-goals

- **Do not attempt the actual decomposition in this task.** This task *is* the planning/characterization work. Extracting any phase of `generateResponse` or any field cluster off of `chatServiceImpl` is explicitly out of scope here — those become one or more follow-on tasks once this task's responsibility map and phase-boundary proposal are architect-approved.
- Do not "fix" any behavior discovered to look wrong while writing characterization tests — characterize what exists today; surface anything that looks like a real bug as a discovery, don't silently correct it inside this task.
- Do not attempt to reduce `chatServiceImpl`'s field/method count as a goal in itself — the guide is explicit elsewhere in this wave (see the sibling `02-selftoolstransport-decomposition.md` task) that metric reduction is not a valid reason to split a type; the responsibility map either demonstrates real, extractable cohesive clusters or it doesn't, and the metrics are not the deliverable.
- Do not propose extracting every one of the 52 fields/84 methods into new types in this task's proposal — identify the clusters that are genuinely cohesive (the PTY/agent-runtime cluster is the clearest one; others may or may not be, per what the responsibility map actually shows) and say plainly where a cluster does *not* have a clean extraction boundary yet, rather than forcing one.

## Dependencies

- None hard-blocking. This task can start immediately.
- Produces input for (but does not itself create): one or more follow-on extraction-execution tasks, to be scoped once the architect has reviewed this task's output. Do not pre-create those follow-on task files as part of this task — that is planner/architect work, per this batch's own README ("What this pass deliberately did NOT do").

## Tests required

- The characterization/regression suite described in step 1 above, covering: no-tool-call turn, single-tool-call turn, multi-tool-call turn, provider error mid-stream, context-overflow/compaction trigger, plugin-initiated cancel.
- Targeted coverage additions for the three under-covered branch families named in step 2, with before/after coverage numbers recorded in the Work Log (scoped measurement via `go tool cover -func`, not just the whole-file average).
- All new/changed tests must pass under `-race` (`go test -race ./internal/service/...`) — this package is exactly where GO-SVCCORE-006 (a distinct, already-tracked `-race` timeout investigation, not this task's scope) lives, so a worker should confirm any new test additions don't themselves introduce a new race or timeout, and should note in the Work Log if the existing `-race` timeout affects this task's own verification.

## Prevention

- The characterization suite itself is the durable prevention mechanism for silent behavior drift during any future extraction — this is the guide's own "regression reproducing original defect" principle applied preemptively (there's no single "defect" here, but the same logic: lock behavior before changing structure).
- Recommend (for the architect to decide, not unilaterally enforce here) that any follow-on extraction task include, as an explicit acceptance criterion, "this task's characterization suite still passes unchanged" — making this task's test suite the actual gate for the decomposition program, not just a one-time artifact.
- The rerun-after-each-extraction instruction (step 7 above) is itself a prevention mechanism against a decomposition silently regressing complexity in one phase while improving another — carry it forward explicitly into any follow-on task.

## Verification

```bash
go build ./internal/service/...
go vet ./internal/service/...
go test ./internal/service/... -run 'GenerateResponse|Characterization' -v
go test -race ./internal/service/...
go tool cover -func=<profile> | grep -i "chat_generate.go"
```

Observable behavior required for PASS: the characterization suite exists, passes against current (unmodified) `generateResponse`/`chatServiceImpl`, and demonstrably exercises the six representative turn shapes named above (each as an identifiable test case, not folded into one undifferentiated test); coverage of the three named branch families (provider-error, compaction-recovery, plugin-cancel) is measurably improved over the audit's 36.5% whole-function baseline, with the actual before/after numbers recorded in the Work Log; the responsibility map and phase-boundary proposal exist as reviewable content in this task file's Work Log (or a linked doc, if the worker judges the map too large to inline — but if so, the file path must be recorded here).

## Risk / rollback

- **Regression surface:** none to production behavior — this task adds tests and a written proposal; it makes no changes to `generateResponse` or `chatServiceImpl`'s actual implementation. The only "risk" is a characterization test that is written incorrectly and locks in a misunderstanding of current behavior, which a reviewer should specifically check for (does each test's assertion match a real, traced code path, not an assumption).
- **Rollback approach:** trivial — this task's output is additive (new test files, a written map/proposal). A revert is a plain file removal with no cascading effect on any other code.

## Done means

- [x] A characterization/regression test suite exists exercising `generateResponse` through its real production entry point (the Dispatcher/`chatRunnerAdapter` path or an equivalent that faithfully preserves observable behavior), covering the six representative turn shapes named above.
- [x] Coverage of the provider-error, compaction-recovery, and plugin-cancel branch families is measurably improved over the 36.5% baseline, with before/after numbers recorded.
- [x] A full responsibility map for `chatServiceImpl` exists in the guide's exact format (capability / fields owned / methods owned / shared mutable state / dependencies / callers / candidate extraction boundary), covering every visible responsibility cluster on the type, with the PTY/agent-runtime cluster's entry explicitly citing the `StreamManager` precedent.
- [x] A phase-boundary proposal for `generateResponse` exists, naming 3-6 coherent phases with real (re-verified, not assumed) line ranges, and explicit candidate extraction boundaries for each.
- [x] The proposal explicitly states the outer state-machine flow must remain recognizable post-extraction, and explicitly states that any follow-on extraction task must rerun behavior/race/complexity reports after each individual extraction, not just at the end.
- [x] The proposal explicitly frames a full rewrite as a last resort, only to be recommended if a concrete, written reason surfaces during the mapping work.
- [x] **This task's own "done" is the responsibility map + phase-boundary proposal + characterization test suite — not a refactored `chatServiceImpl` or a decomposed `generateResponse`.** No production code in `chat.go` or `chat_generate.go` is modified by this task.
- [ ] An architect has reviewed and signed off on the responsibility map and phase-boundary proposal before any follow-on extraction task is created or dispatched.
- [x] `go build ./...`, `go vet ./...`, and the verification commands above all pass clean (subject to the documented pre-existing package-wide `-race` timeout; the task-specific race gate passes).

## Work log

- Added `internal/service/chat_generate_characterization_test.go`. Every required
  turn shape enters through the production `Dispatcher.Run` →
  `chatRunnerAdapter` → `generateResponse` door with the real `StreamManager`,
  Context Service, and SQLite store. The identifiable cases cover plain,
  single-tool, deterministic multi-tool, mid-stream provider error, successful
  provider-overflow compaction/retry, and real `plugin.Host`
  `message.sending` cancellation. A seventh production-door case pins the
  pre-loop budget/compaction gate; an additional production-door case pins
  `tool.executing` cancellation through tool settlement; and a targeted helper
  case pins forced rate-budget recovery.
- The overflow case proves two provider calls, a real `drop_enrichment`
  compaction stage, `slot_changed`, retry completion, `stream_end`, and the
  persisted recovered assistant response. The provider-error case pins the
  exact assistant row ID and role, buffered partial content, and
  `had_error:true` metadata.
- The `tool.executing` cancellation case uses a real `plugin.Host`, proves the
  hook payload, verifies `ToolService.Execute` is skipped, pins blocked
  `tool_call` → `tool_result` ordering/error output, finds the refusal block in
  the continuation provider request, and verifies terminal stream completion
  plus the persisted assistant result.
- Added the architect-review input at
  `TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md`.
  It contains the six-phase `generateResponse` proposal, the complete
  87-method responsibility reconciliation in the required map format, the
  current `StreamManager` comparison, the recognizable-outer-flow constraint,
  and per-extraction behavior/race/complexity rerun requirements. It creates no
  follow-on task and does not constitute architect approval.
- Corrected the responsibility map's ambiguous repeated field ownership with a
  canonical exactly-once inventory. It assigns all 52 current struct fields to
  one primary capability, reconciles `7 + 9 + 7 + 10 + 3 + 3 + 1 + 9 + 0 + 0 + 3 = 52`,
  and was mechanically compared with the live struct: 52 names, 52 unique
  names, zero missing, zero extra.
- Re-verified current source rather than relying on stale task citations:
  `chat_generate.go` is 3,684 lines (`generateResponse` lines 121–2,036),
  `chat.go` is 1,407 lines, and `chatServiceImpl` has 52 fields / 87 production
  pointer-receiver methods across 19 files. `StreamManager` currently has nine
  fields and 25 pointer-receiver methods. The map records why the task's
  pre-populated nine-field runtime cluster is not cohesive as written and
  records three construction-only fields with no production receiver read.
- Before coverage (`go test ./internal/service/... -coverprofile=/tmp/w5-10-01-before.out`):
  package 62.3%; `generateResponse` 36.7%;
  `recoverFromContextOverflow` 23.4%; `enforceBudgetOrCompact` 5.9%.
  Start-line-weighted branch-family ranges were provider error 1/111 statements
  (0.9%), compaction recovery 15/111 (13.5%), and the expanded plugin-cancel
  family 1/26 (3.8%).
- Final coverage (`go test ./internal/service/... -coverprofile=/tmp/w5-10-01-after-tool-cancel.out`):
  package 65.6%; `generateResponse` 53.7%;
  `recoverFromContextOverflow` 78.7%; `enforceBudgetOrCompact` 67.6%.
  The same scoped ranges are provider error 28/111 (25.2%), compaction recovery
  59/111 (53.2%), and plugin cancel 26/26 (100.0%). The measured ranges were
  provider error 1,067–1,260 plus 1,396–1,451; compaction recovery 651–653,
  1,067–1,188, 1,396–1,421, and 2,265–2,395; plugin cancel
  `chat_generate.go` 908–929 plus `chat_tool_executor.go` 252–276.
- Verification results: `go build ./internal/service/...` exit 0;
  `go vet ./internal/service/...` exit 0;
  `go test ./internal/service/... -run 'GenerateResponse|Characterization' -v`
  exit 0; focused characterization/recovery `go test -race` exit 0 (68.167s);
  `go build ./...` exit 0; `go vet ./...` exit 0; `go test ./...` exit 0
  (`internal/service` 92.781s). Full `go test -race ./internal/service/...`
  exited 1 only at the existing 10-minute package timeout (600.841s) while
  `TestResolveProvider_StoredProviderID_UsesRuntimeProviderType` was opening and
  migrating SQLite; no race report occurred. This is the already-tracked
  GO-SVCCORE-006 condition, not a timeout or race introduced by the new tests.
- Review correction verification: focused characterization/recovery non-race
  exit 0 (2.655s), focused `-race` exit 0 (84.086s), service build exit 0, and
  service vet exit 0. The first full coverage-profile attempt hit an unrelated
  existing `driveBootSession` send-on-closed-channel panic; the identical retry
  passed in 85.282s and produced the final profile above.
- No production implementation was changed: `internal/service/chat.go` and
  `internal/service/chat_generate.go` are untouched.

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified (re-traced line numbers against current source, re-ran coverage measurement, checked each characterization test against real code rather than trusting the worker's description). -->
