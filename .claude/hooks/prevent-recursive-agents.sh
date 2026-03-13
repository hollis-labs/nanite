#!/bin/bash
# Volon PreToolUse hook: block Task tool (Agent spawning) for sub-agents.
# When VOLON_AGENT_DEPTH >= 1, the current process is a sub-agent and must
# not spawn further sub-agents via the Task tool.
# Exit 2 = hard block; exit 0 = allow.

DEPTH="${VOLON_AGENT_DEPTH:-0}"

if [ "$DEPTH" -ge 1 ] 2>/dev/null; then
  echo "VOLON: Sub-agents cannot spawn further agents (VOLON_AGENT_DEPTH=$DEPTH)." >&2
  echo "Only the Orchestrator (depth 0) may use the Task tool." >&2
  exit 2
fi

exit 0
