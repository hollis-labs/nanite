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
  hook set, and it is the one that now exists. Grep for stragglers:
  `grep -rn 'go-lint\|go-vet' --include='*.md' . | grep -v node_modules | grep -v TASKS/gate-integrity`
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

## Review notes
