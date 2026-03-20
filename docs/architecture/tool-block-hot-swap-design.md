# Tool Block Hot-Swap Architecture (Design)

> Written: 2026-03-20 (Session 26)
> Status: PROPOSED — needs ADR before implementation
> Origin: Session 26 hardening sprint revealed tool management is ephemeral and opaque

## Problem

Tools are currently anonymous and ephemeral:
- Rebuilt from scratch every iteration
- No identity, no addressability, no revision tracking
- Can't selectively edit/swap tools mid-conversation
- Tool results are the biggest context consumers but have no compaction policy
- Skills follow the same pattern — injected as text, no structured management

## Proposed: Addressable Context Blocks

Every piece of injected context (tools, skills, tool results, system prompt sections) gets a structured block with an ID, revision, and compaction policy.

### Block Types

```
TOOL_BLOCK (id, rev, tools[])
  - Represents the current tool set for this session
  - Each tool has: name, source (MCP server), loaded_at (iteration), use_count
  - Rev increments on any change (add/remove/swap)
  - Supports operations: add, remove, swap, reload

SKILL_BLOCK (id, rev, skills[])
  - Active skills injected into context
  - Each skill has: name, status (active/available/disabled), injected_at
  - Swap operations: activate, deactivate, replace

RESULT_BLOCK (id, tool_name, iteration, compaction_policy)
  - Wraps a tool call result
  - Compaction policies:
    KEEP_FULL    — preserve entire result (default for 2 turns)
    KEEP_SUMMARY — auto-generated summary (e.g., "47 tasks returned, 12 P1")
    DROP         — remove entirely (stale/superseded)
    KEEP_REF     — pointer to stored version (Cortex or disk)

CONTEXT_BLOCK (id, rev, source, content)
  - System prompt sections, enrichment context, workspace data
  - Can be selectively updated without rebuilding entire prompt
```

### Hot-Swap Operations

```
┌──────────────────────────────────────────────────────┐
│ swap_tools(block_id, remove=["cortex_search"],       │
│            add=["hadron_blueprints_list"])            │
│                                                       │
│ 1. Find TOOL_BLOCK by ID                             │
│ 2. Remove cortex_search from tools array             │
│ 3. Load hadron_blueprints_list from broker            │
│ 4. Increment rev                                      │
│ 5. Next API call uses updated tool set                │
│ 6. Inject change notice into conversation:            │
│    "Tool set updated (rev 4): -cortex_search          │
│     +hadron_blueprints_list"                          │
│ 7. Previous cortex_search results stay in history     │
│    (their RESULT_BLOCKs are independent)              │
└──────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────┐
│ swap_skill(block_id, deactivate="task-triage",       │
│            activate="sprint-review")                  │
│                                                       │
│ 1. Find SKILL_BLOCK by ID                            │
│ 2. Mark task-triage as inactive (remove from context) │
│ 3. Inject sprint-review content into context          │
│ 4. Increment rev                                      │
│ 5. LLM now has sprint-review guidance instead of      │
│    task-triage                                        │
└──────────────────────────────────────────────────────┘
```

### Compaction Control

Tool results are the biggest context consumers. A single `engine_tasks_list` can be 8K tokens. With addressable result blocks, the compaction step (PruneAfterTurn) can make intelligent decisions:

```
Compaction policies by tool type (configurable):

  web_fetch       → KEEP_SUMMARY after 0 turns (HTML is huge, rarely re-read)
                    Summary: "200 OK · text/html · 42KB · title: 'Anthropic Docs'"

  engine_tasks_list → KEEP_FULL for 2 turns, then KEEP_SUMMARY
                      Summary: "47 tasks (12 P1, 23 P2, 12 P3)"

  cortex_search   → KEEP_FULL for 3 turns (small, high value)

  context_write   → DROP after 1 turn (write confirmation, no ongoing value)

  hadron_run      → KEEP_SUMMARY after 1 turn
                    Summary: "Blueprint X executed, status: success, 3 stages"

Custom policies can be set per:
  - Tool name (most specific)
  - MCP server (e.g., all engine tools: KEEP_FULL for 2 turns)
  - Content type (JSON vs HTML vs plain text)
  - Result size (>4K tokens → aggressive compaction)
```

### Relationship to Envelopes

Envelopes are the RENDERING layer — they define how content appears in the UI (cards, reports, error displays). Blocks are the CONTEXT layer — they define how content is managed, addressed, and compacted in the conversation.

```
Tool execution produces BOTH:
  1. A RESULT_BLOCK (context management — what the LLM sees)
  2. An ENVELOPE (UI rendering — what the user sees)

These are independent:
  - Compacting a RESULT_BLOCK to KEEP_SUMMARY doesn't change the envelope
  - The user still sees the full rendered card
  - But the LLM only sees the summary in subsequent turns
```

### Implementation Layers

```
Layer 1: Result Block IDs + compaction policies
  - Tag each tool result with an ID
  - Add compaction policy to PruneAfterTurn()
  - Minimal change, high value (biggest context savings)

Layer 2: Tool Block with swap operations
  - Track tool set as a versioned block
  - Add swap/add/remove operations
  - Expose as builtin tools or API endpoints

Layer 3: Skill Block management
  - Track active skills as a block
  - Hot-swap skills mid-conversation
  - Expose via request_skills meta-tool (like request_tools)

Layer 4: Full context graph
  - All blocks interconnected
  - Dependency tracking (result X came from tool Y in block Z)
  - Visual context inspector in the UI
```

## Stash/Recall Pattern

Beyond hot-swap (replacing tools in the active set), tools and heavy context can be **stashed** — removed from the active context window but kept addressable for instant recall.

```
ACTIVE CONTEXT (what the LLM sees this turn)
  ┌─ TOOL_BLOCK tb-001 rev=5 ───────────────────┐
  │ engine_tasks_list    (active, 200 tokens)     │
  │ engine_task_create   (active, 350 tokens)     │
  │                                    ~550 tokens │
  └───────────────────────────────────────────────┘

STASH (not in context, instant recall)
  ┌─ STASH ────────────────────────────────────────┐
  │ cortex_search         (stashed iter 3, 280 tok)│
  │ hadron_run            (stashed iter 3, 420 tok)│
  │ hadron_blueprints_list(stashed iter 3, 310 tok)│
  │ web_fetch             (stashed iter 1, 180 tok)│
  │                                               │
  │ Also stashable:                                │
  │ • Skill definitions (500-2000 tokens each)     │
  │ • System prompt sections (enrichment, workspace)│
  │ • Large tool results (summaries stay, full stashed)│
  └────────────────────────────────────────────────┘

OPERATIONS:
  stash(tools=["cortex_search", "hadron_run"])
    → Remove from active context
    → Save full definitions to stash
    → Inject notice: "Stashed 2 tools (recall anytime)"
    → Savings: ~700 tokens freed

  recall(tools=["cortex_search"])
    → Pull from stash into active context
    → Available for use immediately
    → Inject notice: "Recalled cortex_search"

  stash_skill(skill="task-triage")
    → Remove skill content from system prompt
    → Keep in stash for recall
    → Savings: 500-2000 tokens

AGENT-DRIVEN STASH/RECALL:
  The agent can manage its own context. Give it builtin tools:

  manage_context(action="stash", targets=["hadron_run", "web_fetch"])
  manage_context(action="recall", targets=["cortex_search"])
  manage_context(action="list_stash")  → shows what's available
  manage_context(action="status")      → active vs stashed summary

  The agent sees a stash manifest in its system prompt:
  "## Stashed (available via manage_context recall):
   cortex_search, hadron_run, hadron_blueprints_list, web_fetch
   task-triage (skill), sprint-review (skill)"

  This lets the agent:
  1. Recognize when it needs a stashed tool
  2. Recall it before use
  3. Stash tools it's done with to free context
  4. Make intelligent decisions about context budget

  Example flow:
    User: "Now let's look at the Hadron blueprints"
    Agent thinks: "I need hadron tools. Let me recall them
                   and stash the engine tools I'm done with."
    Agent calls: manage_context(action="swap",
                   stash=["engine_task_create"],
                   recall=["hadron_blueprints_list", "hadron_run"])
    Agent calls: hadron_blueprints_list()
```

### Why This Matters

Current state: every iteration rebuilds tools from scratch (~15K tokens). The LLM has no awareness of context cost and no ability to optimize its own footprint.

With stash/recall: the agent actively manages a ~4K token active tool set and a stash of everything else. Context savings compound over multi-turn conversations. The agent learns which tools it needs for the current task phase and sheds the rest.

This is **context sovereignty at the agent level** — the agent controls what's in its own context window, not just the platform.

## Nanite Integration Example

A "Send to Nanite" message action demonstrates the envelope → action → external system pattern:

```
Every message in the chat is a StructuredMessage (v=1).
The message chrome (UI around the message) can have actions.

"Send to Nanite" action:
  1. User clicks ⋯ menu on any message → "Send to Nanite"
  2. Frontend extracts: message content, metadata, session context
  3. POST /api/nanite/ingest {
       content: message.text,
       source: "conduit",
       session_id: "...",
       message_id: "...",
       tags: ["captured-from-chat"]
     }
  4. Nanite ingests, indexes, makes available for RAG
  5. Toast: "Sent to Nanite ✓"

This is a message-level action, not an envelope.
Envelopes are for rich embedded content (cards, reports).
Actions are for operations on messages (copy, pin, forward, capture).

The same pattern works for:
  - "Create Task" → POST to Engine
  - "Save to Cortex" → POST to Cortex
  - "Share" → copy link or send to another session
  - "Bookmark" (already exists)
```
