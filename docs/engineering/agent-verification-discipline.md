# Agent verification discipline

**Status:** active · authored 2026-08-24, session `session-20260824-37765f5d`
**Companions:** `failure-modes.md` (why measurements mislead) · `tracking-integrity.md` (drift checks)

---

## What this is, and how to use it

This is the block of execution rules that every dispatched agent must follow. It is **operational** — for an agent doing work right now. Its companion `failure-modes.md` is **analytical** — for an author writing a task file, explaining *why* these rules exist. Read that one once; follow this one every time.

**Reference this file from dispatch prompts. Do not copy it inline.**

That instruction is the point of the document. This block has been propagating between sessions by copy-paste — an agent inherits it in a boot prompt, copies it into the prompts it writes for its own subagents, sometimes appending a hazard it discovered. That has worked, but it is fragile: improvements live only in whatever prompt happened to be written last, and die when that session ends. Two hazards added on 2026-08-24 (§3.4, §3.5) existed nowhere but in one session's subagent dispatches until this file was written.

Suggested dispatch wording:

> Follow `docs/engineering/agent-verification-discipline.md` in full. It is not optional and not a summary of something else.

If you discover a new hazard while executing, **add it to §3 of this file** rather than to your own prompt. That is how it survives.

---

## 1. Numbers

### 1.1 Derive at the moment of use

Never carry a number from a task file, from your dispatch prompt, or from earlier in your own session. Measure it again, now.

Every count error in the 2026-08-21→23 audit batch came from a *stored* number. Not one came from a fresh measurement.

### 1.2 Ship the command next to the number

A number with its command is re-runnable by the next reviewer. A number in prose is unfalsifiable.

This is the single highest-leverage rule here, and the cheapest. It is why a fabricated "371 methods" was caught (the real figure was 237) — an executing agent could re-run the command and see the regex ended at `(`. It is why a `0 issues` lint result was caught on 2026-08-24 as a malformed invocation rather than believed.

Generated prose will produce a plausible number without effort. The command is the part that cannot be plausibly fabricated.

```
misspell: 439 | baseline 429 -> delta: +10

$ golangci-lint run --config <cfg> --enable-only=misspell \
    --issues-exit-code=0 --output.json.path=mis.json $(cat dirs.txt)
```

### 1.3 A zero is a signal, not a result

**A count of zero from a command that should match something is the cheapest available evidence that your command is wrong.** Check the command before reporting the zero.

Real, 2026-08-24: `golangci-lint` passed import paths where directory paths were expected, emitted `typechecking error: … directory not found`, then `0 issues.` and exit 0. Reported as-is it would have become "misspell is clean." It was a scan of nothing.

The same rule applies to a zero you *want* — verify the mechanism still fires. `gosec G104` genuinely dropped 103→0, confirmed by checking the rule still reports on a purpose-built sample.

### 1.4 Iteration counts, not verdicts

"It passed" is not a result. "0 failures in 20,000 iterations, `go test -count=20000 …`" is. A single green run never clears an intermittent defect.

---

## 2. Documents and code

### 2.1 Code wins

Task files, design docs, comments and trackers are best-effort snapshots. When one disagrees with current source, **the source is right and the document gets corrected** — not worked around.

### 2.2 Re-derive every cited line number before editing

Citations drift. Both task files in the 2026-08-24 batch had drifted line numbers despite carrying correct `verified-at` stamps — the stamps did their job by making the drift expected.

When you correct a citation, say so in your report. That is a finding, not housekeeping.

### 2.3 Stamp what you cite

Any number or code citation you write into a durable artifact carries either its command or `verified-at: <commit>`.

---

## 3. Environment hazards

Universal first, then repo-specific. **Append here when you find a new one.**

### 3.1 `grep -h` / `-o` voids a downstream path filter

`-h` strips filenames *before* your pipe, so a later `grep -v _test` filters nothing and the count silently includes test files. Use `--include`/`--exclude` instead.

### 3.2 Recursive commands rooted at `.` descend into nested worktrees

Agent worktrees under `.claude/worktrees/` are full repo copies. A `gofmt -l .` or `grep -r .` walks all of them. Root at explicit paths.

### 3.3 A regex ending at `(` counts every match, not your subset

Anchor the whole pattern you mean, or you will count supersets and report them as subsets.

### 3.4 `cp` is aliased to `cp -i` — it silently refuses

*Found 2026-08-24.* `cp -f src dst` prints `overwrite dst? (y/n [n]) not overwritten` and **exits without copying**. A restore that silently failed produced one wrong verification result this session before being caught.

Use `command cp -f`, and **always verify a restore** with `shasum` on both sides or `git diff --stat` against the expected diffstat. Never assume a restore took.

### 3.5 Know which package count you mean (Nanite-specific)

*Found 2026-08-24.* `go list ./...` returns **110** packages; CI's git-tracked filter yields **109**. The extra is `ui/node_modules/flatted/golang/pkg/flatted`, a stray Go package vendored inside npm. Both numbers are correct answers to different questions. State which you mean.

### 3.6 `timeout` is not a macOS builtin

Use `gtimeout` (coreutils) or rely on the tool's own timeout flag.

### 3.7 Go's default test timeout reports as a failure (Nanite-specific)

Go's default is **10 minutes per test binary**. Exceeding it prints `FAIL … 600.7s`, which reads exactly like a test failure. Pass an explicit `-timeout` on any high-count run. This misread has already cost time once.

### 3.8 `mkdir` is aliased to `mkdir -pv` — it writes to stdout

*Found 2026-08-25.* `mkdir -p some/dir` prints `some/dir`. In an interactive transcript that line appears immediately above whatever you ran next, and reads as that command's output — it cost one wrong reading of a `gofmt` result before being caught. Inside `$(...)` it silently contaminates the captured value.

Check with `type mkdir` before trusting interleaved output; use `command mkdir -p` in anything whose stdout you read. **Check the shell you are actually in rather than assuming §3.4's list is complete or current** — in the same shell where this was found, `type cp` reported `/bin/cp` with no alias.

### 3.9 `gofmt -l` prints nothing when it cannot run at all

`gofmt -l` and `goimports -l` write their file list to stdout and their errors to stderr, and a binary that is missing (127), a file they cannot parse (2), or a path they cannot read all produce **empty stdout**. Empty stdout is what "everything is formatted" also looks like. Any check built on `gofmt -l` must inspect the exit status; discarding stderr with `2>/dev/null` and ignoring the status makes a tool failure indistinguishable from a clean tree. This shipped twice in this repo — in `scripts/check.sh`'s format stage and in `lefthook.yml`'s `go-format` hook.

### 3.10 `ls-remote refs/tags/<t>` returns the tag object, not the commit

*Found 2026-08-25.* For an **annotated** tag, `git ls-remote origin refs/tags/v0.3.0` returns the
tag object's SHA, which never equals the commit. Comparing it against a pinned commit reports a
spurious mismatch. Peel it: `refs/tags/v0.3.0^{}`.

`git rev-list -n1 <tag>` and `git describe` peel on their own, so a check built on those is already
correct — the hazard is specific to `ls-remote` and to `cat-file`-style lookups. All four
`hollis-labs/libs` siblings use annotated tags, so this bites every pin-versus-tag comparison in
this repo.

---

## 4. Verifying that your verification verifies

The defect class named in `failure-modes.md` §6 — a check that reports success without having checked. Four instances surfaced on 2026-08-24.

### 4.1 Prove the check can fail

**For any fix with a regression test: revert the fix, watch the test fail, restore, verify the restore byte-for-byte.** Then report that you did it and what the failure looked like.

An assertion never observed failing is an assertion of unknown strength.

### 4.2 Ask what input would make this pass while proving nothing

An empty collection, a zero count, an unreachable target, a check that never ran. If that input is reachable, the check needs a **positive control** — prove the mechanism *can* observe the thing before asserting its absence.

### 4.3 Assert presence before properties

```go
if len(workers) != 1 { t.Fatalf("got %d workers, want 1", len(workers)) }
if workers[0].Status != want { … }
```

A bare `for … range` over an empty slice asserts nothing and passes. That is a real defect found in this repo, not a hypothetical.

---

## 5. Amplification has directions

Running a test *harder* is not one axis. Picking the wrong direction produces a false green.

| Suspected | Use | Because |
|---|---|---|
| data race | `-race`, low count | usually fires on the first round |
| timing / ordering | high `-count`, **drop `-race`** | `-race` inflates the gaps these bugs live in |
| wide-window race | `GOMAXPROCS=1` + load | needs contention, not repetition |

Measured 2026-08-24: a task prescribed `-race -count=100` as its acceptance gate for an ordering bug. `-race` made that flake **~18× rarer** (inter-event gap p50 106 µs → 2,187 µs); expected failures under the prescribed command were **0.005** with the bug fully present. It would have gone green and been called fixed.

**A deterministic reproduction beats every amplification strategy.** Prefer making the test not need luck.

Full guidance: `testing-workflow.md`.

---

## 6. Scope and escalation

- **Do the dispatched task. Nothing else.** If you find other work — and you will — **write it down and report it; do not do it.**
- **Escalate rather than deciding alone** when a fix requires changing production behavior rather than test/sync code, when it would touch a file outside your stated fence, or when a question your dispatch flagged has no clear answer in the code.
- **Do not expand scope to route around a blocker.** Stop and ask.

An honest negative result is a real deliverable. *"Not reproduced in N iterations; here is the hazard I found by reading, here is what I ruled out"* is worth more than a manufactured fix.

---

## 7. Reporting

State, every time:

1. Starting commit and tree state.
2. Every file changed and why.
3. Line numbers **you** derived, with the command.
4. Every command run and its result — commands beside numbers.
5. Iteration counts.
6. Evidence the regression test fails without the fix.
7. **Scope-parking list** — what you found and deliberately did not fix.
8. Corrections you made mid-flight to your own earlier numbers.

Item 8 matters more than it looks. Agents that report their own corrections are measurably more trustworthy than agents that present a clean narrative, because the clean narrative is the cheaper thing to generate.

---

## Provenance

These rules were not designed. They accumulated from real failures in the `TASKS/audit-remediation/` batch (Waves 0–4, 2026-08-21→23), where a verification pass ran at every handoff and **every pass produced a finding** — almost never a bad judgment call, almost always a wrong number or a stale citation.

They then propagated by copy-paste: into a handoff prompt written by one session, inherited by the next, copied into that session's own subagent dispatches, extended with newly found hazards, and passed on again. Nobody instructed this. It was observed on 2026-08-24 when an orchestrator was asked how it had arrived at "ship the command next to the number" and found the instruction verbatim at line 112 of its own boot prompt, authored by a session it never spoke to.

That propagation is a good sign and an unreliable mechanism. This file exists to make it durable.

**Portability:** §§1, 2, 4, 5, 6, 7 and hazards 3.1–3.3, 3.6 are project-agnostic and should be carried to other repos as-is. Hazards 3.4, 3.5 and 3.7 are environment- or repo-specific — keep the section, replace the contents.
