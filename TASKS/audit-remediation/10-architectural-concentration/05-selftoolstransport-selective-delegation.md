# `SelfToolsTransport` — implement the four approved selective delegations

**Phase:** Audit remediation — Wave 5 reopened implementation beat
**Status:** reviewed
**Depends on:** reviewed evidence task `10/02`; AD-13 decided
**Gated on:** none — the operator expressly approved AD-13 on 2026-08-23
**Findings:** GO-MCPTOOL-006
**requires_architect_decision:** false
**requires_security_review:** true — presentation extraction moves the shared trust/access gate
**requires_regression_test:** true
**Parallel-safe with:** `10/04`; this task exclusively owns its listed selftools and composition-root files while active
**Touches:** `internal/selftools/`, `cmd/nanite/main.go`, focused wiring/tests in `internal/api/` and `internal/mcpserver/`, `docs/engineering/selftoolstransport-capability-map.md`, and this task file. Do not edit shared audit trackers; the orchestrator owns those.

## Operator decision and intent

The operator approved the reviewed AD-13 recommendation on 2026-08-23 and
directed implementation in Wave 5 rather than adding it to the follow-up
stack. Implement **selective delegation**, not a uniform domain split:

1. messaging plus session handoff;
2. todo/plan work tracking;
3. agent profile management plus source resolution; and
4. combined card/panel presentation.

`SelfToolsTransport` remains the one MCP catalog and dispatch adapter. This
task succeeds by moving real behavior, policy, and cohesive dependencies into
four owners. Lowering counts alone is not success; forwarding methods left on
`SelfToolsTransport`, duplicate catalogs, or hollow wrappers are failures.

The reviewed source of truth is
`docs/engineering/selftoolstransport-capability-map.md`. Reconcile current
source before editing; at authorization time the baseline was 31 fields, 82
production receiver methods, 66 `CallTool` clauses, and 68 tool names.

## Required target shape

### `MessagingTools`

- Add a concrete collaborator constructed from `*messaging.Service` and
  `mcp.ElicitationService`.
- Move all nine `message_*` / messaging-session `handoff_*` handlers and their
  timeout/directive-elicitation policy onto it.
- Do not confuse these operations with Glass-4 `handoff_stash` and
  `handoff_pointers_expand`; those remain where they are.
- Do not add an interface solely around the already-cohesive messaging service.

Before moving the handlers, add transport-door characterization for normal
send; inbox/thread including `nil` slices serialized as `[]`; ack/resolve;
catch-up default limit; handoff request using context session identity rather
than spoofable arguments; approve/reject; and the unwired-service result for
all nine names. Retain a `SelfToolsTransport.CallTool` routing test across all
nine names after extraction.

### `WorkTrackingTools`

- Own the todo/plan persistence dependency and work-change broadcaster.
- Move the nine todo/plan handlers and `notifyWorkChanged` onto this owner.
- Narrow the current todo interface to the methods actually used; do not carry
  unused `UpdateTodoScope` or `DeleteTodo` merely for compatibility.
- Introduce a narrow session-project lookup seam. Make
  `resolveProjectIDFromSession` one package-level policy taking that seam so
  work tracking and the still-unmoved reminder/pin handlers share exactly one
  implementation.

Add coverage proving each successful mutation broadcasts exactly once (todo
create/update and plan create/update/step-add/delete), reads do not broadcast,
todo/reminder/pin project autofill shares the one session-project resolver,
and plan retains its distinct legacy session-scope autofill behavior.

### `AgentProfileTools`

- Own a narrow store interface containing only agent get/list/create/update
  operations and the existing `AgentClassifier`.
- Move create, list, update, classification, and agent-source resolution onto
  this owner. Keep one editability gate.
- `SelfToolsTransport.Store` remains for unapproved domains; do not expand this
  task into a blanket Store-interface migration.
- Package-level error/result/helper declarations may remain package-level when
  they are not meaningful owner state.

Preserve the existing editability and source-resolution suite and add a test
showing an injected classifier is actually used instead of the fallback.

### `PresentationTools`

- Own `PanelSignalSink`, `PanelLookup`, and the trust resolver.
- Move `card_show` policy/render-target resolution together with panel
  open/close, mode signal, access resolution, and signal emission.
- There must be exactly one `resolvePanelAccess` decision point used by both
  card rendering and panel operations. Do not split the shared security gate.
- Static panel IDs, result/envelope formatting helpers, and static tool
  definitions remain package-level; do not create a second catalog.

Migrate direct receiver tests to the new owner and retain transport routing
smokes for `card_show`, `panel_open`, `panel_close`, and `signal_mode`, plus the
API same-turn card-flush regression.

## Composition and compatibility invariants

- `SelfToolsTransport` gains exactly four collaborator fields and loses the
  eight dependency fields they own: `Messaging`, `Elicitation`, `TodoStore`,
  `Work`, `AgentClassifier`, `PanelSignalSink`, `PanelLookup`, and
  `TrustResolver`.
- `CallTool` remains the only name switch and routes directly to collaborator
  methods. `ListTools`, static definitions, unknown-tool behavior, tool names,
  response shapes, scratchpad rejection, and request context are unchanged.
- Preserve the two-stage construction lifecycle. `NewSelfToolsTransport(store)`
  must create nil-safe collaborators usable by the local MCP server and proxy;
  `cmd/nanite/main.go` then supplies runtime services after the container and
  plugin host exist. Catalog availability must not depend on runtime wiring.
- The local MCP-server path must retain current behavior: store-backed agent
  operations work; unavailable messaging/todo dependencies return their
  existing errors; built-in presentation remains nil-safe.
- Preserve the API's `*SelfToolsTransport` type and same-turn card flush.
- Do not replace existing `context.TODO()` calls or change unrelated behavior
  during extraction.

## Execution order and commit discipline

Work sequentially because all four moves touch the transport struct and switch:

1. characterization coverage, then messaging/session handoff;
2. work tracking and the singular project resolver;
3. agent profiles/source resolution;
4. combined presentation and the singular trust gate.

Commit each completed boundary separately after its focused tests pass. Do not
continue after a failing boundary; diagnose and correct it first. Update the
capability map at the end with actual type names, ownership, counts, and any
source corrections. Record commands and results in this file's Work Log.

## Acceptance criteria

- [x] Four concrete collaborators own the approved behavior and dependencies.
- [x] No moved handler remains a `SelfToolsTransport` forwarding wrapper.
- [x] `SelfToolsTransport` has 27 fields: the 31-field baseline minus eight
      moved dependencies plus four collaborators.
- [x] It has 50 production receiver methods: the 82-method baseline minus the
      32 methods moved by this task.
- [x] All 66 `CallTool` clauses and 68 tool names remain present and routed.
- [x] Messaging and work-tracking coverage described above is added before or
      with the corresponding extraction.
- [x] The presentation trust/access gate and the work project resolver each
      have one implementation.
- [x] No behavior, schema, tool name, response, catalog, or availability
      contract changes.
- [x] No unapproved domain is extracted and no blanket Store split is begun.
- [x] The capability map and Work Log reflect the implemented source.

## Verification

Run after each boundary:

```bash
go test ./internal/selftools ./internal/api ./internal/mcpserver ./cmd/nanite
go test -race ./internal/selftools ./internal/api ./internal/mcpserver ./cmd/nanite
git diff --check
```

Run at completion:

```bash
go build ./...
go vet ./...
go test ./...

git grep -h '^func (st \*SelfToolsTransport)' -- \
  'internal/selftools/*.go' ':!internal/selftools/*_test.go' | wc -l
# expected: 50

sed -n '/^type SelfToolsTransport struct {/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | grep -cE '^[[:space:]]+[A-Za-z][A-Za-z0-9]*[[:space:]]'
# expected: 27

sed -n '/func (st \*SelfToolsTransport) CallTool/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | grep -c '^[[:space:]]*case '
# expected: 66

git grep -n 'resolvePanelAccess' -- 'internal/selftools/*.go'
git grep -n 'ElicitUserInput' -- 'internal/selftools/*.go'
git grep -n 'BroadcastWorkChanged' -- 'internal/selftools/*.go'
```

The task-scoped race command must pass. Full repository verification is also
required. Do not report the previously timed-out package-wide service race run
as passing unless it is independently rerun successfully.

## Non-goals

- Uniform one-owner-per-domain decomposition.
- Any fifth extraction, including learning, chat-history, subagent, dispatch,
  workflow, background, builder, installation, or scheduling domains.
- A new dispatch registry, tool catalog, or schema source of truth.
- Opportunistic behavior fixes, context sweep, Store decomposition, or API
  redesign.
- Reviewer approval or shared tracker edits by the worker.

## Fresh review requirements

The reviewer must independently trace every moved tool name through `CallTool`,
verify the four ownership moves are substantive rather than wrappers, prove
the two singular policy seams, recheck the exact 27-field/50-method/68-name
inventory, exercise nil-safe local-MCP construction, and rerun the focused
normal/race suites. Review is read-only and must not edit or claim architect
approval.

## Work Log

### 2026-08-23 — Worker implementation

Implemented the four operator-approved boundaries in their required serial
order. `SelfToolsTransport` remains the sole static catalog and `CallTool`
name switch; each moved route calls its owner directly, with no forwarding
receiver left behind.

#### 1. Messaging and messaging-session handoff

- Commit `1cc75ce7` (`refactor(selftools): delegate messaging tools`).
- Added transport-door characterization before extraction for send,
  inbox/thread nil-to-`[]` serialization, ack/resolve, catch-up's default limit,
  context-derived handoff session identity, approve/reject, and all nine
  unwired-service results. The retained routing table exercises all nine names
  through `SelfToolsTransport.CallTool`.
- `MessagingTools` now owns `*messaging.Service`, elicitation, all nine
  handlers, the common timeout, and directive-elicitation policy. Glass-4
  handoff was not moved. `NewSelfToolsTransport` constructs a nil-safe owner;
  `cmd/nanite/main.go` replaces it after runtime services exist.
- Focused normal suite passed. Focused messaging race passed in 102.017s.
  Full scoped normal suite passed (`internal/selftools` 45.286s,
  `internal/api` 19.803s, `internal/mcpserver` 3.184s, `cmd/nanite` 5.754s).

Race qualification at this first boundary: the full four-package race command
hit Go's default 10-minute timeout in the selftools package while repeatedly
building fresh 145-migration fixtures; API, MCP server, and command packages
passed and no race report was emitted. The required isolated retry,
`go test -race ./internal/selftools -timeout 20m`, also timed out during the
same migration-heavy fixture setup with no race report. This is recorded as a
timeout limitation, not a PASS. After reproducing it, later boundaries used a
boundary-focused race plus the full scoped normal/build/vet gates, with one
final aggregate attempt reserved for final verification.

#### 2. Todo and plan work tracking

- Commit `bc7b6b6c` (`refactor(selftools): delegate work tracking tools`).
- `WorkTrackingTools` owns an exact 11-method persistence interface, narrow
  session lookup, and broadcaster. Unused `UpdateTodoScope` and `DeleteTodo`
  were not carried forward. All nine handlers and the broadcast policy moved;
  the sole `resolveProjectIDFromSession` is a package-level policy shared with
  the still-unmoved reminder/pin paths.
- Tests prove every successful todo/plan mutation broadcasts exactly once and
  reads do not broadcast. The focused race passed in 68.612s; the full scoped
  normal suite, `go build ./...`, `go vet ./...`, and `git diff --check` passed.
- Task/source correction: the task's original “identical across todo, plan,
  reminder, and pin” premise was false. Plans have no `project_id` and already
  used `sessionID` as `scope_id` for a missing non-workspace scope. Behavior
  was preserved: todo/reminder/pin share the one session-to-project resolver,
  while plan keeps its distinct legacy session-scope autofill. The requirement
  and capability map were corrected; no behavior change was made.

#### 3. Agent profiles and source resolution

- Commit `c82a49fd` (`refactor(selftools): delegate agent profile tools`).
- `AgentProfileTools` owns a five-operation store seam, the injected
  classifier and fallback, create/list/update, source resolution, and the one
  editability policy. The broad transport `Store` remains for unapproved
  domains.
- Existing editability/source tests passed, and the new injection test proves
  the supplied classifier wins over fallback. The focused race passed in
  132.289s; the full scoped normal suite, build, vet, and diff checks passed.

#### 4. Combined card and panel presentation

- Commit `894cd04a` (`refactor(selftools): delegate presentation tools`).
- `PresentationTools` owns the panel signal sink, plugin-panel lookup, trust
  resolver, `card_show`, panel open/close, mode signals, render-target policy,
  signal emission, and the sole `resolvePanelAccess` decision point. Direct
  policy tests now target the owner; transport smokes cover all four routed
  tool names. The API panel-sink path and same-turn card flush remain covered.
- Focused normal presentation/API tests passed. Focused race passed for
  `internal/selftools` in 182.953s and `internal/api` in 12.691s. The full
  scoped normal suite passed (`internal/selftools` 52.517s,
  `internal/api` 20.226s, `internal/mcpserver` 2.858s, `cmd/nanite` 5.179s),
  followed by passing `go build ./...`, `go vet ./...`, and
  `git diff --check`.

#### Implemented shape

- Four collaborators replace the eight approved raw dependency fields.
- `SelfToolsTransport`: 27 fields and 50 production receiver methods.
- `CallTool`: unchanged at 68 names in 66 clauses.
- Exactly one package-level project resolver and one presentation access gate.
- Two-stage construction remains intact: constructor owners are nil-safe and
  store-backed where required; the composition root replaces them with live
  runtime services.
- No fifth domain was extracted and no blanket Store migration began.
- Final non-race verification passed: `go build ./...`, `go vet ./...`,
  `go test ./...`, and `git diff --check`.
- Structural verification returned 50 production transport receiver methods,
  27 transport fields, 66 `CallTool` clauses / 68 names, one
  `resolvePanelAccess` definition, and one `resolveProjectIDFromSession`
  definition.
- Final isolated aggregate race attempt:
  `go test -race ./internal/selftools -timeout 20m -count=1` exited 1 after
  1200.458s. Go's timeout fired in `TestSelfToolsTransport_UnknownTool` while
  `newTestStore` → `store.New` → `Store.migrate` → goose `runMigrations` was
  applying the fresh 0x91 (145) migration fixture. No race detector report was
  emitted. This independently reproduced the migration-fixture timeout and is
  a qualified timeout, not a race PASS; all four boundary-focused race suites,
  the full scoped normal suites, and the final repository-wide non-race suite
  passed as recorded above.

## Review notes

- The messaging/work-tracking midpoint review passed, including direct routing,
  nil-safe construction, narrow dependencies, mutation broadcast cardinality,
  and the single shared project resolver.
- Source reconciliation corrected the task's original scope premise before it
  could become a behavior change: plans have no `project_id` and retain their
  legacy non-workspace `scope_id=sessionID` autofill, while todo/reminder/pin
  share session-to-project resolution. The implementation and tests preserve
  and document that distinction.
- The final production review passed all four substantive ownership moves and
  the single presentation trust gate. Its only initial closeout finding was
  missing task/map completion documentation; that documentation was added and
  the re-review passed. No fifth extraction or blanket Store split was added.
- The expressly approved selective-delegation balance is therefore complete:
  four cohesive owners now hold the chosen behavior and policy, while
  `SelfToolsTransport` remains the catalog/dispatch adapter at 27 fields and 50
  production receiver methods with all 68 tool names unchanged.
