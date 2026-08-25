# Move whole-repo analysis off commit time and into a landing script

**Phase:** A — Remove the friction
**Status:** not-started
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

## Review notes
