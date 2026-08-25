# Turn vs. Run — implementation

Implements `docs/engineering/architecture/22-turn-vs-run.md`'s "Target design," approved for
implementation by the operator 2026-08-21 (see that doc's own updated "Target design" section:
*"Approved for implementation, 2026-08-21 — exposing Turn as a callable primitive and the
`CancelActiveGeneration` → `Run.Cancel` rename are now real, scoped, sequenced work, tracked
under `TASKS/turn-vs-run/`"*). A sibling to `TASKS/plugin-system/`, `TASKS/loops/`,
`TASKS/skills/`, `TASKS/teams/`, `TASKS/agent-host-acp/`, `TASKS/scheduling/`,
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, and
`TASKS/filesystem-snapshots/` — kept in its own top-level `TASKS/` subfolder for the same
reason those are: this work originates from a dedicated architecture-review pass, not
`docs/engineering/TASKS.md`'s original Phase 0-9 plan.

## Read before starting any task here

1. `docs/engineering/architecture/22-turn-vs-run.md` in full — short, but every sentence is
   load-bearing. The core split: **Turn** = one model invocation + stream + tool-call
   production + return control (`ls.iteration` in `chat_loop_state.go`, today); **Run** =
   repeated Turns + tool settlement + continuation policy + completion
   (`generateResponse` as a whole, today). `chat_loop_state.go`'s own vocabulary
   (`MaxTurns`, `TerminationMaxTurns`) confusingly names the *outer* (Run) level "Turn" —
   this batch does **not** rename those constants; see "What this batch does NOT do."
2. `docs/engineering/GLOSSARY.md`'s **Turn** vs. **Run** entry and its **Turn.Cancel** vs.
   **Run.Cancel** vs. **Session.Stop** vs. `interrupt.requested`/`interrupt.acknowledged` vs.
   ACP's `session/cancel` entry — both already landed (this planning session did not add
   them; a prior design-alignment pass did). Task `02` below makes the code/comments this
   batch touches match what the Glossary already states, and closes out the
   Glossary/architecture-doc's own "recommended but not yet executed" framing.
3. `docs/engineering/EXECUTION-PROCESS.md` — task-file format, worker/reviewer discipline,
   escalation rules.
4. `internal/service/chat_generate.go`'s `generateResponse` (the whole function — it's long,
   ~3,650 lines total; the tool-settling loop itself is lines 757-1726) and
   `internal/service/chat_loop_state.go` (`loopState`, `resolveIterationLimits`,
   `shouldStop`) — the current, real Run implementation this batch partially refactors.
5. `internal/service/workflow_step_executor.go`'s `ExecuteLLMStep` (lines 123-254) and
   `internal/agentworkflow/interfaces.go`'s `StepExecutor` interface — the duplicate loop
   this batch's Phase 2 partially consolidates.
6. `internal/loopdetect/detector.go` — confirmed, independently, this planning session: a
   per-session sliding-window repeat-count detector (default window 10, threshold 3) owned
   by one application-wide `Detector` instance, keyed by `session_id`. Genuinely unrelated to
   Turn/Run — a different mechanism (runaway-repetition detection, not iteration/settlement
   accounting). No task below touches it.

## What this planning session's own research confirmed, corrected, or scoped beyond doc 22

Independent research against current code (not taken on doc 22's or the Glossary's prose
alone), logged in full in `TASKS/ESCALATIONS.md`'s 2026-08-21 "Turn vs. Run planning" entry:

1. **The tool-settling loop body is far larger and more state-entangled than doc 22's own
   one-paragraph description suggests.** `generateResponse`'s `for ls.iteration = 0; ;
   ls.iteration++` loop (`chat_generate.go:757-1726`) is ~970 lines. Inside it: direct SSE
   sends to the caller's `chan chat.StreamEvent` mid-stream (deltas, thinking blocks, PTY
   presence broadcasts, auto-artifact creation — all fired *during* stream consumption, not
   after); five different mid-loop retry shapes that decrement `ls.iteration` and `continue`
   rather than advancing (compaction recovery on a stream-start error, mid-stream
   context-overflow recovery, two separate rate-budget-pause auto-retries); an OTel span per
   provider call; and per-iteration reads/writes into `loopState` (`consecutiveFailures`,
   `compactRecoverableAttempts`, `rateBudgetPauseAttempts`, `totalRequestToolsCalls`,
   `consecutiveEmptyRequests`, `lastActivity`, `toolCallRefs`). This is not a
   "pull the loop body into a function" refactor — it needs a real, narrow contract for what
   moves into the primitive and what stays Run-level policy. See task `01`'s Context for the
   exact boundary this batch draws (provider-call + stream-consumption + tool-call
   extraction only; recovery/retry *decisions*, tool execution, and tool settlement all stay
   in `generateResponse`).
2. **`workflow_step_executor.go`'s `ExecuteLLMStep` is a materially simpler shape than
   `generateResponse`'s loop** — confirmed by direct comparison, not assumed from doc 22's
   framing. It has no SSE streaming, no plugin filters/hooks, no reflexes, no PTY/CLI
   routing, no compaction/rate-budget recovery, no permission checks (tool execution is a
   direct `e.tools.Execute` call gated only by the capability-restriction allowlist, not
   `preCheckTools`'s permission/blocked/concurrency-safety machinery), and no per-tool caps.
   Its own doc comment ("runs one capability-restricted agent turn") is itself an instance of
   the same one-word-two-meanings problem doc 22 names: the *implementation* is a whole
   capability-restricted tool-settling loop (up to `DefaultMaxToolIterations = 10` rounds),
   i.e. a capability-restricted **Run**, not a single **Turn** — doc 22's own text says
   exactly this ("documented as running 'one capability-restricted agent turn,' but its
   actual implementation... is a completely separate, independently-maintained tool-use
   loop"). This confirms consolidation is real and worth doing, but it must reuse only the
   inner *model-call-and-stream-consumption* unit — `ExecuteLLMStep`'s own tool-settlement
   loop (capability-restricted, no permission checks) is deliberately different from
   `generateResponse`'s (`preCheckTools`/`executeToolBatch`/`postProcessToolResults`, with
   full permission/blocked/concurrency gating) and must **not** be collapsed into one shared
   tool-settlement path. See task `04`.
3. **`go-llm-types` (the shared streaming-event library both call sites already depend on)
   already uses "Turn" for exactly this unit** — confirmed directly,
   `libs/go-llm-types/types.go:101-104`: `IsTurnComplete(ev StreamEvent) bool` reports
   "whether ev is a terminal event marking the end of a turn." This is real, existing,
   upstream corroboration for doc 22's definition — not new vocabulary this batch invents.
4. **Real, load-bearing doc/comment drift found beyond `CancelActiveGeneration` itself,**
   all citing the *old* `Turn.Cancel` framing as if it were still correct or still-future:
   - `internal/runtime/agent/acp_session.go:365-373`'s `Stop` doc comment says ACP's
     `session/cancel` "is `Turn.Cancel`'s analog, not `Session.Stop`'s, per
     `docs/engineering/GLOSSARY.md`'s 'Turn.Cancel vs. Session.Stop...' entry" — the cited
     entry title is stale (it's now "Turn.Cancel vs. Run.Cancel vs. Session.Stop vs. ...")
     and, more importantly, the *substance* is now wrong per that same entry's current text:
     "ACP's `session/cancel` method... is spec'd as Run-scoped... it must map onto
     `Run.Cancel`, never onto `Session.Stop`." This comment currently says the opposite of
     what the Glossary it cites now states. Real correctness fix, not cosmetic — task `02`.
   - `docs/engineering/architecture/17-acp.md:72` asserts "When the ACP client abstraction
     implements this method, it should call our `Turn.Cancel` path — never our
     `Session.Stop` path" — same staleness, same fix, same file this batch's rename task
     touches for consistency (an architecture doc, not application code, but a direct,
     load-bearing correctness claim about which internal method an ACP client must call).
   - `docs/engineering/architecture/00-overview.md:53` ("`CancelActiveGeneration`→`Run.Cancel`
     rename... recommended, not yet executed") and `:55` ("Turn as a callable harness
     primitive, and the `workflow_step_executor.go` consolidation it would enable... target
     architecture only, no implementation plan") are both now stale the moment this batch's
     tasks land — `53` is task `02`'s job to correct; `55` is task `04`'s (the task that
     actually finishes the consolidation).
5. **Scoping deviation from the illustrative 3-task sketch: this batch is 4 tasks, not 3.**
   The illustrative sketch's item (a) ("extract/expose a Turn primitive... without changing
   behavior") is split into two tasks here — `01` (define the primitive, unit-tested in
   isolation, not yet wired into `generateResponse`) and `03` (rewire `generateResponse` to
   call it, preserving all five retry/recovery shapes exactly). Reasoning: finding 1 above
   means this is a materially larger, higher-blast-radius change than a typical single task
   in this project's other batches — splitting lets a reviewer sign off on the primitive's
   *contract* (inputs, outputs, what it does and does not own) before the actual rewire of a
   ~970-line, five-retry-shape function is attempted, rather than reviewing both at once.
   This also opens a real parallelization win doc 22's own "Target design" paragraph implies
   but doesn't spell out: once `01` lands, `03` (chat's Run consuming Turn) and `04`
   (workflow's capability-restricted Run consuming Turn) are mutually independent — both
   need the primitive to exist, neither needs the other's rewire done first. See "Task
   sequence" below.

## What this batch does NOT do

- **Rename `MaxTurns`/`TerminationMaxTurns`/`resolvedMaxTurns` (`chat_loop_state.go`) or the
  `/api/harness/v1/sessions/{id}/turns` HTTP route.** Doc 22 flags that `chat_loop_state.go`'s
  own vocabulary confusingly calls the *outer* (Run) loop "Turn," and the HTTP route name
  has the identical collision (a whole Run is called a "turn" at the wire level). Doc 22's
  own "Naming" and "Target design" sections, and the operator's approval, are scoped
  *exclusively* to `CancelActiveGeneration`'s conceptual rename — neither section proposes
  renaming these. Per this project's "plan how, not whether" discipline, this batch does not
  expand into an unauthorized rename of a persisted `TerminationCode` enum value (referenced
  by the `chat-loop-terminated` envelope schema and potentially stored run rows) or a public
  HTTP path — both are real, separately-decidable, larger/breaking changes. Flagged as a real
  open question worth a future, dedicated operator conversation if the collision proves
  confusing in practice; not silently left as an oversight.
- **Durable-agent Turn-level wakes** (`durable_agent_runtime_controller.go`'s `SendMessage`
  always gets a full Run per wake today) — doc 22 explicitly states no evidence of pressure
  for this; not planned.
- **`Turn.Cancel` itself (single-model-call cancellation)** — doc 22 explicitly marks this a
  real future option, not part of the approved batch. `CancelActiveGeneration` stays
  Run-scoped; this batch only fixes its *name*, not its granularity.
- **Any change to `internal/loopdetect`** — a different, unrelated mechanism (see "Read
  before starting," item 6).
- **Any change to tool execution, tool settlement, permission checking, or per-tool caps** —
  in either `generateResponse` (`preCheckTools`/`executeToolBatch`/`postProcessToolResults`)
  or `workflow_step_executor.go`'s own capability-restricted tool loop. The Turn primitive
  this batch builds covers *only* the model-call-and-stream-consumption unit; everything
  downstream of "the model asked for these tools" is explicitly out of scope and stays
  exactly as it is today in both call sites.
- **Any frontend/UI work** — none of this batch touches anything user-facing beyond identical
  SSE event sequences (verified byte-for-byte-equivalent as part of task `03`'s Done means).

## Task sequence

Flat-numbered `01`-`04` across two phases, same convention as `TASKS/plugin-system/` and
`TASKS/agent-host-acp/` — each file's own header states its Phase.

**Phase 1 — Define the primitive; fix the name.** Two independent starting points.

| Task | Depends on |
|---|---|
| `01-define-turn-primitive.md` | none |
| `02-rename-cancelactivegeneration-to-run-cancel.md` | none |

**Phase 2 — Consume the primitive.** Both need `01`; mutually independent of each other
(disjoint files — `03` touches `chat_generate.go`/`chat_loop_state.go`, `04` touches
`workflow_step_executor.go`).

| Task | Depends on |
|---|---|
| `03-wire-generateresponse-onto-turn-primitive.md` | `01` |
| `04-consolidate-workflow-step-executor-onto-turn-primitive.md` | `01` |

## Parallelization plan

**Wave 1 — parallel, worktree-isolated.** `01` and `02`. Confirmed file-disjoint: `01` adds a
new file (`internal/service/turn.go` or equivalent — worker's call, see task `01`) and touches
nothing existing; `02` touches `internal/service/chat.go` (doc comments only),
`internal/runtime/agent/acp_session.go` (doc comment only), `docs/engineering/GLOSSARY.md`,
`docs/engineering/architecture/17-acp.md`, and `docs/engineering/architecture/00-overview.md`
— zero overlap with `01`.

**Wave 2 — parallel, worktree-isolated, once `01` merges.** `03` and `04`. Confirmed
file-disjoint: `03` touches `internal/service/chat_generate.go` and
`internal/service/chat_loop_state.go` (read-only, for `ls.limits.idleTimeout`); `04` touches
only `internal/service/workflow_step_executor.go`. Both read (don't modify) task `01`'s new
file. Merge either order.

## Migration numbering

**No migration needed.** This batch is a pure in-process Go refactor (task `01`, `03`, `04`)
plus doc-comment/architecture-doc corrections (task `02`) — no schema, no new table, no new
column. Confirmed the current highest migration on disk at this planning session's authoring
time (2026-08-21) is `134_agent_profiles_protocol_transport.sql`; `TASKS/plugin-system`
provisionally claims `135`, `TASKS/skills` claims `136`-`137`, `TASKS/loops` claims `138`
onward — none of that is relevant here since this batch consumes no number from that
sequence. If a future task in this folder is ever added that *does* need a migration,
re-list the migrations directory immediately before claiming a number, per every sibling
batch's own standing caution.

> **⚠️ Sibling-claim restatement is stale — annotated 2026-08-24 at `5ec930c8`.** This batch's
> own "no migration needed" verdict is unaffected and still correct. The sibling numbers it
> quotes have all moved: Skills' `136`-`137` **landed**; Loops landed `138`-`146` (not
> "`138` onward" as planned — `63d79028` shifted its range by +3); and Plugin System's `135`
> is **unusable**, a permanently burned hole rather than a pending claim. `Store.migrate` in
> `internal/store/store.go` builds goose without `WithAllowOutofOrder`, and goose selects
> migrations by version number alone — in `UpVersions` (`internal/gooseutil/resolve.go`, goose
> v3.27.3) the applied set is a map keyed on the version integer, no filename and no checksum,
> and both selection loops skip any version already in it. So for a file filling a hole at or
> below a database's highest applied version, **which of two things happens depends on whether
> that database has already applied that version**: not previously applied → collected as
> missing, the run fails with a missing-migration error and the service does not boot; already
> applied → both loops skip it, the file never runs, nothing is reported, goose considers the
> database up to date. **Silent.** There is no third case — a version equal to the highest
> applied version is by construction already applied. The silent branch is the dangerous one:
> schema divergence between databases of different vintages, with no startup failure to
> announce it.
>
> If a task is ever added here that does need a migration: **next free is 148** at `5ec930c8`
> (`ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1` →
> `147_remove_untouched_official_catalog_source.sql`) — and re-derive it then, don't carry it
> from here. See `TASKS/INDEX.md`'s "Migration numbering — the claiming rule" banner and
> `docs/engineering/tracking-integrity.md` check 9.

## Escalations logged during this planning pass

See `TASKS/ESCALATIONS.md`'s corresponding 2026-08-21 "Turn vs. Run planning" entry for: the
loop-body complexity finding driving the 3-task sketch's split into 4 real tasks; the three
stale `Turn.Cancel` doc/comment citations found beyond `CancelActiveGeneration` itself; and
the explicit decision not to expand scope into renaming `MaxTurns`/`TerminationMaxTurns`/the
`/turns` HTTP route.
