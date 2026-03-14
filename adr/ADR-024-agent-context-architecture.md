# ADR-024: Agent Context Architecture

## Status: Accepted

## Date: 2026-03-14

## Context

Fragments Engine agents run across multiple projects, worktrees, and sessions. Context continuity is the #1 pain point. Claude Code provides MEMORY.md (auto-injected into every conversation) and hooks (PreToolUse/PostToolUse). The portfolio has Cortex as a structured context store. Current state: MEMORY.md used for actual memory storage (path-scoped, isolated per worktree), boot sequence requires user to say "boot", sub-agents lose context, no enforcement of tool usage patterns.

Key discoveries:
- MEMORY.md is auto-injected BEFORE CLAUDE.md — highest prompt priority
- `autoMemoryDirectory` setting and `CLAUDE_CODE_REMOTE_MEMORY_DIR` env var can redirect memory path
- Hooks can inject context via stderr on every tool call
- Team memory exists at .claude/memory/team/ (git-synced)
- Worktrees get isolated memory by default (different path = different memory)

## Decision

Five-layer agent context architecture:

### Layer 1: MEMORY.md as Boot-Level Directive Injection
- MEMORY.md contains ONLY: system imperatives, boot hash, core tool descriptions, ADR index pointers, Cortex namespace pointers
- NOT used for actual memory storage — Cortex handles persistence
- Stays under 50 lines — lean, fast, always in context
- Includes auto-boot imperative: "Execute boot sequence in CLAUDE.md immediately, do not wait for user"
- Includes core tool scaffold: Volon operations, Cortex context tools, service health
- Includes progressive discovery pointers: "For blueprints → hadron_bp_*", "For full inventory → /discover-tools"
- Can inject process templates and gate instructions at the highest prompt priority

### Layer 2: CLAUDE.md + agentrc Boot Sequence
- CLAUDE.md triggers boot: profile selection → agent-boot.md → bootstrap.md
- Boot profiles define role-specific tool access, write paths, and constraints
- Bootstrap.md provides iteration state and next steps
- Boot hash generated at session start for tracing and segment detection

### Layer 3: Hook-Based Enforcement and Progressive Discovery
- PreToolUse hooks: deterministic gates (project_id required, path guards, namespace ownership)
- PostToolUse hooks: context injection (tool tips, capture reminders, memory mirroring)
- PostToolUse on Write/Edit to memory paths: mirror writes to Cortex automatically
- End-of-turn detection: if significant work done, prompt for /session-handoff
- Progressive discovery: when agent tries manual approach, hook suggests the right tool/skill
- Boot hash verification: hook checks if boot imperative is still in active context

### Layer 4: Cortex as the Real Memory System
- All persistent context → Cortex via namespaced typed records
- Searchable, cross-agent, cross-session, cross-project
- Written via MCP tools + hook mirroring from MEMORY.md writes
- Read at boot via /reorient and progressive discovery
- Session snapshots: structured handoff documents with embeddings

### Layer 5: frag Launcher Environment Setup
- `CLAUDE_CODE_REMOTE_MEMORY_DIR` set per-project so all worktree agents share one MEMORY.md
- Boot hash generation (FE-YYYYMMDD-{short-hash})
- Service health pre-check before agent launch
- Project-specific env vars (VOLON_POSTGRES_DSN, etc.)

### Auto-Capture Hooks
- PostToolUse on git commit: detect decision keywords → prompt for /adr
- PostToolUse on task transition to done: trigger sprint auto-close check (already implemented)
- End-of-session: prompt for /session-handoff if in-progress work exists
- These are nudges, not mandatory — agent can skip if not relevant

### Boot Hash Contract
- Generated at session start: FE-{date}-{4char-hash}
- Written to /tmp/frag-boot-hash-{pid}
- Included in MEMORY.md header
- Tagged on all Cortex writes for segment detection
- Hooks can verify hash presence to detect context loss from compaction

## Consequences

Positive:
- Auto-boot without user intervention (MEMORY.md imperative fires first)
- Core tools always in agent context without reading files
- Progressive discovery reduces tool surface overwhelm
- Cortex as system of record — cross-agent, searchable, persistent
- Hook enforcement is deterministic — not dependent on LLM judgment
- Provider-agnostic except MEMORY.md auto-injection (replicable pattern)
- Boot hash enables tracing and drift detection

Negative:
- MEMORY.md now has dual purpose (Claude's auto-memory + our directives) — must be carefully managed
- 50-line budget for directives is tight — requires discipline
- Hook overhead on every tool call (must stay fast, <100ms)
- Cortex becomes a hard dependency for context continuity
- frag launcher must be used for proper env setup — direct `claude` invocation skips Layer 5

## References

- ADR-022: Special Agent Integration Architecture (native/MCP/CLI layers)
- ADR-015: Central Agent Filesystem
- ADR-013: Conduit Separation (Mentat = agent, Conduit = harness)
- Claude Code memory system: `autoMemoryDirectory`, `CLAUDE_CODE_REMOTE_MEMORY_DIR`
- Existing skills: /reorient, /session-handoff, /adr
