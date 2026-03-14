# frag Launcher — Environment Variable Specification

**Status:** Spec only (launcher not yet implemented)
**ADR:** ADR-024 Agent Context Architecture, Layer 5
**Date:** 2026-03-14

## Purpose

The `frag` launcher (`~/bin/frag`) wraps `claude` invocations to set environment variables that enable ADR-024's context architecture. Without `frag`, agents get isolated memory directories and no boot hash.

## Required Environment Variables

### CLAUDE_CODE_REMOTE_MEMORY_DIR

```bash
export CLAUDE_CODE_REMOTE_MEMORY_DIR="$HOME/.agentrc/memory/${PROJECT_ID}"
```

**What it does:** Redirects Claude Code's memory system to a shared, project-scoped directory instead of the default path-scoped directory (`~/.claude/projects/-<escaped-path>/memory/`).

**Why it matters:** Without this, every worktree agent gets an isolated empty memory directory. With it, all agents for the same project share one MEMORY.md containing the auto-boot imperative and directive scaffold (ADR-024 Layer 1).

**Already implemented in:** Volon executor (`internal/runtime/scheduler/runner.go` line 548). The frag launcher needs to do the same for interactive sessions.

### VOLON_POSTGRES_DSN

```bash
export VOLON_POSTGRES_DSN="postgres://localhost/volon?sslmode=disable"
```

**What it does:** Points Volon MCP to the canonical Postgres database. Required for all Volon operations.

### Boot Hash (future)

```bash
export FRAG_BOOT_HASH="FE-$(date +%Y%m%d)-$(head -c 2 /dev/urandom | xxd -p)"
```

**What it does:** Generates a unique session identifier at launch time. Written to `/tmp/frag-boot-hash-$$`. Used by hooks and Cortex writes for segment detection.

**Not yet implemented** — see TASK-338.

## Health Pre-Check

Before launching `claude`, `frag` should verify services are running:

```bash
curl -sf http://127.0.0.1:8095/v1/health >/dev/null  # Hadron
curl -sf http://127.0.0.1:8085/health >/dev/null       # Volon GUI
curl -sf http://127.0.0.1:8080/v1/health >/dev/null    # Cortex
```

If any fail, warn but don't block (services may not be needed for all work).

## Project ID Resolution

The launcher needs to determine `PROJECT_ID` from the current directory:

1. Read `agentrc.yaml` in cwd → extract `project_id` field
2. If no `agentrc.yaml`, fall back to directory basename
3. If explicitly passed via `frag --project <id>`, use that

## Example Implementation Sketch

```bash
#!/usr/bin/env bash
set -euo pipefail

# Resolve project ID
if [[ -f agentrc.yaml ]]; then
    PROJECT_ID=$(grep '^project_id:' agentrc.yaml | awk '{print $2}')
else
    PROJECT_ID=$(basename "$PWD")
fi

# Set memory directory
export CLAUDE_CODE_REMOTE_MEMORY_DIR="$HOME/.agentrc/memory/$PROJECT_ID"
mkdir -p "$CLAUDE_CODE_REMOTE_MEMORY_DIR"

# Set Volon DSN
export VOLON_POSTGRES_DSN="${VOLON_POSTGRES_DSN:-postgres://localhost/volon?sslmode=disable}"

# Health pre-check (warn only)
for svc in "Hadron:8095/v1/health" "Volon:8085/health" "Cortex:8080/v1/health"; do
    name="${svc%%:*}"
    url="http://127.0.0.1:${svc#*:}"
    curl -sf "$url" >/dev/null 2>&1 || echo "warn: $name not reachable at $url"
done

# Launch claude with all args passed through
exec claude "$@"
```

## Memory Directory Structure

```
~/.agentrc/memory/
  mentat/
    MEMORY.md          # Auto-boot imperative + directives
    *.md               # Individual memory files
  volon/
    MEMORY.md
  cortex/
    MEMORY.md
```

Each project gets its own memory directory. MEMORY.md follows the ADR-024 directive format (~50 lines max). Individual memory files are project-specific context.

## Migration Path

Currently, memory files live at `~/.claude/projects/-<escaped-path>/memory/`. The frag launcher creates a new canonical location at `~/.agentrc/memory/<project_id>/`. Migration:

1. Copy existing MEMORY.md content to new location
2. Set `CLAUDE_CODE_REMOTE_MEMORY_DIR` to point to new location
3. New sessions use the new path; old sessions continue working until path is updated
4. Eventually, old memory directories can be cleaned up
