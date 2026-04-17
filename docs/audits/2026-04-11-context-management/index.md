# Deep Review: context-management — 2026-04-11

## Scope

**Scope string:** `context-management` (INDEX.md item 27)

**Interpretation:** Slot system, hot-swap, auto-compaction, `/compact` command. Mixed deep-review + claimed-vs-actual verification per explicit mandate. Primary deliverable: prove whether these features work end-to-end or are stubs/partials.

**Files read in full:**
- `internal/context/slot.go` (84 lines)
- `internal/context/window.go` (197 lines)
- `internal/context/compaction.go` (268 lines)
- `internal/context/tokens.go` (20 lines)
- `internal/context/window_test.go` (181 lines)
- `internal/context/compaction_test.go` (205 lines)
- `internal/chat/context.go` (120 lines)
- `internal/chat/context_client.go` (439 lines)
- `internal/chat/commands.go` (229 lines)
- `internal/chat/commands_builtin.go` (245 lines)
- `internal/service/context.go` (127 lines)
- `internal/service/context_test.go` (149 lines)
- `internal/service/chat_generate.go` (lines 1-320, 680-700)
- `internal/service/events_composite.go` (lines 100-135)
- `internal/service/events.go` (lines 25-30)
- `internal/service/container.go` (lines 430-450)
- `internal/service/chat.go` (lines 55-100)
- `internal/service/chat_test.go` (lines 1-80)
- `internal/api/sessions.go` (lines 252-289)
- `internal/api/api.go` (lines 65-75)
- `internal/api/context_breakdown.go` (144 lines)

**Extended scope beyond literal label:**
- `internal/service/context.go` — the integration layer between `internal/context/` and the chat engine
- `internal/service/chat_generate.go` — the actual generate path that should consume context management
- `internal/api/sessions.go` — the `/compact` API handler

**Prior audits cross-referenced:**
- `2026-04-11-contextbroker/` — broker subsystem. Findings 02 and 07 (multi-byte truncation) are the same pattern found here. Finding 01 (similarity ranking) is broker-internal. Not re-flagged.
- `2026-04-10-chat-engine/` — engine decomposition. Context assembly delegation is the handoff point for this scope.

## Methodology

**Categories applied:**
- Correctness — primary focus. Does the code do what the names and comments claim?
- Claimed-vs-actual verification — explicit mandate. Traced call paths from entrypoints (generate, /compact API) through to the context management subsystem.
- Error handling — budget enforcement error paths
- Antipatterns — dead code, dead event hooks

**Categories deferred:**
- Security — no trust boundaries in the context management layer itself (input validation is upstream)
- Concurrency — `internal/context/` has no goroutines; `PruneAfterTurn` runs single-threaded within the generate goroutine
- Standards and tooling — deferred to whole-repo sweep (already complete for this campaign)
- Test quality — tests for `internal/context/` are thorough; the gap is that the tested code isn't wired

**Tools NOT run (narrow scope deferral):**
- `go vet`, `go test -race` — deferred, covered by whole-repo sweep
- `govulncheck` — deferred to dependency-audit scope

## Claimed-vs-actual verdict

| Feature | Claimed | Actual |
|---------|---------|--------|
| **Slot system** | 8 named slots with budgets, ordering, cache keys | Data structures exist and are well-tested. **Not wired into the generate path.** Only 2 of 8 slots are ever populated. `AssembleSlots` has zero non-test callers. |
| **Hot-swap** | Runtime slot swapping | **Does not exist.** `ContextWindow` is ephemeral (created and discarded per-call). No persistent window, no swap API, no runtime mutation path. |
| **Auto-compaction** | Fires when context exceeds budget | `NeedsCompaction()` is computed but **never acted on**. `CompactionPipeline` (3-stage with LLM summarizer) is **never instantiated in production**. The actual budget enforcement is `EnforceTokenBudget` — a brute-force trim cascade that works but is not "compaction." |
| **`/compact` command** | Compact session context | API handler is an **MVP stub** that concatenates and truncates at 2000 chars. Does not invoke `CompactionPipeline`, does not call LLM, does not fire events, does not actually reduce context for subsequent turns (marks messages as compacted but writes same content back). |
| **PruneAfterTurn** | Tool result compaction | **Works end-to-end.** Correctly replaces old tool outputs with markers in the database. Runs after every turn. |
| **EnforceTokenBudget** | Pre-send budget gate | **Works end-to-end.** 4-stage cascade prevents context overflow. In-memory only (doesn't persist reductions). |

**Summary:** The context management subsystem has ~570 lines of well-tested infrastructure (`internal/context/`) that is not wired into the application. The two features that actually work (`PruneAfterTurn`, `EnforceTokenBudget`) live in `internal/chat/context_client.go` and predate the slot system. The slot system, hot-swap, auto-compaction, and the `/compact` command are stubs or partial implementations.

## Findings

### By severity

**Critical (2)**
- [01 — Slot system exists as data structures but is not wired into the generate path](01-critical-slot-system-unwired.md)
- [02 — /compact API handler is an MVP stub — CompactionPipeline never invoked](02-critical-compact-api-is-mvp-stub.md)

**High (2)**
- [03 — Hot-swap does not exist as a feature](03-high-hot-swap-does-not-exist.md)
- [04 — Auto-compaction never fires — NeedsCompaction computed but never acted on](04-high-auto-compaction-never-fires.md)

**Medium (3)**
- [05 — EmitPreCompact / EmitPostCompact event hooks are dead code](05-medium-compact-events-dead-code.md)
- [06 — AssembleContext budget hardcodes 200k regardless of provider window size](06-medium-budget-ignores-provider-window.md)
- [07 — Compact handler splits multi-byte UTF-8 at 2000-char boundary](07-medium-compact-handler-multibyte-split.md)

**Low (0)**
_none_

**Info (1)**
- [08 — Observations (what works, design notes)](08-info-observations.md)

### By topic

**Claimed-vs-actual / Stub detection**
- [01 — Slot system unwired](01-critical-slot-system-unwired.md)
- [02 — /compact is MVP stub](02-critical-compact-api-is-mvp-stub.md)
- [03 — Hot-swap does not exist](03-high-hot-swap-does-not-exist.md)
- [04 — Auto-compaction never fires](04-high-auto-compaction-never-fires.md)

**Dead code**
- [05 — Compact event hooks dead](05-medium-compact-events-dead-code.md)

**Correctness / Budget**
- [06 — Budget ignores provider window](06-medium-budget-ignores-provider-window.md)
- [07 — Multi-byte truncation in compact handler](07-medium-compact-handler-multibyte-split.md)

**Observations**
- [08 — What works, design notes](08-info-observations.md)

## Recommended next steps

1. **Wire `AssembleSlots` into the generate path** (fixes findings 01, 03, 04). This is the single highest-leverage change — it activates the slot system, enables cache-hit tracking, and provides the hook point for auto-compaction. Requires persisting `ContextWindow` per-session and translating `SlotBlock` output into provider payloads.

2. **Implement `Summarizer` and wire `CompactionPipeline`** (fixes finding 02, partially fixes 04). Use the utility model to implement the `Summarizer` interface. Wire the pipeline into both the `/compact` API handler and the auto-compaction path triggered by `NeedsCompaction()`.

3. **Fire compaction events** (fixes finding 05). Add `EmitPreCompact`/`EmitPostCompact` calls around the pipeline. This activates memory extraction post-compaction.

4. **Pass provider window size through to budget enforcement** (fixes finding 06). Replace hardcoded `DefaultContextWindow` with the provider's reported context window.

5. **Fix multi-byte truncation** (fixes finding 07). Apply rune-aware slicing. One-line fix.

6. **Populate remaining 6 slots** (enhancement). Currently only system and conversation slots are populated. Memory, agent, rules, tools, session, and context slots are defined but empty. This is the incremental payoff after the slot system is wired.

## Known issues skipped

- **Similarity ranking always fails** — `2026-04-11-contextbroker/01-high-similarity-ranking-always-fails.md`. Broker-internal. Not context management.
- **FormatPacket unsanitized injection** — `2026-04-10-chat-engine/06-high-context-broker-unsanitized-injection.md`. Trust boundary issue in broker output formatting. Not re-flagged.
- **Multi-byte truncation in source_conduit and source_session** — `2026-04-11-contextbroker/02-high, 07-medium`. Same pattern as finding 07 here, but different code paths. Not re-flagged.
- **scope_guard.go dead code** — `2026-04-10-chat-engine`. Already tracked. The defense-layer pattern was considered but is not in this scope.

## Noticed but out of scope

- **`internal/chat/context_client.go:L80-L83` converts system/tool roles to "user" unconditionally.** The Anthropic API constraint drives this, but it means tool results lose their role distinction in the provider payload. For providers that support a `tool` role (OpenAI, Gemini), this is suboptimal. Suggested follow-up scope: `provider-message-formatting`.

- **`internal/service/chat_generate.go:L265` runs `EnforceTokenBudget` inside the tool-use loop.** On each iteration, it re-estimates tokens and potentially drops messages/tools. If the LLM keeps generating tool calls that expand context, the budget enforcement runs repeatedly but always uses the original system prompt (which may have grown via plugin filters). This is correct but fragile — a plugin that appends large content to the system prompt on every filter pass would cause progressive message loss. Suggested follow-up scope: `tool-use-loop-budget-stability`.

- **`internal/api/context_breakdown.go` does not use the slot system.** The context breakdown API endpoint estimates tokens independently of both `AssembleContext` and `AssembleSlots`, leading to numbers that may not match what the generate path actually sends. When the slot system is wired, this endpoint should read from the persisted `ContextWindow`. Suggested follow-up scope: `context-breakdown-accuracy`.
