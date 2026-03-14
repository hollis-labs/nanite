#!/usr/bin/env bash
# PostToolUse hook: warn when stale agent worktrees accumulate
# Runs after Agent tool use completes. Non-blocking — warning only.
# Exit 0 always (never block).

THRESHOLD=5
SEARCH_ROOT="$HOME/Projects-apps"

# Count worktree directories matching agent patterns
count=0

# Pattern 1: .claude/worktrees/agent-*
for d in "$SEARCH_ROOT"/*/.claude/worktrees/agent-*; do
  [ -d "$d" ] && count=$((count + 1))
done

# Pattern 2: *-agent-* directories directly under project roots
for d in "$SEARCH_ROOT"/*-agent-*; do
  [ -d "$d" ] && count=$((count + 1))
done

if [ "$count" -gt "$THRESHOLD" ]; then
  echo "WARNING: $count agent worktrees detected in $SEARCH_ROOT (threshold: $THRESHOLD)." >&2
  echo "Consider running /worktree-cleanup to reclaim disk space." >&2
fi

exit 0
