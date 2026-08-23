# Wave 5 handoff — for the Wave 6 orchestrator

**Audience: a fresh session with zero memory of Wave 5.** Wave 5 is closed. All
five tasks are `reviewed`; none is in progress or blocked. The
pre-implementation baseline is `92315edf`; the reopened implementation and
central review integration spans `d61751c2..7304e7be`, followed only by the
final closeout-documentation commit.

Wave 5 was not only an evidence/planning wave. It first produced the reviewed
maps and dispositions in `10/01`–`10/03`; the operator then expressly approved
AD-12 and AD-13 on 2026-08-23 and directed both production implementations to
land in the same wave as `10/04` and `10/05`.

---

## 1. What actually shipped

| Task | Final outcome | Production impact |
|---|---|---|
| `10/01` | Added production-door `generateResponse` characterization and a reviewed 52-field/87-method responsibility map with six action boundaries. | Tests and architecture evidence only. The first review found duplicate field ownership and missing real `tool.executing` cancellation coverage; correction `f77076a9` fixed both before PASS. |
| `10/02` | Reconciled `SelfToolsTransport` at 31 fields, 82 receiver methods, and 68 names/66 clauses; recommended selective delegation. Recounted `ToolClient` at 20 methods across three files and found no useful split. | Documentation only. Fresh review caught a false claim that chat-slot assembly consumed `RecallToolLearnings`; correction `07ca5958` established `callToolDescribe` as its sole production consumer before PASS. |
| `10/03` | Recounted and dispositioned `Container`, Store, and plugin `Host` against current source. | No application code. The operator approved no `Container` decomposition, no blanket Store split/interface pass, and only pain-driven Host sub-registry work through the existing `GO-PLUGIN-004` boundary. |
| `10/04` | Implemented AD-12 as six private actions around a visible `generateResponse` coordinator. | Production service refactor plus additive characterization. No action calls another; the coordinator retains loop routing, retry decrements, root lifecycle, and cleanup. |
| `10/05` | Implemented AD-13 as four substantive self-tool owners while retaining one MCP catalog/dispatch adapter. | Production selftools/main wiring plus focused tests. No fifth domain or blanket Store migration was included. |

The five task rows in `TASKS/INDEX.md` are all `reviewed`. The nine findings
mapped to the first three tasks are also `reviewed`, but their dispositions are
intentionally different: remediation for the chat concentration and approved
selftools boundary; accepted risk or false-positive for the metric-only
Container/Store cases; selective deferral for Host; and no useful ToolClient
split.

## 2. Operator decisions and why

### AD-12 — six-action chat pipeline

The operator expressly approved the reviewed action/pipeline shape. The outer
workflow remains readable in `generateResponse`; each action owns one phase's
logic and returns an explicit directive. This reduces accidental complexity
without scattering the essential provider/tool loop, cancellation, retry, and
terminal-cleanup semantics across callbacks or six service objects. A wholesale
`chatServiceImpl` split and full rewrite were rejected.

The six implemented actions are:

1. `prepareTurn`
2. `initializeRun`
3. `requestProviderIteration`
4. `consumeProviderIteration`
5. `settleToolTurn`
6. `finalizeRun`

### AD-13 — selective selftools delegation

The operator expressly approved four cohesive boundaries rather than a uniform
owner per tool domain. `SelfToolsTransport` remains the only static catalog and
name dispatcher. The approved owners are:

1. `MessagingTools` — messaging and messaging-session handoff;
2. `WorkTrackingTools` — todo/plan persistence and post-mutation broadcast;
3. `AgentProfileTools` — agent CRUD, classification/editability, and source
   resolution; and
4. `PresentationTools` — card/panel rendering, signals, and the shared trust
   gate.

The reasoning was ownership and policy cohesion, not count reduction. Tiny
stateless adapters, already-delegated handlers, and domains crossing
load-bearing seams remain on the transport. Further delegation requires a
named coupling, ownership, or testability problem.

### AD-14 — no blanket `Container`/Store/Host refactor

The approved balance remains binding: `Container` is a composition root, Store
retains its file-per-domain database-handle shape, and consumer-owned narrow
interfaces are introduced only for a named consumer with demonstrated pain.
Plugin `Host` may split a named sub-registry only if `GO-PLUGIN-004`/UnloadPlugin
work supplies concrete ownership, teardown, locking, or testability evidence.

## 3. Final production shape

### Chat execution

`generateResponse` remains the visible coordinator in
`internal/service/chat_generate.go`. Its cognitive/cyclop/gocyclo complexity
moved monotonically from `458/228/225` to `19/18/18`; maintainability moved
from `0` to `27`. The six actions live in
`internal/service/chat_generation_actions.go` and remain bounded by their
phase rather than recreating the old monolith. `chatServiceImpl`'s broader
52-field/87-method inventory and runtime-session ownership were not split.

The coordinator still owns all `continue`, `break`, retry decrement, and
terminal routing; root span and deferred cleanup; stream and presence cleanup;
and final PTY disposition. `providerAttempt` makes provider cancel/span closure
idempotent across request and consume paths. The extraction preserves
stream/tool ordering, delayed-delta recovery behavior, envelope/filter/routed
broadcast order, persistence before `stream_end`, and terminal scheduling.

### Self-tools

`SelfToolsTransport` now has exactly 27 fields and 50 production receiver
methods. Its switch still contains all 68 names in 66 clauses, and the static
catalog and response behavior are unchanged. The four owners contain 31
receiver methods in total: messaging 9, work tracking 10, agent profiles 5,
and presentation 7. The thirty-second moved policy is the package-level
`resolveProjectIDFromSession` function.

There is exactly one project resolver and one `resolvePanelAccess` trust gate.
Construction remains two-stage: `NewSelfToolsTransport(store)` installs
nil-safe/store-backed owners for the local MCP path, and `cmd/nanite/main.go`
replaces them with live runtime dependencies.

One source premise was corrected during `10/05`: todo, reminder, and pin
project autofill share the session-to-project resolver, but plans do not have a
`project_id`. Plans retain their legacy rule: a missing non-workspace
`scope_id` is filled from the current `sessionID`. No behavior was changed to
force the original, false “identical across all four” premise.

## 4. Integrated commits and fresh review

Branch-local SHAs remain in the task Work Logs. The authoritative equivalents
on current `main` are:

| Boundary | Integrated commits |
|---|---|
| `10/01` characterization/map plus review correction | `4fc5d460`, `f77076a9` |
| `10/02` capability map plus caller-map correction | `70e20a2b`, `07ca5958` |
| `10/03` disposition record | `97b048b2` |
| Original evidence-beat review close | `fb6527ab` |
| Reopen point / pre-implementation baseline | `92315edf` |
| Reopened implementation dispatch | `d61751c2` |
| AD-12 six-action pipeline | `1d79d7c3..cbf8554f` |
| AD-13 four selective delegations | `25689cb5..126c2a2a` |
| Final task/finding review sync | `7304e7be` |

AD-12 landed prepare, initialize, settle, finalize, consume, and request as
separate reviewed moves. Midpoint review caught one observable-schema leak:
the mutable Go expression `run.tools` had escaped into telemetry/detail/reason
literals. The correction restored every observable literal to `tools`, kept
`run.tools` only as a Go expression, and added a production-door assertion.
Final fresh review passed the complete six-action pipeline.

AD-13's four code boundaries passed focused fresh review. Its first final
review then failed only the acceptance record because the task and capability
map still described the pre-extraction state. `126c2a2a` updated status, Work
Log, ownership/count reconciliation, source correction, and timeout record;
focused re-review passed that correction. `7304e7be` then marked both
implementation tasks and their findings reviewed on `main`.

## 5. Verification and exact race qualification

The merged tree passed:

```bash
go build ./...
go vet ./...
go test ./... -count=1
```

The AD-12 and AD-13 focused normal suites passed. Their focused race suites
also passed. These focused results do not convert either aggregate race timeout
into a pass.

The final service aggregate command was:

```bash
go test -race ./internal/service/... -timeout 20m -count=1
```

It exited 1 after **1200.838s** with `test timed out after 20m0s` while
`TestDurableAgentStartCreatesOrReusesSession` was in SQLite-backed test-store
setup. It emitted no `DATA RACE` report. This result is a **TIMEOUT, not a
PASS**.

The final selftools aggregate command was:

```bash
go test -race ./internal/selftools -timeout 20m -count=1
```

It exited 1 after **1200.458s** while
`TestSelfToolsTransport_UnknownTool` was in `newTestStore` → `store.New` →
`Store.migrate` → goose `runMigrations`, applying the fresh 0x91 (145)
migration fixture. It emitted no race-detector report. This result is also a
**TIMEOUT, not a PASS**.

## 6. Independent checks before trusting the dependency

Run from a clean checkout of `7304e7be` or a descendant that intentionally
supersedes it:

```bash
git status --short
git merge-base --is-ancestor 7304e7be HEAD
sed -n '/### Wave 5/,/### Wave 6a/p' TASKS/INDEX.md

# Six named actions plus the one visible coordinator.
grep -n -E 'func \(s \*chatServiceImpl\) (generateResponse|prepareTurn|initializeRun|requestProviderIteration|consumeProviderIteration|settleToolTurn|finalizeRun)' \
  internal/service/chat_generate.go internal/service/chat_generation_actions.go

# Expect 27 fields, 50 transport receiver methods, 66 clauses, and 68 names.
sed -n '/^type SelfToolsTransport struct {/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | grep -cE '^[[:space:]]+[A-Za-z][A-Za-z0-9]*[[:space:]]'
git grep -h -E '^func \([^)]*\*SelfToolsTransport\)' -- \
  'internal/selftools/*.go' ':!internal/selftools/*_test.go' | wc -l
sed -n '/func (st \*SelfToolsTransport) CallTool/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | grep -c '^[[:space:]]*case '
sed -n '/func (st \*SelfToolsTransport) CallTool/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | sed -n 's/^[[:space:]]*case //p' \
  | awk -F',' '{n+=NF} END {print n}'

go build ./...
go vet ./...
go test ./... -count=1
```

Do not describe the two 20-minute aggregate race results as passes unless a
future run independently completes with exit 0.

## 7. Durable carried-forward record

The following remain relevant; reference the named
`TASKS/ESCALATIONS.md` entries instead of rediscovering them:

- **“Wave 5 `10/01` corrected the ChatService and StreamManager inventory
  premises”** — retain the 52/87 inventory and the observation that `commands`,
  `dbPath`, and `adapterRegistry` are construction-only wiring residue. That
  observation alone is not deletion authorization.
- **“Wave 5 `10/02` found stale comments for a retired chat-slot learning
  consumer”** — three source-comment blocks still imply a chat-slot consumer
  that no longer exists. A future stale-comment task must explicitly add those
  files to scope.
- **“Wave 5 `10/03` corrected concentration metrics and a retired
  Store-interface example”** — preserve the pain-driven Container/Store/Host
  rule and use `GO-PLUGIN-004` as the Host evidence boundary.
- **“Wave 8's three mechanical-cleanup tasks carry six
  `requires_architect_decision` items with no queue entry”** — resolve or
  explicitly waive those six decision records before Wave 8 kickoff.
- The Skills task `10` review entry records a pre-existing
  `driveBootSession` send-on-closed-channel race. It recurred once during Wave
  5 verification and passed on retry; Wave 5 did not fix it.

A central decision record is inconsistent and should be reconciled by the
next tracking-authorized session: `ARCHITECT-DECISIONS.md` contains both a
decided AD-20 (“rename both types by
  role”) and a later stale duplicate open AD-20 entry. Do not infer AD-20's
  state from the duplicate heading; reconcile the decision record before
  dispatching `11/08`.

## 8. Wave 6 dependency state

Wave 5's dependency is satisfied. In particular, `11/06`, `11/08`, and
`11/09` no longer wait on “Wave 5 complete,” and `11/01`/`11/04`/`11/11` have
their named `10/01`/`10/02` dependencies.

That does **not** make all of Wave 6a immediately dispatchable. AD-19 remains
open and gates the semantic-divergence tasks; its classification table must be
produced and the operator must decide shared implementation versus parity
tests. Reconcile the duplicate AD-20 record before treating `11/08` as ready.
Wave 6b tasks that depend on Wave 6a remain sequenced behind it.

## 9. Final Wave 5 state

| Status | Count |
|---|---:|
| Reviewed | 5 |
| In progress | 0 |
| Blocked | 0 |

AD-12 and AD-13 are decided and implemented. AD-14's reviewed no-blanket-
refactor disposition remains in force.
