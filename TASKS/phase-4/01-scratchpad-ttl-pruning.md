# Scratchpad TTL pruning — reality check: confirm there is nothing to prune

**Phase:** 4
**Status:** implemented
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

**Conclusion: confirmed no persistent scratchpad state exists post-Phase-0-#27; no TTL mechanism was needed or built.** Independently re-verified the planning pass's finding against current code rather than trusting the Context section's prose. Evidence:

1. **Phase 0 #27 landed on `main` and is present in this worktree.** `git log --oneline -- TASKS/phase-0/27-cut-p7-scratchpad-snapshot.md` shows commit `d67bc481 Phase 0 #27: cut P7 (scratchpad->handoff_stashes compaction snapshot)`, and its own `## Work log` is filled in with `**Status:** implemented`. Grepped the whole repo (`grep -rn "StashWriter\|ScratchpadSnapshot\|HandoffStashPayload\|BuildPayloadFromScratchpad\|storeStashWriter\|NewStashWriter\|scratchpadSnapshot(" --include="*.go" .`) — zero live references remain anywhere (the sole hit is a stale comment string `HandoffStashPayload` in `internal/context/handoff_envelope.go:12`, describing legacy-schema detection, not a live symbol). P7 (the one mechanism that ever pushed scratchpad content into `handoff_stashes`) is confirmed gone.

2. **The scratchpad is still exactly what its own doc comment says: a per-turn, in-memory `loopState` field, nothing more.** `internal/service/chat_loop_state.go:257-260` — `scratchpad map[string]any` / `scratchpadBytes int` with the comment "P4 Scratchpad — per-turn writable key/value buffer... Evicted automatically: loopState is created fresh per generateResponse call." Confirmed `newLoopState` (`chat_loop_state.go:290-301`) constructs `scratchpad: make(map[string]any)` fresh on every call — no seeding from any prior-session or DB source. Size caps unchanged and still time-agnostic: `scratchpadMaxValueBytes = 8 * 1024`, `scratchpadMaxTotalBytes = 64 * 1024` (`chat_loop_state.go:276-279`).

3. **The three self-tools (`scratchpad_write`/`_read`/`_clear`, `internal/mcp/self_tools.go:810-892`) dispatch to `internal/service/chat_scratchpad.go`**, whose `handleScratchpadTool` doc comment states outright: *"No MCP transport, no DB — pure in-process."* Read `callScratchpadWrite`/`callScratchpadRead`/`callScratchpadClear` and the underlying `ls.scratchpadWrite`/`scratchpadRead`/`scratchpadClear` methods (`chat_loop_state.go:590-654`) in full — all operate solely on the in-memory `ls.scratchpad` map; none touch `s.store`, any store interface, or any file/disk path.

4. **No DB table for scratchpad content anywhere.** `grep -rli "scratchpad" internal/store/migrations/*.sql` matches only three files, all unrelated to the P4 scratchpad tool's content:
   - `042_bottom_drawer_pinned_cards.sql` — `"scratchpad"` is one literal value in an unrelated UI `card_type` vocabulary for a FE-only drawer-pin table (`bottom_drawer_pinned_cards`); confirmed by reading the full migration — no relation to `loopState.scratchpad`.
   - `103_drop_session_intent.sql` — a comment citing the "Handoffs, scratchpad, and Glass-4" architecture-doc section for context; not a schema element.
   - `061_internal_profiles_file_sot.sql` — `"scratchpad"` appears as a tool-name string in a default per-profile tool list (telling an agent profile which tools it may use), not as stored scratchpad *content*.
   `grep -rn "scratchpad" internal/store/*.go` returns zero matches — no store-layer Go code references the scratchpad at all.

5. **Checked every other repo-wide mention of "scratchpad" for a persistence path the planning pass might have missed** (`grep -rln "scratchpad" --include="*.go" .` — 26 files) — all are either the self-tool/loopState code already covered above, prose/prompt text describing the scratchpad to the agent (`internal/chat/context.go`, `internal/chat/commands.go`'s `/scratch` slash-command handler which only returns a client-side `action` for the frontend to append text — no backend write), doc comments, or two forward-looking/unresolved references worth calling out explicitly since they mention "scratchpad" as a *source*:
   - `internal/service/handoff_glass4_fallback.go:28` — the `ensureGlass4HandoffPreCompact` doc comment claims `RecentDecisions: pulled from scratchpad keys when present`, but the actual `buildFallbackHandoff` function (lines 76-91) never reads `ls.scratchpad` at all — its own inline comment says `RecentDecisions and ActivePointers intentionally empty in the fallback`. This is a **stale/inaccurate doc comment describing an intent that was never implemented**, not a real scratchpad-to-DB persistence path. Noting it here for the record per standing practice of flagging discrepancies found along the way; not fixed as part of this task since it's an unrelated doc-comment correction outside this task's scoped "Touches" (`chat_loop_state.go`, `self_tools.go`) and has no functional effect (RecentDecisions is unconditionally empty regardless of scratchpad state).
   - `internal/dispatch/executor.go`'s `ContextHandle{Source: "scratchpad", ...}` — a typed pointer field on `ExecutorRequest.ContextHandles`. Traced its only two live call sites (`internal/mcp/self_tools_dispatch_executor.go` parses it from tool args; `internal/executor/envelope_render/executor.go:176` explicitly errors with *"the in-process pilot does not resolve ContextHandles"*) — confirmed this is a documented, currently-unresolved forward-looking extension point, not a live mechanism that reads or persists scratchpad content today.

6. **Cross-checked `docs/engineering/GLOSSARY.md:49`** — "Scratchpad — an agent's own working-notes tool... being usually-empty is expected, not a defect. Not a continuity mechanism (that's Glass-4's job)." Consistent with all of the above. No new name was introduced by this task (it's a no-op), so no glossary collision to worry about.

**Conclusion:** every path scratchpad content could theoretically reach a longer-lived sink through — P7 (cut), the self-tools themselves (in-memory only), a dedicated DB table (none exists), the Glass-4 fallback's doc-claimed-but-unimplemented read, and the dispatch executor's unresolved `ContextHandle` — is either already cut or was never live. There is genuinely nothing for a TTL mechanism to prune. No code changes were made; this task closes as a documented no-op, per its own "What to do" step 2.

**Baseline checks (all pass, no functional change made):**
- `go build ./cmd/nanite/` — ok.
- `go vet ./...` — one pre-existing failure, unrelated to this task and already documented as pre-existing in Phase 0 #27's own Work Log: `internal/service/container.go:1142/1162/1213` (`stopReaper`/`stopRuntimeReaper` "not used on all paths" lostcancel warnings). Confirmed via the same file/lines cited in `27-cut-p7-scratchpad-snapshot.md`'s Work Log — predates this task, not introduced by it.
- `go test ./...` — full suite passes, all packages `ok`.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
