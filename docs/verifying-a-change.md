# Verifying a change

Four tiers run against this repo, and they check different things on purpose. A
change that passes one has not been checked by the others, and the gap between
them is where the expensive mistakes live.

`AGENTS.md` lists the commands and says when to run them. This says what each
one covers, and — the part that matters more — what it does not.

## The tiers

| Tier | Trigger | Scope |
|---|---|---|
| Formatting | `pre-commit`, every commit | the staged diff |
| The landing check | `./scripts/check.sh`, by hand | the whole tree, except lint |
| Push gate | `pre-push`, pushes to `main` only | the whole tree |
| Full-repo gate | scheduled, daily | the whole tree, plus analysis nothing else runs |

The first two rows exist only after `lefthook install`; `README.md` carries that
step and the command that confirms it took.

## Commit time is formatting

The `pre-commit` stages format Go, check migration purity, and lint the
frontend, all scoped to what you staged. `scripts/check.sh`'s own header
explains why they stay that narrow, and it is worth the thirty seconds — the
argument is about what makes a hook survive rather than about formatting.

**A clean commit proves your diff is formatted. It proves nothing about whether
the tree builds.**

## The landing check is the one you actually run

`./scripts/check.sh` runs four stages — format, `go vet ./...`, `golangci-lint`,
and `go test ./...` — and names each stage that failed rather than stopping at
the first.

Its lint stage measures **from the merge base with `origin/main`**, not across
the tree; the script's header gives the reasoning. The consequence is the part
to carry away — **the landing check cannot tell you the repository is clean,
only that you did not make it worse.**

Its test stage is the suite without `-race`.

## The push gate is scoped to `main`

Three stages run on `pre-push`: the migration-number guard, the test suite, and
the comparator's own tests. All three are conditioned on the branch, so a push
to a work-in-progress branch pays for none of them and a push to `main` pays for
all.

Read a skipped stage on a feature branch as "not applicable here", not as
"passed". They are the same line of output.

The migration-number guard is the exception worth knowing: a duplicate migration
number is this repo's one unrecoverable failure, so that stage carries no file
filter. A filter there would fail open and print as a benign skip — which is the
shape of every trap on this page.

## The full-repo gate, and what a green run proves

The scheduled gate runs what nothing else does: a linter ratchet against a
committed baseline, a vulnerability scan, a security scan run twice with a
known-noise policy applied, module verification, dead-code reporting, and the
aggregate suite under `-race`.

It opens by discovering the tracked Go package list and asserting that list
matches a committed shape. That assertion exists because every scanning step is
handed that list, and only two of them have a comparator behind them: if the
list silently shrinks, the rest scan less and still report success. **A gate
that examined nothing prints the same bytes as a gate that found nothing.**

What a green run proves: no *new* findings against the baseline, in the
categories the baseline covers, on the package list it was given.

What it does not prove:

- **That the code is clean.** The ratchet compares against a recorded floor. A
  finding that predates the baseline is not a regression and will not be
  reported.
- **That a dropped count is an improvement.** A finding count that falls can
  mean the defect was fixed or that the scan covered less. Those are
  indistinguishable in the report and a repeat run is what separates them.
- **That a suppression is justified.** Suppressions are enumerated rather than
  counted, because a count of four is satisfied by any four sites, including
  four that are not the ones anyone agreed to.
- **Anything about today.** The gate reports on the tree it ran against, on a
  schedule. Its result is a fact about a run, not a property of the repository.

If the gate is failing, that is a fact about a run and belongs in the tracker —
not in this file, and not in a status line anyone has to remember to update.

## Concurrency needs more than any tier gives you

The `-race` suite runs once per gate run, and once is a weak signal for a defect
that is timing-dependent by definition. A change touching goroutines, channels,
`context` cancellation, mutexes, atomics or shutdown ordering wants `-race` with
a high repeat count on the package you touched, run by you, before it lands.

`-race` and a high `-count` are different instruments rather than two settings
of one dial: the first changes what the runtime observes, the second changes how
many chances it gets to observe it. A single `-race` run and twenty un-raced
ones both leave the interesting window unexamined.

## What this does not cover

- **The UI build and the envelope generators.** `make build` and
  `make check-envelopes` have their own failure modes and are not part of any
  tier above.
- **Deployment.** `go build` produces a binary that is not the one the running
  service executes; `AGENTS.md` carries that boundary.
- **Whether the change is correct.** Every tier here checks that you did not
  break something already known. None of them reads your diff.
