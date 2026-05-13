# Slot system invariants

> **Status:** load-bearing contract — every invariant below is enforced
> by a test in `internal/service/slot_invariants_test.go`. The
> deliberate-violation tests in the same file prove each assertion has
> teeth (a real break is caught, not just nominally checked).
>
> **Provenance:** CW-20260512-0124 (SP-20260512-0011 Wave 4). Codifies
> emergent properties of Sprints 1-3 and Sprint 4 W1+W2+W3.

The Context Broker assembles every dispatched LLM turn from named slots
defined in `internal/context/slot.go` (the `SlotOrder` slice). The six
invariants below describe contracts that hold across ALL agent-flavored
dispatch types (chat, sync subagent, async subagent, background_agent)
regardless of CallerType. If a future refactor needs to change one,
update this doc IN THE SAME COMMIT so the test name and the
human-readable contract stay in sync.

---

## INV1 — Stable sent shape (5-7 slots, fixed positions)

**Invariant.** The Context Broker's per-turn `AssemblyPlan` emits
exactly `len(ctxpkg.SlotOrder)` decisions in the SAME order across
every dispatch flavor. Positions are load-bearing: Anthropic's
`cacheable_prefix_tokens` math assumes positional stability. Wire-level
slot count (slots with non-empty content) lands in the 5..N range
where N == `len(SlotOrder)`; the 5-7 typical range is the working-
session shape (Universal + System + Agent + Rules + conversation,
plus optional Permissions/Workspace/Tools).

**Why it matters.** A slot drop or reorder breaks the cacheable prefix
and produces non-deterministic cache miss patterns. Every dispatch
type MUST emit the same plan shape so cache savings are predictable
across chat ↔ subagent ↔ background dispatches.

**Enforced by.** `invariantStableSentShape` in
`internal/service/slot_invariants_test.go`, exercised by
`TestSlotInvariants_AcrossDispatchTypes` for each of the four dispatch
flavors. Cross-flavor identity is additionally pinned by
`TestSlotInvariants_IdenticalShapeAcrossCallerTypes`.

**Relied on by.** `internal/llm/anthropic/cache_plan.go`
(`stablePrefixSlotPriority` walk assumes ordered SlotBlocks);
`internal/service/chat_generate.go` (`request_build` slog walks
`ctxpkg.SlotOrder` to emit `<slot>_tokens` fields per dispatch).

---

## INV2 — Universal slot always at position 0

**Invariant.** `SlotUniversal` sits at position 0 of every plan AND
carries non-empty content sourced verbatim from
`chat.UniversalRulesBlock()`. Every dispatch flavor — chat, sync/async
subagent, background_agent — sees the universal-rules block as the
first system-prompt block on the wire.

**Why it matters.** This is the structural fix for the c160 subagent
fabrication regression: pre-CW-20260512-0114, agents with empty
`SystemPrompt` and no template assignment received zero grounding/
refusal rules. The universal slot guarantees those rules reach every
agent regardless of profile body or template assignment. CW-20260512-0122
extended the block with the "Acknowledge subagent failure" clause so
the rule continues to be the right shape post-envelope.

**Enforced by.** `invariantUniversalAtPositionZero` in
`internal/service/slot_invariants_test.go`. Deliberate-violation
coverage:
`TestSlotInvariants_DeliberateViolation_UniversalSlotRemoved` (catches
a regression that disconnected the universal source) and
`TestSlotInvariants_DeliberateViolation_PositionZeroNotUniversal`
(catches a refactor that reordered SlotOrder).

**Relied on by.** `internal/chat/universal_rules.go::UniversalRulesBlock`
(content authority); `internal/contextbroker/assembly.go::DecideAssembly`
(walks `SlotOrder` with SlotUniversal at index 0);
`internal/llm/anthropic/cache_plan.go::stablePrefixSlotPriority`
(SlotUniversal first in the priority list).

---

## INV3 — Cache marker priority (universal + system, never dynamic)

**Invariant.** Anthropic cache_control markers land ONLY on slots in
`internal/llm/anthropic/cache_plan.go::stablePrefixSlotPriority` —
today exactly `[SlotUniversal, SlotSystem]`. Both slots are
non-compactable (per `ctxpkg.DefaultCompactable`) and ship with
non-empty content on a well-formed assembly. Per-turn dynamic slots
(SlotMode, SlotMemory, SlotPermissions, SlotWorkspace, SlotTools,
SlotSession, SlotContext, SlotUserContext, SlotHandoff,
SlotConversation) NEVER carry markers — dynamic-content marker
placement would invalidate the cache offset.

**Why it matters.** Cache markers are a finite resource (Anthropic's
4-marker cap per request). The codified priority guarantees
SlotUniversal gets the first marker (the largest stable prefix
savings); a marker on dynamic content would mean every turn invalidates
the cache. CW-20260512-0109 (SP-20260512-0008 W3) codified the priority.

**Enforced by.** `invariantCacheMarkerPriority` in
`internal/service/slot_invariants_test.go` pins the broker-side
precondition (cacheable-prefix anchors are ActionShip with non-empty
content; compactability flags match `ctxpkg.DefaultCompactable`). The
downstream wire-level marker placement is pinned by
`TestCacheMarkerPriority_UniversalSlotFirst_LoadBearing` and
`TestCacheMarkerPriority_NeverOnDynamicContent` in
`internal/llm/anthropic/cache_marker_priority_test.go`.

**Relied on by.** `internal/llm/anthropic/params.go`
(`buildSystemBlocks` / `buildTools` / `buildMessages` consult the
plan); `internal/llm/anthropic/cache_plan.go::planCacheMarkers`
(enforces the cap via reverse-priority drop).

---

## INV4 — Mode-aware content swap correctness

**Invariant.** Changing a session's `current_mode_id` (the
session-scoped `*store.Mode` pointer) changes `SlotMode`'s CONTENT on
the next dispatch. Its POSITION and IDENTITY in the plan remain
unchanged. CW-20260512-0115 (SP-20260512-0009 W5) made mode a SESSION
attribute (not an agent attribute); two sessions sharing one agent
profile can run in different modes concurrently and see different
SlotMode bodies.

**Why it matters.** Mode is the user-facing knob for build/plan/read
behavior. Mid-session mode swaps must reach the LLM on the very next
turn without restart. Position stability ensures the cacheable prefix
slots ahead of SlotMode (Universal, System) survive the swap.

**Enforced by.** `invariantModeAwareContentSwap` in
`internal/service/slot_invariants_test.go`, plus the existing
load-bearing tests
`Test_ModeIsSessionAttribute_SameAgentDifferentModes_DifferentSlotContent`,
`Test_ModeChangeMidSession_NextDispatchReflectsNewMode`, and
`Test_ModeChangeMidSession_CacheableSlotsUnchanged` in
`internal/service/mode_session_attr_test.go`.

**Relied on by.** `internal/service/chat_generate.go` (reads
`session.CurrentModeID` at top of every turn via `GetSessionMode`);
`internal/chat/context_client.go::AssembleSlotSources` (renders
`SlotMode` from the resolved `*store.Mode`).

---

## INV5 — Pointer / stash determinism

**Invariant.** When a slot's content exceeds its per-slot budget, the
broker substitutes a deterministic pointer envelope
(`<ref:artifact_id=ART-..., tokens=N, available via dev_read>`) and
stashes the original content. Same `(SessionID, SlotName, Content)`
tuple MUST produce the same `ArtifactID` across runs — content
addressing via `contextbroker.DeterministicArtifactID`. Two
back-to-back assemblies of identical input produce byte-identical
pointer envelopes; the cacheable prefix is preserved.

**Why it matters.** The pointer envelope itself is part of the wire
payload. Non-deterministic IDs would invalidate the cacheable prefix
on every turn where an oversized slot is present (e.g. AGENTS.md
walk-up of a large project tree). CW-20260512-0110 wired the
artifact-store-backed stash with the atomicity contract: stash MUST
complete before pointer ships; if it fails, the slot falls back to
inline ship rather than emit a pointer to nowhere.

**Enforced by.** `invariantPointerStashDeterminism` in
`internal/service/slot_invariants_test.go`. Deliberate-violation
coverage:
`TestSlotInvariants_DeliberateViolation_PointerNonDeterministic`
(a regressed stasher returning different IDs across calls is caught
end-to-end).

**Relied on by.** `internal/contextbroker/stash.go::SlotStasher`
contract (the artifact-store-backed implementation in
`internal/service`); `internal/contextbroker/assembly.go::DecideAssembly`
(atomicity fallback at the
`stasher.StashSlot(...) → err != nil` branch).

---

## INV6 — Permission visibility

**Invariant.** When the agent has effective path constraints (binary-
scoped `DevToolsAllowedPaths` or session-scoped `PathGrants`), the
`SlotPermissions` block renders the constraints in human-readable form
with a stable "## Path access" header. The LLM reads what it can reach
rather than reasoning about access from priors and fabricating.
CW-20260512-0118 (SP-20260512-0010 W2) is the structural fix for the
c160 turn-16 fabrication regression target.

**Why it matters.** Pre-CW-20260512-0118 the path constraint substrate
was invisible to the LLM. The path_grants lineage system gave child
sessions access to paths the parent mentioned, but the child's prompt
had no language about that — when a guessed path failed, the child
concluded "I have no access" and fabricated. SlotPermissions closes
the loop: the constraint substrate now ships to the LLM as part of the
prompt, with a refusal hook that points at the path-naming convention.

**Enforced by.** `invariantPermissionVisibility` in
`internal/service/slot_invariants_test.go`. The renderer's shape
(scope qualifier text, deny-enumeration, provenance tags, refusal
hook) is pinned by `TestAssembleSlotSources_PermissionsSlot_*` in
`internal/chat/context_client_permissions_test.go`. Subagent deny
inheritance (W3 / CW-20260512-0119) is pinned by
`TestDeriveSubagentRuleSet_ThreeLevelChain` in
`internal/permission/derivation_test.go` +
`TestChatRunner_ThreeLevelChainPropagatesDenies` in
`internal/service/subagent_runner_derivation_test.go`.

**Relied on by.** `internal/chat/context_client.go::buildPermissionsSlotContent`
(renderer); `internal/permission/summary.go::RenderPermissionSummary`
(human-readable projection); the universal-rules block's refusal hook
(in `internal/chat/universal_rules.go`) which cites the
SlotPermissions content.

---

## How to evolve the contract

If a future ticket needs to change one of these invariants:

1. Update the contract paragraph above WITH the rationale in the same
   commit as the test change.
2. The test name in `internal/service/slot_invariants_test.go` is the
   primary pointer; rename intentionally if scope changes (a
   `git grep` for the old test name will surface stale doc references).
3. Add a deliberate-violation test for the new invariant shape if you
   add a new one — the violation pin is the load-bearing teeth check.
4. Update `internal/llm/anthropic/cache_plan.go::stablePrefixSlotPriority`
   in lock-step if INV3 changes — that list is the exhaustive
   definition of the cacheable prefix.
5. The W1 cross-CallerType structural pin
   (`TestRun_identicalShapeAcrossCallerTypes` in
   `internal/dispatcher/dispatcher_test.go`) is the upstream peer of
   `TestSlotInvariants_IdenticalShapeAcrossCallerTypes` here — they
   pin the same property at different layers (dispatcher input vs
   broker output). Keep them aligned.
