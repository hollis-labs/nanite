# Memory & Knowledge Tools

An inventory of Nanite's native memory/knowledge/recall surface — self-tools plus the storage and retrieval systems behind them. Produced by an operator-directed research session (2026-08-20): **research only, no code or schema changed.**

**Scope.** Native tools first-party to Nanite (`nanite_*`-namespace self-tools, now living in `internal/selftools` — moved from `internal/mcp` during the harness-reactive-self-tools build, see [11](11-harness-reactive-self-tools.md)) and the embedded Tesseract library, plus the storage/recall systems underneath them. Deliberately **excludes**:

- MCP-origin memory/knowledge tools reachable only through MCP client registration (`mcp__mux__memory_*`, `mcp__mux__knowledge_*`, `mcp__mux__tesseract_lookup`, `mcp__mux__context_*`). Nanite embeds Tesseract as a Go library directly (see §5) — those MCP tools are a separate, externally-reachable surface over the same underlying Conduit/Vanta system, out of scope here.
- Generic file/bash exploration tools — not purpose-built for memory/knowledge.
- Inter-agent messaging (`message_send`/`_inbox`/`_thread`/`_ack`/`_resolve`/`_catch_up`) — coordination, not memory; see [Inter-Agent Messaging](07-inter-agent-messaging.md).
- Self-tool introspection (`whoami`, `tool_describe`, `tool_list`, `tool_validate`) and `agent_schedules` — neither stores or recalls domain knowledge.

## Taxonomy at a glance

| Category | Tools | Backend | Durable? |
|---|---|---|---|
| Ephemeral working memory | `scratchpad_*`, `todo_*`, `plan_*`, `context_pin`/`_unpin` | in-memory / `todos` / `plans` / `pinned_content` | mixed — scratchpad no, rest yes |
| Session-boundary continuity | `handoff_request`/`_approve`/`_reject`, `handoff_stash`, `handoff_pointers_expand` | `session_handoffs` / `handoff_stashes` | yes (routing is transient; stash content is durable) |
| Durable per-agent state | `procedure_get`, `reminder_set` | `agent_procedures`, `reminders` (migration `069_per_agent_state.sql`) | yes |
| Skills catalog | `skill_create`/`_list`/`_update`/`_delete` | `skills` | yes |
| Embedded long-term memory | `lesson_capture`, plus automatic extraction/recall (no tool call) | Tesseract (`github.com/hollis-labs/tesseract`), via `internal/memory.Service` | yes |
| Chat/session search | `chat_search`, `chat_get` | `sessions`/`messages` (SQLite) | yes (reads existing history, doesn't write memory) |

## 1. Ephemeral working memory (turn/session-scoped)

| Tool | File:line | Backend | Scope |
|---|---|---|---|
| `scratchpad_write` / `_read` / `_clear` | `internal/selftools/self_tools.go:810,849,874` → `internal/service/chat_scratchpad.go:72,88,98` | in-memory `map[string]any` on `loopState` — no DB | current turn only |
| `todo_create` / `_update` / `_list` | `internal/selftools/self_tools.go:318-380` | SQLite `todos` (`internal/store/todos.go`) | turn / session / project |
| `plan_create` / `_update` / `_step_add` / `_list` / `_get` / `_delete` | `internal/selftools/self_tools.go:381-490` | SQLite `plans` (`internal/store/plans.go`), steps as JSON | workspace / project / session |
| `context_pin` / `_unpin` | `internal/selftools/self_tools.go:1113-1165` | in-memory (turn) or SQLite `pinned_content` (`internal/store/pinned_content.go`) (session/project) | turn / session / project |

**Scratchpad is explicitly not memory.** Its own tool description tells the calling agent so directly — long-term memory is Vanta's job (§5). Per `GLOSSARY.md`: *"Its value is the act of an agent externalizing its reasoning, not the content persisting afterward — being usually-empty is expected, not a defect. Not a continuity mechanism (that's Glass-4's job)."* It's the one tool in this whole inventory that is pure in-process state — a Go map, no DB row, no MCP transport.

Todos and plans are **independent, not nested** — a todo is not a plan-step; the tool descriptions steer callers toward one or the other rather than modeling one in terms of the other. These three (plus context-pin) are the DB-durable, UI-surfaced (todo-list card, plan-review envelope, bottom-drawer Pins tab) native analog of what Claude Code's own TodoWrite/plan-mode do ephemerally per CLI process.

Pinned content is re-surfaced into context by `internal/chat/context_client.go:356` (`ListPinnedContent` → appended under a "## Pinned Context" header inside the `SlotUserContext` budget); turn-scoped pins bypass that path and are injected directly by the reminder engine instead.

**Naming inconsistency worth flagging:** scope vocabulary differs across these three tools — todos use `turn/session/project`, plans use `workspace/project/session` (no turn tier), pins use `turn/session/project`. Not a bug, but worth normalizing if these get touched again.

**Open design question (operator, 2026-08-20): todo/plan have two distinct use cases the current tool surface doesn't distinguish.** (1) An agent's own internal working scratch for sequencing how it executes a request — the same job Claude Code's own TodoWrite/plan-mode do. (2) A plan/todo list authored *for the user* — a shared artifact the user and agent work through together, surfaced as a first-class UI object (the plan-review envelope, the todo-list card), not just internal bookkeeping. Today `plan_create`/`todo_create` serve both without a field distinguishing them. Worth its own dedicated session — noted here so it's not lost, not resolved.

## 2. Continuity across session boundaries and compaction

Two mechanisms share the word "handoff" but do unrelated jobs — a naming collision `GLOSSARY.md` doesn't yet flag (see "Glossary updates" below).

### (A) Cross-agent session handoff — routing, not memory

| Tool | File:line | Backend | Purpose |
|---|---|---|---|
| `handoff_request` | `internal/selftools/self_tools.go:641` | `session_handoffs` + `session_agents` | Request transfer of a session's primary agent |
| `handoff_approve` | `internal/selftools/self_tools.go:658` | `session_handoffs` + `session_agents` | Atomically rebind the session's primary agent |
| `handoff_reject` | `internal/selftools/self_tools.go:672` | `session_handoffs` | Cancel a pending handoff |

Lives in the messaging subsystem (see `docs/messaging.md`, "Handoff operations"), against `*sql.DB` directly since it spans multiple tables. Carries no memory content of its own — purely "who is bound to this session now."

**Open question (operator, 2026-08-20): this mechanism's actual usage isn't clearly remembered and needs investigation before deciding whether the name is right.** Working hypothesis to check: it's an orchestration/delegation primitive — possibly related to agent forking or handing a session off to a more specialized agent mid-conversation — rather than a "memory" concept at all. If that's confirmed, the fix may be purely nominal (rename away from "handoff" to something like `session_transfer_*` so it stops colliding with Glass-4 handoff in an agent's mental model) rather than any behavior change. Needs a dedicated look at real call sites/usage before either the name or the doc framing here is finalized — not resolved by this research pass.

### (B) Glass-4 handoff — the actual memory-preservation mechanism

| Tool | File:line | Backend | Purpose |
|---|---|---|---|
| `handoff_stash` | `internal/selftools/self_tools_handoff.go:24` | `handoff_stashes` (Glass-4 JSON envelope) | Agent self-authors a compaction-survival payload: `session_intent`, `next_step_anchor`, ≤3 `recent_decisions`, ≤5 `active_pointers` — hard-capped ~1500 tokens, enforced by `ValidateHandoff` |
| `handoff_pointers_expand` | `internal/selftools/self_tools_handoff.go:78` | `handoff_stashes` | Retrieve a full stashed payload by `cache_key` |

Already defined in `GLOSSARY.md`: *"the agent-self-authored continuity snapshot ... written before compaction and re-injected after."* Its defining behavior: `internal/context/compaction.go:192-209` — when a handoff stash exists for a session, compaction's own `stageSummarizeOldest` step is **skipped entirely**; the self-authored handoff substitutes for LLM-generated summarization and is rendered via `RenderHandoffForSlot` as the first thing the post-compaction agent reads. Framed by its own doc comment: *"compaction is amnesia; with handoff, it's sleep."*

**Origin confirmed (operator, 2026-08-20):** this formalizes and automates a pre-existing manual operator practice — when compaction is approaching, an operator working directly with an agent has it write a "handoff doc" noting where things stand and what to resume, then re-reads it after compaction to pick back up. `handoff_stash`/`handoff_pointers_expand` is that same practice, done by the agent for itself, automatically. This is *the* well-named half of the two handoff mechanisms — no ambiguity about its purpose, only about sharing a name with (A) above.

### A third, unrelated "stash"

`internal/contextbroker/stash.go` (INV5 in `internal/context/INVARIANTS.md`) is a generic mechanism unrelated to either handoff above: **any** context-broker slot that exceeds its token budget gets content-addressed and swapped for a deterministic `<ref:artifact_id=...>` pointer (retrieved via `dev_read`), purely for prompt-cache-prefix stability — same input must always yield the same pointer ID. Shares no code path with Glass-4 handoff, despite the similar "stash → pointer → expand" shape.

## 3. Durable per-agent state

Backed by the `AgentStateStore` interface (`internal/store/agent_state_store.go`) — an explicit seam, not yet a literal per-agent SQLite file. Today `*Store` satisfies it against the **central SQLite DB, sharded by `agent_id`**; the file's own comment anticipates "a future per-agent-file backend (one SQLite file per agent, or per-agent JSON files on disk)" implementing the same interface later without touching call sites. **This is the direct answer to "is there a dedicated per-agent sqlite database": not yet — there's an interface designed for that shape, currently backed by shared central tables.**

Five tables land in one migration, `internal/store/migrations/069_per_agent_state.sql`:

```sql
agent_known_tools    (agent_id, tool_name, pinned, activation_count, last_used_at, added_at, ttl_seconds, reason)   PK(agent_id, tool_name)
agent_known_skills    (agent_id, skill_name, pinned, activation_count, last_used_at, added_at, ttl_seconds, reason)  PK(agent_id, skill_name)
agent_procedures      (agent_id, name, body, scope DEFAULT 'agent', created_at, updated_at)                         PK(agent_id, name)
agent_log             (id, agent_id, session_id, ts, kind, entry)
agent_knowledge_seed  (agent_id, seed_key, namespace, body, tags_json, applied_at, created_at)                      PK(agent_id, seed_key)
```

All FK to `agent_profiles(id)`, all idempotent `CREATE TABLE IF NOT EXISTS` (no `schema_migrations` ledger yet — every migration re-runs every boot), no real Down migration (predates the goose ledger cutover — see [Storage & Migrations](05-storage-and-migrations.md)).

| Table | Tool(s) | Read/Write path | Status |
|---|---|---|---|
| `agent_procedures` | `procedure_get` (`internal/selftools/self_tools_procedure.go:25`) | **Read-only via self-tool.** Written by `seedProcedures` (`internal/service/ingest.go:321,381`) parsing an agent profile's `procedures:` YAML frontmatter at ingest time, or via the admin REST API (`internal/api/agent_capabilities.go:387,436`). No self-tool lets an agent write its own procedures — deliberately operator/profile-authored, agent-read-only. See §4a for the open boundary question against skills and workflows, and §4b for whether this content belongs in Tesseract instead. |
| `agent_knowledge_seed` | none directly | Manifest of content queued to be written into Tesseract on an agent's first activation. Consumer wiring is flagged in its own doc comment as landing in a later follow-up ("FU-7f") — not confirmed fully wired end-to-end by this research. |
| `agent_known_tools` / `agent_known_skills` | none directly | Per-agent roster + activation telemetry — closer to preference/usage tracking than knowledge storage. Noted here for completeness (they're in the same migration/interface) rather than as core "memory" tools. |
| `agent_log` | none | **Dead — recommended for removal.** `AppendLog`/`ListLog` exist on `*Store` and on the `AgentStateStore` interface; confirmed by direct repo-wide grep that `AppendLog(` has exactly two hits — the method definition and the interface declaration, zero call sites, zero callers of `ListLog` either. The migration's own comment describing it as holding "passes, lessons, etc." documents original intent that was never wired up, not current behavior. Operator call (2026-08-20): remove the table, interface methods, and Go struct; nothing currently depends on it, and it can be re-added if a real need shows up. Not removed in this research session — recorded here as the decision, execution is a follow-up. |

`reminder_set` (`internal/selftools/self_tools_reminders_pins.go:19`) → SQLite `reminders` table, `turn`/`session`/`project` scope, fires on a time or turn-count trigger. Not one of the five migration-069 tables but the same durable-state category.

## 4. Skills — procedural knowledge

`skill_create` / `_list` / `_update` / `_delete` (`internal/selftools/self_tools.go:57-121`) operate on **one unified `skills` DB table** (`internal/store/skills.go`), not two parallel systems. Each row carries a `Source` field (`builtin` / `user` / `project` / `plugin` / `claude`), classified by `internal/store/skills_source.go`'s `ClassifySkillSource` — the `~/.nanite/skills/` filesystem-drop convention CLAUDE.md describes gets ingested into this same table under `user`/`project` sources, alongside builtin and plugin-registered rows. `skill_delete` refuses to delete `builtin` rows. (`internal/toolclient/tool_knowledge.go` is unrelated — a small static, curated tool-description catalog used for keyword-matching tool suggestions to the LLM, not a memory/skills store.)

A dedicated deep-dive on skills themselves — the tool/usage surface, how it's framed to agents, and specifically how it relates to procedures and workflows below — is planned as its own future session. Not undertaken here. §4a's "Resolution" subsection records two concrete follow-ups already filed for that session: skill composability (skills referencing/including other skills, parameterization) and explicit skill triggering (vs. today's implicit description-matching).

## 4a. Procedures vs. Skills vs. Workflows — three systems, boundary not yet drawn

Operator, 2026-08-20: all three are likely valid distinct concepts, but the boundary between them isn't documented anywhere, and the tooling doesn't obviously enforce or reflect one. Grounding what each *currently is*, structurally, so a future session can draw the line deliberately rather than by accident:

| | Procedures | Skills | Workflows |
|---|---|---|---|
| Storage | `agent_procedures` (SQLite, per-agent) | `skills` (SQLite, shared catalog) | YAML files (`internal/agentworkflow/definition_yaml.go`) + `workflow_runs`/step tables for run-state |
| Shape | Named free-text body | Tool-name bindings under a category | Typed DAG: steps of kind `llm` / `tool` / `gate`, with dependencies and optional `verify` |
| Who authors it | Operator, via agent-profile `procedures:` frontmatter or admin API — never the agent | Operator/plugin/filesystem-drop; agent can CRUD via self-tools | Operator, as a YAML definition file |
| How it's consumed | Agent reads the body via `procedure_get` and follows it itself (prose, agent-interpreted) | Agent's tool roster is shaped by which skills are attached | The engine executes it deterministically — `workflow_run` self-tool — steps run whether or not the model would have chosen to |
| Execution model | None — it's read-only reference text | None — it's a grouping/roster mechanism | Real: `agentworkflow.WorkflowEngine` sequences; `StepExecutor` (in `internal/service`) does the actual LLM/tool/verify work; state persists per-step for crash/resume |

The structural distinction that already exists: **procedures and skills are both passive reference material an agent reads or is scoped by; workflows are an active thing the harness runs on the agent's behalf**, with real crash-resumable run state. But that doesn't resolve the actual tension — a "procedure" today is exactly the kind of multi-step prose a workflow YAML could also encode, just interpreted by the model instead of executed deterministically by the engine, and it's not documented anywhere *when* a piece of process knowledge should be one vs. the other.

### Resolution (operator, 2026-08-20)

**Workflows are settled** — deterministic, harness-executed, for processes that must happen in a specific order/timing. No further tension with the other two; this axis (does the model retain discretion, or does the harness enforce sequencing) is independent of the Procedures/Skills question below.

**Skills and Procedures stay two distinct primitives — not collapsed.** They answer different questions along a *generality* axis: a Skill is a general capability/reference an agent can draw on regardless of its specific job (matches the industry-standard "Agent Skills" meaning, deliberately kept intact rather than fought); a Procedure is role/job-specific standard operating knowledge for a scenario that role is known to encounter (validated directly by the code: `procedure_get`'s own doc comment records that `project-manager.md` and `system-architect.md` referenced it in their boot/checklist instructions before the tool existed — procedures grew out of specific role profiles needing their own playbooks, exactly matching the original intent).

**Real gap found and confirmed by reading the code, not just inferred: Procedures lack the reuse mechanism Skills already have.** Skills have a genuine catalog+attachment split — one global `skills` row, referenced by any number of agents via `agent_known_skills` (with its own pinning/activation telemetry). Procedures don't: `agent_procedures`' primary key is `(agent_id, name)`, the body text is owned directly per agent, and while the `scope` column documents `'shared'` as a valid value, nothing consumes it that way — `ListAgentProcedures`/`GetAgentProcedure` both hard-filter `WHERE agent_id = ?`, and `procedure_get`'s own tool description states outright *"the returned procedure is always the one seeded for YOUR OWN profile, not another agent's."* Three Torque-PM agent instances across three project scopes today each need "what to do when a sprint slips" seeded three separate times, with no way to author it once.

**Follow-up filed: give Procedures the same catalog+attachment shape Skills already have.** Sketch, not a committed design — a shared procedure-body catalog (analogous to `skills`) plus a per-agent attachment table (analogous to `agent_known_skills`), so the same procedure content can be authored once and attached to every `agent_profiles` row that needs it. Binds at the `agent_profiles` level, matching the existing precedent that capability grants (tools/skills/permissions) bind at the Agent layer, not the Role layer — Roles stay seed-hints only, not authoritative live grants (see `Role`'s doc comment, `internal/store/roles.go`). Not implemented in this session.

**Playbooks deliberately not built.** The instinct behind the name (and the older, pre-Agent-Workflow "workflows" concept it's meant to replace — common user/agent interaction *patterns*) is real, but a fourth primitive isn't justified yet: a Procedure body can already say "use skill X, and if this needs strict ordering kick off workflow Y" in prose, composed by agent judgment. Defer building a formal Playbook (a named, structured index over skills/procedures/workflows) until there's a concrete case prose-composition can't serve — e.g. needing it surfaced in a UI picker or matched programmatically rather than read by an agent. Held as a name, not a missing table.

### Follow-ups filed against Skills specifically (operator, 2026-08-20)

Two more gaps surfaced discussing the above, both scoped to the deferred skills deep-dive (§4), not resolved here:

- **Skill composability.** Explore letting a skill reference/include other skills, and parameterize itself (variables, passed-in task IDs, etc.) rather than being a flat, self-contained unit — the same shape Claude Code's own skill system and other agentic frameworks support. Today's `skills` table has no representation for this at all.
- **Explicit skill triggering.** How does a skill actually get invoked — by the user, by the system, by the harness/reflexes? Grounding for why this matters: the "skill broker" (`internal/skillbroker`, a rule-matching layer) was already cut during the steering consolidation ([Steering](03-steering.md)) — what's left is catalog + permission/allowlist filtering (which skills an agent can even see), not a live "trigger this skill because of what's happening" mechanism. Claude Code's own approach — matching the skill's free-text `description` against conversational intent — is implicit and description-quality-dependent. Operator wants Nanite to support that *kind* of triggering (user-invoked, system-invoked, and reflex/harness-invoked) as an explicit, structured feature — not by hoping a description string is descriptive enough. Natural next step once reflexes' `dispatch_to_agent`-style predicate matching (already a real, working pattern — [Reflex Action Taxonomy](10-reflex-action-taxonomy.md)) is looked at as a possible reusable mechanism for skill triggering too, rather than inventing a second matching system from scratch.

## 4b. Where should this content actually live: SQLite or Tesseract?

A second, related tension the operator flagged: `agent_procedures` bodies are SQLite rows today, but Tesseract (§5) is explicitly designed to hold knowledge and memory. Worth evaluating in the same future session as the procedures/skills/workflows boundary: should procedure bodies (and other durable agent/domain-specific knowledge currently living in plain SQLite tables) actually live in Tesseract instead, where they'd get Tesseract's ranking/confidence/promotion machinery for free? Not decided — this doc only names the question.

## 5. Embedded Tesseract (Vanta) memory

`github.com/hollis-labs/tesseract` (go.mod) is a genuinely **embedded Go library**, not an MCP server — distinct from the `mcp__mux__memory_*`/`tesseract_lookup` MCP tools, which are a separate externally-reachable surface over the same underlying system. "Vanta" is the product name layered over Tesseract used throughout docs/config/role prompts.

**`internal/memory.Service`** (`internal/memory/service.go:50-238`) is the native Go client: `Store`/`Recall`/`Get`/`Promote`/`Deprecate` over Tesseract's `memory.Store`, using Conduit's 3-tier namespace model (`user/{u}/memory`, `.../project/{p}/memory`, `.../session/{s}/memory`). Wired at boot: `internal/service/container.go:658,1276-1285` — `memorySvc = memory.NewService(conduitInstance.MemoryStore())`.

**Writes — two automatic, one explicit:**

| Path | File:line | Trigger |
|---|---|---|
| Per-message extraction | `internal/memory/extraction.go:79-123` | Plugin event `message.received`; regex-gated ("remember", "i prefer", "actually", …) then a cheap utility-LLM call extracts a structured memory, session-scoped |
| Post-compaction extraction | `internal/memory/extraction.go:125-158` | Plugin event `context.compacted`; richer LLM extraction pass over the compacted segment (decisions/preferences/corrections/facts) |
| `lesson_capture` (explicit self-tool) | `internal/selftools/self_tools_remember.go:25,149` → `internal/learnings.Recorder.Capture` | Agent-initiated; **not** `agent_log` — genuinely agent-facing durable memory, own namespacing convention (`tool_use`/`project`/`session` scope, confidence 0.85, status `draft`) layered on `memory.Service` |

**Reads — two independent auto-recall paths, plus the context broker's non-Tesseract sources:**

| Source | File:line | Backend | Trigger |
|---|---|---|---|
| `contextbroker.MemorySource` | `internal/contextbroker/source_memory.go` | `memory.Service.Recall`, cascades session→project→user namespaces | Every turn, unless the agent profile disables `auto_recall` (`internal/chat/auto_recall_settings.go`: per-agent enable/limit/min-confidence/timeout) |
| `internal/learnings.Recaller` (`RecallByToolName`) | `internal/learnings/learnings.go` | `memory.Service.Recall` | Around tool selection — surfaces up to 2 hints before a tool call |
| `contextbroker.ConduitSource` | `internal/contextbroker/source_conduit.go` | MCP `context_broker_fetch`/`context_search` against a "conduit" server | Every turn, as one of the broker's fan-out sources |
| `contextbroker.PCCSource` | `internal/contextbroker/source_pcc.go` | flat files, `.nanite/pcc/global/<project>/*.md` (fixed 6-file set), scored by filename/intent | Every turn |
| `contextbroker.SessionSource` | `internal/contextbroker/source_session.go` | `store.ListMessages` | Every turn — recent conversation window |

`contextbroker.Broker` (`internal/contextbroker/broker.go`) aggregates all of the above, weighting/budgeting/merging them into one token-bounded `ContextPacket`.

**One blurry boundary worth flagging:** `ConduitSource` is native context-broker code, but it reaches Vanta *through* MCP (`context_broker_fetch`/`context_search`) rather than through `internal/memory.Service` directly — an internal implementation detail using MCP as its transport, not an agent-callable MCP tool. It's the one place in this inventory where "native" and "MCP" blur, included here because it's wired into the broker's own source list alongside the fully-native `MemorySource`.

`chat_search`/`chat_get` (§6) are unrelated to all of this — plain SQLite reads, no embeddings, no Tesseract involvement.

**Usage audit deferred (operator, 2026-08-20).** Tesseract/Vanta is a system Nanite built and is confident in structurally, but its *actual* usage and usefulness inside Nanite hasn't been evaluated — development so far has prioritized building and stabilizing the harness itself, not measuring how well auto-recall/extraction/grounding perform once there's real session volume to look at. Planned once more usage data exists, not undertaken here. That evaluation should also settle §4b's open question — whether procedures and other durable agent/domain-specific knowledge belong in Tesseract rather than plain SQLite tables.

## 6. Chat/session search

| Tool | File:line | Backend | Purpose |
|---|---|---|---|
| `chat_search` | `internal/selftools/self_tools_chat_search.go:124-288` | `store.ListMessages` (SQLite `sessions`/`messages`) | Regex/substring search over one chat's messages — current session by default, or any chat by short-code/session-id via `target` |
| `chat_get` | `internal/selftools/self_tools_chat_search.go:305-378` | same | Paginated raw message fetch by short-code/session-id |

Not Tesseract, not FTS, not cross-corpus — a straightforward string/regex read over one session's stored messages.

**Flagged as needing development (operator, 2026-08-20).** The idea behind `chat_search`/`chat_get` is sound but underdeveloped relative to what's actually needed: regex/substring-only, one session at a time. The stated direction is a hybrid retrieval approach — BM25/full-text plus possibly vector/embedding search — over chat history, not a straight replacement of one method with another. Scope isn't uniform, either: durable, long-running agents in particular need to search and reason over their *entire* corpus of past sessions, where a short-lived task agent typically only needs its own current chat's history. Today's `target` parameter lets a caller point at one other chat by short-code/session-id, but there's no cross-corpus or ranked-relevance search of the kind durable-agent continuity would need. This area needs real design work, not captured further here.

## Open findings worth carrying forward

- **`agent_log` is dead code** — operator decision (2026-08-20): remove it. See §3's table entry for detail; execution is a follow-up, not done in this session. **Filed as `CW-20260820-0001`.**
- **Scope-vocabulary drift** across `todo`/`plan`/`context_pin` (§1) — three tools, three slightly different scope enums for what's conceptually the same tiering. Folded into tension 1's session below.
- **`agent_knowledge_seed`'s consumer wiring** is documented as landing in a follow-up ("FU-7f") in its own source comment — not confirmed end-to-end wired by this research. **Filed as `CW-20260820-0002`.**
- **Two unrelated "handoff" mechanisms and two unrelated "stash" mechanisms** share vocabulary (§2) — real for today's readers, not just a historical footnote; see glossary updates below.

## Tensions & follow-up sessions (round 2 audit, 2026-08-20)

The operator's overall framing: this system is in reasonably good shape structurally — the audit's purpose is to make sure everyone knows where things currently are, and to record where things *should* be, so the same term meaning two things in two places (the "handoff" collision being the clearest example) stops costing confusion. The main live tensions, each scoped as its own future session rather than resolved here. All six below are filed as Torque tasks (project `PRJ-20260417-0002`), same disposition as this project's other standing follow-ups (`CW-20260819-000X`) — `manual=true`, not yet promoted to dispatch-eligible:

1. **Plan/todo dual-use** (§1) — agent-internal working sequence vs. a shared plan/todo authored for the user to work through together. No field distinguishes them today. **Filed as `CW-20260820-0003`** (includes the scope-vocabulary-drift cleanup above).
2. **Cross-agent `handoff_request`/`_approve`/`_reject`'s actual purpose and whether "handoff" is even the right name for it** (§2A) — suspected to be an orchestration/delegation/forking primitive, not confirmed. Needs real usage investigation before a rename or reframing. **Filed as `CW-20260820-0004`.**
3. ~~**Procedures vs. skills vs. workflows boundary**~~ **Resolved** (§4a, 2026-08-20) — Workflows settled (deterministic, unrelated axis); Skills and Procedures stay distinct (generality axis: broad capability vs. role-specific SOP), validated by `procedure_get`'s own history. Playbooks deliberately not built — no case yet that prose composition of the other three can't already serve. One real, confirmed gap carried forward as its own follow-up: **Procedures need the same catalog+attachment reuse shape Skills already have** (today every agent needs its own duplicate copy; no working `scope='shared'` path despite the column existing). **Filed as `CW-20260820-0005`.**
4. **Skills deep-dive** (§4) — now scoped to three concrete threads: (a) the tool/usage surface and framing to agents, (b) **skill composability** — skills referencing/including other skills, parameterization (variables, task IDs), matching how Claude Code and other agentic frameworks do it, (c) **explicit skill triggering** — user/system/harness-reflex invocation as a first-class structured mechanism, not implicit description-keyword matching (the prior `internal/skillbroker` rule-matching layer was already cut during steering consolidation — see [Steering](03-steering.md) — so this isn't reviving that, it's asking whether reflexes' existing predicate-matching pattern can be reused for skill triggering instead of building a second one). **Filed as `CW-20260820-0006`.**
5. **Tesseract usage audit, and whether SQLite-resident knowledge (procedures, etc.) should move into it** (§4b, §5) — deferred until there's enough real session volume to evaluate against. **Filed as `CW-20260820-0007`**, deliberately low priority (4) as a not-yet-actionable placeholder — don't pick up until real session volume exists.
6. **Hybrid chat/session search (BM25/FTS/vector) and corpus scope for durable agents** (§6) — today's tools are regex/substring, single-chat; durable-agent continuity needs whole-corpus, ranked recall. **Filed as `CW-20260820-0008`.**

## Glossary updates

This research surfaced one real naming collision not yet captured in `docs/engineering/GLOSSARY.md` (which already defines **Scratchpad**, **Compaction**, and **Glass-4 handoff**): the `handoff_request`/`_approve`/`_reject` self-tools use "handoff" for cross-agent session routing, a completely different mechanism from Glass-4 handoff's memory preservation. A **Session handoff** entry disambiguating it from **Glass-4 handoff** has been added. If tension 2 above (§2A) concludes a rename is warranted, that entry is the one to update.

## Status

Research-only inventory as of 2026-08-20, extended same-day across two follow-up review passes: (1) operator context added as open-question notes in §1/§2/§4a/§4b/§6 plus the "Tensions & follow-up sessions" roundup; (2) tension #3 (procedures/skills/workflows) resolved in §4a, with two concrete follow-ups filed — the Procedures reuse-mechanism gap, and skill composability + explicit triggering (folded into the deferred skills deep-dive, item 4).

No code or schema changed in either pass. File paths throughout were updated once already, mid-session, after background implementation work (`TASKS/harness-reactive-self-tools/`) moved every self-tool from `internal/mcp` to `internal/selftools` — worth re-checking citations here if further drift is suspected. `agent_log` removal is a recorded decision, not yet executed.

**Round 3 (2026-08-20, same day): every follow-up from this doc filed as a Torque task** (`CW-20260820-0001` through `-0008`, project `PRJ-20260417-0002`) — the two "Open findings" execution/verification items and all six "Tensions & follow-up sessions" items, cross-referenced inline above. None promoted to dispatch-eligible yet (`manual=true`, operator's call on when to run each).
