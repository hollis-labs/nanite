# Drop the last two replaces and delete the sibling-checkout block entirely

**Phase:** 2 — Sibling decoupling, completion
**Status:** implemented
**Depends on:** `01` (edits the same two files) and `02` (its tags must exist
on the proxy). Both are **real** dependencies, not sequencing preferences:
without `02`'s tags this task cannot resolve the modules at all, and without
`01` it conflicts in both files it edits.
**Touches:** `go.mod`, `go.sum`, `.github/workflows/full-repo-quality.yml`.
Repo: nanite.

## Context

This task ends the pinned-sibling coupling. After `01` removed two replaces
and `02` published the tags the other two were waiting on, all four `replace`
directives and all four `actions/checkout` steps can go, and the workflow stops
checking out any sibling repository at all.

The coupling being removed, restated once so this task stands alone: the
workflow pins sibling SHAs, `go.mod` redirects the modules at those checkouts,
and therefore **the pin decides what CI validates — not `go.mod`, not the
published tag.** Nothing in either file made that visible, and it cost a full
CI cycle once already when a pin sat one commit behind a release.

Why deleting beats checking: a drift check would have to be maintained, would
fire only after someone had already introduced the drift, and leaves the
underlying "CI validates unreleased source" property intact. Removing the
replaces makes the pins unnecessary, which makes the drift impossible rather
than detectable. `[[nanite_workflow_pinned_sibling_refs]]`

## What to do

1. **Gate check first.** Confirm `02` actually published:
   ```
   for m in go-harness-filters go-runtime-events; do
     printf '%-20s ' "$m"
     curl -sS "https://proxy.golang.org/github.com/hollis-labs/$m/@v/list" | tr '\n' ' '; echo
   done
   ```
   Both must list `v0.1.1`. If not, stop — this task is blocked, not slow.

   **Ask the proxy over HTTP.** `go env GOPRIVATE` is `github.com/hollis-labs/*`,
   which defaults `GONOPROXY` to the same value and overrides a `GOPROXY=`
   prefix, so `go list -m -versions` answers from git and never consults the
   proxy — it returns the same "yes" whether or not the proxy has the tag.
   proxy.golang.org populates lazily on first request, so that difference is
   real for minutes after a push. See `agent-verification-discipline.md` §3.11.
2. Remove the `replace (...)` block containing `go-harness-filters` and
   `go-runtime-events`, along with the `TASKS/agent-host-acp/06` comment above
   it (which exists solely to explain why those replaces are needed).
3. Bump both `require` lines from `v0.1.0` to `v0.1.1`. Note that
   `go-harness-filters` is currently marked `// indirect` and
   `go-runtime-events` is a direct require — `go mod tidy` will sort out which
   is which; do not hand-edit the `// indirect` markers.
4. Delete the `Check out go-harness-filters` and `Check out go-runtime-events`
   steps. Then check whether the surrounding comment at the top of the checkout
   block (at `77137106`, `.github/workflows/full-repo-quality.yml:28`, *"Nanite's
   go.mod has four local replaces at ../../libs/<module>."*) still describes
   anything real. After this task it does not — remove it rather than
   amending it to describe a smaller number.
5. Run `go mod tidy`. Expect `go.sum` churn for all four modules.
6. Verify the workflow no longer creates a `libs/` directory at all, and that
   nothing else in it assumes one exists:
   `grep -n 'libs/' .github/workflows/full-repo-quality.yml`.

## Done means

- `grep -c ' => \.\./\.\./libs/' go.mod` returns `0` — no replace points into
  the sibling tree.
- `grep -c 'hollis-labs/go-modelsdev\|hollis-labs/go-envelopes\|hollis-labs/go-harness-filters\|hollis-labs/go-runtime-events' .github/workflows/full-repo-quality.yml`
  returns `0`.
- `grep -n 'libs/' .github/workflows/full-repo-quality.yml` returns nothing.
- `go build ./...` and `go test ./...` pass **with the entire
  `~/dev/hollis-labs/libs/` tree moved aside.** This is the acceptance test that
  matters and the only one that actually proves decoupling; a build run with
  the siblings present cannot distinguish success from the replaces still
  silently working. Restore the tree afterwards.
- `go mod tidy -diff` exits clean.
- **A hand-dispatched gate run is green** —
  `gh workflow run "Full-repo quality gate" --ref main` — with the run URL and
  its conclusion recorded in the Work log. This is the first gate run in which
  CI resolves every sibling from the proxy; a local pass does not substitute
  for it. Ignore an `internal/memory` `SQLITE_BUSY` failure (`CW-20260825-0001`).

## Work log

**Implemented 2026-08-25.** Starting commit `51a10cc6`, branch `main`, working
tree clean (`git status --porcelain` -> empty). `origin/main` was 7 behind
(`git rev-list --left-right --count origin/main...main` -> `0	7`). Landed as
`834c9506`; `main` pushed `22d312be..834c9506`.

Method deviation, on the operator's instruction: **the `~/dev/hollis-labs/libs/`
tree was not moved aside.** A parallel session was live inside those
repositories, and moving the tree would have pulled a directory out from under
it. Nothing in this session read, wrote, built or ran git against that path.
The substitute is strictly stronger for this task — see "Decoupling" below.

### Step 1 — the gate, asked over HTTP

```
for m in go-harness-filters go-runtime-events; do printf '%-20s ' "$m"
  curl -sS "https://proxy.golang.org/github.com/hollis-labs/$m/@v/list" | tr '\n' ' '; echo; done
#   go-harness-filters   v0.1.0 v0.1.1
#   go-runtime-events    v0.1.0 v0.1.1
```

Checked for the version string, not for exit status — `curl -sS` without `-f`
exits 0 on a 404. `go list -m -versions` was deliberately not used
(`agent-verification-discipline.md` §3.11).

### Why `v0.1.1` was load-bearing, confirmed rather than assumed

```
grep -rn 'hrepair\.Chain' $(go env GOMODCACHE)/github.com/hollis-labs/go-agent-wrapper@v0.8.1/filters/repair_pipeline.go
#   19:	return RepairPipeline{Repairer: hrepair.Chain(repairers)}
grep -rn '^type Chain' $(go env GOMODCACHE)/github.com/hollis-labs/go-harness-filters@v0.1.0/   # -> no output
grep -rn '^type Chain' $(go env GOMODCACHE)/github.com/hollis-labs/go-harness-filters@v0.1.1/
#   repair/repair.go:61:type Chain []Repairer
```

Published `go-agent-wrapper v0.8.1` calls a symbol that does not exist at
`go-harness-filters v0.1.0`. That is the real reason these two replaces could
not be dropped alongside the other two in `01`.

### Files changed

| File | Change |
|---|---|
| `go.mod` | Deleted lines 95-108 (the `TASKS/agent-host-acp/06` comment, the `replace (...)` block, its trailing blank). Bumped line 39 `go-harness-filters` and line 102 `go-runtime-events` from `v0.1.0` to `v0.1.1`. |
| `go.sum` | `go mod tidy` — 4 lines added, 0 removed. |
| `.github/workflows/full-repo-quality.yml` | Deleted lines 35-48 (both sibling checkout steps) and lines 28-29 (the `../../libs/<module>` geometry comment). |
| `docs/engineering/runbooks/full-repo-quality-gate.md` | Rewrote lines 12-21 — the authorized scope addition. |

All line numbers derived at `51a10cc6` with
`grep -n 'hollis-labs\|^replace' go.mod` and
`grep -n 'libs/\|checkout\|repository:\|ref:\|path:\|name:' .github/workflows/full-repo-quality.yml`,
then re-confirmed with `awk 'NR>=a && NR<=b {printf "%d: %s\n", NR, $0}'` before
each edit. **None had drifted** from the task file's citations.

### Done means — re-derived at `834c9506`, each with a positive control

| Check | Result | Positive control at `51a10cc6` |
|---|---|---|
| `grep -c ' => \.\./\.\./libs/' go.mod` | `0` | `git show 51a10cc6:go.mod \| grep -c ...` -> `2` |
| `grep -c '<the four module names>' <workflow>` | `0` | same form at `51a10cc6` -> `2` |
| `grep -c 'libs/' <workflow>` | `0` | same form at `51a10cc6` -> `3` |
| `grep -c '=>' go.mod` (any replace at all) | `0` | same form at `51a10cc6` -> `2` |
| `grep -c 'actions/checkout' <workflow>` | `1` | same form at `51a10cc6` -> `3` |

Every zero here was checked against a command proven to fire (§1.3). `go.mod`
now carries **no `replace` directive of any kind**, not merely no `libs/` one.

### Decoupling — proven by fresh clone, with the failure mode observed first

`git clone` of the local repo into the session scratchpad, at a path with **no
`libs/` directory anywhere above it**. From the clone, `../../libs` resolves to
`<scratchpad>/libs`, confirmed absent — as were `<clone>/libs`,
`<clone>/../libs` and `<scratchpad>/decouple/libs`.

**Negative control first (§4.1).** The two `replace` lines were appended back
onto the *clone's* `go.mod` and `go build ./...` run:

```
/…/go-agent-wrapper@v0.8.1/adapters/adapter.go:4:2: github.com/hollis-labs/go-runtime-events@v0.1.1:
    replacement directory ../../libs/go-runtime-events does not exist
/…/go-agent-wrapper@v0.8.1/filters/repair_pipeline.go:6:2: github.com/hollis-labs/go-harness-filters@v0.1.1:
    replacement directory ../../libs/go-harness-filters does not exist
```

That is what this proof produces when decoupling has *not* happened. Restored
with `git checkout -- go.mod` and verified byte-for-byte
(`shasum -a256` identical to the main tree's, `git status --porcelain` empty).

**Positive proof**, in the restored clone, with a **fresh empty `GOMODCACHE`**
and the privacy overrides genuinely cleared:

```
GOMODCACHE=<scratch>/gomodcache2 GOPRIVATE=none GONOPROXY=none GONOSUMDB=none \
  GOSUMDB=sum.golang.org GOPROXY=https://proxy.golang.org,direct <cmd>
```

| Command | Exit |
|---|---|
| `go build ./...` | `0` (every dependency downloaded from proxy.golang.org) |
| `go vet ./...` | `0` |
| `go mod verify` | `0` — `all modules verified` |
| `go mod tidy -diff` | `0` |
| `go test ./...` | `0` — 99 `ok` packages, 0 `FAIL` |
| `git status --porcelain` after all of it | empty |

`go list -m` in that clone, for all four modules:

```
github.com/hollis-labs/go-envelopes        v0.3.0  replace=(none)  dir=<scratch>/gomodcache2/…@v0.3.0
github.com/hollis-labs/go-modelsdev        v0.2.0  replace=(none)  dir=<scratch>/gomodcache2/…@v0.2.0
github.com/hollis-labs/go-harness-filters  v0.1.1  replace=(none)  dir=<scratch>/gomodcache2/…@v0.1.1
github.com/hollis-labs/go-runtime-events   v0.1.1  replace=(none)  dir=<scratch>/gomodcache2/…@v0.1.1
```

No `dir` is under `~/dev/hollis-labs/libs/` or under the developer's own
`GOMODCACHE`. This is the fresh-clone case the batch exists to deliver, run
directly rather than simulated.

### The gate run

**`https://github.com/hollis-labs/nanite/actions/runs/32864133579` — conclusion
`success`**, `workflow_dispatch --ref main`, head SHA `834c9506`, 15:10:27Z ->
15:27:12Z. All **17** steps `success`, including `Verify modules` (`go mod
verify` + `go mod tidy -diff`) and `Assert the discovered package list matches
the committed shape` (`--expected 109`). `Check out Nanite` was the only
checkout step. No `SQLITE_BUSY` in the log
(`gh run view 32864133579 --log | grep -c 'SQLITE_BUSY'` -> `0`), so
`CW-20260825-0001` did not fire.

Per the runbook's "How to report a gate result honestly": this green means no
regressions in any asserting step at the package count the baseline was
calibrated on. It is **the first gate run in which CI resolved every dependency
from the module proxy** rather than from pinned sibling checkouts.

### Corrections made to my own work mid-flight

1. **`GOPRIVATE=` (empty) does not clear `GOPRIVATE`.** Go falls back to the
   `GOENV` file (`go env GOENV` -> `~/Library/Application Support/go/env`,
   which sets `GOPRIVATE=github.com/hollis-labs/*`). Only a non-empty sentinel
   works:

   ```
   GOPRIVATE=     go env GOPRIVATE   # -> github.com/hollis-labs/*
   GOPRIVATE=none go env GOPRIVATE   # -> none
   ```

   My first `go mod tidy` and first clone download were run with `GOPRIVATE=`
   and so did **not** have the proxy or the checksum DB enforced, contrary to
   what I claimed at the time. Both were re-run with `=none`; `go mod tidy
   -diff` exits `0` and `go mod verify` reports `all modules verified` with the
   sumdb genuinely in force, so the committed `go.sum` is what a
   proxy-resolved, sum.golang.org-verified tidy produces. This also affects
   `agent-verification-discipline.md` §3.11, whose stated escape hatch is
   `GOPRIVATE= GONOPROXY=none …` — the `GONOPROXY=none` half works, the
   `GOPRIVATE=` half is inert, so `GONOSUMDB` stays set and the checksum DB
   stays bypassed. Reported, not edited (§6).

2. **`Origin.VCS=git` in a cached `.info` does not mean a direct VCS fetch.**
   I first read it as evidence the cache had bypassed the proxy. It does not
   discriminate: proxy.golang.org serves that same field, and
   `curl -sS https://proxy.golang.org/github.com/hollis-labs/go-harness-filters/@v/v0.1.1.info`
   returns a byte-identical document. §3.11's closing paragraph implies
   otherwise. Reported, not edited.

3. **A 363-line `go mod tidy -diff` in the clone was my own contamination.**
   I ran `go mod download all` first, which **writes `go.sum`** — it grew the
   clone's `go.sum` from 240 to 450 lines, and `tidy -diff` then correctly
   reported 210 removable lines. I spent several checks hunting a phantom
   difference between the clone and the main tree (package sets: 110 vs 109,
   the known `flatted` stray, §3.5 — ruled out by copying the file into the
   clone and seeing the diff unchanged) before `git status` in the clone showed
   ` M go.sum`. Restored with `git checkout -- go.sum`, verified by `shasum`,
   and the clean re-run exits `0`. **Do not run `go mod download all` before
   `go mod tidy -diff` in a verification clone.**

4. **The task file's step 5 expects "`go.sum` churn for all four modules."**
   Only two churned — 4 lines added, 0 removed. `01` (`f08ac62a`) already added
   the `go-envelopes` and `go-modelsdev` entries when it dropped their
   replaces; a module under a local `replace` has no `go.sum` entry at all, so
   each pair lands in the task that removes its own replace.

5. `$?` after a pipeline in this `zsh` reports the **last** command's status,
   not the first — `cmd | head; echo $?` reported `head`'s `0` twice early on.
   Re-run with redirection to a file instead of a pipe. (`$pipestatus`, not
   `$PIPESTATUS`, in zsh.)

### Scope parked — found, deliberately not done

- **`docs/engineering/runbooks/full-repo-quality-gate.md:23-29`** ("Release
  resolution: …") still ends *"and the workflow pins that exact release
  commit."* There is no pin now. My scope addition was fenced to the paragraph
  above it, so this was left. For `06`.
- **`agent-verification-discipline.md` §3.11** — corrections 1 and 2 above both
  land in that section. Left per §6.
- **The pre-push `go test` hook did not run for this push.** `lefthook` reported
  `go-test (skip) no matching push files` for a push whose only changes were
  `go.mod`, `go.sum`, a workflow and a doc. A `go.mod` change is among the most
  likely to break a build and it is not covered by the hook's file glob.
  `go test ./...` was run by hand instead (exit `0`, 99 `ok`).
- **The workflow still checks Nanite out at `apps/nanite`** with a matching
  `defaults.run.working-directory`. That path existed to preserve the
  `../../libs` geometry and no longer needs to, but flattening it touches
  `go-version-file`, `cache-dependency-path` and the artifact paths. Not
  required by this task and not done.

## Review notes
