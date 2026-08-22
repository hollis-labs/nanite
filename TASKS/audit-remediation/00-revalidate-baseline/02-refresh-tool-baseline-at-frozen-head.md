# Rescue the audit's raw evidence, then refresh the tool baseline at frozen HEAD

**Phase:** Audit remediation — Wave 0 (revalidate the baseline)
**Status:** not-started
**Depends on:** none. **Step 1 is urgent and should run before anything else in this batch — see the banner below.** Steps 2+ are hard-gated on the dev freeze, same as `00/01`.
**Blocks:** every task in `01/` through `13/`, jointly with `00/01`. Specifically blocks `08/03` (G304 triage), `08/08` (dependency bumps), `13/01` (dead-code removal), and `13/03` (gofmt backlog), whose scopes are *defined by counts this task refreshes*.
**Parallelizable with:** `00/01` — different tooling, no overlapping writes except the final `findings.json` merge (see Non-goals).
**Touches:** `docs/audits/2026-08-21-go-quality/raw/` (new — evidence rescue), `docs/audits/2026-08-21-go-quality/raw-<FROZEN-SHA>/` (new — refreshed baseline), `.gitignore` (a narrow negation rule, see Step 1), and the count-bearing lines in the `## Context` of `08/03`, `08/08`, `13/01`, `13/03`. **No production code changes whatsoever.**
**requires_architect_decision:** ~~true~~ **resolved** — AD-23 decided *accept* on 2026-08-21; step 1 is done bar the commit. Steps 2-4 need no decision.
**requires_security_review:** false
**requires_regression_test:** false

---

> ## ✅ STEP 1 IS DONE (2026-08-21) — READ IT ANYWAY, THEN SKIP TO STEP 2
>
> The operator copied the evidence to `docs/audits/2026-08-21-go-quality/raw/`
> (29 files, 8.0 MB) and the planning session added the `.gitignore` negation
> at `.gitignore:88-91`. Verified: all 29 files stage (21 of 29 would have been
> silently skipped without the negation), and all 14 `raw/` paths cited by
> `REPORT.md` and the task files resolve. **AD-23 is decided: accept.**
> The one thing still outstanding is that it is **uncommitted** — confirm it is
> in git history before treating this step as closed. The original hazard
> description follows, retained because it explains why the negation rule
> exists and must not be removed.
>
> `REPORT.md` cites `raw/<file>` as the backing evidence for essentially every
> finding — its own line 8 says *"Raw tool output backing every finding below
> lives in `raw/`."* **That directory does not exist on `main`.** It exists in
> exactly one place: the un-merged worktree
> `.claude/worktrees/go-quality-audit/docs/audits/2026-08-21-go-quality/raw/`,
> where `git status` reports it as untracked (`??`), and where most of it is
> matched by `*.log` in the operator's **global** gitignore
> (`/Users/chrispian/.gitignore:11`). The merge commit `8258176e` brought
> `REPORT.md` and `findings.json` onto `main`; the evidence they cite never
> came with them.
>
> **If that worktree is pruned, the evidence behind all 113 findings is gone
> permanently and unreproducibly** — the tool output was generated against
> `8feeee5c`, a commit whose working state no longer exists anywhere else.
> Rescue it before doing anything else in this batch.

---

## Context

### Findings addressed

None directly. This task exists because a specific class of finding is
**count-bearing** — its scope is a number that was measured at `8feeee5c` and
is quoted verbatim in a task file that a worker will otherwise trust:

| Finding | Count quoted in the task file | Task that trusts it |
|---|---|---|
| `GO-HYG-001` | "all 122 files currently failing `gofmt -l`" | `13/03` |
| `GO-SEC-003` | "68 production-code gosec G304 findings" (`REPORT.md:384`) | `08/03` |
| `GO-SEC-001` | govulncheck's vulnerable-dependency set (`REPORT.md:224-255`) | `08/08` |
| `GO-DEAD-*` / `13/01`'s list | 4 symbols "confirmed unreachable by `deadcode`" (`REPORT.md:731`) | `13/01` |
| `GO-STORE-*` duplication | "~20 files, 41 `dupl` hits" (`REPORT.md:723`) | `11/13` |

Every one of those numbers was true of `8feeee5c` and is now of unknown
accuracy after **40 commits / 156 files / +24,891 lines**. A worker told to
"fix the 122 gofmt failures" will find a different number and have no way to
know whether the difference means progress, regression, or a stale task file.

### Root cause

Two distinct problems that happen to share a fix:

1. **Evidence never landed** (the banner above) — a gitignore interaction, not
   a decision anyone made.
2. **Counts were never refreshed** — the task-creation pass explicitly deferred
   all revalidation to the planner, and the planner (this batch's planning
   session) deferred the mechanical half to this task because it cannot run
   meaningfully until the freeze.

### Desired invariant

**Every count a task file asks a worker to act on was measured against the
frozen HEAD that worker will be working from, and the original measurement it
replaces is preserved and diffable.**

### Scope

Tool execution and file preservation only. This task measures; it does not
decide what any measurement means for a finding's disposition — that is
`00/01`'s job.

## What to do

### Step 1 — Rescue the original evidence (do this first, before the freeze even)

**Done 2026-08-21 — the operator copied the directory in full** (29 files,
8.0 MB; `diff` against the source confirms nothing was left behind), so the
selective-copy plan below was not needed. The commands are retained as the
record of what was preserved and why. The cited subset — confirmed by
`grep -n "raw/" docs/audits/2026-08-21-go-quality/REPORT.md`, all 14 of which
resolve — is:

```
govulncheck.log            golangci-baseline.log      golangci-baseline.json
lint-triage-output.txt     cluster-samples.txt        golangci-audit-complexity.log
complexity-summary.txt     fanin-fanout.tsv           package-loc-top.tsv
test.log                   test-race-extended-timeout.log
coverage-func.log          gosec.json                 deadcode.log
```

```bash
SRC=.claude/worktrees/go-quality-audit/docs/audits/2026-08-21-go-quality/raw
DST=docs/audits/2026-08-21-go-quality/raw
mkdir -p "$DST"
for f in govulncheck.log golangci-baseline.log golangci-baseline.json \
         lint-triage-output.txt cluster-samples.txt golangci-audit-complexity.log \
         complexity-summary.txt fanin-fanout.tsv package-loc-top.tsv \
         test.log test-race-extended-timeout.log coverage-func.log \
         gosec.json deadcode.log deadcode-test.log staticcheck.log errcheck.log vet.log; do
  cp -n "$SRC/$f" "$DST/$f"
done
du -sh "$DST"
```

The global `*.log` ignore must be overridden **narrowly** — do not touch the
global file. Add to this repo's `.gitignore`:

```gitignore
# Audit evidence: REPORT.md cites these by path; the global *.log ignore would
# otherwise silently drop the entire evidence chain for 113 findings.
!docs/audits/**/raw/*.log
!docs/audits/**/raw-*/*.log
```

Verify the override actually took — `git check-ignore` returning nothing is
the pass condition:

```bash
git check-ignore -v docs/audits/2026-08-21-go-quality/raw/golangci-baseline.log
git status --short docs/audits/2026-08-21-go-quality/raw/ | head
```

Then commit **this step alone**, ahead of the rest of the task. The point is
to get the evidence into git history before anything can prune that worktree;
don't let it wait on the freeze.

> **If the operator declines the repo-weight cost** (AD-23): the fallback is
> copying the subset to a durable location outside the repo and recording the
> absolute path in this task's Work log **and** in `REPORT.md`'s line 8, so the
> citation isn't silently dangling. An unrecorded external copy is not an
> acceptable outcome — it re-creates the same problem one machine later.

### Step 2 — Confirm the freeze, then re-run the audit's own tooling

Same gate as `00/01`: record the exact SHA, and use it as the directory
suffix so the two baselines never get confused.

```bash
FROZEN=$(git rev-parse --short HEAD)
OUT="docs/audits/2026-08-21-go-quality/raw-$FROZEN"
mkdir -p "$OUT"
```

Re-run with the **audit's own config**, not the project's fast gate — the
whole point is comparability. `docs/audits/2026-08-21-go-quality/audit-golangci.yml`
is explicitly "AUDIT-ONLY … Not used by CI/hooks/make lint" and enables the
complexity/duplication linters (`cyclop`, `gocyclo`, `gocognit`, `maintidx`,
`nestif`, `dupl`, `funlen`) the baseline depends on.

```bash
gofmt -l . | grep -v '^ui/' > "$OUT/gofmt-l.txt"; wc -l < "$OUT/gofmt-l.txt"
go vet ./... > "$OUT/vet.log" 2>&1
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 > "$OUT/golangci-baseline.log" 2>&1
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 \
  --output.json.path "$OUT/golangci-baseline.json" > /dev/null 2>&1
gosec -fmt=json -out="$OUT/gosec.json" ./... 2> "$OUT/gosec-stderr.log"
govulncheck ./... > "$OUT/govulncheck.log" 2>&1
deadcode ./... > "$OUT/deadcode.log" 2>&1
go mod verify > "$OUT/mod-verify.log" 2>&1
go mod tidy -diff > "$OUT/mod-tidy-diff.log" 2>&1
```

Any tool that isn't installed: record that fact in `$OUT/MISSING.txt` rather
than silently skipping it. A missing tool is a real finding about the dev
environment, and `12/01`'s lint-gate task needs to know.

### Step 3 — Diff the counts and write the delta table

Write `$OUT/DELTA.md` with one row per count-bearing measure:

```markdown
| Measure | At `8feeee5c` (audit) | At `<FROZEN>` | Delta | Affects |
|---|---:|---:|---:|---|
| `gofmt -l` files | 122 | ? | ? | `13/03` |
| gosec G304 (production) | 68 | ? | ? | `08/03` |
| govulncheck vulns | see `raw/govulncheck.log` | ? | ? | `08/08` |
| `deadcode` unreachable symbols | see `raw/deadcode.log` | ? | ? | `13/01` |
| `dupl` hits in `internal/store` | 41 across ~20 files | ? | ? | `11/13` |
| errcheck / staticcheck / revive totals | `REPORT.md:290` | ? | ? | `12/01` baseline |
```

A count that went **up** is worth calling out explicitly in the Work log —
it means post-audit development added to the backlog, which is exactly the
regression the `12/01` ratchet exists to stop, and is useful ammunition for
that task.

### Step 4 — Correct the task files that quote stale numbers

Amend the `## Context` of `08/03`, `08/08`, `13/01`, `13/03`, and `11/13` to
quote the refreshed number, with the original in parentheses:

> …all **N** files currently failing `gofmt -l` (was 122 at the audited commit
> `8feeee5c`; refreshed at `<FROZEN>` by `00/02` — see
> `docs/audits/2026-08-21-go-quality/raw-<FROZEN>/DELTA.md`).

Do not delete the original figure. The delta is the interesting part.

## Non-goals

- **Do not set any `disposition`.** That is `00/01`'s exclusive write. If both
  tasks run in parallel, `00/02` writes *only* to task-file Context sections
  and the `raw-*/` directories — it never touches `findings.json`. If a
  refreshed count implies a disposition change, say so in `$OUT/DELTA.md` and
  let `00/01` make the call.
- **Do not fix anything a refreshed tool run reports**, including trivially
  fixable new findings. New findings go to `TASKS/ESCALATIONS.md` per the
  guide's §10 rule (*"record new findings separately rather than rewriting
  baseline"*).
- **Do not overwrite `docs/audits/2026-08-21-go-quality/raw/`** with refreshed
  output. The suffixed `raw-<FROZEN>/` directory exists precisely so both are
  diffable forever.
- Do not re-run the full `-race` suite chasing the `internal/service` timeout —
  that is `04/03`'s task, with its own investigation method.

## Done means

- ~~`docs/audits/2026-08-21-go-quality/raw/` exists with the cited evidence
  subset and `git check-ignore` returns nothing for its `*.log` files~~ —
  **done 2026-08-21.** Remaining: confirm it is **committed**. Until it is in
  history, the rescue has not actually happened.
- `docs/audits/2026-08-21-go-quality/raw-<FROZEN>/` contains a refreshed run of
  every tool listed in Step 2, or a `MISSING.txt` naming each one that could
  not run and why.
- `raw-<FROZEN>/DELTA.md` has a real number in every `?` cell.
- `08/03`, `08/08`, `13/01`, `13/03`, and `11/13` quote refreshed counts with
  the original preserved in parentheses.
- `docs/audits/2026-08-21-go-quality/findings.json` and `REPORT.md` bodies are
  otherwise **unchanged** (the only permitted `REPORT.md` edit is the line-8
  evidence-location note, and only under the AD-23-declined fallback) —
  verify with `git status`.

## Work log

## Review notes
