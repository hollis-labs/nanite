# tokf Research & CLI Tooling Integration Plan

> Research date: 2026-03-14
> Status: Research complete, epic creation pending
> Participants: Chrispian + Mentat meta-agent session

## 1. tokf Overview

**tokf** (mpecan/tokf) is a config-driven CLI tool that reduces LLM context consumption by filtering verbose command output through TOML-defined pipelines. Installed at `/opt/homebrew/bin/tokf` v0.2.28 via Homebrew.

### Key Capabilities

| Feature | Description |
|---------|-------------|
| **Filter pipeline** | skip → keep → replace → sections → chunks → template |
| **3-tier config** | built-in (48 filters) → user (`~/.config/tokf/filters/`) → project (`.tokf/filters/`) |
| **Shell substitution** | `SHELL=tokf` routes all task runner commands through filters |
| **Hook integration** | `PreToolUse` hooks for Claude Code, Opencode, Codex |
| **Variant delegation** | Detects framework (vitest/jest/pytest) → delegates to specialized filter |
| **Exit code masking** | Exits 0, prepends error text (configurable passthrough) |
| **Token tracking** | SQLite DB at `~/Library/Application Support/tokf/tracking.db` |
| **OTel export** | `--otel-export` flag (requires feature build) |
| **Safety checks** | `tokf verify --safety` detects prompt injection, shell injection, hidden unicode |
| **Skill support** | `tokf skill install` generates Claude Code skill for filter authoring |

### Current State in Fragments Engine

**Status: Installed but zero integration with any FE project or agent tool.**

| Aspect | Status |
|--------|--------|
| Claude Code hooks | Not installed |
| Opencode hooks | Not installed |
| Codex hooks | Not installed |
| Custom filters | None (48 built-in only) |
| Any FE project reference | Zero |
| Mentat internal filters | Separate Go implementation (`internal/filter/`) |
| Token savings to date | 815 runs, 54K tokens saved (36.1%) — passive CLI only |

### Token Savings Analytics (from `tokf gain`)

```
total runs:     815
input tokens:   151,388 est.
output tokens:  96,694 est.
tokens saved:   54,694 est. (36.1%)

Top filters by savings:
  npm run *     runs: 30   saved: 37,869 (74.9%)
  go test       runs: 144  saved: 19,405 (62.0%)
  git commit    runs: 66   saved: 3,850  (80.2%)
  git push      runs: 15   saved: 342    (88.6%)
```

## 2. Architectural Patterns Worth Adopting

### A. 3-Tier Config Hierarchy

tokf: embedded → `~/.config/tokf/filters/` → `.tokf/filters/`

**Apply to:**
- Hadron blueprints: stdlib blueprints → user `~/.config/hadron/blueprints/` → project `.hadron/blueprints/`
- Mentat skills: built-in → global `~/.claude/skills/` → project `.claude/skills/` (already done)
- Output filters: built-in → user-level → project-level

### B. TOML Filter Pipeline

Multi-stage, composable, declarative filtering. Each stage is independent and optional.

**Apply to:**
- Hadron pipeline output processing
- Toolbroker response normalization
- PII/secrets stripping layer
- Potential `core/filter` Go package sharing tokf's TOML format

### C. Controlled Shell Execution

Force execution through a wrapper that audits, filters, and budgets every command.

```
Agent → Bash tool → controlled shell →
  1. Audit log (who, what, when)
  2. Policy check (is this command allowed?)
  3. tokf filter (compress output)
  4. PII strip (redact sensitive data)
  5. Token budget check
  6. Return filtered result → context window
```

**Apply to:**
- Hadron blueprint `run` stages
- Agent task execution
- Multi-agent orchestration

### D. Variant Delegation

Detect context (project type, framework, etc.) and delegate to specialized handler.

**Apply to:** Toolbroker intent routing, Hadron blueprint selection

### E. Exit Code Masking

Normalize errors so agents don't panic on non-zero exit codes.

**Apply to:** Hadron pipeline stages, agent Bash tool calls

### F. Safety Verification

`tokf verify --safety` pattern for validating user-contributed config.

**Apply to:** Blueprint validation, skill validation, hook validation

## 3. Hook Conflict Analysis & Claude Code Bug

### Known Bug: `updatedInput` silently discarded with multiple PreToolUse hooks

**Bug:** [anthropics/claude-code#15897](https://github.com/anthropics/claude-code/issues/15897) — open since 2025-12-31, no fix ETA.

**Root cause:** When multiple PreToolUse hooks match the same tool, Claude Code runs them in parallel. The last hook to return overwrites `updatedInput`. If any hook returns no `updatedInput` (e.g., a passthrough/observational hook), the rewrite from earlier hooks is silently discarded.

**Our scenario:** envelope-guard matched `Write|Edit|Bash` and tokf matched `Bash`. Both fired on Bash tool calls. Envelope-guard returned no `updatedInput`, overwriting tokf's command rewrite. Result: zero tokf filtering on agent commands.

**Fix applied (2026-03-14):** Narrowed envelope-guard matcher to `Write|Edit` only (removed `Bash`). tokf is now the sole PreToolUse hook for Bash, so `updatedInput` works correctly.

**Impact of fix:** Envelope-guard no longer inspects Bash commands for protected path writes. This is acceptable because:
- File writes via Bash (e.g., `echo > file`) are rare compared to `Write`/`Edit` tool calls
- The `Write|Edit` matcher still catches the vast majority of envelope violations
- tokf's filtering is higher value than Bash-level envelope checking

**Broader implication:** Any project with multiple PreToolUse hooks matching the same tool will hit this bug. All FE projects should ensure no PreToolUse matcher overlap with tokf's `Bash` hook until CC#15897 is fixed.

### Hook Configuration (post-fix)

**Global** (`~/.claude/settings.json`):
- `PreToolUse` on `Bash` → tokf hook handler (command rewrite for token filtering)

**Project** (`.claude/settings.json`):
- `PreToolUse` on `Write|Edit` → `envelope-guard.sh` (protected path checking)
- `PreToolUse` on `Agent` → `prevent-recursive-agents.sh`
- `PostToolUse` on `Agent` → `worktree-check.sh`

## 4. Similar Tools Research

### Tier 1: Install This Week

| Tool | Purpose | Why |
|------|---------|-----|
| **Gitleaks** (gitleaks/gitleaks) | Go-native secrets detection, stdin pipe support | PII/secrets filter for agent context. `gitleaks detect --pipe`. Go-native. |
| **mise** (jdx/mise) | Polyglot task runner + tool version manager | Replace Makefiles, standardize targets across all 7 Go projects. Rust binary. |

### Tier 2: Next Sprint

| Tool | Purpose | Why |
|------|---------|-----|
| **Rivet Sandbox Agent** (rivet-dev/sandbox-agent) | HTTP API for agent sandbox control | Replace tmux-based multi-agent pattern. Event streaming to Postgres. Session replay. |
| **Nushell MCP** (nu-mcp) | Structured data shell with MCP server | Replace bash+jq chains in skills/blueprints. Typed pipelines. |

### Tier 3: Evaluate Later

| Tool | Purpose | Why |
|------|---------|-----|
| **Daytona** (daytonaio/daytona) | Sandboxed code execution, Go SDK | Cloud deployment sandbox for blueprints. |
| **Presidio** (microsoft/presidio) | NLP-based PII detection | Deep PII detection beyond regex (names, addresses). REST API mode. |
| **Factory-style structured summarization** | Session summary pattern | Implement in Mentat chat engine for Cortex writes. |

### Not Recommended

| Tool | Why Not |
|------|---------|
| **RTK** | Overlaps with tokf — we already have tokf installed and it's more configurable |
| **Nx** | JS-heavy, overkill for Go-dominant stack |
| **Lattice Proxy** | Too immature, adds network dependency |
| **Claw Compactor** | Unverified "97% compression" claims |

## 5. Skills vs MCP — Decision Framework

| Use Case | Best Tool | Why |
|----------|-----------|-----|
| Data CRUD (tasks, context, blueprints) | **MCP** | Typed params, discovery, chaining |
| Complex workflows (deploy, audit, sync) | **Skills** | Multi-step, context-aware, human guidance |
| Output filtering | **tokf hooks** | Transparent, zero-cost to agent |
| Config/conventions | **agentrc** | Declarative, shared across tools |

**Best practice:** Use both. MCP for data operations, skills for workflows that compose multiple MCP calls. tokf demonstrates this pattern: CLI operations (`tokf run`) + skill for teaching agents (`tokf skill install`).

## 6. Context Capture Decision

**For end-of-session context writes: Use skills/commands as subprocesses.**

Rationale:
- The agent already has full conversation context loaded
- Skills run in the agent's context window — no token cost for "remembering"
- Subprocess pattern prevents capture from stalling interaction
- Events would require forwarding transcript + regeneration — wasteful
- Cost: ~2-5K tokens for summarization prompt + response (negligible vs session)

Future: Events can replace the hook trigger, but something still needs to generate the summary. The agent itself (via skill) is cheapest since it has full context.

## 7. Mentat Internal Filter vs tokf

**Current state:** Mentat has `internal/filter/` (Go) with `NoEmoji` filter. tokf is separate.

**Decision needed:** Clarify filtering ownership.
- **Option A:** Mentat internal filters handle content transformation (emoji, formatting). tokf handles token compression (CLI output). Clear separation.
- **Option B:** Delegate all filtering to tokf (custom TOML filter for emoji stripping). Mentat becomes a consumer.
- **Recommended:** Option A — they solve different problems at different layers. Document in ADR.

## 8. PII/SOC2 Filter Architecture

Two-layer approach:
1. **tokf custom filters** — regex-based, fast, catches obvious patterns (emails, SSNs, API keys)
2. **Gitleaks** — Go-native, 160+ secret type detectors, stdin pipe support

For deeper NLP-based PII (names, addresses): evaluate Presidio later.

Example tokf security filter:
```toml
# ~/.config/tokf/filters/security/pii.toml
command = "*"
[[replace]]
pattern = '[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}'
replacement = '[REDACTED_EMAIL]'
[[replace]]
pattern = '\b\d{3}-\d{2}-\d{4}\b'
replacement = '[REDACTED_SSN]'
[[replace]]
pattern = '(?i)(sk-|pk_|AKIA)[A-Za-z0-9]{20,}'
replacement = '[REDACTED_KEY]'
```

## 9. Task Runner Decision

**mise over Make/Just.** Reasons:
- Handles Go version pinning + task running + env management in one tool
- `.mise.toml` per project replaces Makefiles
- Parallel task execution by default
- Monorepo task support for multi-repo portfolio orchestration
- Rust binary, well-maintained

The key insight from tokf is `SHELL=tokf` — the runner becomes a dumb sequencer while the shell controls execution context. mise supports custom shell settings.
