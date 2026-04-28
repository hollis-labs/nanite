# ADR-026: Managed Agents — Session, Harness, and Sandbox Decoupling

**Status**: Accepted
**Date**: 2026-04-26
**Ticket**: CW-20260420-0020
**Related**: CW-20260426-0018 (B5-DF), `docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md`

---

## Context

Ticket CW-20260420-0020 (Managed Agents) required that session management,
harness identity, and sandbox execution be properly decoupled. The three-role
harness spec (2026-04-21) defined the architectural boundary:

- **Session** — lifecycle of a user conversation; owns message history and
  agent bindings.
- **Harness** — the Chat agent's persistent identity and tool surface within
  a session. Invisible to users; dispatches work via `executeTask`.
- **Sandbox** — the isolated execution environment populated for each Worker
  or Planner sub-agent run. Ephemeral; scoped per dispatch.

Without decoupling, harness identity leaked into sandbox configuration (e.g.,
the Mentat/Fragments Engine identity in `PlatformPromptTemplate` was injected
into every agent that had templates assigned, regardless of role). This created
a bifurcated identity: sessions without templates got a clean Nanite identity,
sessions with templates got stale Mentat content.

---

## Decision

Formalize the three-boundary model in code:

### 1. Session boundary — `internal/store/agents.go`, `internal/service/agent.go`

Session-agent bindings live in `session_agents` (join table). The Chat agent
is the primary binding (`is_primary = true`). Workers are spawned per-run via
`subagent_runs` and do not persist as session agents. This ensures session
lifetime and agent lifetime are independent.

`ResolveForSession()` in `internal/service/agent.go` enforces this: it resolves
the Chat agent from session bindings, then falls back to `user_settings.default_agent`,
then to the hardcoded `file-default` — all within session scope.

### 2. Harness boundary — `internal/agent/builtin/default.md`, migration 027

The Chat-role harness prompt is now a first-class DB template assigned to
`file-default`. Retiring `PlatformPromptTemplate` (the Mentat-era Go var)
ensures the harness identity is uniform: both the legacy path (no templates)
and the template path (templates assigned) now produce the same canonical
Chat identity. The composition layer (`BuiltinPromptTemplates` + variable
interpolation) remains active for additive context.

### 3. Sandbox boundary — `internal/agent/adapter.go` `SandboxContext`

`SandboxContext` carries only the session-scoped information that adapters
need to populate a sandbox directory: session ID, agent profile, MCP server
list, allowed tools, and project root. It does NOT carry harness prompt
content — the sandbox agent loads its own system prompt from its profile.

`CLIAgentAdapter.PopulateSandbox()` writes CLI-specific config files
(e.g., `.claude/settings.json`, `mcp.json`) into an isolated `sandboxDir`.
Each adapter implementation (`adapter-claude`, `adapter-nanite-native`,
`adapter-gemini`, etc.) populates its own format without coupling to the
harness or session beyond what `SandboxContext` provides.

---

## Consequences

**Positive:**
- Harness identity is now uniform across all session configurations (bifurcated
  identity bug resolved by retiring `PlatformPromptTemplate`).
- Sandbox population is adapter-specific and does not depend on harness content.
- Session lifetime is independent of agent lifetime; spawned Workers are
  ephemeral subagent runs.
- The composition layer (`BuiltinPromptTemplates`) is preserved for future
  per-agent customization without re-introducing global identity injection.

**Negative / constraints:**
- File-based agent IDs (`file-default`, `file-<slug>`) are synthetic and not
  in `agent_profiles`. The `agent_prompt_templates.agent_id` column has no FK
  constraint by design (noted in 001_schema.sql). Any tooling that assumes all
  `agent_id` values in `agent_prompt_templates` correspond to `agent_profiles`
  rows will be incorrect for file-based agents.
- `executeTask` dispatch (connecting Chat → ScopeTier → Worker/Planner) is
  the remaining B3 work; this ADR covers the identity/decoupling concern only.

---

## Verification

Code locations confirming the three-boundary model is in place:

| Boundary | File | Key construct |
|---|---|---|
| Session | `internal/service/agent.go:29` | `defaultFallbackAgent = "file-default"` |
| Session | `internal/service/agent.go:153` | `ResolveForSession()` FSM |
| Harness | `internal/store/prompt_templates.go` | `PlatformPromptTemplate` removed; `ComposePromptForAgent()` no longer auto-prepends |
| Harness | `internal/store/migrations/027_chat_role_harness_prompt.sql` | Chat-role harness prompt seeded + assigned to `file-default` |
| Sandbox | `internal/agent/adapter.go:23` | `CLIAgentAdapter.PopulateSandbox()` interface |
| Sandbox | `internal/agent/adapter.go:49` | `SandboxContext` struct (session-scoped, harness-free) |
