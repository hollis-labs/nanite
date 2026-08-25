# Gate Integrity — implementation

The first batch planned after the AD-24 development freeze was lifted
(2026-08-25). Unlike its sibling batches, this one does not implement a
`docs/engineering/architecture/NN-*.md` design — it is drawn from the
hand-off register the pre-unfreeze batch left behind
(`pre-unfreeze-batch-summary.md` §8, plus §7's process findings), triaged and
re-verified against live code by the post-unfreeze planning session on
2026-08-25.

A sibling to `TASKS/reflex-taxonomy/`, `TASKS/scheduling/`, `TASKS/teams/`,
`TASKS/agent-host-acp/`, `TASKS/plugin-system/`, `TASKS/skills/`,
`TASKS/loops/`, `TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`, and
`TASKS/code-mode/` — kept in its own top-level `TASKS/` subfolder for the same
reason those are.

**Everything below was verified at `77137106`.** Line numbers and counts in
these task files were derived at that commit and *will* drift. Re-derive
before acting on any of them — every citation here ships the command that
produced it for exactly that reason.

## What this batch is, and what it is not

**This is the tail end of standing up a first quality gate, not a rescue.** The
gate works. Of its 17 steps, 8 assert; **7 of those 8 are sound.** Derive the
split yourself rather than trusting this paragraph — the classifier is in this
batch's planning notes, but the short version is that only the gosec step has a
known defect, and only in one direction.

What the gate already does correctly, and what this batch must not regress:

- **Regressions are caught.** `compare_counts` fails on increases and on
  findings from unbaselined rule names. Nothing in the gate currently lets a
  straightforward regression through silently.
- **Coverage is canaried.** `Assert the discovered package list matches the
  committed shape` (expected 109) guards the input handed to lint, govulncheck,
  gosec, deadcode and the race suite. The pre-unfreeze batch added this, and it
  is the single most load-bearing assertion in the workflow.
- **The lint ratchet no longer passes vacuously.** `Report.Error` is checked
  (`scripts/quality-ratchet.py:183`). That was the worst defect the gate had and
  it is fixed.

The scope of what is left, stated precisely:

- **`04` — the gosec step can bank a *fake improvement*.** A spurious decrease
  from the known 193-vs-210 nondeterminism is indistinguishable from a real one,
  and the comparator's advisory tells you to lower the baseline — the one action
  that converts a transient flake into permanent data loss. Observed once in 12
  runs. Because comparison is per-`rule_id`, a same-run regression in the same
  rule could in principle be masked; narrow and unlikely, but real.
- **`01`-`03` — CI validates against sibling source that is in no published
  release.** A green gate therefore does not prove the *published* dependency
  set builds. Cheap to remove entirely.
- **`05` — the runbook reproduces the vacuous pass** that `0023` closed on the
  CI side. A human debugging a confusing result can get a false green.
- **`06` — citations that point at moved lines**, and one at a file that does
  not exist.

**None of this is a blanket trust problem, and no task here should be written or
reviewed as though it were.** This project is pre-release with no consumers; the
gate's job is to show direction — are we improving — and it does that today for
every asserting step. These four tasks close the tail so the one remaining
soft spot stops being a judgment call.

## Cross-repo scope — read before dispatching `02`

Task `02` lands in **`libs/go-harness-filters` and `libs/go-runtime-events`**,
not in nanite. It is the only task here that leaves this repo. Check both
sibling repos' own `git log` immediately before merging, not just at
task-authoring time — `agent-host-acp` and `filesystem-snapshots` both hit
real cross-repo collisions doing exactly this.

## Read before starting any task here

1. `docs/engineering/agent-verification-discipline.md` **in full.** This batch
   is almost entirely numbers and citations; §1.1 (derive at the moment of
   use), §1.2 (ship the command next to the number) and §2.2 (re-derive cited
   line numbers) are the whole job.
2. `docs/engineering/failure-modes.md` — why measurements and documents
   mislead.
3. `TASKS/INDEX.md`'s **"What changed during the freeze"** section, and the
   migration claiming rule directly below it.
4. `docs/engineering/EXECUTION-PROCESS.md` and
   `docs/engineering/templates/03-task-file-template.md`.
5. **The load-bearing corrections below.** They are the reason this batch is
   shaped the way it is, and three of them contradict the register this batch
   was drawn from.

## Load-bearing corrections this planning session made — real findings, not assumed

Each was found by checking the register against live code, not by reading
documents against each other.

### 1. Two of the four pinned siblings are pinned past any published release

`.github/workflows/full-repo-quality.yml:35-61` checks out four siblings at
hard-pinned SHAs. Two of those pins are **one commit ahead of the newest tag
that exists**, so CI validates against source that is in no release:

```
for m in go-modelsdev:7d932798b85145ec93f923e392f5d41762894e8c \
         go-envelopes:58243d84db7df1480960e2b747bea6ffca9fd6db \
         go-harness-filters:57a6b0919c0c5f06db90b367184988a72c430d39 \
         go-runtime-events:8756744985a6602d6ab1fb0df78d5aabc3920b1b; do
  n=${m%%:*}; s=${m##*:}; d=~/dev/hollis-labs/libs/$n
  t=$(git -C "$d" describe --tags --abbrev=0)
  printf '%-20s pin=%s tag=%s MATCH=%s\n' "$n" "${s:0:8}" "$t" \
    "$([ "$s" = "$(git -C "$d" rev-list -n1 "$t")" ] && echo YES || echo NO)"
done
```

At `77137106` that returns `MATCH=YES` for `go-modelsdev` (v0.2.0) and
`go-envelopes` (v0.3.0), and `MATCH=NO` for `go-harness-filters` and
`go-runtime-events` — each **1 commit ahead** of its `v0.1.0`
(`git -C <repo> rev-list --count v0.1.0..<pin>` returns `1` for both).

### 2. The coupling is *removable*, not merely checkable — which changes the fix

The register recommends a drift check, or "at minimum a comment at each pin."
Both under-solve it. All four sibling working trees are clean
(`git -C <repo> status --porcelain | wc -l` returns `0` for each), two pins
are byte-identical to published tags, and both of those tags are on the
proxy:

```
GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/go-envelopes
#   -> v0.1.0 v0.1.1 v0.2.0 v0.3.0
```

So `go-envelopes` and `go-modelsdev` resolve to **the same source** whether
they come from the `replace` or from the proxy. Their replaces are removable
today at zero behavioral risk (task `01`), and the other two become removable
after one tag each (tasks `02`, `03`). The end state deletes four `replace`
directives and four checkout steps, and the hazard class with them.

### 3. `go.mod` is three minor versions stale on go-envelopes, and one of its comments cites a file that does not exist

`go.mod:20` requires `github.com/hollis-labs/go-envelopes v0.1.1` while the
`replace` at `go.mod:102` points the build at v0.3.0's source. Separately,
`go.mod:115` reads *"Mirrors the go-agent-wrapper replace immediately
above."* There is no such replace:

```
grep -c 'go-agent-wrapper =>' go.mod     # -> 0
```

Grounds task `01`.

### 4. The gosec nondeterminism has a lead the register says does not exist

The register states the investigation "starts from zero, not from a lead."
That is too pessimistic. The gate never sets gosec's concurrency:

```
grep -n 'gosec -no-fail' .github/workflows/full-repo-quality.yml
#   159:          gosec -no-fail -exclude-dir=.claude -fmt=json \
gosec --help | grep -A1 concurrency
#   -concurrency int      Concurrency value (default 10)
```

The anomaly appeared under concurrent load, and its signature — a *strict
subset* of 17 findings missing, with `files` and `lines` identical, exit 0 and
well-formed JSON — is what a race in concurrent result aggregation looks like.
The hypothesis that was tested and eliminated was the **build** cache, an
unrelated mechanism. `-concurrency 1` is a one-flag experiment. Grounds `04`.

### 5. A dropped-findings run does not just pass the ratchet — the ratchet tells you to bake the loss in

In neither the register nor the boot prompt. It is what `04` is actually for.
**Scope it honestly:** this affects the *improvement* direction only, on one
step. Increases still fail correctly, so the gate cannot silently absorb a
straightforward regression. `compare_counts` in `scripts/quality-ratchet.py`
fails on increases and on unbaselined rule names, then treats a **reduction**
as success with an advisory:

```
grep -n 'reductions detected' scripts/quality-ratchet.py
```

At `77137106` that message reads *"lower the committed baseline to preserve
them."* So the bad 193-finding run exits 0, passes, and prints guidance which,
if followed, permanently deletes 17 real findings from the gate. `TASKS/INDEX.md`
warns "never raise a baseline to make a regression pass"; this is the same
error inverted, and the tooling actively invites it.

Note what this means for the coverage floor: `Stats.files`/`Stats.lines` were
**identical** in the bad run, so a floor keyed on them cannot catch this. The
floor and the nondeterminism are two different defects that happen to live in
the same workflow step. `04` fixes both and must not conflate them.

### 6. The comparator has no coverage floor and ignores gosec's own error channel — but the floor is defense-in-depth, not a hole

```
grep -n 'Stats\|Golang errors\|NumFiles' scripts/quality-ratchet.py    # -> no matches
```

`gosec_command` (`scripts/quality-ratchet.py:397`) reads only `Issues`, and
treats a missing `Issues` array as zero findings. Grounds `04`.

**Do not over-rate this one.** The `Assert the discovered package list matches
the committed shape` step (expected 109) already guards the *input* handed to
every scanner, so a shrunken scan is caught upstream. A `Stats`-based floor
only adds the narrower case where gosec received 109 packages and internally
parsed fewer. Worth having as a second layer; not the gap the register implies.
Build it, but do not let it displace the advisory reword, which is cheaper and
worth more.

### 7. The runbook reproduces the vacuous pass that `0023` just fixed

```
grep -c 'Report.Error' docs/engineering/runbooks/full-repo-quality-gate.md   # -> 0
grep -n 'section.get("Error")' scripts/quality-ratchet.py                    # -> 183
```

The CI comparator checks `Report.Error`; the runbook's local-reproduction
block does not mention it. Anyone reproducing the gate by the runbook can get
a clean-looking local pass from a lint run that analyzed nothing. This is the
same defect class `TASKS/audit-remediation/`'s `0023` closed, still live on the
human-facing side. Grounds `05` — and it is why `05` is a real task rather than
a line in the `06` sweep.

### 8. Worktrees DO inherit the hooks — but they cannot pass `go-lint`

Tested 2026-08-25 by committing into a throwaway worktree, not inferred.
`TASKS/INDEX.md` said "a fresh clone **or a new worktree** has no checks at
all"; the worktree half is false. `core.hooksPath` is an absolute path into the
parent clone's `.git/hooks`, which worktrees share — `go-format`, `go-vet` and
`go-lint` all fired and correctly rejected the commit.

**But the rejection was spurious.** `go.mod`'s four `replace` directives use
relative `../../libs/<module>` paths that do not resolve from a worktree
outside `~/dev/hollis-labs/apps/`, producing ~16 unrelated
`undefined: envelopes` typecheck errors. The natural escape is `--no-verify`,
which disables *every* hook, not just the failing one.

**This is now the strongest argument for `01`-`03`** — stronger than the
CI-pin-drift rationale those tasks were written around. Removing the relative
replaces is what makes worktree isolation actually usable, and worktree
isolation is what every batch's parallelization plan assumes.
`[[nanite_worktree_hooks_and_relative_replaces]]`

### 9. Two register items are already resolved — do not schedule them

- **Tracker reconciliation.** The register says `CW-20260824-0001` and
  `CW-20260824-0005` are still `todo`. Both are `done` in Torque, updated
  `2026-08-25T00:47`. Verified via `torque_task_get`, not assumed.
- **"The quality gate cannot run at all."** `pre-unfreeze-batch-summary.md`
  §8 bills this as *"the single highest-value item to schedule after the
  unfreeze."* §0 of that same document, and `TASKS/INDEX.md`'s point 4, record
  the gate running green at `61698b4e` (run `32791971817`). §8 was written
  before §0 was appended and was never reconciled. **A planner reading §8 first
  schedules dead work.**

### 10. Two register numbers are wrong; one register citation is wrong in substance too

- `docs/engineering/README.md` omits **7** of the 15 files in
  `docs/engineering/*.md`, not the register's "~10" — re-derive with the
  `comm` command in `06`, do not carry either number.
- The register flags `PREVENTION.md:161` for citing a `<15s staged files only`
  claim at `lefthook.yml:3`. Line 3 of `lefthook.yml` is now a **blank comment
  line**, and the substance has also changed: only 3 of the 5 pre-commit
  commands are staged-only. `go-vet` and `go-lint` are whole-repo
  (`sed -n '9,12p' lefthook.yml`). Both the pointer and the claim need fixing,
  in two files — `06` handles it.

## What this batch does NOT do

- **It does not add branch protection or a real merge gate.** The quality gate
  fires on `schedule` + `workflow_dispatch` only, so the per-clone git hooks
  are the sole automatic check between an agent writing code and it landing on
  `main`. That is the largest change in risk posture now that parallel batches
  have resumed, and it is a **cost/plan decision for the operator, not
  engineering** — surfaced explicitly, deliberately unplanned. See
  `TASKS/INDEX.md` point 4 and `[[nanite_gate_is_detector_not_merge_gate]]`.
- **It does not diagnose the gosec nondeterminism to root cause.** `04` runs
  one bounded experiment and lands a mechanism that is correct whether or not
  the experiment converges. Chasing a flake that did not recur across 8 runs
  and could not be deliberately reproduced is open-ended; the wrapper is not.
- **It does not finish the go-envelopes US-English migration.** v0.3.0 is
  deliberately half-migrated — US spelling in the `session-task` schema, UK in
  the Go vocabulary (`ResponseStatusCancelled`, `ErrorCodeUserCancelled`).
  Finishing it is source-breaking across in-house consumers, one of which
  (`apps/tangent`) *persists* the value, so it needs a coordinated change plus
  its own data migration plus another breaking bump. Out of scope here on
  purpose. `[[go_envelopes_v030_half_migrated_enum]]`
- **It does not touch the tesseract bump.** Gated on an apparently mid-flight
  typed-namespace migration. Understand that migration's state before scoping
  the bump. `[[nanite_tesseract_bump_gated_on_namespace_migration]]`
- **It does not repair `frontend-lint`.** Real work, separately tracked as
  `CW-20260816-0087`. `[[lefthook_frontend_lint_disabled]]`
- **It does not close the fresh-clone gap.** Task `07`'s guard, like every
  other hook, only protects clones where `lefthook install` has run. A fresh
  clone has nothing until someone runs it. Making that loud rather than silent
  is real work and is not scoped here.
- **It does not rewrite the ~25 drifted migration-filename citations in
  landed Work Logs.** Those are historical records of what was true when
  written, not live claims. Correcting them would falsify the record.

## Task sequence — ordered by the operator's roadmap, not by task number

Task numbers are stable identifiers. **Priority is the wave, not the number.**

The roadmap this serves, set by the operator 2026-08-25: **serial now →
parallel git worktrees when they work → Docker containers soon.** Nanite needs
to be stable enough that attention can move to that setup, so the ordering
below optimizes for unblocking the next step, not for closing findings.

### Wave A — do now. Removes the current friction and unblocks everything after it.

| Task | Why it is in Wave A |
|---|---|
| `08` Move whole-repo analysis off commit time | Cheapest, highest relief. Hooks are days old and already have a `--no-verify` bypass. Do this first. |
| `01` Drop the two published siblings' replaces | Starts making the repo self-contained |
| `02` Cut `v0.1.1` for the other two siblings | Sibling repos; runs in parallel with everything |
| `03` Drop the remaining replaces and the checkout block | **The keystone** — see below |
| `04a` Reword the gosec reduction advisory *only* | A few lines; prevents permanent baseline data loss |

**`01`-`03` is the keystone and was badly undersold when this batch was first
written.** It was filed as CI-pin hygiene. It is actually the single change that
makes the repo *self-contained*, and four separate things depend on that:

- **A second person can clone and build.** Today `git clone && go build` fails
  unless they reproduce the exact `hollis-labs/{apps,libs}` directory layout,
  because the four `replace` directives use relative `../../libs/<module>`
  paths.
- **Git worktrees work anywhere.** Today a worktree outside
  `~/dev/hollis-labs/apps/` cannot resolve those paths — tested 2026-08-25,
  ~16 unrelated `undefined: envelopes` typecheck errors.
- **Docker containers work.** Same root cause: a container would need the
  sibling repos mounted at a specific relative path. Afterwards it needs only
  the source and network access to the proxy.
- **CI validates released source.** The original rationale, and the least
  important of the four.

### Wave B — before switching on parallel worktrees

| Task | Why it waits |
|---|---|
| `07` Migration number collision guard | Guards the one *unrecoverable* failure class, but it is near-impossible to hit while working serially — you would have to author two migrations in two worktrees without looking. Needed the moment parallel work starts, not before. |

### Wave C — before or alongside Docker

Not yet scoped, deliberately. A container image and a decision on how the
landing script and the quality gate run inside it. **Do not scope this until
`01`-`03` has landed** — the container's shape depends on the repo being
self-contained, and scoping it first would bake in the workaround.

### Wave D — after the unblocking work. Real work, planned, just not first.

**These are planned, not optional and not deferred indefinitely.** Operator,
2026-08-25: *"We can do all the tasks, that was never the issue. It was always
about fixing the crucial things that prevent us from working in standard ways."*
Volume is not the constraint; order is. Everything below gets done — it just
goes after the work that keeps a standard workflow working. If one of these
turns out to matter more than a Wave A item, raise it and move it.

| Task | Why it sits after Waves A-B |
|---|---|
| `04b` gosec repeat-run wrapper + `Stats` coverage floor | Blocks no workflow. A 1-in-12 flake; a second occurrence would teach more than the mechanism does, and the floor is defense-in-depth behind the 109-package assertion that already exists. |
| `05` Runbook false-green | Real defect — a human debugging a gate result can get a false pass. Small, self-contained, blocks nothing. |
| `06` Citation and config drift sweep | Eight verified drifts. Genuinely worth fixing; nothing depends on it. |

### How this batch is ordered, and when to deviate

**The priority is keeping standard workflows working** — clone, build, test,
worktree, container, a second contributor. That is what Wave A is for, and it is
why `01`-`03` and `08` come before findings that are individually more severe.

**This is a priority, not a wall.** It is overridable, and the operator is who
overrides it. If something in Wave D turns out to matter more, or a Wave A item
turns out not to, say so and it moves. Do not treat the ordering as a rule to be
defended.

**Severity is not binary — say which you have found.** "Might block a workflow
in an edge case" and "will break every existing deployment" are not the same
thing and must not be reported in the same register. When you find something
that could affect a standard workflow, raise it with:

- what specifically breaks, and the command or trace that shows it;
- **who hits it and when** — everyone on every build, or one path under one
  configuration;
- the implication if it is left alone for now;
- your read on severity, and your confidence in that read.

Then let the operator decide. A finding raised with that detail is useful even
when the answer is "not now." A finding raised as a blocker without it costs a
session.

**Do not gold-plate the ordering.** The point is that work stays unblocked, not
that every item gets audited against a priority scheme before it can be touched.
If something is small and in front of you, doing it is usually cheaper than
classifying it.

## Parallelization plan

Cross-checked against each task's own `Touches` list.

**Wave A runs serially** — `08` → `01` → `02` → `03` → `04a`, one at a time.
Operator decision, 2026-08-25, carried in
`docs/engineering/orchestrator-kickoffs/gate-integrity-wave-a.md`. The
file-disjointness analysis below is why that ordering costs little, not a
licence to run the tasks concurrently.

- **Only two pairs actually collide.** `08` touches `lefthook.yml`,
  a new script, and three docs. `02` is in sibling repos entirely. `04a` touches
  `scripts/quality-ratchet.py`.
  - **Real overlap 1:** `01` and `03` both edit `go.mod`, `go.sum` and
    `.github/workflows/full-repo-quality.yml`. `03` also needs `02`'s tags
    published. Sequence: `01` → (`02` completes) → `03`.
  - **Real overlap 2:** `08` and `04a` both touch documentation describing the
    hook set and gate behavior. Minor; sequence if they collide.
- **Do not use out-of-tree worktrees to parallelize this batch** until `01`-`03`
  has landed — that is the very breakage being fixed. Until then, either work
  serially or place worktrees as siblings under `~/dev/hollis-labs/apps/`.

## Validation

`01` and `03` are the only tasks whose acceptance genuinely requires a CI run,
because the thing they change *is* what CI resolves. Both must dispatch the
gate by hand after landing and wait for green:

```
gh workflow run "Full-repo quality gate" --ref main
```

Known intermittent to ignore, not chase: `internal/memory` failing with
`SQLITE_BUSY` (`CW-20260825-0001`) — root-caused in tesseract, fixed there,
not yet picked up by nanite's pin.
