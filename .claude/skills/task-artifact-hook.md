# task-artifact-hook

Auto-attach artifacts (files created or modified) to the current Volon task when completing work. This skill documents the hook setup and provides the implementation.

## Usage
This is a **hook configuration**, not a slash command. It runs automatically when files are written during task execution.

## Setup

Add the following to your project's `.claude/settings.json` under the hooks configuration:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit",
        "command": "~/.agentrc/hooks/shared/task-artifact.sh"
      }
    ]
  }
}
```

## Hook Script

Install the hook script at `~/.agentrc/hooks/shared/task-artifact.sh`:

```bash
#!/usr/bin/env bash
# Auto-attach file artifacts to the current Volon task
# Triggered on Write/Edit tool use
set -euo pipefail

# Parse tool input
INPUT=$(cat 2>/dev/null || echo "{}")
echo "$INPUT" | python3 -c "import sys,json; json.load(sys.stdin)" 2>/dev/null || exit 0

FILE_PATH=$(echo "$INPUT" | python3 -c "
import sys, json
d = json.load(sys.stdin).get('tool_input', {})
print(d.get('file_path', '') or d.get('path', ''))
" 2>/dev/null || echo "")

# Skip if no file path
[ -z "$FILE_PATH" ] && exit 0

# Skip non-project files (temp files, system files)
case "$FILE_PATH" in
  /tmp/*|/var/*|/dev/*) exit 0 ;;
esac

# Get current task ID from session state
TASK_FILE="/tmp/tiamat_current_task_${PPID}.id"
[ ! -f "$TASK_FILE" ] && exit 0
TASK_ID=$(cat "$TASK_FILE")
[ -z "$TASK_ID" ] && exit 0

# Record the artifact (append to a session artifact log)
ARTIFACT_LOG="/tmp/tiamat_artifacts_${TASK_ID}.log"
echo "$FILE_PATH" >> "$ARTIFACT_LOG"

# Exit cleanly — never block the agent
exit 0
```

## How It Works

1. When an agent starts a task (transitions to "doing"), it writes the task ID to `/tmp/tiamat_current_task_$PPID.id`
2. Every Write/Edit operation triggers this hook, which logs the file path
3. When the task completes (transition to "done"), the agent reads the artifact log and attaches files to the task via `volon_task_artifact_create`

## Integration with Task Lifecycle

### On task start (add to agent boot/task-claim logic):
```bash
echo "TASK-20260314-XXX" > "/tmp/tiamat_current_task_$$.id"
```

### On task complete (add to task-done transition):
```bash
TASK_ID="TASK-20260314-XXX"
ARTIFACT_LOG="/tmp/tiamat_artifacts_${TASK_ID}.log"
if [ -f "$ARTIFACT_LOG" ]; then
  # Deduplicate and attach
  sort -u "$ARTIFACT_LOG" | while read -r FILE; do
    # Use volon_task_artifact_create MCP tool
    echo "Artifact: $FILE"
  done
  rm -f "$ARTIFACT_LOG"
fi
rm -f "/tmp/tiamat_current_task_$$.id"
```

## Notes
- The hook always exits 0 to never block the agent
- Artifacts are deduplicated before attachment
- Temp files, system files, and non-project paths are excluded
- The hook uses the parent PID to scope to the current session
- This pairs with the existing audit.sh hook (they don't conflict — different matchers)
