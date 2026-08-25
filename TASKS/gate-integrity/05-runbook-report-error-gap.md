# Close the runbook's vacuous-pass gap — local reproduction must check Report.Error

**Phase:** 1 — Measurement integrity
**Status:** not-started
**Depends on:** none
**Touches:** `docs/engineering/runbooks/full-repo-quality-gate.md`. Repo: nanite.

## Context

`TASKS/audit-remediation/`'s task `0023` fixed a lint ratchet that passed
vacuously: `golangci-lint` sets `Report.Error` when it could not analyze
something it was asked to analyze, and the comparator was not looking, so a
run that scanned nothing could report success. The fix landed on the CI side —
`scripts/quality-ratchet.py:183` reads `section.get("Error")`.

**The same hole is still open on the human-facing side.** Verified at
`77137106`:

```
grep -c 'Report.Error' docs/engineering/runbooks/full-repo-quality-gate.md   # -> 0
grep -n 'section.get("Error")' scripts/quality-ratchet.py                    # -> 183
```

The runbook's local-reproduction section (around line 45 at `77137106` —
re-derive) walks a reader through running the gate's checks by hand. Following
it produces a clean-looking local pass from a lint run that analyzed nothing,
which is precisely the failure `0023` exists to prevent. The person most likely
to follow that runbook is someone debugging a confusing gate result — the worst
possible moment to hand them a false green.

This is why it is its own task rather than a line in `06`'s drift sweep.
`06` fixes citations that point at moved lines; this fixes a procedure that
produces a wrong answer. Different severity, different acceptance.

The batch's own `README.md` records this as load-bearing correction 7.

## What to do

1. Read `scripts/quality-ratchet.py`'s `golangci_scan_error` and the
   `Report.Error` handling around line 183 — **re-derive the line number** —
   so the runbook describes what the comparator actually does rather than a
   paraphrase of it.
2. Update the local-reproduction block so a reader running the gate by hand
   checks `Report.Error` before believing an issue count. Give them the actual
   command, not a description of the concept — something they can paste, in the
   style the rest of the runbook already uses.
3. Say plainly what a set `Report.Error` means: the run did not analyze what it
   was asked to, so its issue count is meaningless — **not** "zero issues."
   That inference is the entire defect.
4. While in the file, check whether the same gap exists for the gosec
   reproduction path. Task `04` is adding `Stats`-based coverage checks and a
   repeat-run wrapper on the CI side; if `04` has already landed, the runbook
   should reflect it, and if it has not, note the dependency in the Work log
   rather than documenting a mechanism that does not exist yet.
5. Do not rewrite the runbook beyond this. It is a working document and the
   rest of it is not in scope.

## Done means

- `grep -c 'Report.Error' docs/engineering/runbooks/full-repo-quality-gate.md`
  returns a non-zero count, in the local-reproduction section specifically.
- The runbook gives a runnable check, and states the "meaningless, not zero"
  interpretation explicitly.
- A reader following the runbook against a deliberately-broken invocation (for
  example, a mistyped package path) reaches the conclusion "this run is
  invalid" rather than "this run found no issues." **Verify by actually doing
  it** and record the invocation and the output in the Work log — this is a
  documentation task whose acceptance is behavioral, so do not accept it on
  inspection.
- Any gosec-side gap is either fixed or explicitly recorded as pending `04`.

## Work log

## Review notes
