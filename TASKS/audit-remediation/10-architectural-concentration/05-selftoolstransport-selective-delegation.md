# `SelfToolsTransport` — implement the four approved selective delegations

**Phase:** Audit remediation — Wave 5 reopened implementation beat
**Status:** not-started
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
and project-scope autofill remains identical across todo, plan, reminder, and
pin paths.

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

- [ ] Four concrete collaborators own the approved behavior and dependencies.
- [ ] No moved handler remains a `SelfToolsTransport` forwarding wrapper.
- [ ] `SelfToolsTransport` has 27 fields: the 31-field baseline minus eight
      moved dependencies plus four collaborators.
- [ ] It has 50 production receiver methods: the 82-method baseline minus the
      32 methods moved by this task.
- [ ] All 66 `CallTool` clauses and 68 tool names remain present and routed.
- [ ] Messaging and work-tracking coverage described above is added before or
      with the corresponding extraction.
- [ ] The presentation trust/access gate and the work project resolver each
      have one implementation.
- [ ] No behavior, schema, tool name, response, catalog, or availability
      contract changes.
- [ ] No unapproved domain is extracted and no blanket Store split is begun.
- [ ] The capability map and Work Log reflect the implemented source.

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

Not started.
