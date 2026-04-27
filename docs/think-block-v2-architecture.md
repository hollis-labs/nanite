# Think-Block v2 Architecture

**Ticket:** CW-20260420-0022 — Think-block v2 (context-aware, PeerQuery-driven)
**Phase:** 6 / Cognition arc (F5)
**Depends on:** F2 (Think-block v1), E1 (Reflex matcher), G2 (PeerQuery primitive)

---

## Overview

Think-block v2 makes the system-prompt affordance hint list dynamic: instead of the
static four-item list shipped in v1, a hint-selector peer agent (dispatched via
PeerQuery) chooses which hints are most relevant for the current turn based on the
user's input, ScopeTier classification, and any matched reflex.

Fallback to v1 is automatic on any failure (peer unavailable, timeout, bad response).

---

## Fallback Chain

Controlled by two environment flags:

| NANITE_THINK_BLOCK_V1 | NANITE_THINK_BLOCK_V2_ENABLED | Result |
|---|---|---|
| false | any | v0 baseline (`thinkToolBlock`) |
| true (default) | false (default) | v1 static (`thinkToolBlockV1`) |
| true | true | v2 dynamic (peer dispatch) |
|   | peer dispatch fails | → fallback to v1 |
|   | peer returns unknown IDs | → fallback to v1 |
|   | rendered block > 200 tokens | → truncate, not fallback |

Default path: **v1 static**. Opt into v2 with `NANITE_THINK_BLOCK_V2_ENABLED=true`.

---

## Hint Catalog

### Location
- Source file: `config/think-hints/hints.yaml`
- Embedded copy (for Go embedding): `internal/chat/hints/hints.yaml`
- Loader: `internal/chat/hint_catalog.go`

### Schema
```yaml
hints:
  - id: scratchpad           # unique slug (a-z, 0-9, -)
    affordance: scratchpad   # display name
    body: >-                 # prompt text injected in the think block (~50 tokens)
      **Scratchpad** (nanite_scratchpad_write/read): ...
    triggers:
      scope_tier_in: [trivial, small, medium, large, open]  # optional tier filter
      reflex_id_in:  [researcher-mention]                   # optional reflex filter
      text_pattern:  "compaction"                           # optional substring match
    priority: 40             # tiebreak; higher wins (0–100)
```

### v1 Catalog (8 entries)

| ID | Affordance | Priority |
|---|---|---|
| scratchpad | scratchpad | 40 |
| memory_recall | memory_recall | 38 |
| playbook_research | playbook_research | 35 |
| peer_query | peer_query | 30 |
| use_handoff_stash | use_handoff_stash | 25 |
| consult_skills | consult_skills | 22 |
| scope_check | scope_check | 20 |
| reviewer_gate | reviewer_gate | 18 |

The four core affordances from v1 (scratchpad, memory_recall, playbook_research,
peer_query) are preserved with identical semantics. Four additional hints extend the
set for compaction-recovery, skill-awareness, scope discipline, and review-gate
scenarios.

---

## Hint-Selector Peer Agent

**Profile:** `config/agents/hint-selector.yaml`
**Slug:** `hint-selector`
**Effort:** EffortLow (fast, no reasoning)
**Model:** claude-haiku-4-20250514 (fast, cheap)
**Tool surface:** none (text-in / text-out only)

### Request shape (JSON)
```json
{
  "user_input":    "string — current user message",
  "scope_tier":   "trivial|small|medium|large|open",
  "reflex_match": "reflex-id-or-empty-string",
  "hint_catalog": [
    {"id": "...", "affordance": "...", "body": "...", "priority": 0}
  ]
}
```

### Response shape (JSON)
```json
["scratchpad", "memory_recall", "scope_check"]
```

A JSON array of hint IDs, ordered most-to-least relevant. The dispatcher
extracts the first `[...]` occurrence from the raw response to tolerate
prose-wrapped outputs.

---

## Dispatch Flow

```
assemble_system_prompt()
  │
  ├─ hintOpts.Dispatcher != nil && V2_ENABLED?
  │     YES → ThinkToolBlockWithDispatch(ctx, dispatcher, userInput, scopeTier, reflexID)
  │               │
  │               ├─ BuildDispatchPayload(userInput, scopeTier, reflexID, catalog)
  │               ├─ dispatcher.Dispatch(ctx, payload)  ← PeerQuery via HintDispatcher
  │               ├─ parseHintIDs(response)
  │               ├─ resolveHints(catalog, ids)
  │               ├─ renderDynamicBlock(selected)
  │               └─ guardTokenBudget(block, selected)   ← ≤200 token cap
  │     NO  → ThinkToolBlock() → v1 or v0 per V1 flag
  │
  └─ assembled system prompt
```

### Integration point

`ContextClient.HintDispatcher` (field on `internal/chat.ContextClient`).
Set this field in the service container wiring when v2 is active. The
production wiring is NOT yet done — the Container's ChatService would inject
a `dispatchSpawner`-backed `HintDispatcher` implementation using the
`hint-selector` agent slug. This is flagged as a follow-up (see below).

---

## Token Budget

Same 200-token cap as v1 (measured via `EstimateTokens` = chars/4).

`guardTokenBudget` truncates by removing hints from the tail of the selected
list (peer's ranking preserved — highest-priority hints survive truncation).
If even a single hint exceeds the budget, the function falls back to v1.

---

## Limitations and Follow-ups

- **Production wiring not landed:** `ContextClient.HintDispatcher` is exposed
  but the service `Container` does not yet inject a real `HintDispatcher`
  implementation. A thin adapter wrapping `dispatch.Spawner` with the
  `hint-selector` slug is the next step.
- **DB-seeded catalog:** v1 uses embedded YAML (code-authored). Editability
  without redeploy requires DB-seeded catalog rows — filed as a follow-up
  (pre-launch latitude per Phase 6 boot decisions).
- **Eval harness (`7.1`):** No A/B comparison of v1 vs v2 quality is included.
  The fallback path keeps v1 the default until eval validates v2 improvement.
- **ScopeTier/reflex signal not yet plumbed:** `ContextClient.AssembleContext`
  does not yet receive the live ScopeTier or ReflexMatchID for the current
  turn (those are computed later in the dispatch pipeline). For now the
  `HintSelectOpts` carries empty strings for those fields when called from
  the slot-sources path; the peer agent's system-prompt rules cover the
  common cases without them.
- **One-file YAML catalog:** All hints live in one file. Multi-file splitting
  (one-per-hint or one-per-theme) is supported by the loader but unused.
