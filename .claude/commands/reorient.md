Run the reorient skill for context orientation. Determine the project_id from agentrc.yaml (or default to "mentat"). Launch 4 parallel sub-agents:

Agent 1 (Volon State): Query volon_epics_list, volon_sprints_list, volon_tasks_list (by sprint for active sprints, plus doing/blocked/todo) for project_id="$ARGUMENTS" (or default). Return compact summary: epics, active sprints with task counts, in-progress work, blockers, top priority todo, total done/total.

Agent 2 (Recent Activity): Run git log --oneline --since="3 days ago" in the current project directory. Check config/repos.yaml for managed projects and their git activity. Run git log --since="3 days ago" --name-only -- docs/ adr/ .agentrc/ for doc changes. Use cortex_context_view to browse app/<project_id> namespace for session records. Return: commits, doc changes, cortex sessions, cross-project activity.

Agent 3 (System Health, model: haiku): curl health endpoints for Volon (:8085/v1/tasks), Cortex (:8080/v1/health/readiness), Hadron (:8095/v1/health), Mentat (:8090/api/health). Check for stale git worktrees across ~/Projects-apps/. Read .agentrc/bootstrap.md. Return: service status, bootstrap iteration, worktree state.

Agent 4 (Context & Memory, model: haiku): Read the memory index MEMORY.md, check .agentrc/pcc/global/ file dates, compare active epics in memory vs volon_epics_list. Return: memory section count, PCC state, any drift detected.

Once all agents return, synthesize into a single report with: services, state summary, epics, active work, recent activity, drift check, and next steps. Present to user for review. If drift detected, offer to fix (update memory, refresh PCC, correct stale references).
