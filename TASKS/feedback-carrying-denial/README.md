# Feedback-Carrying Denial — implementation

Implements `docs/engineering/architecture/23-feedback-carrying-denial.md`, a follow-up
architecture topic from the harness audit **approved for implementation, 2026-08-21**
(operator sign-off recorded directly in that doc's own updated text). The doc's target: a
denial should carry a decision, a reason, and — where the denying subsystem can produce
one — a context-specific suggestion, with provenance, while enforcement itself stays hard.
Concretely: extend `internal/recover`'s existing `RecoverableError{Kind, ToolName, SentArgs,
SchemaURI, ErrorPath, ErrorReason, Suggestion}` taxonomy with new policy-class `Kind` values,
each explicitly ineligible for C2's automatic LLM-repair loop, and apply that one shared shape
across four surfaces that today produce flat, hardcoded, or generic-template prose.

**Not part of `docs/engineering/TASKS.md`'s Phase 0-9 sequence.** A sibling to
`TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`, `TASKS/scheduling/`,
`TASKS/teams/`, `TASKS/agent-host-acp/`, `TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`,
`TASKS/skills/`, and `TASKS/loops/` — kept in its own top-level `TASKS/` subfolder for the
same reason those are: this work originates from a dedicated architecture-review pass, not
`docs/engineering/TASKS.md`'s original plan.

## Read before starting any task here

1. `docs/engineering/architecture/23-feedback-carrying-denial.md` in full — the "foundation
   already exists" framing, the four-surface inventory, the target design, the halt_session
   special case, and the "what's genuinely still open" list. This planning pass resolves all
   three open items below with real evidence, not a coin flip — read each task's own Context
   for the trace, not just this README's summary.
2. `internal/recover/recover.go` and `repair.go` in full — the real C1/C2 shape this batch
   extends. **`internal/service/tool.go`'s `attemptRepair` (the real C2 call site, lines
   404-510) and `buildAgentErrorEnvelope` (line 573) are the two functions every task below
   converges on** — the shared JSON-envelope renderer this batch reuses already exists and
   already takes a `*recover.RecoverableError` directly; no task here invents a second
   renderer.
3. `docs/engineering/GLOSSARY.md` — check before introducing any new name. This batch adds
   one entry (task `01`) disambiguating the new `RecoverableError.Source` field from
   `AgentReflex.ProvenanceTier` (`internal/agent/reflexes/telemetry.go:109`) — both are
   "who/where did this come from" tags on unrelated mechanisms, and the naming-collision
   discipline this file exists for applies directly.
4. `docs/engineering/EXECUTION-PROCESS.md` — the task-file format, worker/reviewer
   discipline, and escalation rules every task file below follows.

## This planning session's own research corrected or sharpened the doc's framing on four points

Found during independent verification against current code, not assumed from the
architecture doc's own prose — logged in `TASKS/ESCALATIONS.md`'s corresponding 2026-08-21
entry:

1. **The MCP trust-tier surface already flows through `internal/recover`'s classify/repair
   pipeline; the other three don't.** `internal/mcp/manager.go`'s `ExecuteTool`/
   `ExecuteToolOnServer` (lines 752/819) wrap `ValidateResultSize`'s error and return it as
   the tool-transport error, which reaches `internal/service/tool.go`'s `attemptRepair` via
   `Execute` → `callTransport`. Permission denial, human-reject, and plugin pre-hook denial
   are all decided and rendered directly inside `chat_tool_executor.go`'s tool-plan-building
   loop, **before the tool transport is ever called** — they never touch
   `recover.Classify`/`Wrap`/`attemptRepair` today and structurally can't reach C2's
   auto-repair loop regardless of `Kind`. This matters for scoping each task's actual risk:
   only task `05` (MCP) touches the real repair-gating logic; `02`/`03`/`04` only need to
   *construct* a `*recover.RecoverableError` directly and render it with the existing
   `buildAgentErrorEnvelope` — Classify/Wrap/repair-eligibility never see them.
2. **`IsRecoverable()` currently conflates two questions that the new policy Kinds pull
   apart, and this is the one piece of real, required design work in this batch — not "which
   package."** `Kind.IsRecoverable()` (`internal/recover/recover.go:86`) is the single gate
   both `attemptRepair` (`tool.go:414`) and `classifyAndFormatToolError` (`tool.go:539`) use
   to decide *both* "does this get a structured envelope" and "should C2 attempt an LLM
   repair" — today those are the same boolean because every existing `Kind` wants both. The
   four new policy Kinds want the first (yes, structured envelope) and explicitly not the
   second (no auto-repair — doc 23's own "fail loudly" rule). Task `01` adds a second method,
   `Kind.AutoRepairEligible()`, rather than overloading `IsRecoverable()` a second time — see
   its own Context for why folding this into `IsRecoverable()` itself was rejected.
3. **The plugin-SDK backward-compat question (doc 23's third open item) resolves cleanly,
   and asymmetrically, once traced end-to-end.** `internal/plugin/subprocess/plugin.go`'s
   `subprocessEventHook.Handle` (line 549) already calls
   `CallResult[EventHandleResult](...)`, and `EventHandleResult` — a type alias to
   `github.com/hollis-labs/plugin-sdk/subprocess.EventHandleResult`, **pinned at v0.3.0
   already, no local `replace` directive** — **already has a `Reason string` field on the
   wire** (`plugin-sdk@v0.3.0/subprocess/types.go:128`, populated by the SDK's own
   `server.go:293`). Nanite's host-side code simply never reads `result.Reason` today. So:
   *Reason* needs zero SDK version bump for either builtin or subprocess plugins — task `04`
   just has to stop discarding it. *Suggestion* has no wire field in v0.3.0 at all — a real
   SDK addition would be needed for a subprocess plugin to send one as a distinct field from
   Reason. This batch's call (task `04`): land Reason end-to-end now (free); subprocess
   plugins fold "try this instead" guidance into the Reason string they already send;
   builtin/in-process hooks (a real, live pattern — `internal/memory/extraction.go`'s
   `perTurnHook`/`postCompactHook` implement `plugin.EventHook.Handle` directly in Go today,
   confirming this path is real, not hypothetical) get both Reason *and* Suggestion for free
   via the existing `event.Data` map convention, no wire protocol involved. A real
   plugin-sdk version bump adding a dedicated `Suggestion` field to `EventHandleResult` is a
   named, explicit follow-up for subprocess-plugin parity — not blocking this task's Done
   means.
4. **`events_composite.go`'s `context.pre_compact` `EmitPreHook` call already discards its
   return value** (line 159, inside a `safego.Go` goroutine, result unused) **and
   `subprocessEventHook.isPreHookEvent` (`plugin.go:623`) doesn't even list `context.pre_compact`
   as a cancel-eligible type** — so today this event type is fire-and-forget end-to-end
   despite riding the `EmitPreHook` API. Not a bug this batch is fixing (doc 23 doesn't ask
   for it, and it's orthogonal to Reason/Suggestion visibility), but task `04`'s own Context
   flags it so a worker doesn't mistake "this call site ignores the new return shape" for a
   missed spot.

## What's genuinely still open per doc 23 — resolved by this planning pass

- **Whether `internal/recover` is extended in place vs. a sibling type.** Decided: **extend
  in place.** `buildAgentErrorEnvelope` (`internal/service/tool.go:573`) is the one existing,
  already-correct JSON-envelope renderer and it takes `*recover.RecoverableError` concretely
  — a sibling type would either duplicate that renderer or need an adapter, both regressions
  against this repo's own "one typed source of truth" bar. The MCP surface (task `05`)
  already flows through `recover.Classify`'s idempotent already-wrapped-error path
  (`recover.go:169-172`) for free once it constructs a `*RecoverableError` directly — a
  sibling type would need its own parallel classify/gate logic. See task `01`'s Context for
  the full trace.
- **The exact new `Kind` names.** Doc 23's illustrative four map cleanly onto the four real
  surfaces once traced against the code — not a renaming exercise, a real 1:1 fit:
  `KindPermissionDenied` (permission engine's mode/rule-based deny, task `02`; also the
  human-reject path, task `03` — same Kind, distinguished by `Source`, since both are "a
  permission decision," just from a rule vs. a human), `KindCapabilityForbidden`
  (`tool_execution_rules.go`'s agent_tools/known_tools membership gate — a grant-absence, not
  a policy judgment call, task `02`), `KindPolicyRefused` (plugin pre-hook refusal, task
  `04`), `KindResultTooLarge` (MCP trust-tier `ValidateResultSize` failure, task `05`). Still
  provisional per doc 23's own caveat — a worker finding a better name mid-implementation
  documents the deviation, doesn't silently rename without a note.
- **Plugin-SDK backward compatibility.** Resolved above (item 3) — Reason needs no bump;
  Suggestion-for-subprocess-plugins is a named follow-up, not this batch.

## Migration numbering

**None needed.** Verified against real schema and code, not assumed: approval requests
(`internal/permission/engine.go`'s `ApprovalRequest`/`ApprovalResponse`) are purely
in-memory (`sync.Map` + a `chan`, no `store` reference anywhere in that file) — task `03`'s
`Feedback` field is transient, and it still gets persisted for free once it lands in
`chat.ToolCallRef.ErrorReason`, which already rides the normal message-content persistence
path (no new column). The permission engine, plugin pre-hook contract, and MCP validator are
all in-process Go types with no DB-backed state at all. The Recovery Pack (task `06`) reads
`sessions.halted_reason` — a real, already-existing column (confirmed live and
`GLOSSARY.md`-documented, `internal/store/sessions.go:35`) — no schema change. This batch is
the one sibling folder in `TASKS/` that claims **zero** migration numbers; nothing here
collides with `TASKS/plugin-system`'s `135`, `TASKS/skills`'s `136`-`137`, or
`TASKS/loops`'s `138`-onward claims.

> **⚠️ Sibling-claim restatement is stale — annotated 2026-08-24 at `5ec930c8`.** The "zero
> migration numbers" verdict above is unaffected and still correct; only the sibling numbers
> it quotes have moved. Skills' `136`-`137` **landed**. Loops landed `138`-`146`, not
> "`138`-onward" as planned — `63d79028` shifted its whole range by +3. Plugin System's `135`
> is **unusable**, not pending: that shift left it a permanently burned hole. Nanite's goose
> provider runs without `WithAllowOutofOrder` (`Store.migrate` in `internal/store/store.go`),
> and goose selects migrations by version number alone — in `UpVersions`
> (`internal/gooseutil/resolve.go`, goose v3.27.3) the applied set is a map keyed on the
> version integer, no filename and no checksum, and both selection loops skip any version
> already in it. So for a file filling a hole at or below a database's highest applied version,
> **which of two things happens depends on whether that database has already applied that
> version**: not previously applied → collected as missing, `Up` aborts and the service fails
> to start; already applied → both loops skip it, the file never runs, nothing is reported,
> goose considers the database up to date. **Silent.** There is no third case — a version equal
> to the highest applied version is by construction already applied. The silent branch is the
> dangerous one: schema divergence between databases of different vintages, with no startup
> failure to announce it. Highest on disk is now `147`;
> next free is **148**, re-derived at use. See `TASKS/INDEX.md`'s "Migration numbering — the
> claiming rule" banner and `docs/engineering/tracking-integrity.md` check 9.

## A drift note, logged not chased

`internal/agent/reflexes/telemetry.go:106`'s comment cites `05-provenance-tier-enforcement.md`
as the source of `ProvenanceTier` — no such file exists anywhere under `docs/`. Unrelated to
any task in this batch (it's a different provenance concept — see the GLOSSARY item above)
and not something any task here is asked to fix; noted so a future reader of that comment
doesn't waste time hunting for a doc that isn't there.

## Task sequence

Flat-numbered `01`-`06` across three phases, same convention as `TASKS/plugin-system/` and
`TASKS/loops/`. Each file's own header states its Phase.

**Phase 1 — Shared taxonomy foundation.** No schema, pure Go. Everything else in Phase 2
builds on this.

| Task | Depends on |
|---|---|
| `01-shared-kind-taxonomy-and-auto-repair-gate.md` | none |

**Phase 2 — The four extension points.**

| Task | Depends on |
|---|---|
| `02-permission-engine-structured-denial.md` | `01` |
| `03-human-reject-feedback.md` | `02` (same file, `internal/permission/engine.go` and `chat_tool_executor.go`'s permission switch — sequence, don't run as true concurrent commits) |
| `04-plugin-prehook-structured-contract.md` | `01`; sequence after `03` (shares `chat_tool_executor.go`, a disjoint block from `03`'s — coordinate merge order, not a real content dependency) |
| `05-mcp-trust-tier-suggestion.md` | `01` |

**Phase 3 — The halt_session special case.** Independent mechanism — no `recover.Kind`
involved at all, see task `06`'s own Context.

| Task | Depends on |
|---|---|
| `06-halt-session-recovery-pack-replay.md` | none |

## Parallelization plan

**Wave 1 — parallel, worktree-isolated.** `01`, `06`. Fully independent starting points:
`01` touches `internal/recover/recover.go` and `internal/service/tool.go`; `06` touches
`internal/recovery/pack/pack.go` and `internal/service/recovery_pack_glue.go` — no shared
mechanism, no shared file.

**Wave 2.** `02` and `05`, both depending only on `01`, mutually parallel-safe — confirmed
file-disjoint: `02` touches `internal/permission/{engine,rules}.go`,
`internal/service/tool_execution_rules.go`, and `chat_tool_executor.go`'s permission-deny +
default + execution-rules blocks; `05` touches only `internal/mcp/manager.go`.

**Wave 3.** `03` (needs `02`), then `04` (needs `01`, sequenced after `03` for the shared-file
reason above — both touch `chat_tool_executor.go`, in different, non-overlapping blocks of
the same function, so true concurrency is avoidable but not worth the risk given three tasks
already touch this one file across the batch). `04` also touches
`internal/plugin/events.go` and `internal/plugin/subprocess/plugin.go` — the concurrently
active `TASKS/plugin-system` batch touches `internal/plugin/host.go` and subprocess
registration internals (different files); low collision risk, but re-check both batches'
diffs against `internal/plugin/*` before merging either.

## What this batch does NOT do

- **Sandboxing or capability-restricting plugins.** Out of scope for this doc and this
  batch — see `TASKS/plugin-system/README.md`'s own scope fence for that separate work.
- **Fixing `context.pre_compact`'s fire-and-forget/non-cancellable status.** A real,
  pre-existing gap (see "corrected or sharpened" item 4 above) but orthogonal to
  Reason/Suggestion visibility and not asked for by doc 23. Flagged in task `04`'s Context,
  not fixed by it.
- **Bumping `github.com/hollis-labs/plugin-sdk` to add a `Suggestion` field to
  `EventHandleResult`.** A real, named follow-up for subprocess-plugin parity (see item 3
  above) — not this batch, since Reason alone (needing no bump) already closes the
  model-visible gap doc 23 is most concerned with, and a cross-repo SDK version bump is a
  genuinely separate piece of work with its own release/versioning discipline.
- **Reworking `internal/recover`'s prose-based `Classify()` matching for the three
  chat-tool-executor surfaces.** Permission, human-reject, and plugin-prehook denials are
  already fully known/structured at their call sites — they construct
  `*recover.RecoverableError` directly rather than being pattern-matched from an error
  string, so no new `Classify()` prose branch is needed for them (only MCP's surface, task
  `05`, and it resolves via the existing idempotent already-wrapped-error path, not a new
  prose pattern either).
- **A new reflex action kind, or any change to `internal/agent/reflexes`.** The halt_session
  mechanism itself (which reflex kinds can fire it, its `deny_overrides` semantics) is
  untouched — task `06` only changes what happens to the halt *reason* on the session's next
  cold-boot turn, not how or when a session gets halted.

## Escalations logged during this planning pass

See `TASKS/ESCALATIONS.md`'s corresponding 2026-08-21 entry for the four corrections above
(items are logged as findings, not stop-and-wait escalations — none blocked planning) and
for the resolution of doc 23's three explicitly-open questions.
