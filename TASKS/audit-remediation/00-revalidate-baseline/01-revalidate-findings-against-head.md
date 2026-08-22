# Revalidate all 113 audit findings against frozen HEAD and set real dispositions

**Phase:** Audit remediation — Wave 0 (revalidate the baseline)
**Status:** reviewed
**Depends on:** none — but **hard-gated on the dev freeze** (see Context). Do not start while other batches are still landing code.
**Blocks:** every task in `01/` through `13/`. This is the batch's dispatch gate. Also blocks **AD-01 through AD-04**, which the operator decides against this task's interim critical/high report — see "Interim report required" under What to do.
**Parallelizable with:** `00/02` (different tooling; see that task's Non-goals for the `findings.json` merge protocol).
**Touches:** `TASKS/audit-remediation/findings.json` (the working tracking view — the *only* file this task writes structurally), and the `## Context` sections of any task file under `01/`–`13/` found to carry a stale pointer or superseded premise. **No production code changes whatsoever.**
**requires_architect_decision:** false — this task *populates* the architect-decision queue with real evidence, it does not resolve any decision in it.
**requires_security_review:** false
**requires_regression_test:** false — this task adds no behavior.

## Context

### Findings addressed

All 113. Not by fixing them — by deciding, for each, whether it still needs
fixing. This task produces the remediation guide's §7 output-format **C
(finding disposition table)** in its `findings.json` form.

### Root cause

The audit ran against commit `8feeee5c`. The task-creation pass that produced
`TASKS/audit-remediation/` deliberately did **not** revalidate against current
source — it says so explicitly in its own README ("Audited commit vs. current
state — Wave 0, not yet done"). So every one of the 113 findings carries
`disposition: remediate` as a *placeholder*, and every task file's `file:line`
citations are as of `8feeee5c`, not as of HEAD.

Measured drift at planning time (2026-08-21):

```
$ git diff --stat 8feeee5c..HEAD
 156 files changed, 24891 insertions(+), 539 deletions(-)
$ git log --oneline 8feeee5c..HEAD | wc -l
      40
```

That drift is concentrated in audited packages — `internal/store/skills.go`
alone gained 275 lines, and migrations `136`/`137` landed. `internal/service/`
(17 findings), `internal/api/` (11 findings), and `internal/store/` (10
findings) are the three most-audited packages *and* three of the most-churned.

### Current behavior

`TASKS/audit-remediation/findings.json` — `catalog_kind:
"working-remediation-view"`, `finding_count: 113`, `commit: "8feeee5c"`. Every
entry has the three tracking fields the task-creation pass added:

```json
"task_file": "TASKS/audit-remediation/04-container-reaper-lifecycle/01-....md",
"disposition": "remediate",
"task_status": "not-started"
```

Verified at planning time: **113 of 113** entries have
`disposition: "remediate"`. Severity distribution: 3 critical, 8 high,
33 medium, 45 low, 24 informational. **44 of 113** carry
`requires_architect_decision: true`.

### Desired invariant

**Every one of the 113 findings carries a disposition that a human actually
decided, backed by a look at current source — and no task in this batch is
dispatched against a finding that is already fixed.**

Concretely, after this task:

1. Zero findings remain on the placeholder value *by default*. `remediate` is
   still a legitimate outcome — it just has to be a decision, evidenced in the
   entry's `revalidation_note`, not a leftover.
2. The 3 critical and 8 high findings each carry an explicit, individually
   evidenced re-confirmation that they are still open (or a specific commit
   citation showing they were fixed). These are what justify the dev freeze;
   they get the most scrutiny.
3. Any task file whose premise evaporated is marked so at the top of its own
   file, not silently left looking dispatchable.

### Scope

- Read current source for each finding's cited `files`/`symbols`.
- Write `disposition` + a new `revalidation_note` field per finding in
  `TASKS/audit-remediation/findings.json`.
- Amend task-file `## Context` sections where citations are stale.
- Append the `## Outcome (<DATE>)` section to
  `00-revalidate-baseline/README.md`.

### Out of scope — and why

- **Re-auditing.** The guide is explicit: *"Do this quickly; do not re-audit
  the whole repository."* If you notice a new defect that the audit missed,
  record it in `TASKS/ESCALATIONS.md` as a new finding — do **not** add it to
  `findings.json`, which is a frozen-schema catalog of *this* audit's output
  (guide §10: *"record new findings separately rather than rewriting
  baseline"*).
- **Fixing anything.** Even a one-line fix you're certain of. It belongs to
  its wave's task, with that task's regression test.
- **Resolving architect decisions.** See `ARCHITECT-DECISIONS.md`.

## What to do

### 0. Confirm the freeze is real, first

Do not start otherwise — a moving baseline invalidates this entire task's
output.

```bash
git log --oneline -5
git status --short
git worktree list
```

Record the exact HEAD SHA you revalidate against in the `## Outcome` section
and in a new top-level `revalidated_at_commit` field in `findings.json`. If
HEAD moves mid-task, note it; findings revalidated before the move may need a
spot re-check.

### 1. Work finding-by-finding, hardest first

Process in this order — it front-loads the decisions that justify the freeze:

1. **3 critical** — `GO-PLUGIN-001`, `GO-PLUGIN-002`, `GO-SEC4-001`
2. **8 high** — `GO-AGENT-001`, `GO-AGENT-002`, `GO-PLUGIN-003`,
   `GO-RUNTIME-002`, `GO-SEC4-002`, `GO-STORE-003`, `GO-SVCEXEC-001`,
   `GO-SVCEXEC-002`
3. **33 medium**
4. **45 low + 24 informational** — these can go faster; many are
   comment/naming findings where "does the string still exist" is the whole
   check. Task `00/02` refreshes the count-bearing ones mechanically — don't
   hand-count what that task is measuring.

> **Interim report required — do not batch this to the end.** AD-01 through
> AD-04 (the Linux sandbox fail-open pair, the macOS seatbelt disclosure, and
> the plugin-install convergence shape) are **Wave 0 decisions** as of the
> operator's 2026-08-21 direction, and the operator decides them against *this
> task's* revalidated evidence rather than audit-era evidence that is 40
> commits stale. The findings behind them — `GO-PLUGIN-001/002/003` and
> `GO-SEC4-001/002/005/006` — are all in the critical/high tranche processed
> first, precisely so this can happen.
>
> **Report the critical and high results the moment they exist**, as a short
> written summary (disposition + evidence per finding), and do not wait for
> the remaining 100+ findings. Wave 1 is blocked on those four decisions, and
> those four decisions are blocked on this interim report. Note that
> `GO-SEC4-005` (AD-03) is a *low*-severity finding and therefore falls outside
> the critical/high tranche — pull it forward and revalidate it alongside them
> anyway, out of severity order, because AD-03 needs it.

For each finding, read the cited file(s) at the cited symbol(s) and answer:

- Does the code the finding describes still exist, unchanged in the relevant
  respect?
- If it changed — was it *fixed* (disposition `already-resolved`, cite the
  commit), *restructured but still defective* (`remediate`, correct the
  pointer), or *deleted/replaced* (`superseded`, say by what)?
- Is the finding's premise actually right? The audit's own
  `false_positive_considerations` field is a starting hypothesis, not a
  verdict — several findings ship with one. Where you conclude
  `false-positive`, the note must engage with the audit's specific evidence
  strings, not just assert disagreement.

### 2. Write the disposition

Set `disposition` to one of `allowed_dispositions` (already declared at the
top of `findings.json`):

`remediate | already-resolved | accepted-risk | false-positive | superseded |
defer | retire-feature | needs-architect-decision | needs-more-evidence`

Add a **new** field alongside it:

```json
"revalidation_note": "<one or two sentences: what you read, what you concluded, and the file:line or commit that supports it>"
```

Guidance on the values that are easy to misuse:

- **`needs-architect-decision`** — use it when the *disposition itself*
  depends on a pending call in `ARCHITECT-DECISIONS.md` (most of `09/`'s
  islands: whether `GO-MEM-001` is `retire-feature` or `remediate` literally
  is the architect's decision). Do not use it merely because the *fix*
  needs architect input while the finding is plainly still open — that's
  `remediate` with the task's own `requires_architect_decision` flag.
- **`already-resolved`** — requires a commit SHA or a current-source citation
  proving it. "Looks fine now" is not a disposition.
- **`accepted-risk`** — is an operator/architect call, not a worker's. If you
  believe a finding should be accepted, set `needs-architect-decision` and add
  a row to `ARCHITECT-DECISIONS.md`.
- **`needs-more-evidence`** — legitimate and expected for a handful. Say
  precisely what evidence would settle it.

### 3. Correct the task files that revalidation invalidates

Three cases, three different treatments:

- **Stale line numbers only** — amend the citation in the task's `## Context`
  and move on. Expected to be common and is not itself notable.
- **Premise partially evaporated** (2 of 3 findings fixed, 1 open) — narrow
  the task's scope in place, and note in its Context that Wave 0 narrowed it
  and why.
- **Premise fully evaporated** — add a banner immediately under the
  `**Status:**` line:

  ```markdown
  > **CLOSED BY WAVE 0 REVALIDATION (<DATE>).** <Which findings, resolved how,
  > citing commit/source.> This task is not dispatchable. Left in place rather
  > than deleted so `findings.json`'s `task_file` mapping stays intact.
  ```

  Set its `**Status:**` to `done` and the corresponding findings'
  `task_status` to `done` with `disposition: already-resolved`. **Do not
  delete the file** — `findings.json` and `FINDING-INDEX.md` both point at it
  by path.

### 4. Keep the two trackers consistent

`FINDING-INDEX.md` maps finding → task file. If (and only if) you split,
merge, or rename a task file, update `FINDING-INDEX.md` and `findings.json`'s
`task_file` in the same edit. They are cross-validated; drift between them is
the failure mode this batch's README explicitly warns about.

### 5. Write the outcome summary

Append `## Outcome (<DATE>)` to `00-revalidate-baseline/README.md`: the HEAD
SHA revalidated against, a disposition-count table, the explicit
critical/high re-confirmation, a list of every task file closed or narrowed,
and any finding you had to leave at `needs-more-evidence` with what would
settle it.

## Done means

- `findings.json` has `revalidated_at_commit` set to a real SHA, and all 113
  entries carry a `revalidation_note` plus a `disposition` that is a decision
  rather than the inherited placeholder.
- A programmatic check passes: no entry is missing `revalidation_note`; every
  `disposition` is in `allowed_dispositions`; every `task_file` path resolves
  to a file that exists on disk.

  ```bash
  python3 - <<'EOF'
  import json, os, collections
  d = json.load(open('TASKS/audit-remediation/findings.json'))
  f = d['findings']
  assert d.get('revalidated_at_commit'), 'revalidated_at_commit not set'
  missing = [x['id'] for x in f if not x.get('revalidation_note')]
  bad = [x['id'] for x in f if x['disposition'] not in d['allowed_dispositions']]
  gone = [x['task_file'] for x in f if not os.path.exists(x['task_file'])]
  print('no note:', missing); print('bad disposition:', bad); print('missing task file:', sorted(set(gone)))
  print(collections.Counter(x['disposition'] for x in f))
  assert not (missing or bad or gone)
  EOF
  ```

- Each of the 3 critical and 8 high findings has a `revalidation_note` citing
  a specific current-source location or a specific fixing commit — not a
  generic "still present."
- **The interim critical/high report was delivered to the operator before the
  full sweep finished**, and included `GO-SEC4-005` pulled forward out of
  severity order. AD-01 through AD-04 cannot be decided without it, and Wave 1
  cannot dispatch without those decisions.
- Every task file closed or narrowed by this pass says so in its own text; no
  task file remains that looks dispatchable but isn't.
- `00-revalidate-baseline/README.md` has a real `## Outcome (<DATE>)` section.
- `docs/audits/2026-08-21-go-quality/findings.json` is **unchanged** — verify
  with `git status` before finishing.

## Work log

**2026-08-22 — Part 1 of 2 (interim critical/high report), by dispatch instruction.**

Confirmed freeze/baseline first: `git log`, `git status --short` (clean), `git worktree list`.
HEAD revalidated against: `531dcfccbbf870fcbe80b269546bb8b28622a8f0` (merge commit for
`00/02`, 2026-08-22 08:44:27 -0500). Set `findings.json`'s new top-level
`revalidated_at_commit` field to this SHA.

Per the dispatching agent's instruction, this session processed only the 12-finding
critical/high/pulled-forward-low tranche (3 critical, 8 high, `GO-SEC4-005` pulled forward
for AD-03) and stopped to report — not the full 113. For every one of the 12, read the
current source at the finding's cited file(s)/symbol(s) (not the audit-era citation blindly)
before setting `disposition` + `revalidation_note`. Used `git diff 8feeee5c..HEAD --
<paths>` per package to confirm exactly which cited files had zero commits since the audit
(most had none) vs. which had drift (only `internal/store/agents.go` and
`internal/plugin/agent_profiles.go`, both via one unrelated skills-index commit, `e1ba2ac6`).

Dispositions set: 9 `remediate` (`GO-PLUGIN-001/002/003`, `GO-SEC4-001`, `GO-AGENT-001/002`,
`GO-STORE-003`, `GO-SVCEXEC-001/002`), 3 `needs-architect-decision` (`GO-SEC4-002`,
`GO-SEC4-005`, `GO-RUNTIME-002`). All 3 critical and 8 high findings reconfirmed still open
against current source — none were already-resolved, false-positive, or superseded. See the
final report delivered to the operator (relayed via the dispatching agent) for the full
per-finding evidence; not restated here to avoid drift between the two.

**Disposition-vs-`needs-architect-decision` reasoning, since this is the tranche's easiest
value to misuse:** used `remediate` where the *bug itself* is unambiguous even though its
concrete fix shape is architect-gated (`GO-PLUGIN-001/002/003` — AD-04 itself says "the
direction is not in doubt"; `GO-SEC4-001` — the task's own desired invariant is unconditional,
AD-01's two options are both fixes, not an accept-as-is path). Used `needs-architect-decision`
where the task file's own text frames the *disposition itself* as open, not just the
implementation: `GO-SEC4-002` (task file: "should each end up either genuinely fixed, or
explicitly and visibly documented as an accepted reduced-guarantee mode"), `GO-SEC4-005`
(task file's two live questions are "does the tradeoff still hold" and "is disclosure
adequate" — AD-03's job), `GO-RUNTIME-002` (task file's own "What to do" lists explicit
accepted-risk as one of 4 live options for the architect, not a worker's call).

**Task-file correction (stale line numbers only, case 1 of 3 from "Correct the task files"):**
`06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md` cited
`internal/store/agents.go:1211-1214`/`1211-1221` for `DeleteAgentByID`'s doc comment/function.
Current source has it at `1213-1216`/`1213-1223` — a +2-line shift caused by an unrelated
doc-comment edit earlier in the same file (commit `e1ba2ac6`, the skills-index-redesign task,
which also touched `internal/plugin/agent_profiles.go`'s doc comments in a pure 1:1 word swap
— `agent_skills` -> `agent_known_skills` — with no line-count change, so that file's citations
needed no correction). Updated all three citations in the task file (lines 28, 32, 105) with an
explicit revalidation note; the function's own text and the fix's scope are unaffected. No
other task file among the 12 needed a Context correction — every other citation (`01/01`,
`02/01`, `02/02`, `03/01`, `08/07`, `10/01`) was verified exact-match against current source,
because the underlying files are byte-identical to the audited commit (confirmed via
`git diff 8feeee5c..HEAD -- <path>` returning empty for each).

**No production code touched.** No `## Outcome` section written yet — genuinely incomplete;
the remaining ~101 findings, the task-file-correction sweep across the rest of `01/`-`13/`,
the `## Outcome` section, and the final programmatic validation check are Part 2, to follow a
separate dispatch. `docs/audits/2026-08-21-go-quality/findings.json` (pristine) confirmed
untouched via `git status`/`git diff --stat` before finishing this part.

**2026-08-22 — Part 2 of 2 (remaining ~101 findings, task-file sweep, Outcome, final check),
by dispatch instruction after the interim report was relayed to the operator.** Explicitly
directed to proceed without waiting for AD-01–AD-04, since nothing in the remaining work
depends on them.

Processed all 33 medium, then 45 low, then 24 informational findings (severity order, per
the task file). Methodology: for each finding, cross-referenced its cited `files` against
`git diff --name-only 8feeee5c..HEAD` — 88 of the 101 had zero cited files touched since the
audit (byte-identical), letting me confirm the specific claimed symbol/behavior directly
against unchanged source with high confidence; the other 13 (mostly `internal/service/
container.go`, `cmd/nanite/main.go`, and a few `internal/api/` files touched by the
skills/loops batches) got a closer read, including diffing the specific hunks to confirm
whether the finding's flagged code itself moved vs. merely shifted line numbers. Read the
actual cited symbol/behavior for every one of the 101 (not just the file-level diff) before
setting a disposition — greps and targeted `Read` calls are cited per-finding in each
`revalidation_note`.

**Disposition-value discipline, applied consistently across all 101 (same rule stated in
Part 1, now generalized):** `remediate` when the underlying defect is unambiguous and only
the *implementation shape* is architect-gated (e.g. `GO-MCPTOOL-006`'s decomposition
boundaries parallel to Part 1's `GO-SVCEXEC-001/002` treatment; `GO-RUNTIME-001`'s
signature-vs-cleanup-hook choice parallel to `GO-SEC4-001`'s AD-01 treatment).
`needs-architect-decision` reserved for findings where the disposition itself — not just the
fix — is what's undecided: all six production islands (`GO-MEM-001/002`, `GO-SVCEXEC-003`,
`GO-MCPTOOL-001/002/003`), the gravitational-package review trio gated by AD-14
(`GO-DEP-002`, `GO-STORE-001`, `GO-STORE-005`), the AD-19 share-vs-parity-test cluster in
`11-semantic-duplication-migration-drift/` (used each task file's own `Gated on:` field as
the ground-truth signal — pulled via `grep -rn "Gated on:" TASKS/audit-remediation/*/*.md`
across the whole batch rather than re-deriving per finding — since some folders separate
per-sub-finding `requires_architect_decision` overrides from the task file's overall gate,
e.g. `06/02` explicitly marks `GO-STORE-004`/`GO-STORE-006` as *not* needing architect input
despite the task file's own overall `Gated on: AD-14`, which applies only to the
`GO-STORE-005` sub-section), the config-naming (AD-20) and lint/gofmt-timing (AD-21/AD-22)
questions, and a handful of findings whose own `false_positive_considerations` or task-file
prose explicitly frame the fix-or-accept call as unresolved (`GO-SEC4-002/003`,
`GO-API-001/003`, `GO-RUNTIME-002/004`, `GO-STORE-008/009`, `GO-CHAT-008`, `GO-MEM-004/005`).

**Catalog-gap correction, per explicit dispatch instruction:** set `GO-MEM-002` and
`GO-MCPTOOL-003` to `needs-architect-decision` (bringing `findings.json` in line with
`ARCHITECT-DECISIONS.md`'s "gap worth naming" and the guide's blanket six-island
wire/defer/retire requirement) — re-confirmed both islands are still fully unwired in
production against current source first.

**`defer`/`false-positive`/`needs-more-evidence` used where the allowed-value list's more
common options didn't fit an honest read of the finding:** 12 `defer` (mostly informational
observations whose own audit text already concludes "no action required" or "optional,
judgment call," with no live defect); 4 `false-positive`, each engaging with the audit's own
evidence string rather than just asserting disagreement (`GO-DEP-001`/`GO-STORE-002`: the
audit's own text concludes `Container`/`*Store`'s size is not a defect; `GO-MEM-008`: the
audit's own text names this an example of its "reported dead != remove" guardrail working
*correctly*; `GO-RUNTIME-008`: `agent.Boot`'s complexity judged essential and correctly
handled); 1 `needs-more-evidence` (`GO-STORE-006` — the task file's own text says the audit
could not complete a caller-concurrency trace within budget; noted precisely what would
settle it).

**Task-file `## Context` corrections (stale line numbers only — no premise evaporated, none
closed, none narrowed):** 6 task files corrected in place, all citing `internal/service/
container.go` or `cmd/nanite/main.go` line numbers that shifted (by +2 to +62 lines) from
unrelated additive work landed by the intervening `TASKS/skills/` and `TASKS/loops/`
batches — the flagged code itself is unchanged in every case, confirmed by direct diff
against `8feeee5c`:
- `06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md` (done in Part 1)
- `04-container-reaper-lifecycle/01-fix-container-constructor-partial-failure-cleanup.md` (`GO-LIFE-001`: reaper-start/struct-capture/error-return citations, plus the quoted `go vet` output flagged as reflecting audit-era line numbers)
- `04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md` (`GO-SVCCORE-002`: wake-reactor precedent comment citation, `1133-1143` → `1184-1194`; re-confirmed the file's own already-present drift note on `agent_deps.go`'s zero-`safego.Go` count is still accurate)
- `07-runtime-correctness-lifecycle/02-fix-cmdserve-fatal-cleanup-bypass.md` (`GO-RUNTIME-001`: all 7 `slogx.Fatal` call sites re-grepped and re-cited; count is still 7, not the audit's approximate "5," confirming the task file's own already-noted discrepancy)
- `07-runtime-correctness-lifecycle/04-container-shutdown-idempotency-guard.md` (`GO-RUNTIME-005`: `Container.Shutdown`'s start/internals in `container.go` and its one call site in `main.go`)
- `08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md` (`GO-API-004`: not a line-number fix — recorded that the in-repo comment's cited blocker ("producers not all landed") is now factually resolved; all three producers — HTTP handler, reflex hook, self-tool — confirmed present in current source)

No task file's premise evaporated, fully or partially — every one of the 113 findings was
reconfirmed still open in some form. No task got the `CLOSED BY WAVE 0 REVALIDATION` banner;
none had its scope narrowed. `FINDING-INDEX.md` and `findings.json`'s `task_file` field
needed no changes (no task file split/merged/renamed).

**Outcome section written** in `00-revalidate-baseline/README.md` (`## Outcome (2026-08-22)`):
HEAD SHA, full disposition-count table across all 113, the critical/high re-confirmation, the
two catalog-gap corrections, the `needs-architect-decision`/`defer`/`false-positive`/
`needs-more-evidence` rationale, the 6 corrected task files, and a pointer to `00/02`'s
refreshed tool baseline for count-bearing findings.

**Final programmatic check — actually run, not claimed:**

```
$ python3 -c "..." # the exact snippet from this task file's 'Done means' section
no note: []
bad disposition: []
missing task file: []
Counter({'remediate': 64, 'needs-architect-decision': 32, 'defer': 12, 'false-positive': 4, 'needs-more-evidence': 1})
ALL CHECKS PASSED
```

`docs/audits/2026-08-21-go-quality/findings.json` (pristine) reconfirmed byte-for-byte
untouched (`git status --short` / `git diff --stat` both empty for that path) before
finishing. All "Done means" criteria met; `**Status:**` set to `implemented` above.

## Review notes

**PASS (2026-08-22)** — fresh reviewer, no shared context with the implementing worker.
Independently re-derived from source rather than re-reading Work Log claims and agreeing
(per this project's `TASKS/skills/` review-FAIL precedent). Ran the "Done means" programmatic
check directly against current `findings.json` — passed exactly as reported. Read current
source directly for all 3 critical + 8 high + `GO-SEC4-005` — every cited file/line/code
shape in each `revalidation_note` matches the repo exactly (`internal/api/catalog.go:301`/
`365-371`, `internal/sandbox/os_linux.go:64-71`/`132-148`, `internal/agent/managed_files.go:87-95`,
`internal/service/agent_config.go:176-197`, `internal/store/agents.go:1213-1223`,
`internal/server/auth.go:15-22`/`server.go:163-174`). Spot-checked 14 further findings across
`needs-architect-decision`/`false-positive` dispositions (`GO-MEM-002`, `GO-MCPTOOL-003`,
`GO-DEP-001`, `GO-STORE-002`, `GO-MEM-008`, `GO-RUNTIME-008`, `GO-MEM-001`, `GO-STORE-001`,
`GO-STORE-005`, `GO-INFRA-001`, `GO-SVCEXEC-003`, `GO-API-007`, `GO-MCPTOOL-001`, `GO-STORE-006`)
against source directly — all held up. Verified all 6 task-file line-number corrections
byte-for-byte against current source. Confirmed `docs/audits/2026-08-21-go-quality/findings.json`
(pristine) byte-identical to pre-Wave-0 state.

**One non-blocking observation, not fixed here:** 4 findings beyond the two named catalog-gap
corrections (`GO-SEC4-007`, `GO-MCPTOOL-012`, `GO-CHAT-007`, `GO-CHAT-001`) carry
`disposition: needs-architect-decision` while `requires_architect_decision: false` in
`findings.json` — same shape as the `GO-MEM-002`/`GO-MCPTOOL-003` gap this task explicitly
corrected, but not named anywhere as a gap. Not a functional bug — nothing dispatches off this
JSON boolean; the actual dispatch-gating mechanism is each task file's own `Gated on:` header,
confirmed correct in all 4 cases. Worth a trivial catalog-consistency cleanup whenever
`findings.json` is next touched; not blocking Wave 0's closure.
