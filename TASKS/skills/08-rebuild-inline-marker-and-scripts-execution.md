# Rebuild inline deterministic execution (code-fence-aware) + `scripts/` execution

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** not-started
**Depends on:** `06`
**Touches:** new file `internal/skill/exec.go` (the rebuilt replacement for the old,
fully-deleted `internal/skill/context.go` marker logic — task `01` deleted the old file's
execution logic in full; this task is a fresh build, not a resurrection), integrates with task
`09`'s policy/sandbox gate (this task's execution calls route through it — coordinate the exact
call signature with task `09` if sequencing allows, otherwise define a placeholder interface
here that task `09` implements).

## Context

`docs/engineering/architecture/20-skills.md`'s "Materialization pipeline" section: *"Inline
deterministic execution. The retired `` !`cmd` `` marker is not revived as-is. It's rebuilt as
one materializer provider among the others above — structurally parsed with real awareness of
markdown code-fence boundaries (closing the accidental-execution gap the old regex had), and
routed through the same policy/sandbox gate every script execution goes through, rather than a
bare, ungated shell-out. Scripts. `scripts/` files execute through the same policy/sandbox gate
as inline markers — see below. Distinct from inline execution only in that a script is an
explicit file the package ships, not text computed from the `SKILL.md` body."*

**The old marker's real flaw, confirmed this planning session (task `01` deleted this code —
recap for context, not something to look up in a now-deleted file)**: the old regex
(`` !`([^`]+)` `` — a bare backtick-delimited pattern) had zero awareness of markdown code-fence
context. A documentation example inside a fenced code block containing the literal text
`` !`some command` `` would execute identically to a real marker — a real accidental-execution
bug, not a hypothetical one. This task's rebuild must parse the markdown body structurally
(track fence state — inside vs. outside a ` ``` `/`~~~` block — before matching the marker
pattern) so an example inside a code fence is never executed.

**Scope boundary with task `09`**: this task defines *what* gets executed (the parsed marker
command, or a `scripts/` file's invocation) and *when* (as part of materialization). Task `09`
defines *how* it's executed safely (the sandbox profile, the capability-grant check). This task
should call into task `09`'s gate as a black box, not implement its own ad-hoc sandboxing —
if task `09` hasn't landed yet when this task starts, define the interface/call signature this
task needs (e.g. `func ExecuteGated(ctx, skillID string, cmd []string, profile ExecProfile)
(stdout string, err error)`) and let task `09` implement the real body, rather than building a
temporary ungated version "to be replaced later" — that's exactly the kind of half-finished
implementation this project's standing discipline avoids.

## What to do

1. Implement a structural markdown parser (or a minimal fence-tracking scanner — doesn't need to
   be a full markdown AST, just enough to track fence open/close state line-by-line) that
   identifies `` !`cmd` `` markers only when they occur outside any fenced code block.
2. Implement the inline-marker execution path: for each real (non-fenced) marker found, extract
   the command text, call through to task `09`'s gated execution primitive (see Context above),
   and substitute the command's output back into the materialized body at the marker's position
   — matching the old `ResolveDynamicContext`'s substitution behavior (`ReplaceAllStringFunc`-style),
   but gated.
3. Implement `scripts/` file execution: given a package's `scripts/` directory (available via
   task `03`'s vendored-store read path) and whatever invocation convention the real
   Agent-Skills-spec defines for scripts (check the spec — a script might be invoked by name
   with arguments, or run unconditionally as part of materialization; don't assume, verify
   against the actual spec convention), call through to the same task `09` gated-execution
   primitive.
4. A reasonable execution timeout (matching the old marker's 10-second precedent as a starting
   point, but confirm this is still appropriate for `scripts/` files, which may legitimately need
   longer — consider making this configurable per-invocation rather than hardcoding a single
   value for both marker and script execution).
5. Failure handling: a marker or script execution failure should produce a clear, attributable
   error (which skill, which marker/script) — not silently substitute empty output, matching this
   project's general "don't fail open, don't fail silent" discipline.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A test proves the code-fence-awareness fix directly: a `SKILL.md` body containing a real
  `` !`cmd` `` marker outside any fence executes and substitutes correctly; the identical literal
  text `` !`cmd` `` inside a fenced code block does **not** execute — this is the single most
  important regression test in this task, since it's the exact bug class the old marker had.
- A `scripts/` file executes correctly through the gated primitive when materialization calls
  for it.
- All execution — marker and script alike — routes through task `09`'s gate; a test confirms no
  execution path in this file calls `exec.Command` (or equivalent) directly, bypassing the gate.
- An execution timeout is enforced and produces a clear, attributable error on expiry.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
