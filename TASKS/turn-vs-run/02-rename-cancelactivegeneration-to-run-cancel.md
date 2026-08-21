# Rename CancelActiveGeneration's conceptual name: Turn.Cancel → Run.Cancel

**Phase:** 1 — Define the primitive; fix the name (`TASKS/turn-vs-run`)
**Status:** not-started
**Depends on:** none. File-disjoint from task `01` — safe to run in parallel.
**Touches:** `internal/service/chat.go` (two doc comments — the `ChatService` interface
method at ~line 68-73, and the implementation at ~line 528-536), `internal/runtime/agent/
acp_session.go` (one doc comment, lines 365-373), `docs/engineering/GLOSSARY.md` (the
**Turn.Cancel** vs. **Run.Cancel** vs. **Session.Stop**... entry, ~line 35), `docs/
engineering/architecture/17-acp.md` (one paragraph, ~line 72), `docs/engineering/
architecture/00-overview.md` (one bullet, ~line 53). No application behavior changes — this
is a documentation/comment-only rename. The underlying Go identifier `CancelActiveGeneration`
itself is **not** renamed (see Context).

## Context

Implements `docs/engineering/architecture/22-turn-vs-run.md`'s "Naming" section: *"Rename
`CancelActiveGeneration`'s conceptual name from `Turn.Cancel` to `Run.Cancel` — same scope,
correct name, `GLOSSARY.md` updated in this pass."*

**"Conceptual name," not the Go symbol.** Confirmed this session: there is no Go identifier
anywhere in the codebase literally named `TurnCancel` — `CancelActiveGeneration`
(`internal/service/chat.go:73` interface, `:537` implementation) is already a descriptive Go
name. What's being renamed is the *concept this function implements*, as described in prose:
doc 22, the Glossary, and code comments have called that concept "Turn.Cancel," and that
label is wrong under the now-settled vocabulary — `CancelActiveGeneration` cancels the entire
in-flight Run (the whole `generateResponse` tool-settling loop for a session), not a single
Turn (one model call). Confirmed directly: `internal/service/chat.go:537-546`'s
`CancelActiveGeneration` looks up `s.activeGen[sessionID]` (one entry per *session*, not per
iteration) and calls its `cancel()` — the same `context.CancelFunc` `generateResponse`'s
whole call is running under (`chat.go:570`, `genCtx, cancel := context.WithCancel(...)`,
threaded through `runGeneration` into `generateResponse`'s `ctx` parameter). Cancelling it
aborts the *entire* Run, mid-Turn or not — exactly `Run.Cancel`'s definition.

**Every real citation this task must correct, confirmed by direct grep this session** (do not
trust these line numbers without re-checking — this file was read once, at planning time):

1. `internal/service/chat.go:68-73` (interface) and `:528-536` (implementation) — doc
   comments describing `CancelActiveGeneration`. Currently correct on substance, silent on
   naming; add the `Run.Cancel` label explicitly so a future reader doesn't have to
   re-derive it from `22-turn-vs-run.md`.
2. `internal/runtime/agent/acp_session.go:365-373` — `Stop`'s doc comment:
   > *"Stop implements the Session.Stop contract for the ACP backend. Cancel first (ACP's
   > `session/cancel` — despite the wire method's name, spec'd as turn-scoped; this is
   > `Turn.Cancel`'s analog, not `Session.Stop`'s, per `docs/engineering/GLOSSARY.md`'s
   > 'Turn.Cancel vs. Session.Stop...' entry)..."*
   This is now **substantively wrong**, not just stale-named: the Glossary entry it cites
   (title now "**Turn.Cancel** vs. **Run.Cancel** vs. **Session.Stop** vs. ...") itself states
   *"ACP's `session/cancel` method, despite its name, is spec'd as Run-scoped (cancels the
   in-flight prompt; the session survives) — it must map onto `Run.Cancel`, never onto
   `Session.Stop`."* The comment currently says the opposite of what the Glossary it cites
   now says. Fix the comment to say `Run.Cancel`'s analog and correct the cited entry title.
3. `docs/engineering/architecture/17-acp.md:72` — same substance, in an architecture doc:
   *"This maps onto our own `Turn.Cancel` concept... not onto `Session.Stop`... it should
   call our `Turn.Cancel` path — never our `Session.Stop` path."* Same fix: `Turn.Cancel` →
   `Run.Cancel` in both places in this paragraph. This is a real, load-bearing correctness
   statement (which internal method an ACP client abstraction must call), not cosmetic.
4. `docs/engineering/GLOSSARY.md`, the **Turn.Cancel** vs. **Run.Cancel** vs. **Session.Stop**
   vs. `interrupt.requested`/`interrupt.acknowledged` vs. ACP's `session/cancel` entry
   (~line 35) currently reads, in part: *"**Run.Cancel** (currently named
   `CancelActiveGeneration`, `internal/service/chat.go`, still under its old name
   `Turn.Cancel` in code/comments — the rename to `Run.Cancel` is recommended but not yet
   executed, see `architecture/22-turn-vs-run.md`)..."* Update this parenthetical once this
   task's comment fixes land — it should state the rename is done, not pending, and can drop
   the forward-reference to doc 22 for the *rename* specifically (doc 22 stays the right
   citation for the broader Turn/Run split).
5. `docs/engineering/architecture/00-overview.md:53` — currently listed under "What's
   genuinely still open": *"`CancelActiveGeneration`→`Run.Cancel` rename... recommended, not
   yet executed."* Update or remove this bullet once this task lands — it's no longer open.

**Not in scope for this task** (left alone deliberately):
- `docs/engineering/architecture/19-api-cli-runtime-parity.md` — its own text already
  narrates the historical sequence correctly (line 38: *"settles the naming (`Run.Cancel`,
  not `Turn.Cancel` — see the naming recommendation above, updated accordingly)"*) even though
  an earlier paragraph in the same doc (line 24) still proposes `Turn.Cancel` as the
  then-current recommendation. That's accurate as a *historical* record of how the naming
  evolved across two review passes — not a live, currently-wrong claim the way items 2-3
  above are. Leave it as-is.
- `TASKS/agent-host-acp/*.md` — historical task files from a different, already-reviewed
  batch, referencing "the existing `Turn.Cancel` vs. `Session.Stop` entry" as it stood *at
  that batch's own planning time*. These are immutable historical record (matches this
  project's standing log-integrity discipline for task files) — not live documentation, and
  not this task's job to edit.
- `internal/api/harness_v1_test.go:165`'s `TestHarnessV1TurnCancelAndEvents` test function
  name. Cosmetic only (it's a test name, not a concept-naming claim) — rename it if you're
  already touching the file for an unrelated reason, but it's not required for this task's
  Done means, and this task's Touches list doesn't otherwise include this file.

## What to do

1. `internal/service/chat.go`: update the `ChatService` interface's `CancelActiveGeneration`
   doc comment (~line 68-73) and the implementation's doc comment (~line 528-536) to name the
   concept explicitly as `Run.Cancel` — e.g. (illustrative, adapt to fit each comment's
   existing content, don't just prepend boilerplate): *"CancelActiveGeneration implements
   `Run.Cancel`: it cancels the entire in-flight Run (the whole tool-settling generateResponse
   loop) for a session, not a single Turn — see `docs/engineering/architecture/
   22-turn-vs-run.md` and `GLOSSARY.md`'s Turn.Cancel/Run.Cancel entry."* Keep every existing
   fact in both comments (the CW-tagged history, the "registry slot NOT cleared here" note,
   etc.) — this is an addition, not a rewrite.
2. `internal/runtime/agent/acp_session.go:365-373`: fix `Stop`'s doc comment per Context item
   2 above — `Turn.Cancel`'s analog → `Run.Cancel`'s analog; correct the cited Glossary entry
   title to match its real current title.
3. `docs/engineering/architecture/17-acp.md:72`: fix per Context item 3 above — both
   occurrences of `Turn.Cancel` in that paragraph become `Run.Cancel`.
4. `docs/engineering/GLOSSARY.md`: update the **Turn.Cancel** vs. **Run.Cancel** vs.
   **Session.Stop**... entry's `Run.Cancel` parenthetical per Context item 4 — state the
   rename as done, not pending. Do not otherwise restructure the entry; it's substantively
   correct today, just forward-looking about this one rename.
5. `docs/engineering/architecture/00-overview.md:53`: update or remove per Context item 5 —
   this bullet is no longer open once this task lands.
6. Grep once more, right before marking this task done, for any other live (non-historical,
   non-test-name) occurrence of the literal string `Turn.Cancel` outside
   `docs/engineering/architecture/22-turn-vs-run.md` (which this batch does not touch) and
   `docs/engineering/architecture/19-api-cli-runtime-parity.md` (deliberately left alone, see
   Context) — the codebase moves fast; a citation found stale at planning time (2026-08-21)
   may have already drifted further, or a new stale reference may have appeared. Fix anything
   real you find; note it in the Work Log if it wasn't in this task file's original list.

## Done means

- All five citations in "What to do" are corrected exactly as described.
- No behavioral change anywhere — `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`
  pass with zero diff outside comments/docs.
- `git diff --stat` shows only the files in this task's Touches list (plus anything real
  found by item 6, noted in the Work Log).
- A final grep for `Turn.Cancel` outside the two deliberately-excluded files (doc 22, doc 19)
  returns nothing live and uncorrected.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
