# Close the runbook's vacuous-pass gap — local reproduction must check Report.Error

**Phase:** 1 — Measurement integrity
**Status:** implemented
**Depends on:** none
**Touches:** `docs/engineering/runbooks/full-repo-quality-gate.md`. Repo: nanite.

## Context

`TASKS/audit-remediation/`'s task `0023` fixed a lint ratchet that passed
vacuously: `golangci-lint` sets `Report.Error` when it could not analyze
something it was asked to analyze, and the comparator was not looking, so a
run that scanned nothing could report success. The fix landed on the CI side —
`golangci_scan_error` in `scripts/quality-ratchet.py` reads `Report.Error` and
raises before any count is compared.

**Premise correction, measured 2026-08-25 at `d60c8264`. The Work log carries
the run.** This task was authored asserting that the runbook's
local-reproduction block reproduces that vacuous pass. **It does not, and did
not when the task was written.** The block is fail-closed twice over:
`golangci-lint` v2.11.4 exits `7` on a scan error *even with*
`--issues-exit-code=0`, `set -euo pipefail` aborts the block there before
`quality-ratchet.py` ever runs, and had the exit code been dropped
`golangci_scan_error` raises rather than comparing counts. Following the block
literally cannot produce a passing ratchet from a broken run.

What is real is a **reading** hazard rather than a control-flow one. On a cold
`golangci-lint` cache, the broken run's entire stdout is the single line
`0 issues.` — byte-for-byte what a clean run prints — with the diagnosis only on
stderr and in an exit status nobody looks at. Warm, the same broken run prints
the full `2813 issues:` summary and still exits `7`. And a report the reader did
not watch being produced — `audit-lint.json` out of the
`full-repo-quality-reports` artifact, which this runbook advertises in its
opening paragraph — carries no exit status at all. That is the gap this task
closes.

**The task's original two commands were also the wrong test, independently of
the premise.** `grep -c 'Report.Error' <runbook>` is whole-file, and the file's
one pre-existing hit sat in the *guarantees* section, nowhere near the
local-reproduction block — so a non-zero answer never meant the block carried
the check, and the `-> 0` it recorded would not have meant what the task
assumed. Scope the grep to the section instead:

```
awk '/^The baseline lives at/,/^Stage 1 is active now/' \
  docs/engineering/runbooks/full-repo-quality-gate.md \
  | /usr/bin/grep -c -F 'Report.Error'
#   -> 0 at d60c8264   (whole-file count there: 1, in the guarantees section)
#   -> 4 after this task
```

For the record, both of the original Context citations have drifted. At
`77137106` they read `grep -c 'Report.Error' <runbook> # -> 0` and
`grep -n 'section.get("Error")' scripts/quality-ratchet.py # -> 183`; at
`d60c8264` the same commands return `1` and `206`. `golangci_scan_error` is
cited by function name everywhere in this task's output for that reason.

The runbook's local-reproduction section walks a reader through running the
gate's checks by hand. The person most likely to follow it is someone debugging
a confusing gate result — the worst possible moment to hand them a line that
reads `0 issues.`

This stays its own task rather than a line in `06`'s drift sweep. `06` fixes
citations that point at moved lines; this fixes what a procedure tells a reader
to conclude. Different severity, different acceptance.

The batch's own `README.md` records this as load-bearing correction 7. That
entry's `grep -c 'Report.Error' … # -> 0` is the same whole-file test corrected
above; its conclusion ("anyone reproducing the gate by the runbook can get a
clean-looking local pass") is too strong — the pass is clean-*looking* on
stdout, but it is not a pass.

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

**Worker session `nanite-05`, 2026-08-25. Started at `d60c8264`, shared working
tree with session `nanite-36` (task `04b`) — 7 dirty paths at start, none mine.**

### Outcome in one line

The task's premise is **disproved**: the local-reproduction block was already
fail-closed against `Report.Error`, twice. The real defect is what the reader
*sees*, not what the shell does, and that is what the runbook change fixes.

### The empirical run — verbatim

Deliberate break: the reproduction block from
`docs/engineering/runbooks/full-repo-quality-gate.md` extracted byte-for-byte
from the fenced block, with **exactly one line added** after the discovery
loop, and nothing else changed:

```
+packages+=("./internal/does-not-exsit")   # <-- deliberate typo: "exsit"
```

Run as `bash <block>` from the repo root, `golangci-lint` v2.11.4
(`golangci-lint --version` -> `golangci-lint has version 2.11.4 built with
go1.26.1`), which is the version pinned in the workflow.

**Cold `golangci-lint` cache** (`golangci-lint cache clean` immediately before):

```
EXIT=7
--- stdout ---
0 issues.
--- stderr ---
level=error msg="[linters_context] typechecking error: stat /Users/chrispian/dev/hollis-labs/apps/nanite/internal/does-not-exsit: directory not found"
```

**Warm cache, same broken block, twice in a row:**

```
BROKEN_RUN_1_EXIT=7
--- stdout ---
2813 issues:
* cyclop: 289
... (full per-linter summary)
--- stderr ---
level=error msg="[linters_context] typechecking error: stat /Users/chrispian/dev/hollis-labs/apps/nanite/internal/does-not-exsit: directory not found"
BROKEN_RUN_2_EXIT=7   (identical output)
```

**Control — the unmodified block, cold cache:**

```
EXIT=0
2813 issues:
...
platform: runner=macos-15 goos=darwin goarch=arm64 matches baseline
audit-config linters coverage floor: current=2813 floor=1407 (baseline 2813, 50%)
audit-config linters: baseline=2813 current=2813
audit-config linters: ratchet passed
correctness Stage 2: errcheck=0, errorlint=0, nilerr=0
correctness Stage 2: zero-tolerance passed
```

So the control proves the block can reach the comparator and pass (§4.2), and
cold cache alone does not zero the count — the `0 issues.` comes from the broken
path plus a cold cache together.

**Conclusion: a false green was *not* reachable through control flow.** The
block exits `7`, `set -euo pipefail` aborts it, and `quality-ratchet.py` never
runs. A reader following the runbook literally reaches neither sentence — the
block simply stops, and the only two signals are one `level=error` line on
stderr and the exit status.

**A false green *was* reachable through reading.** On a cold cache the entire
stdout is `0 issues.` — identical to a clean run — and nothing anywhere says the
run is invalid.

I also checked the "pasted into a terminal" path, because the block is a fenced
`bash` snippet and readers paste those. `set -euo pipefail` is honored in an
interactive paste in both shells here, so that path is fail-closed too:

```
zsh  -is  set -e  -> exit=1  'STILL RUNNING' lines=0
bash -is  set -e  -> exit=1  'STILL RUNNING' lines=0
zsh  -is  CONTROL -> exit=0  'STILL RUNNING' lines=1     (positive control)
bash -is  CONTROL -> exit=0  'STILL RUNNING' lines=2     (positive control)
```

My **first** attempt at that measurement was wrong and I am recording the
correction (§7.8): I wrote the harness as `for form in "zsh -is"; do $form < f;
done`, and zsh does not word-split an unquoted parameter expansion, so every row
returned `127` and **the positive control returned 0 matches too**. A control
that fails the same way as the subject is §3.17's signal that the instrument is
broken, not the subject. Rewritten as a function taking separate arguments, it
gives the table above.

### Every command run to derive a number, beside its result

All at `d60c8264` unless noted. `/usr/bin/grep` throughout (§3.13); the runbook
file itself was clean in the shared tree, `scripts/quality-ratchet.py` was not,
so anything about that file is derived with `git show HEAD:`.

```
git log -1 --format=%H                                          -> d60c8264fbbe6bf9106345522f6b69b0f21ba6a7
git status --short | wc -l                                      -> 7

# the premise, whole-file (the wrong test)
/usr/bin/grep -c -F 'Report.Error' <runbook>                    -> 1   (task file said 0 at 77137106)
/usr/bin/grep -n -F 'Report.Error' <runbook>                    -> 61: in "What it does guarantee"
/usr/bin/grep -c -F 'quality-ratchet.py' <runbook>              -> 2   (positive control for the above)

# the premise, section-scoped (the right test)
awk '/^The baseline lives at/,/^Stage 1 is active now/' <runbook> | /usr/bin/grep -c -F 'Report.Error'
    at HEAD  -> 0
    after    -> 4
git show HEAD:<runbook> | awk '<same range>' | /usr/bin/grep -c -F 'quality-ratchet.py'  -> 1
    (positive control: the awk range does select real content at HEAD)

# the comparator
git show HEAD:scripts/quality-ratchet.py | /usr/bin/grep -n -F 'section.get("Error")'    -> 206
/usr/bin/grep -n -F 'section.get("Error")' scripts/quality-ratchet.py                    -> 236  (dirty tree, 04b)
git show HEAD:scripts/quality-ratchet.py | /usr/bin/grep -c -F 'def golangci_scan_error' -> 1
/usr/bin/grep -c -F 'def golangci_scan_error' scripts/quality-ratchet.py                 -> 1
git show HEAD:scripts/quality-ratchet.py | /usr/bin/grep -n -F 'golangci_scan_error'     -> 22, 175, 296

# the artifact path that has no exit status
git show HEAD:.github/workflows/full-repo-quality.yml | sed -n '160,173p'
    -> uploads audit-lint.json, audit-lint.txt, audit-linters.json, gosec.json,
       deadcode.txt as `full-repo-quality-reports`

# gosec dependency (see below)
git show HEAD:.github/workflows/full-repo-quality.yml | /usr/bin/grep -n gosec  -> :85 installs gosec@v2.28.0
gosec --version | /usr/bin/grep Version                                          -> Version: dev
git show HEAD:scripts/quality-ratchet.py | /usr/bin/grep -c -F 'gosec_scan_errors'  -> 0
/usr/bin/grep -c -F 'gosec_scan_errors' scripts/quality-ratchet.py                  -> 3   (uncommitted, 04b)
git show HEAD:scripts/quality-ratchet.py | /usr/bin/grep -c -F 'def gosec_command'  -> 1   (positive control)
git ls-tree HEAD -- scripts/gosec-repeat-run.sh | wc -l                             -> 0   (untracked)

# the two 04b-assigned passages, re-derived and left alone
/usr/bin/grep -n 'one item, one direction' <runbook>  -> 65
/usr/bin/grep -n 'one extra run' <runbook>            -> 74
```

Numbers that will drift and are stamped as such: `2813`, `1407` and `166` are
tied to the 2026-08-25 baseline; `206`/`236`/`175` move with `04b`.

### What changed

One file, one hunk — `git diff -U0 -- <runbook> | /usr/bin/grep -c '^@@'` -> `1`,
`@@ -140,0 +141,61 @@`, a pure insertion immediately after the reproduction
block's closing fence. Nothing else in the runbook was touched.

The insertion:

- states that a set `Report.Error` makes the count **meaningless, not zero**,
  and that such a count must never be compared against the baseline or written
  into it;
- records the cold/warm x broken/unmodified matrix above, so the next reader
  does not have to re-derive that the block is fail-closed;
- names `golangci_scan_error` as the comparator-side guard;
- says plainly that `0 issues.` beside a nonzero exit is a scan that did not
  happen, and to check `$?`;
- gives a pasteable `python3 - "$report" <<'PY'` check for the case with no exit
  status to lean on, including `audit-lint.json` from the artifact.

The pasteable check was verified **as committed** — extracted back out of the
markdown with a regex and run against both a broken and a good report:

```
A) broken report -> INVALID: golangci-lint set Report.Error, so this run did not analyze
                    what it was asked to and its 0 issue(s) mean nothing: typechecking
                    error: stat .../internal/does-not-exist: directory not found
                    exit=1
B) good report   -> scan covered its targets: 166 issue(s)
                    exit=0
```

B is the positive control: the check is capable of passing, so A's failure is
measuring something.

### `golangci_scan_error` cited by function name, not by line number — reasoning

The task's step 1 says "re-derive the line number." I derived it (`175` at
`d60c8264` for the `def`, `206` for the `section.get("Error")` line, `236` in
the shared dirty tree) and then deliberately did **not** put any of them in the
runbook. Reasons, in order of weight:

1. This document's entire complaint is that a stale citation lets a reader
   believe a wrong thing. Embedding a volatile line number in the fix would be
   writing the bug into the fix.
2. The number is *already* wrong in two directions at once right now: `04b`'s
   uncommitted 314-insertion diff moves `206` to `236`, so anything I wrote
   would be stale before it was reviewed.
3. `def golangci_scan_error` is unique at both HEAD and tree (`-c` -> `1` both
   ways, above), so the name is a strictly better locator than the number — it
   is greppable, stable across `04b`, and self-describing.

The runbook says so explicitly for the next person: *"Grep that function by
name; it moves, so do not cite a line number for it."*

### Item 4 — gosec: pending `04`, deliberately not documented

The runbook has no gosec local-reproduction block at all, so there is no
gosec-side instance of *this* task's defect to fix. The gosec-side mechanism
`04` is building is **not landed and not verifiable here**, on two independent
counts, both checked rather than assumed:

- `gosec_scan_errors` appears **0** times in `scripts/quality-ratchet.py` at
  `d60c8264` and **3** times in the shared working tree; `scripts/gosec-repeat-run.sh`
  is untracked at HEAD (`git ls-tree HEAD -- …` -> 0 lines) and present on disk.
  Both are `nanite-36`'s uncommitted `04b` work.
- Local `gosec` reports `Version: dev` against the workflow's pinned
  `gosec@v2.28.0` (`:85` at HEAD), so even a behavioral check here would not be
  evidence about what CI runs.

So the gosec side is recorded as **pending `04`** and nothing about it was
written into the runbook. When `04b` lands, the runbook's gosec material needs a
pass — see the next section.

### Found and deliberately not fixed (scope-parking, §6)

1. **Two `04b`-assigned passages, noted and untouched.** `:65`
   (`### What it does not guarantee — one item, one direction`) and `:74`
   (`… reduction — it is one extra run.**`), re-derived above. `04b` automates
   the manual gosec re-run those describe, which makes them stale. They are
   `04b`'s to correct, land after this, and correcting them here would mean
   writing against uncommitted code. Left exactly as found — the single-hunk
   diff above is the proof.

2. **A third gosec passage in the same family, not in my fence.** `:224`,
   *"**Run `gosec` at least three times and require identical finding sets
   before writing any number into the baseline.**"* — the same manual procedure
   `04b` automates, in the "Standalone gosec known noise" section rather than
   the guarantees section. Not touched. Whoever corrects `:65`/`:74` should
   look at `:224` in the same pass; it is easy to miss because it is 150 lines
   away from the other two.

3. **`golangci_scan_error`'s docstring is stale either way — routed to `06`.**
   Its closing sentence reads:

   ```
   git show HEAD:scripts/quality-ratchet.py | sed -n '195,200p'
   #   ... or a caller that dropped the exit code, such as
   #   the local reproduction procedure in
   #   docs/engineering/runbooks/full-repo-quality-gate.md, which does not check
   #   it.
   ```

   That is wrong **in both worlds**, which is why it needs no adjudication:
   *before* this task the procedure did not drop the exit code (it exits `7`
   under `set -euo pipefail` — measured above), and *after* this task the
   runbook checks `Report.Error` explicitly. Either way the named example is
   false. Note the surrounding paragraph's point still stands — the comparator
   genuinely cannot see how a report was produced; only the example is wrong.

   Not fixed here: `scripts/quality-ratchet.py` is outside this task's
   single-file `Touches`, and it is the file carrying `04b`'s uncommitted work.
   **Routed to `06`.**

4. **`README.md`'s load-bearing correction 7 carries the same over-strong
   claim** as this task's original Context ("anyone reproducing the gate by the
   runbook can get a clean-looking local pass") and the same whole-file grep.
   Corrected in this task's Context; the batch README is not in `Touches` and
   was not edited.

5. **A stray `scripts/__pycache__/` appeared mid-session and I removed it.**
   Honest account: it showed up between two `git status` checks, taking the
   dirty count from 7 to 8. I assumed it was mine (I had run
   `python3 scripts/quality-ratchet.py lint` four times) and deleted it — then
   tested causation and found it was **not** mine: neither
   `python3 scripts/quality-ratchet.py --help` nor a full run of the
   reproduction block recreates it, because a script run as `__main__` is never
   byte-compiled to `__pycache__`. It was almost certainly `nanite-36`'s test
   run importing the module. It is a regenerable cache, so nothing was lost, but
   I should have established causation before deleting. `.gitignore` has no
   `__pycache__` entry (`/usr/bin/grep -n -F '__pycache__' .gitignore` -> no
   match, against a 91-line file) — worth adding, not in scope here.

### Baseline check

`./scripts/check.sh` — output recorded in the report accompanying this task.
This change touches one markdown file, so the golangci-lint stage correctly
reports `examined nothing: lint`; that is the expected result for a docs-only
diff, not a gap in the check.

### Commit, and the decision capture

Landed as `9d82c56d` — `git show --stat 9d82c56d` -> exactly the two paths in
`Touches` plus this file. Committed with the pathspec form
(`git commit -F <msg> -- <path> <path>`) rather than `git add` + `git commit`,
because by then `nanite-36` had **staged** its `04b` work: five paths, 952
insertions, sitting in the shared index. A plain `git commit` would have
swallowed all of it. The pathspec form builds its tree from `HEAD` plus the
named paths and leaves the index alone; verified by capturing
`git diff --cached` before and after and comparing —
`shasum` -> `0bfed121cff4ef6ba61bc0b91a5b46a87c0bd925` both times, `cmp` clean.
No `--amend` was used afterwards for the same reason: an amend *does* take the
index.

`./scripts/check.sh` -> exit `0`; `format OK (1s)`, `vet OK (1s)`,
`lint --- (0s) examined nothing` (`0 changed Go files vs d60c8264`),
`test OK (4s)`, trailer
`check.sh: no stage failed — but these examined nothing: lint`. Expected for a
docs-only diff.

**Decision capture not performed by this worker.** The standing instruction is
that decisions go to Tesseract (`user/chrispian/memory/decisions`, tags
`["nanite","gate-integrity"]`). No `mcp__mux__memory_write` tool was present in
this session's toolset and no equivalent CLI surface was found
(`mux --help` exposes no memory command; `/opt/homebrew/bin/tesseract` is the
OCR binary). The capture text is in this session's report to the dispatcher
rather than written here, so nothing claims a write that did not happen.

### Shared-tree discipline

`git status --short` was run after every verification step. The dirty set at
start and at commit is the same 7 paths, all `nanite-36`'s, plus the
`__pycache__` excursion described above. `git add` was given exactly two
explicit paths; no `-A`, no `.`, no `-a`. No `git stash` at any point.


## Review notes
