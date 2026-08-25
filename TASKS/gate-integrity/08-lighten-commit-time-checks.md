# Move whole-repo analysis off commit time and into a landing script

**Phase:** A — Remove the friction
**Status:** implemented
**Depends on:** none. **Do this first** — it costs the least and it is the one
task that immediately stops the current failure mode.
**Touches:** `lefthook.yml`, a new landing script (suggest `scripts/check.sh`
— match the repo's existing script conventions), `CLAUDE.md` and `AGENTS.md`
(both document the hook set), `TASKS/INDEX.md` point 3. Repo: nanite.

## Context

The hooks were activated on 2026-08-24 in `2b3b0216` — **one day before this
task was written.** In the 6 commits since, one already required
`--no-verify` (`4f3d38c4`, disclosed in the pre-unfreeze summary §7e: the
newly-live `go-lint` correctly flagged two intentional `legacyStatus`
constants and had no way to express "this finding is intended").

Re-derive both numbers rather than trusting them:

```
git log -1 --format='%h %ad %s' --date=short 2b3b0216
git log --oneline 2b3b0216..HEAD | wc -l
```

That is a bypass on a mechanism days old — and `--no-verify` is all-or-nothing.
It disables `go-format`, `go-vet`, `go-lint` and `migration-purity` together,
so a single unrelated false positive costs every check at once. That is the
actual risk here, and it is larger than anything the heavy checks catch at
commit time.

### Why commit time is the wrong place for these

`lefthook.yml`'s own measured header records the cost:

```
sed -n '15,27p' lefthook.yml
```

`go-lint` is the entire variance — ~16s cold, ~2-3s warm — because
`golangci-lint` analyzes the **whole repo** and only then filters the report to
lines the staged diff touches. `go-vet` is likewise whole-repo (`go vet ./...`).
Neither is scoped to what you changed, so both fail for reasons unrelated to
the commit in front of you — including in any worktree where `go.mod`'s
relative `../../libs/<module>` replaces do not resolve, which produces ~16
`undefined: envelopes` typecheck errors having nothing to do with the change.

### The cadence is defined and mid-implementation — this task is one step of it

`docs/engineering/testing-workflow.md` defines Tiers 0-4, "which tier does my
task need," and the cost breakdown (three packages dominate the `-race` suite:
`internal/store` 252s, `internal/api` 143s, `internal/service` 65s). **That
implementation is in flight.** The tiers were defined first; the hooks went
live 2026-08-24; this task wires the two together. It continues work already
underway — it does not discover a gap, and it must not introduce a competing
scheme.

### The goal this serves

Nanite is pre-release with no consumers. The purpose of these checks is to
**find bugs**, not to gate a release. A bug found when a feature lands instead
of at commit time costs essentially nothing; a blocked commit that trains
`--no-verify` costs every check simultaneously. Operator decision, 2026-08-25:
**commit-time checks become formatting only; whole-repo analysis moves to a
script run when a feature lands.**

## What to do

1. **`pre-commit` keeps only the instant, staged-scoped checks:** `go-format`
   and `migration-purity`. Both are measured at ~0.05s and neither can fail for
   a reason outside the staged diff. Remove `go-vet` and `go-lint` from
   `pre-commit`.
2. **Write the landing script — map it onto the tiers that already exist.**
   `docs/engineering/testing-workflow.md` already defines Tiers 0-4 and this
   task must not invent a competing scheme. Read it first. The relevant ones:

   | Tier | When | Cost |
   |---|---|---|
   | 0 | inner loop, the package you are editing | seconds — *"not a gate, it is feedback"* |
   | 1 | **feature done, before commit** | ~1-2 min |
   | 3 | full suite, `-race` | ~9 min |
   | 4 | flake investigation only | hours, `-timeout=180m` |

   The landing script is **Tier 1 plus static analysis**: `gofmt`/`goimports`,
   `go vet ./...`, `golangci-lint run`, and `go test ./...`. Runnable from a
   clean shell with no arguments, reporting which stage failed.

   **Do not put Tier 3 or Tier 4 in the script or on any hook.** Tier 3 is what
   the nightly gate runs; Tier 4 is deliberate flake hunting with explicit
   multi-hour timeouts. Operator constraint, 2026-08-25: 30s to two minutes is
   fine, *"what I absolutely can't have is tests running 30+ minutes"* — that is
   Tier 4, and it must never sit between someone and a commit or a push.

   Have the script name the tier it is running, so the vocabulary in
   `testing-workflow.md` and the vocabulary a developer sees at the terminal are
   the same one.

3. **`pre-push` — keep `go test ./...`, and scope it to `main`.** Operator
   decision, 2026-08-25: *"Pre-push sounds perfect. Add the branch-scoped
   variant."* Push time is not a concern — the operator pushes only at the end
   of a piece of work — so the backstop stays, but WIP-branch pushes should not
   pay for it.

   Verified working at the installed `lefthook 2.1.4` in a scratch repo before
   this task was written — **re-verify at whatever version is installed when you
   execute**:

   ```yaml
   pre-push:
     commands:
       go-test:
         only:
           - ref: main
         glob: "*.go"
         skip_empty: true
         run: go test ./...
   ```

   The two skip reasons print differently, which is what makes this testable:
   on a non-matching branch lefthook reports `(skip) by condition`; on `main`
   with no Go files in the push it reports `(skip) no matching push files`.

   Keep the existing `glob: "*.go"` and `skip_empty: true` — they already mean a
   docs-only or config-only push runs nothing at all.

   **Correct the measured header while you are here.** It currently claims
   `4.32s with everything cached`. Two back-to-back cached runs on 2026-08-25
   measured **6.32s** and **8.85s**. Re-derive rather than copying either
   figure:
   ```
   /usr/bin/time -p go test ./... 
   ```
   Note this is the **no-`-race`** suite. The `-race` full suite is a different
   tier entirely (~9 min) and does not belong on a hook.

4. **Update every place that documents the hook set**, because three files
   currently describe it and all will be wrong: `CLAUDE.md`'s "What the hooks
   gate" paragraph, `AGENTS.md`, and `TASKS/INDEX.md` point 3. State what the
   hooks do now; do not narrate what changed.
5. **Document when to run the landing script** in `CLAUDE.md` — the honest
   answer is "when a feature lands, before you push a branch you care about, and
   before dispatching the quality gate," not "every commit."
6. Leave `frontend-lint` as-is. It is `skip: true` for an unrelated real reason
   (`CW-20260816-0087`) and is not part of this change.

## Done means

- `pre-commit` runs only `go-format` and `migration-purity`. Prove it by
  committing a `.go` file with a deliberate `go vet` failure and showing the
  commit **succeeds** — that is the behavior change, and inspecting the YAML
  does not demonstrate it.
- A deliberately misformatted file is still **rejected** at commit. The point is
  a lighter hook, not an absent one.
- The landing script exists, runs from a clean shell with no arguments, and
  **fails with a clear stage name** when any stage fails. Prove each failure
  path by breaking one thing at a time — a script that always passes satisfies
  a careless reading of this criterion.
- Total `pre-commit` wall time for a one-file Go commit is reported in the Work
  log from a real `git commit`, read off lefthook's own summary, not estimated.
- **The `main`-scoped pre-push is proven both ways**: on a feature branch
  lefthook reports `go-test (skip) by condition`; on `main` with a Go file in
  the push the suite actually runs. Show both outputs. A config that skips
  everywhere satisfies half this criterion and is useless.
- The landing script's stages correspond to a tier named in
  `docs/engineering/testing-workflow.md`, and no hook or script runs Tier 3 or
  Tier 4.
- `lefthook.yml`'s cached-timing header is re-derived, not copied.
- `CLAUDE.md`, `AGENTS.md` and `TASKS/INDEX.md` point 3 all describe the same
  hook set, and it is the one that now exists. Grep for stragglers with a
  pattern that can find **prose**, not only the hyphenated command names. The
  original form here was `grep -rn 'go-lint\|go-vet' --include='*.md' .` and it
  structurally cannot match `format/lint/vet`, `via lefthook pre-commit`, or
  `reproducing the pre-commit hook` — four live documents stayed wrong behind
  it while this criterion read as passing. Use instead:

  ```
  grep -rniE 'pre-?commit|lefthook|commit[ -]time' \
      --include='*.md' --include='*.yml' --include='*.yaml' --include='*.sh' . |
    grep -v node_modules |
    grep -Ei 'go-lint|go-vet|[^a-z-]vet|lint|golangci|staticcheck|errcheck'
  ```

  It over-matches on purpose — every hit gets triaged as either a **live claim**
  (fix it) or a **historical record** quoting the old hook set as the thing
  being corrected (leave it). A grep narrow enough to need no triage is a grep
  narrow enough to miss the prose forms.
- The `pre-push` decision is stated explicitly in the Work log.

## Work log

Executed 2026-08-24. Started from `9591c1a6`, clean tree, on `main`, in the main
checkout (no worktree). `main` advanced to `26c04ac8` mid-task — the Orchestrator
landed `e859bb00` and `26c04ac8`, both `TASKS/*.md`-only, touching no `.go` file,
`lefthook.yml`, `Makefile` or `scripts/`, so no measurement below is invalidated.
Installed lefthook is **2.1.4** (`lefthook version`).

### What landed

- **`lefthook.yml`** — deleted the `go-lint` and `go-vet` `pre-commit` commands
  (they occupied lines 62-81 at the point of deletion, re-derived with
  `grep -n 'go-lint:\|go-vet:\|migration-purity:' lefthook.yml`; the task file's
  own citations had drifted from `77137106`). `pre-commit` is now `go-format`,
  `migration-purity`, and the still-`skip: true` `frontend-lint`. Rewrote the
  measured header: scope block, a pointer to the landing check, and re-derived
  cost figures for both hooks. `pre-push` gained `only: - ref: main` and a
  comment block recording both skip reasons and the fact that it is the
  **no-`-race`** suite, explicitly not Tier 3.
- **`scripts/check.sh`** (new, mode 755) — the landing check. Four stages,
  named: `format`, `vet`, `lint`, `test`. Runs every stage rather than stopping
  at the first, then prints `check.sh: FAILED stages: <names>` and exits 1.
- **`CLAUDE.md`** — "What the hooks gate" rewritten to the hook set that now
  exists, plus a `pre-push` paragraph and a landing-check paragraph stating when
  to run it ("when a feature lands… not on every commit") and which tiers do not
  belong on a hook.
- **`AGENTS.md`** — the same hook set stated in "Common operations", and
  `./scripts/check.sh` added to "Test and lint" with its stage list and the
  reason its lint stage is scoped.
- **`Makefile:50`** — the `test` target's comment claimed `go test -race ./...`
  "matches pre-push hook behavior". It does not: `pre-push` has never carried
  `-race`. Corrected to name Tier 3 and to say plainly that the hook and the
  landing check both run the no-`-race` suite. In scope because the dispatch
  forbids conflating the two "anywhere in code, comments, or docs".
- **`TASKS/ESCALATIONS.md`** — the "Standing caveat — the gate detects, it does
  not gate" paragraph's clause *"a fresh clone **or a new worktree** has no
  checks at all"*. The worktree half is false. Corrected that clause only.
- **`TASKS/gate-integrity/06-…md`** — the parenthetical figures only. Not one
  word of `06`'s instructions or scope was touched; `06` remains a Wave D task
  owned by someone else.

### `TASKS/INDEX.md` was handed off, not edited — deviation

This task file's `Touches` line names `TASKS/INDEX.md` point 3, and the original
dispatch assigned it. Mid-task the Orchestrator corrected both, citing the
standing convention recorded in `TASKS/ESCALATIONS.md` (only the Orchestrator
writes `TASKS/INDEX.md`, reinforced by two further entries covering write agents
sharing a checkout). **No edit to `TASKS/INDEX.md` was made by this task.** The
exact current text, its re-derived line range, and the proposed replacement —
covering both the hook-set description and the re-derived timings — were handed
to the Orchestrator in the completion report under `## INDEX.md handoff`, for
the Orchestrator to apply under its own attribution. Point 3 sits at
`TASKS/INDEX.md:50-79` at `26c04ac8`
(`grep -n '^\*\*3\. Git hooks are live\|^\*\*4\. The quality gate works' TASKS/INDEX.md`
→ 50 and 81).

### The `pre-push` decision, stated explicitly

**`go test ./...` stays on `pre-push`, scoped to `main` via lefthook's
`only: - ref: main`.** It was neither moved nor removed. Rationale, operator,
settled before execution: pushes happen only at the end of a piece of work, so
push time is not a concern — but a WIP-branch push should not pay for the suite.
The repo has no branch protection and the quality gate fires on `schedule` +
`workflow_dispatch` only, so this hook is the last automatic check before code
lands on `main`.

### Tier mapping — no competing scheme introduced

`docs/engineering/testing-workflow.md`'s Tiers 0-4 are used as-is, unrenamed and
unrenumbered. The landing check's test stage is **Tier 1** and says so at the
terminal. One honest deviation is recorded in the script's own header: that
document's Tier 1 form is `go test -race -count=1` over the changed packages
plus their dependents, while the script runs whole-repo `go test ./...` without
`-race`, because a landing script has to run with no arguments and cannot know
which packages changed. Wider in scope, no amplification, same wall-time budget.
The script therefore tells the reader to run Tier 2 themselves if the change
touched concurrency or lifecycle ordering. **No hook and no script runs Tier 3
or Tier 4.**

### Deviation: the lint stage is scoped, not `golangci-lint run` bare

Step 2 of this file prescribes `golangci-lint run`. Measured at `9591c1a6`:

```
/usr/bin/time -p golangci-lint run     # exit 1 — "1724 issues", real 22.03
```

A stage that can never pass is worse than no stage — it is precisely the
"trains you to ignore output" failure this task exists to end. Whole-repo lint
is already the nightly gate's job, where it runs with `--issues-exit-code=0`
against a ratcheting baseline (`.github/workflows/full-repo-quality.yml:129-137`,
`scripts/quality-ratchet.py`). `.golangci.yml`'s own header prescribes
`--new` for adoption on legacy code. The stage therefore runs
`golangci-lint run --new-from-rev "$(git merge-base HEAD main)"`, overridable
with `CHECK_LINT_BASE`. On `main` that base is `HEAD`, which is the same
semantics the retired `go-lint` hook had.

### Correction to this file's rationale: `skip_empty: true` is inert

Step 3 says to keep `glob` and `skip_empty: true` because "they already mean a
docs-only or config-only push runs nothing at all." The `glob` half holds. The
`skip_empty` half does not: at lefthook 2.1.4 `lefthook dump` omits the key
entirely and `lefthook validate` exits 1 reporting *"No values are allowed
because the schema is set to 'false'"* for each of the four commands carrying
it. The skip behavior is 2.1.4's default and was confirmed directly in both
directions (see the proofs below). Per the decision/rationale rule the stated
action stands — the key was kept — and the correction is recorded in
`lefthook.yml`'s header so the next reader is not misled. Note `lefthook
validate` already failed this way before this task (six commands carried
`skip_empty` at `9591c1a6`; four do now). Dropping the key is a separate
cleanup, parked and reported.

### Which bucket each stale-`4.32s`/`44.87s` hit went in

`grep -rn '4\.32s\|44\.87' --include='*.md' --include='*.yml' . | grep -v node_modules`
returned six hits at `9591c1a6`, matching the dispatch's list exactly.

| Hit | Bucket | Action |
|---|---|---|
| `lefthook.yml:146-147` | asserts as current fact (the source) | **fixed** — re-derived |
| `CLAUDE.md:37` | asserts as current fact | **fixed** — re-derived |
| `TASKS/INDEX.md:53-54` | asserts as current fact | **handed to the Orchestrator**, not edited |
| `TASKS/gate-integrity/06-…:66-67` | asserts as fact for a future Wave D worker to trust | **fixed** — figures dropped; a task file naming no number cannot go stale |
| `TASKS/gate-integrity/08-…:127` (this file) | quotes the number as the thing being fixed | **left as-is** |
| `docs/engineering/orchestrator-kickoffs/gate-integrity-wave-a.md:237` | quotes the number as the thing being fixed | **left as-is** |

### Re-derived numbers, each with its command (at `9591c1a6` unless noted)

**`pre-push` suite.** `go test ./...` covers all 110 packages `go list ./...`
reports — that count includes the stray vendored
`ui/node_modules/flatted/golang/pkg/flatted`; 109 excludes it; 99 have test
files (`go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./... | grep -v '^$' | wc -l`).

```
go clean -testcache && /usr/bin/time -p go test ./...   # real 41.34   (0 of 99 cached)
/usr/bin/time -p go test ./...                          # real  5.06   (99/99 cached)
/usr/bin/time -p go test ./...                          # real  4.59   (99/99 cached)
```

The cold run was forced with `go clean -testcache` — the Go **build** cache was
left warm, which is the same methodology the previous header used ("99 packages
ran, 0 cached" is a test-cache statement).

**`pre-commit`, from three real `git commit` attempts with one staged `.go`
file, read off lefthook's own summary — not estimated:**

| run | total | `go-format` |
|---|---:|---:|
| 1 (also re-synced hooks) | **0.17s** | 0.16s |
| 2 (the rejection below) | **0.06s** | 0.04s |
| 3 | **0.06s** | 0.03s |

`migration-purity` printed `(skip) no files for inspection` and `frontend-lint`
`(skip) by condition` on all three.

**Landing check**, `/usr/bin/time -p ./scripts/check.sh`:

```
golangci-lint cache clean && go clean -testcache && /usr/bin/time -p ./scripts/check.sh   # real 67.36
/usr/bin/time -p ./scripts/check.sh                                                       # real  8.75
```

Both inside the operator's 30s-to-two-minutes budget.

### Proofs — behavioral, not by inspection

**1. `pre-commit` no longer blocks on `go vet`.** On throwaway branch
`throwaway/hook-proofs`, a `gofmt`- and `goimports`-clean file whose only defect
is a `printf` verb/arg mismatch (`go vet ./internal/checkproof/` → exit 1,
`fmt.Sprintf format %d has arg "not-an-int" of wrong type string`) **committed
successfully** as `db3be327`:

```
│  frontend-lint (skip) by condition
│  migration-purity (skip) no files for inspection
┃  go-format ❯
summary: (done in 0.17 seconds)
✔️ go-format (0.16 seconds)
[throwaway/hook-proofs db3be327] throwaway: deliberate go vet failure…
=== commit exit=0 ===
```

**2. A misformatted file is still rejected.** Same branch, next commit attempt:

```
┃  go-format ❯
Unformatted files:
internal/checkproof/misformatted.go
exit status 1
summary: (done in 0.06 seconds)
🥊 go-format (0.04 seconds)
=== commit exit=1 ===
```

`HEAD` stayed at `db3be327`. A lighter hook, not an absent one. The branch was
then deleted (`Deleted branch throwaway/hook-proofs (was db3be327)`) and the
tree confirmed clean.

**3. The landing script fails correctly, one stage at a time.** Each break was
made in isolation and the script re-run:

| break | reported |
|---|---|
| misformatted `.go` | `FAILED stages: format` (vet, lint, test all OK) |
| `printf` verb/arg mismatch | `FAILED stages: vet lint test` — a vet defect necessarily also trips `lint` and `go test`'s built-in vet |
| unused unexported func (lint-only, vet-clean, compiles) | `FAILED stages: lint` (format, vet, test all OK) |
| deliberately failing `_test.go` | `FAILED stages: test` (format, vet, lint all OK) |

The lint-only and test-only breaks are the positive controls for those two
stages: without them, `lint`'s routine `0 issues.` would be a zero of unknown
meaning. Per §4.2, the input that would make the `format` stage pass while
proving nothing is an **empty file list**, so the stage asserts presence before
properties — it prints its file count (`1354 Go files`) and fails with
`ERROR: found 0 Go files to check — the file list is broken, not clean.`
Proven by running a copy of the script with `go_files()` stubbed to emit
nothing; it failed as designed.

**4. The `main`-scoped `pre-push` is proven in both directions, with no push to
`origin`.**

*Skip direction*, on `throwaway/hook-proofs`, which **did** have a `.go` file in
its range vs `origin/main` — so the skip is attributable to the branch condition
and not to an empty file set:

```
│  go-test (skip) by condition
summary: (done in 0.03 seconds)
```

*Run direction*, on `main`. `origin/main..HEAD` held no `.go` file, so a
temporary local commit `de1ed746` was made on `main` carrying one trivial `.go`
file, then `lefthook run pre-push`:

```
┃  go-test ❯
ok  	github.com/hollis-labs/nanite/cmd/nanite	4.908s
…111 package lines…
summary: (done in 41.89 seconds)
✔️ go-test (41.89 seconds)
```

The suite genuinely ran (test cache cleared first). `de1ed746` was then dropped
with `git reset --mixed HEAD~1` plus `rm -rf internal/prepushproof`, and `HEAD`
confirmed back at `9591c1a6` with a clean tree. Nothing was pushed to `origin`
at any point, and `--no-verify` was never used.

*Third case observed in passing*, on `main` with a docs-only range:
`go-test (skip) no matching push files` — the other skip reason, distinct from
the branch condition.

**No `git stash` was run at any point.** Shelving used throwaway branches and a
throwaway local commit, both cleaned up.

### Baseline

`go build ./cmd/nanite/` OK · `go vet ./...` exit 0 ·
`./scripts/check.sh` exit 0, all four stages pass at `26c04ac8`.

### No schema work

This task touched no migration. `internal/store/migrations/` is unmodified.

---

## Second pass — Orchestrator review corrections (same day, after `ea42a879`)

Two defects raised on review. Both fixed; both proven by demonstration, not by
reading the change.

### A. The lint stage passed vacuously for work already committed on `main`

**Reproduced before touching anything.** A probe package with two real findings
(`unused` + `errcheck`), `gofmt`-clean:

```
# uncommitted, base = merge-base(HEAD, main) = ea42a879
internal/lintprobe/probe.go:10:11: Error return value of `os.Remove` is not checked (errcheck)
internal/lintprobe/probe.go:6:6:   func unusedHelper is unused (unused)
2 issues:  * errcheck: 1  * unused: 1        ->  check.sh: FAILED stages: lint   (exit 1)

# byte-identical file, committed on main, same script, base = 1ca86fdf = HEAD
0 issues.                                     ->  check.sh: all stages passed    (exit 0)
```

That is a §1.3 zero. On `main`, `git merge-base HEAD main` **is** `HEAD`, so
everything already committed is outside the diff. It matters because the
operator works directly in `main` and commits as they go, then runs the landing
check — precisely the path where the stage checked nothing and printed `OK`. On
a feature branch the old base was already correct.

**Fix 1 — the base.** `git merge-base HEAD origin/main`, with a two-step
fallback and `CHECK_LINT_BASE` still overriding. On a feature branch it is the
last pushed ancestor; on `main` it is the last pushed commit — which is exactly
the "run this before you push" scope the script's own header documents, and it
covers committed and uncommitted work alike. Fallback chain verified in a
scratch git repo across all three states:

| state | resolved base |
|---|---|
| no `origin/main`, `main` exists | `merge-base HEAD main` |
| `CHECK_LINT_BASE=deadbeef` | `deadbeef` |
| neither ref exists | `HEAD` |

**Fix 2 — the positive control, reported rather than failed.** Zero changed Go
files is a legitimate state here (unlike zero Go files in the `format` stage,
which is always broken), so it must not fail — but it must not read as
"examined and clean" either. The stage now resolves and prints its base, counts
the Go files in scope (`git diff --name-only --diff-filter=d <base> -- '*.go'`
unioned with untracked `*.go`), and distinguishes the two outcomes with a third
stage result that is neither OK nor FAIL:

```
==> lint — golangci-lint run --new-from-rev 77137106
    0 changed Go files vs 77137106 — nothing to lint
    ---  (0s)  examined nothing
…
check.sh: no stage failed — but these examined nothing: lint      (exit 0)
```

versus, with something in scope:

```
    1 changed Go file(s) vs 77137106; only findings on
    lines they touched — whole-repo lint is the nightly gate's job
```

**Proof, both directions.** Same committed-on-`main` probe that produced the
vacuous pass above, under the fixed script: base resolves to `77137106`,
`1 changed Go file(s)`, both findings reported, `check.sh: FAILED stages: lint`,
exit 1. On a throwaway feature branch carrying the same committed probe: same
two findings, `FAILED stages: lint`, exit 1. With no changed Go files at all:
the `examined nothing` output above, exit 0.

One honest note on the feature-branch case: because local `main` is currently
ahead of `origin/main`, the resolved base on a branch is `origin/main`
(`77137106`), not the local branch point. That is a superset of the branch's own
diff and it costs nothing here, since none of the intervening commits touch a
`.go` file.

### B. `testing-workflow.md` — the doc → script direction

Scope-parking item 7 from the first pass was in scope after all; the kickoff
that dispatched this task made closing the disconnect part of `08`. Before:
`grep -c 'check\.sh' docs/engineering/testing-workflow.md` → **0**. After →
**4**. Three additions, no tier renamed, renumbered or added
(`grep -o 'Tier [0-9]' … | sort -u` → exactly `Tier 0..4`, and the five
`### Tier N` headings are intact):

- **§3, immediately under "The tiers"** — a "Where each tier actually runs"
  table mapping every tier to its real mechanism, and a plain statement that
  **no git hook runs any tier**: `pre-commit` is formatting only, and
  `pre-push`'s `go test ./...` on `main` is the whole suite *without* `-race`,
  so it is neither Tier 1's scope nor Tier 3's amplification and should be read
  as a backstop, not a tier. Tier 3's row cites
  `.github/workflows/full-repo-quality.yml`, its `schedule` (`17 7 * * *`) +
  `workflow_dispatch` triggers, and `go test -race -count=1` at line 188 — all
  re-derived from the workflow, not carried.
- **§3 Tier 1** — `./scripts/check.sh` named as the runnable form of the tier,
  with its four stages, its measured cost, and the difference already recorded
  in the script header now stated in the doc too: whole-repo `go test ./...`
  without `-race` rather than changed-packages-plus-dependents, because a
  no-argument script cannot know what you changed. Wider scope, no
  amplification; run Tier 2 yourself if its trigger applies.
- **§4** — one line under the table: every row is something you run by hand, and
  §3's mapping covers less than the table asks for, deliberately.
- **§6** — the go-live bullet *"Promote Tier 1 to a pre-push hook"* replaced by
  what is true and what genuinely remains: decide whether the landing check runs
  from a hook rather than on request (keeping the "or people `--no-verify` past
  it" warning, which still holds), and decide whether the push path should carry
  `-race`, since today only the nightly run amplifies for memory-model
  violations.

### A throwaway commit landed on `main` and was removed forward, not rebased

Reproducing defect A required being on `main` — the bug is that
`merge-base HEAD main` equals `HEAD` there, which cannot be reproduced from a
branch. The probe was therefore committed to `main` as `1ca86fdf`. Before it
could be cleaned up, the Orchestrator's `11c417c4` landed on top of it.

`1ca86fdf` was removed **forward**, in `0d91e73a`, rather than by
`git rebase --onto`. Nothing here is pushed (`origin/main` is `77137106`), so a
rewrite would have been safe from the remote's point of view — but it would have
rewritten a peer agent's commit under them in a shared checkout while they were
actively committing, which is the same shared-mutable-ref hazard class as the
repo-global `git stash` incidents `EXECUTION-PROCESS.md` records. Not worth the
tidier history. `main` therefore carries `1ca86fdf` and `0d91e73a`, both labelled
as throwaway with the reason in the message. **If the Orchestrator wants them
squashed out, that is theirs to decide — they own `main`'s history.**

No `git stash` was run. No push to `origin`. No out-of-tree worktree. The one
throwaway branch (`throwaway/lint-base-proof`) was deleted
(`Deleted branch throwaway/lint-base-proof (was d555e6a0)`).

### Baseline after the second pass

`go build ./cmd/nanite/` OK · `./scripts/check.sh` exit 0 with `format`, `vet`
and `test` OK and `lint` correctly reporting `examined nothing` (there is no
changed `.go` file in this task's final diff).

---

## Third pass — precision corrections (same day, after `7d04c444`)

### A report that names a file as stale is a claim, and mine lacked its grep

The second-pass report told the Orchestrator that **both** `CLAUDE.md` and
`AGENTS.md` carried the stale *"merge base with `main`"* wording. Only
`AGENTS.md` did:

```
grep -n 'merge base' CLAUDE.md     #  -> no output, exit 1
grep -n 'merge base' AGENTS.md     #  -> 123: … merge base with `main`) …
```

`CLAUDE.md` said `golangci-lint` *"scoped to what your work added"* and named no
base at all — imprecise, not false. This is the batch's own failure class
committed inside the batch: I asserted a second file's contents from memory of
having written similar prose in it, and shipped the claim without the grep that
§1.2 requires for a number and equally requires for "this file says X". The fix
direction happened to be right, so it cost nothing here. Recorded because the
clean version of this report would simply not have mentioned it.

### What changed

- **`AGENTS.md:123`** — base corrected to `origin/main`.
- **`CLAUDE.md:50`** — base **added** (`"scoped to what your work added since the
  merge base with `origin/main`"`). Judgment call, since leaving it was equally
  defensible: `CLAUDE.md` is loaded into every agent's context, so brevity has
  real value there — but the precision costs six words, and the specific
  confusion it forecloses ("is the work I just committed in scope?") is exactly
  the defect the second pass fixed. Six words to make a just-fixed bug
  un-re-introducible is worth it.
- **`scripts/check.sh` header, and its inline comment at the lint stage** — both
  carried *my own* now-known-imprecise claim that on a feature branch the base
  "is the branch point" / that the two bases "agree". They agree only while
  `main` is fully pushed. Both rewritten to state the real behavior where a
  reader meets it first: the base is the last **pushed** commit; when local
  `main` is ahead, the stage lints a **superset** of the branch's own diff, so
  findings from commits you did not write are expected, not a bug. It
  over-reports and never under-reports, which is the safe direction for this
  stage. Also stated at the tail of `AGENTS.md`'s landing-check section.
- **`scripts/check.sh:57`** — the `--help` range is a hardcoded line span and the
  header grew again, so it was silently truncating 9 lines. Recomputed to
  `2,50p` and verified by running `./scripts/check.sh --help | wc -l` → **49**,
  ending exactly on the last header line. That is twice this hardcoded range has
  gone stale in two passes; making it self-delimiting stays parked, not done,
  because it is outside what was asked.

No behavior changed in this pass — `check.sh`'s only non-comment edit is the
`--help` line span. Verified: `go build ./cmd/nanite/` OK, `./scripts/check.sh`
exit 0 (`format`/`vet`/`test` OK, `lint` correctly `examined nothing`).

### Every place naming the lint base now agrees

```
grep -rn 'merge base with `main`' --include='*.md' --include='*.sh' . | grep -v node_modules
#  -> no output, exit 1
```

`AGENTS.md:123`, `CLAUDE.md:50`, `docs/engineering/testing-workflow.md:115`,
`scripts/check.sh:36` and `:132` all name `origin/main`. `TASKS/INDEX.md:70`
does too; that file is the Orchestrator's and was not touched by this task at
any point.

### The two throwaway commits stay

`1ca86fdf` and `0d91e73a` remain in `main`'s history, confirmed by the
Orchestrator as the right call: rewriting `main` under a concurrent writer in a
shared checkout is the same shared-mutable-ref hazard as the repo-global
`git stash` incidents, and "nothing is pushed" does not make it safe when
someone else is committing into the same ref. Both are labelled and
self-documenting; the probe itself is gone.

## Review notes

**Reviewed 2026-08-25 at `ff8840b0` by a fresh reviewer dispatch** — no shared
context with the implementing worker. Transcribed here by the Orchestrator
because the reviewer agent type is read-only by design; the verdict and the
findings are the reviewer's, the transcription is mine.

**Verdict: FAIL — narrow.** The mechanism is correct and every behavioural
acceptance criterion reproduced independently. Two real defects and one
fragility remain, none in the core design.

**Reproduced rather than taken on trust:** `pre-commit` accepts a `go vet`
failure and still rejects a misformatted file; the landing script fails with the
correct stage name for each of four breakages in isolation; the lint stage
catches a finding in work already committed on `main`; `pre-push` skips by
condition on a feature branch that *did* carry a `.go` file, so the skip is
attributable to the branch rather than an empty file set; the `examined nothing`
third outcome fires and never prints `OK`; the fallback chain and
`CHECK_LINT_BASE` behave as documented; tiers are unchanged at five; the cited
workflow line numbers re-derive correctly.

**Finding 1 — `scripts/check.sh`'s format stage can report `OK` for files it
never examined.** `gofmt`'s exit status and stderr are both discarded, so a
missing `gofmt` (exit 127), an unparseable file, or an unreadable path is
indistinguishable from clean. The stage guards the empty-list input and nothing
else, while `goimports` immediately below it *is* guarded with `command -v`. The
reviewer hit this unintentionally mid-review: a tracked `.go` file deleted from
the worktree but not the index produced `1355 Go files` / `OK` while `gofmt`
could not open one of them. The printed count comes from `git ls-files`, so it
names paths git knows about, not files actually examined. Severity low-to-
moderate, confidence high on mechanism. The same shape exists in `lefthook.yml`'s
`go-format` hook, which this task left alone — pre-existing, but it is the only
remaining commit-time gate.

**Finding 2 — four live documents still tell readers `go vet` / `golangci-lint`
run at commit time.** `README.md:37` (outward-facing Quick Start),
`.nanite/agents/backend.md:386`, `.nanite/agents/reviewer-backend.md:223`, and
`TASKS/audit-remediation/PREVENTION.md:123` and `:124`. This misses the "every
place that documents the hook set" criterion. The straggler grep in *Done means*
matches only the hyphenated command names `go-lint`/`go-vet`, so it structurally
cannot find prose forms — the criterion's own verification instrument was too
narrow. The two `.nanite/agents/` files are per-agent boot context, so an agent
can read "vet runs via lefthook pre-commit" and skip running it: a document
asserting a gate that does not exist, which is the `frontend-lint` problem
inverted.

**Finding 3 — `scripts/check.sh`'s `--help` is a hardcoded `sed` span with no
assertion.** Correct at this commit (49 lines, ending on the last header line)
but silently truncated 9 lines one commit earlier, the second drift in two
passes. `--help` exits 0 unconditionally, so a `sed` that reads nothing still
succeeds. Separately, `cd "$(git rev-parse --show-toplevel)"` runs before the
help branch, so a relative-path invocation from a subdirectory re-resolves `$0`
against the repo root and prints nothing.

**Checked and cleared:** no automatic coverage was lost by removing `go-vet`
from `pre-commit` — the nightly gate's ratchet config
(`docs/audits/2026-08-21-go-quality/audit-golangci.yml:35`, `:82-85`) enables
`govet` with `enable-all: true` minus `fieldalignment`, strictly broader,
including the `lostcancel` analyzer behind the incident `PREVENTION.md` cites.
*(Orchestrator's note: my first transcription of this bullet dropped the file
and line numbers. A later worker read the uncited claim, correctly added a
citation, and my relay of that reached me as "the reviewer cited the wrong
file" — which was never true and was never written down anywhere. The reviewer's
citation was right from the start. Restored here; the drift was mine.)* No Tier 3 or Tier 4 on any hook or in
the script, and nothing conflates the `pre-push` suite with Tier 3. No naming
collision for `scripts/check.sh`. The script is bash-3.2-safe. `lefthook
validate`'s non-zero exit is pre-existing and this task reduced it from six
carriers of `skip_empty` to four.

**Could not check:** `pre-push` executing the suite on real `main` in this repo,
which would need a throwaway `.go` commit on shared `main` while the Orchestrator
was committing — judged not worth the shared-ref hazard. Established instead by
`lefthook run pre-push` on `main` printing `(skip) no matching push files`
rather than `(skip) by condition` (proving the `only:` condition passed), plus a
scratch repo with this `lefthook.yml` copied verbatim where a `.go` commit on
`main` made the command execute and branches named `feat/wip` and `mainline`
both skipped by condition. The reviewer flagged the "actually runs" half as
out-of-repo evidence rather than smoothing it over.

---

## Fourth pass — review-finding fixes (after `6c139038`)

Executed 2026-08-25. Started from `6c139038` (`git rev-parse HEAD`), **clean
tree** (`git status --porcelain` → empty), on `main`, in the main checkout.
`git worktree list` → one entry, this checkout. Fixes the three findings in
**Review notes** above, plus the glossary entry the dispatch added. The Review
notes section itself was not touched.

**No commit was made on `main`, and none was needed.** Every hook proof below
used `git add` / `git rm --cached` plus `lefthook run pre-commit` — which is
exactly what a real `git commit` invokes, verified rather than assumed:
`.git/hooks/pre-commit:71` is `call_lefthook run "pre-commit" "$@"`. No
`git stash` at any point, no push to `origin`, no worktree, no throwaway branch.

### Finding 1 — a format check could report OK for files it never examined

Reproduced first, at `6c139038`, three ways in `scripts/check.sh` and two in
`lefthook.yml`. All five printed a pass while the tool had failed:

| what was broken | before the fix |
|---|---|
| `rm internal/brand/brand.go` (tracked, still in the index) | `1354 Go files` / `OK` — while `git ls-files -z '*.go' \| xargs -0 gofmt -l` exited **1** with `stat internal/brand/brand.go: no such file or directory` |
| `env -i PATH=/usr/bin:/bin` (no `gofmt`) | `1354 Go files` / `OK` — and, tellingly, an honest `note: goimports not on PATH` on the very next line |
| an unparseable `internal/checkproof/broken.go` | `1355 Go files` / `OK` — while `gofmt -l` on that file alone exited **2** with `broken.go:3:14: expected ')', found '{'` |
| the same unparseable file, **staged**, via `lefthook run pre-commit` | `✔️ go-format (0.01 seconds)` |
| a **clean** staged `.go` file with `gofmt` off `PATH` | `✔️ go-format (0.02 seconds)` |

The root cause is one shape in two places: `gofmt -l` and `goimports -l` write
their file list to stdout and their errors to stderr, and **empty stdout is
what both "clean" and "never ran" look like**. Both call sites discarded the
exit status and sent stderr to `/dev/null`.

**Fixed in both.** `scripts/check.sh:143-176` (`grep -n 'gofmt_status=\|goimports_status=\|format_tool_failed' scripts/check.sh`) and `lefthook.yml`'s
`go-format` block at `lefthook.yml:54-99` (`grep -n '^    go-format:' lefthook.yml`).
Each tool's status is captured, stderr is folded into the captured output so the
reason is printable, and a non-zero status fails the stage/hook with the tool's
own message indented under an `ERROR:` line. `goimports`' `command -v` guard —
the asymmetry the reviewer named — is unchanged; a *missing* `goimports` is
still an announced note, a *failing* one is now a failure. The stage's file
count is now labelled `(tracked + untracked, per git ls-files)`, because that is
what it counts; the status checks are what make it mean anything.

`lefthook.yml`'s `go-format` was **deliberately out of scope in the first pass**
and the reviewer correctly called it pre-existing rather than introduced. The
Orchestrator put it in scope with a reason worth recording, because it is the
stronger version of the argument: this task's premise is that commit time is
formatting only, so a formatting check that can pass vacuously means commit time
gates **nothing at all**. That makes it load-bearing for `08`'s own design, not
adjacent cleanup. Agreed and done.

**Proof after the fix** — same five inputs, plus the two controls that matter:

```
scripts/check.sh   clean tree                          format OK
scripts/check.sh   tracked file deleted from worktree  FAIL — "gofmt did not run cleanly (exit 1)"
                                                              stat internal/brand/brand.go: no such file or directory
scripts/check.sh   gofmt absent from PATH              FAIL — "gofmt did not run cleanly (exit 127)"
                                                              xargs: gofmt: No such file or directory
scripts/check.sh   unparseable .go file                FAIL — "gofmt did not run cleanly (exit 1)"
                                                              broken.go:3:14: expected ')', found '{'
lefthook go-format clean staged .go file               exit=0
lefthook go-format gofmt absent from PATH              exit=1 — "sh: gofmt: command not found"
lefthook go-format unparseable staged .go file         exit=1 — exit 2, parse error printed
lefthook go-format misformatted staged .go file        exit=1 — "Unformatted files:" (08's own criterion, unregressed)
```

`internal/brand/brand.go` was restored with `git checkout --` and verified
byte-for-byte (`shasum` → `9414b1ef091e7ee291281d94f77e890e805c271f`, matching
the pre-deletion reading).

### Finding 2 — live documents describing a commit-time vet/lint gate

Every line number below was re-derived at `6c139038` before editing; all four the
review cited were still accurate.

| file:line (derived) | was | now |
|---|---|---|
| `README.md:37` | *"the pre-commit format/lint/vet/migration checks"* | *"the pre-commit format/migration checks and the `main`-scoped pre-push test run"* |
| `.nanite/agents/backend.md:386` | *"**Vet:** `go vet ./...` (via lefthook pre-commit)"* | states plainly that **nothing runs it for you at commit time**, and names the two places that do |
| `.nanite/agents/reviewer-backend.md:223` | *"use only when reproducing the pre-commit hook"* | the landing check's lint stage, with its real `--new-from-rev` form, plus *"there is no commit-time lint invocation to reproduce"* |
| `TASKS/audit-remediation/PREVENTION.md:123` | `go vet` *"is already in `lefthook.yml` pre-commit"* | corrected, and points at where the enforcement actually lives |
| `TASKS/audit-remediation/PREVENTION.md:124` | enforcement point *"`lefthook.yml` go-vet"* | the nightly gate's ratcheted `govet`, plus the landing check's `vet` stage |

`backend.md:387` (`gofmt`/`goimports` *"via lefthook pre-commit"*) is still true
and was **not** touched, as instructed. `PREVENTION.md` changed exactly two
lines — `git diff --stat` → `4 ++--`, i.e. 2 insertions / 2 deletions.

**Two more live claims, in a file already in scope, fixed beyond the four.** The
broadened grep (below) surfaced `.nanite/agents/backend.md:385` (*"Legacy
pre-commit lint: `golangci-lint run --new --timeout 30s` (lefthook pre-commit…)"*)
and `:419` (*"Pre-commit hooks via lefthook: `gofmt`, `goimports`,
`golangci-lint --new`, `go vet` (parallel)"*). Both are the same defect as
`:386`, in the same per-agent boot-context file, and `:419` is the most explicit
false hook-set list in the repo. Fixing `:386` while leaving them would have left
the file self-contradicting itself two paragraphs later and still teaching an
agent to skip `go vet`. `:385` now describes the landing check, a new `:387`
carries the real scoped-lint invocation, and `:420` (was `:419`) states the hook
set that exists. Recorded here because it is beyond the dispatch's enumerated
four, not because it was ambiguous.

**`README.md` also gained a pointer** (`README.md:44-46`) to
`./scripts/check.sh`. Removing the false claim alone would have left a new
contributor's Quick Start implying vet and lint simply stopped happening. Three
lines; judgment call, logged.

**Verified before writing it, not carried from the dispatch.** The claim that the
nightly gate's `govet` is strictly broader than `go vet ./...`:

```
go tool vet help | awk '/^Registered analyzers:/{f=1;next} f && /^    [a-z]/{print $1}' | sort -u   -> 35
golangci-lint config verify --config <govet enable-all probe>   # enum in the rejection message -> 45
comm -23 vet-default.txt govet-all.txt        -> 0 analyzers in go vet but not in govet
comm -13 vet-default.txt govet-all.txt        -> 10 extra: fieldalignment (disabled) + atomicalign
                                                 deepequalerrors findcall httpmux nilness
                                                 reflectvaluecompare shadow sortslice unusedwrite
grep -H '^lostcancel$' vet-default.txt govet-all.txt   -> present in both
```

So: superset by 9 live analyzers, `lostcancel` included. `lostcancel` is the
analyzer behind this row's own cited diagnostic — its message is *"the cancel
function is not used on all paths"*, which is verbatim what `PREVENTION.md:122`
quotes for `GO-LIFE-001`. The enforcement is a **ceiling ratchet**, not a report:
`scripts/quality-ratchet.py:116-140`'s `compare_counts` returns 1 on any
increase, so one new `lostcancel` finding lifts `govet` past its committed
baseline in `.github/quality/full-repo-baseline.json` and fails the run.

**Correction to the review's own citation.** The Review notes say *"the nightly
gate enables `govet` with `enable-all: true` minus `fieldalignment`"* without
naming the config. The gate does **not** read the repo-root `.golangci.yml`; it
reads `docs/audits/2026-08-21-go-quality/audit-golangci.yml`
(`.github/workflows/full-repo-quality.yml:130`). The two files happen to carry
identical `govet` settings (`.golangci.yml:111-116` and
`audit-golangci.yml:82-85`), so the reviewer's conclusion holds — but a reader
following the sentence would open the wrong file. `PREVENTION.md:123` names the
one the gate actually uses.

### Finding 2, second half — the criterion's own instrument

*Done means* now carries a broadened straggler grep in place of
`grep -rn 'go-lint\|go-vet' --include='*.md' .`, which matched only the
hyphenated command names and structurally could not find `format/lint/vet` or
`via lefthook pre-commit`. That is why the criterion read as passing while four
documents stayed wrong.

**The new instrument was itself given a positive control** (§4.1 — an assertion
never observed firing is of unknown strength). Run against the pre-fix tree with
`git grep … 6c139038`, so no checkout was needed:

```
git grep -niE 'pre-?commit|lefthook|commit[ -]time' 6c139038 -- '*.md' '*.yml' '*.yaml' '*.sh' |
  grep -v node_modules | grep -Ei 'go-lint|go-vet|[^a-z-]vet|lint|golangci|staticcheck|errcheck'
```

finds **all four** review-named documents plus `backend.md:385` and `:419`. The
old narrow form, on the same tree, finds exactly one of them —
`PREVENTION.md:124` — and only incidentally, because that line happens to contain
the literal string `go-vet`. It misses `README.md:37`, `backend.md:385/386/419`,
`reviewer-backend.md:223` and `PREVENTION.md:123` entirely.

**What was searched, and every hit triaged.** The broadened grep on the fixed
tree returns **46** lines (`… | wc -l`). By bucket:

- **5 hits are this pass's own corrected text** — `README.md:44`,
  `PREVENTION.md:123`, `backend.md:386`, `backend.md:420`,
  `reviewer-backend.md:223`. (`PREVENTION.md:124` no longer matches at all: it no
  longer names a hook.)
- **6 hits are this task file** quoting the old hook set as the thing being
  fixed. Left, by the same rule the first pass applied to the stale-`4.32s` list.
- **~28 hits are historical records** — dated audit reports
  (`docs/audits/2026-08-21-go-quality/**`, `docs/audits/2026-04-11-**`, the
  latter still citing an `.agentrc/` path that no longer exists), completed task
  files (`TASKS/audit-remediation/12-…/01-full-repo-scheduled-lint-gate.md`, 11
  hits, whose `lefthook.yml:27-31` citations were already stale before this
  batch), dated reviewer verdicts (`TASKS/ESCALATIONS.md:872`,
  `TASKS/skills/01:319`), and `adr/ADR-021`, which describes the state *before*
  hooks existed. A record of what was true when it was written is not a straggler.
- **7 hits are live-reading claims I did not fix** — see the parking list below.

### Finding 3 — `--help`

Both halves reproduced at `6c139038` before touching anything:

```
cd internal && ../scripts/check.sh --help
  -> sed: ../scripts/check.sh: No such file or directory
  -> exit=0, 0 lines of help
```

`cd "$(git rev-parse --show-toplevel)"` ran before the help branch, so `$0` was
re-resolved against the repo root; and `--help` exited 0 unconditionally, so
reading nothing succeeded.

**Fixed.** `scripts/check.sh:54-78` (`grep -n '^self=\$0' scripts/check.sh` → 57):
`$0` is resolved to an absolute path **before** the `cd` (absolute / contains a
slash / bare-name-on-`PATH`, all three handled), the help branch now runs before
the `cd` — so `--help` also works outside a git repo — and the hardcoded
`sed -n '2,50p'` is replaced by an `awk` pass that prints from line 2 and stops
at the first line that is not a `#` comment. It derives the end from the header's
actual end. Then it asserts the read produced something and exits **1** if not.

**Proof, both properties:**

```
./scripts/check.sh --help | wc -l                              -> 49
awk 'NR>1 && !/^#/{print NR-1; exit}' scripts/check.sh         -> 50   (49 = lines 2..50)
cd internal          && ../scripts/check.sh --help | wc -l     -> 49, exit 0
cd internal/store/migrations && ../../../scripts/check.sh --help | wc -l  -> 49
cd ui/src            && bash ../../scripts/check.sh --help | wc -l       -> 49
```

Header grown by six lines in place, last of them `# LAST HEADER LINE OF THE
PROOF BLOCK.`: header end moved to file line **56**, `--help` printed **55**
lines, and its last line was `LAST HEADER LINE OF THE PROOF BLOCK.` — from the
repo root and from `internal/` alike. Restored with `command cp -f` and verified
byte-for-byte (`shasum` before `1d7b1d0c0b30bc66f55215fd2360f8b65cff06c6`, after
identical), then `--help` re-confirmed at 49 lines.

**The new assertion was observed failing.** A copy of the script with its entire
leading comment block stripped:

```
ERROR: --help read no header lines from '<path>'.
       The help text is broken, not empty.
exit=1
```

### Glossary

`docs/engineering/GLOSSARY.md:104` — a **Landing check** entry, matching the
file's `**Term** (`path`) — prose` format. Checked for collision first: before
this pass `grep -ni 'quality gate\|landing check' docs/engineering/GLOSSARY.md`
returned nothing, so neither term had an entry. The entry defines the landing
check and disambiguates it from the quality gate **by trigger**, as instructed —
manual/local/on-request versus scheduled (`cron: "17 7 * * *"`) +
`workflow_dispatch`/in-CI — and notes that their contents diverge *because* of
that trigger difference, not independently of it. Triggers re-derived from
`.github/workflows/full-repo-quality.yml:3-6`. It also records that neither is a
*merge* gate, since this repo has no branch protection.

### Two hazards appended to the discipline doc

`docs/engineering/agent-verification-discipline.md` §3 says to append newly found
hazards there rather than to one's own prompt, so:

- **§3.8** — `mkdir` is aliased to `mkdir -pv` in this environment
  (`type mkdir` → `mkdir is an alias for mkdir -pv`). It writes the directory
  name to stdout. This cost one wrong reading mid-pass (below). Note §3.4's
  claim about `cp` did not reproduce in the same shell: `type cp` → `/bin/cp`,
  and `alias | grep '^cp='` is empty. §3.4's *advice* (`command cp -f`, always
  verify a restore) was followed anyway and the restore was verified.
- **§3.9** — the `gofmt -l` silent-failure shape from Finding 1, so the next
  person writing a formatting check does not re-derive it from a third incident.

### Corrections to my own numbers, mid-flight

- **A zero that was my command, not the world.** My first attempt to enumerate
  `go vet`'s analyzers —
  `go tool vet help | sed -n '/^Registered analyzers:/,/^$/p'` — returned **0**
  names, and I reported `comm` output against that empty set before noticing.
  The range terminated on the blank line that immediately *follows* the
  `Registered analyzers:` header. Rewritten as an `awk` state machine, sanity-
  checked against a name whose presence I already knew (`grep -c '^printf$'` → 1),
  and only then used. Every govet/vet count above comes from the corrected form.
- **A stray `internal/checkproof` line** in an early proof block looked like
  `gofmt` output and briefly read as a `gofmt -l` result. It was `mkdir -pv`.
  Re-derived with stdout and stderr separated; that is §3.8 above.
- **Hazard §3.2 does not apply in this checkout, and I assumed it did.**
  `.claude/worktrees/` exists, but `ls -la` shows it holds only symlinks to other
  repos, `grep -r` does not follow symlinks, and `git worktree list` reports one
  entry. No recursive command in this pass walked a nested copy.

### Scope-parked — found, deliberately not fixed

1. **`TASKS/INDEX.md:1503-1504`** — *"`nilerr` is enabled today; it never gated
   because the pre-commit hook runs `golangci-lint run --new`"*, present tense.
   That hook no longer exists. **Fenced — the Orchestrator's file.**
2. **`TASKS/ESCALATIONS.md:1001-1002`** and
   **`TASKS/audit-remediation/README.md:155`** — the *same sentence*, two more
   copies. Not fixed **on purpose**: the third copy is the fenced one, and
   repairing two of three would leave the trio inconsistent. All three want one
   edit by whoever owns `INDEX.md`.
3. **`docs/audits/2026-08-21-go-quality/audit-golangci.yml:15`** — a comment
   reading *"NOT used by lefthook's pre-commit `go-lint`"*. Vacuously still true,
   but it names a retired hook, and unlike the rest of that directory this file
   is **live**: the nightly gate reads it (`full-repo-quality.yml:130`).
4. **`.nanite/agents/reviewer-backend.md:22`** — lists `lefthook` pre-commit in a
   tooling inventory. Not false (the hook exists; it runs formatting), so left.
5. **`skip_empty`** — untouched, as fenced. `lefthook validate` exits **1** both
   at `6c139038` and after this pass, with the identical four complaints; my edit
   added no new validation error.
6. **`frontend-lint`** — left `skip: true` (`CW-20260816-0087`), as fenced.

### Baseline, at the end of this pass

```
go build ./cmd/nanite/                       exit 0
go vet ./...                                 exit 0
/usr/bin/time -p ./scripts/check.sh          exit 0
    format  OK   1354 Go files (tracked + untracked, per git ls-files)
    vet     OK
    lint    ---  examined nothing  (0 changed .go files vs 77137106 — this
                                    pass's diff contains no Go file)
    test    OK   go test ./...  (Tier 1)
lefthook validate                            exit 1 — pre-existing, unchanged
```

Two runs of the landing check, both warm, both exit 0: `real 20.88` and
`real 21.84` (`/usr/bin/time -p ./scripts/check.sh`). Quoted as a pair rather
than as one figure precisely because a single number here would be re-quoted as
if it were stable; per-stage seconds vary by a second or two between runs. Well
inside the operator's 30s-to-two-minutes budget either way.

`go test ./...` ran as the landing check's `test` stage and passed. No migration
was touched: `git status --porcelain internal/store/migrations/` is empty.
