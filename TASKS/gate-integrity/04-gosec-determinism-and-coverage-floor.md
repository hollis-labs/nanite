# Stop a silently-degraded gosec run from passing the ratchet — and test the concurrency lead

**Phase:** 1 — Measurement integrity
**Status:** `04a` implemented (step 6, the advisory reword). `04b` — steps 1-5 — remains **not-started**. Set this way rather than to a whole-file `implemented` because five of the six steps and five of the six "Done means" bullets are untouched; only *The reduction message no longer advises lowering the baseline unconditionally* is satisfied.
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


## Review notes
