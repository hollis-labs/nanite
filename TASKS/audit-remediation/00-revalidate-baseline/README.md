# 00 — Revalidate the baseline (Wave 0)

**This folder gates the entire batch.** No task in `01/` through `13/` may be
dispatched to a worker until both tasks here are `reviewed` **and** AD-01
through AD-04 are `decided` (see "The third track" below).

**The repo-wide development freeze has been lifted** (AD-24, decided
2026-08-21, lifted by the operator 2026-08-25). While it was in force, all
tasks in all batches were frozen and `TASKS/audit-remediation/` was the
operator's #1 priority and the only authorized work. It blocks nothing now —
see the banner at the top of `TASKS/INDEX.md`, and its **"What changed during
the freeze"** section.

The Go quality audit was run against commit `8feeee5c`. Development did not
stop. As of this batch's planning pass (2026-08-21), `git diff --stat
8feeee5c..HEAD` reports **40 commits, 156 files changed, +24,891/-539 lines** —
and two more batches were still landing code when this folder was first
written. **Those finished and the freeze took effect** (AD-24) —
specifically so this revalidation ran against a baseline that stayed put. That
window has closed: the freeze was lifted 2026-08-25 and `main` moves again, so
anyone re-running this work now must re-establish a stable baseline by some
other means and confirm it directly (`git log`, `git status`,
`git worktree list`) before starting. A moving baseline invalidates the whole
task.

That drift is not incidental. It lands squarely on packages the audit found
defects in: `internal/store/` (+275 lines in `skills.go` alone, two new
migrations `136`/`137`), `internal/service/`, `internal/api/`. Several of the
113 findings are plausibly already fixed, already moot, or now pointing at
line numbers that have moved. **Every finding in `findings.json` currently
carries the placeholder `disposition: remediate`, which is an artifact of the
task-creation pass, not a judgment anyone made.**

Per the remediation guide's Wave 0 (`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`
§4) this is explicitly a *quick* pass — revalidate, don't re-audit:

> Do this quickly; do not re-audit the whole repository.

## Why this is two tasks, not one

The two halves have genuinely different methods, different tooling, and
different failure modes, and conflating them is how a "quick revalidation"
turns into a second audit:

- **`01-revalidate-findings-against-head.md`** — the *human/agent judgment*
  half. Read current source for each of the 113 findings, decide a real
  `disposition`, correct stale `file:line` pointers in task files. Read-only
  against production code; writes only to `findings.json` and task-file
  Context sections.
- **`02-refresh-tool-baseline-at-frozen-head.md`** — the *mechanical* half.
  Re-run the audit's own tool configuration (`audit-golangci.yml`, `gosec`,
  `govulncheck`, `deadcode`, `gofmt -l`) at the frozen HEAD and diff the
  counts against the audit's recorded baseline. Several findings
  (`GO-HYG-001`'s 122 gofmt failures, `GO-SEC-003`'s G304 site list,
  `GO-SEC-001`'s vulnerable dependencies, the confirmed-dead-code list behind
  `13/01`) are *count-bearing* — their task files quote numbers that are
  already stale and will mislead a worker who trusts them.

Task `02` can run in parallel with `01` (different tooling, no shared writes
except the final `findings.json` merge — see `02`'s Non-goals). Both must
complete before Wave 1.

## The third track: AD-01 through AD-04 are decided during Wave 0

**Operator direction, 2026-08-21.** These four architect decisions were
originally filed as Wave 1 work. They are now resolved *inside the Wave 0
window*, as an operator-owned track running alongside the two tasks above:

| Decision | Question | Backing findings |
|---|---|---|
| **AD-01** | Linux sandbox: fail closed without `bwrap`, or visible opt-in? | `GO-SEC4-001` (critical), `GO-SEC4-002` (high), `GO-SEC4-006` |
| **AD-02** | Linux network allowlist enforcement level | `GO-SEC4-002` (high) |
| **AD-03** | macOS seatbelt read boundary: disclose, narrow, or accept | `GO-SEC4-005` (low) |
| **AD-04** | Plugin-install convergence: concrete integration shape | `GO-PLUGIN-001/002` (critical), `GO-PLUGIN-003` (high) |

The ordering that makes this work:

```
00/01 revalidates critical + high (+ GO-SEC4-005, pulled forward)
        ↓  interim report to the operator — NOT held until the full sweep ends
operator decides AD-01 … AD-04 against revalidated evidence
        ↓
rest of Wave 0 completes
        ↓
Wave 1 becomes dispatchable
```

Two reasons this beats leaving them in Wave 1. First, nothing about these
decisions depends on Wave 0's *tooling* output, so holding them would stall
the four release-blocking tasks behind a revalidation that was never going to
answer them. Second, they very much *do* depend on Wave 0's revalidation of
their own findings — deciding the sandbox's fail-open posture against
audit-era evidence that is 40 commits stale is exactly the mistake the guide's
Wave 0 exists to prevent.

`GO-SEC4-005` is low severity and so falls outside `00/01`'s critical/high
tranche. It is pulled forward out of severity order anyway, because AD-03
needs it. `00/01`'s instructions say so explicitly.

AD-05 (provision a real signing key for the default catalog source) stays a
Wave 1 follow-up and gates nothing.

## What comes out of this folder

1. **`findings.json` with real dispositions** — all 113 entries moved off the
   `remediate` placeholder onto one of `allowed_dispositions`. This *is* the
   remediation guide's §7 output-format **C (finding disposition table)**; it
   is not a separate deliverable.
2. **A written revalidation summary** appended to this README as an
   `## Outcome (<DATE>)` section — headline counts (how many already-resolved,
   how many confirmed-open, how many need more evidence), and specifically an
   explicit re-confirmation that the **3 critical and 8 high** findings are
   still open, since those are what justify the dev freeze.
3. **Corrected task files** wherever revalidation found a stale pointer,
   a superseded premise, or a task whose whole scope evaporated.
4. **AD-01 through AD-04 decided** and written into `ARCHITECT-DECISIONS.md`
   with their reasoning — see the decision track above. Wave 1 does not
   dispatch until all four are `decided`.
5. **A refreshed tool baseline** under `docs/audits/2026-08-21-go-quality/raw/`
   (new files, not overwriting the audit's originals).

## Hard rule — do not edit the pristine catalog

`docs/audits/2026-08-21-go-quality/findings.json` is the frozen audit
snapshot. It stays byte-identical forever so a future re-audit has something
stable to diff against. All mutation happens in
`TASKS/audit-remediation/findings.json`, the working tracking view. This is
stated in the batch README and repeated here because it is the single easiest
thing for a well-meaning worker to get wrong.

## Outcome (2026-08-22)

**Revalidated against HEAD `531dcfccbbf870fcbe80b269546bb8b28622a8f0`** (the merge
commit landing `00/02`'s tool-baseline refresh, 2026-08-22 08:44:27 -0500).
Freeze confirmed clean at both dispatch points (`git status --short` empty,
worktree branched exactly from that commit). All 113 findings in
`TASKS/audit-remediation/findings.json` now carry a `disposition` that is a
decision plus a `revalidation_note` citing what was read and concluded — the
placeholder `remediate` is gone. `findings.json`'s new top-level
`revalidated_at_commit` field records the SHA above.

Dispatched in two parts per the operator's interim-report requirement (see
`01-revalidate-findings-against-head.md`'s Work Log for the full split):
Part 1 delivered the 3 critical + 8 high + `GO-SEC4-005` (pulled forward for
AD-03) tranche as an interim report so AD-01–AD-04 could be decided without
waiting on the full sweep; Part 2 (this pass) completed the remaining 33
medium + 45 low + 24 informational findings.

### Disposition counts — all 113

| Severity | remediate | needs-architect-decision | defer | false-positive | needs-more-evidence | Total |
|---|---:|---:|---:|---:|---:|---:|
| critical | 3 | 0 | 0 | 0 | 0 | **3** |
| high | 6 | 2 | 0 | 0 | 0 | **8** |
| medium | 20 | 13 | 0 | 0 | 0 | **33** |
| low | 31 | 11 | 2 | 0 | 1 | **45** |
| informational | 4 | 6 | 10 | 4 | 0 | **24** |
| **Total** | **64** | **32** | **12** | **4** | **1** | **113** |

### Critical/high re-confirmation (justifies the dev freeze)

All 3 critical and all 8 high findings were re-confirmed **still open** against
current source — none were already-resolved, false-positive, or superseded.
For 9 of the 12, the cited files are byte-identical to the audited commit
`8feeee5c` (`git diff 8feeee5c..HEAD -- <path>` empty); the remaining 3
(`GO-STORE-003`, and the two touching `internal/service/container.go`-adjacent
files indirectly) had only cosmetic line-number drift from unrelated
intervening work, with the flagged code itself unchanged. Full per-finding
evidence was delivered to the operator as the interim report ahead of
AD-01–AD-04 (see the dispatching session's relay); dispositions: 9
`remediate` (`GO-PLUGIN-001/002/003`, `GO-SEC4-001`, `GO-AGENT-001/002`,
`GO-STORE-003`, `GO-SVCEXEC-001/002`), 3 `needs-architect-decision`
(`GO-SEC4-002`, `GO-SEC4-005`, `GO-RUNTIME-002`).

### The two catalog gaps corrected

Per `ARCHITECT-DECISIONS.md`'s "gap worth naming" and `TASKS/ESCALATIONS.md`'s
planning-pass correction: `GO-MEM-002` and `GO-MCPTOOL-003` are now set to
`needs-architect-decision` (were previously missing `requires_architect_decision: true`
in `findings.json` despite being two of the six AD-06..AD-11 production
islands). Both re-confirmed still fully unwired against current source.

### `needs-architect-decision` disposition rationale (32 findings)

Applied consistently: `remediate` where the underlying defect is unambiguous
and only the *implementation shape* is architect-gated (e.g. `GO-SEC4-001`'s
fail-closed-vs-visible-opt-in, `GO-MCPTOOL-006`/`GO-SVCEXEC-001/002`'s
decomposition-boundary-only questions, `GO-RUNTIME-001`'s signature-change-vs-
cleanup-hook choice) — paralleling how AD-01/AD-12/AD-13/AD-17 were treated.
`needs-architect-decision` reserved for findings where the *disposition
itself* (fix vs. accept vs. wire/defer/retire vs. collapse-vs-parity-test) is
the open question: the six production islands (AD-06–AD-11), the
gravitational-package review (AD-14, `GO-DEP-002`/`GO-STORE-001`/`GO-STORE-005`),
the semantic-duplication share-vs-parity-test cluster (AD-19, 9 findings in
`11-semantic-duplication-migration-drift/`), the config-naming blast-radius
question (AD-20, `GO-INFRA-001`), the lint-ratchet/gofmt-sweep timing
questions (AD-21/AD-22, `GO-HYG-001`/`GO-CHAT-007`), and a handful of findings
whose own false_positive_considerations or task-file text explicitly frame
the fix-or-accept call as unresolved (`GO-SEC4-002/003`, `GO-API-001/003`,
`GO-RUNTIME-002/004`, `GO-STORE-008`, `GO-STORE-009`, `GO-CHAT-008`,
`GO-MEM-004/005`).

### `defer` and `false-positive` dispositions (16 findings, mostly informational)

12 findings — largely the informational "architectural observation" class
from `10-architectural-concentration/` and `13-mechanical-cleanup/05` — were
set to `defer`: the audit's own recommendation for each is "no action
required" or "optional, judgment call," with no live defect and no immediate
fix planned (`GO-MCPTOOL-007/010`, `GO-PLUGIN-006`, `GO-API-009`, `GO-EXEC-003/004`,
`GO-SVCCORE-007/009`, `GO-MCPTOOL-005`, `GO-RUNTIME-006`, `GO-CHAT-009`). 4 were
set to `false-positive`, each engaging directly with the audit's own
evidence: `GO-DEP-001`/`GO-STORE-002` (the audit's own text concludes
`Container`/`*Store`'s size is not itself a defect — a legitimate composition
root and a healthy large package, respectively), `GO-MEM-008` (the audit's
own text names this an example of its "reported dead != remove" guardrail
working correctly), and `GO-RUNTIME-008` (`agent.Boot`'s complexity judged
essential and correctly handled, same shape as `cmdServe`).

### `needs-more-evidence` (1 finding)

`GO-STORE-006` (`SyncDurableAgentInstanceConfig`'s missing transaction wrap).
The task file's own text says the audit could not complete a
caller-concurrency trace within its budget, and the fix's correct shape
depends entirely on that trace's answer. What would settle it: tracing
whether any real production caller can invoke this method concurrently for
the same slug; if yes, wrap in a transaction (or adopt the sibling
self-checking `WHERE`-clause idiom already used elsewhere in the same file).

### Task files corrected (stale line numbers only — none closed, none narrowed)

No task file's premise evaporated (fully or partially) — every one of the 113
findings was reconfirmed still open in some form (open bug, open island
decision, or open architect question), so no task got the
`CLOSED BY WAVE 0 REVALIDATION` banner, and none had its scope narrowed.
Six task files had stale `internal/service/container.go` / `cmd/nanite/main.go`
line-number citations corrected in place (the underlying code is unchanged;
only line numbers shifted, from unrelated additive work by the intervening
`TASKS/skills/` and `TASKS/loops/` batches — `+2` to `+62` lines depending on
where in the file):

- `06-store-correctness/01-fix-deleteagentbyid-error-swallowing.md` (`GO-STORE-003`: `agents.go` doc-comment/function citations, `1211-1214`/`1211-1221` → `1213-1216`/`1213-1223`)
- `04-container-reaper-lifecycle/01-fix-container-constructor-partial-failure-cleanup.md` (`GO-LIFE-001`: reaper-start/struct-capture/error-return citations in `container.go`)
- `04-container-reaper-lifecycle/04-track-untracked-goroutine-spawns.md` (`GO-SVCCORE-002`: the wake-reactor precedent comment's `container.go` citation, `1133-1143` → `1184-1194`)
- `07-runtime-correctness-lifecycle/02-fix-cmdserve-fatal-cleanup-bypass.md` (`GO-RUNTIME-001`: all 7 `slogx.Fatal` call-site line numbers in `cmd/nanite/main.go`, re-confirmed still 7 sites)
- `07-runtime-correctness-lifecycle/04-container-shutdown-idempotency-guard.md` (`GO-RUNTIME-005`: `Container.Shutdown`'s start line and internals in `container.go`, plus its one call site in `main.go`)
- `08-remaining-security-hardening/10-api-validation-duplication-and-pagination-bug.md` (`GO-API-004`: not a line-number fix — recorded that the in-repo comment's cited blocker, "producers not all landed," is now factually resolved; all three producers confirmed present)

`FINDING-INDEX.md` and `findings.json`'s `task_file` field required no changes
— no task file was split, merged, or renamed during this pass.

### Refreshed tool baseline (`00/02`, landed separately, `55d9b9c6`/`531dcfcc`)

`docs/audits/2026-08-21-go-quality/raw-1d3bfd96/` holds the frozen-HEAD
re-run with `DELTA.md` diffing every count-bearing measure against the audit
era. Cited directly rather than hand-recomputed wherever a finding's
disposition depended on a count: `gofmt -l` 122→130, gosec G304
(production) 68→70, `deadcode` 214→202, `internal/store` `dupl` hits
41→40 (across 22 files, not ~20), govulncheck's 14-vulnerability-ID set
unchanged.

### What's next

Wave 1 is gated on AD-01 through AD-04 being `decided` in
`ARCHITECT-DECISIONS.md` (operator track, running against Part 1's interim
report) — this folder's own work is complete independent of that. The
programmatic "Done means" check passes (see `01-revalidate-findings-against-head.md`'s
Work Log for the actual run output); `docs/audits/2026-08-21-go-quality/findings.json`
(pristine) is confirmed byte-for-byte untouched throughout both parts of this task.
