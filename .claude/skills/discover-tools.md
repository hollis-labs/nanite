# discover-tools

Query all connected MCP servers, list their tools, and categorize them by capability domain.

## Usage
`/discover-tools [server]`

**server** (optional): Filter to a specific MCP server name (e.g., "hadron", "volon", "cortex", "cerberus")

## Instructions

1. Query each MCP server for its available tools. The known servers are:
   - **Volon** — task/sprint/project management, backlog, comments
   - **Hadron** — blueprints, automation, pipelines, schedules, runs
   - **Cortex** — context memory, namespaces, search, typed views
   - **Cerberus** — process/service management (start, stop, status, logs)

2. If a `server` argument is provided, filter to only that server.

3. For each server, list all tools grouped by capability domain:

   | Domain | Tool Pattern Examples |
   |--------|---------------------|
   | Task Management | `volon_task_*`, `volon_tasks_list`, `volon_backlog_*` |
   | Sprint Planning | `volon_sprint_*`, `volon_sprints_list` |
   | Project Management | `volon_project_*`, `volon_projects_list` |
   | Comments & Collaboration | `volon_comment_*`, `volon_comments_list` |
   | Blueprint Execution | `hadron_bp_*`, `hadron_blueprint_*`, `hadron_blueprints_list` |
   | Automation Runs | `hadron_run_*`, `hadron_runs_list` |
   | Pipelines & Scheduling | `hadron_pipeline_*`, `hadron_schedule_*` |
   | Context Read | `context_view`, `context_search`, `context_head`, `context_history` |
   | Context Write | `context_write`, `context_typed_write`, `context_embed` |
   | Context Namespaces | `context_namespace_*`, `context_namespaces_list` |
   | Context Promotion | `context_promote_*` |
   | Service Management | `cerberus_start`, `cerberus_stop`, `cerberus_restart` |
   | Service Monitoring | `cerberus_status`, `cerberus_health`, `cerberus_logs` |

4. For Hadron blueprints specifically, sub-categorize:
   - **Build**: `hadron_bp_build_*`
   - **Test/Lint**: `hadron_bp_test_*`, `hadron_bp_lint_*`
   - **Audit/Compliance**: `hadron_bp_*audit*`, `hadron_bp_otel_compliance_*`, `hadron_bp_api_contract_*`
   - **Backup/Snapshot**: `hadron_bp_backup_*`, `hadron_bp_time_machine_*`
   - **Release**: `hadron_bp_tag_release`, `hadron_bp_release_notes_*`
   - **Docker**: `hadron_bp_docker_*`
   - **Reports**: `hadron_bp_*report*`, `hadron_bp_*generator*`, `hadron_bp_*dashboard*`
   - **Game (SUDS)**: `hadron_bp_suds_*`
   - **Other**: everything else

5. Display a summary count per server and domain.

## Display Format

```
=== TOOL DISCOVERY ===
4 MCP servers connected | <total> tools available

VOLON (task orchestration) — <N> tools
  Task Management: volon_task_create, volon_task_get, volon_task_update, ...
  Sprint Planning: volon_sprint_create, volon_sprint_get, ...
  ...

HADRON (automation engine) — <N> tools
  Core: hadron_health, hadron_blueprints_list, hadron_run_enqueue, ...
  Blueprints (<N> total):
    Build (5): hadron_bp_build_carrier, hadron_bp_build_cortex_backend, ...
    Test/Lint (2): hadron_bp_test_go_project, hadron_bp_lint_go_project
    ...

CORTEX (context memory) — <N> tools
  ...

CERBERUS (service manager) — <N> tools
  ...

=== END DISCOVERY ===
```

## When to Use
- When you need to find which MCP tool does what
- When onboarding to the Fragments Engine portfolio
- When checking if a capability exists before building something new
- When documenting available automation for a project
