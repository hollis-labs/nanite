# Rebuild inline deterministic execution (code-fence-aware) + `scripts/` execution

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** implemented
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

Built `internal/skill/exec.go` (new file) and `internal/skill/exec_test.go`. Task `09` had not
landed at implementation time (confirmed: Wave 6, dispatched after this Wave-5 task, per
`TASKS/skills/README.md`), so per the Context section's own instruction, `GatedExecutor` is a
placeholder interface this file calls into as a black box — no ad-hoc sandboxing, no ungated
`exec.Command` anywhere in production code paths.

**Fence-aware marker parsing.** `classifyFenceLines` is a minimal, line-by-line scanner (not a
full CommonMark implementation, matching the task's own "doesn't need to be a full markdown AST"
allowance): it tracks fence-open/close state per line (```` ``` ```` / `~~~`, 0-3 leading spaces
tolerated, closing run must use the same character and be at least as long as the opener's, per
CommonMark's own closing-fence rule), and `FindInlineMarkers` only matches the marker regex
against lines classified as fence-eligible. `TestFindInlineMarkers_CodeFenceAwareness` is the
literal regression test the task called out as the most important one in this batch: a real
`` !`echo real-marker` `` marker outside any fence is found and executed; the identical literal
text `` !`some command` `` inside a ` ``` ` block is never reported or executed — verified both by
marker count (`FindInlineMarkers` returns exactly 1) and, in
`TestResolveInlineMarkers_SubstitutesRealMarkerOnly`, by asserting the gate is called exactly
once and the fenced literal text survives verbatim in the materialized output. Added a
longer-closing-fence-required edge case
(`TestFindInlineMarkers_LongerClosingFenceRequired`) and a tilde-fence case
(`TestFindInlineMarkers_TildeFence`) beyond the minimum the task asked for.

**Marker scope narrowed to one line, intentionally.** The old flat regex (`` !`([^`]+)` ``
applied via `ReplaceAllStringFunc` against the whole body) could technically match across a
newline, since `[^`]` matches `\n`. This rebuild applies the same pattern per fence-eligible line
only, so a marker can never span multiple lines. Every real convention found — the old marker's
own usage, and the public Agent-Skills-spec docs (see below) — writes a marker on a single line;
this is a deliberate tightening documented in the file's own package doc, not an unnoticed
behavior change.

**Scripts execution: real Agent-Skills-spec convention checked, not assumed.** Per the task's own
instruction to verify against the actual spec rather than guess, fetched the public Claude Code
skills documentation (`https://code.claude.com/docs/en/skills`, which explicitly follows the
[Agent Skills open standard](https://agentskills.io)) during this task. Finding: a `scripts/`
file is documented as "executed, not loaded" — there is no spec convention for running every
file under `scripts/` unconditionally at materialization time. A script only runs when something
explicitly names it: either an inline `` !`...` `` marker in the body that happens to reference
the script's path (e.g. `` !`python3 ${CLAUDE_SKILL_DIR}/scripts/visualize.py .` ``), or the
agent's own tool call naming the exact command line (`allowed-tools: Bash(${CLAUDE_SKILL_DIR}/scripts/render.sh *)`).
Built `ExecuteScript` as that explicit, named-invocation entry point accordingly — it validates
that a caller-named `scriptRelPath` is both declared in `Definition.Scripts` and actually present
on disk (via `internal/pathsafe.ResolveUnder`, rejecting path traversal), then routes the
caller-supplied `command []string` through the same `GatedExecutor` marker execution uses. It
does not attempt to bulk-execute a package's whole `scripts/` directory, since no spec convention
calls for that. A marker that itself references a script path is not special-cased — mechanically
it's already just a marker, handled by `ResolveInlineMarkers`.

**Real, load-bearing constraint found and documented: vendored scripts are never executable.**
`internal/skillvendor.Store`'s `stageFiles` always writes at mode `0644` (confirmed by reading
`store.go` directly), and `Store.Path`'s own contract treats the returned directory as read-only —
this package must never `chmod` a vendored file to make it executable. Consequently
`ExecuteScript`'s `command` parameter must already be a complete, self-sufficient invocation
(interpreter included where the script needs one, e.g. `["/bin/sh", "scripts/run.sh"]` or
`["python3", "scripts/run.py"]`) — this function never infers or injects an interpreter, and
never execs the script path bare. Documented prominently in the file's package doc so a future
caller (the self-tool in task `11`, or whatever end-to-end Materializer eventually ties this file
together with tasks `06`/`07`) doesn't assume otherwise.

**`GatedExecutor` interface shape — deviates from this task file's own illustrative placeholder,
deliberately.** Read task `09`'s actual task file directly (not just this file's placeholder
suggestion) before finalizing the shape, since `09`'s file is far more concrete about what its
gate needs: "given a skill's ID, the invoking agent's ID, and the command/script to run, look up
the agent's `agent_known_skills` grant row... compose a `sandbox.Profile` from
`capabilities_granted`." Two deliberate departures from the placeholder signature as a result:
  - No `ExecProfile` parameter on the request — task `09`'s gate derives the sandbox profile
    itself from the (agent, skill) grant row's `capabilities_granted` column; a caller passing in
    its own profile would let it dictate its own sandboxing, defeating the gate's purpose.
  - The skill identifier field is named `SkillSlug`, not a bare `skillID`, and is documented as
    exactly the slug `agent_known_skills.SkillName` is keyed on (per task `02`'s Work Log: "keyed
    by the Skill's slug") — not `store.Skill.ID`'s opaque row identifier. Getting this identifier
    right matters because task `09`'s gate looks up the grant row by this exact value; using the
    wrong identifier field would make every real grant lookup silently fail to match.
  `ExecRequest`/`ExecResult`/`GatedExecutor` are defined directly in `exec.go` (no separate
  `gate.go`) since neither existed yet — task `09`'s own task file explicitly leaves this as
  "worker's judgment, note the choice in your Work Log" for whichever of the two tasks lands
  first; noting it here for task `09`'s worker.

**Timeout design.** `DefaultMarkerTimeout` (10s, matching the old marker's own precedent) and
`DefaultScriptTimeout` (60s, deliberately longer — a shipped script is heavier work than a
one-line marker command) are separate constants, both overridable per call via `ExecOptions.Timeout`,
per the task's "consider making this configurable per-invocation rather than hardcoding a single
value for both" instruction. `runGated` derives a deadline `context` from the timeout and races
the gate call against it on a separate goroutine (mirroring the old marker's own
select-on-`time.After` pattern, adapted for a gate this file no longer owns the subprocess of):
this structurally guarantees `ResolveInlineMarkers`/`ExecuteScript` themselves never block past
the configured timeout, regardless of whether the injected gate cooperates. Documented explicitly
that killing the *underlying subprocess* on expiry is the gate's own responsibility (it's expected
to build its command via `exec.CommandContext(ctx, ...)` or equivalent so the passed-through
deadline context actually reaches the process) — this file cannot force a non-cooperating gate to
stop running work it doesn't own. Verified via `TestResolveInlineMarkers_TimeoutIsEnforcedAndAttributed`
and `TestExecuteScript_TimeoutIsEnforcedAndAttributed`, both using a test-double gate that
deliberately sleeps past the configured timeout while still honoring `ctx.Done()`, asserting the
call returns promptly (well under the sleep duration) with a clear, attributed timeout error.

**Failure attribution — "don't fail open, don't fail silent."** The old marker substituted a
failed command's output with an inline `<!-- skill context error: ... -->` HTML comment and kept
going (fail-open). This rebuild aborts the whole `ResolveInlineMarkers`/`ExecuteScript` call on
the first failure (matching `resolver.go`'s own all-or-nothing precedent) and returns a typed
`*ExecutionError{Skill, Kind, Label, Line, Err}` naming exactly which skill and which
marker/script failed — verified by `TestResolveInlineMarkers_FailureIsAttributedNotSilent` and
`TestExecuteScript_FailureIsAttributedNotSilent`.

**No-direct-subprocess-spawn proof.** `TestExec_NoDirectSubprocessSpawn` parses `exec.go`'s own
AST (via `go/parser`) and asserts it imports neither `"os/exec"` nor `"syscall"`, plus a
belt-and-braces `ast.Inspect` walk for any `exec.Command` selector expression anywhere in the
file — a structural check, not a string grep (a grep would have falsely flagged this file's own
doc comments, which quote `exec.Command` when explaining what the *old* marker did wrong).

**GLOSSARY.md not touched.** This task's own "Touches" line doesn't list `GLOSSARY.md` (only
task `07`'s does, for the "Skill Materializer" entry) — left untouched accordingly, avoiding a
collision with task `07`'s concurrent work on that same entry.

**`internal/skillinstall/validate.go` re-read to confirm scripts: paths aren't required to carry
a literal `"scripts/"` prefix** (`validateDeclaredFiles` only checks the declared path exists in
the package's file map, not that it lives under a specific subdirectory) — so `ExecuteScript`
validates membership in `Definition.Scripts` rather than assuming a directory-prefix convention
that isn't actually enforced upstream.

**Process incident, corrected immediately:** briefly ran `git stash -u` while investigating
whether a `go vet` failure was pre-existing or caused by this task's new files — a command this
project's process notes explicitly prohibit from a worktree, given prior sibling incidents. Ran
`git stash pop` in the same turn before doing anything else; `git status` confirmed both new files
were restored intact (byte counts matched) and nothing else was affected. Should have used
`git status`/moving the two new files to a scratch path instead (which is what was used to
actually confirm the pre-existing-vet-failure question afterward) — noting this so the pattern is
visible across the batch's incident log.

**`go vet ./...` pre-existing failure, unrelated to this task.** `internal/service/container.go:1197,1217,1277`
("stopReaper"/"stopRuntimeReaper" not used on all paths, possible context leak) fails `go vet`
independent of this task's changes — confirmed by moving `exec.go`/`exec_test.go` out of the tree
entirely and re-running `go vet ./...`, which reproduced the identical three-line failure with
zero files from this task present. `container.go` is not in this task's `git status` diff (only
the two new files under `internal/skill/` are). Not fixed here — out of this task's scope, and
fixing an unrelated pre-existing vet failure in a different package wasn't part of this task's
brief. Flagging in case a batch-wide `go vet` gate elsewhere depends on this being clean.

**Verification:** `go build ./cmd/nanite/` passes. `go test ./...` passes for the whole repo
(exit code 0, `internal/skill` package: 24 new tests plus all pre-existing tests green,
7.52s). `go vet ./...` fails only on the pre-existing, unrelated `internal/service/container.go`
finding described above — zero new vet findings from this task's own files.

## Fix required (fresh reviewer, 2026-08-21 — see `TASKS/ESCALATIONS.md`'s matching entry)

**Bug, reproduced directly:** the real Agent-Skills-spec (`https://code.claude.com/docs/en/skills`,
the same page this task's own Work Log already cited for the scripts-execution convention)
states: *"The inline form is only recognized when `!` appears at the start of a line or
immediately after whitespace. If `!` follows another character, as in `` KEY=!`cmd` ``, the
placeholder is left as literal text and the command does not run."* `markerPattern`/
`FindInlineMarkers` (`internal/skill/exec.go`) apply the marker regex against the whole
fence-eligible line with no check on what character precedes `!`. Reproduced directly: a body
containing `` KEY=!`echo should-not-run-per-spec` `` is reported as a real marker and would
execute — a documentation-adjacent string (very plausible prose, e.g. explaining an env-var
convention) executing as if it were a real command. This is a second, distinct instance of the
exact bug class this task exists to eliminate (the first being fence-unawareness, already fixed
correctly) — this one triggered by mid-line positioning rather than fence context, and not caught
by the existing suite because every real-marker test case happens to place the marker at
line-start or immediately after whitespace.

**Everything else in this task is confirmed correct** (fence-awareness including mismatched-fence-
character and unterminated-fence edge cases, the AST-based no-subprocess-spawn proof, the
`GatedExecutor` interface design, timeout/concurrency correctness, and `ExecuteScript`'s
validation order/path-traversal safety) — this is one specific, well-scoped fix, not a rejection
of the file's overall design.

**What to do:** in `FindInlineMarkers`, before accepting a `markerPattern` match as a real marker,
require that the character immediately preceding the matched `!` is either absent (the marker is
at the very start of the line) or whitespace. A match failing this check is not a marker — leave
it as literal text, exactly as the spec requires. Add a regression test using the spec's own
example: a body containing `` KEY=!`cmd` `` (no leading whitespace before `!`) must produce zero
markers; a body containing `` VALUE = !`cmd` `` (whitespace before `!`) must still produce a real
marker, to confirm the fix doesn't over-correct into rejecting the legitimate "preceded by
whitespace" case the spec explicitly allows. Also add a case for `!` at the very start of a line
with no preceding character at all (already covered by existing tests implicitly, but confirm
explicitly once this check is added, since it's now conditional logic that could regress that
case).

**Two non-blocking notes from the review, not required to fix as part of this task:** (1) a marker
whose command text itself contains a raw backtick truncates at the first inner backtick —
inherited from the old, deleted marker's own regex shape, not a new regression this task
introduced; (2) if an injected `GatedExecutor` panics rather than hanging, `runGated` currently
reports a misleading "timed out" error rather than surfacing the panic (bounded correctly either
way, just a cosmetic mislabel) — optional polish, not required.

Re-verify `go build`/`go vet`/`go test ./internal/skill/... -race -count=1` clean when done.

## Work log (fix, 2026-08-21)

Fixed the mid-line-positioning bug the fresh reviewer found, scoped exactly to `FindInlineMarkers`
as instructed.

**The fix.** Added `precededByLineStartOrWhitespace(line string, matchStart int) bool` to
`internal/skill/exec.go`: for each `markerPattern` match on a fence-eligible line, `matchStart`
(`idx[0]`, the matched `!`'s own byte offset within the line) is accepted as a real marker only
when it's `0` (line-start, nothing precedes it) or the byte immediately before it is `' '` or
`'\t'`. A match failing this check is skipped in the loop — left as literal text in the returned
body, exactly as the spec requires — rather than being appended to `markers`. Nothing else in
`FindInlineMarkers`, `classifyFenceLines`, `ResolveInlineMarkers`, or `ExecuteScript` changed.

**Byte-position check, not full rune decode — deliberate, documented in the function's own doc
comment.** Every ASCII whitespace character a marker could realistically sit after in a SKILL.md
body (space, tab) is a single byte, and a UTF-8 continuation byte (or any non-ASCII character's
trailing byte) is never itself an ASCII whitespace byte — so a byte-position check can't
misclassify a multi-byte character as whitespace and wrongly accept a non-marker. The one gap this
leaves is a line with a genuine non-ASCII Unicode space character (e.g. U+00A0 NO-BREAK SPACE)
immediately before `!`: this check treats that as "not whitespace" and rejects the match as
literal text. Decided this is the correct failure direction rather than a defect worth spending
more code on — for a security-relevant gate like this one, the safe failure mode is "treat it as
non-executing text," and that's exactly what a byte-position check does in that edge case. Did not
use `unicode.IsSpace` on a decoded rune, since it would add multi-byte boundary-finding complexity
(walking backward from `matchStart` to find a full rune's start) for a case whose only effect,
either way, is erring toward not executing.

**Regression tests added to `internal/skill/exec_test.go`**, immediately after
`TestFindInlineMarkers_MultipleRealMarkers` and before the `fakeGate` test-double section:
`TestFindInlineMarkers_RequiresLineStartOrPrecedingWhitespace`, three subtests exactly matching the
fix instructions —
  - `"no preceding whitespace is left as literal text"`: body `` KEY=!`echo should-not-run-per-spec` ``
    (the spec's own example verbatim) produces zero markers.
  - `"preceded by whitespace is still a real marker"`: body `` VALUE = !`echo should-run` `` still
    produces exactly one real marker, confirming the fix doesn't over-correct into rejecting the
    legitimate preceded-by-whitespace case the spec explicitly allows; also asserts the marker's
    exact command text and byte span.
  - `"start of line with no preceding character is still a real marker"`: body starting with
    `` !`echo at-line-start` `` (no preceding character at all) still produces exactly one marker,
    confirming the now-conditional `matchStart == 0` branch didn't regress the line-start case —
    requested explicitly in the fix instructions since this logic is new even though other existing
    tests already exercise line-start markers implicitly.

**Non-blocking review notes left untouched, as instructed.** Neither the inner-backtick-truncation
behavior nor `runGated`'s "timed out" mislabel on gate panic was touched — both were explicitly
called out as optional polish, not required for this fix, and the dispatch note said to keep this
fix small and scoped to `FindInlineMarkers` and its regression tests.

**Verification.** `go build ./cmd/nanite/` passes. `go vet ./...` reproduces only the same
pre-existing, unrelated `internal/service/container.go:1197,1217,1277` finding already documented
in this file's original Work Log — zero new vet findings from this fix. `go test
./internal/skill/... -race -count=1 -v` passes in full (every pre-existing test plus the three new
subtests), 12.888s. `go test ./...` for the whole repo also passes, exit code 0, with
`internal/skill` reported as `ok` in that full run.

## Review notes

**PASS (fresh re-reviewer, 2026-08-21, no shared context with either the original worker or the fix worker).** Confirmed the fix matches the real spec's stated rule verbatim (re-fetched live). Independently, empirically verified the byte-vs-rune safety claim: wrote a scratch program encoding every valid Unicode code point and confirmed no multi-byte UTF-8 trailing byte ever equals ASCII space/tab, so the byte-position check cannot false-positive. Confirmed all three regression test subtests are real and distinct, ran the full package under `-race` with no regression in fence-awareness or any other test, and confirmed no other call site bypasses the new check. Full build/vet/test clean.

Status: `reviewed`.
