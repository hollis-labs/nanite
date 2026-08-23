# Production islands — decision and disposition table

This folder began as the remediation guide's Wave 4 decision queue: six
fully-built, extensively-tested Go features that were — per the audit's
`deadcode` run plus exhaustive grep confirmation for each — **unreachable
from any production entry point** as of the audited commit (`8feeee5c`). The
reachability descriptions below are retained as historical pre-remediation
evidence until each task closes; a row with an implemented disposition records
the post-remediation state instead. AD-06's grounding retirement, AD-07's
Hadron-gate retirement, and AD-11's curated-matcher retirement are represented
in that completed form.

## Read this before touching any file in this folder

**Check current source first.** The remediation guide's own Wave 4 section
says this explicitly: *"some were completed after the audited commit."*
Development on `main` did not stop when the audit ran. Every task file below
opens its "What to do" section with an instruction to re-verify — via
`deadcode`, direct grep for the relevant constructors/call sites, and reading
the actual composition root (`internal/service/container.go` and siblings) —
that each feature is still unreachable before treating any of the
wire/defer/retire framing below as live. A feature that shipped a real caller
between the audit date and whenever this task is picked up needs its task
file's disposition corrected, not silently executed against stale evidence.

## The production-reachability proof chain

Per the remediation guide's planning principles, a feature is not "done" —
and, symmetrically, is not fairly called "dead" either — without checking the
full chain:

```text
production entry point
    ↓
construction / registration / wiring
    ↓
feature invocation
    ↓
observable behavior
```

For all six features below, the audit traced this chain and found it breaks
at the **construction/registration/wiring** step: the code exists, compiles,
and is unit-tested in isolation, but nothing in `internal/service/container.go`
(or the relevant equivalent composition point) ever constructs the type and
assigns it where a production code path would find it. No production entry
point ever reaches step 2, so steps 3 and 4 are moot — the feature cannot
currently produce any observable behavior in a running `nanite serve`
process, regardless of how well-built it is.

## The three-way decision

Per the guide's Wave 4 framing, every island task in this folder originally
queued the same three-way choice for the architect. Those choices are now
recorded in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`; rows not yet
updated after implementation preserve their original decision-queue context
and are not evidence that a decision remains open:

- **wire** — this was the intended feature; connect it to a real production
  entry point and test the real path end to end.
- **defer** — intentionally staged for later; record a trigger/owner, and
  make sure nothing in docs or comments describes it as live in the interim.
  Per the guide: a deferred feature **must not look production-live in docs
  and should not impose unnecessary boot/runtime cost** — this is a hard
  requirement of choosing "defer," not a nice-to-have, and each task file
  below states what that requirement concretely means for that feature (env
  vars left defaulting on, per-boot I/O still paid, stale package-doc claims,
  etc.).
- **retire** — remove the implementation, its tests, and any docs that
  describe it as current or planned, if it no longer reflects real
  architectural intent.

Each task file's original option analysis presents the tradeoffs for its own
island without recommending one; the architect's resulting calls are recorded
in `ARCHITECT-DECISIONS.md`.

## Decision table

| Feature | Finding | Historical audit state or implemented disposition | Decision (wire/defer/retire) | Notes |
|---|---|---|---|---|
| Grounding memory recall | `GO-MEM-001` (medium, high confidence) | **Retired by `09/01` (2026-08-23):** deleted `internal/grounding` (534 production / 361 test lines), its `internal/store` persistence adapter, the `SelfToolsTransport` fields, and the complete E2 pre-dispatch recall/enriched-message state. The broader `internal/memory.Service` remains live. | **Retire — AD-06, implemented** | See `01-grounding-memory-recall.md` Work Log. |
| Hadron context gate | `GO-MEM-002` (low as-is / would-be-medium if revived unfixed, high confidence) | **Retired by `09/02` (2026-08-23):** deleted `internal/contextbroker/gate_hadron_blueprints.go` (302 production lines) and its test file (337 test lines). Pre-remediation verification found zero non-test callers and no registration alongside the four live `ContextSource` implementations. Retirement preserves those four sources and removes the latent unclamped-relevance-score hazard. | **Retire — AD-07, implemented** | See `02-hadron-context-gate.md` Work Log. |
| Team semantic routing | `GO-SVCEXEC-003` (medium, high confidence) | **Wired by `09/03` (2026-08-23):** the composition root constructs `Container.TeamRouting`; `handleLaunchTeam` preflights the dependency, launches the run, and requires `InstallTeamRunRouting` to install run-scoped semantic reflexes. Installation failure removes returned partial reflexes best-effort and returns a structured 500 containing the persisted run ID/status; the already-created run is not rolled back. The separate explicit `SendToSlot`/`ResolveLazySlot` path remains outside this task. | **Wire — AD-08, implemented** | See `03-team-semantic-routing.md` Work Log. |
| Tool builder/YAML architecture | `GO-MCPTOOL-001` (medium, high confidence) | Built: `internal/tool/tool.go`, `builder.go`, `register.go`, `adapt.go`, `yaml_loader.go`. Unreachable: every exported symbol `deadcode`-flagged; only `ResultCache` (a different, live part of the same package) survives. `tool.go`'s package doc still actively claims this is "the primary way to construct tools in Go code" — false of the current runtime. A prior task's own comment (`internal/service/tool_concurrency_classification.go:36-40`) already reached the same "not wired into the live runtime" conclusion. | _(architect to fill in)_ | See `04-tool-builder-yaml-architecture.md`. "Wire" here is a materially bigger lift than the other five islands — see task file. |
| Reasoning-augmented tool selection | `GO-MCPTOOL-002` (medium, high confidence) | Built: `RankTools`/`SelectWithSignals` (`internal/toolclient/ranking.go`), `SelectToolsAugmented` (`internal/toolclient/broker.go:170-193`). Unreachable: former consumer (a debug SQL row) removed by `TASKS/phase-0/23-export-and-drop-decision-tables.md`, per `ranking.go:292-297`'s own comment. Production entry point `SelectToolsAsProvider` (`broker.go:416`) never calls this path. Per-boot wiring cost (skills-dir load, memory-recaller construction — `internal/service/container.go:692-710`) is still paid regardless, feeding only this dead path. | _(architect to fill in)_ | See `05-reasoning-augmented-tool-selection.md`. |
| Curated tool knowledge matcher | `GO-MCPTOOL-003` (low, high confidence) | **Retired by `09/06` (2026-08-23):** deleted `internal/toolclient/tool_knowledge.go` (405 production lines) and its 162-line package-local test after re-confirming zero external callers. The live `internal/toolclient/intent.go` `SelectByIntent` keyword scorer remains unchanged. | **Retire — AD-11, implemented** | See `06-curated-tool-knowledge-matcher.md` Work Log. |

The remaining placeholder cells are the task-creation pass's historical
snapshot. Their authoritative calls live in `ARCHITECT-DECISIONS.md`; update a
row to its implemented state when that task closes rather than rewriting its
pre-remediation evidence early.

## A note on `internal/toolclient`'s three-island cluster

Findings 04, 05, and 06 (tool builder/YAML architecture, reasoning-augmented
selection, curated tool knowledge) were three of what the audit counted as
**five independent "what tools match/should exist for this intent" mechanisms**
in the tool-selection cluster (REPORT.md §8.7's "Duplication synthesis"
paragraph). AD-11's implemented retirement removes the curated matcher from
the current architecture. The live `toolclient.SelectByIntent` keyword scorer
remains the intent-selection source of truth; `stash.BuiltinCategorizer` is
also live and serves a different bucketing purpose. Findings 04 and 05 retain
their own task records and dispositions rather than being resolved here.
