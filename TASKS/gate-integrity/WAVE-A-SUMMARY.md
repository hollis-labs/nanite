# Gate Integrity — Wave A summary

For the operator, deciding whether Wave B starts.

**Written at `d35085ae`.** Numbers below were derived at that commit by the
doc-writer with the command shown, not copied from task files.

**Scope note on escalations.** Per your instruction, `TASKS/ESCALATIONS.md` and
its process were out of scope for this closing pass. It was not read and not
written. Findings this wave raised are recorded in the task files' Work logs and
Review notes, and the ones that matter are summarized in §4 below. If entries
were added to `ESCALATIONS.md` during the wave, they are not reflected here.

---

## 1. What shipped

Five tasks, run serially, 33 commits (`git log --oneline 77137106..HEAD | wc -l`).

### Sibling decoupling — `01`, `02`, `03`

The repo no longer depends on a sibling directory layout to build Go code.

- **`go.mod` carries zero `replace` directives** — `grep -c '=>' go.mod` → `0`.
  It previously carried four, all relative `../../libs/<module>` paths.
- **The quality-gate workflow checks out only nanite** —
  `grep -c 'actions/checkout' .github/workflows/full-repo-quality.yml` → `1`,
  and `grep -c 'libs/' …` → `0`. Four sibling checkout steps were deleted; the
  workflow's YAML step count went 17 → 13.
- **All four siblings resolve from the module proxy at published tags:**
  go-envelopes `v0.3.0`, go-modelsdev `v0.2.0`, go-harness-filters `v0.1.1`,
  go-runtime-events `v0.1.1`. `go.mod:20` previously recorded go-envelopes
  `v0.1.1` while the build ran v0.3.0's source through the replace — three
  minor versions of drift, now gone.

**Re-verified independently at `d35085ae`:** cloned the repo into a scratch
directory whose `../../libs` path is confirmed absent, ran `go build
./cmd/nanite/` → exit 0, and `go list -m` reports `Replace=<nil>` for all four
siblings. The clone was deleted.

### The four things this was run to buy

| Goal | Status |
|---|---|
| A second person can `git clone && go build` | **True for Go.** Not true for the UI — see §5. |
| Git worktrees work outside `~/dev/hollis-labs/apps/` | **True for Go.** Proven by a decoupled *clone*; `TASKS/INDEX.md` records honestly that a real out-of-tree `git worktree` has not itself been exercised. |
| A container needs only source + proxy access | **True for the Go binary.** Not for `npm run build` or `make install`. |
| CI validates released source | **True.** Gate run `32864133579` resolved every dependency from the proxy. |

### Commit-time and push-time checks — `08`

- **Commit time is formatting only.** `go-vet` and `go-lint` were removed from
  `pre-commit` (`grep -c 'go-vet:\|go-lint:' lefthook.yml` → `0`). What remains,
  per `lefthook dump`: `go-format`, `migration-purity`, and the still-disabled
  `frontend-lint`. Both remaining checks are scoped to the staged diff, so
  neither can fail for a reason outside the change in front of you. That removes
  the reason people were reaching for `--no-verify`, which disabled every check
  at once.
- **A new landing check, `scripts/check.sh`.** Four named stages — `format`,
  `vet`, `lint`, `test` — run with no arguments, every stage runs even after one
  fails, and it names each failing stage. Its test stage is Tier 1 of the
  existing `docs/engineering/testing-workflow.md`; no new tier scheme was
  invented. Run it when a feature lands, not on every commit.
- **`pre-push` now runs `go test ./...` on every push to `main`.** The branch is
  the only scoping (`only: - ref: main`); the file filter was removed. See §4.B
  for why.
- **Two silent fail-opens were closed.** `gofmt -l` prints nothing both when a
  tree is clean *and* when the tool never ran — so a missing `gofmt`, an
  unparseable file or an unreadable path all reported `OK`. Fixed in both
  `scripts/check.sh`'s format stage and `lefthook.yml`'s `go-format` hook.

### The gosec reduction advisory — `04a`

The comparator used to tell you to *"lower the committed baseline to preserve
them"* whenever findings decreased — the one action that turns a transient
dropped-findings run into permanent data loss. It now asks for a repeat run
first (`scripts/quality-ratchet.py:164-169`), and explicitly endorses banking a
reduction that reproduces, so it does not over-correct into "never trust an
improvement." `python3 scripts/quality-ratchet_test.py` → `Ran 45 tests … OK`; the count was
44 before (`git show 93939f6b:scripts/quality-ratchet_test.py | grep -c '    def test_'`
→ `44`, against `45` at HEAD). Increases still fail exactly as before.

### Documentation and durable process

- **Five new environment hazards** in `docs/engineering/agent-verification-discipline.md`
  §3 — §3.8 through §3.12 (`grep -cE '^### 3\.'` → `12`; it was `7` at
  `77137106`). These are the most reusable output of the wave; §5 of the handoff
  summarizes each. Two of them were found by workers correcting durable docs
  written earlier in the *same* session.
- **`CLAUDE.md`, `AGENTS.md`, `TASKS/INDEX.md`, `testing-workflow.md` and
  `lefthook.yml` all describe the same hook set**, and it is the one that
  exists. Four further documents that told readers vet and lint run at commit
  time were corrected in `1e9215ca` — `README.md`'s Quick Start,
  `.nanite/agents/backend.md`, `.nanite/agents/reviewer-backend.md` and
  `TASKS/audit-remediation/PREVENTION.md`. The two `.nanite/agents/` files are
  per-agent boot context, so they had been actively teaching agents to skip
  `go vet`. A follow-up, `72c705e6`, put four more stale claims in the past
  tense (`git show 72c705e6 --name-only`).
- **The runbook's gate-shape paragraph now matches the workflow** (13 steps, 8
  assert, 7 of those 8 sound — the split is unchanged because none of the four
  deleted steps asserted anything), and a **Landing check** glossary entry
  distinguishes it from the quality gate by trigger.

---

## 2. The two quality-gate runs

Derived with `gh run view <id> --json url,conclusion,headSha,event`:

| Run | Head SHA | Event | Conclusion |
|---|---|---|---|
| [`32856005953`](https://github.com/hollis-labs/nanite/actions/runs/32856005953) — after `01` | `f08ac62a` | `workflow_dispatch` | **success** |
| [`32864133579`](https://github.com/hollis-labs/nanite/actions/runs/32864133579) — after `03` | `834c9506` | `workflow_dispatch` | **success** |

Reported per the runbook's "How to report a gate result honestly": each green
means **no regressions in any asserting step, at the package count the baseline
was calibrated on** (109). Neither run showed a gosec reduction, so nothing
needs confirming or banking. Both runs concluded `success`, so the known
`internal/memory` `SQLITE_BUSY` intermittent (Torque `CW-20260825-0001`) failed
neither; `03`'s Work log records the explicit
`gh run view 32864133579 --log | grep -c 'SQLITE_BUSY'` → `0`, which the
doc-writer did not re-run.

`32864133579` is the first run in the gate's existence in which CI resolved
every dependency from the module proxy rather than from pinned sibling
checkouts. In `32856005953`'s log, the only two occurrences of `go-envelopes` or
`go-modelsdev` across 2,149 lines are `go: downloading` — recorded in `01`'s
Work log and confirmed by its reviewer.

**Neither run covers current `HEAD`** — see §6.

---

## 3. Cross-repo work — `02`

Two sibling releases were cut. This is the only task that left the nanite repo;
nanite itself was untouched by it.

| Repo | Tag | Points at | On the proxy |
|---|---|---|---|
| `libs/go-harness-filters` | `v0.1.1` | `57a6b091…` — the exact SHA the workflow had pinned | yes |
| `libs/go-runtime-events` | `v0.1.1` | `8756744985…` — same | yes |

Verified read-only at `d35085ae` with `git -C`, and the proxy asked over HTTP:
`curl -sS https://proxy.golang.org/github.com/hollis-labs/<m>/@v/list` returns
`v0.1.0 v0.1.1` for both.

`v0.1.1` was load-bearing, not hygiene: published `go-agent-wrapper v0.8.1`
calls `hrepair.Chain`, a symbol that does not exist in go-harness-filters
`v0.1.0`. Without the tag, `03` could not have dropped that replace at all.

### Open item — the changelog commits are written but unpushed

Both repos sit one commit ahead of their remote. Verified read-only:

```
git -C ~/dev/hollis-labs/libs/go-harness-filters status -sb
#   ## main...origin/main [ahead 1]     HEAD 48dfd6a "Record the v0.1.1 release in the changelog"
git -C ~/dev/hollis-labs/libs/go-runtime-events  status -sb
#   ## main...origin/main [ahead 1]     HEAD 531d0c0 "Record the v0.1.1 release in the changelog"
```

Both trees are clean. Pushing a branch was outside the worker's authorization,
so the commits were left local. Until they are pushed, the published repos show
a `v0.1.1` tag with no `v0.1.1` changelog entry. This does not block anything —
the modules are on the proxy and consumable. One command each:

```
git -C ~/dev/hollis-labs/libs/go-harness-filters push origin main
git -C ~/dev/hollis-labs/libs/go-runtime-events  push origin main
```

**Note before you look:** a parallel session was live in that tree during the
wave. Nothing in this closing pass wrote to it — the readings above are `git -C`
status queries only.

---

## 4. Findings raised, and how each resolved

Three of the four reviews that ran returned a verdict short of clean, and in
every case the finding was fixed rather than waived.

### A. `08` — reviewer verdict FAIL (narrow), then reopened a second time

The first fresh review reproduced every behavioral criterion and still found
three real defects: a format stage that could report `OK` for files it never
examined; four live documents still telling readers vet and lint run at commit
time; and a `--help` implementation that silently printed nothing when invoked
from a subdirectory. **All three fixed**, each with the failure reproduced first
and the fix proven after. The document sweep found two *more* false claims in
the same file the reviewer had cited, and those were fixed too.

The review also found that the acceptance criterion's own verification
instrument was too narrow — the prescribed `grep` matched only the hyphenated
command names, so four documents stayed wrong while the criterion read as
passing. The criterion now carries a broadened grep that over-matches on
purpose, with every hit triaged as a live claim or a historical record.

### B. `08` reopened after closing — the `pre-push` glob was a silent fail-open

Found downstream, during a fresh review of `03`. `pre-push`'s `glob: "*.go"`
matched neither `go.mod`, nor `go.sum`, nor any of the non-Go inputs compiled
into the binary — 22 `//go:embed` directives pull in 147 migration `.sql` files
plus the UI bundle, plugin manifests, agent profiles and more. **So a
migration-only push to `main` ran no tests and printed `go-test (skip) no
matching push files`, which reads as a benign, correct skip.** Migration number
collisions are this repo's one unrecoverable failure class, making that the
worst thing to skip quietly.

Your decision, 2026-08-25: drop the glob entirely. Enumerating the extra paths
was considered and rejected — it covers only the cases someone thought of and
goes stale against the next `go:embed`. **Accepted cost: a docs-only push to
`main` now also runs the suite.** The suite's cost is measured with its command
in `lefthook.yml`'s own header and in `CLAUDE.md` — cached runs are seconds; a
cold run is well inside your 30s-to-two-minutes constraint. The fix was proven
in a real bare clone used as `origin`, one push set at a time, with a positive
control in both directions.

### C. `01` — reviewer verdict FAIL (narrowly, and only on the Work log)

The code change was correct and its acceptance criteria genuinely met. Four
Work-log *claims* were wrong or overstated, and the Orchestrator corrected them
in place rather than dispatching a fix. One mattered beyond the record: the
worker's "is it on the proxy?" check never contacted the proxy, because
`GOPRIVATE` silently defeats a `GOPROXY=` prefix. `01`'s conclusion survived
(both versions really were published), **but the same command shape was the hard
gate between `02` and `03`**, where the answer was genuinely unknown and the
proxy populates lazily. It would have returned a false green at exactly the
moment it was load-bearing. Replaced with a direct `curl` in the kickoff, the
batch README and both task files, and written up as hazard §3.11.

The reviewer also closed a proof gap: `01`'s byte-identity comparison compared
git against git, since `GOPRIVATE` also defaults `GONOSUMDB`. `02`'s reviewer
later closed it properly by diffing the proxy's published `.zip` against
`git archive <tag>` — identical for both modules.

### D. `02` — reviewer verdict PASS, one low-severity record defect

The irreversible part — the two tags — was verified five independent ways. One
Work-log rationale was wrong (`./nanite` is gitignored, so a plain `go build`
would not have shown in `git status --porcelain`); marked inline, not rewritten.

`02` also found that **both sibling repos' own `check` workflow has never
passed, including at `v0.1.0`** — go-harness-filters fails at `go fmt (verify)`
on a doc-comment indent, go-runtime-events on `golangci-lint-action` with exit
3. Every affected line blames to each repo's `v0.1.0` commit, so none of it is a
regression from this release. Not fixed: fixing it means a new commit, and
tagging anything other than the pinned SHA is the exact failure `02` existed to
prevent. **Worth its own task, in those repos.**

### E. `03` — reviewer verdict PASS

Recorded in commit `c169830f`'s message. See §6.2 for a gap in how that was
written down.

### F. `04a` — reviewer verdict PASS, two precision findings, both fixed

The new code comment said the suspect gosec run had "identical `Stats`". It did
not: `Stats.found` equals `len(Issues)` and moves with a finding drop; only
`files` and `lines` were identical. That imprecision pointed the wrong way for
its intended reader, since `04b`'s planned floor is keyed on `files`/`lines`
*because* counts move. The comment also claimed exclusivity stronger than the
runbook's calibrated *"one **known** soft spot"*. Both fixed in `aa454aef`.

---

## 5. Still open, and needing your attention

### The frontend is not decoupled — the biggest open item

`01`-`03` made the repo self-contained **for Go**. Three call sites still
hardcode `../../libs/go-envelopes`: `scripts/generate-plugin-imports.mjs:23`,
`scripts/generate-envelope-types.mjs:36`, and `Makefile:24,27,31`.
`ui/package.json`'s `prebuild` and `predev` run two of them, and `make install`
depends on the third. Measured in a decoupled clone at `d35085ae`: both scripts
exit 1, `make generate-envelopes` exits 2.

**So `npm run build`, `npm run dev` and `make install` still require the sibling
tree.** No Go build, no `go test` and no gate step is affected — the gate runs
no frontend step at all.

This has **no task file**. It is the thing Wave C's container work most needs
resolved, because a container image scoped today would bake in a mount of the
sibling repos. The fix is a resolution decision, not a mechanical edit: the most
plausible shape is reading the manifest directory from
`go list -m -f '{{.Dir}}' github.com/hollis-labs/go-envelopes`.

### Deliberately still open — planned, not deferred indefinitely

| Item | Wave | State |
|---|---|---|
| `07` — migration number collision guard | **B** | `not-started`. **This is the gate on parallel worktree work.** It guards the one unrecoverable failure class, and Wave A made it *more* urgent by making out-of-tree worktrees actually usable. |
| Container image + how checks run inside it | **C** | `not scoped`, deliberately. Should be scoped only after a decision on the frontend gap above. |
| `04b` — gosec repeat-run wrapper + `Stats` coverage floor | **D** | `not-started`. Blocks no workflow. |
| `05` — runbook reproduces the vacuous lint pass | **D** | `not-started`. Real defect on the human-facing side; a person debugging a gate result locally can get a false green. |
| `06` — citation and config drift sweep | **D** | `not-started`. Wave A generated more work for it: several documents now quote superseded text under a `77137106` stamp, and `full-repo-quality-gate.md`'s "Release resolution" paragraph still ends *"and the workflow pins that exact release commit"* — there is no pin now. |

### Explicitly out of this batch, still true

Branch protection and real merge gating remain **a cost/plan decision for you,
not engineering** — surfaced, deliberately unplanned. The gate fires on
`schedule` + `workflow_dispatch` only, so the local git hooks are still the sole
automatic check between an agent writing code and it landing on `main`. Wave A
strengthened that check (the `pre-push` suite now runs on every push to `main`)
but did not change the posture.

Also unchanged and out of scope: the `frontend-lint` repair
(`CW-20260816-0087`), the fresh-clone `lefthook install` gap, the go-envelopes
half-migrated enum, and the tesseract bump.

---

## 6. Things I'd check before Wave B starts

These are inconsistencies the doc-writer found between the task files, the index
and the repo. None is a code defect. Each is listed with what to verify.

1. **`TASKS/INDEX.md` says all five are `reviewed`; every task file's own
   `**Status:**` field still says `implemented`.** `sed -n '4p'` on `01`, `02`,
   `03`, `08` all return `**Status:** implemented`. The convention elsewhere in
   the repo is `reviewed` (66 of them in `TASKS/audit-remediation/`). Cosmetic,
   but a reader of a task file alone cannot tell it was reviewed.
2. **`03`'s `## Review notes` section is empty.** The heading is the last line of
   the file. `c169830f`'s commit message says "03 reviewed PASS" and the index
   records `reviewed`, but no verdict, findings or independent re-derivations
   were transcribed — unlike `01`, `02`, `04a` and `08`, which all carry full
   transcriptions. `03` is the keystone change; its review evidence is the one
   most worth having on the record. **Worth asking whether that review happened
   and its notes were simply not transcribed, or something else.**
3. **`08`'s fifth-pass Work log now misdirects a reader about which commit
   removed the `pre-push` glob.** It records a shared-checkout incident in which
   commit `517721fa` swept the pass's uncommitted work into a peer's commit,
   states it was "not rewritten," and says `517721fa` is where the glob was
   removed. At `d35085ae` that commit is **not in `main`'s history**
   (`git merge-base --is-ancestor 517721fa HEAD` exits non-zero) — the change
   was correctly split into `60cfb1be` (discipline doc) and `aedbc296`
   (`lefthook.yml` and the three docs). So the incident was resolved after the
   Work log was written, and the log was not updated. **`aedbc296` is the
   commit.**
4. **`01`'s Review notes mischaracterize two standards docs** as "stubs whose
   only heading is *'Not yet documented'*", and raise a process gap on that
   basis. Both files carry substantive bullet lists plus a trailing
   `## Not yet documented` section, and `standards/code-quality.md` contains the
   exact *"A silent fail-open is worse than a loud failure"* line that `08` later
   cited as authority. Neither file changed this wave. `EXECUTION-PROCESS.md:62`
   does point reviewers at them, and the checklist items it names are present —
   so the process gap as described does not hold.
5. **Nine commits are unpushed.** `git rev-list --left-right --count
   origin/main...HEAD` → `0 9`; `origin/main` is `83ac15eb`. The unpushed set
   includes **all of `04a`'s code** (`scripts/quality-ratchet.py` and its tests)
   and **`08`'s `pre-push` widening** (`lefthook.yml`). Neither gate run covers
   them. Pushing them will itself trigger the full test suite, since the widened
   hook now fires on every push to `main`.
6. **`lefthook validate` exits 1**, and has throughout — three `skip_empty` keys
   remain on the pre-commit commands and the key is not in lefthook 2.1.4's
   schema. `08` reduced the carriers from six to three. Pre-existing, parked
   deliberately, but it means `lefthook validate` is not usable as a clean
   signal today.

---

## 7. Current `TASKS/INDEX.md` state — Gate Integrity section

Ten rows (`sed -n '/^| Task | Wave | Status | Depends on |/,/^$/p' TASKS/INDEX.md`):

| Status | Count | Which |
|---|---|---|
| reviewed | **5** | `08`, `01`, `02`, `03`, `04a` — all of Wave A |
| not-started | **4** | `07` (Wave B), `04b`, `05`, `06` (all Wave D) |
| not scoped | **1** | the Wave C container item |
| blocked | **0** | — |

Wave A is complete. The index's keystone paragraph has been updated to say the
Go half is done and the frontend half is not.
