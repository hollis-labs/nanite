# halt_session's reason rides the Recovery Pack's next-cold-boot replay

**Phase:** 3 — The halt_session special case (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/recovery/pack/pack.go` (`RecoveryPackInput`, `BuildRecoveryPack`),
`internal/service/recovery_pack_glue.go` (`buildSessionRecoveryPrefix`).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md`: "**Reflex `halt_session` is a
genuine special case** — there is no same-turn tool-result to attach a suggestion to, because
the turn never reaches the LLM. Target: the halt's reason/guidance rides the Recovery Pack's
next-cold-boot context replay... so a resumed session isn't blind to why it was halted, rather
than trying to force a same-turn message that structurally can't exist." Unlike tasks
`02`-`05`, this mechanism has **nothing to do with `internal/recover`'s `Kind` taxonomy** —
`halt_session` aborts a turn before the model is ever called
(`internal/service/chat_generate.go:538-558`), so there is no tool result, no `Kind`, no
envelope to build. This task's own "Kind"-shaped concept is a completely separate mechanism:
the Recovery Pack's own `Reason`-rendering, extended.

**The natural extension point already exists — investigated per this batch's own charge, not
assumed.** `internal/recovery/pack/pack.go`'s `RecoveryPackInput{Session, Agent, Reason,
History, PackPath}` and `BuildRecoveryPack` already render a `Reason` into the pack: `if
in.Reason != "" { b.WriteString(fmt.Sprintf("- Recovery reason: %s\n", in.Reason)) }`
(`pack.go:126-128`). But this field is **already spoken for** — `internal/service/recovery_pack_glue.go:74`
hardcodes `const reason = "host service restart (cold boot with prior history)"` and passes
it as `RecoveryPackInput.Reason` (line 82) — this is "why did we cold-boot," not "why was this
session halted." Conflating the two under one field would be wrong (this repo's own
`GLOSSARY.md` discipline: "an unqualified reuse that reads as one thing but means another" is
exactly the failure mode it exists to prevent) — this task adds a **second, distinct** field
rather than overloading the existing one.

**The halt reason is already there for the taking, no extra query needed.**
`store.Session.HaltedReason *string` (`internal/store/sessions.go:35`) is populated by the
same `SELECT` that loads every other `Session` field (`sessions.go:117/226`, columns
`sess.halted_at, sess.halted_reason`) — and `buildSessionRecoveryPrefix` already receives
`session *store.Session` as a parameter (`recovery_pack_glue.go:57`). No new store method, no
new query.

**Confirmed this planning session: nothing clears `halted_reason` automatically today.**
`store.ClearSessionHalt` (`internal/store/session_halt.go:75-96`) exists but has **zero
callers anywhere in the codebase** outside its own file — its doc comment even says
"Spike-style: the only resume path is operator action (manual SQL UPDATE OR POST
/api/sessions/{id}/resume)," and no such `/resume` handler exists yet either. This means: (a)
whenever a halted session next produces a turn — by whatever mechanism eventually resumes it,
today or in the future — `session.HaltedReason` will still be populated if this task reads it
promptly, since nothing races to clear it first; (b) whether a halted session's *next* turn is
actually a cold boot (vs. still-warm, re-triggering the same halt reflex condition
immediately) is a separate, pre-existing architecture question this task does not resolve —
doc 23 itself scopes this to "the Recovery Pack's next-cold-boot context replay" specifically,
not "guarantee a halted session always gets a graceful resume." This task makes the cold-boot
case correct; it does not build a `/resume` endpoint or address the warm-session-re-halts-
immediately case, neither of which doc 23 asks for.

## What to do

1. **`internal/recovery/pack/pack.go`** — add `HaltReason string` to `RecoveryPackInput`
   (distinct from the existing `Reason string`, per Context — do not conflate). In
   `BuildRecoveryPack`, render it as its own section when non-empty, placed after the
   `## Session` block and before `## Recent turns` (so the agent sees "you were halted, and
   here's why" before it sees the replayed conversation), e.g.:
   ```go
   if in.HaltReason != "" {
       b.WriteString(fmt.Sprintf("\n## Halted\nThis session was halted before this restart: %s\n", in.HaltReason))
       b.WriteString("Consider whether the condition that caused the halt is still true before continuing as if nothing happened.\n")
   }
   ```
   (Exact wording is illustrative — the point is the agent gets both the fact and a nudge to
   reconsider before blindly resuming, not just a bare string. Document your final wording in
   the Work Log if it differs.)
2. **`internal/service/recovery_pack_glue.go`'s `buildSessionRecoveryPrefix`** — read
   `session.HaltedReason` (already available on the `session *store.Session` parameter, no
   new fetch) and pass it through as `RecoveryPackInput.HaltReason` when non-nil/non-empty:
   ```go
   haltReason := ""
   if session != nil && session.HaltedReason != nil {
       haltReason = *session.HaltedReason
   }
   built := pack.BuildRecoveryPack(pack.RecoveryPackInput{
       Session:    session,
       Agent:      agent,
       Reason:     reason,
       HaltReason: haltReason,
       History:    history,
       PackPath:   packPath,
   })
   ```
   Also add `HaltReason` (when non-empty) to `recoveryPackPlantedMeta`/
   `recoveryPackPlantedMetadata` (`recovery_pack_glue.go:102-135`) — the `event_log` postmortem
   row should record that a halt reason was included in the planted pack, matching this
   file's own existing discipline of a real, queryable postmortem rather than a bare marker
   (see that function's own doc comment).
3. Do **not** touch `store.MarkSessionHalted`/`ClearSessionHalt`, the `halt_session` reflex
   action kind, or `chat_generate.go`'s synchronous halt-abort logic (lines 528-558) — this
   task only changes what the *next* cold-boot turn sees, not how or when a session gets
   halted or resumed.
4. Tests: extend `internal/recovery/pack/pack_test.go`'s `TestBuildRecoveryPack` (or add a
   sibling test) asserting a `RecoveryPackInput` with `HaltReason` set renders a `## Halted`
   section containing that text, and that an empty `HaltReason` renders no such section at
   all (matching the existing `if in.Reason != ""`-style conditional-section pattern already
   used for `## Session`'s optional lines). Extend whatever test covers
   `buildSessionRecoveryPrefix` (check for an existing one in `internal/service` before
   assuming none exists) to assert a session with `HaltedReason` set produces a pack
   containing that text, and that the `recovery_pack_planted` event_log metadata reflects it.

## Done means

- `RecoveryPackInput.HaltReason` exists, distinct from `Reason`; `BuildRecoveryPack` renders
  it as its own `## Halted` section only when non-empty.
- `buildSessionRecoveryPrefix` reads `session.HaltedReason` and threads it through with no new
  store query.
- The `recovery_pack_planted` event_log row's metadata reflects whether a halt reason was
  included.
- A cold-booted session that was previously halted, on its next recovered turn, sees why it
  was halted in the planted recovery pack — verified by a test, not just a code read.
- No change to `halt_session`'s own reflex mechanism, `MarkSessionHalted`/`ClearSessionHalt`,
  or the synchronous same-turn abort in `chat_generate.go`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
