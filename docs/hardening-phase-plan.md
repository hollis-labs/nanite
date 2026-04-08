# Hardening Phase — Task Plan

**Date:** 2026-04-07
**Inputs:** `docs/research/gaps-and-opportunities.md`, `docs/research/nanite-alignment-matrix.md`, session discussion
**Status:** Phase 1 complete, executing

---

## Task 1: God-Object Decomposition — DONE

**Completed:** 2026-04-07

- `engine.go`: 2164 → 197 lines (91% reduction)
- `delegate.go`: 267 → 41 lines (type definitions only)
- All dead code removed — Engine struct, 20+ methods, duplicate constants
- Kept: shared types (`StreamEvent`, `Usage`, `PresenceEvent`), utility functions
- All tests pass, build clean

---

## Task 8: Progressive Disclosure Threshold — DONE

**Completed:** 2026-04-07

- Removed duplicate constant from `engine.go`
- Single source of truth in `service/tool.go`, bumped from 5 → 10
- TODO: make configurable via user_settings (deferred to avoid scope creep)

---

## Task 7: Dead Event Audit

**Priority:** Low | **Effort:** Small

**Events to audit (likely still dead as of 2026-04-07):**
- `message.deleted` — wire when message delete API is called
- `widget.loaded` — **delete** (frontend concern)
- `action.triggered` — **delete** (frontend concern)
- `workflow.started/complete/failed` — **mark planned-v2** (needed once Task 6 ships)
- `config.changed` — **mark planned-v2** (needed for plugin settings UI)
- `plugin.installed/uninstalled` — **mark planned-v2** (needed for plugin management)
- `provider.fallback` — wire when circuit breaker triggers fallback
- `scope.changed` — wire or delete (check if scope changes are tracked)
- `artifact.deleted` — wire when artifact delete API is called
- `api.request/response` — **delete** (middleware logging covers this)

**Acceptance:**
- Each event is wired, marked planned-v2 with a comment, or deleted
- No events defined that will never have subscribers

---

## Bonus: Think Tool

**Priority:** Low | **Effort:** XS

- No-op tool definition: accepts `thought` string, returns empty
- System prompt block explaining when to use it (integrate new info, check completeness)
- Per-agent override in agent YAML for custom completeness checklists

---

## Task 5: Internal Todo & Plan System

**Priority:** High | **Effort:** Medium-Large

**Current state:**
- `internal/task/` exists (832 lines) with `Service`, `LocalBackend`, snapshot support
- Backend APIs exist for CRUD
- No integration with chat loop
- Fragments Engine dependency should be removed from core

**Design — Todos:**
- Three scopes: Workspace, Project, Session
- CRUD via built-in tools: `nanite_todo_create`, `nanite_todo_update`, `nanite_todo_list`, `nanite_todo_delete`
- Both agents and users can edit
- Metadata: labels, priority, status (pending/in_progress/done/blocked), parent_id (nesting)
- Session todos auto-surface in context assembly

**Design — Plans:**
- Parallel to todos: a plan is a structured sequence of steps with dependencies
- Plans can reference todos (plan step → todo item)
- Plans track: proposed → approved → in_progress → complete
- Agents create plans before executing multi-step work
- Plan steps can have acceptance criteria, tool requirements, scope constraints

**Design — Task decomposition:**
- Plans decompose into todos automatically
- Support for hierarchical decomposition (epic → task → subtask)
- Basic metadata: feature, phase, sprint labels
- Query across scopes: "all blocked items in this project"

**Approach:**
1. Extend `internal/task/` or build `internal/todo/` (separate from Engine tasks)
2. Schema: `todos` table (scope, parent_id, metadata JSON), `plans` table (steps JSON, status)
3. Built-in tools registered in MCP
4. Frontend: todo panel in session sidebar, plan viewer
5. Remove Fragments Engine as required dependency

**Acceptance:**
- Todos at all three scopes, CRUD via agent tools + API
- Plans created by agents, viewable by users
- No Fragments Engine dependency in core
- Tests for service + store layers

---

## Task 2: Sandboxing (Tier 1 + Tier 2)

**Priority:** High — blocks code execution (#9) | **Effort:** Medium

**Current state:**
- `internal/sandbox/` (389 lines) is a content staging area — CLAUDE.md + agent context + .mcp.json
- No OS-level isolation, no filesystem boundary, no network proxy
- Shell feature has denylist (`shell/denylist.go`) but no sandbox enforcement

**Two execution modes with different trust models:**

**Agent-exec (full isolation):**
- `sandbox.AgentExec(cmd, opts)` — agent-initiated tool calls
- Full sandbox: restricted CWD, stripped PATH, env filtering, denylist
- OS-level isolation (Tier 2) when available

**User-exec (guardrails only):**
- `sandbox.UserExec(cmd, opts)` — user-initiated `!` shell commands
- Denylist for catastrophic commands (rm -rf /, drop database, etc.)
- No approval gates — user already consented by typing the command
- No CWD restriction — user operates in their real environment
- Protect against foot-guns, don't gate on permission

**Tier 1 — Convention-level sandbox (both modes):**
- Denylist enforcement at exec boundary (consolidate from shell layer)
- Agent-exec: restricted CWD + stripped PATH + env filtering
- User-exec: denylist only + env filtering (no API key leaks in output)

**Tier 2 — OS-level isolation (agent-exec only):**
- macOS: `sandbox-exec` (seatbelt) profile:
  - Read/write within sandbox CWD only
  - Network: proxy-allowlisted domains only (or deny-all for pure compute)
  - No process inspection, no signal sending to non-child processes
- Linux: bubblewrap with equivalent constraints
- Fallback: Tier 1 on unsupported platforms, with a logged warning

**Acceptance:**
- Agent-exec: full CWD + PATH + env restrictions + OS isolation on macOS
- User-exec: denylist + env filtering, no CWD restriction
- Denylist logic consolidated (one place, not shell + sandbox)
- Existing shell tests pass

---

## Task 3: Envelope Contract Generator

**Priority:** Medium | **Effort:** Small

**Current state:**
- Plugin generator exists (`nanite plugin new --with-envelope`)
- Envelope sync is manual and fragile (CLAUDE.md warns about silent drops)
- No typed contracts, no validation

**Approach:**
1. Define JSON Schema for each envelope type (source of truth)
2. Go test: `TestEnvelopeContracts` validates backend payloads against schemas
3. TS codegen: script reads schemas, generates TypeScript types + registry entries
4. Wire into build: `make build` runs codegen before frontend build
5. Extend plugin generator to scaffold schema + test + TS type for new envelopes

**Acceptance:**
- Every registered envelope type has a JSON Schema
- Backend test validates payload shapes against schemas
- Frontend types are generated (not hand-maintained)
- Adding a new envelope type without updating both sides fails the build

---

## Task 9: Code Execution Tool

**Priority:** Medium | **Effort:** Small
**Blocked by:** Task 2 (sandboxing)

**Approach:**
- Built-in tool: `nanite_code_execute`
  - Params: `code` (string), `language` (shell/python/js), `timeout` (seconds)
  - Runs via `sandbox.AgentExec()` (full isolation)
  - Returns: stdout, stderr, exit_code
  - Token budget on output (truncate with steering hint)
- Start with shell only; Python/JS follow
- Also supports the MCP "filesystem-as-tool-catalog" pattern: agent writes script → executes → reads result

**Acceptance:**
- Shell execution works in agent sandbox
- Output truncated with model-friendly steering hints
- Timeout enforced
- Tests for happy path + timeout + error cases

---

## Bonus: Reasoning-Blind Filter View

**Priority:** Medium | **Effort:** Small

- Filter positions declare what context subset they see
- `tool.executing` filter can register under a reasoning-blind view
- View receives: user message + tool call only, never assistant reasoning
- Enables future safety classifier plugin without breaking changes

---

## Task 4: Memory System (Cortex-backed)

**Priority:** High | **Effort:** Medium

**Current state:**
- Phase C investigation complete (13 open questions resolved or resolvable)
- Cortex API complete with embedding stubs
- No memory extraction or storage in Nanite

**Approach:**
1. Memory service: `internal/memory/` package
   - `MemoryService` interface: `Extract(ctx, sessionID, content) []Memory`, `Store(ctx, Memory) error`, `Recall(ctx, query, scope) []Memory`
   - Cortex-backed store using the existing `contextbroker/source_cortex.go` patterns
   - Memory types: preference, correction, decision, fact, context (subtypes of single `memory` type per Phase C)
2. Extraction triggers:
   - Per-turn: lightweight extractor on `assistant_response` / `user_message` via filter chain subscriber (preferences, corrections)
   - Post-compaction: rich structured extraction triggered by `context.compacted` event
3. Memory recall:
   - Wire into context assembly as a context source in the broker
   - Scope: session → project → workspace (cascade with dedup)
4. Memory tools for agents: `nanite_memory_save`, `nanite_memory_recall` (opt-out per agent config)

**Acceptance:**
- Memories survive session boundaries
- Per-turn extraction captures corrections/preferences
- Post-compaction extraction captures structured context
- Memory recall integrated into context assembly
- Agents can opt out of memory tools

---

## Task 6: Workflow Engine v2

**Priority:** Medium | **Effort:** Medium
**Status:** Implementation approved (was design-only, promoted after Hadron evaluation)

**Decision:** Build our own engine. Evaluated Hadron (sibling project) — too heavy to embed
(Wails/gRPC/full-OTel transitive deps, no custom step handlers, all internal/ packages).
Use Hadron's DAG model and retry strategies as reference architecture.

**Architecture:**
- Deterministic workflow engine. Skills handle non-deterministic; workflows handle sequencing.
- `internal/workflow/` package (~1.5K lines)

**Core primitives:**
- `Pipeline` — named collection of steps with dependency graph
- `Step` — atomic unit: handler + required/optional + depends_on + gate + timeout + retry
- `StepHandler` interface: `Execute(ctx, StepInput) (*StepOutput, error)`

**Step handlers:**
- `FuncStep` — Go function (deterministic, programmatic)
- `ShellStep` — command via `sandbox.AgentExec()` (isolated)
- `SkillStep` — runs a Nanite skill (stub: validates + renders template, full integration later)
- `ParallelStep` — fan-out N sub-steps, fan-in with fail policy (all_must_pass / any_pass / ignore)
- `LoopStep` — repeat until gate passes or max iterations (generator/critic pattern)

**Executor:**
- Kahn's algorithm for topological sort
- Concurrent execution of independent steps at same DAG level
- Gate functions for pre-execution validation
- Clean exit: cancel → remaining steps Cancelled; required fail → dependents Skipped
- Event emission via callback (wired to plugin events at integration layer)

**YAML loader:**
- Parse workflow definitions from YAML files
- Validate: no duplicate IDs, no missing deps, no cycles
- Template interpolation in prompts (evaluated at runtime, not load time)

**What's deferred:**
- Persistence/checkpointing (RunState is in-memory for now)
- Full SkillStep integration with chat service
- Workflow API endpoints + frontend UI
- Session binding

---

## Execution Order

```
Phase 1 — Quick wins (DONE):
  ✅ Task 1 — God-object decomp (engine.go 2164→197 lines)
  ✅ Task 8 — Progressive threshold (5→10, single source)

Phase 2 — Quick wins + Todo system (DONE):
  ✅ Task 7 — Dead event audit (2 deleted, 1 wired, 6 planned-v2)
  ✅ Think tool (no-op scratchpad + system prompt)
  ✅ Task 5 — Internal todo/plan system (full stack: migration, store, service, API, agent tools)

Phase 3 — Sandboxing (DONE):
  ✅ Task 2 — Sandboxing Tier 1 + Tier 2
    Two trust models: agent-exec (full isolation) vs user-exec (guardrails)
    macOS sandbox-exec seatbelt for OS-level isolation

Phase 4 — Contracts + Code exec (DONE):
  ✅ Task 3 — Envelope contracts (24 schemas, Go tests, TS codegen, build integration)
  ✅ Task 9 — Code execution (nanite_code_execute, shell/python/js)
  ✅ Reasoning-blind filter view (FilterView type, RegisterFilterWithView)

Phase 5 — Memory:
  Task 4 — Memory system (waiting on Cortex embeddings)

Phase 6 — Workflow engine (IN PROGRESS):
  Task 6 — Workflow engine v2 (building now)
```

---

## Alignment Matrix Coverage

| Matrix Section | Covered By |
|---|---|
| 1. Chat Engine Loop & Agent Runtime | Task 1 (decomp) ✅, Think tool, Task 9 (code exec) |
| 3. Tool System | Task 8 (threshold) ✅, Task 9 (code exec), Task 3 (contracts) |
| 5. Plugin Host, Events, Filters | Task 7 (dead events), Reasoning-blind filter |
| 6. Sandboxing & Safety | Task 2 |
| 7. Memory & Continuity | Task 4 |
| 8. Workflows & Determinism | Task 6 |
