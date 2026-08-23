# Audit Remediation — implementation

Implements the remediation program derived from
`docs/audits/2026-08-21-go-quality/REPORT.md` — a 13-package-cluster Go
quality/architecture audit run against commit `8feeee5c` (2026-08-21) — as
sequenced by `docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. A
sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/teams/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`,
`TASKS/loops/`, `TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`, and
`TASKS/code-mode/` — kept in its own top-level `TASKS/` subfolder for the same
reason those are.

**63 task files, 113 findings, 9 waves.** This is by a wide margin the largest
batch this process has run (previous maximum: `TASKS/skills/` at 12 tasks), and
it is the only one whose source of truth is an audit rather than a design doc.
Both facts change how it is dispatched — see "Dispatch model" below.

## Status

**Planning complete 2026-08-21. Awaiting operator sign-off. Not dispatched.**

This batch has no `docs/engineering/architecture/NN-*.md` design doc, and
deliberately so: its design input is a completed audit plus an advisor's
remediation guide, and writing a synthetic architecture doc restating them
would add a layer of paraphrase between a worker and the real evidence.
Per `docs/engineering/templates/01-architecture-doc-status-block.md`'s intent
— that a batch's approval state must be checkable rather than inferred — the
sign-off block lives here instead:

| | |
|---|---|
| **Design input** | `docs/audits/2026-08-21-go-quality/REPORT.md` (audit, 2026-08-21) + `docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md` (advisor guide, vendored into the repo by this planning pass) |
| **Task inventory** | Created by a dedicated task-creation pass (61 files), merged in `8258176e` |
| **Planned** | 2026-08-21 — this pass: sequencing, dependency ordering, parallelization, Wave 0 gate, architect-decision queue, prevention table |
| **Approved for implementation** | ☐ **Not yet.** Operator sign-off required before any dispatch. |
| **Blocking prerequisites** | (1) ~~The dev freeze (AD-24)~~ — **decided and in effect 2026-08-21**, see below. (2) Wave 0 complete, **including AD-01 through AD-04 decided**. (3) ~~AD-23~~ decided *accept* — evidence rescued and committed (`e02f52c9`). |

An Orchestrator reading this file must treat an unchecked approval box as a
hard stop, and confirm with the operator directly rather than inferring
approval from the batch's thoroughness.

### The development freeze (AD-24, decided 2026-08-21)

**ALL tasks in the repository are frozen** — every batch, every phase, every
`TASKS/` folder. Not scoped to audited packages, not scoped to this batch's
dependencies. **`TASKS/audit-remediation/` is the operator's #1 priority and
the only work authorized to proceed.**

- **Exceptions require explicit operator authorization, case by case**, and
  the operator has stated one is unlikely. An agent must never grant itself
  one. Size, low risk, "it's only docs," and "this batch was already planned"
  are not exceptions — if work seems to need one, stop and ask.
- **The operator is the gate for resuming.** Resumption is not automatic on
  any condition: not a wave boundary, not "all critical/high closed," not a
  green test run, not `TASKS/INDEX.md` showing a batch complete. There is no
  derived trigger.
- **In-flight work at the time of the freeze finishes**; nothing new starts.

Mirrored as a banner at the top of `TASKS/INDEX.md`, because the freeze
governs every batch tracked there — not just this one.

## Read before starting any task here

1. **`docs/audits/2026-08-21-go-quality/REPORT.md`** — the audit. Every task
   cites the specific `§8.N` section(s) it draws from; read those, not the
   whole report, unless you need the cross-cluster synthesis in `§9`.
2. **`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`** — the guide.
   Its §4 is this batch's wave structure, §5 the dependency-field scheme, §6
   the task-file content shape, §9 the architect-decision queue, §11 the
   planning guardrails. **Vendored into the repo by this planning pass** —
   it previously existed only at `~/dev/chrispian/inbox/…`, outside the
   repository and unreadable by a context-free session.
3. **`ARCHITECT-DECISIONS.md`** (this folder) — 24 decisions. **Do not dispatch
   a task whose `Gated on` decision is still `open`.** This is the mechanism
   that keeps a worker from silently answering a question the audit
   escalated on purpose.
4. **`00-revalidate-baseline/README.md`** (this folder) — the Wave 0 gate and
   why it exists. Nothing in `01/`–`13/` dispatches before it closes.
5. **`PREVENTION.md`** (this folder) — the guide's §7 output-format D. Read
   the headline finding at the top even if you skip the rest; it explains why
   the batch's most severe bug was invisible to tooling that was already
   running.
6. **`FINDING-INDEX.md`** (this folder) — the finding→task mapping, all 113,
   cross-validated programmatically.
7. **`docs/engineering/EXECUTION-PROCESS.md`** — the operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path).
   Both matter here: this batch runs more parallel worktrees than any before it.
8. **`internal/context/INVARIANTS.md`** + `internal/service/slot_invariants_test.go`
   — the established in-repo pattern for making an architectural rule
   enforceable. Several tasks should add invariants in this style rather than
   inventing a mechanism.

## Load-bearing corrections this planning session found — real findings, not assumed

Four, all verified against live state at planning time.

### 1. The audit's raw evidence was not in the repo — found, rescued 2026-08-21, not yet committed

`REPORT.md:8` states *"Raw tool output backing every finding below lives in
`raw/`."* **`docs/audits/2026-08-21-go-quality/raw/` does not exist on `main`.**
It exists only here:

```
.claude/worktrees/go-quality-audit/docs/audits/2026-08-21-go-quality/raw/   (8.0 MB, 29 files)
```

`git status` in that worktree reports the directory untracked (`??`), and
`git check-ignore -v` confirms why the logs never staged:

```
/Users/chrispian/.gitignore:11:*.log    docs/audits/2026-08-21-go-quality/raw/golangci-baseline.log
```

The merge commit `8258176e` brought `REPORT.md` and `findings.json` onto
`main`; the evidence they cite did not come with them. Roughly 8 MB of that is
directly cited by `REPORT.md` (`govulncheck.log`, `golangci-baseline.json`,
`deadcode.log`, `gosec.json`, `fanin-fanout.tsv`, …). It cannot be
regenerated — it was measured against a working tree that no longer exists.
**Resolved the same day.** AD-23 was decided *accept*: the evidence is now at
`docs/audits/2026-08-21-go-quality/raw/` (29 files, 8.0 MB) with a narrow
`.gitignore` negation at `.gitignore:88-91`, without which 21 of the 29 files
would still be silently skipped by the global `*.log`/`*.out` rules. All 14
`raw/` paths cited by `REPORT.md` and the task files resolve. **It is still
uncommitted** — the rescue is not complete until it is in git history.

### 2. The lint rule that would have caught the batch's most severe finding exists — and was scoped away from the package where the bug lived

`.golangci.yml:88-103` forbids `filepath.Join` with the message *"use
internal/pathsafe.ResolveUnder to prevent path traversal (Phase 1 Wave 1)"*.
`.golangci.yml:180-184` then silences it outside three paths:

```yaml
- text: 'ResolveUnder'
  linters: [forbidigo]
  path-except: '(internal/sandbox/|internal/mcp/|internal/service/install/)'
```

`GO-PLUGIN-002` (critical, unconfined path-traversal write) is a bare
`filepath.Join(cs.pluginsDir, entry.Name)` at `internal/api/catalog.go:301`.
`internal/api/` is not in that list. The rule was correct, present, and
configured not to look there. Full analysis in `PREVENTION.md`; the immediate
consequence is that **widening that one `path-except` is the highest
value-per-line change in the batch**, and `08/09`/`12/01` own it.

### 3. `GO-STORE-003` was already in the lint output and nobody saw it

`REPORT.md:693` attributes the high-severity `DeleteAgentByID` finding to
golangci's `nilerr` linter at `raw/golangci-baseline.log:6421`. The linter is
enabled in `.golangci.yml` today. It didn't gate because the fast hook runs
`golangci-lint run --new` (`lefthook.yml`) — changed code only, which
structurally cannot surface a pre-existing finding in untouched code. This is
the concrete, non-hypothetical argument for `12/01`, and it should be quoted
in that task rather than argued from first principles.

### 4. Two of the six production islands aren't flagged as needing a decision

`GO-MEM-002` (Hadron context gate) and `GO-MCPTOOL-003` (curated tool-knowledge
matcher) carry `requires_architect_decision: false` in `findings.json`, despite
the guide's §4 Wave 4 table listing all six islands as requiring an explicit
wire/defer/retire call. Both are in the decision queue anyway (AD-07, AD-11);
Wave 0 should correct the catalog. Verified by direct query over
`findings.json` — 44 of 113 findings carry the flag, and those two are not
among them.

## What this batch does NOT do

- **It does not re-audit.** The guide is explicit: *"Do this quickly; do not
  re-audit the whole repository."* A new defect found mid-batch goes to
  `TASKS/ESCALATIONS.md` as a new finding — **not** into `findings.json`,
  which is a frozen-schema catalog of *this* audit's output (guide §10:
  *"record new findings separately rather than rewriting baseline"*).
- **It does not refactor `Container`.** The audit found it wiring-only with two
  methods; 60 fields is not, by itself, a defect. `10/03` exists to record that
  as a decision rather than let it silently become a task.
- **It does not split `internal/store`.** Gravitational-package *review*, not
  mandatory split. The guide's guardrails forbid *"creat[ing] repository
  interfaces everywhere because Store has high fan-in."*
- **It does not delete islands on reachability evidence alone.** Six features
  are production-unreachable; that is an input to a wire/defer/retire decision
  (AD-06…AD-11), not a verdict. The guide's do-not list names *"delet[ing]
  islands without checking current intent/current source."*
- **It does not chase metrics.** No splitting a type to reduce field or method
  counts (`10/02` says this explicitly); no duplication work justified by LOC
  reduction; complexity findings get classified essential/accidental/mixed
  before anything is touched.
- **It does not require the historical lint backlog to reach zero.** `12/01`
  baselines history and blocks regressions. Demanding zero would stall the
  ratchet indefinitely — the guide calls this out directly.
- **It claims no migration numbers.** See "Migration numbering" below.
- **It does not touch the frontend.** All 113 findings are Go. `ui/` is
  untouched except where `12/01`'s gate configuration incidentally names it.

## Dispatch model — why this batch is not one kickoff

Every prior sibling batch was dispatched as a single Orchestrator session with
a single kickoff prompt. At 63 tasks this one cannot be: no Orchestrator has
held anything near that, and the failure mode (tracking drift, tasks silently
skipped, `INDEX.md` going stale mid-batch) is well-attested in this project's
own history.

Instead: **one batch, one `TASKS/INDEX.md` section, one `findings.json`
tracker — but eleven dispatch units**, each getting its own kickoff prompt
written by a kickoff-prompt author when its turn comes (per
`docs/engineering/templates/06-orchestrator-kickoff-template-sibling-batch.md`).
Unit sizes are 2–10 tasks, in line with what this process has actually
executed successfully.

| Unit | Wave | Tasks | Count | Dispatch precondition |
|---|---|---|---:|---|
| **W0** | 0 | `00/01`–`00/02` | 2 | Dev freeze in effect (AD-24 ✅). AD-23 decided (✅). **AD-01–AD-04 are resolved during this window** as an operator-owned third track — see below. |
| **W1** | 1 | `01/*`, `02/*`, `03/01`, `12/02` | 6 | W0 closed **and** AD-01–AD-04 all `decided`. |
| **W2a** | 2 | `04/*`, `05/01` | 6 | W0 closed. |
| **W2b** | 2 | `06/*`, `07/*` | 7 | W0 closed. AD-14, AD-17, AD-18 decided. |
| **W3** | 3 | `08/*` | 10 | W1 closed (`08/05`, `08/09` depend on it). AD-15, AD-16 decided. |
| **W4** | 4 | `09/*` | 6 | W2 closed. AD-06–AD-11 decided **after** `00/01` reports current reachability. |
| **W5** | 5 | `10/*` | 3 | W4 closed. AD-12, AD-13 decided. |
| **W6a** | 6 | `11/01,02,05,06,07,08,09,10,11` | 9 | W5 closed. AD-19, AD-20 decided. |
| **W6b** | 6 | `11/03,04,12,13,14,15,16` | 7 | W6a closed. |
| **W7** | 7 | `12/01`, `12/03` | 2 | W6 closed. AD-21 decided. |
| **W8** | 8 | `13/*` | 5 | W7 closed. AD-22 decided. |

**`12/02` (engineering standards docs) is deliberately pulled forward from
Wave 7 into Wave 1.** The guide files it under the quality-ratchet wave, but
it is doc-only, collides with nothing, and its content is already specified by
`PREVENTION.md`. Landing it first means the six named standards are citable by
every remediation task that follows, instead of being written down after the
work they were supposed to govern. This is a planning-session deviation from
the guide's ordering, made deliberately and recorded here.

## Task sequence

Task IDs are `<folder>/<file>` — e.g. `04/03` is
`04-container-reaper-lifecycle/03-investigate-internal-service-race-timeout.md`.
Folder-scoped numbering is retained rather than flattened to `01`–`63`,
because `findings.json`'s `task_file` field and every row of
`FINDING-INDEX.md` already address tasks by folder path; renumbering would
rewrite both trackers for no functional gain.

### Wave 0 — Revalidate the baseline (gates everything)

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `00/01` revalidate findings against HEAD | dev freeze | — | Sets all 113 dispositions. **Blocks the entire batch.** Must deliver an **interim critical/high report** before the full sweep finishes — AD-01–AD-04 are decided from it |
| `00/02` rescue evidence + refresh tool baseline | dev freeze (step 1: none) | AD-23 ✅ | Step 1 **done and committed** (`e02f52c9`, 2026-08-21) |
| *(operator track)* decide **AD-01–AD-04** | `00/01`'s interim report | — | Moved from Wave 1 by operator direction. Wave 1 does not dispatch until all four are `decided` |

**Why AD-01–AD-04 moved into Wave 0.** Nothing about them depends on Wave 0's
tooling output, so holding them to Wave 1 would stall the four
release-blocking tasks behind a revalidation that was never going to answer
them. But they *do* depend on Wave 0 revalidating their own findings —
`GO-PLUGIN-001/002/003` and `GO-SEC4-001/002/005/006` — because deciding the
sandbox's fail-open posture or the plugin-install convergence shape against
evidence that is 40 commits stale is precisely the mistake Wave 0 exists to
prevent. `00/01` already processes critical-then-high first; the added
requirement is that it *reports* that tranche immediately rather than holding
it. `GO-SEC4-005` (AD-03) is low severity and gets pulled forward out of
order because AD-03 needs it.

### Wave 1 — Release-blocking trust boundaries

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `01/01` unify plugin-install pipeline | W0 | AD-04 | 2 critical + 1 high. The batch's most severe task |
| `01/02` wire allow-unsigned-plugins setting | `01/01` | — | Sequencing only — same `SignatureVerifier` construction site |
| `02/01` sandbox fail-closed without bwrap | W0 | AD-01, AD-02 | 1 critical + 1 high |
| `02/02` macOS seatbelt read-boundary disclosure | W0 | AD-03 | Docs-only unless AD-03 says otherwise |
| `03/01` canonical slug→path validation | W0 | — | 2 high |
| `12/02` engineering standards docs | W0 | — | Pulled forward from Wave 7 — see above |

### Wave 2 — Correctness, lifecycle, concurrency

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `04/01` container constructor partial-failure cleanup | W0 | — | `NewContainer` only |
| `04/02` API test container shutdown leak | W0 | — | Test files only |
| `04/03` investigate `internal/service` race timeout | `04/02` | — | Needs a clean baseline first — hard dependency, not sequencing |
| `04/04` track untracked goroutine spawns | W0 | **AD-26** (Part B only) | ~18 `safego.Go` sites |
| `04/05` close untested service config functions | W0 | — | Test-only |
| `05/01` approve concurrency cap + queued cancel | W0 | — | `internal/subagent` only — fully independent |
| `06/01` `DeleteAgentByID` error swallowing | W0 | — | High. Already in `nilerr` output — see correction 3 |
| `06/02` store context/transaction gaps triage | ~~`06/01`~~ **none** | — | **Re-scoped by AD-14 (2026-08-22):** `GO-STORE-005` moved out to `06/03`; now just `GO-STORE-004` + `GO-STORE-006`. Architect gate lifted. |
| `06/04` cancellation safety for terminal writes | `06/03` | — | **Landed** `fe16e138`. `validated`. Fix task for `06/03` — 50 sites audited, 29 detached / 21 left. |
| `06/03` full `context.Context` propagation sweep | none technically | AD-14 | **Landed** `fe16e138` with companion fix `06/04`. `validated`: 371/371 methods take `ctx`, 0 non-context calls, `go test ./...` 0 FAIL, `go vet` at the expected 4 pre-existing findings. Out-of-wave, not part of Wave 2a/2b dispatch units — but no longer blocks them from proceeding. |
| `07/01` worktree orphan branch cleanup | W0 | — | `internal/worktree` only — independent |
| `07/02` `cmdServe` fatal cleanup bypass | W0 | AD-17 | Gates `07/05`, `08/07`, `11/10` (same file) |
| `07/03` bound background job registry growth | W0 | AD-18 | Doc-comment half needs no decision |
| `07/04` container shutdown idempotency guard | `04/01` | — | Same file as `04/01` |
| `07/05` MCP config silent decode errors | `07/02` | — | Same file as `07/02` |

### Wave 3 — Remaining security hardening

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `08/01` A2A webhook URL validation | W2 | — | SSRF; shares CIDR logic with `11/07` |
| `08/02` MCP dev grep symlink TOCTOU | W2 | — | |
| `08/03` triage remaining gosec G304 sites | `00/02` | — | Scope **is** the refreshed count |
| `08/04` permission default-mode write gap | W2 | AD-16 | |
| `08/05` secret-key heuristic hardening | `02/01` | — | Same file (`internal/sandbox/exec.go`) |
| `08/06` sandbox proxy header timeout | W2 | — | Independent |
| `08/07` server auth/bind/TLS posture | `07/02` | AD-15 | High. Same file as `07/02` |
| `08/08` dependency/toolchain vuln bumps | `00/02` | — | **Runs alone** — see parallelization |
| `08/09` autocomplete + artifact path hardening | `01/01` | AD-04, AD-27, AD-28 | Now 5 findings (`GO-API-001/002/003/008` + reassigned `GO-SEC4-007`) — the wave's biggest task |
| `08/10` API validation duplication + pagination bug | W2 | — | |

### Wave 4 — Production islands

All six gated on `00/01`'s reachability report — **decide after Wave 0, not
before.**

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `09/01` grounding memory recall | `04/01`, `07/04` | AD-06 | **Decided retire (2026-08-22)** — does not touch `container.go`; deletion reaches into `internal/selftools/self_tools_transport.go`/`self_tools_dispatch.go` instead |
| `09/02` Hadron context gate | `04/01`, `07/04` | AD-07 | **Decided retire (2026-08-22)** — does not touch `container.go`'s `sources` slice |
| `09/03` team semantic routing | W2 | AD-08 | Check `TASKS/teams/` for original intent |
| `09/04` tool builder / YAML architecture | W2 | AD-09 | **Outcome changes `13/01`'s scope** |
| `09/05` reasoning-augmented tool selection | W2 | AD-10 | |
| `09/06` curated tool-knowledge matcher | W2 | AD-11 | Self-contained |

### Wave 5 — Architectural concentration

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `10/01` `chatServiceImpl`/`generateResponse` decomposition | W4 | AD-12 | 2 high. **Exclusive lock on `internal/service`** |
| `10/02` `SelfToolsTransport` decomposition | `09/01` | AD-13 | Shares `self_tools_transport.go` with `09/01` |
| `10/03` `Container` + `internal/store` review note | — | AD-14 | No code changes. Can land any time |

### Wave 6a — Semantic divergence and migration drift

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `11/01` subagent completion vs. message wake policy | `10/01` | AD-19 | |
| `11/02` Harness-v1 vs. native durable-agent handlers | `03/01` | AD-19 | Shares `durable_agents.go` |
| `11/05` MCP result-processing tail | `09/04` | AD-19 | Shares `mcp/manager.go` |
| `11/06` provider streaming error divergence | W5 | AD-19 | May be legitimately independent — classify first |
| `11/07` SSRF CIDR denylist duplication | `08/01` | AD-19 | Classification only, no code |
| `11/08` `internal/config` naming collision | W5 | AD-20 | **Runs alone if AD-20 picks tree-wide rename** |
| `11/09` elicitation duplication + dead doc | W5 | AD-19 | Doc describes a type that doesn't exist |
| `11/10` envelope registry triplication | `07/02`, `07/05`, `08/07` | AD-19 | Shares `cmd/nanite/main.go` |
| `11/11` dispatch reflex double-evaluation | `10/01`, `10/02`, `09/01` | AD-19 | |

### Wave 6b — Mechanical and boilerplate duplication

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `11/03` `StructuredMessage` unwrap duplication | W6a | — | Independent files |
| `11/04` traffic-light calculation duplication | `10/01` | — | |
| `11/12` `DevServerName` constant duplication | `09/05` | — | Two-line fix |
| `11/13` store scan-loop duplication | `06/01`, `06/02` | — | ~20 files in `internal/store` |
| `11/14` adapter plugin boilerplate | W6a | — | Independent — 4 adapter packages |
| `11/15` API response boilerplate | `01/01`, `08/09`, `08/10` | — | Broad `internal/api` touch |
| `11/16` workflow/dispatch naming collisions | W6a | — | No code change |

### Wave 7 — Quality ratchet

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `12/01` full-repo scheduled lint gate | W6, `00/02` | AD-21 | Baseline from `00/02`'s frozen-HEAD numbers |
| `12/03` goroutine lint coverage gap | `04/04` | — | Fix `Makefile:74-81` — fixed package list, non-fatal `-` prefix |

### Wave 8 — Mechanical cleanup

| Task | Depends on | Gated on | Notes |
|---|---|---|---|
| `13/01` confirmed dead-code removal | `09/02`, `09/04`, `03/01`, `00/02` | — | Scope depends on island decisions |
| `13/02` stale comments and docs | W7 | — | Broad — `internal/store/*.go` (25+ files), `cmd/nanite/main.go` |
| `13/03` naming and formatting fixes | **everything** | AD-22 | **Absolutely last, alone** — see parallelization |
| `13/04` low-risk error-handling batch | W7 | — | Independent files |
| `13/05` low-risk hygiene + lock scope | `11/05`, `11/10`, `09/02` | — | |

## Parallelization plan

Cross-checked against each task's own `Touches` list for real file overlap —
not inferred from the dependency table. Worktree isolation
(`isolation: "worktree"`) for everything marked parallel.

**Wave 0** — `00/01` ∥ `00/02`. Different tooling; the only shared write is
`findings.json`, and `00/02` is explicitly forbidden from touching it.

**Wave 1** — `01/01` ∥ `02/01` ∥ `03/01` ∥ `02/02` ∥ `12/02`, then `01/02`.
`01/01` and `03/01` both touch `internal/api/` but disjoint files
(`catalog.go`/`plugins.go` vs. `agents.go`/`durable_agents.go`). `01/02` is
sequenced after `01/01` — *file-disjoint but still sequenced*, because both
edit the same `SignatureVerifier` construction site and doing them in one pass
avoids two conflicting edits to the same wiring.

**Wave 2a** — `04/01` ∥ `04/02` ∥ `04/04` ∥ `04/05` ∥ `05/01`, then `04/03`.
`04/01` and `04/04` are both in `internal/service` but different files
(`container.go` vs. `delegation.go`/`events_composite.go`); safe in worktrees,
merge sequentially. `04/03` is a **hard** dependency on `04/02`, not
sequencing: investigating a race timeout against a baseline with known
un-shutdown test containers measures the wrong thing.

**Wave 2b** — `06/01` → `06/02`; `07/01` ∥ `07/03` ∥ `07/02`; `07/02` → `07/05`;
`04/01` → `07/04`. Note `07/04` is a Wave 2b task with a Wave 2a dependency —
if the two units run back to back this is free; if they run concurrently,
`07/04` waits.

**`06/03` runs alone, outside this or any other unit's parallel set.** It
rewrites every signature in `internal/store` (67 non-test files) plus call
sites across the 32 importing packages — a half-swept package does not
compile, so it cannot run concurrently with `06/01`, `06/02`, `11/13`,
`13/01`, `13/02`, or anything else with an open worktree. It must start from
a clean `main` and land in one merge before any of those resume.

**Wave 3** — mostly parallel: `08/01` ∥ `08/02` ∥ `08/04` ∥ `08/06` ∥ `08/10`.
Sequenced: `08/05` after `02/01` (same file), `08/07` after `07/02` (same
file), `08/09` after `01/01` (same function). **`08/08` (dependency bumps)
runs alone in a quiet window** — it rewrites `go.mod`/`go.sum`, which every
other open worktree also carries, so a parallel run guarantees conflicts in
every branch. The guide's "independent dependency upgrades can run in
parallel" does not survive contact with worktree-based dispatch.

**Wave 4** — **all six run fully parallel.** The sequencing note that used to
live here (`09/01`/`09/02` run sequentially against each other because both
edit `container.go` if wired) is obsolete as of AD-06/AD-07 (decided
2026-08-22, both **retire**): neither touches `container.go` — verified,
zero real grounding-wiring references and the one string match on "hadron"
in that file is an unrelated comment about a different app's binary name.
All six tasks are file-disjoint.

**Wave 5** — effectively serial. `10/01` takes an **exclusive lock on
`internal/service`**: it is a multi-phase extraction of an 84-method type,
performed one phase at a time with behavior re-verified after each, and any
concurrent edit to that package invalidates its characterization tests.
`10/02` can run alongside only because `internal/selftools` is disjoint —
but it must follow `09/01`, which edits the same transport's fields. `10/03`
writes no code and can land whenever.

**Wave 6a** — `11/06` ∥ `11/07` ∥ `11/09` are independent. `11/08` **runs
alone if AD-20 chooses the tree-wide rename** — its own `Touches` warns it may
reach *"every caller of `config.Config` and `config.AppConfig` across the
tree."* Decide AD-20 before scheduling the wave, not during it.

**Wave 6b** — `11/03` ∥ `11/12` ∥ `11/14` ∥ `11/16` freely. `11/13`
(`internal/store`, ~20 files) and `11/15` (`internal/api`, 6 files) each want
their package to themselves.

**Wave 8 — the ordering that actually matters.** `13/03` must be the **last
thing that lands in the batch, with no other branch open.** If AD-22 chooses
the full repo-wide `gofmt` sweep, it rewrites 122 files (count as of the
audited commit; `00/02` refreshes it) and will conflict with every
outstanding worktree in the repository. `13/02` has the same property in
milder form — it touches 25+ files in `internal/store` plus `cmd/nanite/main.go`
— and should follow everything except `13/03`. `13/01`, `13/04`, `13/05` can
run in parallel with each other beforehand.

## Migration numbering

**This batch claims no migration numbers and needs none.** All 113 findings
are Go-level: security boundaries, lifecycle, duplication, dead code, lint
posture. No task introduces, alters, or drops a schema object. The highest
migration on disk at planning time is `137`
(`internal/store/migrations/137_*_drop_agent_skills.sql`), landed by
`TASKS/skills/`.

Two things a later reader should still check:

- **`135` is unclaimed.** Per the root `HANDOFF.md`, `TASKS/plugin-system/`
  provisionally claimed `135` and was never dispatched, so `136`/`137` landed
  around it. That gap is not this batch's to fill.
- **Re-list before assuming.** If a Wave 0 revalidation or an architect
  decision (`07/03`'s retention policy is the one plausible candidate — a
  persist-and-evict design would need storage) turns a task into a
  schema-touching one, `ls internal/store/migrations/` **at that moment** and
  claim the next free number then. Do not reserve one now against a
  possibility.

## Tracking progress — `findings.json`

`TASKS/audit-remediation/findings.json` is the **working tracking view**:
`docs/audits/2026-08-21-go-quality/findings.json` plus three fields per
finding. That second path is the **pristine** audit snapshot and stays
byte-identical forever, so a future re-audit has a stable base to diff
against. Editing it is the single easiest mistake to make here.

- **`task_file`** — path of the task addressing this finding. Pre-populated
  from `FINDING-INDEX.md`. If a planner splits/merges/renames a task, update
  this field **and** `FINDING-INDEX.md` in the same edit; they are
  cross-validated and drift between them is silent.
- **`disposition`** — one of `allowed_dispositions`. **All 113 currently read
  `remediate`, which is a placeholder, not a judgment.** Wave 0 (`00/01`)
  replaces every one with a real decision plus a `revalidation_note`. Filling
  this field in *is* producing the guide's §7 output-format C disposition
  table — not a separate deliverable.
- **`task_status`** — one of `allowed_task_statuses`. **Must mirror the
  `**Status:**` line of the finding's own task file.** A task addressing 3
  findings means updating 3 entries when its status changes, even though they
  resolve in one PR.

`disposition` and `task_status` are independent axes: `defer` +
`not-started` (an island the architect chose not to wire) and `remediate` +
`done` (fixed and closed) are both coherent. Don't conflate "what should
happen" with "how far along it is."

## Escalations logged during this planning pass

See `TASKS/ESCALATIONS.md`, entry **"Audit Remediation — planning pass
(2026-08-21)"**. It records the four load-bearing corrections above, with the
evidence-rescue item (AD-23) flagged as time-sensitive.
