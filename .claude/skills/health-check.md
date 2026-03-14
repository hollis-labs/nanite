# Health Check

Check all Fragments Engine services and display a unified health status dashboard.

## When to use

- When the user asks about service health or status
- At the start of a session to verify service availability
- When debugging connectivity issues

## Procedure

1. **Check each service** by running via Bash:
   ```bash
   START=$(python3 -c "import time; print(int(time.time()*1000))")
   STATUS=$(curl -sf -o /dev/null -w "%{http_code}" <url> --max-time 3 2>/dev/null || echo "000")
   END=$(python3 -c "import time; print(int(time.time()*1000))")
   LATENCY=$((END - START))
   ```

   Services to check:

   | Service | URL | Health Endpoint |
   |---------|-----|----------------|
   | Hadron Daemon | http://127.0.0.1:${HADRON_PORT:-8095} | `GET /` |
   | Volon GUI | http://127.0.0.1:${VOLON_GUI_PORT:-8085} | `GET /v1/tasks` |
   | Cortex | http://127.0.0.1:${CORTEX_PORT:-8080} | `GET /v1/health/readiness` |

2. **Format output** as a status table:
   ```
   === TIAMAT SERVICES ===
   | Service        | Status  | Latency |
   |----------------|---------|---------|
   | Hadron Daemon  | UP/DOWN | Nms     |
   | Volon GUI      | UP/DOWN | Nms     |
   | Cortex         | UP/DOWN | Nms     |
   ========================
   ```

3. **If `--detailed` requested**, also check:
   - MCP server process status (volon, hadron, cortex)
   - Port conflicts on expected ports

## Output

Console output only. No file writes. Read-only operation.

## Invariants

- Never modify service state
- Timeout after 3s per service
- Always show all services even if checks fail
- Exit cleanly even if all services are down
