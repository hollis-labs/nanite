# Scratchpad TTL pruning — reality check: confirm there is nothing to prune

**Phase:** 4
**Status:** not-started
**Depends on:** `TASKS/phase-0/27-cut-p7-scratchpad-snapshot.md` (removes the one mechanism that ever pushed scratchpad content into longer-lived storage — confirm landed before closing this task)
**Touches:** `internal/service/chat_loop_state.go` (`loopState`'s scratchpad field, read-only confirmation), `internal/mcp/self_tools.go` (`scratchpad_write`/`_read`/`_clear` — read-only confirmation)

## Context

TASKS.md Phase 5: *"Add TTL pruning to the scratchpad tool."* Architecture doc `06-session-lifecycle-and-recovery.md`: *"The scratchpad — a working-notes tool. Its value is the *act* of using it, not the content persisting. Kept, with TTL auto-pruning added (in-memory storage worth considering, given it's genuinely ephemeral)."*

### Verified: the scratchpad has zero persistent state today — this item's literal premise doesn't hold against current code

The scratchpad is a field on `loopState` (`internal/service/chat_loop_state.go:272-274`), whose own doc comment states: *"P4 Scratchpad — per-turn writable key/value buffer... Evicted automatically: loopState is created fresh per `generateResponse` call."* No DB table exists (confirmed: zero `CREATE TABLE.*scratchpad` matches anywhere in `internal/store/migrations/*.sql`). The three self-tools (`scratchpad_write`/`_read`/`_clear`, `internal/mcp/self_tools.go`) operate on this in-memory map only — their own tool descriptions confirm *"the scratchpad clears on turn exit"* / *"is per-generation only."* Size is already capped (8 KiB/value, 64 KiB/turn total, `chat_loop_state.go:296-299`) — not time-limited, because nothing survives past the current turn for a "time" to elapse against.

**P7** (`TASKS/phase-0/27-cut-p7-scratchpad-snapshot.md`) was the *only* mechanism that ever pushed scratchpad content into longer-lived storage (`handoff_stashes`, via `scratchpadSnapshot()` feeding the compaction pipeline) — and it's being cut, not extended, by Phase 0. Once `27` lands, there is genuinely no DB-persisted or otherwise longer-lived scratchpad state anywhere in the system for a TTL mechanism to prune.

This is not a case of "the decision log's rationale for the *action* is wrong but the action still stands" (the sharpened escalation rule's usual answer) — there is no cuttable/buildable target here at all once P7 is gone. Per the same standing project practice of flagging when a `TASKS.md` item's literal premise doesn't survive contact with the current code (see e.g. `TASKS/phase-0/25-drop-unused-session-status-enum.md`'s and `26-cut-session-compaction-summary-fields.md`'s own reality-check corrections), this task exists to record that finding for the operator rather than silently skip the item or invent pruning logic against a structure that has nothing to prune.

## What to do

1. Confirm Phase 0 #27 has landed — re-verify no other mechanism has grown a longer-lived scratchpad-content sink in the meantime.
2. If confirmed nothing persists: close this task as a documented no-op. Record in this file's Work Log exactly what was checked and why no TTL mechanism was built.
3. If, contrary to the above, a real persistent scratchpad-adjacent structure is found during implementation (something this planning pass missed), build the TTL pruning against that real target and document what it actually is — don't force a no-op conclusion if the premise turns out to be wrong in the *other* direction.
4. Separately, from architecture doc `03-steering.md`'s note: *"revisit whether a reflex should nudge usage at the right moments"* — this is explicitly a Phase 3 (Steering) concern, not this task's scope; do not build it here even if convenient.

## Done means

- A clear, evidence-based statement in this file's Work Log: either "confirmed no persistent scratchpad state exists post-Phase-0-#27; no TTL mechanism was needed or built" or a description of whatever real persistent structure was found and the TTL mechanism built against it.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass (no functional change expected in the no-op case).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
