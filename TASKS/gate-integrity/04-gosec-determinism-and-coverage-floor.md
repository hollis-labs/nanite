# Stop a silently-degraded gosec run from passing the ratchet — and test the concurrency lead

**Phase:** 1 — Measurement integrity
**Status:** `04a` implemented (step 6, the advisory reword). `04b` — steps **2-5** implemented and reviewed (Wave D, 2026-08-25); **step 1**, the `-concurrency` experiment, remains **not-started**, so `04b` is **in-progress** as a whole. Set this way rather than to a whole-file `implemented` because five of the six steps and five of the six "Done means" bullets are untouched; only *The reduction message no longer advises lowering the baseline unconditionally* is satisfied.
**Depends on:** none
**Split:** this file covers **`04a`** (step 6, the advisory reword — Wave A)
and **`04b`** (steps 1-5 — Wave D). `04a` is a few lines, blocks nothing, and
prevents permanent baseline data loss; it may be landed on its own without
`04b`, and should be. Do not treat the file as indivisible.
**Touches:** `.github/workflows/full-repo-quality.yml` (the gosec step — at
`77137106` line 159; **re-derive**, task `01` edits the checkout block higher
up in the same file), `scripts/quality-ratchet.py`,
`.github/quality/full-repo-baseline.json`, and a new wrapper script.
Repo: nanite.

## Context

The same `gosec` command, same tree, same 109 packages, produced **193
findings once and 210 three times**. The 193 run was missing 17 findings — a
strict subset — and was indistinguishable from a good run: exit 0,
`GolangErrors` empty, well-formed JSON, and identical `files=661` /
`lines=165247`. Because files and lines matched, the files *were* parsed. This
is analysis nondeterminism, not a coverage failure.
`[[nanite_gosec_analysis_nondeterminism]]`

### The real defect — not in the register

A dropped-findings run does not merely pass the ratchet. **The ratchet tells
you to bake the loss into the baseline.** `compare_counts` in
`scripts/quality-ratchet.py` fails on increases and on unbaselined rule names,
then treats a reduction as success with an advisory:

```
grep -n 'reductions detected' scripts/quality-ratchet.py
```

At `77137106` the message reads *"lower the committed baseline to preserve
them."* So the corrupted 193-finding run exits 0, prints an encouraging
message, and invites an operator to permanently delete 17 real findings from
the gate. `TASKS/INDEX.md` point 6 warns "never raise a baseline to make a
regression pass" — this is that error inverted, and the tooling points the
wrong way rather than staying neutral.

### Scope this honestly before starting

This is the tail of standing up a first gate, not a rescue. **Increases still
fail correctly**, so the gate cannot silently absorb a straightforward
regression; the hole is that a *spurious decrease* is indistinguishable from a
real improvement. Observed once in 12 runs. Because `compare_counts` works
per-`rule_id`, a same-run regression in the same rule could in principle be
masked — narrow, unlikely, real. Nothing here justifies describing the gate as
untrustworthy, and a Work log or review note that does is wrong.

**Step 6, the advisory reword, is `04a` and ships on its own.** Do it first,
independently, and do not hold it behind the rest of this file: right now the tool's own guidance
points at the one action that turns a transient flake into permanent data loss.

### Two distinct defects that happen to share a workflow step — do not conflate them

- **Nondeterminism.** `Stats.files` and `Stats.lines` were *identical* in the
  bad run, so a coverage floor **cannot** catch this. The register is right
  that no canary detects it; only repeat-run comparison does.
- **No coverage floor — defense-in-depth, not a hole.** The `Assert the
  discovered package list matches the committed shape` step (expected 109)
  already guards the input handed to every scanner, so a shrunken scan is caught
  upstream. The floor adds only the narrower case where gosec got 109 packages
  and parsed fewer internally. Build it; do not inflate it. Separately, the
  comparator reads only `Issues` —
  `grep -n 'Stats\|Golang errors\|NumFiles' scripts/quality-ratchet.py` returns
  **no matches** at `77137106` — and `gosec_command`
  (`scripts/quality-ratchet.py:397`) treats a missing `Issues` array as zero
  findings. That is a real gap against a *different* failure: a run that scans
  fewer packages than it should. Key the floor on `Stats.files`/`Stats.lines`,
  **not** finding counts, because remediation moves counts constantly and
  files/lines barely at all.

### The lead the register says does not exist

The register states the investigation "starts from zero, not from a lead."
That is too pessimistic. The gate never sets gosec's concurrency:

```
grep -n 'gosec -no-fail' .github/workflows/full-repo-quality.yml
gosec --help | grep -A1 concurrency        # -concurrency int   (default 10)
```

The anomaly appeared under concurrent load; the signature — a strict subset
missing, identical files/lines, exit 0, valid JSON — is what a race in
concurrent result aggregation looks like. The hypothesis already eliminated
was the **build cache**, tested with an isolated `GOCACHE` — an unrelated
mechanism, so it does not bear on this one.

**This is one bounded experiment, not an open investigation.** The flake did
not recur across 8 further runs and could not be deliberately reproduced. Do
not let step 1 below expand into root-cause archaeology; steps 2-4 are
correct whether or not step 1 converges, and they are the deliverable.

## What to do

1. **Run the experiment, time-boxed.** Compare `gosec -concurrency 1` against
   the default across enough repeats to be worth anything — the existing manual
   protocol is 8 runs, use at least that — and record every run's finding
   count and `Stats` block. Report what you observed, including "no difference
   detected," which is a real result. Do not tune the flag into the gate on a
   single agreeing pair.
2. **Land the repeat-run agreement wrapper.** This is the actual fix and it
   does not depend on step 1. No gosec number reaches the comparator until two
   independent runs of the same tree agree; on disagreement, fail loudly with
   both reports retained as artifacts. The current mitigation is an 8-run
   protocol that "exists only as a human remembering to do it" — a discipline
   with no mechanism behind it decays, and that decay is the problem being
   fixed here.
3. **Add the coverage floor to `gosec_command`**, keyed on `Stats.files` and
   `Stats.lines` against committed baseline values. Note a floor already exists
   on the **lint** side (`verify_lint_coverage`, total findings against 50% of
   the Stage 1 baseline) — different side, different key. Do not mistake one for
   the other; `grep -n 'Stats\|Golang errors\|NumFiles' scripts/quality-ratchet.py`
   still returns nothing, so the gosec floor genuinely does not exist. Re-derive those values from
   a fresh run rather than copying `files=661` / `lines=165247` out of this
   file — those were measured before this batch and are a hint that expires.
   Make a missing or non-numeric `Stats` block a hard failure, not a default of
   zero.
4. **Make a missing `Issues` array fail** rather than silently meaning "no
   findings," matching the `Report.Error` treatment now on the lint side.
5. **Decide on gosec's `Golang errors` key** (the key literally contains a
   space) — the direct analogue of the `Report.Error` check. Its value is
   unproven and its absence is known. Either wire it in or record why not; an
   argued "no" is an acceptable outcome, silence is not.
6. **Fix the reduction advisory.** A reduction is no longer safe to describe as
   "lower the committed baseline to preserve them" now that a known mechanism
   produces spurious reductions. Reword it to say a reduction must be confirmed
   reproducible before the baseline moves, and point at the wrapper.

## Done means

- A repeat-run disagreement **fails the gate**. Prove it with a test that feeds
  the comparator two deliberately-differing reports and asserts a non-zero
  exit — not by reasoning about the code path.
- A report whose `Stats.files` or `Stats.lines` falls below the committed floor
  fails, proven by a test with a doctored report.
- A report with no `Issues` array fails; a report with an empty `Issues` array
  still passes. Both proven by tests — the distinction is the whole point.
- `grep -n 'Stats' scripts/quality-ratchet.py` returns matches inside
  `gosec_command`.
- The `Golang errors` decision is recorded in the Work log with its reasoning
  either way.
- The reduction message no longer advises lowering the baseline unconditionally.
- Step 1's experiment is written up in the Work log with the **full** run table
  — every run's count and `Stats`, not a summary — and an explicit statement of
  what was and was not established. If it converged, say so and say whether
  `-concurrency` was pinned in the gate as a result; if it did not, say that
  plainly.
- The existing full-repo baseline still passes end to end after the changes:
  a hand-dispatched `gh workflow run "Full-repo quality gate" --ref main` is
  green.

## Work log

### `04a` only — step 6, the reduction advisory reword (2026-08-25)

**`04b` (steps 1-5) is untouched and still open.** Nothing below builds the
repeat-run wrapper, adds a `Stats` coverage floor, fails on a missing `Issues`
array, decides the `Golang errors` key, or runs the `-concurrency` experiment.

**Starting commit:** `93939f6b139ab51b6612e138e40b357dd02855ce`, branch `main`,
`git status --porcelain` empty. All line numbers below are from the working tree
atop that commit. Note this file's own citations are stamped `77137106`, which is
five commits behind; every one was re-derived rather than trusted.

#### Citations re-derived (§2.2)

| This file says | Actually |
|---|---|
| `grep -n 'reductions detected' scripts/quality-ratchet.py` (line 34) | Still the right command. Returned **line 145** at `93939f6b`, not a line number this file states. |
| `gosec_command` at `scripts/quality-ratchet.py:397` (line 71) | Correct at `93939f6b` — `grep -n 'def gosec_command' scripts/quality-ratchet.py` -> `397`. Now `416` after this change shifted it. |
| `grep -n 'Stats\|Golang errors\|NumFiles' scripts/quality-ratchet.py` returns no matches (line 69) | Still no matches at `93939f6b`. `04b` step 3 is still needed. |

#### Files changed

**`scripts/quality-ratchet.py`** — `compare_counts`, the reduction branch
(`scripts/quality-ratchet.py:143-166` after the edit). The printed advisory now
reads:

```
<label>: reductions detected for <names>; re-run the same tree and confirm the
reduction reproduces before lowering the committed baseline. A reduction that
reproduces is a real improvement -- bank it; one that does not reproduce is a
dropped-findings run, and baking it into the baseline deletes real findings.
```

An explanatory comment sits above it (`scripts/quality-ratchet.py:144-158`).

**`scripts/quality-ratchet_test.py`** — see *Tests* below.

#### The advisory is shared by both callers — verified, not assumed

```
grep -n 'compare_counts(' scripts/quality-ratchet.py
116:def compare_counts(label: str, baseline: dict[str, int], actual: Counter[str]) -> int:
309:    stage_1_failed = compare_counts("audit-config linters", baseline, actual)
482:    comparison_failed = compare_counts(
```

Two callers, one message. The `audit-config linters` baseline includes
`misspell: 2`, so a routine misspell fix prints the same string a gosec drop
does. Proven by running the comparator against the **real committed baseline**
with a report reducing exactly one rule (throwaway harness, not committed):

```
=== lint (misspell 2 -> 1) exit 0 ===
audit-config linters: reductions detected for misspell; re-run the same tree and
confirm the reduction reproduces before lowering the committed baseline. ...
=== gosec (G301 59 -> 58) exit 0 ===
standalone gosec actionable rules: reductions detected for G301; re-run the same
tree and confirm the reduction reproduces before lowering the committed
baseline. ...
```

That constraint drove the wording. The message asks for a repeat run without
singling out a linter or implying a reduction is suspect: for `misspell` the
repeat run reproduces, and the message then says to bank it. Increases still
fail — untouched, and `test_lint_rejects_a_stage_1_regression` still passes.

#### Decision: pointed at the *requirement*, not at the wrapper

Step 6 says to "point at the wrapper." The wrapper is `04b` step 2 and **does
not exist**, so pointing at it from the tool's output would be a citation to
nothing — the defect class this batch is removing. Instead:

- the **printed message** states the requirement only (re-run, confirm it
  reproduces, then move the baseline) and names no tool, path or task;
- the **code comment** names `TASKS/gate-integrity/04` step 2 (04b) as the
  planned automation and says plainly that until it lands the repeat run is the
  operator's to do, and cites the runbook section for the gosec evidence.

The comment deliberately does **not** restate the drop size or the
one-in-twelve frequency; it points at
`docs/engineering/runbooks/full-repo-quality-gate.md` for both (§5, derive
don't store). Nothing in the message or comment describes the gate as
untrustworthy.

#### Tests — they existed, and were updated

`scripts/quality-ratchet_test.py` already covered this message:
`test_lint_reports_a_reduction_above_the_coverage_floor` asserted the substring
`"reductions detected for gosec"`, which is prefix-only and would have passed
unchanged through this reword. Changes:

- `REDUCTION_ADVICE` module constant (`scripts/quality-ratchet_test.py:35`) —
  one copy of the advice text, asserted by both caller tests so they cannot
  drift apart.
- `test_lint_reports_a_reduction_above_the_coverage_floor`
  (`scripts/quality-ratchet_test.py:628`) — now asserts label + full advice.
- `test_gosec_reports_a_reduction_with_the_same_advice`
  (`scripts/quality-ratchet_test.py:307`), **new** — the standalone-gosec
  caller, `G101` baseline 1 -> 0 with both known-noise entries still matched
  once. This is the caller the wording is calibrated for and it had no
  reduction test at all.
- Negative control (`scripts/quality-ratchet_test.py:626`) — a report exactly at
  baseline must **not** print `reductions detected`, so the two assertions above
  are not satisfied by a message printed unconditionally (§4.2).

**Evidence the assertions can fail (§4.1).** `HEAD`'s script was extracted with
`git show HEAD:scripts/quality-ratchet.py` into a scratch dir beside a byte-identical
copy of the final test file (`shasum` matched on both sides; **no `git stash` was
used at any point**), and the suite run there:

```
Ran 45 tests ... FAILED (failures=2)
AssertionError: '... reductions detected for gosec; re-run the same tree ...'
  not found in '... reductions detected for gosec; lower the committed baseline
  to preserve them ...'
```

Exactly the two advisory tests fail; the other 43 pass. Against the patched
script: `python3 scripts/quality-ratchet_test.py` -> `Ran 45 tests ... OK`
(44 before this change).

#### Baseline checks

| Command | Result |
|---|---|
| `python3 -m py_compile scripts/quality-ratchet.py scripts/quality-ratchet_test.py` | clean |
| `python3 scripts/quality-ratchet_test.py` | `Ran 45 tests ... OK` |
| `go build ./cmd/nanite/` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test ./...` | exit 0, 99 `ok` packages, no `FAIL` |

No quality-gate run: `TASKS/gate-integrity/README.md`'s Validation section scopes
that to `01` and `03`. No Go source, schema, workflow or baseline JSON was
touched, and nothing was pushed to `origin`.

#### Corrections made mid-flight to my own work (§7.8)

1. The first draft of the code comment restated the drop size as a literal
   number carried from this file. Removed — it now points at the runbook for it.
2. The first draft of both test assertions prefixed a placeholder-free literal
   with `f`. Removed.
3. Two line numbers in the first draft of *this log* were computed by adding my
   diff's line delta to a pre-edit `grep` result instead of re-running the grep.
   Both were wrong by two: `gosec_command` was written as `:414` (actually
   `:416` -- `grep -n 'def gosec_command' scripts/quality-ratchet.py`) and the
   missing-`Issues` branch as `:426-428` (actually `:429-430`). Corrected above.
   Arithmetic on a stale citation is still a stale citation.

#### Scope-parked — found, deliberately not fixed

1. **`docs/engineering/runbooks/full-repo-quality-gate.md` is now imprecise**
   about this tool. Its "What it does not guarantee" section says *"the
   comparator prints guidance to lower the committed baseline"*, which described
   the message before this change. Not edited: it is outside the `04a` fence and
   is the operator's calibrated language. Replacement text handed over
   separately.
2. **`TASKS/INDEX.md` and `TASKS/gate-integrity/README.md`** each quote the old
   message, both correctly stamped `At 77137106`, so both remain true as history
   and now describe superseded behaviour. Both are the operator's files;
   untouched by instruction.
3. **`docs/engineering/failure-modes.md`** (the ratchet transcript around line
   258) does not quote the changed clause — its sample line is truncated before
   the `;` — so it needs no edit for this change. It does still say *"The lint
   side has no equivalent [defense]"*, which the coverage floor in
   `verify_lint_coverage` has since made stale. Pre-existing, unrelated to
   `04a`; belongs to `06-citation-and-config-drift-sweep`.
4. **`gosec_command` still treats a missing `Issues` array as zero findings**
   (`scripts/quality-ratchet.py:429-430`: `if issues is None: / issues = []`,
   under `issues = report.get("Issues")` at `:428`).
   Confirmed present. That is `04b` step 4 — left alone.
5. **`04b`'s framing of "no coverage floor" needs a distinction when it runs.** A
   coverage floor already exists on the *lint* side (`verify_lint_coverage`,
   keyed on total finding count at 50% of the Stage 1 baseline). What `04b` step
   3 asks for — a floor on gosec's `Stats.files`/`Stats.lines` — genuinely does
   not exist. Different sides, different keys; noted so `04b` does not mistake
   one for the other.


### `04b` steps 2-5 — agreement wrapper, coverage floor, `Issues`, `Golang errors` (2026-08-25)

**Step 1 (the `-concurrency` experiment) is not in this change and remains
open.** It was dispatched separately; nothing below runs gosec, tunes
`-concurrency`, or bears on that hypothesis. Step 6 landed as `04a` and was not
touched.

**Starting commit:** `d60c8264`, branch `main`, tree clean except two tracking
files the Orchestrator was editing (`TASKS/INDEX.md` and this file). All line
numbers below were derived after the edits, with the command shown.

#### Two premise corrections found before starting

1. **Step 3's own derivation command is stale, and following it literally would
   have read as "already done."** This file (line 69 and line 117) asserts
   `grep -n 'Stats\|Golang errors\|NumFiles' scripts/quality-ratchet.py`
   returns no matches. At `d60c8264` it returned **two**, `:148` and `:149`.
   Both are *comments*, and `git blame -L 144,156 -- scripts/quality-ratchet.py`
   attributes them to **`aa454aef`** — a Wave A commit landed with `04a`, which
   therefore invalidated its own successor's premise check. **The premise
   itself was intact**: no gosec `Stats` handling existed anywhere in code. The
   "Done means" formulation (`grep -n 'Stats'` returns matches *inside*
   `gosec_command`) is the one that tests what the step cares about, and is what
   was used.
2. **This file's `-concurrency` note (line 84) states `default 10`.** gosec
   v2.28.0's `cmd/gosec/main.go:150` declares
   `flag.Int("concurrency", runtime.NumCPU(), ...)`, so the default is the
   machine's CPU count and `10` is what a 10-core host prints. Recorded for
   whoever runs step 1; not acted on here.

Also noted and **not** relied on: the local `gosec` reports `Version: dev` with
no tag, while the gate pins `gosec@v2.28.0`. No local gosec was run. Every
claim about gosec's behaviour below is from
`https://raw.githubusercontent.com/securego/gosec/v2.28.0/<file>`, cited inline.

#### Files changed

| File | What |
|---|---|
| `scripts/quality-ratchet.py` | `GOSEC_COVERAGE_FLOOR_PERCENT`, five new functions, rewritten head of `gosec_command`, `--repeat-report` added to the `gosec` subparser |
| `scripts/quality-ratchet_test.py` | 19 new tests, two-report fixtures, three new module constants |
| `scripts/gosec-repeat-run.sh` | **new**, 117 lines — the repeat-run wrapper |
| `.github/workflows/full-repo-quality.yml` | gosec step calls the wrapper; `gosec-repeat.json` added to the upload |
| `.github/quality/full-repo-baseline.json` | `standalone_gosec.coverage` |

`git diff --stat -- .github scripts` -> `4 files changed, 729 insertions(+), 19
deletions(-)`, plus the new script.

#### Step 2 — repeat-run agreement

`--repeat-report` is **required**, not optional, on the `gosec` subcommand: an
agreement check nobody has to pass decays back into the human-remembered
protocol it replaces. `gosec_command` runs it **first**, before any other check,
so no gosec number reaches the baseline comparison until two runs agree — and
because agreement covers `Stats`, `Golang errors` and the full multiset of
`Issues`, every later check reading only the first report is sound.

**Comparison is order-insensitive, and that is load-bearing rather than
cautious.** `cmd/gosec/sort_issues.go` at v2.28.0 sorts with `slices.SortFunc`
— an *unstable* sort — over `(severity, details, file, line)`, which is not a
total order: two findings differing only in column may be emitted either way
round. A byte-level diff of two honest reports would invent disagreements, and
a check that red-lights on honest input gets switched off. The fingerprint
compares the sorted multiset of fully serialized issues instead, so it catches a
drop, a same-count *swap*, and any changed field.

**The vacuous input is closed.** `--report` and `--repeat-report` naming one
file always agree, so `verify_distinct_reports` rejects that with
`samefile`. Tested.

**Proven against real CI data, not only fixtures.** The `full-repo-quality-reports`
artifact from run `32878651576` was downloaded, its 210 `Issues` truncated to a
193-entry strict subset with `files`/`lines` left untouched — the exact observed
shape — and both comparators run against the **real committed baseline**:

```
HEAD's comparator, single report  -> exit 0
  "standalone gosec actionable rules: reductions detected for G306, G501; ..."
  "standalone gosec actionable rules: ratchet passed"
this change, both reports         -> exit 1
  "standalone gosec repeat-run disagreement: ... Stats: {...found: 210} != {...found: 193}"
  "Issues: 17 finding(s) only in <first>, 0 only in <second>"
```

The unaltered artifact against the real baseline passes end to end (`exit 0`,
`206` actionable + `4` known noise = `210` = `Stats.found`).

**The wrapper** (`scripts/gosec-repeat-run.sh`) runs gosec twice with identical
flags into `gosec.json` and `gosec-repeat.json` under `--output-dir`, then
invokes the comparator with both. Fixed names so the workflow's `if: always()`
upload retains both halves — a disagreement is only diagnosable with both.
Verified under `/bin/bash` 3.2.57, which is what `shell: bash` gets on a
`macos-15` runner: `--help` -> 0, missing/unknown argument -> 2, absent package
list -> 1, empty package list -> 1. It asserts the report file is non-empty
rather than inferring it from gosec's status, because `-no-fail` makes that
status uninformative.

**Cost:** the single gosec step took **13s** in run `32878651576`
(`gh api repos/hollis-labs/nanite/actions/runs/32878651576/jobs`), in a job
whose race suite alone took **7m54s**. Doubling it is not a budget question.

The second run deliberately does **not** clear `GOCACHE` — the build-cache
hypothesis was already eliminated, and clearing it would change the mechanism
under test and the cost.

#### Step 3 — coverage floor, and the margin

Keyed on `Stats.files` and `Stats.lines` against a committed measurement in
`standalone_gosec.coverage`. **Not on finding counts**: `Stats.found` is
literally `len(Issues)` after `filterIssues`, so it tracks every remediation.
Missing or non-numeric `Stats`, and a missing or non-positive committed value,
are all hard failures — never a default of zero.

`standalone_gosec.coverage` also **requires a non-empty `source` string**, the
same idiom as `known_noise`'s required `reason`: an unstamped number cannot be
re-derived by whoever next has to move it. Tested.

**Margin: 75%, chosen against measured churn.** The largest legitimate
peak-to-trough decline in this repository's non-test Go source over its entire
history (first commit 2026-03-07 through `eb182d71`) is **7.53%** — 651 -> 602
files and 151,555 -> 140,150 lines, both inside one week in August 2026 when
several subsystems were cut. Derived by accumulating
`git log --reverse --numstat --format='C|%H|%ct' eb182d71 -- '*.go'` excluding
`_test.go` and taking the largest drawdown; the file series was sampled at a
25-commit stride, so it is a lower bound. Sanity check on the derivation: the
accumulated total came to 166,587 lines against a direct count of **166,573**
at `eb182d71` (`git ls-tree -r --name-only eb182d71 | grep '\.go$' | grep -v
'_test\.go$' | while read f; do git show "eb182d71:$f"; done | wc -l`), a 0.008%
gap from renames and binary-marked diffs.

So 25% of margin clears the worst removal wave this repo has ever had by more
than three times, while still failing a run that parsed a quarter of the tree
less than committed. An exact-equality floor would red-light on the first file
deleted; a floor loose enough to survive anything protects nothing; and a floor
that red-lights on honest work gets weakened by whoever is trying to land that
work. It tightens for free whenever the committed measurement is refreshed
upward, like the lint floor.

**Honest scope of what it catches.** The upstream package-count assertion pins
the input at 109, and step 5's `Golang errors` check catches a package whose
*analysis* failed. What is left for the floor alone is a package that vanished
*silently*: `Analyzer.load` (v2.28.0 `analyzer.go`) logs
`Skipping: <path>. Path doesn't exist.` and returns an empty slice with a **nil
error**, contributing no issues, no error entry and no `Stats`. That is a gross
failure when it happens at all, not a one-package rounding error. A floor tight
enough to catch one package would have to sit inside ordinary churn.

**This is not the repo's first coverage floor**, and the code says so:
`verify_lint_coverage` (`grep -n 'def verify_lint_coverage'
scripts/quality-ratchet.py` -> **244**) has keyed the *lint* side on total
findings at 50% of the Stage 1 baseline since CW-20260824-0023. Different side,
different key.

**Committed values: `files=661`, `lines=165247`.** Taken from the `Stats` block
of `full-repo-quality-reports/gosec.json` in **run `32878651576`** (`conclusion
success`, `head_sha eb182d713589aea540767e4d8f2060e313729c7a`, runner
`macos-15`), not from any local run — the floor is enforced against CI's runs,
and committing a locally-derived number into a gate that checks CI numbers is
the error class this batch removes. The **gosec version rests on the workflow
pin** at `.github/workflows/full-repo-quality.yml:85`, not on the artifact,
whose own `GosecVersion` field reads `dev`. That artifact is usable here because
no Go input changed in between — the range is pinned to shas, not to `HEAD`,
which means something different on every future run:

```
git diff --name-only eb182d71..9d82c56d | /usr/bin/grep -c '\.go$'                          # 0
git diff --name-only eb182d71..9d82c56d | /usr/bin/grep -cE 'go\.(mod|sum)$|tracked-go-packages'  # 0
git diff --name-only eb182d71..9d82c56d | wc -l                                             # 10  (control)
git diff --name-only eb182d71..9d82c56d | /usr/bin/grep -c '\.md$'                          # 8   (control)
git status --porcelain | /usr/bin/grep -c '\.go$'                                          # 0   (this change adds none)
```

`9d82c56d` is `main` at the end of this work, not at its start: `05` landed
(markdown only) while the review round was in progress. Re-derived against it
rather than against the `d60c8264` the earlier draft cited — which is precisely
the hazard that made a `HEAD`-anchored range wrong to ship.

The run id and this derivation are both recorded in the baseline's `source`
string, so the next person to move the numbers is not left with a bare id.

#### Step 4 — a missing `Issues` array now fails

`if issues is None: issues = []` is gone; the check is now
`if not isinstance(issues, list)`, matching the lint side. An **empty** array
still parses as a real report — and that is not merely tolerated, it is correct:
`cmd/gosec/main.go` reaches `filterIssues` unconditionally and that returns
`make([]*issue.Issue, 0)`, so a genuinely clean gosec run emits `"Issues": []`,
never `null` and never an absent key.

**One honest qualification on the "Done means" wording.** An empty `Issues`
array cannot exit 0 here, and that is correct behaviour rather than a gap:
`load_known_noise` requires a non-empty `known_noise` list and every entry must
match exactly once, so a report with no findings at all fails on the substantive
ground that the enumerated suppressions went missing. Asking for exit 0 there
would be asking the comparator to accept a report that cannot exist. What the
test pins instead is the distinction the step is actually about: an empty array
is **not** rejected as malformed, and the comparator demonstrably *ran over it*
— the known-noise summary (`0 finding(s) matched 2 enumerated entries`) and the
per-rule comparison (`G101: 0 (baseline 1)`) are both printed, neither of which
appears for a report the array check rejected.

#### Step 5 — the `Golang errors` decision: **wired in**

Not a defence-in-depth addition. It is a currently-open hole, and the argument
is stronger than "unproven value":

- `report.go` at v2.28.0 declares
  ``Errors map[string][]Error `json:"Golang errors"` `` -- a file path mapped
  to a list of `{line, column, error}`.
- `analyzer.go:408` calls `AppendError` for **every** worker result carrying an
  error, and merges `ParseErrors` output for every file that would not parse.
  gosec then still writes a well-formed report holding every *other* package's
  findings — the exact silent-subset-missing shape.
- `cmd/gosec/main.go:373-384`'s `computeExitCode` returns failure when
  `len(errors) > 0` — **`&& !noFail`**. The gate passes `-no-fail`, which is
  required (the baseline tolerates findings), and which therefore suppresses
  precisely this signal. The workflow's `bash -e` cannot see it, and the
  comparator never read it. So today a run that failed to analyse part of the
  repository reaches the comparator as a clean reduction.

That is the direct analogue of `Report.Error`, except that on the lint side
v2.11.4 exits non-zero and the shell catches it, while here **nothing** does.
Wired in as a hard failure, and `-no-fail` is documented in the wrapper as the
reason a `0` from gosec is not evidence the scan was complete.

The **key must be present**: `ReportInfo.Errors` carries no `omitempty` and
`NewAnalyzer` initialises the map (`analyzer.go:246`), so an absent key means
the report did not come from the pinned tool. A JSON `null` is accepted, because
a nil Go map and an empty one mean the same thing. Both tested. All three real
reports available locally carry `{}` — the two committed audit reports and run
`32878651576`'s artifact.

#### Tests — 45 -> 64, all green

`python3 scripts/quality-ratchet_test.py` -> `Ran 64 tests ... OK`. Nineteen
new, following `04a`'s pattern: module-level constants for shared expected text
(`REPEAT_RUN_DISAGREEMENT`, `REPEAT_RUN_AGREEMENT`, `GOSEC_COVERAGE_FLOOR_PERCENT`
and the two fixture measurements) so paired assertions cannot drift, and a
negative control.

The gosec fixtures now build a report shaped like gosec's — `Golang errors`,
`Issues`, `Stats`, `GosecVersion` — rather than `Issues` alone, which had let
tests pass against a document no gosec would emit. Both halves default to the
same content; the disagreement tests vary one deliberately.

**Negative control (§4.2):** `test_gosec_accepts_two_agreeing_runs_in_a_different_order`
feeds the second run the *same* findings in reversed order, asserts exit 0 and
the agreement line, and asserts `REPEAT_RUN_DISAGREEMENT` is **absent**. Without
it the three disagreement assertions would be satisfied by a message printed
unconditionally. It doubles as the proof that the check tolerates gosec's real
ordering nondeterminism.

**Evidence the assertions can fail (§4.1).** No `git stash` was used at any
point. Two independent forms:

*(a) Whole-file revert.* `git show HEAD:scripts/quality-ratchet.py` extracted to
a scratch dir beside a byte-identical copy of the final test file (`shasum`
matched on both sides; the two scripts' hashes differ, as a control). Result:
`Ran 64 tests ... FAILED (failures=31)` — every gosec test, because the
subcommand gained a required argument (`error: unrecognized arguments:
--repeat-report`). The 33 lint/packages/platform tests still pass, which is the
control that the harness itself is sound. This is *uninformative about the new
assertions individually*, so:

*(b) Five targeted mutants* of the final script, each disabling exactly one new
check, each run against a byte-identical copy of the final test file
(one unique `shasum` across all five dirs):

| Mutant | Failures | Which |
|---|---|---|
| agreement check returns immediately | 4 | the three disagreement tests + the agreement positive control |
| self-comparison guard returns immediately | 1 | `test_gosec_rejects_a_report_compared_against_itself` |
| coverage floor returns immediately | 7 | the six floor/`Stats` tests + the printed-floor pin |
| `if issues is None: issues = []` restored | 1 | `test_gosec_rejects_a_report_without_an_issues_array` |
| `gosec_scan_errors` returns `[]` | 2 | the `Golang errors` present/absent tests |

Every mutant failed **only** the tests targeting its check, and the failure
messages are the intended ones — e.g. the agreement mutant's dropped-finding
test fails with `AssertionError: 0 != 1` above `standalone gosec actionable
rules: ratchet passed` (the defect, reproduced), and the `issues` mutant's fails
with `'has no Issues array' not found in '... expected exactly 1 ... found 0'`
(the missing array silently counted as zero findings).

#### Baseline checks

| Command | Result |
|---|---|
| `python3 -m py_compile scripts/quality-ratchet.py scripts/quality-ratchet_test.py` | exit 0 |
| `python3 scripts/quality-ratchet_test.py` | `Ran 64 tests ... OK` |
| `bash -n scripts/gosec-repeat-run.sh` | exit 0 |
| `python3 -c "import json; json.load(...)"` on the baseline | parses |
| `./scripts/check.sh` | **exit 0** (`no stage failed`; `lint` examined nothing — no Go file changed) |
| `go build ./cmd/nanite/` | exit 0 |
| `go vet ./...` | exit 0 |

No quality-gate run was dispatched: one is authorised after this lands and is
the Orchestrator's to fire. Nothing was committed or pushed; only the paths in
the table above were touched, plus this Work log.

#### Citations

**Superseded twice — the numbers that were here have been deleted rather than
left where they can be copied.** Two of them were stale by +8 the moment they
were written (F2 below records exactly which, what they said, and why), and the
review-round edits then moved every one of them again. **Use the table in
"Review round" below, or better, re-run the commands.**

#### Corrections made mid-flight to my own work (§7.8)

1. First draft of the `Golang errors` check treated a missing key and a JSON
   `null` alike. Separated after reading `report.go` and `analyzer.go:246`:
   absence means the wrong tool produced the report (hard failure), `null`
   means a nil Go map (clean). The conflated version would have been a §4.2
   vacuous check if gosec ever renamed the key.
2. First draft of the agreement check compared the reports whole. Discarded
   after reading `cmd/gosec/sort_issues.go` — the sort is unstable and not a
   total order, so that check would have red-lighted on honest input.
3. The floor margin was first set at 90% from intuition. Replaced with 75%
   after measuring the repo's actual worst decline (7.53%); 90% would have left
   2.5 points of headroom against a wave that has already happened once.
4. `${PIPESTATUS[0]}` was used once to read the wrapper's exit code and came
   back empty — this shell is zsh, where the array is `pipestatus` and 1-based.
   Re-read without a pipe. (Same family as §3.13/§3.15; the empty value was
   loud, but a wrong index would not have been.)

#### Scope-parked — found, deliberately not fixed

1. **The runbook is now stale in two places**, and it belongs to
   `05`'s worker in this same tree — untouched by instruction.
   `docs/engineering/runbooks/full-repo-quality-gate.md`'s *"What it does not
   guarantee — one item, one direction"* says the gosec step can record a false
   improvement and that following the reduction advisory is *"one extra run"* by
   hand. Both are now mechanised. Its *"What it does guarantee"* list should
   also gain the gosec-side floor and the `Golang errors` check. Sequencing
   note: this needs to land **after** `05`, not merged into it.
2. **`TASKS/INDEX.md` and `TASKS/gate-integrity/README.md`** both carry
   `grep -n 'Stats\|Golang errors\|NumFiles' ... # -> no matches` under a
   `77137106` stamp. That was already false at `d60c8264` for the reason in
   premise correction 1, and is emphatically false now. Operator files;
   `06`'s territory.
3. **`docs/engineering/failure-modes.md`** still says *"The lint side has no
   equivalent [defense]"* — stale since `verify_lint_coverage`, and now doubly
   so. Pre-existing, `06`'s territory, as `04a` also noted.
4. **`scripts/__pycache__/` is not in `.gitignore`.** Running `py_compile` on
   either script creates it as untracked clutter. Removed by hand here; a
   `.gitignore` line would stop the next person staging it by accident.
5. **The lint side has no repeat-run agreement**, only gosec does. That is
   deliberate for now — the observed nondeterminism is gosec's, and
   golangci-lint's `Report.Error` exits non-zero — but it is an asymmetry
   someone will eventually ask about.


### `04b` review round — F1/F2/F3 addressed (2026-08-25)

A fresh reviewer returned **PASS-with-findings** on the work above (one Medium,
two Low), relayed by the Orchestrator with four smaller items and one acceptance
condition. All of it is addressed below. **`04a`'s printed advisory string is
untouched** — it is shared calibrated text and was ruled out of scope; verified
with `git diff --cached -- scripts/quality-ratchet.py | grep -E '^[-+].*(reductions detected|A reduction that reproduces)'`,
which returns nothing.

Reviewer negative results worth keeping: the fingerprint is complete against all
four `ReportInfo` fields; order-insensitivity holds across reversal, shuffle and
nested-key reorder while still catching a same-count swap, a nested field
change, `Stats.nosec` and `GosecVersion`; `verify_distinct_reports` survives
symlinks, relative symlinks, hardlinks and `./` noise; the required flag has no
stale caller; floor boundaries are exact in both directions; and the five-mutant
table was independently rebuilt with identical counts.

#### F1 (Medium) — the output composed into an endorsement of the loss

Reproduced exactly as described: feed the real artifact truncated to a 193-entry
strict subset as **both** runs, against the real committed baseline. Before the
fix the operator reads "two runs agree" and then, four lines later, an advisory
asking them to confirm the reduction reproduces — the confirmation appears
already granted. It is not: the wrapper answers *"did two adjacent runs of one
tree agree?"*, the advisory is asking whether the reduction was **earned**.
Question substitution, and the reviewer is right that it does not depend on any
reproducible-drop mechanism existing.

**(a) The agreement message now states its own bound**, printed unconditionally
on every agreement, immediately after the count and therefore **above** the
advisory in a top-to-bottom read (`GOSEC_AGREEMENT_BOUNDARY`,
`scripts/quality-ratchet.py:75`; printed at `:713`). Same input after the fix:

```
standalone gosec repeat-run agreement: two runs of this tree agree on 193 finding(s), Stats and Golang errors
standalone gosec repeat-run agreement: this rules out gosec run-to-run nondeterminism for this
  report, and nothing else. It is NOT confirmation that a reduction below was earned -- two runs
  of one tree reproduce an unearned drop exactly as well as an earned one. Bank a reduction only
  when it maps to a real code change since the committed measurement.
standalone gosec coverage floor: files=661 floor=496 (baseline 661, 75%)
standalone gosec coverage floor: lines=165247 floor=123936 (baseline 165247, 75%)
standalone gosec actionable rules: reductions detected for G306, G501; re-run the same tree and
  confirm the reduction reproduces before lowering the committed baseline. ...
standalone gosec actionable rules: ratchet passed
```

Exit stays 0, deliberately: the tool cannot know whether a drop was earned, and
failing here would red-light every legitimate improvement. What changed is that
it no longer supplies an answer to a question it did not ask.

**(b) `04a`'s stale automation comment is corrected.** It said *"until that
lands, the repeat run is the operator's to do"*; it landed, ~500 lines below in
the same file, in this same change. Rewritten at
`scripts/quality-ratchet.py:210` to say the gosec caller now repeats
automatically, **and that the automated repeat answers a narrower question than
the advisory asks** — the correction that matters more than the status update.
It also states the lint caller has no automated repeat, since the comment is
shared by both callers.

**(c) The boundary statement is written down**, as `gosec_command`'s docstring
(`scripts/quality-ratchet.py:726`) — the function that composes agreement, then
the floor, then `compare_counts` — with a pointer to it at the `compare_counts`
call (`:858`). It covers all three required points:

1. Agreement rules out gosec run-to-run **nondeterminism only**.
2. The coverage floor **does not catch** a findings drop at constant coverage —
   the run this task was opened for had *identical* `Stats.files`/`Stats.lines`;
   the files were parsed and the findings went missing downstream of parsing.
   The floor is deaf to that shape by construction and no threshold on it helps.
3. *"A reduction that reproduces is a real improvement"* is a **necessary
   condition, not a sufficient one**. **That wording is `04a`'s, approved in
   Wave A by the director layer** — this note corrects it, not the string.
   Reproducibility is not earnedness; a reduction is banked when it maps to a
   real code change since the committed measurement.

The reviewer's own probe now finds it:
`/usr/bin/grep -nEi 'cannot catch|does not catch|deterministic drop|NOT confirmation' scripts/quality-ratchet.py`
-> `:77`, `:736`, `:746`.

**Tested, with a negative control.** `AGREEMENT_BOUNDARY`
(`scripts/quality-ratchet_test.py:71`) is a module constant so the bound cannot
be silently dropped; `test_gosec_accepts_two_agreeing_runs_in_a_different_order`
asserts it present, and `test_gosec_rejects_a_repeat_run_that_dropped_a_finding`
asserts it **absent**, so the presence assertion is not satisfied by an
unconditionally printed line. A **sixth mutant** (`print(GOSEC_AGREEMENT_BOUNDARY)`
replaced with `pass`) fails exactly one test and nothing else.

#### F2 (Low) — two stale citations, and the process fix

Confirmed. My log claimed `def gosec_command` -> **684** and the `Stats` match
-> **718**; both were **692** and **726**, off by exactly the eight-line `else:`
block added after I took the numbers. Same class as `04a`'s self-corrected
mistake 3, and worse, because I had written *"All line numbers below were
derived after the edits"*.

**The process fix, applied to this round:** every citation in the table below
was derived **after the final code edit**, as the last action before writing
this section. A citation taken mid-edit is stale by construction, and the
earlier block has had its numbers deleted rather than corrected in place, so
nobody copies them.

**Unit correction, and it is the Orchestrator's, not mine:** `grep -n 'Stats'`
counts **match lines**, not substring occurrences. The Done-means bullet is met,
and met in a better shape than a raw count suggests — the hits inside
`gosec_command` are documentation, and the real read is
`stats = report.get("Stats")` at `verify_gosec_coverage:555`, which
`gosec_command` calls.

**Citations, derived last:**

```
grep -n 'GOSEC_COVERAGE_FLOOR_PERCENT = 75'  scripts/quality-ratchet.py   #  63
grep -n 'GOSEC_AGREEMENT_BOUNDARY = ('       scripts/quality-ratchet.py   #  75
grep -n 'print(GOSEC_AGREEMENT_BOUNDARY)'    scripts/quality-ratchet.py   # 713
grep -n '^def verify_lint_coverage'          scripts/quality-ratchet.py   # 269
grep -n '^def gosec_scan_errors'             scripts/quality-ratchet.py   # 475
grep -n '^def verify_gosec_coverage'         scripts/quality-ratchet.py   # 530
grep -n 'stats = report.get("Stats")'        scripts/quality-ratchet.py   # 555
grep -n '^def gosec_agreement_fingerprint'   scripts/quality-ratchet.py   # 594
grep -n '^def verify_distinct_reports'       scripts/quality-ratchet.py   # 626
grep -n '^def verify_repeat_run_agreement'   scripts/quality-ratchet.py   # 647
grep -n '^def gosec_command'                 scripts/quality-ratchet.py   # 726  (body 726..863)
grep -n 'comparison_failed = compare_counts' scripts/quality-ratchet.py   # 858
grep -c '^      - name:' .github/workflows/full-repo-quality.yml          #  13  (unchanged)
```

`grep -n 'Stats'` returns **2 match lines** inside `gosec_command` — `:747`
(docstring point 2) and `:794` (the call-site comment). Derived by slicing the
function body and counting lines containing the string, not `.count()`.

#### F3 (Low) — the flattened `.get("Golang errors")`

Confirmed and **noted in code rather than fixed**, which is what was asked.
`gosec_agreement_fingerprint` (`:594`) uses `.get("Golang errors")`, which
returns `None` for an absent key and for a JSON `null` alike — the one pair of
states `gosec_scan_errors` is careful to keep apart — and `gosec_scan_errors`
runs only on the first report (`:785`). So `--report` with `null` plus
`--repeat-report` with the key deleted exits 0. Not reachable through
`scripts/gosec-repeat-run.sh`, which runs one binary twice over one tree.

The note sits at `scripts/quality-ratchet.py:615`. **Behaviour deliberately
unchanged**: adding presence to the fingerprint is a two-line fix and I did not
make it, because the instruction was to note it and because changing a
mechanism's behaviour after it has been reviewed is how a reviewed thing stops
being the reviewed thing. It is a two-line fix for whoever wants it.

#### The four smaller items

1. **`coverage.source` no longer claims the artifact says `v2.28.0`.** The
   artifact's own `GosecVersion` reads **`dev`** (confirmed:
   `python3 -c "import json; print(json.load(open('gosec.json'))['GosecVersion'])"`),
   which is what `go install` of a tagged module reports without ldflags. The
   string now says the version rests on the pin at
   `.github/workflows/full-repo-quality.yml:85`, not on the report.
2. **`grep -c '\.go$'` is now escaped** in that string (it shipped as `'.go$'`).
3. **The range is pinned to a sha**, `eb182d71..9d82c56d`, not to `HEAD` —
   `HEAD` means something different on every future run, and it proved the
   point inside this very round: `main` advanced from `d60c8264` to `9d82c56d`
   while I was working, as `05` landed. Re-derived against the new sha: `0` Go
   files, control `10` files changed of which `8` are `.md`, and this change
   itself touches no `.go` file
   (`git status --porcelain | /usr/bin/grep -c '\.go$'` -> `0`, control `7`
   lines total). The first draft of this fix cited `d60c8264` and was already
   one commit stale by the time it was written.
4. **`13s`/`7m54s` no longer appear in the workflow.** Removed rather than
   duplicated with a caveat: two copies of one measurement go stale
   independently. The workflow comment now points at the wrapper's header, which
   carries the figures with the `gh run view <id>` re-derivation. Verified:
   `/usr/bin/grep -c '13s in run'` -> `0` in the workflow, `1` in the script.

#### Empty-`Issues` bullet — a fresh reviewer independently agreed

The reviewer reached the same conclusion recorded above and recommends leaving
it: making the Done-means bullet literally true would require gutting the
justified known-noise cardinality check. **No change.** The bullet is half-met
by design, and the half that is met is the one the step is about.

#### Acceptance condition — the script's mode

The workflow invokes `scripts/gosec-repeat-run.sh` as a bare relative path, not
via `bash`, so the file mode is load-bearing: a `100644` ships a gate that fails
on its first run. Staged with an explicit path (never `git add -A`) and read
back from the index:

```
git add scripts/gosec-repeat-run.sh
git ls-files -s scripts/gosec-repeat-run.sh
100755 5c35c638f0d3c877ef884416e9fb788ca3f9bc0f 0	scripts/gosec-repeat-run.sh
```

`100755`. My five paths are left **staged and uncommitted** so the mode cannot
be lost between here and the commit; nothing else is staged.
`docs/engineering/runbooks/full-repo-quality-gate.md` shows as modified and
unstaged — that is the other session's `05` work in this shared tree, and it
confirms the two of us stayed file-disjoint.

#### Verification after the review round

| Command | Result |
|---|---|
| `python3 -m py_compile` both scripts | exit 0 |
| `python3 scripts/quality-ratchet_test.py` | `Ran 64 tests ... OK` |
| `/bin/bash -n scripts/gosec-repeat-run.sh` | exit 0 |
| baseline JSON parses / workflow YAML parses | OK, 13 steps |
| `./scripts/check.sh` | **exit 0** (`lint` examined nothing — no Go file changed) |
| truncated-both-runs reproduction | exit 0, boundary printed above the advisory (output quoted under F1(a)) |

**Six mutants rebuilt from the final script**, each against a byte-identical copy
of the final test file (one unique `shasum` across all six dirs plus the repo's),
each failing **only** its own tests:

| Mutant | Failures |
|---|---|
| agreement check returns immediately | 4 |
| `print(GOSEC_AGREEMENT_BOUNDARY)` -> `pass` | 1 |
| self-comparison guard returns immediately | 1 |
| coverage floor returns immediately | 7 |
| `if issues is None: issues = []` restored | 1 |
| `gosec_scan_errors` returns `[]` | 2 |

No quality-gate run dispatched; that is the Orchestrator's to fire after this
lands. Nothing committed or pushed. No `git stash` at any point.


## Review notes

**`04a` reviewed 2026-08-25 at `d17c8b62`/`f553b0a7` by a fresh reviewer
dispatch** — no shared context with the implementing worker. Transcribed by the
Orchestrator; the reviewer agent type is read-only by design. **`04b` is not
reviewed and not started.**

**Verdict: PASS**, with two low-severity precision findings in the new code
comment, both since fixed in `aa454aef`.

**Scope held exactly.** The diff touches only `compare_counts`' print, its
comment, and tests. `.github/workflows/full-repo-quality.yml` and
`.github/quality/full-repo-baseline.json` are untouched — no part of `04b`
leaked in.

**Verified independently, not read:** the advisory is genuinely shared —
`compare_counts` is called at `:309` (`"audit-config linters"`) and `:482`
(`"standalone gosec actionable rules"`), and the committed baseline carries
`misspell: 2`. The reviewer reproduced both callers against the real baseline
and got byte-identical advice under different labels, which is the constraint
that shaped the wording. The message endorses banking a reduction that
reproduces, so it does not over-correct into "never trust a reduction" — the
failure mode that would have been worse than the original defect.

**The §4.1 evidence was re-run rather than trusted:** 45 tests against the
patched script pass; against `93939f6b`'s script exactly 2 fail, and they are
the two advisory tests. The pre-existing test asserted only the prefix
`"reductions detected for gosec"`, which matches both the old and new message —
confirmed by extracting the parent revision. It could not have detected the
defect it nominally covered.

**§4.2 answer, recorded honestly:** the reachable vacuous input — an
unconditional print independent of `reductions` — is closed by the negative
control. The residual is generic to string-constant assertions: emptying
`REDUCTION_ADVICE` would silently restore the prefix-only weakness, and nothing
guards the constant's content. Flagged as the honest answer, not as a defect.

### Finding 1 — "identical Stats" was false. Fixed.

The comment said the suspect gosec run had *"identical Stats"*. `Stats` is
`{files, lines, nosec, found}`, and `found` equals `len(Issues)` — verified
against the committed report at
`docs/audits/2026-08-21-go-quality/raw-1d3bfd96/gosec.json`, where both are
335. So in a 210-vs-193 run `found` moved with the drop and `Stats` cannot have
been identical. Every upstream source — this file's own step 3, and the
runbook — names only `files` and `lines`.

The imprecision pointed the wrong way for its intended reader: `04b` step 3
keys its floor on `files`/`lines` and **deliberately not on counts**, an
instruction that only makes sense once you know `found` moves.

### Finding 2 — exclusivity beyond the evidence. Fixed.

*"The one step that can also produce a decrease nobody earned"* is stronger
than the runbook's calibrated *"one **known** soft spot"*. `Report.Error`
catches a lint run that failed to analyze, not one that analyzed everything and
dropped findings silently — the exact shape observed on gosec — and
`verify_lint_coverage` is a 50%-of-total floor a small unearned decrease would
pass. Softened to "known to".

### Recorded for `04b`

A coverage floor already exists on the **lint** side (`verify_lint_coverage`,
total findings against 50% of the Stage 1 baseline). What step 3 asks for — a
floor on gosec's `Stats.files`/`Stats.lines` — genuinely does not exist.
Different sides, different keys. Written into step 3 so its implementer does
not mistake one for the other. Step 4's gap is also confirmed still present at
`scripts/quality-ratchet.py:429-430`.

### Noted, not acted on

`TASKS/INDEX.md` and `TASKS/gate-integrity/README.md` still quote the old
message. Both sit under a `77137106` stamp, so they are true as history while
describing superseded behavior — `06`'s territory.

---

## Review notes — `04b` steps 2-5

**Reviewed 2026-08-25 at `d60c8264` by a fresh reviewer dispatch** — no shared
context with the implementing worker. **Verdict: PASS-with-findings** (one
Medium, two Low); all three fixed and re-verified. Transcribed by the
orchestrator; the reviewer agent type is read-only and wrote nothing here.
**Step 1 — the `-concurrency` experiment — is not covered by this review and
remains open.**

**Scope held.** The diff touches `scripts/quality-ratchet.py`, its test file, the
new `scripts/gosec-repeat-run.sh`, the workflow's gosec step and the baseline's
`standalone_gosec.coverage`. `04a`'s printed advisory string is untouched —
`git diff --cached -- scripts/quality-ratchet.py | grep -E '^[-+].*(reductions detected for|A reduction that reproduces is a real)'`
returns nothing against 389 added lines.

### What the reviewer failed to break — the useful negative result

`ReportInfo` (`gosec v2.28.0/report.go:8-13`) has exactly four fields and the
fingerprint compares all four, so no difference can hide in an uncompared field.
Order-insensitivity holds — reversed issues, shuffled issues and reversed JSON
key order *inside* each issue all agree — while a same-count swap, any changed
field, a changed **nested** field (`cwe.url`), `Stats.nosec` and `GosecVersion`
each fail. `verify_distinct_reports` rejects a literal same path, a symlink, a
*relative* symlink from a subdirectory, a hardlink and `./` path noise; two
distinct paths with identical content correctly pass, being indistinguishable
from two honest agreeing runs. Omitting `--repeat-report` exits 2, and the only
`gosec` subcommand caller in the repo is the wrapper, so no stale caller survived
the signature change. Floor boundaries are exact in both directions
(`files=496` passes, `495` fails; `lines=123936` passes, `123935` fails), with a
string or JSON `true` in `Stats.files` failing. The wrapper fails loudly on every
branch under `/bin/bash` 3.2.57. The worker's five-mutant table was rebuilt
independently and reproduced exactly, each mutant failing only its own tests.

### Finding 1 — MEDIUM. The output composed into an endorsement of the loss. Fixed.

Feeding the real artifact truncated to a 193-entry strict subset as **both** runs
against the real committed baseline, the tool reported agreement, passed the
floor, and then printed `04a`'s advisory asking the operator to *"confirm the
reduction reproduces"* before banking it. **An operator reading top to bottom had
already been handed that confirmation four lines above.** Exit 0, endorsing the
deletion of 17 real findings — the exact outcome `04a` exists to prevent.

The mechanism is **question substitution**: the wrapper answers *"did two
adjacent runs of the same tree agree?"*, the advisory asks *"did the reduction
reproduce?"*, and the correct confirmation for the latter is *"does this map to a
real code change since the committed measurement?"* The finding does **not**
depend on a reproducible-drop mechanism existing; two runs inside one invocation
are simply not evidence a reduction was earned.

Fixed in three parts. **(a)** A `GOSEC_AGREEMENT_BOUNDARY` line, printed
unconditionally on agreement, stating that agreement rules out run-to-run
nondeterminism *and nothing else* and is not confirmation a reduction was earned
— so the bound now lands **above** the advisory in a top-to-bottom read.
**(b)** `04a`'s stale comment, which still said the gosec-side automation had not
landed when it had landed ~500 lines above in the same file, rewritten to say the
automated repeat answers a narrower question than the advisory asks, and that the
lint caller has no automated repeat. **(c)** The boundary written into
`gosec_command`'s docstring — the function that composes agreement → floor →
`compare_counts` — covering all three points: agreement rules out nondeterminism
only; the floor cannot see a findings drop at constant `files`/`lines`; and the
advisory's *"a reduction that reproduces is a real improvement"* is therefore a
**necessary condition, not a sufficient one.**

**Point (c)'s third clause is a correction to `04a`'s model, approved in Wave A
by the director layer, and is attributed there rather than to this worker.**
Exit status is deliberately unchanged: the tool cannot know whether a drop was
earned, and failing would red-light every legitimate improvement. What changed is
that it no longer supplies an answer to a question it did not ask.

Guarded by a negative control — the boundary is asserted present on agreement and
**absent** on disagreement — and by a sixth mutant that stubs the print and fails
exactly one test.

### Finding 2 — LOW. Two of nine re-derived citations were stale by +8. Fixed.

`def gosec_command` was cited as `684` (actually `692`) and the `Stats` match as
`718` (actually `726`), both off by exactly the eight-line `else:` block added
after the citations were taken — the same class as `04a`'s own self-corrected
mistake #3. The stale numbers were **deleted rather than corrected in place**, so
they cannot be copied forward, and the process fix is recorded: derive every
citation last, after the final edit.

*Orchestrator error, recorded here because it reached the reviewer's brief:* I
reported `Stats` as appearing **twice** inside `gosec_command`, having counted
substring occurrences with `.count()` when the "Done means" bullet is about
`grep -n` **matches** — one line contained both `Stats.files` and `Stats.lines`.
The bullet was met throughout, and met in the better shape.

### Finding 3 — LOW, flagged not asserted. Noted, behaviour unchanged.

`gosec_agreement_fingerprint`'s `.get("Golang errors")` flattens an absent key and
a JSON `null`, and `gosec_scan_errors` — which treats absence as a hard failure —
runs on the first report only, so `--report` with `null` plus `--repeat-report`
with the key deleted exits 0. Not reachable through the wrapper, which runs the
same binary twice. The worker noted it and deliberately did **not** change
behaviour after review, which is the right call.

### Smaller items, all addressed

The baseline's `coverage.source` now states that the `v2.28.0` claim rests on the
workflow's pin rather than on the artifact, whose own `GosecVersion` field reads
`dev`; its `grep` pattern is escaped and its range pinned to a sha rather than
`HEAD`. The duplicated `13s`/`7m54s` figures were **removed** from the workflow
rather than caveated, leaving one copy that carries its own re-derivation
command. `scripts/gosec-repeat-run.sh` lands `100755`, verified after staging
(`git ls-files -s`) — load-bearing, because the workflow invokes it as a bare
relative path.

### The empty-`Issues` bullet — half met, deviation accepted

A missing `Issues` array fails; an **empty** one exits 1, but on a pre-existing
known-noise cardinality check rather than on the array check. The reviewer reached
the worker's conclusion independently: the bullet exists to stop the array check
being written as a truthiness test, `isinstance(issues, list)` satisfies that, and
making the bullet literally true would require gutting a justified check that four
enumerated `#nosec` annotations each match exactly once. Left as is by agreement.

### Orchestrator re-derivation

Every load-bearing claim was reproduced independently before acceptance: the
committed floor values against run `32878651576`'s downloaded artifact
(`{files: 661, lines: 165247, nosec: 9, found: 210}` — exact match); all four
gosec source claims in the module cache at `v2@v2.28.0`; the real-data proof in
both directions; Finding 1's composed output before and after the fix; and the
post-fix acceptance run — 64 tests OK, `./scripts/check.sh` exit 0,
`/bin/bash -n` 0, unaltered artifact exit 0, genuine disagreement exit 1.
