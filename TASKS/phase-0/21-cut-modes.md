# Cut Modes, in full (Session Mode, Legacy Agent Mode, mode↔agent junction, `classify.ClassifyMode`)

**Phase:** 0
**Status:** not-started
**Depends on:** none functionally, but see the **serialization note** below re: `18a-cut-dead-storage-and-config` — both tasks edit the same slice literal in `internal/store/agents.go`'s `DeleteAgent`.
**Touches:** `internal/store/migrations/001_schema.sql`-defined `modes`, `agent_modes`, `agent_mode_assignments` tables (new drop migration), `sessions.current_mode_id` column (new migration), `internal/store/modes.go` (delete file), `internal/store/agents.go` (Legacy `AgentMode` CRUD + `DeleteAgent` cleanups slice — **shared with `18a`**), `internal/classify/mode.go` (delete file), `internal/classify/route.go` (doc-comment reference), `internal/service/chat_broker_dispatch.go` (`buildBrokerInput` — **the coupled agent-broker step, see Context**), `internal/service/chat_generate.go` (mode resolution + `mode_suggestion` SSE emit), `internal/service/agent.go` (`ResolveForSession`/`resolveForSession` return signature), `internal/context/slot.go` (`SlotMode`), `internal/chat/context_client.go` (`SlotMode` rendering), `internal/context/INVARIANTS.md` (**INV4 update — load-bearing, see Context**), `internal/service/slot_invariants_test.go`, `internal/service/mode_session_attr_test.go`, `internal/api/*` (mode CRUD routes — enumerate during implementation), `ui/src/components/chat/ChatHeader.tsx` (mode chip/dropdown), `ui/src/components/chat/ChatComposer.tsx` (`/mode`/`/chat`/`/plan`/`/work` slash-command handling — **not** `useShellMode`, see Context), `ui/src/components/settings/agents/AgentDetailView.tsx` ("Modes" tab), `ui/src/components/settings/inspector/InspectorPanel.tsx`, `ui/src/hooks/useChat.ts`, `ui/src/lib/types.ts`

## Context

TASKS.md Phase 0 item 21 (note: TASKS.md's own numbering has an unrelated duplicate "25" between its Cuts and Renames sections — not a duplicate "21"; this file is unambiguously the Modes cut, the only Phase-0 item so numbered): *"Modes, in full — mechanical, with one coupled step: the agent broker's mode-dependent rules (2–4, the `mode=work`/`mode=plan` thresholds) must be stubbed out in the same change, or the broker breaks referencing a deleted signal. One PR, not two efforts landing separately."* Decision log §10 ("Steering — agent broker, strategy planner, modes, promptrouter") and architecture doc `03-steering.md`'s "What's cut" section both give the same four-part enumeration: *"Session Mode (+ `modes` table + `/mode`/`/chat`/`/plan`/`/work` slash commands), Legacy Agent Mode, the mode↔agent junction table, the per-turn `classify.ClassifyMode` signal."* **This is flagged as requiring extra care — it touches a live, enforced invariant test (`internal/context/INVARIANTS.md`'s INV4), not just ordinary application code.**

### Four distinct, real pieces — verified against the schema and code, not just the docs

Verified there are genuinely four separate mechanisms in the tree, matching the docs' enumeration exactly (not three, not five):

1. **Session Mode** — `modes` table (`internal/store/migrations/001_schema.sql`, line 247: `slug`/`name`/`prompt_addendum`/`tool_overrides`/`is_builtin`), `sessions.current_mode_id` column (added by `internal/store/migrations/040_session_current_mode.sql`, nullable FK to `modes(id)`), full CRUD in `internal/store/modes.go` (`CreateMode`/`GetMode`/`GetModeBySlug`/`ListModes`/`UpdateMode`/`DeleteMode`/`SetSessionMode`/`ClearSessionMode`/`GetSessionMode`, plus `BuiltinModes`/`SeedBuiltinModes`). Live frontend: a real mode chip + dropdown in `ui/src/components/chat/ChatHeader.tsx` (`useQuery(["modes"], api.listModes)`, `useQuery(["session-mode", ...], api.getSessionMode)`), doc comment: *"Backed by `sessions.current_mode_id` → `modes.id`; null = 'chat' default."*
2. **Legacy Agent Mode** — a separate, older `agent_modes` table (same migration file, line 61, predates `modes`/`current_mode_id` — per-agent embedded modes: `agent_id`/`slug`/`name`/`prompt_addendum`/`tool_overrides`, no reference to the `modes` catalog). CRUD lives inline in `internal/store/agents.go` (lines ~257, 574, 605). `internal/service/agent.go`'s `ResolveForSession`/`resolveForSession` returns `*store.AgentMode` (distinct Go type from `*store.Mode`) as its second return value — consumed every turn via `chat_generate.go:208`'s `agent, mode, err := s.agents.ResolveForSession(ctx, sessionID)`. Live frontend: a real "Modes" tab in `ui/src/components/settings/agents/AgentDetailView.tsx` (`TabsTrigger value="modes"`, copy: *"Modes define alternate behaviors for this agent — each with its own prompt addendum and tool overrides."*). `modes.go`'s own doc comments confirm the two systems coexist deliberately today: `ClearSessionMode`'s comment says *"restoring the fall-through to the agent-scoped legacy AgentMode"* — Session Mode overrides when set, Legacy Agent Mode is the fallback.
3. **Mode↔agent junction table** — `agent_mode_assignments` (same migration, line 258: `agent_id`/`mode_id REFERENCES modes(id)`), joining `agent_profiles` to the (newer) `modes` catalog — an allowlist of which modes an agent may be switched into. CRUD: `AssignModeToAgent`/`UnassignModeFromAgent`/`GetAgentAssignedModes` in `internal/store/modes.go`. **Do not confuse this with `agent_modes` (item 2) — they are different tables with different shapes; both are real and both get cut, but as separate pieces.**
4. **`classify.ClassifyMode`** — `internal/classify/mode.go`, a deterministic phrase/slash-prefix classifier (`/chat`/`/plan`/`/work` prefixes at confidence 1.0, imperative phrases at 0.85, action-verb-first-token at 0.7, default "chat" at 0.0). Two call sites: `chat_generate.go:512` (emits the non-binding `mode_suggestion` SSE event, consumed by `ui/src/components/settings/inspector/InspectorPanel.tsx` and referenced in `useChat.ts`) and `chat_broker_dispatch.go:372` (feeds the agent broker — see the coupled step below).

### The coupled step — verified exact location, and confirmed it's fully containable inside `apps/nanite`

TASKS.md's warning ("agent broker breaks referencing a deleted signal") is real and precisely located. The agent broker itself (`github.com/hollis-labs/agentkit/broker`, wired via `agentbroker.New()` in `cmd/nanite/main.go:354`) is an **external module in a separate git repository** (`/Users/chrispian/dev/hollis-labs/libs/agentkit`, confirmed via `git rev-parse --show-toplevel` — not a subdirectory of this repo). Its `DeterministicBroker.Decide` (`libs/agentkit/broker/deterministic.go`) has 6 priority-ordered rules; rules 2–4 are exactly the ones TASKS.md means:

```
Rule 2 — in.Mode == ModeWork && in.ModeConfidence >= ModeConfidenceHigh → ProfileWorker, "mode=work"
Rule 3 — in.Mode == ModeWork && in.ModeConfidence >= ModeConfidenceLow  → ProfileWorker, "action-verb-work"
Rule 4 — in.Mode == ModePlan && in.ModeConfidence >= ModeConfidenceHigh && in.ScopeTier == TierOpen → ProfilePlanner, "mode=plan,tier=open"
```

**Good news: this does NOT require touching the external `libs/agentkit` repo.** `in.Mode`/`in.ModeConfidence` are plain fields on the `agentbroker.Input` struct that Nanite itself populates, in exactly one place: `internal/service/chat_broker_dispatch.go`'s `buildBrokerInput` (lines 366-376):

```go
mode := classify.ClassifyMode(userContent)
if mode.Suggested != "" {
    in.Mode = mode.Suggested
    in.ModeConfidence = mode.Confidence
}
```

Once `classify.ClassifyMode` is deleted, **this block must be deleted too, in the same change**, leaving `in.Mode`/`in.ModeConfidence` at their zero values. Since `ModeWork`/`ModePlan` are non-empty string constants, `in.Mode == ModeWork` (and `== ModePlan`) will always be false when `in.Mode` is unset — rules 2/3/4 become permanently unreachable without any change to the external broker package. Rule 1 (reflex override) and rules 5/6 (`ScopeTier`/`ExecutionPattern`-only, mode-independent) are untouched and continue to work. **This is the literal "stubbed out in the same change" TASKS.md asks for — confirmed mechanically sufficient, no cross-repo coordination needed.** If a future reviewer wants the *unreachable* rules 2–4 physically deleted from `agentkit` too (not just neutered), that would be a separate, cross-repo follow-up — out of scope here since `agentkit` is versioned/shared infrastructure (also consumed by sibling apps per its module path), not something this task can safely edit as a side effect.

Also remove the `mode := classify.ClassifyMode(userContent); ... mode_suggestion` block in `chat_generate.go` (lines ~506-535) — the SSE event this emits has no signal to emit once the classifier is gone, and its frontend consumer (`InspectorPanel.tsx`) should stop expecting it.

### INV4 — real correctness requirement, not optional cleanup (read `internal/context/INVARIANTS.md` in full before starting)

Decision log §10 and architecture doc `03-steering.md` both flag: *"this makes the context/slot system's `INV4` invariant ('mode-aware content swap') vacuous — needs a deliberate small update when this lands, not silent rot."* Verified against `internal/context/INVARIANTS.md` directly — **INV4's actual text** (lines 116-143):

> **Invariant.** Changing a session's `current_mode_id` (the session-scoped `*store.Mode` pointer) changes `SlotMode`'s CONTENT on the next dispatch. Its POSITION and IDENTITY in the plan remain unchanged. [...] Mid-session mode swaps must reach the LLM on the very next turn without restart. Position stability ensures the cacheable prefix slots ahead of SlotMode (Universal, System) survive the swap.

Enforced by `invariantModeAwareContentSwap` in `internal/service/slot_invariants_test.go`, plus `Test_ModeIsSessionAttribute_SameAgentDifferentModes_DifferentSlotContent`, `Test_ModeChangeMidSession_NextDispatchReflectsNewMode`, `Test_ModeChangeMidSession_CacheableSlotsUnchanged` in `internal/service/mode_session_attr_test.go`. Relied on by `internal/service/chat_generate.go` (reads `session.CurrentModeID` every turn) and `internal/chat/context_client.go::AssembleSlotSources` (renders `SlotMode` from the resolved `*store.Mode`).

**Once `current_mode_id`/`modes`/`GetSessionMode` are gone, INV4 as literally written is vacuous — there is no more `current_mode_id` to change, so "changing it changes SlotMode's content" is trivially/meaninglessly true (or the test setup that exercises it no longer compiles).** Per `INVARIANTS.md`'s own "How to evolve the contract" section (step 1: *"Update the contract paragraph above WITH the rationale in the same commit as the test change"*), a **"deliberate small update, not silent rot"** here means:

1. `SlotMode` itself is **not** being removed by this task — `SlotOrder`, `INV1` (stable sent shape, 5-7 slots), and `INV3` (cache marker priority list) all reference it structurally (`internal/context/slot.go`'s `SlotOrder` slice, `internal/llm/anthropic/cache_plan.go::stablePrefixSlotPriority`). Removing the slot itself would ripple into INV1/INV3 and the anthropic cache-plan code — **out of scope for this task; do not touch `SlotOrder`'s shape.**
2. What changes: `SlotMode`'s **content source**. Today it's `chat_generate.go` reading `session.CurrentModeID` → `store.GetMode` → `sessionMode.PromptAddendum`. Post-cut, there is no more session-mode pointer to read. The slot's rendering in `internal/chat/context_client.go` needs a decision: does `SlotMode` render nothing (empty, as an intentionally-inert slot preserving the 5-7 position count for INV1), or does it get repurposed for something else? **This task's scope is the former (render empty/absent, preserve position)** — repurposing the slot is a separate, later decision not covered by any doc read for this task; escalate if the mechanical "render empty" approach turns out to be more involved than expected.
3. **Rewrite INV4's contract paragraph in `INVARIANTS.md` in the same commit** to describe the new reality: `SlotMode` is now always-empty/inert (position preserved for INV1, no content-swap behavior to invariant-test anymore), OR document whatever the actual resolution turns out to be. Do not leave the old "mode-aware content swap" language in place once there's no session-mode pointer to swap.
4. **Update or retire the specific tests**: `invariantModeAwareContentSwap` in `slot_invariants_test.go` and the three tests in `mode_session_attr_test.go` currently assert the swap behavior — they will not compile/pass once `current_mode_id`/`GetMode` are gone. Per `INVARIANTS.md`'s evolution guidance, rename/retire them deliberately (with the doc update in the same commit) rather than leaving them broken or silently deleting them without updating the doc that names them.
5. Leave INV1, INV2, INV3, INV5, INV6 and their tests completely untouched — verified none of them depend on mode-specific content, only on `SlotMode`'s continued existence as a named, positioned slot (which this task preserves).

### Serialization note vs. `18a-cut-dead-storage-and-config`

Both this task and `18a` edit `internal/store/agents.go`'s `DeleteAgent` function — specifically its `cleanups` slice (lines ~436-465), a single `[]string` literal. `18a` removes the `agent_cycles`/`agent_boot_plans` lines; this task removes the `agent_modes`/`agent_mode_assignments` lines. Different lines, same slice, same file. If dispatched as parallel worktree branches, this will produce a small, easily-resolved merge conflict on that one slice — not silent corruption, but flag it to the Orchestrator rather than assuming clean auto-merge. Prefer landing one before the other if avoidable.

### Naming collision warning — `useShellMode` is NOT part of this cut

`ui/src/components/chat/ChatComposer.tsx` imports `useShellMode` (a "3-state toggle" for something dev/bash-related, per its own comment: *"Shell mode 3-state toggle"*). This is an unrelated "mode" concept (shell-tool visibility/behavior, not Session/Agent chat mode) that happens to share the word — exactly the class of collision `GLOSSARY.md` exists to prevent. **Do not touch `useShellMode`, `cycleShellMode`, `setShellMode`, or the shell-mode toggle UI** — only the `/mode`, `/chat`, `/plan`, `/work` slash-command handling and the `sessions.current_mode_id`-backed mode chip/dropdown are in scope.

## What to do

1. Delete `internal/classify/mode.go`. Update the doc-comment cross-reference in `internal/classify/route.go` (it mentions `mode.go`'s `ClassifyMode` as a sibling primitive — reword, don't leave a dangling reference).
2. In `internal/service/chat_broker_dispatch.go`'s `buildBrokerInput`: delete the `mode := classify.ClassifyMode(userContent); if mode.Suggested != "" { in.Mode = ...; in.ModeConfidence = ... }` block (lines ~366-376). This is the coupled step — land it in the same commit/PR as the `classify.ClassifyMode` deletion, per TASKS.md's explicit instruction.
3. In `internal/service/chat_generate.go`: delete the `mode_suggestion` SSE-emit block (lines ~495-535, the `classify.ClassifyMode(userContent)` call plus its surrounding `sessionMode`/`mode_suggestion` payload construction). Remove `sessionMode *store.Mode` resolution (`session.CurrentModeID`/`s.store.GetMode`) if nothing else in the function still needs it after this block is gone — check before deleting the variable entirely.
4. Drop `modes`, `agent_modes`, `agent_mode_assignments` tables and the `sessions.current_mode_id` column (new migration).
5. Delete `internal/store/modes.go` in full (`Mode`/`AgentModeAssignment` types, all CRUD, `BuiltinModes`/`SeedBuiltinModes`). Remove the Legacy `AgentMode` CRUD from `internal/store/agents.go` (the `agent_modes`-backed functions around lines 257/574/605) and the `*store.AgentMode` return value from `internal/service/agent.go`'s `ResolveForSession`/`ResolveForSessionReadOnly`/`resolveForSession` — update every call site (`chat_generate.go:208` and others; grep `ResolveForSession` for the full list) to the new signature.
6. Remove the `"DELETE FROM agent_modes WHERE agent_id = ?"` and `"DELETE FROM agent_mode_assignments WHERE agent_id = ?"` lines from `internal/store/agents.go`'s `DeleteAgent` cleanups slice (~lines 438, 441). **Coordinate with `18a`** per the serialization note above.
7. Remove all mode-related REST routes (`internal/api/*` — enumerate via `grep -rn "[Mm]ode" internal/api/api.go` during implementation; expect routes for session-mode get/set, mode CRUD, and agent-mode-assignment CRUD).
8. Update `internal/chat/context_client.go`'s `SlotMode` rendering to produce empty/inert content (no more session-mode pointer to read) — keep the slot's position/identity in `SlotOrder` (`internal/context/slot.go`) unchanged.
9. Update `internal/context/INVARIANTS.md`'s INV4 section per the Context discussion above, in the same commit as the test changes. Update/retire `invariantModeAwareContentSwap` (`internal/service/slot_invariants_test.go`) and the three tests in `internal/service/mode_session_attr_test.go`.
10. Frontend: remove the mode chip/dropdown and `/mode`/`/chat`/`/plan`/`/work` slash-command handling from `ChatHeader.tsx`/`ChatComposer.tsx` (leave `useShellMode` untouched, see Context warning). Remove the "Modes" tab from `AgentDetailView.tsx`. Remove `mode_suggestion` handling from `InspectorPanel.tsx`/`useChat.ts`. Remove mode types from `ui/src/lib/types.ts`.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass — including a real pass/fail run of whatever `slot_invariants_test.go`/`mode_session_attr_test.go` become after step 9 (not just "compiles").
- `cd ui && npm run build` passes.
- `internal/context/INVARIANTS.md`'s INV4 section accurately describes the post-cut behavior of `SlotMode` — no stale "mode-aware content swap" language describing a mechanism that no longer exists.
- Fresh-boot migration produces a DB with no `modes`/`agent_modes`/`agent_mode_assignments` tables, no `sessions.current_mode_id` column.
- The agent broker's rules 1, 5, 6 (reflex override, tier×pattern, default-chat-handle) still fire correctly in a real session; rules 2-4 never fire (confirm via a manual test that a "work"-shaped or "plan"-shaped message no longer triggers mode-based worker/planner dispatch, only reflex-based or tier×pattern-based dispatch still can).
- No `/mode`, `/chat`, `/plan`, `/work` slash commands remain reachable from the chat composer.
- `useShellMode` and its 3-state toggle UI are completely unaffected (regression check for the naming-collision warning above).
- Tested against a real copy of the backed-up database, not just an empty fixture.
- Reviewer for this section explicitly confirms the INV4 update was reviewed as a correctness change, not rubber-stamped as routine cleanup — flag this to the Reviewer dispatch explicitly per this task's "extra care" designation.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
