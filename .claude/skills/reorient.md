# Reorient — Context Orientation Skill

Automated context orientation for session continuity. Gathers current state from all sources, formats a continuity report, and identifies drift. Designed to combat context loss between sessions.

## When to use

- At the start of every new session (especially after boot)
- When the user asks "where were we?" or "what's the current status?"
- When you suspect context has drifted or gone stale
- After extended periods without a session

## Arguments

- `$ARGUMENTS` — Optional. A project_id to scope the orientation (default: read from agentrc.yaml or use "mentat")

## IMPORTANT: Run via Sub-Agents

This skill launches **parallel sub-agents** to gather context without polluting the main conversation. Each agent queries one domain, digests raw data, and returns only the summary. The caller then synthesizes and presents to the user for review.

## Procedure

### Step 1: Determine scope

Read `agentrc.yaml` for project_id if not provided as argument. If no agentrc.yaml, default to the current directory name.

### Step 2: Launch parallel sub-agents

Launch **4 sub-agents** simultaneously using the Agent tool. Each runs independently and returns a compact summary.

**Agent 1 — Volon State** (subagent_type: general-purpose)
```
You are a status reporter. Query Volon MCP tools for project_id="<PROJECT_ID>" and return a compact summary.

1. volon_epics_list — list all epics, note open vs closed
2. volon_sprints_list — list sprints, identify active ones
3. For each active sprint: volon_tasks_list with sprint_id to get task counts by status
4. volon_tasks_list with status="doing" — any in-progress work
5. volon_tasks_list with status="blocked" — any blockers
6. volon_tasks_list with status="todo", limit=10 — top priority backlog

Return this format:
VOLON STATE (<project_id>):
  Epics: <open>/<total> open
    - <epic_id>: <title> [<status>]
  Active Sprints:
    - <sprint-code>: <todo>t/<doing>d/<done>✓ of <total>
  In Progress: <list or "None">
  Blocked: <list or "None">
  Top Priority Todo:
    - [<priority>] <task-id>: <title>
  Total: <done>/<total> tasks complete
```

**Agent 2 — Recent Activity** (subagent_type: general-purpose)
```
You are a git/activity reporter. Check recent activity for the project at <PROJECT_DIR>.

1. Run: git log --oneline --since="3 days ago" --format="%h %s (%cr)" in <PROJECT_DIR>
2. If config/repos.yaml exists, read it and check git log for each managed project path too
3. Check for any new or modified files in docs/, adr/, .agentrc/ in the last 3 days:
   Run: git log --since="3 days ago" --name-only --format="" -- docs/ adr/ .agentrc/ | sort -u
4. Use mcp__cortex__context_view to browse the app/<project_id> namespace for recent session records

Return this format:
RECENT ACTIVITY (last 3 days):
  Commits:
    - <hash> <message> (<time ago>)
  Docs Changed:
    - <file path>
  Cortex Sessions:
    - <key>: <one-line summary>
  Cross-Project Activity:
    - <project>: <N> commits
```

**Agent 3 — System Health** (subagent_type: general-purpose, model: haiku)
```
You are a health checker. Check service status and agentrc state.

1. Service health:
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8085/v1/tasks --max-time 2 (Volon)
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/v1/health/readiness --max-time 2 (Cortex)
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8095/v1/health --max-time 2 (Hadron)
   - curl -sf -o /dev/null -w "%{http_code}" http://127.0.0.1:8090/api/health --max-time 2 (Mentat)

2. Check for stale worktrees:
   - For each directory in ~/Projects-apps/ that is a git repo, run: git -C <path> worktree list | wc -l
   - Report any with more than 1 worktree

3. Read .agentrc/bootstrap.md for current iteration state

Return this format:
SYSTEM:
  Services: Volon [UP/DOWN] | Cortex [UP/DOWN] | Hadron [UP/DOWN] | Mentat [UP/DOWN]
  Bootstrap: Iteration <N>, last updated <date>
  Worktrees: <clean or list of projects with stale worktrees>
```

**Agent 4 — Context & Memory** (subagent_type: general-purpose, model: haiku)
```
You are a context auditor. Check the state of persistent context stores.

1. Read the Claude Code memory index at the MEMORY.md path shown in the conversation context
   - List the sections and note any that look stale (references to old dates, completed work still listed as active)

2. Check .agentrc/pcc/global/ — list all PCC files and their last-modified dates:
   Run: ls -la .agentrc/pcc/global/ 2>/dev/null

3. Check if any active epics/sprints referenced in memory match what's actually in Volon:
   - Use volon_epics_list with project_id="<PROJECT_ID>"

Return this format:
CONTEXT STATE:
  Memory Index: <count> sections, <any staleness notes>
  PCC Files: <count> files, last updated <date>
  Drift Detected:
    - <description of any mismatches or "None detected">
```

### Step 3: Synthesize

Once all 4 agents return, synthesize into a single orientation report:

```
=== REORIENT — <PROJECT_ID> ===
Date: <today>
Profile: <current boot profile>
Iteration: <from bootstrap>

SERVICES: <health line>

STATE SUMMARY:
  <2-3 lines from bootstrap + volon state>

EPICS:
  <epic list with status>

ACTIVE WORK:
  <sprints with task counts>
  In Progress: <list or "None">
  Blocked: <list or "None">

RECENT ACTIVITY (3 days):
  <commits, doc changes, cortex sessions>

DRIFT CHECK:
  <any mismatches between memory/PCC and actual state>

NEXT STEPS:
  1. <highest priority action>
  2. <second priority>
  3. <third priority>

=== END REORIENT ===
```

### Step 4: Review with user

Present the report. Ask if anything needs updating. If drift was detected, offer to fix it (update memory, refresh PCC, correct stale references).

## Invariants

- ALWAYS run via sub-agents — raw API data stays out of main context
- Read-only — no state changes during orientation (changes only happen in Step 4 with user approval)
- Parallel execution — all 4 agents run simultaneously for speed
- Graceful degradation — if a service is down or a source is unavailable, note it and continue
- Works in both CLI (Claude Code) and GUI (Mentat app) contexts
