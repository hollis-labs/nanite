# Wave 5 handoff — for the Wave 6 orchestrator

> **Reopened 2026-08-23.** This handoff describes the completed evidence beat
> only and is not the current Wave 5 closeout. The operator subsequently
> approved AD-12 and AD-13 and expressly directed their implementation in this
> wave through tasks `10/04` and `10/05`. Replace the integration point and
> status sections after those tasks pass fresh review.

**Audience: a fresh session with zero memory of Wave 5.** The immutable Wave 5
integration point is `fb6527ab`. All three Wave 5 tasks are `reviewed`, but this
was a decomposition-planning and decision wave, not a production-extraction
wave: no production refactor shipped.

The statements below that AD-12 and AD-13 remain open are historical. Both are
now decided in `ARCHITECT-DECISIONS.md`; the repository-wide freeze under
AD-24 remains in force.

---

## 1. What actually shipped

| Task | Reviewed outcome | Production impact |
|---|---|---|
| `10/01` | Added production-door characterization coverage for `generateResponse` and a reviewed responsibility/phase map: 52 fields owned exactly once, 87 production receiver methods reconciled exactly once, and six proposed phases. | One new test file, `internal/service/chat_generate_characterization_test.go`; no production `internal/service` code changed and no extraction task was created. |
| `10/02` | Added the reviewed `SelfToolsTransport` capability map: 31 fields, 82 production receiver methods, and all 68 `CallTool` names reconciled. It recommends selective delegation based on cohesion and coupling. Rechecked `ToolClient` as 20 methods across three files and closed that informational finding as no action. | Documentation/tracking only; no `internal/selftools` or `internal/toolclient` production or test code changed and no extraction task was created. |
| `10/03` | Revalidated the concentration findings and recorded the operator-approved balance: no `Container` decomposition; no blanket `Store` split or interface pass absent named consumer pain; selective `Host` work only through evidence from the existing `GO-PLUGIN-004` scope, with an all-category migration sweep rejected. | Documentation/tracking only; no `Container`, Store, or plugin Host code changed. The disposition is also recorded in the AD-14 supplement and Wave 8 task `13/05`. |

The `10/01` characterization cases reach the real production door:
`Dispatcher.Run` → `chatRunnerAdapter` → `generateResponse`. They cover plain,
single-tool, multi-tool, mid-stream provider error, context-overflow recovery,
pre-loop compaction, `message.sending` cancellation, `tool.executing`
cancellation, and forced rate-budget recovery. The tool-cancellation correction
proved execution is skipped while blocked tool events/results continue through
the provider turn and final persistence.

## 2. Coverage and map results

`10/01` recorded these like-for-like focused coverage changes:

| Surface | Before | After |
|---|---:|---:|
| Package | 62.3% | 65.6% |
| `generateResponse` | 36.7% | 53.7% |
| `recoverFromContextOverflow` | 23.4% | 78.7% |
| `enforceBudgetOrCompact` | 5.9% | 67.6% |
| Provider-error ranges | 1/111 statements (0.9%) | 28/111 (25.2%) |
| Compaction-recovery ranges | 15/111 (13.5%) | 59/111 (53.2%) |
| Plugin-cancel ranges | 1/26 (3.8%) | 26/26 (100.0%) |

The reviewed map is
`TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md`.
It corrects the authored 84-method premise to 87, separates tool-partition
state from the runtime-session owner, includes construction-only wiring
residue, and preserves a recognizable outer state machine. Its six proposed
phases are architect-review input only. Any approved future extraction must be
incremental, keep the characterization suite unchanged, and rerun behavior,
race, and audit complexity checks after every individual extraction.

The reviewed `SelfToolsTransport` map is
`docs/engineering/selftoolstransport-capability-map.md`. It reconciles 31
fields, 82 methods across 19 files, and 68 names in 66 `CallTool` clauses. Its
AD-13 recommendation is selective delegation: keep the transport as the MCP
catalog/dispatch adapter; move only cohesive policy/state owners, and do not
wrap tiny stateless handlers or handlers already delegated to narrow services.
The four candidate boundaries are recommendations, not authorized work.

`10/03` replaced the audit-era concentration metrics with current-source
counts: `Container` is 64 fields and four production receiver methods; Store is
372 production receiver methods across 62 of 66 production Go files, with 32
exact root-package importers and 61 literal `*store.Store` occurrences across
25 non-test `internal/service` files; plugin `Host` is 38 fields and 123
production receiver methods across nine files. These counts support the
reviewed disposition record; they are not authorization to decompose by
metric.

## 3. Decisions still required

- **AD-12 is open.** The operator must accept, reject, or revise the six
  `generateResponse` phases and the proposed `chatServiceImpl` capability
  boundaries. No follow-on extraction task exists or may be drafted first.
- **AD-13 is open.** The operator must accept, reject, or revise selective
  delegation and its candidate ordering. A fresh review PASS did not decide
  this architecture question.
- **AD-14 is decided and supplemented.** `10/03` is fully dispositioned; it
  does not create a new refactor task.

This is the required order: map → operator decision → separately scoped
extraction. Wave 5 completed the first step only for AD-12 and AD-13.

## 4. Verification before trusting the dependency

Run from a clean checkout at `fb6527ab`.

### Scope and map reconciliation

```bash
git status --short

# The only code-tree change in the named concentration surfaces is the new test.
git diff --name-only 3942f3c4..fb6527ab -- \
  internal/service internal/selftools internal/toolclient internal/plugin internal/store

# Current source inventories used by the reviewed maps: expect 52, 87, 31, 82.
sed -n '/^type chatServiceImpl struct {/,/^}/p' internal/service/chat.go \
  | grep -cE '^[[:space:]]+[A-Za-z][A-Za-z0-9]*[[:space:]]'
grep -RhoE '^func \([^)]*\*chatServiceImpl\) [A-Za-z0-9_]+' internal/service \
  --include='*.go' --exclude='*_test.go' | sed -E 's/.*\) //' | sort -u | wc -l
sed -n '/^type SelfToolsTransport struct {/,/^}/p' \
  internal/selftools/self_tools_transport.go \
  | grep -cE '^[[:space:]]+[A-Za-z][A-Za-z0-9]*[[:space:]]'
grep -RhoE '^func \([^)]*\*SelfToolsTransport\) [A-Za-z0-9_]+' internal/selftools \
  --include='*.go' --exclude='*_test.go' | sed -E 's/.*\) //' | sort -u | wc -l

# The canonical chat field table must contain 52 unique names and no duplicates.
sed -n '/^### Canonical exactly-once field ownership$/,/^Arithmetic reconciliation:/p' \
  TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md \
  | grep -oE '`[A-Za-z][A-Za-z0-9]*`' | tr -d '`' | sort -u | wc -l
sed -n '/^### Canonical exactly-once field ownership$/,/^Arithmetic reconciliation:/p' \
  TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md \
  | grep -oE '`[A-Za-z][A-Za-z0-9]*`' | tr -d '`' | sort | uniq -d

# Expect six phase headings. Confirm the method formula ends at 87.
grep -c '^### Phase [1-6] ' \
  TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md
grep -A5 '^## Complete 87-method reconciliation$' \
  TASKS/audit-remediation/10-architectural-concentration/01-chatserviceimpl-responsibility-map.md

# Expect 66 clauses; 65 literal names plus three constant-backed names = 68.
sed -n '/switch name {/,/default:/p' internal/selftools/self_tools_transport.go \
  | grep -c '^[[:space:]]*case '
sed -n '/switch name {/,/default:/p' internal/selftools/self_tools_transport.go \
  | grep -oE '"[a-z][a-z0-9_]*"' | sort -u | wc -l
sed -n '/switch name {/,/default:/p' internal/selftools/self_tools_transport.go \
  | grep '^[[:space:]]*case ' | grep -v 'case "'
```

The first scope command should print only
`internal/service/chat_generate_characterization_test.go`. For the dispatch
count, the final command must show `agentSourceResolveToolName`,
`taskUpdateReportToolName`, and `scheduleCreateToolName`; adding those three to
the 65 literal names yields 68.

### Focused behavior and final repository gates

The exact focused command recorded by `10/01` is:

```bash
go test ./internal/service/... -run 'GenerateResponse|Characterization' -v
```

For an exact rerun of the nine new focused cases, including the same scope
under the race detector:

```bash
go test ./internal/service/... \
  -run '^(TestGenerateResponseCharacterization_.*|TestRecoverFromContextOverflow_RateBudgetForcedCompactionSucceeds)$' \
  -count=1 -v
go test -race ./internal/service/... \
  -run '^(TestGenerateResponseCharacterization_.*|TestRecoverFromContextOverflow_RateBudgetForcedCompactionSucceeds)$' \
  -count=1 -v

go build ./...
go vet ./...
go test ./... -count=1
```

At the final integration point, the root build, vet, and full non-race test
suite passed; the task-focused race run also passed. A full
`go test -race ./internal/service/...` did **not** pass: it reached the known
10-minute SQLite migration timeout while
`TestResolveProvider_StoredProviderID_UsesRuntimeProviderType` was opening and
migrating SQLite. It emitted no race report before timing out. Keep that exact
qualification; do not upgrade it to a race PASS.

The pre-existing `driveBootSession` send-on-closed-channel flake recurred once
during a coverage run; an identical retry passed. This is not attributed to
Wave 5 and is already durably logged from the Skills batch.

### Tracker, findings, and decision state

```bash
sed -n '/### Wave 5/,/### Wave 6a/p' TASKS/INDEX.md
jq -r '.findings[] | select(
  .id=="GO-SVCEXEC-001" or .id=="GO-SVCEXEC-002" or
  .id=="GO-MCPTOOL-006" or .id=="GO-MCPTOOL-007" or
  .id=="GO-DEP-001" or .id=="GO-DEP-002" or
  .id=="GO-STORE-001" or .id=="GO-STORE-002" or
  .id=="GO-PLUGIN-006") | [.id,.task_status,.disposition] | @tsv' \
  TASKS/audit-remediation/findings.json
sed -n '/^### AD-12 /,/^## Wave 6 decisions/p' \
  TASKS/audit-remediation/ARCHITECT-DECISIONS.md
```

Expect all three task rows and all nine findings to be `reviewed`; expect
AD-12 and AD-13 to remain `open`. The accepted dispositions differ by finding
and should not be flattened into “all remediated.”

## 5. Durable findings already in `TASKS/ESCALATIONS.md`

Reference these entries rather than restating them in later handoffs:

- **“Wave 5 `10/01` corrected the ChatService and StreamManager inventory
  premises”** — the 52/87 inventory, corrected runtime boundary, and
  construction-only wiring residue.
- **“Wave 5 `10/02` found stale comments for a retired chat-slot learning
  consumer”** — the three comments left for a future authorized cleanup.
- **“Wave 5 `10/03` corrected concentration metrics and a retired
  Store-interface example”** — current counts, retired grounding example, and
  the approved `Container`/Store/Host balance.
- **“Wave 8's three mechanical-cleanup tasks carry six
  `requires_architect_decision` items with no queue entry”** — six missing
  decision records that must be resolved or explicitly waived before Wave 8's
  kickoff.
- The Skills-batch task `10` PASS entry's **pre-existing
  `driveBootSession` send-on-closed-channel race** — relevant because it
  recurred once during Wave 5 verification.

No Wave 5 durable finding is intentionally held only in this handoff.

## 6. Wave 5 state

| Status | Count |
|---|---:|
| Reviewed | 3 |
| In progress | 0 |
| Blocked | 0 |

The wave's task and finding reviews are closed. Its two architecture decisions
are not.
