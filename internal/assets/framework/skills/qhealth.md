# Quick Health (:qhealth)

Compact service health check. Returns a one-line-per-service status with latency. Zero context bloat.

## When to use

- When the user types `:qhealth` or asks "are services up?"
- Quick pre-flight check before running sprints or dispatching agents
- After restarts or deployments

## IMPORTANT: Run in Sub-Agent

This skill MUST be executed via the Agent tool (subagent) to keep the main context clean. Raw curl output stays in the sub-agent.

## Procedure

Launch an Agent with this prompt:

```
You are a health reporter. Check each service and return ONLY a compact table. No raw output, no explanations.

For each service, run via Bash:
  START=$(python3 -c "import time; print(int(time.time()*1000))")
  STATUS=$(curl -sf -o /dev/null -w "%{http_code}" <url> --max-time 3 2>/dev/null || echo "000")
  END=$(python3 -c "import time; print(int(time.time()*1000))")
  LATENCY=$((END - START))

Services:
  | Service | URL | Endpoint |
  |---------|-----|----------|
  | Clockwork GUI | http://127.0.0.1:8085 | /v1/tasks |
  | Tesseract | http://127.0.0.1:8089 | /v1/health/readiness |
  | Hadron | http://127.0.0.1:8095 | /v1/health |

Also check Cerberus daemon:
  - Use mcp__cerberus__cerberus_status to get managed service count and overall state

Return this EXACT format and nothing else:

=== QHEALTH ===
Clockwork [UP/DOWN]  <http_code>  <latency>ms
Tesseract [UP/DOWN]  <http_code>  <latency>ms
Hadron    [UP/DOWN]  <http_code>  <latency>ms
Cerberus  [UP/DOWN]  <N> services managed, <M> running
================
```

## Output

Display the sub-agent's response directly. No additional commentary.

## Invariants

- ALWAYS run via sub-agent — never pull health check output into main context
- Read-only — no state changes
- 3s timeout per service, 15s max total
- Always show all services even if down
- Tesseract's released default address is `:8089`; use the deployment's configured address when it is explicitly overridden
