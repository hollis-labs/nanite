# 00 — Revalidate the baseline (Wave 0)

**This folder gates the entire batch.** No task in `01/` through `13/` may be
dispatched to a worker until both tasks here are `reviewed` **and** AD-01
through AD-04 are `decided` (see "The third track" below).

**The repo-wide development freeze is in effect** (AD-24, decided 2026-08-21).
All tasks in all batches are frozen; `TASKS/audit-remediation/` is the
operator's #1 priority and the only authorized work. Exceptions require
explicit operator authorization. The operator — not a wave boundary or any
other derived condition — is the gate for resuming. See the banner at the top
of `TASKS/INDEX.md`.

The Go quality audit was run against commit `8feeee5c`. Development did not
stop. As of this batch's planning pass (2026-08-21), `git diff --stat
8feeee5c..HEAD` reports **40 commits, 156 files changed, +24,891/-539 lines** —
and two more batches were still landing code when this folder was first
written. **Those have since finished and the freeze is now in effect**
(AD-24) — specifically so this revalidation runs against a baseline that stays
put. Confirm that directly (`git log`, `git status`, `git worktree list`)
before starting; a moving baseline invalidates the whole task.

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

## Outcome

_(Filled in when this folder completes. Until this section exists with real
content, the batch is not cleared for dispatch.)_
