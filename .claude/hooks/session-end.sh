#!/usr/bin/env bash
# Stop hook — writes a staleness flag when a session ends without capture
# The next session's boot sequence detects this and offers recovery
set -euo pipefail

ROOT="$PWD"
FLAG_FILE="$ROOT/.agentrc/.session-unclean.flag"
AGENT_DIR="$ROOT/.agentrc"

# Only run for agent-managed repos
[ -d "$AGENT_DIR" ] || exit 0

# Write the flag with timestamp for the next session to detect
cat > "$FLAG_FILE" <<EOF
session_ended=$(date -u +%Y-%m-%dT%H:%M:%SZ)
project=$(basename "$ROOT")
reason=stop_hook
EOF
