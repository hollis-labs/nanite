# Phase C — Cortex Type/View Registry Audit

> Investigation date: 2026-04-04
> Scope: Understand what Cortex provides today, identify gaps for Nanite's MemoryService

---

## 1. Current Cortex Integration

### How Nanite Queries Cortex Today

Nanite's `internal/contextbroker/` package provides a multi-source context assembly system. Cortex is one of five sources:

| Source | File | Role |
|--------|------|------|
| **CortexSource** | `source_cortex.go` | Queries Cortex via MCP tools |
| **PCCSource** | `source_pcc.go` | Reads local `.agentrc/pcc/` files |
| **SessionSource** | `source_session.go` | Recent chat history (max 50 msgs) |
| **EngineSource** | `source_engine.go` | Tasks/sprints from Engine |
| **HadronBlueprintGate** | `gate_hadron_blueprints.go` | Session-start blueprint cache |

**CortexSource uses a two-stage fallback** (`source_cortex.go:54-98`):
1. **Primary:** `mcp__vanta__context_broker_fetch` — structured intent-based retrieval
2. **Fallback:** `mcp__vanta__context_search` — keyword search (currently broken: no embedding provider)

**Integration point:** `internal/chat/context_client.go:303-348` — `enrichWithContextBroker()` is called during system prompt assembly before every chat turn.

**Intent classification** (`context_client.go:350-376`): User messages are classified into 7 intent types via keyword matching, then mapped to Cortex's 4 native types. `write_code`, `debug_issue`, `plan_feature`, `recall_decision` all collapse to "custom" — lossy mapping.

**Token budget:** Default 50k tokens, split by intent-specific weights:
- Cortex typically gets 30-40% (15-20k tokens)
- Budget calculated per-source at Fetch time (`broker.go:186-223`)

**No explicit Cortex config in main.go** — CortexSource relies on MCP auto-discovery. No pre-flight validation, no auth config, graceful degradation on failure.

---

## 2. Existing Types, Views, Namespaces

### Registered Content Types (15 total)

| Type ID | TTL | Required Fields | Rank Bias | Notes |
|---------|-----|-----------------|-----------|-------|
| `brief/summary` | 2160h (90d) | — | 0.8 | Ephemeral summaries |
| `config/service` | — | — | 1.0 | Service configuration |
| `contract/api` | — | — | 1.1 | API contracts |
| `contract/data` | — | — | 1.1 | Data contracts |
| `decision/adr` | — | — | 1.4 | Architecture decisions (requires human approval for promotion) |
| `note/volatile` | 336h (14d) | — | 0.5 | Short-lived notes (draft only) |
| `principles` | — | — | 1.5 | Highest rank bias |
| `project/identity` | — | name | 1.2 | Project metadata |
| `runbook` | — | — | 0.9 | Operational runbooks |
| `session/snapshot` | 720h (30d) | summary | 0.7 | Session snapshots |
| `strategy/constraints` | — | — | 1.1 | Strategic constraints |
| `strategy/goal` | — | — | 1.2 | Strategic goals |
| `strategy/roadmap` | — | — | 1.0 | Roadmaps |
| `system/map` | — | — | 1.3 | System architecture maps |
| `task/spec` | — | title | 1.0 | Task specifications |

**All types** support status progression: `draft → reviewed → canonical → deprecated` (except `note/volatile` which is `draft` only).

### Registered Namespaces (8 total)

| Namespace | Owner Type | Owner ID |
|-----------|------------|----------|
| `app/cortex` | app | cortex |
| `app/hadron` | app | hadron |
| `app/mentat` | app | mentat |
| `app/nanite` | app | nanite |
| `app/sigil` | app | sigil |
| `app/volon` | app | volon |
| `user/memory` | user | chrispian |
| `user/pins` | user | chrispian |

**Notable:** `user/memory` already exists (owner: chrispian). No policies on any namespace. `app/nanite` is registered but currently **empty** (no records in any view).

### Registered Views (4 total)

| View ID | Types Included | Max Items | Purpose |
|---------|---------------|-----------|---------|
| `agent_boot` | system/map, principles, strategy/constraints, contract/api, contract/data | 20 | Agent startup context |
| `briefing` | brief/summary, decision/adr, system/map, strategy/goal | 25 | Project briefings |
| `strategy` | strategy/goal, strategy/constraints, strategy/roadmap, decision/adr, system/map | 30 | Strategic planning |
| `task_exec` | task/spec, contract/api, contract/data, decision/adr, runbook, system/map | 50 | Task execution context |

**No memory-oriented view exists.**

### Cortex Limitations Discovered

1. **Semantic search unavailable** — `context_search` and `context_rag_query` both return `embedding_unavailable`. This blocks the primary retrieval path for memory recall.
2. **No memory content type** — None of the 15 types are designed for persistent user/project memories.
3. **No memory view** — No view filters for memory-type content.
4. **Namespace policies are null** — No access control, retention, or quota policies on any namespace.

---

## 3. Required Changes for Memory Support

### 3a. New Content Types Needed

**Option A: Single `memory` type with subtype field** (recommended)

```
type_id: "memory"
default_ttl: null (persistent)
allowed_statuses: [draft, reviewed, canonical, deprecated]
required_fields: [subtype, summary]
retrieval_rank_bias: 1.0
```

Subtypes stored in record metadata:
- `user` — User role, preferences, knowledge level
- `feedback` — Corrections and confirmed approaches
- `project` — Ongoing work, goals, incidents
- `reference` — Pointers to external resources

**Why single type:** Subtypes share the same lifecycle (create, recall, update, deprecate). A single type simplifies view definitions, search queries, and the contextbroker. Subtype filtering happens via metadata field, not type hierarchy.

**Option B: Four separate types** (`memory/user`, `memory/feedback`, `memory/project`, `memory/reference`)

Pros: Fine-grained TTL per subtype (e.g., `memory/project` could have 720h TTL since project state decays fast). Fine-grained rank bias (feedback > project > reference > user).

Cons: 4x view/query complexity. Cortex type registry grows. Overkill unless retrieval ranking truly differs per subtype.

**Recommendation: Option A** — start with single type, split later if retrieval quality demands it.

### 3b. New View Needed

```
view_id: "memory_recall"
types: ["memory"]
max_items: 30
rank_weights:
  canonical: 1.0
  reviewed: 0.9
  draft: 0.6
  deprecated: 0.1
```

This view returns memories ranked by status. Deprecated memories still visible (low weight) for conflict detection.

### 3c. New Namespaces Needed

See Section 4 for full namespace strategy. At minimum:
- `user/{user_id}/memory` — per-user memories (cross-project)
- `project/{project_id}/memory` — per-project memories

### 3d. Embedding Provider Required

Cortex's `context_search` and `context_rag_query` are non-functional without an embedding provider. **This is a blocker for semantic memory retrieval.** Options:
1. Configure Cortex with an embedding provider (Anthropic, OpenAI, or local)
2. Build keyword-based retrieval in Nanite as a fallback (subtype + tag filtering via `context_typed_view`)
3. Both — keyword for guaranteed retrieval, semantic for quality

---

## 4. Namespace Strategy Recommendation

### Proposed Hierarchy

```
user/{user_id}/memory          — Personal memories (cross-project)
  └── user preferences, feedback, general knowledge

project/{project_id}/memory    — Project-scoped memories
  └── project decisions, architecture, team conventions

session/{session_id}/memory    — Session-extracted memories (staging)
  └── Raw extractions before promotion to user/project scope
```

### Why This Structure

1. **User memories are global** — "User prefers terse output" applies everywhere. These live in `user/{id}/memory` and are queried regardless of active project.

2. **Project memories are scoped** — "We chose SQLite over Postgres for this service" is project-specific. These live in `project/{id}/memory` and are queried when that project is active.

3. **Session memories are staging** — Raw extractions from PostCompact and per-turn hooks land in `session/{id}/memory` first, then get promoted to user or project scope after deduplication and conflict resolution.

### Namespace Registration

Currently only `app/nanite` and `user/memory` exist. New namespaces needed:
- `user/{user_id}/memory` — OR reuse existing `user/memory` (already registered for chrispian)
- `project/{project_id}/memory` — Dynamic, registered on first write per project
- `session/{session_id}/memory` — Dynamic, registered on first extraction per session

**Open question:** Does Cortex support dynamic namespace registration, or must namespaces be pre-registered? The `context_namespace_register` MCP tool exists, suggesting dynamic registration is supported.

### Existing `user/memory` Namespace

The `user/memory` namespace already exists (owner: chrispian). **Decision needed:**
- Reuse it for Nanite's user-scoped memories? (simple, but mixes Claude Code memories with Nanite memories)
- Create `user/chrispian/nanite-memory` to isolate Nanite's memories? (cleaner separation)
- Use `app/nanite/user/{id}` to keep all Nanite data under `app/nanite`? (app-centric ownership)

**Recommendation:** Use `app/nanite` as the root namespace, with sub-paths for scope:
- `app/nanite/user/{user_id}` — User memories
- `app/nanite/project/{project_id}` — Project memories
- `app/nanite/session/{session_id}` — Session staging

This keeps Nanite's data isolated under its own namespace, avoids collision with `user/memory` (which may be used by other tools), and gives Nanite ownership of its own memory data.

---

## 5. Memory Extraction Design

### Extraction Triggers

**PostCompact (batch extraction):**
- Trigger: Session compaction event (`session.compacted` or equivalent)
- Input: The compacted summary + original messages being compacted
- Extract: Key decisions, user corrections, architecture choices, project facts
- Write to: `app/nanite/session/{session_id}` as draft memories
- Priority: High — this is the richest extraction point

**Per-Turn (lightweight extraction):**
- Trigger: After each assistant response, before the next user turn
- Signals that warrant extraction:
  - User says "remember this" / "don't forget" → immediate explicit memory
  - User corrects approach ("no, not that" / "don't do X") → feedback memory
  - User states preference ("I prefer..." / "always use...") → user memory
  - Architecture decision made → project memory
  - External resource referenced ("check Linear project X") → reference memory
- Write to: `app/nanite/session/{session_id}` as draft, auto-promote obvious ones
- Priority: Medium — cheap per-turn, high signal for corrections/preferences

**Session End (promotion):**
- Trigger: Session archived or explicitly ended
- Process: Review all `session/{id}` memories, deduplicate against existing user/project memories, promote survivors
- Write to: `app/nanite/user/{id}` or `app/nanite/project/{id}`
- Priority: Medium — cleanup and consolidation

### Extraction Implementation

```
MemoryExtractor interface {
    ExtractFromCompaction(ctx, originalMessages, summary) ([]Memory, error)
    ExtractFromTurn(ctx, userMessage, assistantResponse) ([]Memory, error)
    PromoteSessionMemories(ctx, sessionID) error
}
```

**Extraction strategy:** Use the LLM itself (via Nanite's utility model) with a structured prompt:
1. Present the conversation segment
2. Ask: "What facts, preferences, decisions, or corrections should be remembered for future sessions?"
3. Require structured output: `{subtype, summary, content, confidence, scope}`
4. Filter by confidence threshold (e.g., > 0.7)

### Deduplication and Conflict Resolution

**Duplicate detection:**
- Before writing, query existing memories in the target namespace with keyword overlap
- If a memory with >80% content similarity exists, update it instead of creating a new one
- Use Cortex `context_search` when embeddings are available; fall back to keyword matching

**Conflict resolution:**
- Newer memories override older ones for the same topic (e.g., "user prefers verbose output" overrides "user prefers terse output")
- Conflicting memories: deprecate the older one, create the new one
- Never delete — deprecate. Keeps audit trail.

**Staleness:**
- `memory/project` records should have a TTL or review cadence (project state changes fast)
- `memory/user` and `memory/feedback` are long-lived (no TTL)
- `memory/reference` should be periodically validated (links may break)

---

## 6. Open Questions

### Cortex Capabilities

1. **Embedding provider:** When will Cortex have embeddings configured? This blocks semantic memory retrieval. Is there a timeline or is Nanite responsible for configuring this?

2. **Dynamic namespace registration:** Can Nanite call `context_namespace_register` at runtime to create `app/nanite/project/{id}` namespaces on the fly? Or must they be pre-registered?

3. **Sub-namespace queries:** Does `context_typed_view` with `namespaces=app/nanite/*` glob work? The parameter description says "comma-separated namespace globs" — needs verification.

4. **Write-through MCP vs direct API:** Should Nanite write memories via `context_typed_write` MCP tool, or does it need a direct Cortex API client for performance? MCP adds latency per write.

5. **Namespace policies:** Should `app/nanite/session/*` namespaces have auto-expiry policies (e.g., delete session staging data after 30 days)? Cortex supports policies but none are configured today.

### MemoryService Design

6. **Utility model for extraction:** Which model should extract memories? Using the session's active model adds cost to every turn. A cheaper utility model (e.g., Haiku) may suffice for extraction.

7. **Extraction latency:** PostCompact extraction is async (good). Per-turn extraction must not block the response stream. Should it be fire-and-forget, or should the user see "memory saved" confirmations?

8. **Cross-session memory:** When session A saves a memory, should session B (running concurrently) pick it up immediately? Or only on next context assembly?

9. **Memory cap:** How many memories per user/project before quality degrades? Claude Code's file-based system has a 200-line MEMORY.md index limit. What's the right cap for Cortex-backed memories?

10. **Contextbroker changes:** The existing `CortexSource` fetches via `context_broker_fetch` which is intent-driven. Memory retrieval needs a different access pattern — "give me all memories relevant to this project + user" rather than intent-based. Should this be a new source (`MemorySource`) or an extension of `CortexSource`?

### Architecture Decisions Needed

11. **MemorySource vs CortexSource extension:** Adding memory retrieval to CortexSource keeps one source but complicates its contract. A separate `MemorySource` in the contextbroker is cleaner — it queries `app/nanite/user/{id}` + `app/nanite/project/{id}` namespaces directly, with its own budget allocation.

12. **Budget allocation for memories:** What percentage of the 50k context budget should memories get? Suggestion: 10-15% (5-7.5k tokens) — memories are high-signal, low-volume. This means reducing another source's share (likely PCC, since memories subsume some of what PCC provides).

13. **Memory format:** Should memories be stored as structured JSON (machine-readable, queryable) or markdown (human-readable, matches Claude Code's format)? Cortex payload is freeform text — markdown is natural. Metadata fields handle structure.
