# Stop a silently-degraded gosec run from passing the ratchet — and test the concurrency lead

**Phase:** 1 — Measurement integrity
**Status:** not-started
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
   `Stats.lines` against committed baseline values. Re-derive those values from
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

## Review notes
