# Production islands — architect decision table

This folder is the remediation guide's Wave 4 output: six fully-built,
extensively-tested Go features that compile, pass their own test suites, and
are — per the audit's `deadcode` run plus exhaustive grep confirmation for
each — **unreachable from any production entry point** as of the audited
commit (`8feeee5c`). None of these are bugs. Each is a completed feature
sitting one wiring decision away from either mattering or being formally
retired.

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

Per the guide's Wave 4 framing, every island task in this folder queues the
same three-way choice for the architect — **this task-creation pass does not
make any of these calls**:

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

Each task file presents the tradeoffs for its own island in detail and
deliberately does not recommend one. That is the architect's call.

## Decision table

| Feature | Finding | Current state (built/tested/unreachable) | Decision (wire/defer/retire) | Notes |
|---|---|---|---|---|
| Grounding memory recall | `GO-MEM-001` (medium, high confidence) | Built: `internal/grounding/recall.go`+`outcome.go`, ~530 LOC. Tested: `recall_test.go`+`outcome_test.go`, ~360 LOC. Unreachable: `GroundingRecaller`/`GroundingLogger` fields exist on `SelfToolsTransport` (`internal/selftools/self_tools_transport.go:217,220`) but no production code assigns them; also gated off by default via `NANITE_GROUNDING_ENABLED`. | _(architect to fill in)_ | See `01-grounding-memory-recall.md`. |
| Hadron context gate | `GO-MEM-002` (low as-is / would-be-medium if revived unfixed, high confidence) | Built: `internal/contextbroker/gate_hadron_blueprints.go`, 302 LOC. Tested: `gate_hadron_blueprints_test.go`, 337 LOC. Unreachable: never registered as a `ContextSource` in `internal/service/container.go`'s source list (its 4 live siblings are), confirmed — `NewHadronBlueprintGate` has zero non-test callers. **Contains a latent unclamped-relevance-score bug** (`calculateRelevance`, `gate_hadron_blueprints.go:183-230`) that must be fixed before any "wire" decision — see task file. | _(architect to fill in)_ | See `02-hadron-context-gate.md`. |
| Team semantic routing | `GO-SVCEXEC-003` (medium, high confidence) | Built: `internal/service/team_routing.go`'s entire production surface (`TeamRoutingService`, `SendToSlot`, `InstallTeamRunRouting`, `resolveAgentSlugForSlot`). Tested: extensively, per the file's own test suite. Unreachable: the intended wiring point, `internal/api/team_runs.go`'s `handleLaunchTeam`, calls only `LaunchTeamRun` (line 152), never `InstallTeamRunRouting`. **Self-flagged**: `TASKS/teams/HANDOFF.md:30` already names this exact risk and asks a future reader to verify it — this audit did. | _(architect to fill in)_ | See `03-team-semantic-routing.md`. |
| Tool builder/YAML architecture | `GO-MCPTOOL-001` (medium, high confidence) | Built: `internal/tool/tool.go`, `builder.go`, `register.go`, `adapt.go`, `yaml_loader.go`. Unreachable: every exported symbol `deadcode`-flagged; only `ResultCache` (a different, live part of the same package) survives. `tool.go`'s package doc still actively claims this is "the primary way to construct tools in Go code" — false of the current runtime. A prior task's own comment (`internal/service/tool_concurrency_classification.go:36-40`) already reached the same "not wired into the live runtime" conclusion. | _(architect to fill in)_ | See `04-tool-builder-yaml-architecture.md`. "Wire" here is a materially bigger lift than the other five islands — see task file. |
| Reasoning-augmented tool selection | `GO-MCPTOOL-002` (medium, high confidence) | Built: `RankTools`/`SelectWithSignals` (`internal/toolclient/ranking.go`), `SelectToolsAugmented` (`internal/toolclient/broker.go:170-193`). Unreachable: former consumer (a debug SQL row) removed by `TASKS/phase-0/23-export-and-drop-decision-tables.md`, per `ranking.go:292-297`'s own comment. Production entry point `SelectToolsAsProvider` (`broker.go:416`) never calls this path. Per-boot wiring cost (skills-dir load, memory-recaller construction — `internal/service/container.go:692-710`) is still paid regardless, feeding only this dead path. | _(architect to fill in)_ | See `05-reasoning-augmented-tool-selection.md`. |
| Curated tool knowledge matcher | `GO-MCPTOOL-003` (low, high confidence) | Built: `internal/toolclient/tool_knowledge.go`, 405 lines. Unreachable: zero callers outside its own file and test file — confirmed by grep. Third parallel "what tools match this intent" mechanism (alongside the live `intent.go` keyword scorer and the dead `RankTools`, finding above). Smallest LOC and thinnest test investment of the six islands. | _(architect to fill in)_ | See `06-curated-tool-knowledge-matcher.md`. |

No disposition in the "Decision" column is filled in by this pass — per the
guide's own Wave 4 framing and this batch's operator instruction, that is
explicitly deferred to whoever runs the architect-decision queue next
(remediation guide §9, item 4).

## A note on `internal/toolclient`'s three-island cluster

Findings 04, 05, and 06 (tool builder/YAML architecture, reasoning-augmented
selection, curated tool knowledge) are three of what the audit counted as
**five independent "what tools match/should exist for this intent" mechanisms**
in the tool-selection cluster (REPORT.md §8.7's "Duplication synthesis"
paragraph) — the other two (`stash.BuiltinCategorizer`, `toolclient.SelectByIntent`)
are live and serve genuinely different purposes. No file/type dependency ties
these three task files together, so this pass does not sequence them — but an
architect resolving one may want visibility into the other two before
deciding, since a "wire" call on one could make a "retire" call on another
more or less attractive (e.g. building real `mcp.Manager` integration for the
`internal/tool` architecture, per finding 04's "wire" option, could plausibly
absorb or obsolete what finding 06's curated matcher does). Each of the three
task files cross-references this note; none assumes a sequencing order.
