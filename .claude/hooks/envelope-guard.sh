#!/usr/bin/env bash
# Unified PreToolUse hook for all Tiamat projects
# Auto-detects project role for appropriate enforcement level
# Exit 0 = allow, Exit 2 = hard block
set -euo pipefail

ROOT="$PWD"

# Resolve agent config directory
if [ -d "$ROOT/.agentrc" ]; then
  AGENT_DIR=".agentrc"
  AGENT_CONF="agentrc.yaml"
else
  AGENT_DIR=""
  AGENT_CONF=""
fi

# --- Detect project role ---
PROJECT_ROLE="generic"
if [ -n "$AGENT_DIR" ] && [ -f "$ROOT/$AGENT_DIR/boot/meta-agent.md" ]; then
  PROJECT_ROLE="orchestrator"
elif [ -f "$ROOT/agentrc.yaml" ]; then
  PROJECT_ROLE="volon-managed"
fi

# --- Parse tool input ---
INPUT=$(cat)
[ -z "$INPUT" ] && exit 0
echo "$INPUT" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null || exit 0
TOOL_NAME=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('tool_name',''))" 2>/dev/null || echo "")
FILE_PATH=$(echo "$INPUT" | python3 -c "import sys,json; d=json.load(sys.stdin).get('tool_input',{}); print(d.get('file_path','') or d.get('path',''))" 2>/dev/null || echo "")
COMMAND=$(echo "$INPUT" | python3 -c "import sys,json; print(json.load(sys.stdin).get('tool_input',{}).get('command',''))" 2>/dev/null || echo "")

# --- Universal hard blocks (all roles) ---
if [ "$TOOL_NAME" = "Write" ] || [ "$TOOL_NAME" = "Edit" ]; then
  # state/ — always managed by gui-server
  if echo "$FILE_PATH" | grep -qE '\.agentrc/state/'; then
    echo "BLOCKED: .agentrc/state/ is managed by the gui-server. Do not write directly." >&2
    exit 2
  fi
  # pcc/global/ — refreshed only via /pcc-refresh
  if echo "$FILE_PATH" | grep -qE '\.agentrc/pcc/global/'; then
    echo "BLOCKED: .agentrc/pcc/global/ is refreshed only via /pcc-refresh." >&2
    exit 2
  fi
fi

# --- Bash command guard (all roles) ---
if [ "$TOOL_NAME" = "Bash" ] && [ -n "$COMMAND" ]; then
  if echo "$COMMAND" | grep -qE '(rm|mv|chmod)\b.*\.agentrc/(state|pcc/global)'; then
    echo "BLOCKED: destructive operation on protected path." >&2
    exit 2
  fi

  # --- Service launch guard ---
  # Block direct service launches. Agents MUST use Cerberus (cerberus restart <id>)
  # to start/stop/restart any Tiamat service. Direct launches clobber Cerberus-managed
  # instances and lose flags like --scheduler-autostart.
  if echo "$COMMAND" | grep -qE 'go run \./cmd/(gui-server|volon|contextd|hadrond|mentat-chat|carrier)'; then
    echo "BLOCKED: Do not launch services directly with 'go run'. Use Cerberus instead:" >&2
    echo "  cerberus restart volon-api     # Volon API" >&2
    echo "  cerberus restart cortex-api    # Cortex API" >&2
    echo "  cerberus restart hadron-daemon # Hadron Daemon" >&2
    echo "  cerberus restart mentat-api    # Mentat API" >&2
    echo "  cerberus build <id>            # Rebuild binary first if needed" >&2
    echo "  cerberus status                # Check all service status" >&2
    exit 2
  fi
  # Match direct binary execution at command position (start of line, after &&, after ;, after |)
  # but not inside quoted strings or echo/cat heredocs
  if echo "$COMMAND" | grep -qE '(^|&&|\|\||;|\|)\s*\./gui-server\b' || \
     echo "$COMMAND" | grep -qE '(^|&&|\|\||;|\|)\s*\./contextd\b' || \
     echo "$COMMAND" | grep -qE '(^|&&|\|\||;|\|)\s*\./hadrond\b' || \
     echo "$COMMAND" | grep -qE '(^|&&|\|\||;|\|)\s*\./mentat-chat\b' || \
     echo "$COMMAND" | grep -qE '(^|&&|\|\||;|\|)\s*\./carrier\b'; then
    echo "BLOCKED: Do not launch service binaries directly. Use Cerberus instead:" >&2
    echo "  cerberus restart <service-id>" >&2
    echo "  cerberus build <service-id>" >&2
    echo "  cerberus status" >&2
    exit 2
  fi
fi

# --- Task-driven enforcement (opt-in) ---
# Activates when VOLON_TASK_DRIVEN=1 via env var or .claude/hooks.config
VOLON_TASK_DRIVEN="${VOLON_TASK_DRIVEN:-0}"
if [ -f "$ROOT/.claude/hooks.config" ]; then
  # shellcheck disable=SC1091
  source "$ROOT/.claude/hooks.config"
fi

if [ "$VOLON_TASK_DRIVEN" = "1" ]; then
  # Check once whether any task is in 'doing' status
  _TD_HAS_DOING=""
  if [ -n "$AGENT_DIR" ] && [ -d "$ROOT/$AGENT_DIR/tasks" ]; then
    _TD_HAS_DOING=$(grep -rl '^status: doing' "$ROOT/$AGENT_DIR/tasks/" 2>/dev/null | head -1)
  fi

  if [ -z "$_TD_HAS_DOING" ]; then
    # No doing task — enforce whitelist

    _td_is_whitelisted() {
      local p="$1"
      # Whitelisted paths: task files, bootstrap, backlog, .claude/, root *.md
      echo "$p" | grep -qE '(^|\/)\.agentrc\/tasks\/' && return 0
      echo "$p" | grep -qE '(^|\/)\.agentrc\/bootstrap\.md$' && return 0
      echo "$p" | grep -qE '(^|\/)\.agentrc\/backlog\/' && return 0
      echo "$p" | grep -qE '(^|\/)\.claude\/' && return 0
      # Root-level .md files (no subdirectory slash before the filename)
      echo "$p" | grep -qE '^[^/]*\.md$' && return 0
      # Absolute path pointing to a root-level .md
      echo "$p" | grep -qE "^${ROOT}/[^/]*\.md\$" && return 0
      return 1
    }

    _td_block_message() {
      echo "BLOCKED: No task claimed. Run \`volon task start <TASK-ID>\` before writing code." >&2
      # Show up to 3 todo tasks
      if [ -n "$AGENT_DIR" ] && [ -d "$ROOT/$AGENT_DIR/tasks" ]; then
        local todo_files
        todo_files=$(grep -rl '^status: todo' "$ROOT/$AGENT_DIR/tasks/" 2>/dev/null | head -3)
        if [ -n "$todo_files" ]; then
          echo "Available tasks:" >&2
          while IFS= read -r tf; do
            local tid tpri
            tid=$(basename "$tf" .md)
            tpri=$(grep '^priority:' "$tf" 2>/dev/null | head -1 | sed 's/priority: *//')
            echo "  ${tid} [${tpri:-?}] $(grep '^title:' "$tf" 2>/dev/null | head -1 | sed 's/title: *//')" >&2
          done <<< "$todo_files"
        fi
      fi
      exit 2
    }

    # Write/Edit enforcement
    if [ "$TOOL_NAME" = "Write" ] || [ "$TOOL_NAME" = "Edit" ]; then
      if [ -n "$FILE_PATH" ]; then
        if ! _td_is_whitelisted "$FILE_PATH"; then
          _td_block_message
        fi
      fi
    fi

    # Bash enforcement — block write-like commands to non-whitelisted paths
    if [ "$TOOL_NAME" = "Bash" ] && [ -n "$COMMAND" ]; then
      if echo "$COMMAND" | grep -qE '(cat\s*>|tee\s|sed\s+-i|echo\s*>)'; then
        # Extract target path from the command (best-effort)
        _td_bash_target=$(echo "$COMMAND" | grep -oE '(cat\s*>|tee\s|sed\s+-i|echo\s*>)\s*[^ ]+' | head -1 | sed 's/^[^ ]* *//')
        if [ -n "$_td_bash_target" ] && ! _td_is_whitelisted "$_td_bash_target"; then
          _td_block_message
        fi
      fi
    fi
  fi
fi

# --- Role-specific enforcement ---
case "$PROJECT_ROLE" in
  orchestrator)
    # Soft enforcement: warn but allow most writes
    if [ "$TOOL_NAME" = "Write" ] || [ "$TOOL_NAME" = "Edit" ]; then
      # Check for active task
      TASKS_DOING=0
      if [ -n "$AGENT_DIR" ] && [ -d "$ROOT/$AGENT_DIR/tasks" ]; then
        TASKS_DOING=$(grep -rl '^status: doing' "$ROOT/$AGENT_DIR/tasks/" 2>/dev/null | wc -l | tr -d ' ')
      fi
      if [ "$TASKS_DOING" -eq 0 ] && [ -n "$FILE_PATH" ]; then
        if ! echo "$FILE_PATH" | grep -qE '\.agentrc/tasks/'; then
          echo "WARNING: No task is currently 'doing'. Claim a task before writing non-task files." >&2
        fi
      fi
      # Warn on config changes
      if [ -n "$FILE_PATH" ]; then
        if echo "$FILE_PATH" | grep -qE '(^|/)config/'; then
          echo "WARNING: writing to config/ — ensure this is intentional." >&2
        fi
        if echo "$FILE_PATH" | grep -qE '(^|/)agentrc\.yaml$'; then
          echo "WARNING: writing to agentrc.yaml — ensure this is intentional." >&2
        fi
      fi
    fi
    if [ "$TOOL_NAME" = "Bash" ] && [ -n "$COMMAND" ]; then
      if echo "$COMMAND" | grep -qE '(cat\s*>|echo\s*>|sed\s+-i).*(config/|agentrc\.yaml)'; then
        echo "WARNING: modifying config via shell — ensure this is intentional." >&2
      fi
    fi
    ;;

  volon-managed)
    # Hard enforcement: block all agent dir managed paths, force MCP tools
    AGENT_DEPTH="${VOLON_AGENT_DEPTH:-0}"
    if [ "$TOOL_NAME" = "Write" ] || [ "$TOOL_NAME" = "Edit" ]; then
      for suffix in "tasks/" "backlog/" "logs/"; do
        local_prefix=".agentrc/${suffix}"
        if echo "$FILE_PATH" | grep -qE "^(\.\/)?${local_prefix}"; then
          echo "BLOCKED: Direct writes to ${local_prefix} are blocked. Use Volon MCP tools (volon_task_create, etc.)." >&2
          exit 2
        fi
      done
      # Block bootstrap.md only for sub-agents
      if echo "$FILE_PATH" | grep -qE '\.agentrc/bootstrap\.md' && [ "$AGENT_DEPTH" -gt 0 ]; then
        echo "BLOCKED: Sub-agent write to bootstrap.md is blocked." >&2
        exit 2
      fi
    fi
    if [ "$TOOL_NAME" = "Bash" ] && [ -n "$COMMAND" ]; then
      # Block destructive shell ops on managed paths
      if echo "$COMMAND" | grep -qE '\b(rm|mv|chmod)\b' && \
         echo "$COMMAND" | grep -qE '\.agentrc/(tasks|backlog|state|logs)/'; then
        echo "BLOCKED: Shell operations on managed paths are blocked. Use Volon MCP tools." >&2
        exit 2
      fi
      # Block direct sqlite3 writes to volon.db (all writes must go through Volon API/MCP)
      if echo "$COMMAND" | grep -qE 'sqlite3\b.*volon\.db.*(INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)'; then
        echo "BLOCKED: Direct DB writes are blocked. Use Volon MCP tools — all projects share the central Volon DB." >&2
        exit 2
      fi
      # Block volon CLI task creation from non-volon repos (creates stray local DBs)
      if echo "$COMMAND" | grep -qE '\bvolon\s+task\s+(create|start|done|block)'; then
        echo "WARNING: Use Volon MCP tools (volon_task_create, volon_task_transition) instead of the CLI. CLI may create a local DB instead of writing to the central Volon DB." >&2
      fi
    fi
    ;;

  generic)
    # Minimal enforcement: only universal blocks above
    ;;
esac

exit 0
