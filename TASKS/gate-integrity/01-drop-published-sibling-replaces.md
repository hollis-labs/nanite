# Drop the go-envelopes and go-modelsdev replaces, and bump go-envelopes to the version actually being built

**Phase:** 1 — Sibling decoupling
**Status:** implemented
**Depends on:** none
**Touches:** `go.mod`, `go.sum`, `.github/workflows/full-repo-quality.yml`
(the `go-modelsdev` and `go-envelopes` checkout steps only — at `77137106`
those are lines 35-47; **re-derive**, task `04` edits the same file lower
down). Repo: nanite.

Closes Torque `CW-20260816-0090` ("Follow-up: release go-envelopes with
report-card session_link, drop local go.mod replace"), tagged
`blocking-merge`, open since 2026-08-16.

## Context

`.github/workflows/full-repo-quality.yml` checks four sibling repos into
`libs/` at hard-pinned SHAs, and `go.mod`'s `replace` directives point the
modules at those checkouts. The consequence is that **the workflow pin — not
`go.mod`, not the published tag — decides what CI validates against**, and
nothing in either file makes that visible.

It has already cost a full CI cycle once: the US-English migration changed the
`session-task` enum in go-envelopes and released v0.3.0, and CI still failed
against the *old* enum because the pin sat one commit behind.

**The planning session's correction: for two of the four siblings this is
removable outright, not something to monitor.** Both `go-envelopes` and
`go-modelsdev` are pinned to a SHA that is byte-identical to their newest tag,
both tags are published on the module proxy, and both sibling working trees
are clean — so the `replace` and the proxy resolve to the same source. Dropping
them is a no-op for what gets compiled, and it removes two of the four coupled
pins permanently.

Derived at `77137106`:

```
d=~/dev/hollis-labs/libs/go-envelopes
git -C $d status --porcelain | wc -l                 # 0  (clean)
git -C $d rev-list -n1 v0.3.0                        # 58243d84...  == the CI pin
GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/go-envelopes
#   -> v0.1.0 v0.1.1 v0.2.0 v0.3.0
grep -rn 'session_link' $d/manifest/                 # report-card.schema.json:73
```

That last line is `CW-20260816-0090`'s actual acceptance condition: the schema
addition the replace existed for is in a published release now.

Two related defects in the same file, both real at `77137106`:

- **`go.mod:20` requires `go-envelopes v0.1.1`** while the build runs v0.3.0's
  source through the replace. The recorded version is three minor versions
  stale and actively misleading — a reader checking "what version of
  go-envelopes does nanite use?" gets the wrong answer from the obvious place.
- **`go.mod:115` says *"Mirrors the go-agent-wrapper replace immediately
  above."*** There is no go-agent-wrapper replace:
  `grep -c 'go-agent-wrapper =>' go.mod` returns `0`. The comment is
  load-bearing prose in a block explaining why the *other* replaces must stay,
  so a stale pointer there is worse than a stale pointer in a doc.

## What to do

1. **Re-derive the pin/tag/proxy state before changing anything.** Run the
   three-command block above for both `go-envelopes` and `go-modelsdev`. If
   either sibling tree is dirty, or either pin no longer equals its newest
   tag, or either tag is absent from the proxy — **stop and escalate.** This
   task's whole safety argument is "the two sources are identical"; if that is
   no longer true, the task is not a no-op and needs re-scoping.
2. Delete the `replace github.com/hollis-labs/go-envelopes => ../../libs/go-envelopes`
   directive and the `CW-20260816-0069` comment block above it, and bump the
   `require` from `v0.1.1` to `v0.3.0`.
3. Delete the `replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev`
   directive. Its `require` is already `v0.2.0` — confirm, do not assume.
4. Delete the `Check out go-modelsdev` and `Check out go-envelopes` steps from
   the workflow. **Find them by name, not by the line numbers above** — task
   `04` edits the gosec step in the same file and may land first.
5. Fix the stale comment on the surviving `replace (...)` block. Do not write
   what it used to say or why it changed — state what is true now: these two
   siblings are pinned ahead of their published `v0.1.0` tags, so the replaces
   must stay until task `02`/`03` land. Name `TASKS/gate-integrity/03` as the
   thing that removes them.
6. Run `go mod tidy`. The gate runs `go mod tidy -diff`
   (`.github/workflows/full-repo-quality.yml`, "Verify modules" step) and will
   red on an untidied `go.sum`.

## Done means

- `grep -c 'go-envelopes =>\|go-modelsdev =>' go.mod` returns `0`.
- `grep -n 'go-envelopes' go.mod` shows `v0.3.0` and no replace.
- `grep -c 'go-agent-wrapper =>' go.mod` still returns `0`, and no comment in
  `go.mod` claims such a replace exists.
- `go build ./...` and `go test ./...` pass with **no** `libs/go-envelopes` or
  `libs/go-modelsdev` checkout present — the real proof. Verify by temporarily
  moving those two sibling directories aside, or by building in a clone that
  has no `libs/` sibling tree at all. A build that passes only because the
  sibling directory happens to sit at the replace path proves nothing.
- `go mod tidy -diff` exits clean.
- The workflow no longer names `hollis-labs/go-envelopes` or
  `hollis-labs/go-modelsdev`:
  `grep -c 'go-envelopes\|go-modelsdev' .github/workflows/full-repo-quality.yml`
  returns `0`.
- **A hand-dispatched gate run is green**, because the thing this task changes
  is precisely what CI resolves:
  `gh workflow run "Full-repo quality gate" --ref main`.
  Ignore an `internal/memory` `SQLITE_BUSY` failure — that is
  `CW-20260825-0001`, known and fixed upstream.
- `CW-20260816-0090` transitioned to `done` in Torque with a comment naming the
  landing commit.

## Work log

**Implemented 2026-08-25.** Starting commit `71154966`, branch `main`, working
tree clean (`git status --porcelain` -> empty), `origin/main` identical
(`git rev-parse HEAD` == `git rev-parse origin/main` == `71154966...`).
Landed as `f08ac62a`.

### Step 1 — preconditions re-derived. All four held; nothing was worked around.

```
for n in go-envelopes go-modelsdev; do d=~/dev/hollis-labs/libs/$n
  git -C "$d" status --porcelain | wc -l
  t=$(git -C "$d" describe --tags --abbrev=0); git -C "$d" rev-list -n1 "$t"; done
```

| | dirty lines | newest tag | tag SHA | workflow pin | proxy |
|---|---|---|---|---|---|
| `go-envelopes` | 0 | `v0.3.0` | `58243d84db7df1480960e2b747bea6ffca9fd6db` | same | `v0.1.0 v0.1.1 v0.2.0 v0.3.0` |
| `go-modelsdev` | 0 | `v0.2.0` | `7d932798b85145ec93f923e392f5d41762894e8c` | same | `v0.1.0 v0.2.0` |

Proxy column from
`GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/<n>`
(exit 0 for both).

**One thing the task file's block would have gotten wrong.** Both tags are
*annotated*, so `git ls-remote origin refs/tags/<t>` returns the tag object,
not the commit — `65939f99...` for `v0.3.0` and `222c848f...` for `v0.2.0`,
neither of which equals the pin. Peeled, they do:

```
git -C <d> ls-remote origin 'refs/tags/v0.3.0^{}'   # 58243d84... == the pin
git -C <d> ls-remote origin 'refs/tags/v0.2.0^{}'   # 7d932798... == the pin
```

Both siblings are also at `rev-list --count origin/main..HEAD` -> `0`, so the
local checkout is not ahead of what was published.

### Byte-identity, measured rather than argued

The safety argument is "the `replace` and the proxy resolve to the same
source", so it was measured directly: every git-tracked file in each sibling
checkout was sha256-compared against the same path in the proxy-served module
in `$(go env GOMODCACHE)` (`.github/**` and `.gitignore` excluded — the module
zip does not carry them).

```
go-envelopes v0.3.0   checked=50  differing=0  missing=0
go-modelsdev v0.2.0   checked=17  differing=0  missing=0
```

Per §1.3 a zero is a signal, so the comparator got a positive control:
`doc.go` compares equal as-is, and the same comparison against the file plus
one appended comment line reports unequal. The mechanism can see a difference.

`CW-20260816-0090`'s own acceptance condition also re-derived:
`grep -rn 'session_link' ~/dev/hollis-labs/libs/go-envelopes/manifest/` ->
`manifest/schemas/report-card.schema.json:73` — the schema addition the
replace existed for is in the published v0.3.0.

### Line numbers, re-derived at `71154966`

The task file's citations were derived at `77137106`, fourteen commits back.
Re-derived with `grep -n 'go-envelopes\|go-modelsdev\|go-agent-wrapper' go.mod`
and `grep -n 'name: Check out' .github/workflows/full-repo-quality.yml`:

| Citation | At `77137106` | At `71154966` | Drifted? |
|---|---|---|---|
| `require go-envelopes v0.1.1` | `go.mod:20` | `go.mod:20` | no |
| `replace go-modelsdev` | `go.mod:95` | `go.mod:95` | no |
| `replace go-envelopes` | `go.mod:102` | `go.mod:102` | no |
| *"Mirrors the go-agent-wrapper replace"* | `go.mod:115` | `go.mod:115` | no |
| `Check out go-modelsdev` / `go-envelopes` | wf `35-47` | wf `35-47` | no |

None had drifted. Found by name regardless, per the instruction.

`go.mod:21` was already `github.com/hollis-labs/go-modelsdev v0.2.0` —
confirmed, not assumed, so step 3 needed no `require` change.

### Files changed

- **`go.mod`** — bumped `require github.com/hollis-labs/go-envelopes` from
  `v0.1.1` to `v0.3.0`; deleted the `go-modelsdev` replace, the five-line
  `CW-20260816-0069` comment block and the `go-envelopes` replace; rewrote the
  surviving `replace (...)` block's comment.
- **`go.sum`** — `go mod tidy` added exactly four lines, the `h1:`/`go.mod`
  pair for each of `go-envelopes v0.3.0` and `go-modelsdev v0.2.0`. No
  removals: with a filesystem `replace` there were no hashes to remove.
- **`.github/workflows/full-repo-quality.yml`** — deleted the
  `Check out go-modelsdev` and `Check out go-envelopes` steps, found by step
  name. Also changed the standing comment above `Check out Nanite` from *"four
  local replaces"* to *"two"* (workflow line 28) — that sentence is the
  rationale for the whole checkout block and would otherwise have been wrong
  the moment the steps went.

The new comment on the surviving block states only what was derived here:

```
git -C ../../libs/<module> rev-list --count v0.1.0..HEAD   -> 1   (both)
go list -m -versions github.com/hollis-labs/<module>       -> v0.1.0 (both)
```

It names `TASKS/gate-integrity/02` (tags a release from each pin) and
`TASKS/gate-integrity/03` (removes the replaces and the two remaining checkout
steps). **The old comment's `undefined: hrepair.Chain` claim was deliberately
not carried forward** — it may well be true, but verifying it means dropping
those two replaces and building, which is `03`'s job, and restating an
unverified claim in a durable artifact is the thing §2.3 exists to stop. The
version-gap fact above is load-bearing on its own.

### "Done means", each line re-derived after the change

```
grep -cE 'go-envelopes =>|go-modelsdev =>' go.mod                       -> 0
grep -n 'go-envelopes' go.mod          -> 20: github.com/hollis-labs/go-envelopes v0.3.0
grep -c 'go-agent-wrapper =>' go.mod                                    -> 0
grep -c 'go-agent-wrapper replace' go.mod                               -> 0
grep -cE 'go-envelopes|go-modelsdev' .github/workflows/full-repo-quality.yml -> 0
go mod tidy -diff                                                       -> exit 0
go mod verify                                                           -> all modules verified
```

Four of those are zeros, so each got the §1.3 check — the same command run
against the `71154966` blobs via `git show HEAD:<file> | grep -c`:

```
'go-envelopes =>|go-modelsdev =>' in go.mod                  2 -> 0
'go-envelopes|go-modelsdev' in the workflow                  6 -> 0
'go-agent-wrapper =>' in go.mod                              0 -> 0
'go-agent-wrapper replace' in go.mod                         1 -> 0
```

The greps fire. Note `go-agent-wrapper =>` was **already** 0 at `71154966`:
the defect was never a wrong replace, it was a comment asserting a replace that
has never existed. That is the count that moved, `1 -> 0`.

### Baseline check, main tree

```
go build ./cmd/nanite/    exit 0
go build ./...            exit 0
go vet ./...              exit 0, 0 lines of output
go test ./...             exit 0, 110 ok/no-test-file lines, 0 FAIL lines
```

### The decoupling proof

A build that passes while `libs/go-envelopes` sits at the replace path proves
nothing, and the acceptance criterion is specifically about its absence.

**The operator's real `~/dev/hollis-labs/libs/` was not touched.** The proof
ran in a throwaway `git clone` of this repo at `f08ac62a`, placed under the
session scratchpad in an `apps/nanite` + `libs/` geometry so the surviving
relative replaces still resolve.

`libs/` there contains **only** `go-harness-filters` and `go-runtime-events`,
freshly cloned and checked out at the two SHAs the workflow still pins. The
proof layout deliberately is *not* an empty `libs/`: those two replaces are
`03`'s to remove, and an empty `libs/` would fail on them and prove nothing
about this task's two modules.

```
libs/go-envelopes    -> ABSENT
libs/go-modelsdev    -> ABSENT
libs/go-harness-filters, libs/go-runtime-events -> present at the pinned SHAs
```

Module resolution inside the clone, `go list -m -f '{{.Path}}@{{.Version}} {{.Replace}} {{.Dir}}'`:

```
go-envelopes@v0.3.0        replace=(none)  dir=$GOMODCACHE/github.com/hollis-labs/go-envelopes@v0.3.0
go-modelsdev@v0.2.0        replace=(none)  dir=$GOMODCACHE/github.com/hollis-labs/go-modelsdev@v0.2.0
go-harness-filters@v0.1.0  replace=../../libs/go-harness-filters
go-runtime-events@v0.1.0   replace=../../libs/go-runtime-events
```

```
go build ./...   exit 0
go test  ./...   exit 0, 109 ok/no-test-file lines, 0 FAIL lines
```

109 in the clone vs 110 in the main tree is the §3.5 delta, not a shortfall:
the main tree's extra package is `ui/node_modules/flatted/golang/pkg/flatted`,
and `node_modules` is not tracked so a clone does not have it. Both trees were
`git status --porcelain` clean immediately after the test run.

**Control run — with no `libs/` sibling tree at all**, the scratch `libs/` was
moved aside (inside the scratchpad; the operator's tree was never involved) and
the build re-run:

```
go build ./...  exit 1
  go-agent-wrapper@v0.8.1/adapters/adapter.go:4:2:
    github.com/hollis-labs/go-runtime-events@v0.1.0: replacement directory
    ../../libs/go-runtime-events does not exist
  go-agent-wrapper@v0.8.1/filters/repair_pipeline.go:6:2:
    github.com/hollis-labs/go-harness-filters@v0.1.0: replacement directory
    ../../libs/go-harness-filters does not exist
```

Occurrences of each module name in that failure output: `go-envelopes` **0**,
`go-modelsdev` **0**, `go-harness-filters` 1, `go-runtime-events` 1. So the
only thing still binding this repo to a sibling checkout is the pair `03`
removes. `libs/` was restored and re-listed afterwards.

### The gate

Every local proof above passed before anything was committed; the commit was
pushed before the gate was dispatched, so the run resolves the tree this task
actually changed.

- `f08ac62a` pushed to `origin/main` (`71154966..f08ac62a`).
- `gh workflow run "Full-repo quality gate" --ref main` ->
  run [`32856005953`](https://github.com/hollis-labs/nanite/actions/runs/32856005953),
  `event=workflow_dispatch`, `headSha=f08ac62a`.
- **Conclusion `success`.** All 21 steps green, `Complete job` included. No
  step failed, so there is no failing step to name.

Reported per the runbook's "How to report a gate result honestly":

- **What this green means:** no regressions in any asserting step, at the
  package count the baseline was calibrated on —
  `tracked Go packages: 109 matches the committed expectation`.
- **No gosec reduction to confirm or bank.** The gate's one known soft spot is
  a false *improvement* on the gosec step; it did not fire.
  `standalone gosec actionable rules: baseline=206 current=206`, every rule
  equal to its baseline (`G101 3/3 … G704 3/3`), `ratchet passed`. Nothing was
  lowered and nothing needs re-confirming.
- `audit-config linters: ratchet passed`;
  `correctness Stage 2: errcheck=0, errorlint=0, nilerr=0` -> passed.
- `Verify modules` — the `go mod verify` + `go mod tidy -diff` step, the one
  this task's `go.sum` change had to satisfy — passed on the CI checkout.
- `Run aggregate race suite` passed, ~12 min. No `internal/memory`
  `SQLITE_BUSY`, so `CW-20260825-0001` did not surface on this run.

**The decisive line, and the reason `01` needed a real CI run.** A clean
Actions checkout now has no `libs/go-envelopes` or `libs/go-modelsdev`
directory at all. `Discover tracked Go packages` logs:

```
go: downloading github.com/hollis-labs/go-envelopes v0.3.0
go: downloading github.com/hollis-labs/go-modelsdev v0.2.0
```

Those two lines are the **only** occurrences of either module name in the
entire 2,149-line run log
(`gh run view 32856005953 --log | grep -ic 'go-envelopes\|go-modelsdev'` -> `2`).
CI is now resolving both from the module proxy against a published tag, which
is precisely what the pinned checkouts prevented.

### Corrections to the record found while doing this

1. **`TASKS/ESCALATIONS.md`'s 2026-08-24 entry says
   `github.com/hollis-labs/tesseract` is private.** It is public now:
   `gh api repos/hollis-labs/tesseract --jq .visibility` -> `public`, which is
   what `docs/engineering/runbooks/full-repo-quality-gate.md`'s "Module
   resolution" section already asserts. The escalation entry itself notes the
   remedy was never recorded; this is presumably it. No tesseract resolution
   failure occurred anywhere in this task. Not edited — that entry is a record
   of what was true on 2026-08-24, and the runbook already carries the current
   fact.
2. **Annotated-tag peeling**, above — the task file's and the batch README's
   `ls-remote`-shaped checks would have reported a spurious mismatch.

### Deliberately not done

- **`CW-20260816-0090` was not transitioned in Torque.** No Torque tooling in
  this session. Landing commit for whoever closes it: `f08ac62a`.
- **`TASKS/INDEX.md` and `TASKS/gate-integrity/README.md` not edited** — the
  Orchestrator's files.
- **`docs/engineering/runbooks/full-repo-quality-gate.md:12-21` is now stale**
  and is outside this task's `Touches`. It says Nanite's *"four public
  local-replace modules are checked out at the exact pinned commits below"* and
  lists `go-modelsdev` and `go-envelopes` among them; after this commit there
  are two. Reported rather than fixed: `03` rewrites that same paragraph to
  zero, and `06` is the citation-drift sweep. Same for
  `docs/engineering/orchestrator-kickoffs/gate-integrity-wave-a.md:101,120-121`.
- **Historical references to `libs/go-envelopes` in landed Work Logs** —
  `TASKS/INDEX.md:436,440`, `TASKS/ESCALATIONS.md:141,166,169`,
  `TASKS/phase-6/*`, `TASKS/agent-host-acp/03:18-19` — left alone. They record
  what was true when written; the batch README rules this class out explicitly.
- **The frontend envelope generators are still coupled to
  `../../libs/go-envelopes`, and this task does not decouple them.** Found
  while checking what else resolves through that path; measured, not inferred.
  `scripts/generate-plugin-imports.mjs:23` and
  `scripts/generate-envelope-types.mjs:36` both hardcode
  `resolve(ROOT, '..', '..', 'libs', 'go-envelopes', 'manifest', ...)`, which is
  the same sibling path the `replace` used and is unrelated to `go.mod`. In the
  decoupling clone above:

  ```
  node scripts/generate-plugin-imports.mjs --check   -> exit 1
      ERROR: Core envelope manifest not found: .../proof-01/libs/go-envelopes/manifest/envelopes.yaml
  node scripts/generate-envelope-types.mjs           -> exit 1
      Manifest schemas directory not found: .../proof-01/libs/go-envelopes/manifest/schemas
  ```

  Control, main tree with the sibling present:
  `node scripts/generate-plugin-imports.mjs --check` -> exit 0.

  **Who hits it and when:** anyone running `npm run build` or `npm run dev` in
  a checkout without the sibling tree — `ui/package.json:17-18` run both
  generators from `prebuild`/`predev`. That is exactly the fresh-clone,
  out-of-tree-worktree and container case this batch's `01`-`03` exists to
  unblock. It does **not** affect the Go build, `go test`, or the quality gate
  (the gate runs no frontend step).

  **Implication if left:** `01`-`03` will land and the repo will still not be
  clone-and-build for the frontend half. `generate-envelope-types.mjs` already
  accepts `--manifest-dir`; `generate-plugin-imports.mjs` has no equivalent
  flag. The fix is a resolution decision — most plausibly read the manifest
  dir from `go list -m -f '{{.Dir}}' github.com/hollis-labs/go-envelopes` —
  which is a design call and is outside this task's `Touches`.

  **Severity:** medium, high confidence in the measurement. Not a blocker for
  `01`; it is a gap in what `01`-`03` as a group is supposed to deliver.

- **`lefthook.yml`'s pre-push `go-test` has `glob: "*.go"`**, so this push
  printed `go-test (skip) no matching push files` — a `go.mod`/`go.sum`-only
  change can break the build and does not trip the hook. The behavior is
  documented at `lefthook.yml:180-195` and the skip reason is deliberate, so
  this is an observation, not a defect claim. The full suite was run by hand in
  both trees regardless. Low severity; nobody hits it without editing `go.mod`
  alone.

## Review notes
