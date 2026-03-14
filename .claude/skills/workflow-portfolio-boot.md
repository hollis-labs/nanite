# workflow-portfolio-boot

Single command to start all services, verify health, and orient the agent to the current portfolio state.

## Usage
`/workflow-portfolio-boot [--skip-services]`

**--skip-services**: Skip service startup, just do orientation

## Instructions

Execute these phases in order:

### Phase 1: Start Services
Unless `--skip-services` is set:

1. Use `cerberus_start` to start all configured services (or verify they're running with `cerberus_status`)
2. Expected services: Volon, Hadron, Cortex, Cerberus itself
3. Wait for health checks to pass on each service:
   - `cerberus_health` for Cerberus
   - `hadron_health` for Hadron
   - `volon_health` for Volon
4. If any service fails to start, report it but continue with the rest

### Phase 2: Verify Health
1. Run `/health-check` or equivalent:
   - Check each service endpoint
   - Verify MCP connectivity (can you call each server's tools?)
   - Check Cortex DB accessibility
   - Check Volon Postgres connectivity
2. Report any issues found

### Phase 3: Orient
1. Read `.agentrc/bootstrap.md` for current iteration state
2. Query Volon for active tasks across all projects:
   - `volon_tasks_list status="doing"` — what's in progress?
   - `volon_tasks_list status="blocked"` — what's stuck?
3. Check git status across managed projects (use `/git-status-all` or scan `config/repos.yaml`)
4. Check inbox for unread messages (`/check-inbox`)
5. Check for recent ADRs or decisions that need attention

### Phase 4: Summary

```
=== PORTFOLIO BOOT ===
Date: <date>
Iteration: <N from bootstrap>

SERVICES:
  Volon:    <running|down> (port <port>)
  Hadron:   <running|down> (port <port>)
  Cortex:   <running|down> (port <port>)
  Cerberus: <running|down> (port <port>)

HEALTH: <all healthy | N issues>
  <any issues listed>

ACTIVE WORK:
  Tasks doing: <N> across <N> projects
  Tasks blocked: <N>
  <top 3 active tasks listed>

GIT STATUS:
  <N> projects with uncommitted changes
  <list if any>

INBOX: <N> unread messages

ORIENTATION:
  Current focus: <from bootstrap>
  Next steps: <from bootstrap>

Ready to work.
=== END BOOT ===
```

## When to Use
- At the start of every new session
- After a system restart or crash
- When you need to re-orient after being away
